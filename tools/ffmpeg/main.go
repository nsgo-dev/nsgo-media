// Command ffmpeg builds the FFmpeg shared libraries NSGo Media loads, with
// LGPL components only, at the pinned version, for each desktop target and
// as frameworks for iOS, into archives, and FFmpeg's source with its diff
// into source/, which an app offers beside them. The libraries and iOS
// frameworks are published as a release of this repository, which the
// core's nsgo-natives.yaml declares for nsgo build to put into an app.
//
// It runs on macOS with Xcode and Go: Xcode's clang builds the macOS and
// iOS targets, and a pinned Zig, which it downloads, cross-compiles Windows and
// Linux. Every download is checked against its pinned SHA-256.
//
//	go run ./tools/ffmpeg                        # every target into dist/ffmpeg
//	go run ./tools/ffmpeg -target macos_arm64    # one target
//	go run ./tools/ffmpeg -layout internal/ffmpeg/layout.go  # regenerate the binding's struct layouts
//	go run ./tools/ffmpeg -natives internal/ffmpeg/library   # the release's files and the manifest declaring them
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
	layout := flag.String("layout", "", "instead of building, write the binding's struct layouts from FFmpeg's headers to this Go file")
	natives := flag.String("natives", "", "instead of building, write the release's files from the archives built into -out, and the manifest declaring them with their notices into this package directory")
	flag.Parse()

	if *natives != "" {
		if err := writeNatives(*out, *natives); err != nil {
			fail(err)
		}
		return
	}

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
	for _, name := range strings.Split(*targets, ",") {
		t, ok := targetByName(strings.TrimSpace(name))
		if !ok {
			fail(fmt.Errorf("unknown target %q; known: %s", name, strings.Join(allTargets, ", ")))
		}
		if _, err := b.build(t); err != nil {
			fail(fmt.Errorf("%s: %w", t.name, err))
		}
	}
	if err := b.writeSource(); err != nil {
		fail(fmt.Errorf("source: %w", err))
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
