package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var targets = map[string]bool{
	"macos_arm64": true, "macos_x64": true, "macos_universal": true,
	"windows_x64": true, "linux_x64": true,
}

// libSet is one target's archive: its libraries by file name, FFmpeg's
// license and the note of how they were built.
type libSet struct {
	libs    map[string][]byte
	license []byte
	note    []byte
}

// source finds the archives tools/ffmpeg built in dir.
type source struct {
	dir   string
	cache map[string]*libSet
}

// ffmpegVersion is the version in the archives' names, FFmpegVersion in
// tools/ffmpeg/pinned.go.
const ffmpegVersion = "9.0.2"

const modulePath = "github.com/oneo-me/nsgo-media"

const buildHint = "build them in NSGo Media's directory with go run ./tools/ffmpeg, or name their directory with -libs"

// defaultLibs is where tools/ffmpeg writes its archives: dist/ffmpeg in the
// current directory if it is there, as in NSGo Media's own checkout, or else
// in the directory of the NSGo Media module the current module uses, such
// as a local checkout a replace directive names.
func defaultLibs() string {
	local := filepath.Join("dist", "ffmpeg")
	if st, err := os.Stat(local); err == nil && st.IsDir() {
		return local
	}
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", modulePath).Output()
	if dir := strings.TrimSpace(string(out)); err == nil && dir != "" {
		return filepath.Join(dir, "dist", "ffmpeg")
	}
	return local
}

var libraryName = regexp.MustCompile(`^(lib[a-z]+\.\d+\.dylib|[a-z]+-\d+\.dll|lib[a-z]+\.so\.\d+)$`)

func archiveName(target string) string {
	return "ffmpeg-" + ffmpegVersion + "-" + target + ".zip"
}

func (s *source) load(target string) (*libSet, error) {
	if set := s.cache[target]; set != nil {
		return set, nil
	}
	data, err := s.archive(target)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	set := &libSet{libs: map[string][]byte{}}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		switch {
		case libraryName.MatchString(f.Name):
			set.libs[f.Name] = b
		case f.Name == "COPYING.LGPLv2.1":
			set.license = b
		case f.Name == "BUILD.txt":
			set.note = b
		}
	}
	if len(set.libs) == 0 {
		return nil, fmt.Errorf("%s holds no libraries", archiveName(target))
	}
	if s.cache == nil {
		s.cache = map[string]*libSet{}
	}
	s.cache[target] = set
	return set, nil
}

func (s *source) archive(target string) ([]byte, error) {
	path := filepath.Join(s.dir, archiveName(target))
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no FFmpeg libraries for %s: %s is missing; %s", target, path, buildHint)
	}
	return data, err
}

// packageTarget is the target at the end of a package directory's name,
// <name>-<version>-<target>.
func packageTarget(dir string) string {
	base := filepath.Base(dir)
	if i := strings.LastIndexByte(base, '-'); i >= 0 && targets[base[i+1:]] {
		return base[i+1:]
	}
	return ""
}

func bundle(pkg, target, sign string, src *source) error {
	pkg = filepath.Clean(pkg)
	st, err := os.Stat(pkg)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return nil // a file beside the packages, such as a checksum
	}
	app := ""
	if strings.HasSuffix(pkg, ".app") {
		app, pkg = pkg, filepath.Dir(pkg)
	}
	if target == "" {
		target = packageTarget(pkg)
	}
	if target == "" {
		if app == "" {
			fmt.Printf("skipping %s: not a package of a desktop target\n", pkg)
			return nil
		}
		target = "macos_universal"
	}
	if !targets[target] {
		return fmt.Errorf("unknown target %s", target)
	}
	if strings.HasPrefix(target, "macos") {
		if app == "" {
			apps, _ := filepath.Glob(filepath.Join(pkg, "*.app"))
			if len(apps) != 1 {
				return errors.New("no .app in the package")
			}
			app = apps[0]
		}
		return bundleMac(app, target, sign, src)
	}
	set, err := src.load(target)
	if err != nil {
		return err
	}
	for name, data := range set.libs {
		if err := os.WriteFile(filepath.Join(pkg, name), data, 0o755); err != nil {
			return err
		}
	}
	if err := writeNotices(filepath.Join(pkg, "FFmpeg"), set); err != nil {
		return err
	}
	fmt.Printf("bundled FFmpeg into %s\n", pkg)
	return nil
}

func writeNotices(dir string, set *libSet) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "COPYING.LGPLv2.1"), set.license, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "BUILD.txt"), set.note, 0o644)
}

// bundleMac puts the libraries into the app's Frameworks, joining both
// architectures' for a universal app, and signs them and the app again,
// keeping the app's identifier, entitlements and options.
func bundleMac(app, target, sign string, src *source) error {
	frameworks := filepath.Join(app, "Contents", "Frameworks")
	if err := os.MkdirAll(frameworks, 0o755); err != nil {
		return err
	}
	var libs []string
	if target == "macos_universal" {
		arm, err := src.load("macos_arm64")
		if err != nil {
			return err
		}
		x64, err := src.load("macos_x64")
		if err != nil {
			return err
		}
		tmp, err := os.MkdirTemp("", "nsgo-media-lipo")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		for name, a := range arm.libs {
			b, ok := x64.libs[name]
			if !ok {
				return fmt.Errorf("macos_x64 has no %s", name)
			}
			pa, pb := filepath.Join(tmp, "arm64-"+name), filepath.Join(tmp, "x64-"+name)
			os.WriteFile(pa, a, 0o755)
			os.WriteFile(pb, b, 0o755)
			out := filepath.Join(frameworks, name)
			if err := run("lipo", "-create", pa, pb, "-output", out); err != nil {
				return err
			}
			libs = append(libs, out)
		}
		if err := writeNotices(filepath.Join(app, "Contents", "Resources", "FFmpeg"), arm); err != nil {
			return err
		}
	} else {
		set, err := src.load(target)
		if err != nil {
			return err
		}
		for name, data := range set.libs {
			out := filepath.Join(frameworks, name)
			if err := os.WriteFile(out, data, 0o755); err != nil {
				return err
			}
			libs = append(libs, out)
		}
		if err := writeNotices(filepath.Join(app, "Contents", "Resources", "FFmpeg"), set); err != nil {
			return err
		}
	}
	if sign == "" {
		sign = signingIdentity(app)
	}
	args := []string{"--force", "--sign", sign}
	if sign != "-" {
		args = append(args, "--timestamp", "--options", "runtime")
	}
	for _, lib := range libs {
		if err := run("codesign", append(args, lib)...); err != nil {
			return err
		}
	}
	appArgs := []string{"--force", "--sign", sign, "--preserve-metadata=identifier,entitlements,requirements,flags,runtime"}
	if sign != "-" {
		appArgs = append(appArgs, "--timestamp")
	}
	if err := run("codesign", append(appArgs, app)...); err != nil {
		return err
	}
	if err := run("codesign", "--verify", "--deep", "--strict", app); err != nil {
		return err
	}
	fmt.Printf("bundled FFmpeg into %s, signed with %q\n", app, sign)
	return nil
}

// signingIdentity is the identity the app is signed with, "-" for ad hoc.
func signingIdentity(app string) string {
	out, _ := exec.Command("codesign", "-dvv", app).CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Authority=") {
			return strings.TrimPrefix(line, "Authority=")
		}
	}
	return "-"
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
