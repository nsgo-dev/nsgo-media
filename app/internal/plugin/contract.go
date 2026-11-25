package plugin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The exports the app calls. A plugin exports any of them; what it leaves
// out, the app leaves out.
const (
	ExportAccount    = "account"
	ExportSignIn     = "signIn"
	ExportSignOut    = "signOut"
	ExportHome       = "home"
	ExportBrowse     = "browse"
	ExportSearch     = "search"
	ExportSuggest    = "suggest"
	ExportResolve    = "resolve"
	ExportVideo      = "video"
	ExportStream     = "stream"
	ExportProgress   = "progress"
	ExportSetSetting = "setSetting"
	ExportFollowing  = "following"
	ExportFollow     = "follow"
	ExportUnfollow   = "unfollow"
	ExportDanmaku    = "danmaku"
)

// Exports lists every export of the contract.
var Exports = []string{
	ExportAccount, ExportSignIn, ExportSignOut, ExportHome, ExportBrowse, ExportSearch,
	ExportSuggest, ExportResolve, ExportVideo, ExportStream, ExportProgress, ExportSetSetting,
	ExportFollowing, ExportFollow, ExportUnfollow, ExportDanmaku,
}

// ItemKind is what an item opens: a video opens video, the others browse.
type ItemKind string

// The item kinds.
const (
	KindVideo      ItemKind = "video"
	KindUploader   ItemKind = "uploader"
	KindCollection ItemKind = "collection"
	KindLink       ItemKind = "link"
)

// Source is something the app fetches for a plugin, a picture, a stream or
// subtitles, with the request headers it needs.
type Source struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Item is a card of a page.
type Item struct {
	Kind     ItemKind `json:"kind"`
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Image    *Source  `json:"image,omitempty"`
	// Duration is a video's length in seconds.
	Duration float64 `json:"duration,omitempty"`
}

// Section is a titled row of items, with a browse id for the rest of them.
type Section struct {
	Title string `json:"title,omitempty"`
	Items []Item `json:"items"`
	More  string `json:"more,omitempty"`
}

// Page is what home, browse and search answer. Next, when set, is the
// cursor that asks for the page after it.
type Page struct {
	Title    string    `json:"title,omitempty"`
	Sections []Section `json:"sections"`
	Next     string    `json:"next,omitempty"`
}

// Account is the signed-in viewer.
type Account struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Avatar *Source `json:"avatar,omitempty"`
}

// Video is a video's details.
type Video struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Image       *Source    `json:"image,omitempty"`
	Uploader    *Item      `json:"uploader,omitempty"`
	Published   *time.Time `json:"published,omitempty"`
	Duration    float64    `json:"duration,omitempty"`
	Parts       []Part     `json:"parts"`
	// Part is the id of the part playing starts with, as when a link
	// chose one; "" is the first.
	Part    string `json:"part,omitempty"`
	Related []Item `json:"related,omitempty"`
	// Show is the show the video belongs to, whose episodes are its
	// parts, when it is one the account can follow.
	Show *VideoShow `json:"show,omitempty"`
}

// VideoShow is the show a video belongs to, as its page follows it.
type VideoShow struct {
	ID string `json:"id"`
	// Status is the status the account follows it with, one of the
	// manifest's; "" is not followed.
	Status string `json:"status,omitempty"`
	// Watched is the episodes watched, the first of them.
	Watched int `json:"watched,omitempty"`
}

// Show is a show the account follows.
type Show struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Image *Source `json:"image,omitempty"`
	// Video is the id of the video that opens it, whose parts are its
	// episodes.
	Video  string `json:"video"`
	Status string `json:"status"`
	// Watched is the episodes watched, the first of them, and Episodes
	// those out.
	Watched  int `json:"watched,omitempty"`
	Episodes int `json:"episodes,omitempty"`
	// New is an episode out since the viewer last watched.
	New bool `json:"new,omitempty"`
}

// Following is what following answers: the shows followed, newest
// update first, and the cursor of the page after it.
type Following struct {
	Shows []Show `json:"shows"`
	Next  string `json:"next,omitempty"`
}

// Part is one playable part of a video.
type Part struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration,omitempty"`
}

// Stream is what to play for a part: a video, an audio track when it is
// separate or alone, the quality answered among those on offer, and the
// subtitle tracks.
type Stream struct {
	Video     *Source    `json:"video,omitempty"`
	Audio     *Source    `json:"audio,omitempty"`
	Quality   string     `json:"quality,omitempty"`
	Qualities []Quality  `json:"qualities,omitempty"`
	Subtitles []Subtitle `json:"subtitles,omitempty"`
}

// Quality is a quality a stream is on offer at.
type Quality struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Subtitle is a subtitle track: SRT, WebVTT or ASS at a URL, or as Text
// when the plugin converted them from a format the player does not read.
type Subtitle struct {
	Label    string `json:"label"`
	Language string `json:"language,omitempty"`
	Text     string `json:"text,omitempty"`
	Source
}

// Danmaku is a comment shown over the picture as its time plays: one
// that scrolls across, or one fixed at the top or the bottom.
type Danmaku struct {
	// Time is when it shows, in seconds from the part's start.
	Time float64     `json:"time"`
	Mode DanmakuMode `json:"mode,omitempty"`
	// Color is #rrggbb, "" for white.
	Color string `json:"color,omitempty"`
	Text  string `json:"text"`
}

// DanmakuMode is where a comment shows; "" scrolls.
type DanmakuMode string

// The danmaku modes.
const (
	DanmakuScroll DanmakuMode = "scroll"
	DanmakuTop    DanmakuMode = "top"
	DanmakuBottom DanmakuMode = "bottom"
)

// MaxDanmaku bounds the comments of one answer of danmaku.
const MaxDanmaku = 20000

// The parameters of the exports.
type (
	PageParams struct {
		Cursor string `json:"cursor,omitempty"`
	}
	BrowseParams struct {
		ID     string `json:"id"`
		Cursor string `json:"cursor,omitempty"`
	}
	SearchParams struct {
		Query string `json:"query"`
		// Kind, when set, asks for results of that kind alone.
		Kind   ItemKind `json:"kind,omitempty"`
		Cursor string   `json:"cursor,omitempty"`
	}
	SuggestParams struct {
		Query string `json:"query"`
	}
	ResolveParams struct {
		Text string `json:"text"`
	}
	VideoParams struct {
		ID string `json:"id"`
	}
	StreamParams struct {
		Video   string `json:"video"`
		Part    string `json:"part"`
		Quality string `json:"quality,omitempty"`
	}
	ProgressParams struct {
		Video    string  `json:"video"`
		Part     string  `json:"part"`
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
	}
	SettingParams struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	FollowingParams struct {
		// Status, when set, asks for the shows of that status alone.
		Status string `json:"status,omitempty"`
		Cursor string `json:"cursor,omitempty"`
	}
	FollowParams struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	UnfollowParams struct {
		ID string `json:"id"`
	}
	DanmakuParams struct {
		Video string `json:"video"`
		Part  string `json:"part"`
		// From and To are the span of the part, in seconds, whose
		// comments the app asks for.
		From float64 `json:"from"`
		To   float64 `json:"to"`
	}
)

// The error codes a plugin throws, each with what the app does.
const (
	CodeSignIn      = "signIn"
	CodeSetup       = "setup"
	CodeUnavailable = "unavailable"
)

// What errors.Is finds in the *Error of each code.
var (
	ErrSignIn      = errors.New("plugin: sign in to continue")
	ErrSetup       = errors.New("plugin: the plugin's settings need attention")
	ErrUnavailable = errors.New("plugin: unavailable")
)

// Error is what a plugin threw or rejected with: its message and, when it
// tells the app what to do, its code.
type Error struct {
	Plugin  string
	Code    string
	Message string
	// Stack is the JavaScript stack, for logs.
	Stack string
}

func (e *Error) Error() string { return "plugin " + e.Plugin + ": " + e.Message }

// Is matches the sentinel of the error's code.
func (e *Error) Is(target error) bool {
	switch e.Code {
	case CodeSignIn:
		return target == ErrSignIn
	case CodeSetup:
		return target == ErrSetup
	case CodeUnavailable:
		return target == ErrUnavailable
	}
	return false
}

// InvalidError is an answer that breaks the contract.
type InvalidError struct {
	Plugin, Export string
	Err            error
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("plugin %s: %s answered %v", e.Plugin, e.Export, e.Err)
}

func (e *InvalidError) Unwrap() error { return e.Err }

// checker collects what an answer breaks, by where it is. allow approves
// the URLs of sources, which the app fetches only from the plugin's hosts;
// files lets a native plugin's streams and subtitles be local files.
type checker struct {
	allow    func(*http.Request) error
	files    bool
	statuses []Status
	errs     []error
}

func (c *checker) fail(at, format string, args ...any) {
	c.errs = append(c.errs, fmt.Errorf(at+": "+format, args...))
}

func (c *checker) err() error { return errors.Join(c.errs...) }

func (c *checker) need(at, name, v string) {
	if strings.TrimSpace(v) == "" {
		c.fail(at, "%s is empty", name)
	}
}

// status checks a show's status against the manifest's; "" is one when
// emptyOK.
func (c *checker) status(at, status string, emptyOK bool) {
	if status == "" && emptyOK {
		return
	}
	for _, s := range c.statuses {
		if s.ID == status {
			return
		}
	}
	c.fail(at, "status %q is none of the manifest's statuses", status)
}

func (c *checker) source(at string, s *Source) {
	if s == nil {
		return
	}
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		c.fail(at, "url %q is not an http or https URL", s.URL)
	} else if err := c.allow(&http.Request{URL: u}); err != nil {
		c.fail(at, "url %q is not on a host of the network permission", s.URL)
	}
	for k := range s.Headers {
		if k == "" || strings.ContainsAny(k, " :\r\n") {
			c.fail(at, "header %q is not a header name", k)
		}
	}
}

// media checks a stream's or subtitles' source, which may be a file.
func (c *checker) media(at string, s *Source) {
	if s != nil && c.files {
		if _, ok := FilePath(s.URL); ok {
			return
		}
	}
	c.source(at, s)
}

func (c *checker) item(at string, it *Item) {
	switch it.Kind {
	case KindVideo, KindUploader, KindCollection, KindLink:
	default:
		c.fail(at, "kind %q is none of video, uploader, collection and link", it.Kind)
	}
	c.need(at, "id", it.ID)
	c.need(at, "title", it.Title)
	c.source(at+".image", it.Image)
	if it.Duration < 0 {
		c.fail(at, "duration is negative")
	}
}

func (c *checker) items(at string, items []Item) {
	for i := range items {
		c.item(fmt.Sprintf("%s[%d]", at, i), &items[i])
	}
}

func (p *Page) check(c *checker) {
	for i, s := range p.Sections {
		c.items(fmt.Sprintf("sections[%d].items", i), s.Items)
	}
}

func (a *Account) check(c *checker) {
	c.need("account", "id", a.ID)
	c.need("account", "name", a.Name)
	c.source("account.avatar", a.Avatar)
}

func (it *Item) check(c *checker) { c.item("item", it) }

func (v *Video) check(c *checker) {
	c.need("video", "id", v.ID)
	c.need("video", "title", v.Title)
	c.source("video.image", v.Image)
	if v.Uploader != nil {
		c.item("video.uploader", v.Uploader)
	}
	if len(v.Parts) == 0 {
		c.fail("video", "has no parts")
	}
	found := v.Part == ""
	for i, p := range v.Parts {
		at := fmt.Sprintf("parts[%d]", i)
		c.need(at, "id", p.ID)
		c.need(at, "title", p.Title)
		found = found || p.ID == v.Part
	}
	if !found {
		c.fail("video", "part %q is not among its parts", v.Part)
	}
	c.items("related", v.Related)
	if v.Show != nil {
		c.need("video.show", "id", v.Show.ID)
		c.status("video.show", v.Show.Status, true)
		if v.Show.Watched < 0 {
			c.fail("video.show", "watched is negative")
		}
	}
}

func (f *Following) check(c *checker) {
	for i, s := range f.Shows {
		at := fmt.Sprintf("shows[%d]", i)
		c.need(at, "id", s.ID)
		c.need(at, "title", s.Title)
		c.need(at, "video", s.Video)
		c.source(at+".image", s.Image)
		c.status(at, s.Status, false)
		if s.Watched < 0 || s.Episodes < 0 {
			c.fail(at, "watched or episodes is negative")
		}
	}
}

func checkDanmaku(list []Danmaku, c *checker) {
	if len(list) > MaxDanmaku {
		c.fail("danmaku", "has %d comments, more than %d", len(list), MaxDanmaku)
		return
	}
	for i, d := range list {
		at := fmt.Sprintf("danmaku[%d]", i)
		c.need(at, "text", d.Text)
		if d.Time < 0 {
			c.fail(at, "time is negative")
		}
		switch d.Mode {
		case "", DanmakuScroll, DanmakuTop, DanmakuBottom:
		default:
			c.fail(at, "mode %q is none of scroll, top and bottom", d.Mode)
		}
		if d.Color != "" && !colorPattern.MatchString(d.Color) {
			c.fail(at, "color %q is not a color such as #ffffff", d.Color)
		}
	}
}

func (s *Stream) check(c *checker) {
	if s.Video == nil && s.Audio == nil {
		c.fail("stream", "has neither video nor audio")
	}
	c.media("stream.video", s.Video)
	c.media("stream.audio", s.Audio)
	found := s.Quality == ""
	for i, q := range s.Qualities {
		at := fmt.Sprintf("qualities[%d]", i)
		c.need(at, "id", q.ID)
		c.need(at, "label", q.Label)
		found = found || q.ID == s.Quality
	}
	if !found {
		c.fail("stream", "quality %q is not among its qualities", s.Quality)
	}
	for i := range s.Subtitles {
		at := fmt.Sprintf("subtitles[%d]", i)
		sub := &s.Subtitles[i]
		c.need(at, "label", sub.Label)
		switch {
		case sub.Text == "":
			c.media(at, &sub.Source)
		case sub.URL != "":
			c.fail(at, "has both a url and text")
		}
	}
}
