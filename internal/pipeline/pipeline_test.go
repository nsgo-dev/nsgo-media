package pipeline

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo-media/internal/audioout"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join(testdata(), name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// silent plays through the null output, paced as a device is.
func silent(fill audioout.Fill) (audioout.Output, error) {
	return audioout.NewNull(48000, 2, fill), nil
}

type events struct {
	mu   sync.Mutex
	list []Event
}

func (e *events) add(ev Event) {
	e.mu.Lock()
	e.list = append(e.list, ev)
	e.mu.Unlock()
}

func (e *events) has(k EventKind) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range e.list {
		if ev.Kind == k {
			return true
		}
	}
	return false
}

func open(t *testing.T, src string, ev *events) *Pipeline {
	t.Helper()
	opts := Options{OpenOutput: silent}
	if ev != nil {
		opts.OnEvent = ev.add
	}
	p := New(opts)
	if _, err := p.Open(src); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// run ticks the presenter as a 120 Hz display would until cond holds.
func run(t *testing.T, p *Pipeline, timeout time.Duration, cond func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		st := p.Tick()
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out at %v (shown %v)", st.Position, shownPTS(p))
		}
		time.Sleep(8 * time.Millisecond)
	}
}

func shownPTS(p *Pipeline) time.Duration {
	if f := p.Shown(); f != nil {
		return f.PTS - p.start
	}
	return -1
}

func TestOpenInfo(t *testing.T) {
	p := New(Options{OpenOutput: silent})
	defer p.Close()
	info, err := p.Open(fixture(t, "av.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Tracks) != 4 || len(info.Chapters) != 2 {
		t.Fatalf("tracks %+v chapters %+v", info.Tracks, info.Chapters)
	}
	if info.Chapters[1].Title != "Two" || info.Chapters[1].Start != time.Second {
		t.Errorf("chapter %+v", info.Chapters[1])
	}
	if p.AudioTrack() != 1 {
		t.Errorf("audio track %d, want the default, 1", p.AudioTrack())
	}
}

func TestPlaysToTheEndInOrder(t *testing.T) {
	ev := &events{}
	p := open(t, fixture(t, "av.mkv"), ev)
	first := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	if first.Frame.PTS-p.start != 0 {
		t.Fatalf("first frame at %v", first.Frame.PTS-p.start)
	}
	if b := first.Frame.Image.Bounds(); b.Dx() != 160 || b.Dy() != 90 {
		t.Fatalf("frame %v", b)
	}
	p.SetPaused(false)
	began := time.Now()
	last, shown := time.Duration(-1), 0
	st := run(t, p, 5*time.Second, func(s Status) bool {
		if s.Frame != nil {
			if s.Frame.PTS <= last {
				t.Fatalf("frame %v after %v", s.Frame.PTS, last)
			}
			last = s.Frame.PTS
			shown++
		}
		return s.Ended
	})
	took := time.Since(began)
	if took < 1700*time.Millisecond || took > 3*time.Second {
		t.Errorf("2 s played in %v", took)
	}
	if shown < 40 {
		t.Errorf("%d frames shown of 50", shown)
	}
	if st.Position < 1900*time.Millisecond {
		t.Errorf("ended at %v", st.Position)
	}
	if !ev.has(EventEnded) {
		t.Error("no ended event")
	}
}

func TestExactAndFastSeekWhilePaused(t *testing.T) {
	p := open(t, fixture(t, "av.mkv"), nil)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })

	p.Seek(1300*time.Millisecond, true)
	st := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	if got := st.Frame.PTS - p.start; got != 1280*time.Millisecond && got != 1320*time.Millisecond {
		t.Errorf("exact seek to 1.3 s showed %v", got)
	}
	if st.Position < 1250*time.Millisecond || st.Position > 1350*time.Millisecond {
		t.Errorf("position %v after an exact seek", st.Position)
	}

	// Keyframes are a second apart: a fast seek lands on 1 s.
	p.Seek(1700*time.Millisecond, false)
	st = run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	if got := st.Frame.PTS - p.start; got != time.Second {
		t.Errorf("fast seek to 1.7 s showed %v, want the keyframe at 1 s", got)
	}

	// Stepping shows the next frames, 40 ms apart.
	for want := 1040 * time.Millisecond; want <= 1120*time.Millisecond; want += 40 * time.Millisecond {
		p.Step(1)
		st = run(t, p, time.Second, func(s Status) bool { return s.Frame != nil })
		if got := st.Frame.PTS - p.start; got != want {
			t.Fatalf("step showed %v, want %v", got, want)
		}
	}
}

func TestSeekWhilePlayingDropsStaleFrames(t *testing.T) {
	p := open(t, fixture(t, "av.mkv"), nil)
	p.SetPaused(false)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Position > 300*time.Millisecond })
	p.Seek(1500*time.Millisecond, true)
	st := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	if got := st.Frame.PTS - p.start; got < 1480*time.Millisecond {
		t.Fatalf("a frame from before the seek showed: %v", got)
	}
	run(t, p, 3*time.Second, func(s Status) bool {
		if s.Frame != nil && s.Frame.PTS-p.start < 1480*time.Millisecond {
			t.Fatalf("stale frame %v", s.Frame.PTS-p.start)
		}
		return s.Ended
	})
}

func TestRateAndLoop(t *testing.T) {
	p := open(t, fixture(t, "av.mkv"), nil)
	p.SetRate(2)
	p.SetPaused(false)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Position > 100*time.Millisecond })
	began, from := time.Now(), p.Position()
	st := run(t, p, 2*time.Second, func(s Status) bool { return time.Since(began) > 400*time.Millisecond })
	if moved := st.Position - from; moved < 600*time.Millisecond || moved > 1000*time.Millisecond {
		t.Errorf("moved %v in 0.4 s at 2×", moved)
	}
	p.SetLoop(true)
	wrapped := false
	prev := st.Position
	run(t, p, 4*time.Second, func(s Status) bool {
		if s.Position < prev-500*time.Millisecond {
			wrapped = true
		}
		prev = s.Position
		return wrapped && s.Position > 200*time.Millisecond
	})
	if p.Tick().Ended {
		t.Error("ended while looping")
	}
}

// Without the picture the sound plays on, ticked as a background timer
// would, and the picture comes back where it plays.
func TestAudioOnly(t *testing.T) {
	p := open(t, fixture(t, "av.mkv"), nil)
	p.SetPaused(false)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Position > 200*time.Millisecond })
	p.SetAudioOnly(true)
	// The frames decoded before may still show; none after them decodes.
	from := p.Position() + 500*time.Millisecond
	shown := 0
	deadline := time.Now().Add(2 * time.Second)
	var st Status
	for st = p.Tick(); st.Position < 1200*time.Millisecond; st = p.Tick() {
		if st.Frame != nil && st.Frame.PTS-p.start > from {
			shown++
		}
		if st.Ended || time.Now().After(deadline) {
			t.Fatalf("stopped at %v, ended %v", st.Position, st.Ended)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if shown > 0 {
		t.Errorf("%d frames from after %v shown without the picture", shown, from)
	}
	p.SetAudioOnly(false)
	st = run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	if got := st.Frame.PTS - p.start; got < 1150*time.Millisecond {
		t.Errorf("the picture came back at %v, after 1.2 s", got)
	}
	run(t, p, 3*time.Second, func(s Status) bool { return s.Ended })
}

func TestVideoOnlyWithoutAudioClock(t *testing.T) {
	p := open(t, fixture(t, "rotated.mp4"), nil)
	st := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	if b := st.Frame.Image.Bounds(); b.Dx() != 64 || b.Dy() != 96 {
		t.Fatalf("rotated frame %v, want 64×96", b)
	}
	p.SetPaused(false)
	began := time.Now()
	run(t, p, 3*time.Second, func(s Status) bool { return s.Ended })
	if took := time.Since(began); took < 800*time.Millisecond {
		t.Errorf("1 s played in %v", took)
	}
}

func TestSubtitles(t *testing.T) {
	p := open(t, fixture(t, "av.mkv"), nil)
	p.SetPaused(false)
	run(t, p, 3*time.Second, func(s Status) bool { return s.Position > 700*time.Millisecond })
	cues := p.Cues(3)
	if len(cues) != 1 || cues[0].Text != "Hello" || cues[0].Start != 500*time.Millisecond {
		t.Errorf("embedded cues at %v: %+v", p.Position(), cues)
	}
	trs, err := p.AddSubtitles(fixture(t, "sub.srt"))
	if err != nil {
		t.Fatal(err)
	}
	tr := trs[0]
	if len(trs) != 1 || !tr.External || tr.ID != 4 {
		t.Errorf("external track %+v", tr)
	}
	if cues := p.Cues(tr.ID); len(cues) != 1 || cues[0].Text != "Hello" {
		t.Errorf("external cues %+v", cues)
	}
}

func TestHTTP(t *testing.T) {
	data, err := os.ReadFile(fixture(t, "av.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	var ranged sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranged.Store(r.Header.Get("Range"), true)
		}
		http.ServeContent(w, r, "av.mkv", time.Time{}, strings.NewReader(string(data)))
	}))
	defer srv.Close()
	p := open(t, srv.URL+"/av.mkv", nil)
	p.SetPaused(false)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Position > 200*time.Millisecond })
	if p.Buffered() < time.Second {
		t.Errorf("buffered %v", p.Buffered())
	}
	p.Seek(1500*time.Millisecond, true)
	run(t, p, 4*time.Second, func(s Status) bool { return s.Ended })
}

func TestSeveralAndCloseJoins(t *testing.T) {
	before := runtime.NumGoroutine()
	var ps []*Pipeline
	for range 4 {
		p := New(Options{OpenOutput: silent})
		if _, err := p.Open(fixture(t, "av.mkv")); err != nil {
			t.Fatal(err)
		}
		p.SetPaused(false)
		ps = append(ps, p)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		done := 0
		for _, p := range ps {
			if p.Tick().Position > 500*time.Millisecond {
				done++
			}
		}
		if done == len(ps) {
			break
		}
		time.Sleep(8 * time.Millisecond)
	}
	for _, p := range ps {
		if p.Position() < 500*time.Millisecond {
			t.Errorf("a player is at %v", p.Position())
		}
		p.Close()
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Errorf("%d goroutines before, %d after Close", before, after)
	}
}

func TestCloseDuringOpen(t *testing.T) {
	// A server that never answers: Close must still return.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	p := New(Options{OpenOutput: silent})
	done := make(chan error, 1)
	go func() {
		_, err := p.Open(srv.URL + "/stuck.mkv")
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	p.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("open succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Open did not return after Close")
	}
}

func TestHDRToneMappingAndSnapshot(t *testing.T) {
	for _, clip := range []string{"hdr10.mkv", "hlg.mkv"} {
		for _, sw := range []bool{false, true} {
			mode := "auto"
			if sw {
				mode = "software"
			}
			t.Run(clip+"/"+mode, func(t *testing.T) {
				p := New(Options{OpenOutput: silent, Software: sw})
				if _, err := p.Open(fixture(t, clip)); err != nil {
					t.Fatal(err)
				}
				defer p.Close()
				st := run(t, p, 3*time.Second, func(s Status) bool { return s.Frame != nil })
				if img := st.Frame.Image; img != nil {
					w := img.RGBAAt(40, 22)
					pk := img.RGBAAt(120, 22)
					bk := img.RGBAAt(40, 67)
					rd := img.RGBAAt(140, 67)
					if w.R < 210 || w.G < 210 || w.B < 210 {
						t.Errorf("frame ref white = %+v, want >= 210", w)
					}
					if pk.R < 245 || pk.R <= w.R {
						t.Errorf("frame peak = %+v, want >= 245 and > ref white (%d)", pk, w.R)
					}
					if bk.R > 10 || bk.G > 10 || bk.B > 10 {
						t.Errorf("frame black = %+v, want <= 10", bk)
					}
					if rd.R < 200 || rd.G > 40 || rd.B > 40 {
						t.Errorf("frame red = %+v, want R>=200, G<=40, B<=40", rd)
					}
				}
				snap, err := p.Snapshot(0, 0)
				if err != nil {
					t.Fatal(err)
				}
				w := snap.RGBAAt(40, 22)
				pk := snap.RGBAAt(120, 22)
				bk := snap.RGBAAt(40, 67)
				rd := snap.RGBAAt(140, 67)
				if w.R < 210 || w.G < 210 || w.B < 210 {
					t.Errorf("snapshot ref white = %+v, want >= 210", w)
				}
				if pk.R < 245 || pk.R <= w.R {
					t.Errorf("snapshot peak = %+v, want >= 245 and > ref white (%d)", pk, w.R)
				}
				if bk.R > 10 || bk.G > 10 || bk.B > 10 {
					t.Errorf("snapshot black = %+v, want <= 10", bk)
				}
				if rd.R < 200 || rd.G > 40 || rd.B > 40 {
					t.Errorf("snapshot red = %+v, want R>=200, G<=40, B<=40", rd)
				}
			})
		}
	}
}

func TestUploadHDRPlanes(t *testing.T) {
	for _, clip := range []string{"hdr10.mkv", "hlg.mkv"} {
		for _, sw := range []bool{false, true} {
			mode := "auto"
			if sw {
				mode = "software"
			}
			t.Run(clip+"/"+mode, func(t *testing.T) {
				p := New(Options{OpenOutput: silent, Software: sw})
				p.SetGPU(GPUUpload)
				if _, err := p.Open(fixture(t, clip)); err != nil {
					t.Fatal(err)
				}
				defer p.Close()
				st := run(t, p, 3*time.Second, func(s Status) bool { return s.Frame != nil })
				pic := st.Frame.GPU
				if st.Frame.Image != nil || pic == nil || pic.Kind != GPUUpload || !pic.Deep {
					t.Fatalf("frame = %+v, want P010 planes to upload", st.Frame)
				}
				if pic.Width != 160 || pic.Height != 90 || pic.ColorTrc != st.Frame.src.ColorTrc() {
					t.Fatalf("picture = %+v", pic)
				}
				// A 10-bit sample at the top of 16 bits.
				luma := func(x, y int) int {
					pl := pic.Planes[0]
					o := y*int(pl.Pitch) + x*2
					return int(binary.LittleEndian.Uint16(pl.Data[o:])) >> 6
				}
				if w, b := luma(40, 22), luma(40, 67); w < 400 || b > 100 {
					t.Errorf("luma of white %d and black %d", w, b)
				}
				if len(pic.Planes[1].Data) < int(pic.Planes[1].Pitch)*45 {
					t.Errorf("chroma holds %d bytes", len(pic.Planes[1].Data))
				}
				snap, err := p.Snapshot(0, 0)
				if err != nil {
					t.Fatal(err)
				}
				if w := snap.RGBAAt(40, 22); w.R < 210 || w.G < 210 || w.B < 210 {
					t.Errorf("snapshot ref white = %+v, want >= 210", w)
				}
			})
		}
	}
	// SDR pictures still convert to RGBA.
	p := New(Options{OpenOutput: silent})
	p.SetGPU(GPUUpload)
	if _, err := p.Open(fixture(t, "av.mkv")); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if st := run(t, p, 3*time.Second, func(s Status) bool { return s.Frame != nil }); st.Frame.Image == nil || st.Frame.GPU != nil {
		t.Fatalf("SDR frame = %+v, want RGBA", st.Frame)
	}
}
