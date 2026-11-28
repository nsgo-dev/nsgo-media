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

// targets are the iOS targets bundle serves; nsgo build itself puts the
// libraries nsgo-natives.yaml declares into a desktop app's packages.
var targets = map[string]bool{"ios": true, "ios_simulator": true}

// libSet is one target's archive: its frameworks' files by path, and its
// notices by file name: the licenses, the diff of FFmpeg's source and the
// note of how they were built.
type libSet struct {
	libs    map[string][]byte
	notices map[string][]byte
}

// requiredNotices are the notices an archive must hold: the LGPL asks for
// the library's license and how it was built to go with it.
var requiredNotices = []string{"COPYING.LGPLv2.1", "BUILD.txt"}

// source finds the archives tools/ffmpeg built in dir.
type source struct {
	dir   string
	cache map[string]*libSet
}

// ffmpegVersion is the version in the archives' names, FFmpegVersion in
// tools/ffmpeg/pinned.go.
const ffmpegVersion = "9.0.2"

const modulePath = "github.com/nsgo-dev/nsgo-media"

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

var libraryName = regexp.MustCompile(`^lib[a-z]+\.framework/(lib[a-z]+|Info\.plist)$`)

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
	set := &libSet{libs: map[string][]byte{}, notices: map[string][]byte{}}
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
		case !strings.ContainsAny(f.Name, "/\\") && !f.FileInfo().IsDir():
			set.notices[f.Name] = b
		}
	}
	if len(set.libs) == 0 {
		return nil, fmt.Errorf("%s holds no libraries", archiveName(target))
	}
	for _, name := range requiredNotices {
		if set.notices[name] == nil {
			return nil, fmt.Errorf("%s has no %s, which the LGPL asks to ship with the libraries; %s", archiveName(target), name, buildHint)
		}
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
		fmt.Printf("skipping %s: not an iOS package; nsgo build puts the FFmpeg libraries into desktop packages\n", pkg)
		return nil
	}
	if !targets[target] {
		return fmt.Errorf("unknown target %s; bundle serves ios and ios_simulator", target)
	}
	if app == "" {
		apps, _ := filepath.Glob(filepath.Join(pkg, "*.app"))
		if len(apps) != 1 {
			return errors.New("no .app in the package")
		}
		app = apps[0]
	}
	return bundleIOS(app, target, sign, src)
}

// writeNotices writes every notice of set into dir.
func writeNotices(dir string, set *libSet) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, data := range set.notices {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// bundleIOS puts the frameworks into an iOS app's Frameworks, where the
// loader looks, and the notices into its FFmpeg directory. A signed app,
// as a simulator's always is, is signed again with its identity, the
// frameworks first; an unsigned device app leaves them to the signing
// that installs it, as NSGo's leaves its own. The IPA beside the app is
// written again from it.
func bundleIOS(app, target, sign string, src *source) error {
	set, err := src.load(target)
	if err != nil {
		return err
	}
	frameworks := map[string]bool{}
	for name, data := range set.libs {
		out := filepath.Join(app, "Frameworks", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o755)
		if strings.HasSuffix(name, ".plist") {
			mode = 0o644
		}
		if err := os.WriteFile(out, data, mode); err != nil {
			return err
		}
		frameworks[filepath.Dir(out)] = true
	}
	if err := writeNotices(filepath.Join(app, "FFmpeg"), set); err != nil {
		return err
	}
	identity, signed := signature(app)
	if sign != "" {
		identity, signed = sign, true
	}
	if target == "ios_simulator" {
		signed = true
	}
	how := "left unsigned for the app's signing"
	if signed {
		for fw := range frameworks {
			if err := run("codesign", "--force", "--sign", identity, fw); err != nil {
				return err
			}
		}
		if err := run("codesign", "--force", "--sign", identity, "--preserve-metadata=identifier,entitlements,requirements,flags", app); err != nil {
			return err
		}
		if err := run("codesign", "--verify", "--deep", "--strict", app); err != nil {
			return err
		}
		how = fmt.Sprintf("signed with %q", identity)
	}
	ipa := filepath.Join(filepath.Dir(app), strings.TrimSuffix(filepath.Base(app), ".app")+".ipa")
	if _, err := os.Stat(ipa); err == nil {
		if err := writeIPA(app, ipa); err != nil {
			return err
		}
	}
	fmt.Printf("bundled FFmpeg into %s, %s\n", app, how)
	return nil
}

// writeIPA packs app as an IPA at dst: a zip of Payload/<name>.app.
func writeIPA(app, dst string) error {
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	zw := zip.NewWriter(f)
	prefix := "Payload/" + filepath.Base(app)
	err = filepath.WalkDir(app, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(app, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = prefix + "/" + filepath.ToSlash(rel)
		if d.IsDir() {
			h.Name = strings.TrimSuffix(h.Name, "/.") + "/"
			_, err = zw.CreateHeader(h)
			return err
		}
		h.Method = zip.Deflate
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err == nil {
		err = zw.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// signature is the identity the app is signed with, "-" for ad hoc, and
// whether it is signed at all.
func signature(app string) (identity string, signed bool) {
	out, err := exec.Command("codesign", "-dvv", app).CombinedOutput()
	if err != nil {
		return "-", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Authority=") {
			return strings.TrimPrefix(line, "Authority="), true
		}
	}
	return "-", true
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
