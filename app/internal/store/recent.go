package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"
	"sync"
	"time"
)

// maxRecent bounds the videos the recent list keeps.
const maxRecent = 50

// Recent keeps the videos of plugins played last, newest first, in one
// JSON file, so the home page can offer to continue them. Each is the
// item its plugin answered, kept as it came. Its methods may be called
// from any goroutine; Add writes the file before returning, so call it
// off the engine's goroutine.
type Recent struct {
	path    string
	mu      sync.Mutex
	entries []Played
}

// Played is a video played: the plugin's id, the video's item as JSON and
// when it was played.
type Played struct {
	Plugin string          `json:"plugin"`
	Video  string          `json:"video"`
	Item   json.RawMessage `json:"item"`
	Played time.Time       `json:"played"`
}

// OpenRecent reads the list kept at path. A missing file holds none; an
// unreadable one is reported and replaced at the next write.
func OpenRecent(path string) (*Recent, error) {
	r := &Recent{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &r.entries)
	}
	if err != nil {
		r.entries = nil
	}
	return r, err
}

// Add puts the video of plugin first, its item as item.
func (r *Recent) Add(plugin, video string, item json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = slices.DeleteFunc(r.entries, func(p Played) bool { return p.Plugin == plugin && p.Video == video })
	r.entries = slices.Insert(r.entries, 0, Played{Plugin: plugin, Video: video, Item: item, Played: time.Now()})
	if len(r.entries) > maxRecent {
		r.entries = r.entries[:maxRecent]
	}
	if r.path == "" {
		return nil
	}
	data, err := json.Marshal(r.entries)
	if err != nil {
		return err
	}
	return writeFile(r.path, data)
}

// List is the videos played, newest first.
func (r *Recent) List() []Played {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.entries)
}
