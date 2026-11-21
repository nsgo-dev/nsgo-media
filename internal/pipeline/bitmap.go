package pipeline

import (
	"errors"
	"image"
	"math"
	"slices"
	"sync/atomic"
	"time"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// bitmapSubtitles are the subtitle codecs that decode to bitmaps.
var bitmapSubtitles = map[string]bool{
	"hdmv_pgs_subtitle": true, "dvd_subtitle": true, "dvb_subtitle": true,
}

// bitmapCue makes the cue of a decoded bitmap subtitle at start, on a
// canvas of cw×ch turned by rot. end is openEnd when not known; sure
// tells an end the subtitle gave from a time-out it may end before.
func bitmapCue(sub *ffmpeg.Subtitle, start, end time.Duration, sure bool, gen uint64, cw, ch, rot int) Cue {
	c := Cue{Start: start, End: end, Gen: gen, Canvas: image.Pt(cw, ch), sure: sure}
	for _, r := range sub.Rects {
		if r.Image != nil {
			c.Bitmaps = append(c.Bitmaps, Bitmap{X: r.X, Y: r.Y, Image: r.Image})
		}
	}
	turnCue(&c, rot)
	return c
}

// subtitleTimes are when a decoded subtitle shows, from a packet at base
// lasting dur (0 when unknown): its end openEnd when it gives none.
func subtitleTimes(sub *ffmpeg.Subtitle, base, dur time.Duration) (start, end time.Duration, given bool) {
	if sub.PTS != ffmpeg.NoPTS {
		base = time.Duration(sub.PTS) * time.Microsecond
	}
	start = base + time.Duration(sub.Start)*time.Millisecond
	switch {
	case sub.End != math.MaxUint32 && sub.End > sub.Start:
		return start, base + time.Duration(sub.End)*time.Millisecond, true
	case dur > 0:
		return start, base + dur, true
	}
	return start, openEnd, false
}

// turnCue turns a cue's canvas and bitmaps clockwise by deg, as the
// picture is turned.
func turnCue(c *Cue, deg int) {
	if deg != 90 && deg != 180 && deg != 270 {
		return
	}
	cw, ch := c.Canvas.X, c.Canvas.Y
	bs := make([]Bitmap, len(c.Bitmaps))
	for i, b := range c.Bitmaps {
		w, h := b.Image.Rect.Dx(), b.Image.Rect.Dy()
		dw, dh := h, w
		if deg == 180 {
			dw, dh = w, h
		}
		img := image.NewRGBA(image.Rect(0, 0, dw, dh))
		rotate(img.Pix, b.Image.Pix, w, h, deg)
		switch deg {
		case 90:
			bs[i] = Bitmap{X: ch - b.Y - h, Y: b.X, Image: img}
		case 180:
			bs[i] = Bitmap{X: cw - b.X - w, Y: ch - b.Y - h, Image: img}
		case 270:
			bs[i] = Bitmap{X: b.Y, Y: cw - b.X - w, Image: img}
		}
	}
	c.Bitmaps = bs
	if deg != 180 {
		c.Canvas = image.Pt(ch, cw)
	}
}

// bitmapFile is one subtitle stream of an external VobSub file: its
// packets, held in memory as the file is small, decoded as they show.
type bitmapFile struct {
	dec    *ffmpeg.Decoder
	pkts   []*ffmpeg.Packet
	times  []time.Duration // the packets' times from the source's start
	offset time.Duration   // the source's first timestamp
	canvas image.Point

	// The last packet decoded and its cue, turned by rot.
	last    int
	lastRot int
	cue     Cue
	shows   bool
}

// openBitmapFiles reads a VobSub .idx, with the .sub beside it, into a
// stream per language.
func openBitmapFiles(path string, offset time.Duration) ([]*bitmapFile, []Track, error) {
	var interrupt atomic.Bool
	in, err := ffmpeg.OpenInput(path, nil, &interrupt, ffmpeg.InputOptions{})
	if err != nil {
		return nil, nil, err
	}
	defer in.Close()
	streams := in.Format.Streams()
	files := make([]*bitmapFile, len(streams))
	var tracks []Track
	for i, s := range streams {
		par := s.Params()
		if par.Type() != ffmpeg.MediaSubtitle || !bitmapSubtitles[par.CodecName()] {
			continue
		}
		dec, err := ffmpeg.NewDecoder(par, s.TimeBase(), 1, "")
		if err != nil {
			continue
		}
		md := s.Metadata()
		files[i] = &bitmapFile{dec: dec, offset: offset, canvas: image.Pt(par.Width(), par.Height()), last: -1}
		tracks = append(tracks, Track{Kind: Subtitle, Codec: par.CodecName(), Language: md["language"], Title: md["title"], External: true})
	}
	for {
		pkt := ffmpeg.NewPacket()
		if err := in.Format.ReadPacket(pkt); err != nil {
			pkt.Free()
			break
		}
		idx := pkt.StreamIndex()
		ts := packetTime(pkt)
		if idx >= len(files) || files[idx] == nil || ts == ffmpeg.NoPTS {
			pkt.Free()
			continue
		}
		f := files[idx]
		f.pkts = append(f.pkts, pkt)
		f.times = append(f.times, streams[idx].TimeBase().Duration(ts))
	}
	var out []*bitmapFile
	for _, f := range files {
		if f == nil {
			continue
		}
		// The index lists its times in order, but stay safe.
		order := make([]int, len(f.pkts))
		for i := range order {
			order[i] = i
		}
		slices.SortStableFunc(order, func(a, b int) int { return compareDur(f.times[a], f.times[b]) })
		pkts, times := make([]*ffmpeg.Packet, len(order)), make([]time.Duration, len(order))
		for i, j := range order {
			pkts[i], times[i] = f.pkts[j], f.times[j]
		}
		f.pkts, f.times = pkts, times
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, nil, errors.New("subtitles: no bitmap subtitles in " + baseName(path))
	}
	return out, tracks, nil
}

// at is the cue showing at the media time t, turned by rot. A packet
// shows until it ends or the next one starts.
func (f *bitmapFile) at(t time.Duration, rot int) (Cue, bool) {
	t -= f.offset
	i, found := slices.BinarySearchFunc(f.times, t, compareDur)
	if !found {
		i--
	}
	if i < 0 {
		return Cue{}, false
	}
	if i != f.last || rot != f.lastRot {
		f.last, f.lastRot, f.shows = i, rot, false
		f.dec.Flush()
		if sub, err := f.dec.DecodeSubtitle(f.pkts[i]); err == nil && sub != nil {
			start, end, _ := subtitleTimes(sub, f.times[i], 0)
			if i+1 < len(f.times) {
				end = min(end, f.times[i+1])
			}
			cw, ch := f.canvas.X, f.canvas.Y
			if w, h := f.dec.Size(); w > 0 && h > 0 {
				cw, ch = w, h
			}
			if end != openEnd {
				end += f.offset
			}
			f.cue = bitmapCue(sub, start+f.offset, end, true, 0, cw, ch, rot)
			f.shows = len(f.cue.Bitmaps) > 0
		}
	}
	if !f.shows || t+f.offset < f.cue.Start || t+f.offset >= f.cue.End {
		return Cue{}, false
	}
	return f.cue, true
}

func (f *bitmapFile) close() {
	for _, p := range f.pkts {
		p.Free()
	}
	f.pkts = nil
	f.dec.Close()
}
