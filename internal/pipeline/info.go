package pipeline

import (
	"time"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// Kind is a track's media.
type Kind int

const (
	Video Kind = iota
	Audio
	Subtitle
)

// Track is one stream of the source, or an external subtitle file.
type Track struct {
	// ID is the stream's index, or for an external file a number past the
	// source's streams.
	ID       int
	Kind     Kind
	Codec    string
	Language string
	Title    string
	Default  bool
	Forced   bool
	// External is set for a track of an external audio source and for a
	// subtitle file added beside the source.
	External bool

	Width, Height int // the picture's size in pixels, before rotation
	Rotation      int // clockwise degrees the picture is shown at
	FrameRate     float64
	// AspectRatio is the shape of a pixel, width over height; 1 for
	// square pixels.
	AspectRatio float64

	SampleRate, Channels int
	BitRate              int64
}

// Chapter is a named span of the source.
type Chapter struct {
	Start, End time.Duration
	Title      string
}

// Info is what opening a source tells about it.
type Info struct {
	Duration time.Duration
	Format   string
	BitRate  int64
	Metadata map[string]string
	Tracks   []Track
	Chapters []Chapter
}

// textSubtitles are the subtitle codecs that decode to text. Bitmap
// subtitles, such as PGS and DVD's, are out of the first version's scope.
var textSubtitles = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true, "webvtt": true,
	"mov_text": true, "text": true, "microdvd": true, "subviewer": true,
	"subviewer1": true, "sami": true, "realtext": true, "mpl2": true,
	"pjs": true, "vplayer": true, "jacosub": true, "stl": true,
}

func readInfo(f *ffmpeg.FormatContext) *Info {
	info := &Info{
		Duration: f.Duration(),
		Format:   f.FormatName(),
		BitRate:  f.BitRate(),
		Metadata: f.Metadata(),
	}
	for _, s := range f.Streams() {
		par := s.Params()
		md := s.Metadata()
		t := Track{
			ID:       s.Index(),
			Codec:    par.CodecName(),
			Language: md["language"],
			Title:    md["title"],
			Default:  s.Default(),
			Forced:   s.Forced(),
			BitRate:  par.BitRate(),
		}
		switch par.Type() {
		case ffmpeg.MediaVideo:
			if s.AttachedPicture() {
				continue
			}
			t.Kind = Video
			t.Width, t.Height = par.Width(), par.Height()
			t.Rotation = par.Rotation()
			t.FrameRate = s.AvgFrameRate().Float()
			if t.FrameRate == 0 {
				t.FrameRate = s.RealFrameRate().Float()
			}
			t.AspectRatio = 1
			if sar := par.SampleAspectRatio(); sar.Num > 0 && sar.Den > 0 {
				t.AspectRatio = sar.Float()
			}
		case ffmpeg.MediaAudio:
			t.Kind = Audio
			t.SampleRate, t.Channels = par.SampleRate(), par.Channels()
		case ffmpeg.MediaSubtitle:
			if !textSubtitles[t.Codec] {
				continue
			}
			t.Kind = Subtitle
		default:
			continue
		}
		if t.Kind != Subtitle && info.Duration == 0 {
			info.Duration = s.Duration()
		}
		info.Tracks = append(info.Tracks, t)
	}
	for _, c := range f.Chapters() {
		tb := c.TimeBase()
		info.Chapters = append(info.Chapters, Chapter{
			Start: tb.Duration(c.Start()),
			End:   tb.Duration(c.End()),
			Title: c.Metadata()["title"],
		})
	}
	return info
}
