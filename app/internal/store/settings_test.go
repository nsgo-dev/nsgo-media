package store

import (
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
	if s.Language() != "" || !s.Enabled("demo") {
		t.Fatal("new settings are not the defaults")
	}
	if err := s.SetLanguage("zh-Hans"); err != nil {
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
	if again.Language() != "zh-Hans" || again.Enabled("demo") || !again.Enabled("other") {
		t.Fatalf("reopened settings = %+v", again.v)
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
