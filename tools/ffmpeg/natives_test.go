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
		for _, lib := range libraries {
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
	for _, want := range []string{
		"version: 1\n", "  windows_arm64: []\n", "  linux_arm64: []\n",
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
		"notices/NOTICE-ffmpeg.patch":          "diff",
		"notices/macos_arm64/NOTICE-BUILD.txt": "build macos_arm64",
	} {
		if got, _ := os.ReadFile(filepath.Join(pkg, name)); string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"linux_x64-libavcodec.so.61": "avcodec linux_x64",
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

// TestManifestIsTheRelease checks that the core's manifest declares the
// libraries of releaseTag, so a new version or tag comes with -natives run
// again.
func TestManifestIsTheRelease(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("..", "..", "internal", "ffmpeg", "library", "nsgo-natives.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(manifest), "url: "); n != strings.Count(string(manifest), "url: "+releaseURL) || n != 4*len(libraries) {
		t.Errorf("the manifest declares %d libraries, not all of release %s", n, releaseTag)
	}
}
