package app

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

// load loads every plugin, refusing one whose id another plugin has. It
// returns the plugins that loaded and why the others did not.
func (l *library) load() ([]*plugin.Plugin, error) {
	var errs []error
	var plugins []*plugin.Plugin
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
			p, err := plugin.Load(os.DirFS(filepath.Join(l.dir, e.Name())), false, l.opts)
			if add(p, err, e.Name()) {
				l.remember(p, e.Name())
			}
		}
	}
	for _, dir := range l.settings.Linked() {
		p, err := plugin.Load(os.DirFS(dir), false, l.opts)
		if add(p, err, dir) {
			l.link(p, dir)
		}
	}
	return plugins, errors.Join(errs...)
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
		if err := l.settings.SetLinked(c.dir, true); err != nil {
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
	if err != nil {
		os.RemoveAll(filepath.Join(l.dir, id))
		return nil, err
	}
	l.remember(p, id)
	return p, nil
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
	return store.RemovePlugin(l.dir, folder, p.DataDir())
}
