package store

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// MaxPluginSize bounds the files of a plugin being installed, as a plugin
// is one script with its manifest and translations.
const MaxPluginSize = 64 << 20

// ErrInstalled refuses to install a plugin over one in the same folder.
var ErrInstalled = errors.New("a plugin with this id is installed")

// InstallPlugin copies the regular files of src into the folder name of
// dir, the plugins folder. A failed copy leaves nothing behind.
func InstallPlugin(dir, name string, src fs.FS) error {
	dst := filepath.Join(dir, name)
	if _, err := os.Lstat(dst); err == nil {
		return ErrInstalled
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(dir, "."+name+".*")
	if err != nil {
		return err
	}
	if err := copyFS(tmp, src); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

func copyFS(dst string, src fs.FS) error {
	var size int64
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := src.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(in, MaxPluginSize-size+1))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if size += n; err == nil && size > MaxPluginSize {
			err = fmt.Errorf("the plugin is larger than %d MB", MaxPluginSize>>20)
		}
		return err
	})
}

// RemovePlugin removes the folder name of dir, the plugins folder, and
// the plugin's data folder data.
func RemovePlugin(dir, name, data string) error {
	err := os.RemoveAll(filepath.Join(dir, name))
	if data != "" {
		err = errors.Join(err, os.RemoveAll(data))
	}
	return err
}
