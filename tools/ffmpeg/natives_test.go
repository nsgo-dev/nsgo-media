package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteNatives(t *testing.T) {
	out, pkg := t.TempDir(), t.TempDir()
	for _, tg := range targets {
		f, err := os.Create(filepath.Join(out, archiveFile(tg.name)))
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		files := map[string]string{
			"COPYING.LGPLv2.1": "lgpl", "COPYING.libxml2": "mit", patchName: "diff", "BUILD.txt": "build " + tg.name,
		}
		switch tg.name {
		case "windows_x64":
			files["COPYING.libvpl"] = "vpl-mit"
		case "linux_x64":
			files["COPYING.openh264"] = "openh264-bsd"
		}
		for _, lib := range libraries {
			if tg.ios() {
				files["lib"+lib+".framework/lib"+lib] = lib + " " + tg.name
				files["lib"+lib+".framework/Info.plist"] = "plist"
				continue
			}
			files[map[string]string{
				"darwin": "lib" + lib + ".61.dylib", "windows": lib + "-61.dll", "linux": "lib" + lib + ".so.61",
			}[tg.os]] = lib + " " + tg.name
		}
		for name, body := range files {
			w, _ := zw.Create(name)
			w.Write([]byte(body))
		}
		zw.Close()
		f.Close()
	}
	os.MkdirAll(filepath.Join(out, "source"), 0o755)
	os.WriteFile(filepath.Join(out, "source", ffmpegSource.name), []byte("tarball"), 0o644)
	os.WriteFile(filepath.Join(out, "source", patchName), []byte("diff"), 0o644)

	if err := writeNatives(out, pkg); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(pkg, "nsgo-natives.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("avformat windows_x64"))
	iosSum := sha256.Sum256([]byte("avcodec ios_simulator"))
	for _, want := range []string{
		"version: 2\n", "  windows_arm64: []\n", "  linux_arm64: []\n", "  tvos: []\n", "  tvos_simulator: []\n",
		"    - name: libavcodec.framework\n      url: " + releaseURL + "ios_simulator-libavcodec\n      sha256: \"" + hex.EncodeToString(iosSum[:]) + "\"\n" +
			"      licenses: [notices/COPYING.LGPLv2.1, notices/ios_simulator/NOTICE-BUILD.txt]\n",
		"\nlinux:\n  linux_arm64:\n", "      deb: [libva2, libva-drm2]\n",
		"    - name: avformat-61.dll\n      url: " + releaseURL + "windows_x64-avformat-61.dll\n      sha256: \"" + hex.EncodeToString(sum[:]) + "\"\n" +
			"      licenses: [notices/COPYING.LGPLv2.1, notices/windows_x64/NOTICE-BUILD.txt, notices/COPYING.libxml2, notices/NOTICE-ffmpeg.patch]\n",
		"    - name: libavutil.61.dylib\n      url: " + releaseURL + "macos_x64-libavutil.61.dylib\n",
		"licenses: [notices/COPYING.LGPLv2.1, notices/linux_x64/NOTICE-BUILD.txt]\n",
	} {
		if !strings.Contains(string(manifest), want) {
			t.Errorf("the manifest lacks %q:\n%s", want, manifest)
		}
	}
	for name, want := range map[string]string{
		"notices/COPYING.LGPLv2.1":             "lgpl",
		"notices/COPYING.libvpl":               "vpl-mit",
		"notices/COPYING.openh264":             "openh264-bsd",
		"notices/NOTICE-ffmpeg.patch":          "diff",
		"notices/macos_arm64/NOTICE-BUILD.txt": "build macos_arm64",
		"notices/ios/NOTICE-BUILD.txt":         "build ios",
	} {
		if got, _ := os.ReadFile(filepath.Join(pkg, name)); string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"linux_x64-libavcodec.so.61": "avcodec linux_x64",
		"ios-libavutil":              "avutil ios",
		archiveFile("ios"):           "",
		ffmpegSource.name:            "tarball",
		patchName:                    "diff",
	} {
		got, err := os.ReadFile(filepath.Join(out, "release", name))
		if err != nil || want != "" && string(got) != want {
			t.Errorf("release %s = %q, %v", name, got, err)
		}
	}
}

// TestManifestIsTheRelease checks that the core's manifest is version 2
// and declares, for every target tools/ffmpeg builds, its libraries or
// frameworks as the assets of releaseTag that -natives names, with notices
// that are there, so a new version or tag comes with -natives run again.
func TestManifestIsTheRelease(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "ffmpeg", "library")
	manifest, err := os.ReadFile(filepath.Join(dir, "nsgo-natives.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	if !strings.Contains(text, "\nversion: 2\n") || !strings.HasSuffix(text, linuxPackages) {
		t.Errorf("the manifest is not version 2 with the Linux packages")
	}
	// entries maps each target to its entries' names, asset URLs and notices.
	type entry struct{ name, url, licenses string }
	entries := map[string][]entry{}
	libs, _, _ := strings.Cut(text, "\nlinux:\n")
	target := ""
	for _, line := range strings.Split(libs, "\n") {
		switch {
		case strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.Contains(line, ":"):
			target, _, _ = strings.Cut(strings.TrimSpace(line), ":")
			entries[target] = entries[target][:0:0]
		case target != "" && strings.HasPrefix(line, "    - name: "):
			entries[target] = append(entries[target], entry{name: strings.TrimPrefix(line, "    - name: ")})
		case target != "" && strings.HasPrefix(line, "      url: "):
			entries[target][len(entries[target])-1].url = strings.TrimPrefix(line, "      url: ")
		case target != "" && strings.HasPrefix(line, "      licenses: "):
			entries[target][len(entries[target])-1].licenses = strings.Trim(strings.TrimPrefix(line, "      licenses: "), "[]")
		}
	}
	for _, nt := range nativeTargets {
		got, ok := entries[nt.name]
		if !ok {
			t.Errorf("the manifest has no %s", nt.name)
			continue
		}
		if !nt.built {
			if len(got) != 0 {
				t.Errorf("%s declares %d libraries, none built", nt.name, len(got))
			}
			continue
		}
		if len(got) != len(libraries) {
			t.Errorf("%s declares %d libraries, not %d", nt.name, len(got), len(libraries))
			continue
		}
		tg, _ := targetByName(nt.name)
		for i, lib := range libraries {
			e := got[i]
			asset := nt.name + "-" + e.name
			if tg.ios() {
				asset = nt.name + "-lib" + lib
				if e.name != "lib"+lib+".framework" {
					t.Errorf("%s declares %s, not lib%s.framework", nt.name, e.name, lib)
				}
			} else if !libraryFile(tg, lib).MatchString(e.name) {
				t.Errorf("%s declares %s for %s", nt.name, e.name, lib)
			}
			if e.url != releaseURL+asset {
				t.Errorf("%s %s is at %s, not release %s's %s", nt.name, e.name, e.url, releaseTag, asset)
			}
			for _, notice := range strings.Split(e.licenses, ", ") {
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(notice))); err != nil {
					t.Errorf("%s %s: %v", nt.name, e.name, err)
				}
			}
		}
	}
}
