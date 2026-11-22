package player

import (
	"cmp"
	"image"
	"math"
	"slices"
	"time"

	"github.com/nsgo-dev/nsgo/core"

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

// Cue is a subtitle showing from Start to End: the text of a text track,
// or the bitmaps of a PGS, DVD, DVB or VobSub track. A cue replaces the
// one before it on a bitmap track; no cue showing clears the screen.
type Cue struct {
	// End is 0 while a bitmap cue's end is not read yet: a PGS cue shows
	// until the next cue of its track, which the player reads ahead.
	Start, End time.Duration
	// Generation is the seek the cue was decoded after, rising with each
	// seek and each step backward that decodes again: the same cue read
	// again has new bitmaps. It is 0 for a file AddSubtitles added.
	Generation uint64
	Text       string
	// Canvas is the size, in pixels, of the canvas the bitmaps are placed
	// on, which covers the picture; Place maps it onto a view.
	Canvas  image.Point
	Bitmaps []Bitmap
}

// Bitmap is a picture of a cue: Image, premultiplied as image.RGBA is,
// with its top left corner at X, Y on the cue's canvas. A rotated
// video's bitmaps and canvas are turned as its picture is.
type Bitmap struct {
	X, Y  int
	Image *image.RGBA
}

// Place is where bitmap b of c shows over a picture drawn at picture,
// such as PictureBounds gives, in the same units: the canvas is stretched
// over the picture.
func (c Cue) Place(b Bitmap, picture core.Bounds) core.Bounds {
	if c.Canvas.X <= 0 || c.Canvas.Y <= 0 || b.Image == nil {
		return core.Bounds{}
	}
	sx := float64(picture.Size.Width) / float64(c.Canvas.X)
	sy := float64(picture.Size.Height) / float64(c.Canvas.Y)
	return core.Bounds{
		Origin: core.Point{
			X: picture.Origin.X + core.Points(float64(b.X)*sx),
			Y: picture.Origin.Y + core.Points(float64(b.Y)*sy),
		},
		Size: core.Size{
			Width:  core.Points(float64(b.Image.Rect.Dx()) * sx),
			Height: core.Points(float64(b.Image.Rect.Dy()) * sy),
		},
	}
}

// Passthrough is a set of codecs whose audio passes undecoded, in IEC
// 61937 bursts, to a receiver that decodes it.
type Passthrough int

const (
	PassthroughAC3  = Passthrough(pipeline.PassAC3)
	PassthroughEAC3 = Passthrough(pipeline.PassEAC3)
	// PassthroughDTS passes DTS' core: DTS-HD's too where
	// PassthroughDTSHD is not set or the device takes no high bit rate.
	// Other enhanced DTS, such as DTS-ES, DTS 96/24 or DTS Express, plays
	// as PCM.
	PassthroughDTS = Passthrough(pipeline.PassDTS)
	// PassthroughDTSHD passes DTS-HD High Resolution and Master Audio
	// whole, core and extensions, on HDMI's high bit rate carrier.
	PassthroughDTSHD = Passthrough(pipeline.PassDTSHD)
	// PassthroughTrueHD passes TrueHD as its stream is, Atmos and all,
	// on HDMI's high bit rate carrier.
	PassthroughTrueHD = Passthrough(pipeline.PassTrueHD)
)

// Fallback is why the audio plays otherwise than passthrough was set for
// its codec: as PCM, or DTS-HD as its core.
type Fallback int

const (
	// FallbackNone is no fallback: the audio passes through, or
	// passthrough was not set for its codec.
	FallbackNone = Fallback(pipeline.FallbackNone)
	// FallbackRate is a rate other than 1, which only PCM plays.
	FallbackRate = Fallback(pipeline.FallbackRate)
	// FallbackEnhancedDTS is DTS with extensions to its core that do not
	// pass: DTS-HD without PassthroughDTSHD, DTS-ES, DTS 96/24 or DTS
	// Express.
	FallbackEnhancedDTS = Fallback(pipeline.FallbackEnhancedDTS)
	// FallbackDevice is the device or the system not taking the stream,
	// refusing it or failing to start it; AudioOutput.Err says why.
	// DTS-HD then passes its core where PassthroughDTS is set.
	FallbackDevice = Fallback(pipeline.FallbackDevice)
)

func (f Fallback) String() string {
	switch f {
	case FallbackNone:
		return "none"
	case FallbackRate:
		return "rate"
	case FallbackEnhancedDTS:
		return "enhanced DTS"
	case FallbackDevice:
		return "device"
	}
	return "unknown"
}

// AudioOutput is what the audio device plays.
type AudioOutput struct {
	// Passthrough is set while Codec passes through: "ac3", "eac3", "dts"
	// (DTS' core, DTS-HD's too), "dtshd" (DTS-HD whole) or "truehd".
	Passthrough bool
	Codec       string
	// SampleRate and Channels are the device stream's: the PCM's, or
	// passing through, the IEC 61937 carrier's, 2 channels at the codec's
	// rate or four times it for E-AC-3, and for DTS-HD and TrueHD the high
	// bit rate's 8 at 192 kHz (176.4 kHz for 44.1 kHz's family). The
	// track's own are in its Track.
	SampleRate, Channels int
	// Fallback is why the audio plays as PCM or DTS-HD as its core, and
	// Err the device's error for FallbackDevice.
	Fallback Fallback
	Err      error
}

func outputOf(o pipeline.Output) AudioOutput {
	return AudioOutput{Passthrough: o.Passthrough, Codec: o.Codec, SampleRate: o.Rate, Channels: o.Channels, Fallback: Fallback(o.Fallback), Err: o.Err}
}

func sameOutput(a, b AudioOutput) bool {
	return a.Passthrough == b.Passthrough && a.Codec == b.Codec && a.SampleRate == b.SampleRate &&
		a.Channels == b.Channels && a.Fallback == b.Fallback &&
		(a.Err == nil) == (b.Err == nil) && (a.Err == nil || a.Err.Error() == b.Err.Error())
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
		out[i] = Cue{Start: c.Start, End: c.End, Generation: c.Gen, Text: c.Text, Canvas: c.Canvas}
		if c.End == math.MaxInt64 {
			out[i].End = 0
		}
		for _, b := range c.Bitmaps {
			out[i].Bitmaps = append(out[i].Bitmaps, Bitmap{X: b.X, Y: b.Y, Image: b.Image})
		}
	}
	return out
}

func sameCues(a, b []Cue) bool {
	return slices.EqualFunc(a, b, func(x, y Cue) bool {
		return x.Start == y.Start && x.End == y.End && x.Generation == y.Generation && x.Text == y.Text &&
			x.Canvas == y.Canvas && slices.Equal(x.Bitmaps, y.Bitmaps)
	})
}
