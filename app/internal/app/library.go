package app

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/internal/store"
)

// library loads, installs and removes plugins: the built-in ones from the
// binary, the installed ones from the plugins folder, each in a folder of
// its own, and the linked ones from the folders the settings list, used
// where they are. Its methods may be called from any goroutine.
type library struct {
	builtin []Builtin
	// dir is the plugins folder, "" for none.
	dir      string
	settings *store.Settings
	opts     plugin.Options

	mu sync.Mutex
	// folders are the installed plugins' folders in dir, and linked the
	// linked plugins' folders.
	folders map[*plugin.Plugin]string
	linked  map[*plugin.Plugin]string
}

// refusal is an installed or linked plugin the app refused to load, as
// far as its manifest reads, which stays until the viewer removes it.
type refusal struct {
	id, name, version string
	// folder is an installed one's folder in the plugins folder, and dir
	// a linked one's folder.
	folder, dir string
	err         error
}

// readHead reads what it can of the manifest in fsys: its id, name and
// version.
func readHead(fsys fs.FS) (id, name, version string) {
	data, err := fs.ReadFile(fsys, plugin.ManifestFile)
	if err != nil {
		return "", "", ""
	}
	var head struct{ ID, Name, Version string }
	yaml.Unmarshal(data, &head)
	return head.ID, head.Name, head.Version
}

// load loads every plugin, refusing one whose id another plugin has. It
// returns the plugins that loaded, those installed or linked that did
// not, and why.
func (l *library) load() ([]*plugin.Plugin, []*refusal, error) {
	var errs []error
	var plugins []*plugin.Plugin
	var refused []*refusal
	ids := map[string]bool{}
	add := func(p *plugin.Plugin, err error, name string) bool {
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		case ids[p.Manifest.ID]:
			errs = append(errs, fmt.Errorf("%s: plugin %s is already loaded", name, p.Manifest.ID))
			p.Close()
		default:
			ids[p.Manifest.ID] = true
			plugins = append(plugins, p)
			return true
		}
		return false
	}
	for _, load := range l.builtin {
		p, err := load(l.opts)
		add(p, err, "built-in plugin")
	}
	if l.dir != "" {
		entries, err := os.ReadDir(l.dir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			fsys := os.DirFS(filepath.Join(l.dir, e.Name()))
			p, err := plugin.Load(fsys, false, l.opts)
			if add(p, err, e.Name()) {
				l.remember(p, e.Name())
			} else if err != nil {
				r := &refusal{folder: e.Name(), err: err}
				r.id, r.name, r.version = readHead(fsys)
				refused = append(refused, r)
			}
		}
	}
	for _, dir := range l.settings.Linked() {
		fsys := os.DirFS(dir)
		p, err := plugin.Load(fsys, false, l.opts)
		if add(p, err, dir) {
			l.link(p, dir)
		} else if err != nil {
			r := &refusal{dir: dir, err: err}
			r.id, r.name, r.version = readHead(fsys)
			refused = append(refused, r)
		}
	}
	return plugins, refused, errors.Join(errs...)
}

// removeRefused removes a refused plugin: an installed one's folder, a
// linked one's link, and its data unless keepData, as when a plugin
// loaded has its id.
func (l *library) removeRefused(r *refusal, keepData bool) error {
	var data string
	if r.id != "" && l.opts.DataDir != "" && !keepData && filepath.Base(r.id) == r.id {
		data = filepath.Join(l.opts.DataDir, r.id)
	}
	if data != "" {
		// The release goes with the data, which a plugin loaded of its
		// id keeps.
		if err := l.settings.SetRelease(r.id, nil); err != nil {
			return err
		}
	}
	if r.dir != "" {
		err := l.settings.SetLinked(r.dir, false)
		if data != "" {
			err = errors.Join(err, os.RemoveAll(data))
		}
		return err
	}
	if l.dir == "" {
		return errors.New("the app has no plugins folder")
	}
	return store.RemovePlugin(l.dir, r.folder, data)
}

func (l *library) remember(p *plugin.Plugin, folder string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.folders == nil {
		l.folders = map[*plugin.Plugin]string{}
	}
	l.folders[p] = folder
}

// Builtin loads a plugin embedded in the app.
type Builtin func(plugin.Options) (*plugin.Plugin, error)

// BuiltinFS is the built-in plugin of a module in fsys.
func BuiltinFS(fsys fs.FS) Builtin {
	return func(opts plugin.Options) (*plugin.Plugin, error) { return plugin.Load(fsys, true, opts) }
}

func (l *library) link(p *plugin.Plugin, dir string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.linked == nil {
		l.linked = map[*plugin.Plugin]string{}
	}
	l.linked[p] = dir
}

// linkedDir is the folder of a linked plugin, and whether p is one.
func (l *library) linkedDir(p *plugin.Plugin) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir, ok := l.linked[p]
	return dir, ok
}

// candidate is a plugin the viewer picked to install, read but not yet
// run.
type candidate struct {
	fsys     fs.FS
	manifest *plugin.Manifest
	// dir, when set, is the folder the plugin is used in, linked rather
	// than installed.
	dir string
	// from is where the viewer picked it, a folder, a zip or a
	// repository, if known.
	from string
	// release is the GitHub release it comes from, if it does.
	release *release
}

// inspectRelease downloads the plugin zip of r and reads it as inspect
// does.
func (l *library) inspectRelease(ctx context.Context, g *gitHub, r *release, taken func(id string) bool) (*candidate, error) {
	src, err := g.download(ctx, r)
	if err != nil {
		return nil, err
	}
	c, err := l.inspect(src, taken)
	if err != nil {
		return nil, err
	}
	c.from, c.release = r.Repo, r
	return c, nil
}

// readZip reads the zip file at path whole, as a plugin is small.
func readZip(path string) (fs.FS, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > store.MaxPluginSize {
		return nil, fmt.Errorf("the plugin is larger than %d MB", store.MaxPluginSize>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return zip.NewReader(bytes.NewReader(data), int64(len(data)))
}

// inspect reads the plugin in src, at its root or in its only folder, as
// a zip often holds, and checks that it would load without running any
// of its code. taken reports the ids of the plugins loaded.
func (l *library) inspect(src fs.FS, taken func(id string) bool) (*candidate, error) {
	root, err := pluginRoot(src)
	if err != nil {
		return nil, err
	}
	opts := l.opts
	opts.DataDir = ""
	p, err := plugin.Load(root, false, opts)
	if err != nil {
		return nil, err
	}
	p.Close()
	if taken(p.Manifest.ID) {
		return nil, fmt.Errorf("plugin %s: %w", p.Manifest.ID, store.ErrInstalled)
	}
	return &candidate{fsys: root, manifest: p.Manifest}, nil
}

// inspectDir reads the plugin in the folder of path, a path or a file URL,
// to be used where it is.
func (l *library) inspectDir(path string, taken func(id string) bool) (*candidate, error) {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if p, ok := plugin.FilePath(path); ok {
		path = p
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%q is not a full path to a folder", path)
	}
	path = filepath.Clean(path)
	if info, err := os.Stat(path); err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", path)
	}
	if _, err := os.Stat(filepath.Join(path, plugin.ManifestFile)); err != nil {
		return nil, fmt.Errorf("no %s in %s", plugin.ManifestFile, path)
	}
	c, err := l.inspect(os.DirFS(path), taken)
	if err != nil {
		return nil, err
	}
	c.dir = path
	return c, nil
}

func pluginRoot(src fs.FS) (fs.FS, error) {
	if _, err := fs.Stat(src, plugin.ManifestFile); err == nil {
		return src, nil
	}
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "__MACOSX" {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 {
		if _, err := fs.Stat(src, dirs[0]+"/"+plugin.ManifestFile); err == nil {
			return fs.Sub(src, dirs[0])
		}
	}
	return nil, errors.New("no " + plugin.ManifestFile + " in it")
}

// install copies the candidate into the plugins folder, named by its id,
// and loads it from there; a linked one loads from its own folder, which
// the settings keep.
func (l *library) install(c *candidate) (*plugin.Plugin, error) {
	if c.dir != "" {
		p, err := plugin.Load(os.DirFS(c.dir), false, l.opts)
		if err != nil {
			return nil, err
		}
		err = errors.Join(l.settings.SetLinked(c.dir, true), l.settings.SetRelease(p.Manifest.ID, nil))
		if err != nil {
			p.Close()
			return nil, err
		}
		l.link(p, c.dir)
		return p, nil
	}
	if l.dir == "" {
		return nil, errors.New("the app has no folder to install plugins in")
	}
	id := c.manifest.ID
	if err := store.InstallPlugin(l.dir, id, c.fsys); err != nil {
		return nil, fmt.Errorf("plugin %s: %w", id, err)
	}
	p, err := plugin.Load(os.DirFS(filepath.Join(l.dir, id)), false, l.opts)
	if err == nil {
		err = l.settings.SetRelease(id, c.releaseKept())
	}
	if err != nil {
		if p != nil {
			p.Close()
		}
		os.RemoveAll(filepath.Join(l.dir, id))
		return nil, err
	}
	l.remember(p, id)
	return p, nil
}

// releaseKept is the release the settings keep for c, nil for one not
// from GitHub.
func (c *candidate) releaseKept() *store.Release {
	if c.release == nil {
		return nil
	}
	return &c.release.Release
}

// releaseOf is the GitHub release an installed plugin came from, if it
// did; a linked one never did.
func (l *library) releaseOf(p *plugin.Plugin) (store.Release, bool) {
	l.mu.Lock()
	_, ok := l.folders[p]
	l.mu.Unlock()
	if !ok {
		return store.Release{}, false
	}
	return l.settings.Release(p.Manifest.ID)
}

// update replaces the installed plugin old with c, a newer release of it,
// keeping its data. It answers the plugin loaded in old's place, which a
// failed update loads again from the old version.
func (l *library) update(old *plugin.Plugin, c *candidate) (*plugin.Plugin, error) {
	l.mu.Lock()
	folder, ok := l.folders[old]
	l.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("plugin %s is not installed", old.Manifest.ID)
	}
	if c.manifest.ID != old.Manifest.ID {
		return nil, fmt.Errorf("the release holds plugin %s, not %s", c.manifest.ID, old.Manifest.ID)
	}
	// Its runtime stops before the new one reads the data it wrote.
	old.Close()
	var p *plugin.Plugin
	err := store.ReplacePlugin(l.dir, folder, c.fsys, func(path string) error {
		var err error
		p, err = plugin.Load(os.DirFS(path), false, l.opts)
		return err
	})
	if err == nil {
		if serr := l.settings.SetRelease(old.Manifest.ID, c.releaseKept()); serr != nil {
			log.Printf("settings: %v", serr)
		}
	} else {
		p = nil
		back, lerr := plugin.Load(os.DirFS(filepath.Join(l.dir, folder)), false, l.opts)
		if lerr != nil {
			l.forget(old)
			return nil, errors.Join(err, lerr)
		}
		p = back
	}
	l.forget(old)
	l.remember(p, folder)
	return p, err
}

func (l *library) forget(p *plugin.Plugin) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.folders, p)
}

// remove closes an installed plugin and removes its folder and its data;
// a linked one's folder stays, unlinked.
func (l *library) remove(p *plugin.Plugin) error {
	l.mu.Lock()
	folder, ok := l.folders[p]
	delete(l.folders, p)
	dir, linked := l.linked[p]
	delete(l.linked, p)
	l.mu.Unlock()
	if linked {
		p.Close()
		err := l.settings.SetLinked(dir, false)
		if data := p.DataDir(); data != "" {
			err = errors.Join(err, os.RemoveAll(data))
		}
		return err
	}
	if !ok {
		return fmt.Errorf("plugin %s is not installed", p.Manifest.ID)
	}
	p.Close()
	return errors.Join(store.RemovePlugin(l.dir, folder, p.DataDir()), l.settings.SetRelease(p.Manifest.ID, nil))
}
