package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// builder holds the work directory: downloads/ for pinned files, src/ for
// what they unpack to, host/ for tools built for this Mac, deps/ and
// build/ per target.
type builder struct {
	work, out string
	jobs      int
	tool      string // this executable, copied where configure can name it
	nasm      string
	zig       string // the zig executable, once a Zig target needs it
	ffmpeg    string // the unpacked FFmpeg source
}

func newBuilder(work, out string, jobs int) (*builder, error) {
	work, err := filepath.Abs(work)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(work, " \t") {
		return nil, fmt.Errorf("the work directory %q has a space, which FFmpeg's configure cannot take", work)
	}
	if out, err = filepath.Abs(out); err != nil {
		return nil, err
	}
	b := &builder{work: work, out: out, jobs: jobs}
	for _, d := range []string{"downloads", "src", "host/bin", "deps", "build"} {
		if err := os.MkdirAll(filepath.Join(work, d), 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	if err := b.copyTool(); err != nil {
		return nil, err
	}
	if b.ffmpeg, err = b.unpack(ffmpegSource, "ffmpeg-"+FFmpegVersion); err != nil {
		return nil, err
	}
	if err := b.patchFFmpeg(); err != nil {
		return nil, err
	}
	return b, nil
}

// ffmpegPatches are changes to FFmpeg's source, each an old text and its
// replacement, made on date, which BUILD.txt lists and ffmpeg.patch holds.
var ffmpegPatches = []struct{ file, date, why, old, new string }{{
	file: "libavformat/dashdec.c",
	date: "2026-11-22",
	why:  "an interrupted dash_read_packet returns AVERROR_EXIT instead of 0 with no packet, which av_read_frame calls again without end",
	old:  "    }\n    return ret;\n}\n\nstatic int dash_close(",
	new:  "    }\n    if (!ret)\n        ret = AVERROR_EXIT;\n    return ret;\n}\n\nstatic int dash_close(",
}}

// patchFFmpeg applies ffmpegPatches to the unpacked source once.
func (b *builder) patchFFmpeg() error {
	for _, p := range ffmpegPatches {
		path := filepath.Join(b.ffmpeg, p.file)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		switch {
		case strings.Contains(string(data), p.new):
		case strings.Contains(string(data), p.old):
			if err := os.WriteFile(path, []byte(strings.Replace(string(data), p.old, p.new, 1)), 0o644); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: the patch no longer applies", p.file)
		}
	}
	return nil
}

// copyTool puts this executable at a fixed path without spaces, as the
// compiler and pkg-config configure runs.
func (b *builder) copyTool() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	b.tool = filepath.Join(b.work, "host", "bin", "nsgo-media-ffmpeg")
	data, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	return os.WriteFile(b.tool, data, 0o755)
}

// build configures and compiles FFmpeg for t and packs its libraries.
func (b *builder) build(t target) (string, error) {
	fmt.Printf("== %s\n", t.name)
	if t.arch == "x86_64" {
		if err := b.ensureNasm(); err != nil {
			return "", err
		}
	}
	dir := filepath.Join(b.work, "build", t.name)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	args, env, err := b.configureArgs(t)
	if err != nil {
		return "", err
	}
	if err := run(dir, env, filepath.Join(b.ffmpeg, "configure"), args...); err != nil {
		return "", fmt.Errorf("configure: %w (see %s)", err, filepath.Join(dir, "ffbuild", "config.log"))
	}
	if t.os == "linux" {
		// Zig's glibc stubs link sysctl, but glibc 2.32 dropped its header.
		if err := replaceInFile(filepath.Join(dir, "config.h"), "#define HAVE_SYSCTL 1", "#define HAVE_SYSCTL 0"); err != nil {
			return "", err
		}
	}
	if err := run(dir, env, "make", fmt.Sprintf("-j%d", b.jobs)); err != nil {
		return "", fmt.Errorf("make: %w", err)
	}
	return b.pack(t, dir, args)
}

func (b *builder) configureArgs(t target) ([]string, []string, error) {
	args := append([]string{"--arch=" + t.arch}, commonFlags...)
	args = append(args, t.flags...)
	args = append(args, "--pkg-config="+b.tool+" -as pkg-config")
	env := os.Environ()
	if t.arch == "x86_64" {
		args = append(args, "--x86asmexe="+b.nasm)
	}
	nm, err := xcrunFind("nm")
	if err != nil {
		return nil, nil, err
	}
	args = append(args, "--nm="+nm)
	if t.zig == "" {
		clang, err := xcrunFind("clang")
		if err != nil {
			return nil, nil, err
		}
		sdkOut, err := exec.Command("xcrun", "--sdk", t.sdk, "--show-sdk-path").Output()
		if err != nil {
			return nil, nil, fmt.Errorf("xcrun --sdk %s --show-sdk-path: %w; install Xcode", t.sdk, err)
		}
		sdk := strings.TrimSpace(string(sdkOut))
		clangxx, err := xcrunFind("clang++")
		if err != nil {
			return nil, nil, err
		}
		targetFlags := " -arch " + t.arch + " -target " + t.clangTarget() + " -isysroot " + sdk
		cc := clang + targetFlags
		// A macOS library finds its siblings beside it, an iOS framework's
		// binary in the frameworks beside its own.
		rpath := "@loader_path"
		if t.ios() {
			rpath = "@loader_path/.."
		}
		args = append(args, "--cc="+cc, "--extra-ldflags=-Wl,-rpath,"+rpath)
		if t.arch != hostArch() || t.ios() {
			args = append(args, "--enable-cross-compile")
		}
		if !t.ios() {
			// Host tools FFmpeg builds and runs, such as swscale's
			// assembly generator, take SDKROOT too: an iOS one would make
			// them iOS programs.
			env = append(env, "SDKROOT="+sdk)
		}
		xml, err := b.buildLibxml2(t, cc, env)
		if err != nil {
			return nil, nil, err
		}
		env = append(env, libxml2Env(t, xml)...)
		codecs, err := b.buildEncoderLibs(t, cc, clangxx+targetFlags, env)
		if err != nil {
			return nil, nil, err
		}
		return args, append(env, codecs...), nil
	}
	if err := b.ensureZig(); err != nil {
		return nil, nil, err
	}
	zlib, err := b.buildZlib(t)
	if err != nil {
		return nil, nil, err
	}
	env = append(env,
		"NSGO_MEDIA_ZIG="+b.zig,
		"NSGO_MEDIA_ZIG_TARGET="+t.zig,
		"ZIG_GLOBAL_CACHE_DIR="+filepath.Join(b.work, "zig-cache"),
	)
	xml, err := b.buildLibxml2(t, b.zig+" cc -target "+t.zig, env)
	if err != nil {
		return nil, nil, err
	}
	env = append(env, libxml2Env(t, xml)...)
	codecs, err := b.buildEncoderLibs(t, b.zig+" cc -target "+t.zig, b.zig+" c++ -target "+t.zig, env)
	if err != nil {
		return nil, nil, err
	}
	env = append(env, codecs...)
	extraCflags := []string{"-I" + filepath.Join(zlib, "include")}
	extraLdflags := []string{"-L" + filepath.Join(zlib, "lib")}
	switch t.os {
	case "windows":
		nv, err := b.unpackNvCodecHeaders()
		if err != nil {
			return nil, nil, err
		}
		env = append(env, "NSGO_MEDIA_FFNVCODEC_CFLAGS=-I"+filepath.Join(nv, "include"))
		amf, err := b.unpackAMFHeaders()
		if err != nil {
			return nil, nil, err
		}
		extraCflags = append(extraCflags, "-I"+amf)
		vpl, err := b.buildLibvpl(t)
		if err != nil {
			return nil, nil, err
		}
		env = append(env, libvplEnv(vpl)...)
	case "linux":
		root, err := b.unpackLibva()
		if err != nil {
			return nil, nil, err
		}
		env = append(env, "NSGO_MEDIA_PKG_ROOT="+root)
		oh, err := b.buildOpenH264(t)
		if err != nil {
			return nil, nil, err
		}
		env = append(env, openh264Env(oh)...)
	}
	args = append(args,
		"--enable-cross-compile",
		"--cc="+b.tool+" -as cc",
		"--ar="+b.tool+" -as ar",
		"--ranlib="+b.tool+" -as ranlib",
		"--strip=true",
		"--extra-cflags="+strings.Join(extraCflags, " "),
		"--extra-ldflags="+strings.Join(extraLdflags, " "),
	)
	return args, env, nil
}

// libraryFile matches a built library's file under its runtime name:
// libavutil.61.dylib, libavutil.so.61 or avutil-61.dll.
func libraryFile(t target, lib string) *regexp.Regexp {
	switch t.os {
	case "darwin":
		return regexp.MustCompile(`^lib` + lib + `\.\d+\.dylib$`)
	case "windows":
		return regexp.MustCompile(`^` + lib + `-\d+\.dll$`)
	}
	return regexp.MustCompile(`^lib` + lib + `\.so\.\d+$`)
}

// pack zips t's libraries with the licenses of what they hold, the diff of
// FFmpeg's source and a note of how they were built, which the LGPL asks to
// go with them. -natives publishes the libraries and declares the notices.
func (b *builder) pack(t target, dir string, args []string) (string, error) {
	name := filepath.Join(b.out, archiveFile(t.name))
	f, err := os.Create(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, lib := range libraries {
		entries, err := os.ReadDir(filepath.Join(dir, "lib"+lib))
		if err != nil {
			return "", err
		}
		re := libraryFile(t, lib)
		i := slices.IndexFunc(entries, func(e os.DirEntry) bool { return e.Type().IsRegular() && re.MatchString(e.Name()) })
		if i < 0 {
			return "", fmt.Errorf("no built library for %s", lib)
		}
		path := filepath.Join(dir, "lib"+lib, entries[i].Name())
		if t.os == "darwin" {
			if err := run(dir, nil, "strip", "-x", path); err != nil {
				return "", err
			}
		}
		if t.ios() {
			if err := addFramework(zw, t, dir, lib, path); err != nil {
				return "", err
			}
			continue
		}
		if err := addFile(zw, entries[i].Name(), path, 0o755); err != nil {
			return "", err
		}
	}
	if err := addFile(zw, "COPYING.LGPLv2.1", filepath.Join(b.ffmpeg, "COPYING.LGPLv2.1"), 0o644); err != nil {
		return "", err
	}
	xmlSrc := filepath.Join(b.work, "src", strings.TrimSuffix(libxml2Source.name, ".tar.xz"))
	if err := addFile(zw, "COPYING.libxml2", filepath.Join(xmlSrc, "Copyright"), 0o644); err != nil {
		return "", err
	}
	var extraNotes strings.Builder
	vpxSrc := filepath.Join(b.work, "src", strings.TrimSuffix(libvpxSource.name, ".tar.gz"))
	if err := addFile(zw, "COPYING.libvpx", filepath.Join(vpxSrc, "LICENSE"), 0o644); err != nil {
		return "", err
	}
	fmt.Fprintf(&extraNotes, "\nlibavcodec links libvpx's VP9 encoder statically, under the BSD 3-Clause license in COPYING.libvpx.\nSource: %s\nSHA-256: %s\n", libvpxSource.url, libvpxSource.sha256)
	opusSrc := filepath.Join(b.work, "src", strings.TrimSuffix(opusSource.name, ".tar.gz"))
	if err := addFile(zw, "COPYING.opus", filepath.Join(opusSrc, "COPYING"), 0o644); err != nil {
		return "", err
	}
	fmt.Fprintf(&extraNotes, "\nlibavcodec links libopus statically, under the BSD 3-Clause license in COPYING.opus.\nSource: %s\nSHA-256: %s\n", opusSource.url, opusSource.sha256)
	switch t.os {
	case "windows":
		vplSrc := filepath.Join(b.work, "src", strings.TrimSuffix(libvplSource.name, ".tar.gz"))
		if err := addFile(zw, "COPYING.libvpl", filepath.Join(vplSrc, "LICENSE"), 0o644); err != nil {
			return "", err
		}
		fmt.Fprintf(&extraNotes, "\nlibavcodec links libvpl statically, under the MIT license in COPYING.libvpl.\nSource: %s\nSHA-256: %s\n", libvplSource.url, libvplSource.sha256)
		fmt.Fprintf(&extraNotes, "\nBuilt with nv-codec-headers (MIT, dynamic driver load).\nSource: %s\nSHA-256: %s\n", nvCodecHeadersSource.url, nvCodecHeadersSource.sha256)
		fmt.Fprintf(&extraNotes, "\nBuilt with AMF headers (MIT, dynamic driver load).\nSource: %s\nSHA-256: %s\n", amfHeadersSource.url, amfHeadersSource.sha256)
	case "linux":
		ohSrc := filepath.Join(b.work, "src", strings.TrimSuffix(openh264Source.name, ".tar.gz"))
		if err := addFile(zw, "COPYING.openh264", filepath.Join(ohSrc, "LICENSE"), 0o644); err != nil {
			return "", err
		}
		fmt.Fprintf(&extraNotes, "\nlibavcodec links OpenH264 statically, under the BSD 2-Clause license in COPYING.openh264.\nSource: %s\nSHA-256: %s\n", openh264Source.url, openh264Source.sha256)
	}
	var patches strings.Builder
	if len(ffmpegPatches) > 0 {
		diff, err := b.ffmpegDiff()
		if err != nil {
			return "", err
		}
		w, err := zw.Create(patchName)
		if err != nil {
			return "", err
		}
		if _, err := io.WriteString(w, diff); err != nil {
			return "", err
		}
		fmt.Fprintf(&patches, "Changed, as the diff in %s holds:\n", patchName)
		for _, p := range ffmpegPatches {
			fmt.Fprintf(&patches, "  %s on %s: %s\n", p.file, p.date, p.why)
		}
	}
	note := fmt.Sprintf("FFmpeg %s, built for %s by NSGo Media's tools/ffmpeg (https://github.com/nsgo-dev/nsgo-media/tree/main/tools/ffmpeg) under the LGPL 2.1 or later.\n\nSource: %s\nSHA-256: %s\n\n%s\nConfigured with:\n  %s\n\n"+
		"libavformat links libxml2 statically, under the MIT license in COPYING.libxml2, configured with --with-minimum --with-threads and its xmlCleanupParser made a no-op.\nSource: %s\nSHA-256: %s\n%s",
		FFmpegVersion, t.name, ffmpegSource.url, ffmpegSource.sha256, patches.String(), strings.Join(args, "\n  "), libxml2Source.url, libxml2Source.sha256, extraNotes.String())
	w, err := zw.Create("BUILD.txt")
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(w, strings.ReplaceAll(note, b.work, "$WORK")); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	fmt.Printf("== %s: %s\n", t.name, name)
	return name, f.Close()
}

// addFramework adds an iOS target's library at path as the framework
// lib<name>.framework, whose binary is lib<name>: its install name and
// those of the libraries it links become the frameworks', and Info.plist
// names it for the target's platform. The app's signing signs it.
func addFramework(zw *zip.Writer, t target, dir, lib, path string) error {
	fw := "lib" + lib + ".framework"
	bin := filepath.Join(dir, "frameworks", fw, "lib"+lib)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		return err
	}
	args := []string{"-id", "@rpath/" + fw + "/lib" + lib}
	out, err := exec.Command("otool", "-L", bin).Output()
	if err != nil {
		return fmt.Errorf("otool -L %s: %w", bin, err)
	}
	for _, m := range regexp.MustCompile(`@rpath/lib([a-z]+)\.\d+\.dylib`).FindAllStringSubmatch(string(out), -1) {
		if m[1] != lib {
			args = append(args, "-change", m[0], "@rpath/lib"+m[1]+".framework/lib"+m[1])
		}
	}
	if err := run(dir, nil, "install_name_tool", append(args, bin)...); err != nil {
		return err
	}
	if err := addFile(zw, fw+"/lib"+lib, bin, 0o755); err != nil {
		return err
	}
	w, err := zw.Create(fw + "/Info.plist")
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, frameworkPlist(t, lib))
	return err
}

// frameworkPlist is the Info.plist of lib's framework for t.
func frameworkPlist(t target, lib string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key><string>lib` + lib + `</string>
	<key>CFBundleIdentifier</key><string>org.ffmpeg.lib` + lib + `</string>
	<key>CFBundleName</key><string>lib` + lib + `</string>
	<key>CFBundlePackageType</key><string>FMWK</string>
	<key>CFBundleShortVersionString</key><string>` + FFmpegVersion + `</string>
	<key>CFBundleVersion</key><string>` + FFmpegVersion + `</string>
	<key>CFBundleSupportedPlatforms</key><array><string>` + t.platform() + `</string></array>
	<key>MinimumOSVersion</key><string>` + iOSMin + `</string>
</dict>
</plist>
`
}

func addFile(zw *zip.Writer, name, path string, mode os.FileMode) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

// fetch downloads d once and checks its SHA-256, returning its path.
func (b *builder) fetch(d download) (string, error) {
	path := filepath.Join(b.work, "downloads", d.name)
	if sum, err := fileSHA256(path); err == nil && sum == d.sha256 {
		return path, nil
	}
	fmt.Printf("fetching %s\n", d.url)
	resp, err := http.Get(d.url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", d.url, resp.Status)
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != d.sha256 {
		os.Remove(tmp)
		return "", fmt.Errorf("%s: SHA-256 is %s, pinned %s", d.name, got, d.sha256)
	}
	return path, os.Rename(tmp, path)
}

// unpack fetches and extracts an archive into src/, returning the
// directory it holds, dir.
func (b *builder) unpack(d download, dir string) (string, error) {
	path, err := b.fetch(d)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(b.work, "src", dir)
	stamp := filepath.Join(dest, ".nsgo-media-sha256")
	if got, err := os.ReadFile(stamp); err == nil && string(got) == d.sha256 {
		return dest, nil
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := run(filepath.Join(b.work, "src"), nil, "tar", "-xf", path); err != nil {
		return "", err
	}
	return dest, os.WriteFile(stamp, []byte(d.sha256), 0o644)
}

func (b *builder) ensureNasm() error {
	b.nasm = filepath.Join(b.work, "host", "bin", "nasm")
	if _, err := os.Stat(b.nasm); err == nil {
		return nil
	}
	src, err := b.unpack(nasmSource, strings.TrimSuffix(nasmSource.name, ".tar.xz"))
	if err != nil {
		return err
	}
	if err := run(src, nil, "./configure", "--prefix="+filepath.Join(b.work, "host")); err != nil {
		return err
	}
	if err := run(src, nil, "make", fmt.Sprintf("-j%d", b.jobs), "nasm"); err != nil {
		return err
	}
	return os.Rename(filepath.Join(src, "nasm"), b.nasm)
}

func (b *builder) ensureZig() error {
	if b.zig != "" {
		return nil
	}
	d := zigToolchain()
	dir, err := b.unpack(d, strings.TrimSuffix(d.name, ".tar.xz"))
	if err != nil {
		return err
	}
	b.zig = filepath.Join(dir, "zig")
	return nil
}

// buildZlib compiles zlib into a static library for a Zig target.
func (b *builder) buildZlib(t target) (string, error) {
	src, err := b.unpack(zlibSource, strings.TrimSuffix(zlibSource.name, ".tar.gz"))
	if err != nil {
		return "", err
	}
	dest := filepath.Join(b.work, "deps", t.name, "zlib")
	if _, err := os.Stat(filepath.Join(dest, "lib", "libz.a")); err == nil {
		return dest, nil
	}
	for _, d := range []string{"include", "lib", "obj"} {
		if err := os.MkdirAll(filepath.Join(dest, d), 0o755); err != nil {
			return "", err
		}
	}
	env := append(os.Environ(), "ZIG_GLOBAL_CACHE_DIR="+filepath.Join(b.work, "zig-cache"))
	var objs []string
	for _, f := range []string{"adler32", "crc32", "deflate", "infback", "inffast", "inflate", "inftrees", "trees", "zutil", "compress", "uncompr"} {
		obj := filepath.Join(dest, "obj", f+".o")
		if err := run(src, env, b.zig, "cc", "-target", t.zig, "-O2", "-fPIC", "-c", f+".c", "-o", obj); err != nil {
			return "", err
		}
		objs = append(objs, obj)
	}
	if err := run(src, env, b.zig, append([]string{"ar", "rcs", filepath.Join(dest, "lib", "libz.a")}, objs...)...); err != nil {
		return "", err
	}
	for _, h := range []string{"zlib.h", "zconf.h"} {
		data, err := os.ReadFile(filepath.Join(src, h))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dest, "include", h), data, 0o644); err != nil {
			return "", err
		}
	}
	return dest, nil
}

// buildLibxml2 compiles libxml2's smallest configuration, which the DASH
// demuxer's parsing needs, into a static library for t with cc. Its
// xmlCleanupParser, which the demuxer calls after each manifest, is made a
// no-op: it frees the library's global state under other players parsing
// at the same time, and the library frees that state itself at exit.
func (b *builder) buildLibxml2(t target, cc string, env []string) (string, error) {
	src, err := b.unpack(libxml2Source, strings.TrimSuffix(libxml2Source.name, ".tar.xz"))
	if err != nil {
		return "", err
	}
	dest := filepath.Join(b.work, "deps", t.name, "libxml2")
	lib := filepath.Join(dest, "lib", "libxml2.a")
	if _, err := os.Stat(lib); err == nil {
		return dest, nil
	}
	threads := filepath.Join(src, "threads.c")
	data, err := os.ReadFile(threads)
	if err != nil {
		return "", err
	}
	if cleanup := "xmlCleanupParser(void) {\n"; !strings.Contains(string(data), cleanup+"    return;") {
		if err := replaceInFile(threads, cleanup, cleanup+"    return;\n"); err != nil {
			return "", err
		}
	}
	dir := filepath.Join(b.work, "build", t.name+"-libxml2")
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ar, ranlib := "ar", "ranlib"
	if t.zig != "" {
		ar, ranlib = b.zig+" ar", b.zig+" ranlib"
	}
	if err := run(dir, env, filepath.Join(src, "configure"),
		"--host="+libxml2Host(t), "--prefix="+dest,
		"CC="+cc, "AR="+ar, "RANLIB="+ranlib, "CFLAGS=-O2 -fPIC",
		"--enable-static", "--disable-shared", "--with-minimum", "--with-threads", "--without-python",
	); err != nil {
		return "", fmt.Errorf("libxml2 configure: %w", err)
	}
	if t.os == "windows" {
		if err := fixLibtool(dir); err != nil {
			return "", err
		}
	}
	if err := run(dir, env, "make", fmt.Sprintf("-j%d", b.jobs)); err != nil {
		return "", fmt.Errorf("libxml2 make: %w", err)
	}
	if err := run(dir, env, "make", "install"); err != nil {
		return "", fmt.Errorf("libxml2 install: %w", err)
	}
	if t.os == "windows" {
		if err := os.Rename(filepath.Join(dest, "lib", "libxml2.lib"), lib); err != nil {
			return "", err
		}
	}
	return dest, nil
}

// fixLibtool makes the libtool an autotools configure wrote in dir for
// Windows archive with Zig's ar: it takes Zig's MinGW compiler for MSVC's
// and archives with lib.exe.
func fixLibtool(dir string) error {
	return replaceInFile(filepath.Join(dir, "libtool"), `old_archive_cmds="lib -OUT:\$oldlib\$oldobjs\$old_deplibs"`, `old_archive_cmds="\$AR \$AR_FLAGS \$oldlib\$oldobjs~\$RANLIB \$tool_oldlib"`)
}

// buildEncoderLibs builds the libraries of the encoders FFmpeg links
// statically for t, with the C compiler cc and the C++ one cxx, and
// returns what the pkg-config wrapper answers for them.
func (b *builder) buildEncoderLibs(t target, cc, cxx string, env []string) ([]string, error) {
	opus, err := b.buildOpus(t, cc, env)
	if err != nil {
		return nil, err
	}
	vpx, err := b.buildLibvpx(t, cc, cxx, env)
	if err != nil {
		return nil, err
	}
	return append(opusEnv(t, opus), libvpxEnv(t, vpx)...), nil
}

// buildOpus compiles libopus into a static library for t with cc.
func (b *builder) buildOpus(t target, cc string, env []string) (string, error) {
	src, err := b.unpack(opusSource, strings.TrimSuffix(opusSource.name, ".tar.gz"))
	if err != nil {
		return "", err
	}
	dest := filepath.Join(b.work, "deps", t.name, "opus")
	if _, err := os.Stat(filepath.Join(dest, "lib", "libopus.a")); err == nil {
		return dest, nil
	}
	dir := filepath.Join(b.work, "build", t.name+"-opus")
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ar, ranlib := "ar", "ranlib"
	if t.zig != "" {
		ar, ranlib = b.zig+" ar", b.zig+" ranlib"
		env = append(slices.Clone(env), "ZIG_GLOBAL_CACHE_DIR="+filepath.Join(b.work, "zig-cache"))
	}
	if err := run(dir, env, filepath.Join(src, "configure"),
		"--host="+libxml2Host(t), "--prefix="+dest,
		"CC="+cc, "AR="+ar, "RANLIB="+ranlib, "CFLAGS=-O2 -fPIC",
		"--enable-static", "--disable-shared", "--disable-doc", "--disable-extra-programs",
	); err != nil {
		return "", fmt.Errorf("opus configure: %w", err)
	}
	if t.os == "windows" {
		if err := fixLibtool(dir); err != nil {
			return "", err
		}
	}
	if err := run(dir, env, "make", fmt.Sprintf("-j%d", b.jobs)); err != nil {
		return "", fmt.Errorf("opus make: %w", err)
	}
	if err := run(dir, env, "make", "install"); err != nil {
		return "", fmt.Errorf("opus install: %w", err)
	}
	if t.os == "windows" {
		if err := os.Rename(filepath.Join(dest, "lib", "libopus.lib"), filepath.Join(dest, "lib", "libopus.a")); err != nil {
			return "", err
		}
	}
	return dest, nil
}

func opusEnv(t target, dir string) []string {
	libs := "-L" + filepath.Join(dir, "lib") + " -lopus"
	if t.os == "linux" {
		libs += " -lm"
	}
	return []string{"NSGO_MEDIA_OPUS_CFLAGS=-I" + filepath.Join(dir, "include", "opus"), "NSGO_MEDIA_OPUS_LIBS=" + libs}
}

// libvpxTarget is the target libvpx's configure takes for t. Apple's arm64
// targets take its generic arm64 one, which compiles with the compiler as
// given: its Darwin ones choose their own compiler and SDK and know no
// arm64 simulator. Its CPU detection follows the compiler's platform.
func libvpxTarget(t target) string {
	switch {
	case t.os == "darwin" && t.arch == "arm64":
		return "arm64-linux-gcc"
	case t.os == "darwin":
		return "x86_64-darwin20-gcc"
	case t.os == "windows":
		return "x86_64-win64-gcc"
	}
	return "x86_64-linux-gcc"
}

// buildLibvpx compiles libvpx's VP9 encoder into a static library for t
// with cc and cxx, assembling x86 code with nasm. Its configure checks
// nasm's section alignment with readelf, which the tool stands in for.
func (b *builder) buildLibvpx(t target, cc, cxx string, env []string) (string, error) {
	src, err := b.unpack(libvpxSource, strings.TrimSuffix(libvpxSource.name, ".tar.gz"))
	if err != nil {
		return "", err
	}
	dest := filepath.Join(b.work, "deps", t.name, "libvpx")
	if _, err := os.Stat(filepath.Join(dest, "lib", "libvpx.a")); err == nil {
		return dest, nil
	}
	if t.arch == "x86_64" {
		if err := b.ensureNasm(); err != nil {
			return "", err
		}
	}
	bin := filepath.Join(b.work, "host", "bin")
	if err := os.WriteFile(filepath.Join(bin, "readelf"), []byte("#!/bin/sh\nexec "+b.tool+" -as readelf \"$@\"\n"), 0o755); err != nil {
		return "", err
	}
	dir := filepath.Join(b.work, "build", t.name+"-libvpx")
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ar := "ar"
	if t.zig != "" {
		ar = b.zig + " ar"
	}
	env = append(slices.Clone(env),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CC="+cc, "CXX="+cxx, "LD="+cxx, "AR="+ar,
		"ZIG_GLOBAL_CACHE_DIR="+filepath.Join(b.work, "zig-cache"),
	)
	if err := run(dir, env, filepath.Join(src, "configure"),
		"--target="+libvpxTarget(t), "--prefix="+dest, "--as=nasm",
		"--disable-vp8", "--disable-vp9-decoder", "--enable-pic", "--enable-static", "--disable-shared",
		"--disable-examples", "--disable-tools", "--disable-docs", "--disable-unit-tests", "--disable-install-bins",
	); err != nil {
		return "", fmt.Errorf("libvpx configure: %w", err)
	}
	if err := run(dir, env, "make", fmt.Sprintf("-j%d", b.jobs)); err != nil {
		return "", fmt.Errorf("libvpx make: %w", err)
	}
	if err := run(dir, env, "make", "install"); err != nil {
		return "", fmt.Errorf("libvpx install: %w", err)
	}
	return dest, nil
}

func libvpxEnv(t target, dir string) []string {
	libs := "-L" + filepath.Join(dir, "lib") + " -lvpx"
	if t.os == "linux" {
		libs += " -lm -lpthread"
	}
	return []string{"NSGO_MEDIA_VPX_CFLAGS=-I" + filepath.Join(dir, "include"), "NSGO_MEDIA_VPX_LIBS=" + libs}
}

func libxml2Host(t target) string {
	switch t.os {
	case "darwin":
		return t.arch + "-apple-darwin"
	case "windows":
		return "x86_64-w64-mingw32"
	}
	return "x86_64-linux-gnu"
}

// libxml2Env is what the pkg-config wrapper answers for libxml-2.0 from
// the library buildLibxml2 made in dir.
func libxml2Env(t target, dir string) []string {
	inc := filepath.Join(dir, "include")
	cflags := "-I" + inc + " -I" + filepath.Join(inc, "libxml2")
	libs := "-L" + filepath.Join(dir, "lib") + " -lxml2"
	switch t.os {
	case "windows":
		cflags += " -DLIBXML_STATIC"
		libs += " -lbcrypt"
	case "linux":
		libs += " -lm"
	}
	return []string{"NSGO_MEDIA_LIBXML2_CFLAGS=" + cflags, "NSGO_MEDIA_LIBXML2_LIBS=" + libs}
}

// unpackLibva extracts the libva packages into a root of their files.
func (b *builder) unpackLibva() (string, error) {
	root := filepath.Join(b.work, "deps", "libva")
	if _, err := os.Stat(filepath.Join(root, "usr", "include", "va", "va.h")); err == nil {
		return root, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	for _, d := range libvaPackages {
		path, err := b.fetch(d)
		if err != nil {
			return "", err
		}
		if err := extractDeb(path, root); err != nil {
			return "", fmt.Errorf("%s: %w", d.name, err)
		}
	}
	return root, nil
}

func (b *builder) unpackNvCodecHeaders() (string, error) {
	return b.unpack(nvCodecHeadersSource, "nv-codec-headers-n12.1.14.1")
}

func (b *builder) unpackAMFHeaders() (string, error) {
	return b.unpack(amfHeadersSource, "amf-headers-v1.5.3")
}

// buildLibvpl compiles Intel's libvpl dispatcher into a static library for
// Windows with Zig's C++ compiler, avoiding CMake.
func (b *builder) buildLibvpl(t target) (string, error) {
	src, err := b.unpack(libvplSource, strings.TrimSuffix(libvplSource.name, ".tar.gz"))
	if err != nil {
		return "", err
	}
	dest := filepath.Join(b.work, "deps", t.name, "libvpl")
	lib := filepath.Join(dest, "lib", "libvpl.a")
	if _, err := os.Stat(lib); err == nil {
		return dest, nil
	}
	for _, d := range []string{"include/vpl", "lib", "obj"} {
		if err := os.MkdirAll(filepath.Join(dest, d), 0o755); err != nil {
			return "", err
		}
	}
	sources := []string{
		"libvpl/src/windows/main.cpp",
		"libvpl/src/windows/mfx_critical_section.cpp",
		"libvpl/src/windows/mfx_dispatcher.cpp",
		"libvpl/src/windows/mfx_dispatcher_log.cpp",
		"libvpl/src/windows/mfx_driver_store_loader.cpp",
		"libvpl/src/windows/mfx_dxva2_device.cpp",
		"libvpl/src/windows/mfx_function_table.cpp",
		"libvpl/src/windows/mfx_library_iterator.cpp",
		"libvpl/src/windows/mfx_load_dll.cpp",
		"libvpl/src/windows/mfx_win_reg_key.cpp",
		"libvpl/src/mfx_dispatcher_vpl.cpp",
		"libvpl/src/mfx_dispatcher_vpl_loader.cpp",
		"libvpl/src/mfx_dispatcher_vpl_config.cpp",
		"libvpl/src/mfx_dispatcher_vpl_lowlatency.cpp",
		"libvpl/src/mfx_dispatcher_vpl_log.cpp",
		"libvpl/src/mfx_dispatcher_vpl_msdk.cpp",
		"libvpl/src/mfx_config_interface/mfx_config_interface.cpp",
		"libvpl/src/mfx_config_interface/mfx_config_interface_string_api.cpp",
	}
	env := append(os.Environ(), "ZIG_GLOBAL_CACHE_DIR="+filepath.Join(b.work, "zig-cache"))
	var objs []string
	for i, f := range sources {
		obj := filepath.Join(dest, "obj", fmt.Sprintf("%02d-%s.o", i, strings.TrimSuffix(filepath.Base(f), ".cpp")))
		if err := run(src, env, b.zig, "c++", "-target", t.zig, "-O2", "-std=c++11",
			"-DMFX_DEPRECATED_OFF", "-D_UNICODE", "-DUNICODE", "-DNOMINMAX",
			"-I"+filepath.Join(src, "api"),
			"-I"+filepath.Join(src, "api", "vpl"),
			"-I"+filepath.Join(src, "libvpl"),
			"-I"+filepath.Join(src, "libvpl", "src"),
			"-I"+filepath.Join(src, "libvpl", "src", "windows"),
			"-c", f, "-o", obj); err != nil {
			return "", fmt.Errorf("libvpl %s: %w", f, err)
		}
		objs = append(objs, obj)
	}
	if err := run(src, env, b.zig, append([]string{"ar", "rcs", lib}, objs...)...); err != nil {
		return "", fmt.Errorf("libvpl ar: %w", err)
	}
	hdrs, err := os.ReadDir(filepath.Join(src, "api", "vpl"))
	if err != nil {
		return "", err
	}
	for _, h := range hdrs {
		if h.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(src, "api", "vpl", h.Name()), filepath.Join(dest, "include", "vpl", h.Name())); err != nil {
			return "", err
		}
	}
	return dest, nil
}

func libvplEnv(dir string) []string {
	inc := filepath.Join(dir, "include")
	cflags := "-I" + inc + " -I" + filepath.Join(inc, "vpl")
	libs := "-L" + filepath.Join(dir, "lib") + " -lvpl -lc++ -lole32 -ladvapi32 -lcfgmgr32 -ldxgi -ld3d11 -ldxva2"
	return []string{"NSGO_MEDIA_VPL_CFLAGS=" + cflags, "NSGO_MEDIA_VPL_LIBS=" + libs}
}

// buildOpenH264 compiles OpenH264 into a static library for Linux x64.
func (b *builder) buildOpenH264(t target) (string, error) {
	src, err := b.unpack(openh264Source, strings.TrimSuffix(openh264Source.name, ".tar.gz"))
	if err != nil {
		return "", err
	}
	dest := filepath.Join(b.work, "deps", t.name, "openh264")
	lib := filepath.Join(dest, "lib", "libopenh264.a")
	if _, err := os.Stat(lib); err == nil {
		return dest, nil
	}
	for _, d := range []string{"include/wels", "lib"} {
		if err := os.MkdirAll(filepath.Join(dest, d), 0o755); err != nil {
			return "", err
		}
	}
	dir := filepath.Join(b.work, "build", t.name+"-openh264")
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	env := append(os.Environ(), "ZIG_GLOBAL_CACHE_DIR="+filepath.Join(b.work, "zig-cache"))
	if err := run(dir, env, "make", "-f", filepath.Join(src, "Makefile"), fmt.Sprintf("-j%d", b.jobs),
		"OS=linux", "ARCH=x86_64", "USE_ASM=Yes", "ASM="+b.nasm,
		"CC="+b.zig+" cc -target "+t.zig,
		"CXX="+b.zig+" c++ -target "+t.zig,
		"AR="+b.zig+" ar",
		"AR_OPTS=rcs $@",
		"libopenh264.a",
	); err != nil {
		return "", fmt.Errorf("openh264 make: %w", err)
	}
	if err := copyFile(filepath.Join(dir, "libopenh264.a"), lib); err != nil {
		return "", err
	}
	hdrs, err := os.ReadDir(filepath.Join(src, "codec", "api", "wels"))
	if err != nil {
		return "", err
	}
	for _, h := range hdrs {
		if h.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(src, "codec", "api", "wels", h.Name()), filepath.Join(dest, "include", "wels", h.Name())); err != nil {
			return "", err
		}
	}
	return dest, nil
}

func openh264Env(dir string) []string {
	cflags := "-I" + filepath.Join(dir, "include")
	libs := "-L" + filepath.Join(dir, "lib") + " -lopenh264 -lc++ -lpthread -lm"
	return []string{"NSGO_MEDIA_OPENH264_CFLAGS=" + cflags, "NSGO_MEDIA_OPENH264_LIBS=" + libs}
}

// extractDeb extracts a Debian package's data archive into root. A .deb is
// an ar archive holding data.tar.* among its members.
func extractDeb(path, root string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(data), "!<arch>\n") {
		return fmt.Errorf("not an ar archive")
	}
	for off := 8; off+60 <= len(data); {
		hdr := data[off : off+60]
		name := strings.TrimRight(strings.TrimSpace(string(hdr[:16])), "/")
		var size int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(hdr[48:58])), "%d", &size); err != nil {
			return err
		}
		off += 60
		if off+size > len(data) {
			return fmt.Errorf("truncated member %s", name)
		}
		if strings.HasPrefix(name, "data.tar") {
			tmp := filepath.Join(root, name)
			if err := os.WriteFile(tmp, data[off:off+size], 0o644); err != nil {
				return err
			}
			defer os.Remove(tmp)
			return run(root, nil, "tar", "-xf", tmp)
		}
		off += size + size%2
	}
	return fmt.Errorf("no data archive")
}

func run(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func xcrunFind(tool string) (string, error) {
	out, err := exec.Command("xcrun", "-f", tool).Output()
	if err != nil {
		return "", fmt.Errorf("xcrun -f %s: %w; install Xcode", tool, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func hostArch() string {
	out, _ := exec.Command("uname", "-m").Output()
	return strings.TrimSpace(string(out))
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func replaceInFile(path, old, new string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o644)
}
