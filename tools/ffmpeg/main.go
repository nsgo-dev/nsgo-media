// Command ffmpeg builds the FFmpeg shared libraries NSGo Media loads, with
// LGPL components only, at the pinned version, for each desktop target, and
// uploads them to a GitHub release.
//
// It runs on macOS with Xcode and Go: Xcode's clang builds the macOS
// targets, and a pinned Zig, which it downloads, cross-compiles Windows and
// Linux. Every download is checked against its pinned SHA-256.
//
//	go run ./tools/ffmpeg                        # every target into dist/ffmpeg
//	go run ./tools/ffmpeg -target macos_arm64    # one target
//	go run ./tools/ffmpeg -upload v0.1.0         # build, then upload to that release
//	go run ./tools/ffmpeg -layout internal/ffmpeg/layout.go  # regenerate the binding's struct layouts
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	// The tool runs itself as the compiler and pkg-config FFmpeg's
	// configure calls, so no shell scripts or host packages are needed.
	if len(os.Args) > 2 && os.Args[1] == "-as" {
		os.Exit(wrapper(os.Args[2], os.Args[3:]))
	}
	targets := flag.String("target", strings.Join(allTargets, ","), "comma-separated targets to build: "+strings.Join(allTargets, ", "))
	work := flag.String("work", defaultWork(), "directory for downloads and builds, kept between runs")
	out := flag.String("out", filepath.Join("dist", "ffmpeg"), "directory for the archives")
	jobs := flag.Int("jobs", runtime.NumCPU(), "parallel compile jobs")
	upload := flag.String("upload", "", "after building, upload the archives to this existing GitHub release tag with gh")
	layout := flag.String("layout", "", "instead of building, write the binding's struct layouts from FFmpeg's headers to this Go file")
	flag.Parse()

	if runtime.GOOS != "darwin" {
		fail(fmt.Errorf("the tool runs on macOS with Xcode; this is %s", runtime.GOOS))
	}
	b, err := newBuilder(*work, *out, *jobs)
	if err != nil {
		fail(err)
	}
	if *layout != "" {
		if err := writeLayout(b, *layout); err != nil {
			fail(err)
		}
		return
	}
	var archives []string
	for _, name := range strings.Split(*targets, ",") {
		t, ok := targetByName(strings.TrimSpace(name))
		if !ok {
			fail(fmt.Errorf("unknown target %q; known: %s", name, strings.Join(allTargets, ", ")))
		}
		a, err := b.build(t)
		if err != nil {
			fail(fmt.Errorf("%s: %w", t.name, err))
		}
		archives = append(archives, a)
	}
	sums, err := writeChecksums(*out)
	if err != nil {
		fail(err)
	}
	if *upload != "" {
		if err := uploadRelease(*upload, append(archives, sums)); err != nil {
			fail(err)
		}
	}
}

func defaultWork() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "nsgo-media-ffmpeg")
	}
	return filepath.Join(dir, "nsgo-media", "ffmpeg")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "ffmpeg:", err)
	os.Exit(1)
}
