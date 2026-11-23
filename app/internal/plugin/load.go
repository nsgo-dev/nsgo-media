package plugin

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/webapi"
	"github.com/nsgo-dev/nsgo/i18n"
	"go.yaml.in/yaml/v3"
)

// Options are what the app gives every plugin it loads.
type Options struct {
	// AppVersion is the app's version, which minAppVersion is checked
	// against.
	AppVersion string
	// Language is the viewer's language, a BCP 47 tag such as "zh-CN".
	Language string
	// DataDir holds each plugin's data folder, named by its id; "" keeps
	// plugin data in memory.
	DataDir string
	// UI shows a plugin's sign-in prompt and opens its pages in the
	// browser; nil refuses both.
	UI UI
	// Client sends the requests of fetch; nil is a client with a timeout.
	Client *http.Client
	// Logger receives what plugins log; nil is slog's default.
	Logger *slog.Logger
	Limits Limits
}

// Limits bound what a plugin's runtime may take; zero fields are the
// defaults.
type Limits struct {
	// Memory is what a runtime's JavaScript may keep and allocate.
	Memory int64
	// Busy is how long a runtime may run JavaScript without waiting, as
	// for fetch or a timer, before it is stopped.
	Busy time.Duration
	// Call is how long a call may take, and SignIn how long signIn may.
	Call, SignIn time.Duration
}

func (l Limits) withDefaults() Limits {
	if l.Memory == 0 {
		l.Memory = 128 << 20
	}
	if l.Busy == 0 {
		l.Busy = 5 * time.Second
	}
	if l.Call == 0 {
		l.Call = 30 * time.Second
	}
	if l.SignIn == 0 {
		l.SignIn = 5 * time.Minute
	}
	return l
}

// Plugin is a loaded plugin. Its runtime starts on the first call and
// starts again after a limit stopped it. Its methods may be called from
// any goroutine; calls block, so a page makes them off the engine
// goroutine.
type Plugin struct {
	Manifest *Manifest
	// Builtin is set for plugins embedded in the app, which can be
	// disabled but not removed.
	Builtin bool

	opts  Options
	allow func(*http.Request) error
	mod   *moejs.Module
	// native holds the exports of a plugin written in Go, nil for a
	// module's.
	native  Native
	exports map[string]bool
	locales map[string]map[string]string
	// catalog matches the viewer's language to one of locales'.
	catalog *i18n.Catalog
	data    *data

	mu     sync.Mutex
	rt     *runtime
	closed bool
}

// Load loads the plugin at the root of fsys: its manifest, its module and
// its locales. Built-in plugins come from an embedded FS and installed
// ones from a folder, through the same path.
func Load(fsys fs.FS, builtin bool, opts Options) (*Plugin, error) {
	m, err := readManifest(fsys, opts)
	if err != nil {
		return nil, err
	}
	if m.Main == "" {
		return nil, fmt.Errorf("plugin %s: %s names no main module", m.ID, ManifestFile)
	}
	src, err := fs.ReadFile(fsys, m.Main)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", m.ID, err)
	}
	compile := moejs.Compile
	if path.Ext(m.Main) == ".ts" {
		compile = moejs.CompileTS
	}
	mod, err := compile(m.Main, string(src))
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", m.ID, err)
	}
	if reqs := mod.Requests(); len(reqs) > 0 {
		return nil, fmt.Errorf("plugin %s: %s imports %s; a plugin is one module", m.ID, m.Main, strings.Join(reqs, ", "))
	}
	p, err := newPlugin(fsys, m, builtin, opts)
	if err != nil {
		return nil, err
	}
	p.mod = mod
	for _, name := range mod.Exports() {
		p.exports[name] = true
	}
	return p, nil
}

func readManifest(fsys fs.FS, opts Options) (*Manifest, error) {
	raw, err := fs.ReadFile(fsys, ManifestFile)
	if err != nil {
		return nil, err
	}
	return ParseManifest(raw, opts.AppVersion)
}

// newPlugin is a plugin of m without its exports: its locales, its data
// and its options.
func newPlugin(fsys fs.FS, m *Manifest, builtin bool, opts Options) (*Plugin, error) {
	locales, err := readLocales(fsys)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", m.ID, err)
	}
	dir := ""
	if opts.DataDir != "" {
		dir = filepath.Join(opts.DataDir, m.ID)
	}
	d, err := openData(dir)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", m.ID, err)
	}
	p := &Plugin{Manifest: m, Builtin: builtin, opts: opts, locales: locales, data: d, exports: map[string]bool{}}
	// "und", the source, stands for the texts as the plugin writes them.
	p.catalog = i18n.NewCatalog("und", slices.Sorted(maps.Keys(locales))...)
	p.opts.Limits = opts.Limits.withDefaults()
	p.allow = webapi.AllowHosts(m.Permissions.Network...)
	if p.opts.Logger == nil {
		p.opts.Logger = slog.Default()
	}
	p.opts.Logger = p.opts.Logger.With("plugin", m.ID)
	return p, nil
}

// readLocales reads locales/<language>.yaml, each a mapping from a text to
// its translation.
func readLocales(fsys fs.FS) (map[string]map[string]string, error) {
	entries, err := fs.ReadDir(fsys, "locales")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	locales := map[string]map[string]string{}
	for _, e := range entries {
		lang, ok := strings.CutSuffix(e.Name(), ".yaml")
		if e.IsDir() || !ok {
			continue
		}
		raw, err := fs.ReadFile(fsys, "locales/"+e.Name())
		if err != nil {
			return nil, err
		}
		var texts map[string]string
		if err := yaml.Unmarshal(raw, &texts); err != nil {
			return nil, fmt.Errorf("locales/%s: %w", e.Name(), err)
		}
		locales[lang] = texts
	}
	return locales, nil
}

// Name is the plugin's name in the viewer's language.
func (p *Plugin) Name() string { return p.Translate(p.Manifest.Name) }

// Has reports whether the plugin exports name.
func (p *Plugin) Has(export string) bool { return p.exports[export] }

// Translate is the plugin's translation of text into the viewer's
// language, or text itself: from the locale that suits the language best,
// as zh-CN suits zh-Hans.
func (p *Plugin) Translate(text string) string {
	p.mu.Lock()
	lang := p.opts.Language
	p.mu.Unlock()
	if t, ok := p.locales[p.catalog.Match(lang)][text]; ok {
		return t
	}
	return text
}

// SetLanguage changes the viewer's language, a BCP 47 tag, and stops the
// runtime without waiting for it, so the next call starts one in it.
func (p *Plugin) SetLanguage(lang string) {
	p.mu.Lock()
	var r *runtime
	if p.opts.Language != lang {
		p.opts.Language = lang
		r, p.rt = p.rt, nil
	}
	p.mu.Unlock()
	if r != nil {
		go r.stop()
	}
}

// Stop stops the plugin's runtime, failing the calls in flight; the next
// call starts a fresh one.
func (p *Plugin) Stop() { p.end(false) }

// Close stops the plugin's runtime; later calls fail.
func (p *Plugin) Close() { p.end(true) }

func (p *Plugin) end(closed bool) {
	p.mu.Lock()
	r := p.rt
	p.rt = nil
	p.closed = p.closed || closed
	p.mu.Unlock()
	if r != nil {
		r.stop()
	}
}

// DataDir is the plugin's data folder, "" when its data is kept in memory.
func (p *Plugin) DataDir() string { return p.data.dir }
