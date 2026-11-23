package plugin

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// data is what a plugin keeps across runs in its data folder: its storage
// and the viewer's settings.
type data struct {
	dir string

	mu       sync.Mutex
	storage  map[string]json.RawMessage
	settings map[string]any
}

const (
	storageFile  = "storage.json"
	settingsFile = "settings.json"
)

// openData reads the data folder dir; "" keeps the data in memory.
func openData(dir string) (*data, error) {
	d := &data{dir: dir, storage: map[string]json.RawMessage{}, settings: map[string]any{}}
	if dir == "" {
		return d, nil
	}
	if err := d.read(storageFile, &d.storage); err != nil {
		return nil, err
	}
	if err := d.read(settingsFile, &d.settings); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *data) read(name string, v any) error {
	raw, err := os.ReadFile(filepath.Join(d.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// write replaces the file name with v's JSON; d.mu is held.
func (d *data) write(name string, v any) error {
	if d.dir == "" {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d.dir, name+".*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(raw)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(d.dir, name))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func (d *data) get(key string) (json.RawMessage, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.storage[key]
	return v, ok
}

// set stores value under key; nil removes it.
func (d *data) set(key string, value json.RawMessage) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if value == nil {
		delete(d.storage, key)
	} else {
		d.storage[key] = value
	}
	return d.write(storageFile, d.storage)
}

func (d *data) setting(key string) any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.settings[key]
}

func (d *data) setSetting(key string, value any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.settings[key] = value
	return d.write(settingsFile, d.settings)
}
