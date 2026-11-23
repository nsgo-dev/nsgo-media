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

// maxPositions bounds the file: the least recently played are forgotten.
const maxPositions = 1000

// Positions keeps where each video was left, in one JSON file, so
// reopening it can offer to resume there. Its methods may be called from
// any goroutine; Set and Remove write the file before returning, so call
// them off the engine's goroutine.
type Positions struct {
	path    string
	mu      sync.Mutex
	entries map[string]position
}

type position struct {
	Position time.Duration `json:"position"`
	Played   time.Time     `json:"played"`
}

// OpenPositions reads the positions kept at path. A missing file holds
// none; an unreadable one is reported and replaced at the next write.
func OpenPositions(path string) (*Positions, error) {
	p := &Positions{path: path, entries: map[string]position{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &p.entries)
	}
	if err != nil {
		p.entries = map[string]position{}
	}
	return p, err
}

// Get returns where the video key was left.
func (p *Positions) Get(key string) (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[key]
	return e.Position, ok
}

// Set records that the video key was left at pos.
func (p *Positions) Set(key string, pos time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries[key] = position{Position: pos, Played: time.Now()}
	p.prune()
	return p.write()
}

// Remove forgets the video key, as one played to its end.
func (p *Positions) Remove(key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.entries[key]; !ok {
		return nil
	}
	delete(p.entries, key)
	return p.write()
}

// prune forgets the least recently played beyond maxPositions.
func (p *Positions) prune() {
	if len(p.entries) <= maxPositions {
		return
	}
	keys := make([]string, 0, len(p.entries))
	for k := range p.entries {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return p.entries[b].Played.Compare(p.entries[a].Played) })
	for _, k := range keys[maxPositions:] {
		delete(p.entries, k)
	}
}

// write replaces the file whole.
func (p *Positions) write() error {
	data, err := json.Marshal(p.entries)
	if err != nil {
		return err
	}
	return writeFile(p.path, data)
}
