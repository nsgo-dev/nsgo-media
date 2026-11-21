package player

import (
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
