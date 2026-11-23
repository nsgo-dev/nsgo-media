package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
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
		"dist/My App-1.0.0-windows_x64":   "",
		"dist/My App-1.0.0-macos_arm64":   "",
		"dist/releases":                   "",
		"dist/My App-1.0.0-ios":           "ios",
		"dist/My App-1.0.0-ios_simulator": "ios_simulator",
		"dist/My App-1.0.0-tvos":          "",
	} {
		if got := packageTarget(dir); got != want {
			t.Errorf("packageTarget(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestBundleSkipsDesktopPackages(t *testing.T) {
	dist := t.TempDir()
	pkg := filepath.Join(dist, "My App-1.0.0-windows_x64")
	os.MkdirAll(pkg, 0o755)
	os.WriteFile(filepath.Join(pkg, "My App.exe"), nil, 0o755)
	if err := bundle(pkg, "", "", &source{dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(pkg); len(entries) != 1 {
		t.Errorf("bundled into a desktop package: %v", entries)
	}
	if err := bundle(pkg, "windows_x64", "", &source{dir: t.TempDir()}); err == nil {
		t.Error("bundle took a desktop target")
	}
}

func iosPackage(t *testing.T) string {
	t.Helper()
	pkg := filepath.Join(t.TempDir(), "App-1.0.0-ios")
	os.MkdirAll(filepath.Join(pkg, "App.app"), 0o755)
	return pkg
}

func TestBundleWithoutLibraries(t *testing.T) {
	err := bundle(iosPackage(t), "", "", &source{dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "go run ./tools/ffmpeg") {
		t.Fatalf("bundle without libraries: %v", err)
	}
}

func TestBundleWithoutLicense(t *testing.T) {
	libs := t.TempDir()
	writeArchive(t, libs, "ios", map[string]string{"libavutil.framework/libavutil": "avutil", "BUILD.txt": "note"})
	pkg := iosPackage(t)
	err := bundle(pkg, "", "", &source{dir: libs})
	if err == nil || !strings.Contains(err.Error(), "COPYING.LGPLv2.1") {
		t.Fatalf("bundle without the license: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pkg, "App.app", "Frameworks")); err == nil {
		t.Error("bundled libraries without their license")
	}
}

func TestVersionMatchesTheBuildTool(t *testing.T) {
	pinned, err := os.ReadFile(filepath.Join("..", "..", "tools", "ffmpeg", "pinned.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `const FFmpegVersion = "` + ffmpegVersion + `"`; !strings.Contains(string(pinned), want) {
		t.Errorf("tools/ffmpeg/pinned.go has no %s", want)
	}
}

func TestBundlePutsFrameworksIntoAnIOSApp(t *testing.T) {
	libs, dist := t.TempDir(), t.TempDir()
	writeArchive(t, libs, "ios", map[string]string{
		"libavutil.framework/libavutil": "avutil", "libavutil.framework/Info.plist": "plist",
		"COPYING.LGPLv2.1": "license", "BUILD.txt": "note",
	})
	pkg := filepath.Join(dist, "My App-1.0.0-ios")
	app := filepath.Join(pkg, "My App.app")
	os.MkdirAll(app, 0o755)
	os.WriteFile(filepath.Join(app, "My App"), []byte("exe"), 0o755)
	ipa := filepath.Join(pkg, "My App.ipa")
	os.WriteFile(ipa, nil, 0o644)

	// The device app is unsigned, so its frameworks are left for its
	// signing.
	if err := bundle(pkg, "", "", &source{dir: libs}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"Frameworks/libavutil.framework/libavutil":  "avutil",
		"Frameworks/libavutil.framework/Info.plist": "plist",
		"FFmpeg/COPYING.LGPLv2.1":                   "license",
	} {
		got, err := os.ReadFile(filepath.Join(app, name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v", name, got, err)
		}
	}
	zr, err := zip.OpenReader(ipa)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		found = found || f.Name == "Payload/My App.app/Frameworks/libavutil.framework/libavutil"
	}
	if !found {
		t.Error("the IPA lacks the framework")
	}
}
