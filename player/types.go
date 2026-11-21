package player

import (
	"cmp"
	"slices"
	"time"

	"github.com/oneo-me/nsgo-media/internal/pipeline"
)

// State is what a player is doing.
type State int

const (
	// Idle is a player with no source.
	Idle State = iota
	// Opening is a player reading its source's information.
	Opening
	// Paused is an opened player not playing, after Pause, Stop or
	// opening.
	Paused
	// Playing is a player playing.
	Playing
	// Buffering is a player that would play but waits for data.
	Buffering
	// Ended is a player that played to the end without looping.
	Ended
	// Failed is a player whose source could not open or play; Err says
	// why.
	Failed
)

func (s State) String() string {
	switch s {
	case Idle:
		return "idle"
	case Opening:
		return "opening"
	case Paused:
		return "paused"
	case Playing:
		return "playing"
	case Buffering:
		return "buffering"
	case Ended:
		return "ended"
	case Failed:
		return "failed"
	}
	return "unknown"
}

// SeekMode is how precisely a seek lands.
type SeekMode int

const (
	// Fast lands on the keyframe at or before the time, which shows at
	// once.
	Fast SeekMode = iota
	// Exact lands on the frame showing at the time, decoding from the
	// keyframe before it.
	Exact
)

// Kind is a track's media.
type Kind int

const (
	Video    Kind = Kind(pipeline.Video)
	Audio    Kind = Kind(pipeline.Audio)
	Subtitle Kind = Kind(pipeline.Subtitle)
)

// Track is one stream of the source, or an external subtitle file.
type Track struct {
	// ID names the track to SelectAudio, SelectSubtitle and SelectVariant.
	ID       int
	Kind     Kind
	Codec    string
	Language string // the ISO 639 code the source gives, such as "eng"
	Title    string
	Default  bool
	Forced   bool
	// External is set for a track of ExternalAudio's source and for a
	// subtitle file AddSubtitles added.
	External bool

	// Width and Height are a video's size in pixels as encoded; Rotation
	// is the clockwise quarter turns, in degrees, it is shown at, and
	// AspectRatio the shape of a pixel, width over height.
	Width, Height int
	Rotation      int
	AspectRatio   float64
	FrameRate     float64

	SampleRate, Channels int
	BitRate              int64
	// Bandwidth is the bit rate an HLS or DASH manifest gives the variant
	// the track belongs to; 0 for a track of no single variant.
	Bandwidth int64
}

// Chapter is a named span of the source.
type Chapter struct {
	Start, End time.Duration
	Title      string
}

// Info is what opening tells about the source.
type Info struct {
	// Duration is 0 when unknown, as for a live stream's sliding window.
	Duration time.Duration
	// Live is an HLS or DASH stream whose end is not published yet.
	Live bool
	// Seekable is false for a source Seek cannot move in, such as a live
	// stream keeping only a sliding window.
	Seekable bool
	// Format is the container's short names, such as "matroska,webm".
	Format   string
	BitRate  int64
	Metadata map[string]string
	Tracks   []Track
	Chapters []Chapter
}

// Cue is a subtitle text showing from Start to End.
type Cue struct {
	Start, End time.Duration
	Text       string
}

func trackOf(t pipeline.Track) Track {
	return Track{
		ID: t.ID, Kind: Kind(t.Kind), Codec: t.Codec, Language: t.Language, Title: t.Title,
		Default: t.Default, Forced: t.Forced, External: t.External,
		Width: t.Width, Height: t.Height, Rotation: t.Rotation, AspectRatio: t.AspectRatio, FrameRate: t.FrameRate,
		SampleRate: t.SampleRate, Channels: t.Channels, BitRate: t.BitRate, Bandwidth: t.Bandwidth,
	}
}

// Variants are the video tracks of an HLS or DASH stream's variants, by
// bandwidth from the lowest, for SelectVariant; none for other sources.
func (i *Info) Variants() []Track {
	var vs []Track
	for _, t := range i.Tracks {
		if t.Kind == Video && t.Bandwidth > 0 {
			vs = append(vs, t)
		}
	}
	slices.SortStableFunc(vs, func(a, b Track) int { return cmp.Compare(a.Bandwidth, b.Bandwidth) })
	return vs
}

func infoOf(i *pipeline.Info) *Info {
	info := &Info{Duration: i.Duration, Live: i.Live, Seekable: i.Seekable, Format: i.Format, BitRate: i.BitRate, Metadata: i.Metadata}
	for _, t := range i.Tracks {
		info.Tracks = append(info.Tracks, trackOf(t))
	}
	for _, c := range i.Chapters {
		info.Chapters = append(info.Chapters, Chapter{Start: c.Start, End: c.End, Title: c.Title})
	}
	return info
}

func cuesOf(cs []pipeline.Cue) []Cue {
	if len(cs) == 0 {
		return nil
	}
	out := make([]Cue, len(cs))
	for i, c := range cs {
		out[i] = Cue{Start: c.Start, End: c.End, Text: c.Text}
	}
	return out
}

func sameCues(a, b []Cue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
