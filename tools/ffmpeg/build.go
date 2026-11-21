package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/format"
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
	return b, nil
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
		args = append(args,
			"--cc="+clang+" -arch "+t.arch,
			"--extra-cflags=-mmacosx-version-min="+macOSMin,
			"--extra-ldflags=-mmacosx-version-min="+macOSMin+" -Wl,-rpath,@loader_path",
		)
		if t.arch != hostArch() {
			args = append(args, "--enable-cross-compile")
		}
		sdk, err := exec.Command("xcrun", "--show-sdk-path").Output()
		if err != nil {
			return nil, nil, fmt.Errorf("xcrun --show-sdk-path: %w; install Xcode", err)
		}
		return args, append(env, "SDKROOT="+strings.TrimSpace(string(sdk))), nil
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
	if t.os == "linux" {
		root, err := b.unpackLibva()
		if err != nil {
			return nil, nil, err
		}
		env = append(env, "NSGO_MEDIA_PKG_ROOT="+root)
	}
	args = append(args,
		"--enable-cross-compile",
		"--cc="+b.tool+" -as cc",
		"--ar="+b.tool+" -as ar",
		"--ranlib="+b.tool+" -as ranlib",
		"--strip=true",
		"--extra-cflags=-I"+filepath.Join(zlib, "include"),
		"--extra-ldflags=-L"+filepath.Join(zlib, "lib"),
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

// pack zips t's libraries with FFmpeg's license and a note of how they
// were built, which the LGPL asks to go with them.
func (b *builder) pack(t target, dir string, args []string) (string, error) {
	name := filepath.Join(b.out, "ffmpeg-"+FFmpegVersion+"-"+t.name+".zip")
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
		if err := addFile(zw, entries[i].Name(), path, 0o755); err != nil {
			return "", err
		}
	}
	if err := addFile(zw, "COPYING.LGPLv2.1", filepath.Join(b.ffmpeg, "COPYING.LGPLv2.1"), 0o644); err != nil {
		return "", err
	}
	note := fmt.Sprintf("FFmpeg %s, built for %s by NSGo Media's tools/ffmpeg under the LGPL 2.1 or later.\n\nSource: %s\nSHA-256: %s\n\nConfigured with:\n  %s\n",
		FFmpegVersion, t.name, ffmpegSource.url, ffmpegSource.sha256, strings.Join(args, "\n  "))
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

// writeChecksums writes SHA256SUMS over the archives in out.
func writeChecksums(out string) (string, error) {
	names, err := filepath.Glob(filepath.Join(out, "ffmpeg-*.zip"))
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, n := range names {
		sum, err := fileSHA256(n)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, "%s  %s\n", sum, filepath.Base(n))
	}
	path := filepath.Join(out, "SHA256SUMS")
	return path, os.WriteFile(path, []byte(sb.String()), 0o644)
}

func uploadRelease(tag string, files []string) error {
	args := append([]string{"release", "upload", tag, "--clobber"}, files...)
	return run("", nil, "gh", args...)
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

// writePins records the uploaded release and its archives' checksums in
// the bundle command, which downloads them for apps.
func writePins(tag, out, path string) error {
	names, err := filepath.Glob(filepath.Join(out, "ffmpeg-"+FFmpegVersion+"-*.zip"))
	if err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("// Code generated by go run ./tools/ffmpeg -upload; DO NOT EDIT.\n\npackage main\n\n")
	sb.WriteString("// ffmpegVersion, releaseTag and releaseSums name the libraries bundle\n// downloads: the archives of this FFmpeg version uploaded to this release\n// of NSGo Media, by target. An empty tag means none is uploaded yet.\n")
	fmt.Fprintf(&sb, "const ffmpegVersion = %q\n\nconst releaseTag = %q\n\nvar releaseSums = map[string]string{\n", FFmpegVersion, tag)
	for _, n := range names {
		sum, err := fileSHA256(n)
		if err != nil {
			return err
		}
		target := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(n), "ffmpeg-"+FFmpegVersion+"-"), ".zip")
		fmt.Fprintf(&sb, "\t%q: %q,\n", target, sum)
	}
	sb.WriteString("}\n")
	code, err := format.Source([]byte(sb.String()))
	if err != nil {
		return err
	}
	fmt.Printf("pinned %s in %s; commit it\n", tag, path)
	return os.WriteFile(path, code, 0o644)
}
