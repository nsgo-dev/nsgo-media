package library

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestFileName(t *testing.T) {
	for _, c := range []struct{ goos, want string }{
		{"darwin", "libavutil.61.dylib"},
		{"windows", "avutil-61.dll"},
		{"linux", "libavutil.so.61"},
		{"ios", "libavutil.framework/libavutil"},
	} {
		if got := FileName(c.goos, "avutil", 61); got != c.want {
			t.Errorf("FileName(%s) = %s, want %s", c.goos, got, c.want)
		}
	}
}

func TestDirsOrder(t *testing.T) {
	exe := filepath.Join("/Applications", "Demo.app", "Contents", "MacOS", "Demo")
	got := dirs("darwin", "/env", exe)
	want := []string{"/env", filepath.Join("/Applications", "Demo.app", "Contents", "Frameworks"), filepath.Join("/Applications", "Demo.app", "Contents", "MacOS")}
	if !slices.Equal(got[:3], want) {
		t.Fatalf("dirs = %v, want it to start with %v", got, want)
	}
	if got[len(got)-1] != "" {
		t.Fatalf("dirs = %v, want the system's search last", got)
	}

	got = dirs("linux", "", filepath.Join("/opt", "demo", "demo"))
	if !slices.Equal(got, []string{filepath.Join("/opt", "demo"), ""}) {
		t.Fatalf("linux dirs = %v", got)
	}

	app := filepath.Join("/private", "var", "containers", "Bundle", "Application", "ID", "Demo.app")
	got = dirs("ios", "", filepath.Join(app, "Demo"))
	if !slices.Equal(got, []string{filepath.Join(app, "Frameworks")}) {
		t.Fatalf("ios dirs = %v", got)
	}
}
