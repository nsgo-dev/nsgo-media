package app

import (
	"archive/zip"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/oneo-me/nsgo-media/app/internal/plugin"
	"github.com/oneo-me/nsgo-media/app/internal/store"
)

func testLibrary(t *testing.T, builtin ...fs.FS) *library {
	root := t.TempDir()
	var loads []Builtin
	for _, fsys := range builtin {
		loads = append(loads, BuiltinFS(fsys))
	}
	settings, _ := store.OpenSettings("")
	return &library{
		builtin:  loads,
		settings: settings,
		dir:      filepath.Join(root, "plugins"),
		opts:     plugin.Options{AppVersion: Version, Language: "en", DataDir: filepath.Join(root, "plugin-data"), Logger: slog.New(slog.DiscardHandler)},
	}
}

func manifest(id, minApp string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`{"id":"` + id + `","name":"P","version":"1.0.0","minAppVersion":"` + minApp + `","main":"main.ts","media":"video"}`)}
}

var homeSource = &fstest.MapFile{Data: []byte(`export function home(_: unknown): { sections: never[] } { return { sections: [] } }`)}

func TestLibraryLoadsBuiltInAndInstalled(t *testing.T) {
	l := testLibrary(t, fstest.MapFS{"plugin.yaml": manifest("a", "0.1.0"), "main.ts": homeSource})
	for name, files := range map[string]fstest.MapFS{
		"b":     {"plugin.yaml": manifest("b", "0.1.0"), "main.ts": homeSource},
		"new":   {"plugin.yaml": manifest("new", "9.0.0"), "main.ts": homeSource},
		"split": {"plugin.yaml": manifest("split", "0.1.0"), "main.ts": {Data: []byte(`import x from "./x.js"; export const home = x`)}},
		"again": {"plugin.yaml": manifest("a", "0.1.0"), "main.ts": homeSource},
	} {
		if err := store.InstallPlugin(l.dir, name, files); err != nil {
			t.Fatal(err)
		}
	}
	plugins, refused, err := l.load()
	if len(plugins) != 2 || plugins[0].Manifest.ID != "a" || !plugins[0].Builtin || plugins[1].Manifest.ID != "b" || plugins[1].Builtin {
		t.Fatalf("loaded %v", plugins)
	}
	var tooNew *plugin.TooNewError
	if !errors.As(err, &tooNew) || !strings.Contains(err.Error(), "split: plugin split: main.ts imports ./x.js") || !strings.Contains(err.Error(), "again: plugin a is already loaded") {
		t.Fatalf("errors = %v", err)
	}
	// The plugins refused for themselves stay listed until removed; the
	// one of a taken id is only logged.
	var names []string
	for _, r := range refused {
		names = append(names, r.folder+"="+r.id)
	}
	slices.Sort(names)
	if strings.Join(names, " ") != "new=new split=split" {
		t.Fatalf("refused %v", names)
	}
	for _, r := range refused {
		if err := l.removeRefused(r, false); err != nil {
			t.Fatal(err)
		}
		if exists(filepath.Join(l.dir, r.folder)) {
			t.Fatalf("removing refused %s kept its folder", r.folder)
		}
	}
	for _, p := range plugins {
		p.Close()
	}
}

// zipOf writes files to a zip file and returns its path.
func zipOf(t *testing.T, files fstest.MapFS) string {
	path := filepath.Join(t.TempDir(), "plugin.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	if err := w.AddFS(files); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLibraryInstallsAndRemoves(t *testing.T) {
	l := testLibrary(t)
	// A zip holding the plugin's folder, as zipping a folder makes.
	src, err := readZip(zipOf(t, fstest.MapFS{"demo/plugin.yaml": manifest("demo", "0.1.0"), "demo/main.ts": homeSource, "__MACOSX/._demo": {}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.inspect(src, func(id string) bool { return id == "demo" }); !errors.Is(err, store.ErrInstalled) {
		t.Fatalf("inspecting a plugin with a taken id = %v", err)
	}
	c, err := l.inspect(src, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.opts.DataDir); !os.IsNotExist(err) {
		t.Fatal("inspecting a plugin wrote its data")
	}
	p, err := l.install(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Home(t.Context(), plugin.PageParams{}); err != nil {
		t.Fatal(err)
	}
	plugins, _, err := l.load()
	if err != nil || len(plugins) != 1 || plugins[0].Manifest.ID != "demo" {
		t.Fatalf("after installing, loaded %v, %v", plugins, err)
	}
	plugins[0].Close()

	if err := os.MkdirAll(p.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l.remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Home(t.Context(), plugin.PageParams{}); !errors.Is(err, plugin.ErrClosed) {
		t.Fatalf("a removed plugin answered: %v", err)
	}
	for _, path := range []string{filepath.Join(l.dir, "demo"), p.DataDir()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s is kept: %v", path, err)
		}
	}
	if _, err := l.inspect(fstest.MapFS{"a/plugin.yaml": manifest("a", "0.1.0"), "b/plugin.yaml": manifest("b", "0.1.0")}, func(string) bool { return false }); err == nil {
		t.Fatal("a folder of two plugins was accepted")
	}
}

func TestLibraryLinksAFolder(t *testing.T) {
	l := testLibrary(t)
	dir := t.TempDir()
	for name, f := range map[string]*fstest.MapFile{"plugin.yaml": manifest("linked", "0.1.0"), "main.ts": homeSource} {
		if err := os.WriteFile(filepath.Join(dir, name), f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	none := func(string) bool { return false }
	for _, bad := range []string{"relative/path", filepath.Join(dir, "main.ts"), t.TempDir()} {
		if _, err := l.inspectDir(bad, none); err == nil {
			t.Errorf("linking %s was accepted", bad)
		}
	}
	if _, err := l.inspectDir(dir, func(string) bool { return true }); !errors.Is(err, store.ErrInstalled) {
		t.Fatalf("linking a plugin of a taken id = %v", err)
	}
	c, err := l.inspectDir(plugin.FileURL(dir), none)
	if err != nil {
		t.Fatal(err)
	}
	p, err := l.install(c)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := l.linkedDir(p); !ok || got != dir {
		t.Fatalf("linked folder %q", got)
	}
	if entries, _ := os.ReadDir(l.dir); len(entries) != 0 {
		t.Fatalf("linking copied %v into the plugins folder", entries)
	}

	// It loads again from its folder, and unlinked, the folder stays.
	plugins, _, err := l.load()
	if err != nil || len(plugins) != 1 || plugins[0].Manifest.ID != "linked" {
		t.Fatalf("loaded %v, %v", plugins, err)
	}
	if err := l.remove(plugins[0]); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "plugin.yaml")) || len(l.settings.Linked()) != 0 {
		t.Fatalf("unlinking left %q linked or removed the folder", l.settings.Linked())
	}
	p.Close()
}
