package playback

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/oneo-me/nsgo-media/player"
)

// Source is what the player page plays: a local file or a URL the viewer
// opened, or what a plugin's stream answered, as plain data.
type Source struct {
	// Key names the video for its resume position, as stream URLs may
	// change between calls; the URL names it when empty.
	Key   string
	Title string
	// Video is the name of the video a part of several is, which the
	// controls show under the part's title.
	Video string
	// URL is a file path or an http(s) URL, requested with Headers.
	URL     string
	Headers http.Header
	// Audio is a separate audio stream played with URL's picture, as DASH
	// serves them; nil when URL has its own sound.
	Audio *Stream
	// Subtitles are SRT, WebVTT or ASS tracks: files, http(s) URLs or
	// their text.
	Subtitles []Subtitle
	// Start is where playback starts, as when another quality replaces
	// the stream; 0 offers the resume position instead.
	Start time.Duration
	// Qualities are the qualities the source is on offer at, and Quality
	// the one it is at; the page's quality menu shows them.
	Qualities []Quality
	Quality   string
	// FromStart starts at 0 without offering the resume position.
	FromStart bool
	// Parts are the parts of the video the source is one of, and Part
	// its index among them; the page's parts menu shows them.
	Parts []Part
	Part  int
	// Badge marks where the source comes from, before its title; nil for
	// none.
	Badge core.Node
	// Danmaku loads the comments shown over the picture; nil has none,
	// which leaves out the danmaku button.
	Danmaku DanmakuLoader
}

// Part is a part of a video under its number's label and its title.
type Part struct {
	Label, Title string
	Duration     time.Duration
}

// Subtitle is a subtitle track under its label: a file, a URL, or Text
// itself.
type Subtitle struct {
	Label string
	Text  string
	Stream
}

// Quality is a quality on offer under its label.
type Quality struct {
	ID, Label string
}

// Stream is one URL with the headers its requests send.
type Stream struct {
	URL     string
	Headers http.Header
}

// Validate reports a source the player cannot open.
func (s Source) Validate() error {
	if err := checkURL(s.URL); err != nil {
		return err
	}
	if s.Audio != nil {
		if err := checkURL(s.Audio.URL); err != nil {
			return fmt.Errorf("audio: %w", err)
		}
	}
	for _, sub := range s.Subtitles {
		if sub.Text != "" {
			continue
		}
		if err := checkURL(sub.URL); err != nil {
			return fmt.Errorf("subtitles %s: %w", sub.Label, err)
		}
	}
	return nil
}

// checkURL accepts a file path or an http(s) URL. A scheme of one letter
// is a Windows drive.
func checkURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("no URL")
	}
	u, err := url.Parse(raw)
	if err != nil || len(u.Scheme) <= 1 {
		return nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s URLs cannot play", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("the URL has no host")
	}
	return nil
}

// key names the source's resume position.
func (s Source) key() string {
	if s.Key != "" {
		return s.Key
	}
	return s.URL
}

// partTitle is the part's title, which a pinned window shows, else the
// title.
func (s Source) partTitle() string {
	if len(s.Parts) > 1 && s.Part < len(s.Parts) {
		return s.Parts[s.Part].Title
	}
	return s.title()
}

// title is what the page's header shows: the title, else the file's name.
func (s Source) title() string {
	if s.Title != "" {
		return s.Title
	}
	if u, err := url.Parse(s.URL); err == nil && len(u.Scheme) > 1 {
		if name := path.Base(u.Path); name != "/" && name != "." {
			return name
		}
		return u.Host
	}
	return filepath.Base(s.URL)
}

// options are how the player opens the source.
func (s Source) options() []player.OpenOption {
	var opts []player.OpenOption
	if len(s.Headers) > 0 {
		opts = append(opts, player.Headers(s.Headers))
	}
	if s.Audio != nil {
		opts = append(opts, player.ExternalAudio(s.Audio.URL, s.Audio.Headers))
	}
	return opts
}

// Resume positions are kept only between these margins: a video barely
// started, or nearly over, starts from the beginning.
const (
	resumeAfter  = 10 * time.Second
	resumeBefore = 15 * time.Second
)

// resumable reports whether a video of duration d left at pos is worth
// resuming; d is 0 when unknown.
func resumable(pos, d time.Duration) bool {
	return pos >= resumeAfter && (d <= 0 || d-pos >= resumeBefore)
}
