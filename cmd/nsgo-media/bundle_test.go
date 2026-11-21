package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeArchive(t *testing.T, dir, target string, files map[string]string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, archiveName(target)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPackageTarget(t *testing.T) {
	for dir, want := range map[string]string{
		"dist/My App-1.0.0-windows_x64": "windows_x64",
		"dist/My App-1.0.0-macos_arm64": "macos_arm64",
		"dist/releases":                 "",
		"dist/My App-1.0.0-ios":         "",
	} {
		if got := packageTarget(dir); got != want {
			t.Errorf("packageTarget(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestBundlePutsLibrariesBesideTheExecutable(t *testing.T) {
	libs, dist := t.TempDir(), t.TempDir()
	writeArchive(t, libs, "windows_x64", map[string]string{
		"avutil-61.dll": "avutil", "avcodec-63.dll": "avcodec",
		"COPYING.LGPLv2.1": "license", "BUILD.txt": "note",
	})
	pkg := filepath.Join(dist, "My App-1.0.0-windows_x64")
	os.MkdirAll(pkg, 0o755)
	os.WriteFile(filepath.Join(pkg, "My App.exe"), nil, 0o755)
	os.MkdirAll(filepath.Join(dist, "releases"), 0o755)

	src := &source{dir: libs}
	for _, p := range []string{pkg, filepath.Join(dist, "releases")} {
		if err := bundle(p, "", "", src); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]string{
		"avutil-61.dll": "avutil", "avcodec-63.dll": "avcodec",
		"FFmpeg/COPYING.LGPLv2.1": "license", "FFmpeg/BUILD.txt": "note",
	} {
		got, err := os.ReadFile(filepath.Join(pkg, name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v", name, got, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dist, "releases")); len(entries) != 0 {
		t.Error("bundled into the releases directory")
	}
}

func TestBundleWithoutPinnedRelease(t *testing.T) {
	if releaseTag != "" {
		t.Skip("a release is pinned")
	}
	pkg := filepath.Join(t.TempDir(), "App-1.0.0-linux_x64")
	os.MkdirAll(pkg, 0o755)
	if err := bundle(pkg, "", "", &source{}); err == nil {
		t.Fatal("bundled without libraries")
	}
}
