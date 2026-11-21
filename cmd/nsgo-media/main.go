// Command nsgo-media puts the FFmpeg libraries NSGo Media loads into an
// app's packages that nsgo build wrote: a macOS app's Contents/Frameworks,
// signed again with the app, and beside the executable of a Windows or
// Linux package, with FFmpeg's license and build note.
//
// Add it to an app's tools and run it after building:
//
//	go get -tool github.com/oneo-me/nsgo-media/cmd/nsgo-media
//	go tool nsgo build -installers=false
//	go tool nsgo-media bundle dist/*
//
// It takes the libraries of the release this module pins, downloaded once
// and checked against their SHA-256, or with -libs the archives
// tools/ffmpeg built.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "bundle" {
		fmt.Fprintln(os.Stderr, "usage: nsgo-media bundle [-libs DIR] [-target T] [-sign IDENTITY] PACKAGE...")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("bundle", flag.ExitOnError)
	libs := fs.String("libs", "", "a directory of ffmpeg-<version>-<target>.zip archives, as tools/ffmpeg writes, instead of the pinned release's")
	target := fs.String("target", "", "the packages' target, such as macos_arm64; by default the end of each package's name, as nsgo build names them")
	sign := fs.String("sign", "", "the identity a macOS app is signed again with; by default the one it is signed with")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: nsgo-media bundle [-libs DIR] [-target T] [-sign IDENTITY] PACKAGE...")
		fs.PrintDefaults()
	}
	fs.Parse(os.Args[2:])
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	src := &source{dir: *libs}
	failed := false
	for _, pkg := range fs.Args() {
		if err := bundle(pkg, *target, *sign, src); err != nil {
			fmt.Fprintf(os.Stderr, "nsgo-media: %s: %v\n", pkg, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
