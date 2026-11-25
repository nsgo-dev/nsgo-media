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

// Settings are the app's own settings in one JSON file: the language it
// shows, the plugins the viewer disabled, the folders of the plugins
// used where they are, the GitHub releases plugins were installed from and
// whether it sends usage statistics. Its methods may be called from any
// goroutine; the setters write the file before returning.
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
	// Releases are the GitHub releases of the plugins installed from
	// GitHub, by the plugins' ids.
	Releases map[string]Release `json:"releases,omitempty"`
	// NoStats turns the usage statistics off.
	NoStats bool `json:"noStats,omitempty"`
	// Danmaku is how the player shows comments over the picture.
	Danmaku Danmaku `json:"danmaku,omitzero"`
}

// Danmaku is how the player shows comments over the picture. Its zero
// value is the defaults: on, every kind, 85% opaque over the upper half,
// at the medium size and speed.
type Danmaku struct {
	Off bool `json:"off,omitempty"`
	// Hide hides the comments of each kind.
	HideScroll bool `json:"hideScroll,omitempty"`
	HideTop    bool `json:"hideTop,omitempty"`
	HideBottom bool `json:"hideBottom,omitempty"`
	HideColor  bool `json:"hideColor,omitempty"`
	// Transparency is 1 less the opacity, from 0 to 0.9; nil is 0.15.
	Transparency *float64 `json:"transparency,omitempty"`
	// Area is the quarters of the picture from its top that comments
	// fill, 1 to 4; 0 is 2.
	Area int `json:"area,omitempty"`
	// Size and Speed are -1, 0 or 1: small, medium or large, and slow,
	// normal or fast.
	Size  int `json:"size,omitempty"`
	Speed int `json:"speed,omitempty"`
}

// Opacity is the comments' opacity.
func (d Danmaku) Opacity() float64 {
	if d.Transparency == nil {
		return 0.85
	}
	return 1 - min(max(*d.Transparency, 0), 0.9)
}

// SetOpacity sets the comments' opacity, from 0.1 to 1.
func (d *Danmaku) SetOpacity(o float64) {
	t := 1 - min(max(o, 0.1), 1)
	d.Transparency = &t
}

// Quarters is the quarters of the picture comments fill.
func (d Danmaku) Quarters() int {
	if d.Area < 1 || d.Area > 4 {
		return 2
	}
	return d.Area
}

// Release is the GitHub release a plugin was installed from, whose
// repository the app checks for a newer one.
type Release struct {
	// Repo is the repository as owner/repo.
	Repo      string    `json:"repo"`
	Tag       string    `json:"tag"`
	Published time.Time `json:"published"`
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

// Release is the GitHub release the plugin id was installed from, if it
// was.
func (s *Settings) Release(id string) (Release, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.v.Releases[id]
	return r, ok
}

// SetRelease keeps the GitHub release the plugin id was installed from;
// nil forgets it, as for a plugin removed or installed otherwise.
func (s *Settings) SetRelease(id string, r *Release) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.v.Releases[id]; !ok && r == nil {
		return nil
	}
	if r == nil {
		delete(s.v.Releases, id)
	} else {
		if s.v.Releases == nil {
			s.v.Releases = map[string]Release{}
		}
		s.v.Releases[id] = *r
	}
	return s.write()
}

// Stats reports whether the app sends usage statistics, as it does until
// the viewer turns them off.
func (s *Settings) Stats() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.v.NoStats
}

// SetStats turns the usage statistics on or off.
func (s *Settings) SetStats(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v.NoStats = !on
	return s.write()
}

// Danmaku is how the player shows comments.
func (s *Settings) Danmaku() Danmaku {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.v.Danmaku
}

// SetDanmaku keeps how the player shows comments.
func (s *Settings) SetDanmaku(d Danmaku) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.v.Danmaku = d
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
