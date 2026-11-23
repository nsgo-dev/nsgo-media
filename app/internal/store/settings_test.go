package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSettingsOutliveReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Language() != "" || !s.Enabled("demo") || !s.Stats() {
		t.Fatal("new settings are not the defaults")
	}
	if err := s.SetLanguage("zh-Hans"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStats(false); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		id string
		on bool
	}{{"demo", false}, {"other", false}, {"other", true}} {
		if err := s.SetEnabled(step.id, step.on); err != nil {
			t.Fatal(err)
		}
	}

	for _, step := range []struct {
		dir string
		on  bool
	}{{"/a", true}, {"/b", true}, {"/a", true}, {"/b", false}} {
		if err := s.SetLinked(step.dir, step.on); err != nil {
			t.Fatal(err)
		}
	}

	again, err := OpenSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if linked := again.Linked(); len(linked) != 1 || linked[0] != "/a" {
		t.Fatalf("linked folders = %q", linked)
	}
	if again.Language() != "zh-Hans" || again.Enabled("demo") || !again.Enabled("other") || again.Stats() {
		t.Fatalf("reopened settings = %+v", again.v)
	}

	// The releases plugins came from, until forgotten.
	if err := again.SetRelease("demo", &Release{Repo: "o/demo", Tag: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := again.SetRelease("other", &Release{Repo: "o/other", Tag: "v2"}); err != nil {
		t.Fatal(err)
	}
	if err := again.SetRelease("other", nil); err != nil {
		t.Fatal(err)
	}
	third, _ := OpenSettings(path)
	if r, ok := third.Release("demo"); !ok || r.Repo != "o/demo" || r.Tag != "v1" {
		t.Fatalf("release = %+v, %v", r, ok)
	}
	if _, ok := third.Release("other"); ok {
		t.Fatal("a forgotten release is kept")
	}
}

func TestReplacePlugin(t *testing.T) {
	dir := t.TempDir()
	if err := InstallPlugin(dir, "demo", fstest.MapFS{"plugin.yaml": {Data: []byte("v1")}, "old.js": {Data: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		raw, _ := os.ReadFile(filepath.Join(dir, "demo", name))
		return string(raw)
	}
	// A failed load puts the old folder back.
	failed := errors.New("failed")
	if err := ReplacePlugin(dir, "demo", fstest.MapFS{"plugin.yaml": {Data: []byte("v2")}}, func(string) error { return failed }); !errors.Is(err, failed) {
		t.Fatalf("a failed load = %v", err)
	}
	if read("plugin.yaml") != "v1" || read("old.js") != "x" {
		t.Fatal("a failed replacement lost the old folder")
	}
	if err := ReplacePlugin(dir, "demo", fstest.MapFS{"plugin.yaml": {Data: []byte("v2")}}, func(path string) error {
		if raw, _ := os.ReadFile(filepath.Join(path, "plugin.yaml")); string(raw) != "v2" {
			t.Errorf("loading %s, which holds %s", path, raw)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if read("plugin.yaml") != "v2" || read("old.js") != "" {
		t.Fatal("the folder was not replaced")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("the plugins folder holds %v", entries)
	}
}

func TestInstallAndRemovePlugin(t *testing.T) {
	root := t.TempDir()
	dir, data := filepath.Join(root, "plugins"), filepath.Join(root, "plugin-data", "demo")
	src := fstest.MapFS{
		"plugin.yaml":        {Data: []byte("{}")},
		"locales/zh-CN.yaml": {Data: []byte("{}")},
		"link":               {Data: []byte("x"), Mode: os.ModeSymlink},
	}
	if err := InstallPlugin(dir, "demo", src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo", "locales", "zh-CN.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "demo", "link")); err == nil {
		t.Fatal("a symbolic link was copied")
	}
	if err := InstallPlugin(dir, "demo", src); err != ErrInstalled {
		t.Fatalf("installing again = %v", err)
	}

	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemovePlugin(dir, "demo", data); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "demo"), data} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s is kept: %v", path, err)
		}
	}
}

func TestInstallRefusesALargePlugin(t *testing.T) {
	dir := t.TempDir()
	big := fstest.MapFS{"a.js": {Data: make([]byte, MaxPluginSize/2+1)}, "b.js": {Data: make([]byte, MaxPluginSize/2+1)}}
	if err := InstallPlugin(dir, "big", big); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("installing a large plugin = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a failed install left %v", entries)
	}
}
