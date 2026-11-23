package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// releaseTag is the tag of this repository's GitHub release the desktop
// libraries are published under, which nsgo-natives.yaml downloads them
// from. Its assets never change: libraries built again are published
// under a new tag.
const releaseTag = "ffmpeg-" + FFmpegVersion + "-1"

const releaseURL = "https://github.com/nsgo-dev/nsgo-media/releases/download/" + releaseTag + "/"

// nativeTargets are the targets of NSGo's native-library manifest, in its
// order, and whether tools/ffmpeg builds libraries for them; NSGo packages
// none for the others.
var nativeTargets = []struct {
	name  string
	built bool
}{
	{"macos_arm64", true}, {"macos_x64", true},
	{"windows_arm64", false}, {"windows_x64", true},
	{"linux_arm64", false}, {"linux_x64", true},
}

// Notices the manifest lists, as package-relative paths. NSGo reads a
// file named COPYING as a license, which must be one it recognizes, and
// one named NOTICE as a notice going with it.
const (
	noticesDir    = "notices"
	lgplNotice    = noticesDir + "/COPYING.LGPLv2.1"
	libxml2Notice = noticesDir + "/COPYING.libxml2"
	patchNotice   = noticesDir + "/NOTICE-" + patchName
)

func buildNotice(target string) string { return noticesDir + "/" + target + "/NOTICE-BUILD.txt" }

// archiveNotices maps the notices of an archive to the manifest's paths;
// BUILD.txt differs by target.
func archiveNotices(target string) map[string]string {
	return map[string]string{
		"COPYING.LGPLv2.1": lgplNotice,
		"COPYING.libxml2":  libxml2Notice,
		patchName:          patchNotice,
		"BUILD.txt":        buildNotice(target),
	}
}

// writeNatives takes the desktop archives and source in out, which a
// build wrote, and writes:
//
//   - out/release: the files to publish under releaseTag, each desktop
//     target's libraries as <target>-<file>, every archive, and FFmpeg's
//     source with its diff;
//   - pkg/nsgo-natives.yaml, declaring those libraries by their URLs and
//     SHA-256, and their notices under pkg/notices.
func writeNatives(out, pkg string) error {
	release := filepath.Join(out, "release")
	if err := os.RemoveAll(release); err != nil {
		return err
	}
	if err := os.MkdirAll(release, 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(pkg, noticesDir)); err != nil {
		return err
	}
	notices := map[string][]byte{}
	var yaml strings.Builder
	yaml.WriteString("# Written by go run ./tools/ffmpeg -natives: the FFmpeg libraries NSGo's tool\n# puts into a desktop app's packages, published as release " + releaseTag + ".\nversion: 1\nlibraries:\n")
	for _, nt := range nativeTargets {
		if !nt.built {
			fmt.Fprintf(&yaml, "  %s: []\n", nt.name)
			continue
		}
		fmt.Fprintf(&yaml, "  %s:\n", nt.name)
		files, err := readArchive(filepath.Join(out, archiveFile(nt.name)))
		if err != nil {
			return fmt.Errorf("%s: %w", nt.name, err)
		}
		for from, to := range archiveNotices(nt.name) {
			data, ok := files[from]
			if !ok {
				return fmt.Errorf("%s has no %s", archiveFile(nt.name), from)
			}
			if prev, ok := notices[to]; ok && !bytes.Equal(prev, data) {
				return fmt.Errorf("%s differs between the archives", from)
			}
			notices[to] = data
		}
		t, _ := targetByName(nt.name)
		for _, lib := range libraries {
			re := libraryFile(t, lib)
			names := slices.Sorted(func(yield func(string) bool) {
				for name := range files {
					if re.MatchString(name) && !yield(name) {
						return
					}
				}
			})
			if len(names) != 1 {
				return fmt.Errorf("%s holds %d libraries for %s", archiveFile(nt.name), len(names), lib)
			}
			name, data := names[0], files[names[0]]
			asset := nt.name + "-" + name
			if err := os.WriteFile(filepath.Join(release, asset), data, 0o644); err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			licenses := []string{lgplNotice, buildNotice(nt.name)}
			if lib == "avformat" {
				// Only libavformat links libxml2 and holds the change.
				licenses = append(licenses, libxml2Notice, patchNotice)
			}
			fmt.Fprintf(&yaml, "    - name: %s\n      url: %s\n      sha256: %q\n      licenses: [%s]\n",
				name, releaseURL+asset, hex.EncodeToString(sum[:]), strings.Join(licenses, ", "))
		}
	}
	for _, t := range targets {
		if err := copyFile(filepath.Join(out, archiveFile(t.name)), filepath.Join(release, archiveFile(t.name))); err != nil {
			return err
		}
	}
	for _, name := range []string{ffmpegSource.name, patchName} {
		if err := copyFile(filepath.Join(out, "source", name), filepath.Join(release, name)); err != nil {
			return err
		}
	}
	for rel, data := range notices {
		p := filepath.Join(pkg, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(pkg, "nsgo-natives.yaml"), []byte(yaml.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("== publish %s as release %s, then commit %s\n", release, releaseTag, pkg)
	return nil
}

func archiveFile(target string) string { return "ffmpeg-" + FFmpegVersion + "-" + target + ".zip" }

// readArchive reads the files at the top of a zip archive.
func readArchive(name string) (map[string][]byte, error) {
	zr, err := zip.OpenReader(name)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	files := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || path.Dir(f.Name) != "." {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		files[f.Name] = data
	}
	return files, nil
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o644)
}
