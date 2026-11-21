package pipeline

import (
	"image"
	"testing"
	"time"
)

const ms = time.Millisecond

// bitmap.mkv's subtitle tracks hold the same cues: PGS, DVD as FFmpeg
// encodes it, which joins B into A's bitmap, and DVB.
var bitmapTracks = []struct {
	id    int
	codec string
	// rects at 1 s, and the ends of the first cue
	updated  int
	firstEnd time.Duration
}{
	{1, "hdmv_pgs_subtitle", 2, 1000 * ms},
	{2, "dvd_subtitle", 1, 989 * ms},
	{3, "dvb_subtitle", 2, 1000 * ms},
}

// cueAt plays until the position is within [from, to) and returns the
// track's cues there.
func cueAt(t *testing.T, p *Pipeline, track int, from, to time.Duration) []Cue {
	t.Helper()
	var cues []Cue
	run(t, p, 5*time.Second, func(s Status) bool {
		if s.Position >= to {
			t.Fatalf("played past %v to %v", to, s.Position)
		}
		cues = p.Cues(track)
		return s.Position >= from
	})
	return cues
}

func TestBitmapSubtitlesTimesAndClears(t *testing.T) {
	for _, tr := range bitmapTracks {
		t.Run(tr.codec, func(t *testing.T) {
			p := open(t, fixture(t, "bitmap.mkv"), nil)
			if got := p.track(tr.id); got.Kind != Subtitle || got.Codec != tr.codec {
				t.Fatalf("track %d is %+v", tr.id, got)
			}
			p.SelectSubtitle(tr.id)
			p.SetPaused(false)

			cues := cueAt(t, p, tr.id, 650*ms, 950*ms)
			if len(cues) != 1 || cues[0].Start != 500*ms || cues[0].End != tr.firstEnd || cues[0].Gen == 0 {
				t.Fatalf("cues at 0.65 s: %+v", cues)
			}
			c := cues[0]
			if c.Canvas != image.Pt(160, 90) || len(c.Bitmaps) != 1 {
				t.Fatalf("canvas %v, bitmaps %+v", c.Canvas, c.Bitmaps)
			}
			b := c.Bitmaps[0]
			if b.X != 60 || b.Y != 70 || b.Image.Rect.Dx() != 40 || b.Image.Rect.Dy() != 12 {
				t.Errorf("bitmap at %d,%d of %v", b.X, b.Y, b.Image.Rect)
			}
			if px := b.Image.RGBAAt(5, 5); px.A != 255 || px.R < 200 || px.G < 200 {
				t.Errorf("bitmap pixel %v, want opaque white", px)
			}

			// The update at 1 s replaces the cue during its display.
			cues = cueAt(t, p, tr.id, 1150*ms, 1450*ms)
			if len(cues) != 1 || cues[0].Start != 1000*ms || len(cues[0].Bitmaps) != tr.updated {
				t.Fatalf("cues at 1.15 s: %+v", cues)
			}
			// The clear at 1.5 s, or the DVD cue's own end, leaves none.
			if cues = cueAt(t, p, tr.id, 1650*ms, 1950*ms); len(cues) != 0 {
				t.Fatalf("cues at 1.65 s: %+v", cues)
			}
		})
	}
}

func TestBitmapSubtitlesHoldAndSeek(t *testing.T) {
	for _, tr := range bitmapTracks {
		t.Run(tr.codec, func(t *testing.T) {
			p := open(t, fixture(t, "bitmap.mkv"), nil)
			p.SelectSubtitle(tr.id)
			p.SetPaused(false)
			cues := cueAt(t, p, tr.id, 2100*ms, 2600*ms)
			if len(cues) != 1 || cues[0].Start != 2000*ms {
				t.Fatalf("cues at 2.1 s: %+v", cues)
			}
			// C shows until 6 s, which is not read yet: PGS' has no end.
			if tr.codec == "hdmv_pgs_subtitle" && cues[0].End != openEnd {
				t.Errorf("C ends at %v before its clear is read", cues[0].End)
			}

			p.SetPaused(true)
			run(t, p, time.Second, func(Status) bool { return true })
			time.Sleep(200 * ms)
			p.Tick()
			if held := p.Cues(tr.id); len(held) != 1 || held[0].Start != 2000*ms {
				t.Fatalf("cues while paused: %+v", held)
			}
			// Stepping backward decodes again, keeping what shows.
			shown := shownPTS(p)
			p.Step(-1)
			run(t, p, 3*time.Second, func(Status) bool { return shownPTS(p) < shown })
			if held := p.Cues(tr.id); len(held) != 1 || held[0].Start != 2000*ms {
				t.Fatalf("cues after a step backward: %+v", held)
			}

			// Past C's clear, which was never read, C does not show.
			gen := p.genA.Load()
			p.Seek(6500*ms, true)
			if c := p.Cues(tr.id); len(c) != 0 {
				t.Fatalf("cues just after seeking to 6.5 s: %+v", c)
			}
			run(t, p, 3*time.Second, func(Status) bool { return shownPTS(p) >= 6400*ms })
			time.Sleep(100 * ms)
			p.Tick()
			if c := p.Cues(tr.id); len(c) != 0 {
				t.Fatalf("cues at 6.5 s: %+v", c)
			}

			// D, read after the seek, shows with its generation.
			p.Seek(7400*ms, true)
			var c []Cue
			run(t, p, 3*time.Second, func(Status) bool { c = p.Cues(tr.id); return len(c) == 1 })
			if c[0].Start != 7200*ms || c[0].Gen <= gen || c[0].Bitmaps[0].X != 100 {
				t.Errorf("cue at 7.4 s: %+v", c[0])
			}
		})
	}
}

func TestBitmapSubtitlesDecodeOnlyWhenChosen(t *testing.T) {
	p := open(t, fixture(t, "bitmap.mkv"), nil)
	p.SetPaused(false)
	run(t, p, 3*time.Second, func(s Status) bool { return s.Position >= 700*ms })
	if c := p.Cues(1); len(c) != 0 {
		t.Errorf("an unchosen track has cues %+v", c)
	}
}

func TestVobSubFile(t *testing.T) {
	p := open(t, fixture(t, "bitmap.mkv"), nil)
	trs, err := p.AddSubtitles(fixture(t, "vobsub.idx"))
	if err != nil {
		t.Fatal(err)
	}
	if len(trs) != 1 || trs[0].ID != 4 || trs[0].Codec != "dvd_subtitle" || trs[0].Language != "en" || !trs[0].External {
		t.Fatalf("tracks %+v", trs)
	}
	id := trs[0].ID
	p.SelectSubtitle(id)
	for _, c := range []struct {
		at, start, end time.Duration
		x, y           int
	}{
		{700 * ms, 500 * ms, 989 * ms, 60, 70},
		{1700 * ms, 0, 0, 0, 0},
		{3000 * ms, 2000 * ms, 5993 * ms, 20, 60},
		{6500 * ms, 0, 0, 0, 0},
		{900 * ms, 500 * ms, 989 * ms, 60, 70},
	} {
		p.Seek(c.at, true)
		cues := p.Cues(id)
		if c.start == 0 {
			if len(cues) != 0 {
				t.Errorf("cues at %v: %+v", c.at, cues)
			}
			continue
		}
		if len(cues) != 1 || cues[0].Start != c.start || cues[0].End != c.end || cues[0].Canvas != image.Pt(160, 90) {
			t.Fatalf("cues at %v: %+v", c.at, cues)
		}
		if b := cues[0].Bitmaps[0]; b.X != c.x || b.Y != c.y {
			t.Errorf("bitmap at %v: %d,%d", c.at, b.X, b.Y)
		} else if px := b.Image.RGBAAt(5, 5); px.A != 255 || px.R < 200 || px.G < 200 {
			t.Errorf("bitmap pixel at %v: %v, want the index's white", c.at, px)
		}
	}
}

func TestTurnCue(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 12))
	img.Pix[3] = 255 // the top left pixel
	for _, c := range []struct {
		deg         int
		canvas      image.Point
		x, y, w, h  int
		cornerX, cy int
	}{
		{0, image.Pt(160, 90), 60, 70, 40, 12, 0, 0},
		{90, image.Pt(90, 160), 8, 60, 12, 40, 11, 0},
		{180, image.Pt(160, 90), 60, 8, 40, 12, 39, 11},
		{270, image.Pt(90, 160), 70, 60, 12, 40, 0, 39},
	} {
		cue := Cue{Canvas: image.Pt(160, 90), Bitmaps: []Bitmap{{X: 60, Y: 70, Image: img}}}
		turnCue(&cue, c.deg)
		b := cue.Bitmaps[0]
		if cue.Canvas != c.canvas || b.X != c.x || b.Y != c.y || b.Image.Rect.Dx() != c.w || b.Image.Rect.Dy() != c.h {
			t.Errorf("%d°: canvas %v, bitmap at %d,%d of %v", c.deg, cue.Canvas, b.X, b.Y, b.Image.Rect)
		}
		if b.Image.RGBAAt(c.cornerX, c.cy).A != 255 {
			t.Errorf("%d°: the corner is not at %d,%d", c.deg, c.cornerX, c.cy)
		}
	}
}

func TestBitmapSubtitlesChosenLate(t *testing.T) {
	p := open(t, fixture(t, "bitmap.mkv"), nil)
	p.SetPaused(false)
	run(t, p, 8*time.Second, func(s Status) bool { return s.Position >= 5*time.Second })
	// What was read before the track was chosen tells nothing of its cues.
	p.SetPaused(true)
	p.Seek(2500*ms, true)
	p.SelectSubtitle(1)
	run(t, p, 3*time.Second, func(Status) bool { return len(p.Cues(1)) == 1 })
	p.Seek(6500*ms, true)
	if c := p.Cues(1); len(c) != 0 {
		t.Fatalf("cues just after seeking to 6.5 s: %+v", c)
	}
	run(t, p, 3*time.Second, func(Status) bool { return shownPTS(p) >= 6400*ms })
	if c := p.Cues(1); len(c) != 0 {
		t.Fatalf("cues at 6.5 s: %+v", c)
	}
}
