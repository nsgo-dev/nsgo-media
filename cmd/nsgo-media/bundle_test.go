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
		"dist/My App-1.0.0-windows_x64": "windows_x64",
		"dist/My App-1.0.0-macos_arm64": "macos_arm64",
		"dist/releases":                 "",
		"dist/My App-1.0.0-ios":         "ios",
		"dist/My App-1.0.0-tvos":        "",
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
		"COPYING.LGPLv2.1": "license", "COPYING.libxml2": "xml", "ffmpeg.patch": "diff", "BUILD.txt": "note",
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
		"FFmpeg/COPYING.LGPLv2.1": "license", "FFmpeg/COPYING.libxml2": "xml",
		"FFmpeg/ffmpeg.patch": "diff", "FFmpeg/BUILD.txt": "note",
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

func TestBundleWithoutLibraries(t *testing.T) {
	pkg := filepath.Join(t.TempDir(), "App-1.0.0-linux_x64")
	os.MkdirAll(pkg, 0o755)
	err := bundle(pkg, "", "", &source{dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "go run ./tools/ffmpeg") {
		t.Fatalf("bundle without libraries: %v", err)
	}
}

func TestBundleWithoutLicense(t *testing.T) {
	libs := t.TempDir()
	writeArchive(t, libs, "linux_x64", map[string]string{"libavutil.so.61": "avutil", "BUILD.txt": "note"})
	pkg := filepath.Join(t.TempDir(), "App-1.0.0-linux_x64")
	os.MkdirAll(pkg, 0o755)
	err := bundle(pkg, "", "", &source{dir: libs})
	if err == nil || !strings.Contains(err.Error(), "COPYING.LGPLv2.1") {
		t.Fatalf("bundle without the license: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pkg, "libavutil.so.61")); err == nil {
		t.Error("bundled libraries without their license")
	}
}

func TestUniversalNoticesJoinBothBuildNotes(t *testing.T) {
	dir := t.TempDir()
	arm := &libSet{notices: map[string][]byte{"COPYING.LGPLv2.1": []byte("license"), "BUILD.txt": []byte("arm64")}}
	x64 := &libSet{notices: map[string][]byte{"COPYING.LGPLv2.1": []byte("license"), "BUILD.txt": []byte("x86_64")}}
	if err := writeNotices(dir, arm, x64); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "BUILD.txt")); string(got) != "arm64\nx86_64" {
		t.Errorf("BUILD.txt = %q", got)
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
