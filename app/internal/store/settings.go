package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"
	"sync"
)

// Settings are the app's own settings in one JSON file: the language it
// shows, the plugins the viewer disabled and the folders of the plugins
// used where they are. Its methods may be called
// from any goroutine; the setters write the file before returning.
type Settings struct {
	path string
	mu   sync.Mutex
	v    settings
}

type settings struct {
	// Language is a BCP 47 tag, "" for the system's.
	Language string   `json:"language,omitempty"`
	Disabled []string `json:"disabled,omitempty"`
	// Linked are the folders of plugins loaded where they are, not
	// installed into the plugins folder.
	Linked []string `json:"linked,omitempty"`
}

// OpenSettings reads the settings kept at path. A missing file holds the
// defaults; an unreadable one is reported and replaced at the next write.
func OpenSettings(path string) (*Settings, error) {
	s := &Settings{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &s.v)
	}
	if err != nil {
		s.v = settings{}
	}
	return s, err
}

// Language is the language the viewer chose, "" for the system's.
func (s *Settings) Language() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.v.Language
}

// SetLanguage keeps the viewer's language, "" for the system's.
func (s *Settings) SetLanguage(lang string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v.Language = lang
	return s.write()
}

// Enabled reports whether the plugin id is enabled, as every plugin is
// until the viewer disables it.
func (s *Settings) Enabled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !slices.Contains(s.v.Disabled, id)
}

// SetEnabled enables or disables the plugin id.
func (s *Settings) SetEnabled(id string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v.Disabled = slices.DeleteFunc(s.v.Disabled, func(d string) bool { return d == id })
	if !on {
		s.v.Disabled = append(s.v.Disabled, id)
	}
	return s.write()
}

// Linked are the folders of the plugins used where they are.
func (s *Settings) Linked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.v.Linked)
}

// SetLinked adds or removes the folder of a plugin used where it is.
func (s *Settings) SetLinked(dir string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v.Linked = slices.DeleteFunc(s.v.Linked, func(d string) bool { return d == dir })
	if on {
		s.v.Linked = append(s.v.Linked, dir)
	}
	return s.write()
}

func (s *Settings) write() error {
	if s.path == "" {
		return nil
	}
	data, err := json.Marshal(s.v)
	if err != nil {
		return err
	}
	return writeFile(s.path, data)
}
