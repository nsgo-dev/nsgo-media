package main

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// wrapper runs the tool as one of the programs FFmpeg's configure and
// Makefile call for a Zig target: cc, ar, ranlib or pkg-config.
func wrapper(mode string, args []string) int {
	switch mode {
	case "cc":
		return execZig(append([]string{"cc", "-target", os.Getenv("NSGO_MEDIA_ZIG_TARGET")}, ccArgs(os.Getenv("NSGO_MEDIA_ZIG_TARGET"), args)...))
	case "ar", "ranlib":
		return execZig(append([]string{mode}, args...))
	case "pkg-config":
		return pkgConfig(os.Getenv("NSGO_MEDIA_PKG_ROOT"), args)
	case "readelf":
		return readelf(args)
	}
	fmt.Fprintln(os.Stderr, "unknown wrapper", mode)
	return 2
}

func execZig(args []string) int {
	zig := os.Getenv("NSGO_MEDIA_ZIG")
	err := syscall.Exec(zig, append([]string{zig}, args...), os.Environ())
	fmt.Fprintln(os.Stderr, "exec zig:", err)
	return 1
}

// ccArgs adapts the flags configure passes for MinGW and GNU ld to Zig's
// linker, and makes shared libraries find their siblings and drop symbols.
func ccArgs(triple string, args []string) []string {
	var out []string
	shared := false
	for _, a := range args {
		switch {
		case a == "-Wl,--pic-executable,-e,mainCRTStartup":
			out = append(out, "-Wl,-e,mainCRTStartup")
		case strings.HasPrefix(a, "-Wl,--image-base,"):
		default:
			if a == "-shared" {
				shared = true
			}
			out = append(out, a)
		}
	}
	if strings.Contains(triple, "linux") {
		// libva's own libraries need symbols from a newer glibc than the
		// target's; they are the system's at run time.
		out = append(out, "-Wl,--allow-shlib-undefined")
		if shared {
			out = append(out, "-Wl,-rpath,$ORIGIN")
		}
	}
	if shared {
		out = append(out, "-s")
	}
	return out
}

// pkgConfig answers configure's pkg-config questions from the unpacked
// libva packages under root and the libxml2 the builder made, and for
// nothing else, so no library of the build host is found.
func pkgConfig(root string, args []string) int {
	var mode string
	var pkgs []string
	// configure passes a package and its version as one argument.
	var split []string
	for _, a := range args {
		split = append(split, strings.Fields(a)...)
	}
	args = split
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--version":
			fmt.Println("0.29.2")
			return 0
		case a == "--exists", a == "--cflags", a == "--libs", a == "--modversion":
			mode = a
		case strings.HasPrefix(a, "--"):
		case a == ">=" || a == "<=" || a == "=" || a == ">" || a == "<":
			i++ // skip the version that follows
		default:
			pkgs = append(pkgs, a)
		}
	}
	lib := filepath.Join(root, "usr", "lib", "x86_64-linux-gnu")
	var flags []string
	version := "1.17.0"
	for _, p := range pkgs {
		switch p {
		case "libxml-2.0":
			cflags, libs := os.Getenv("NSGO_MEDIA_LIBXML2_CFLAGS"), os.Getenv("NSGO_MEDIA_LIBXML2_LIBS")
			if libs == "" {
				return 1
			}
			flags = append(flags, strings.Fields(cflags+" "+libs)...)
			version = "2.15.4"
			continue
		case "ffnvcodec":
			cflags := os.Getenv("NSGO_MEDIA_FFNVCODEC_CFLAGS")
			if cflags == "" {
				return 1
			}
			flags = append(flags, strings.Fields(cflags)...)
			version = "12.1.14.1"
			continue
		case "vpl":
			cflags, libs := os.Getenv("NSGO_MEDIA_VPL_CFLAGS"), os.Getenv("NSGO_MEDIA_VPL_LIBS")
			if libs == "" {
				return 1
			}
			flags = append(flags, strings.Fields(cflags+" "+libs)...)
			version = "2.17.0"
			continue
		case "opus", "vpx":
			name := strings.ToUpper(p)
			cflags, libs := os.Getenv("NSGO_MEDIA_"+name+"_CFLAGS"), os.Getenv("NSGO_MEDIA_"+name+"_LIBS")
			if libs == "" {
				return 1
			}
			flags = append(flags, strings.Fields(cflags+" "+libs)...)
			version = map[string]string{"opus": "1.5.2", "vpx": "1.17.0"}[p]
			continue
		case "openh264":
			cflags, libs := os.Getenv("NSGO_MEDIA_OPENH264_CFLAGS"), os.Getenv("NSGO_MEDIA_OPENH264_LIBS")
			if libs == "" {
				return 1
			}
			flags = append(flags, strings.Fields(cflags+" "+libs)...)
			version = "2.7.0"
			continue
		}
		if root == "" {
			return 1
		}
		switch p {
		case "libva":
			flags = append(flags, "-I"+filepath.Join(root, "usr", "include"), "-L"+lib, "-lva")
		case "libva-drm":
			flags = append(flags, "-I"+filepath.Join(root, "usr", "include"), "-L"+lib, "-lva-drm", "-lva")
		default:
			return 1
		}
	}
	switch mode {
	case "--cflags":
		fmt.Println(strings.Join(filter(flags, "-I", "-D"), " "))
	case "--libs":
		fmt.Println(strings.Join(filter(flags, "-L", "-l"), " "))
	case "--modversion":
		fmt.Println(version)
	}
	return 0
}

// readelf answers libvpx's configure, which runs readelf -WS on an ELF
// object nasm assembled to check its sections' alignment: a line per
// section ending in its alignment.
func readelf(args []string) int {
	if len(args) == 0 {
		return 2
	}
	f, err := elf.Open(args[len(args)-1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "readelf:", err)
		return 1
	}
	defer f.Close()
	for i, s := range f.Sections {
		fmt.Printf("  [%2d] %s %s %d\n", i, s.Name, s.Type, s.Addralign)
	}
	return 0
}

func filter(flags []string, prefixes ...string) []string {
	var out []string
	for _, f := range flags {
		for _, p := range prefixes {
			if strings.HasPrefix(f, p) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}
