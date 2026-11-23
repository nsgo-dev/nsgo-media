// Command nsgo-media puts the FFmpeg frameworks NSGo Media loads into an
// iOS app's Frameworks, which nsgo build wrote, with FFmpeg's license and
// build note, and signs the app again. nsgo build itself puts the libraries
// into desktop apps' packages, as the core's nsgo-natives.yaml declares.
//
// Add it to an app's tools and run it after building:
//
//	go get -tool github.com/oneo-me/nsgo-media/cmd/nsgo-media
//	go tool nsgo build -target ios
//	go tool nsgo-media bundle dist/*
//
// It takes the archives go run ./tools/ffmpeg built, from dist/ffmpeg in
// the current directory or else in the NSGo Media module's directory, or
// from the directory -libs names.
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
	libs := fs.String("libs", "", "the directory of the ffmpeg-<version>-<target>.zip archives tools/ffmpeg wrote; by default dist/ffmpeg in the current directory, or else in the NSGo Media module's directory")
	target := fs.String("target", "", "the packages' target, ios or ios_simulator; by default the end of each package's name, as nsgo build names them")
	sign := fs.String("sign", "", "the identity the app is signed again with; by default the one it is signed with")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: nsgo-media bundle [-libs DIR] [-target T] [-sign IDENTITY] PACKAGE...")
		fs.PrintDefaults()
	}
	fs.Parse(os.Args[2:])
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	if *libs == "" {
		*libs = defaultLibs()
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
