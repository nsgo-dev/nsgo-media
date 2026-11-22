// Package library finds FFmpeg's shared libraries: the directory the
// NSGO_MEDIA_FFMPEG environment variable names, the app's package, beside
// the executable, then the system's. An iOS app has them as frameworks in
// its Frameworks, and no others.
package library

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// EnvDir is the environment variable naming a directory to load FFmpeg's
// libraries from before any other.
const EnvDir = "NSGO_MEDIA_FFMPEG"

// FileName is the file a library has on goos at a major version, as
// FFmpeg's build names it: libavutil.61.dylib, avutil-61.dll or
// libavutil.so.61, and on iOS the binary of the framework tools/ffmpeg
// makes, libavutil.framework/libavutil, whose major the loader checks.
func FileName(goos, lib string, major int) string {
	switch goos {
	case "ios":
		return fmt.Sprintf("lib%s.framework/lib%s", lib, lib)
	case "darwin":
		return fmt.Sprintf("lib%s.%d.dylib", lib, major)
	case "windows":
		return fmt.Sprintf("%s-%d.dll", lib, major)
	}
	return fmt.Sprintf("lib%s.so.%d", lib, major)
}

// Dirs returns the directories to look in, in order. An empty directory
// stands for the system's own search, which finds libraries by name alone.
func Dirs() []string {
	exe, err := os.Executable()
	if err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
	}
	return dirs(runtime.GOOS, os.Getenv(EnvDir), exe)
}

func dirs(goos, env, exe string) []string {
	var out []string
	add := func(d string) {
		for _, seen := range out {
			if seen == d {
				return
			}
		}
		out = append(out, d)
	}
	if env != "" {
		add(env)
	}
	if goos == "ios" {
		// An iOS app's executable is at its bundle's root, beside its
		// Frameworks, and loads no library from elsewhere.
		if exe != "" {
			add(filepath.Join(filepath.Dir(exe), "Frameworks"))
		}
		return out
	}
	if exe != "" {
		bin := filepath.Dir(exe)
		if goos == "darwin" {
			// An app bundle's executable is in Contents/MacOS, its
			// libraries in Contents/Frameworks.
			add(filepath.Join(filepath.Dir(bin), "Frameworks"))
		}
		add(bin)
	}
	if goos == "darwin" {
		// dlopen's own search covers no package manager's prefix, where a
		// developer's FFmpeg is.
		for _, d := range []string{"/opt/homebrew/opt/ffmpeg/lib", "/usr/local/opt/ffmpeg/lib", "/opt/homebrew/lib", "/usr/local/lib", "/opt/local/lib"} {
			add(d)
		}
	}
	add("")
	return out
}

// Path is where to load lib from in dir: a file path, or for the system's
// search the file name alone.
func Path(goos, dir, lib string, major int) string {
	name := FileName(goos, lib, major)
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}
