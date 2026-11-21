package player

import (
	"image"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/oneo-me/nsgo-media/internal/audioout"
)

func TestMain(m *testing.M) { nsgotest.Main(m) }

func fixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func silent(fill audioout.Fill) (audioout.Output, error) {
	return audioout.NewNull(48000, 2, fill), nil
}

// play runs the app's display frames in real time until cond holds.
func play(t *testing.T, app *nsgotest.App, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		app.Advance(16 * time.Millisecond)
		time.Sleep(8 * time.Millisecond)
	}
}

func TestPlayerPlaysAndShowsFrames(t *testing.T) {
	var p *Player
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		p = New(cx)
		p.openOutput = silent
		p.Open(fixture(t, "av.mkv"))
		p.Play()
		return View{Player: p, TestID: "video", Style: core.Style{Width: core.Pt(320), Height: core.Pt(180)}}
	})
	if p.State() != Opening {
		t.Fatalf("state %v before the source opened", p.State())
	}
	play(t, app, 3*time.Second, func() bool { return p.State() == Playing })
	if info := p.Info(); info == nil || len(info.Tracks) != 4 || info.Duration < time.Second {
		t.Fatalf("info %+v", info)
	}
	if w, h := p.VideoSize(); w != 160 || h != 90 {
		t.Fatalf("video size %d×%d", w, h)
	}
	play(t, app, 3*time.Second, func() bool { return p.Position() > 500*time.Millisecond })
	if f := p.frame.GetUntracked(); f.img == nil || f.version == 0 {
		t.Fatal("no frame shown")
	}
	p.Pause()
	app.Settle()
	at := p.Position()
	play(t, app, time.Second, func() bool { return true })
	time.Sleep(100 * time.Millisecond)
	app.Advance(16 * time.Millisecond)
	if p.State() != Paused || p.Position() != at {
		t.Fatalf("paused at %v, now %v in %v", at, p.Position(), p.State())
	}
	p.Seek(1500*time.Millisecond, Exact)
	p.Play()
	play(t, app, 3*time.Second, func() bool { return p.State() == Ended })
	if p.Position() < 1900*time.Millisecond {
		t.Errorf("ended at %v", p.Position())
	}
	p.Close()
	if p.State() != Idle || p.frame.GetUntracked().img != nil {
		t.Errorf("after Close: %v", p.State())
	}
}

func TestPlayerSubtitlesAndTracks(t *testing.T) {
	var p *Player
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		p = New(cx)
		p.openOutput = silent
		p.Open(fixture(t, "av.mkv"))
		return View{Player: p}
	})
	play(t, app, 3*time.Second, func() bool { return p.Info() != nil })
	if err := p.AddSubtitles(fixture(t, "sub.srt")); err != nil {
		t.Fatal(err)
	}
	ext := p.Info().Tracks[len(p.Info().Tracks)-1]
	if !ext.External || ext.Kind != Subtitle {
		t.Fatalf("added track %+v", ext)
	}
	if err := p.SelectSubtitle(ext.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SelectAudio(2); err != nil {
		t.Fatal(err)
	}
	p.Play()
	play(t, app, 3*time.Second, func() bool { return len(p.Cues()) == 1 })
	if c := p.Cues()[0]; c.Text != "Hello" {
		t.Errorf("cue %+v", c)
	}
	if p.AudioTrack() != 2 {
		t.Errorf("audio track %d", p.AudioTrack())
	}
	play(t, app, 3*time.Second, func() bool { return p.Position() > 1400*time.Millisecond })
	if c := p.Cues(); len(c) != 1 || c[0].Text != "World" {
		t.Errorf("cues at %v: %+v", p.Position(), c)
	}
}

func TestPlayerExternalAudioWithHeaders(t *testing.T) {
	header := http.Header{"Referer": {"https://www.example.com/"}}
	files := http.FileServer(http.Dir(filepath.Join("..", "testdata")))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != header.Get("Referer") {
			http.Error(w, "no referer", http.StatusForbidden)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer srv.Close()
	var p, refused *Player
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		p = New(cx)
		p.openOutput = silent
		p.Open(srv.URL+"/dash-video.mp4", Headers(header), ExternalAudio(srv.URL+"/dash-audio.m4a", header))
		p.Play()
		refused = New(cx)
		refused.openOutput = silent
		refused.Open(srv.URL+"/dash-video.mp4", Headers(header), ExternalAudio(srv.URL+"/dash-audio.m4a", nil))
		return View{Player: p}
	})
	play(t, app, 3*time.Second, func() bool { return p.State() == Playing && refused.State() == Failed })
	if tr := p.Info().Tracks; len(tr) != 2 || !tr[1].External || tr[1].Kind != Audio || p.AudioTrack() != tr[1].ID {
		t.Fatalf("tracks %+v, audio %d", tr, p.AudioTrack())
	}
	// Turning the sound off and on again moves the clock between sources.
	if err := p.SelectAudio(-1); err != nil {
		t.Fatal(err)
	}
	play(t, app, 2*time.Second, func() bool { return p.Position() > 600*time.Millisecond })
	if err := p.SelectAudio(1); err != nil {
		t.Fatal(err)
	}
	play(t, app, 3*time.Second, func() bool { return p.State() == Ended })
}

func TestPlayerFailsOnMissingFile(t *testing.T) {
	var p *Player
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		p = New(cx)
		p.Open(filepath.Join(t.TempDir(), "missing.mkv"))
		return core.View{}
	})
	play(t, app, 3*time.Second, func() bool { return p.State() == Failed })
	if p.Err() == nil {
		t.Fatal("no error")
	}
}

func TestFitRect(t *testing.T) {
	box := core.Size{Width: 200, Height: 100}
	if x, y, w, h := fitRect(box, 100, 100, Contain); x != 50 || y != 0 || w != 100 || h != 100 {
		t.Errorf("contain %v %v %v %v", x, y, w, h)
	}
	if x, y, w, h := fitRect(box, 100, 100, Cover); x != 0 || y != -50 || w != 200 || h != 200 {
		t.Errorf("cover %v %v %v %v", x, y, w, h)
	}
	if x, y, w, h := fitRect(box, 100, 100, Fill); x != 0 || y != 0 || w != 200 || h != 100 {
		t.Errorf("fill %v %v %v %v", x, y, w, h)
	}
}

func TestPlayerStepsAndSnapshots(t *testing.T) {
	var p *Player
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		p = New(cx)
		p.openOutput = silent
		p.Open(fixture(t, "av.mkv"))
		p.Seek(time.Second, Exact)
		return View{Player: p, Style: core.Style{Width: core.Pt(40), Height: core.Pt(23)}}
	})
	play(t, app, 3*time.Second, func() bool { return p.State() == Paused && p.Position() == time.Second })
	p.StepBackward()
	p.StepBackward()
	play(t, app, 3*time.Second, func() bool { return p.Position() == 920*time.Millisecond })
	p.StepForward()
	play(t, app, 3*time.Second, func() bool { return p.Position() == 960*time.Millisecond })
	if f := p.frame.GetUntracked(); f.img == nil || f.img.Bounds().Dx() >= 160 {
		t.Fatalf("frame shown %v, want one the view's size", f.img.Bounds())
	}
	img, err := p.Snapshot(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 160 || b.Dy() != 90 {
		t.Errorf("snapshot %v, want the source's 160×90", b)
	}
	if img, _ = p.Snapshot(80, 80); img.Bounds().Dx() != 80 || img.Bounds().Dy() != 45 {
		t.Errorf("snapshot in 80×80 is %v", img.Bounds())
	}
}

func TestPlayerChoosesVariants(t *testing.T) {
	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(fixture(t, "hls")))))
	defer srv.Close()
	var p *Player
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		p = New(cx)
		p.openOutput = silent
		p.Open(srv.URL + "/hls/master.m3u8")
		p.Play()
		return View{Player: p, TestID: "video", Style: core.Style{Width: core.Pt(320), Height: core.Pt(180)}}
	})
	play(t, app, 5*time.Second, func() bool { return p.State() == Playing })
	info := p.Info()
	vs := info.Variants()
	if len(vs) != 2 || info.Live || !info.Seekable || !p.AutoVariant() {
		t.Fatalf("variants %+v, live %v, seekable %v, auto %v", vs, info.Live, info.Seekable, p.AutoVariant())
	}
	if err := p.SelectVariant(vs[0].ID); err != nil {
		t.Fatal(err)
	}
	if p.AutoVariant() || p.VideoTrack() != vs[0].ID {
		t.Fatalf("auto %v, video track %d after choosing %d", p.AutoVariant(), p.VideoTrack(), vs[0].ID)
	}
	play(t, app, 5*time.Second, func() bool {
		f := p.frame.GetUntracked()
		return f.img != nil && f.img.Rect.Dx() == vs[0].Width
	})
	if w, _ := p.VideoSize(); w != vs[0].Width {
		t.Errorf("video width %d, want the variant's %d", w, vs[0].Width)
	}
	if err := p.SelectVariant(-1); err != nil || !p.AutoVariant() {
		t.Errorf("auto: %v, %v", err, p.AutoVariant())
	}
}

func TestPlayerBitmapSubtitles(t *testing.T) {
	var p *Player
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		p = New(cx)
		p.openOutput = silent
		p.Open(fixture(t, "bitmap.mkv"))
		return View{Player: p, Style: core.Style{Width: core.Pt(320), Height: core.Pt(320)}}
	})
	play(t, app, 3*time.Second, func() bool { return p.Info() != nil })
	if err := p.SelectSubtitle(1); err != nil {
		t.Fatal(err)
	}
	p.Seek(2100*time.Millisecond, Exact)
	p.Play()
	play(t, app, 3*time.Second, func() bool { return len(p.Cues()) == 1 })
	c := p.Cues()[0]
	// C's clear at 6 s is not read yet.
	if c.Start != 2*time.Second || c.End != 0 || c.Generation == 0 || len(c.Bitmaps) != 1 {
		t.Fatalf("cue %+v", c)
	}
	picture := p.PictureBounds(core.Size{Width: 320, Height: 320}, Contain)
	got := c.Place(c.Bitmaps[0], picture)
	want := core.Bounds{Origin: core.Point{X: 40, Y: 70 + 120}, Size: core.Size{Width: 60, Height: 24}}
	if got != want {
		t.Errorf("C placed at %+v, want %+v", got, want)
	}

	p.Pause()
	app.Settle()
	held := p.Cues()
	until := time.Now().Add(300 * time.Millisecond)
	play(t, app, time.Second, func() bool { return time.Now().After(until) })
	if !sameCues(p.Cues(), held) {
		t.Errorf("cues while paused %+v, then %+v", held, p.Cues())
	}
	p.Seek(6500*time.Millisecond, Exact)
	app.Settle()
	if len(p.Cues()) != 0 {
		t.Errorf("cues after seeking past C: %+v", p.Cues())
	}

	if err := p.AddSubtitles(fixture(t, "vobsub.idx")); err != nil {
		t.Fatal(err)
	}
	ext := p.Info().Tracks[len(p.Info().Tracks)-1]
	if err := p.SelectSubtitle(ext.ID); err != nil {
		t.Fatal(err)
	}
	p.Seek(700*time.Millisecond, Exact)
	play(t, app, 3*time.Second, func() bool { return len(p.Cues()) == 1 })
	if c := p.Cues()[0]; c.Start != 500*time.Millisecond || c.Generation != 0 || c.Bitmaps[0].X != 60 {
		t.Errorf("VobSub cue %+v", c)
	}
}

func TestPlaceOnTheView(t *testing.T) {
	// A 40×12 bitmap at 60,70 of a 160×90 canvas over a 160×90 picture
	// in a 320×320 view.
	c := Cue{Canvas: image.Pt(160, 90)}
	b := Bitmap{X: 60, Y: 70, Image: image.NewRGBA(image.Rect(0, 0, 40, 12))}
	box := core.Size{Width: 320, Height: 320}
	cover := 320.0 / 90
	for _, tc := range []struct {
		fit  Fit
		want [4]float64
	}{
		{Contain, [4]float64{120, 70 + 140, 80, 24}},
		{Cover, [4]float64{(320-160*cover)/2 + 60*cover, 70 * cover, 40 * cover, 12 * cover}},
		{Fill, [4]float64{120, 70 * 320.0 / 90, 80, 12 * 320.0 / 90}},
	} {
		x, y, w, h := fitRect(box, 160, 90, tc.fit)
		picture := core.Bounds{Origin: core.Point{X: core.Points(x), Y: core.Points(y)}, Size: core.Size{Width: core.Points(w), Height: core.Points(h)}}
		got := c.Place(b, picture)
		have := [4]float64{float64(got.Origin.X), float64(got.Origin.Y), float64(got.Size.Width), float64(got.Size.Height)}
		for i := range have {
			if math.Abs(have[i]-tc.want[i]) > 0.01 {
				t.Errorf("fit %d: placed at %v, want %v", tc.fit, have, tc.want)
				break
			}
		}
	}
	// An anamorphic DVD canvas stretches over the picture.
	dvd := Cue{Canvas: image.Pt(720, 480)}
	picture := core.Bounds{Size: core.Size{Width: 854, Height: 480}}
	if got := dvd.Place(Bitmap{X: 360, Y: 400, Image: image.NewRGBA(image.Rect(0, 0, 72, 48))}, picture); math.Abs(float64(got.Origin.X)-427) > 0.01 || got.Origin.Y != 400 || math.Abs(float64(got.Size.Width)-85.4) > 0.01 {
		t.Errorf("DVD bitmap placed at %+v", got)
	}
}
