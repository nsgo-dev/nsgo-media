package pipeline

import (
	"bytes"
	"testing"
	"time"
)

// settle ticks until the steps asked for are done, returning the shown
// frame's time.
func settle(t *testing.T, p *Pipeline) time.Duration {
	t.Helper()
	run(t, p, 5*time.Second, func(Status) bool { return p.steps == 0 && p.filling == 0 })
	return shownPTS(p)
}

func step(t *testing.T, p *Pipeline, n int) time.Duration {
	t.Helper()
	p.Step(n)
	return settle(t, p)
}

// frameTimes opens src paused at its first frame and steps forward to
// the last, returning every frame's time; the last step stops there.
func frameTimes(t *testing.T, src string) (*Pipeline, []time.Duration) {
	t.Helper()
	p := open(t, src, nil)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	all := []time.Duration{shownPTS(p)}
	for {
		got := step(t, p, 1)
		if got == all[len(all)-1] {
			break
		}
		if got < all[len(all)-1] {
			t.Fatalf("stepped forward from %v to %v", all[len(all)-1], got)
		}
		all = append(all, got)
		if len(all) > 200 {
			t.Fatal("no end")
		}
	}
	return p, all
}

func TestStepBackwardAndForward(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames int
	}{{"av.mkv", 50}, {"vfr.mkv", 44}} {
		t.Run(tc.name, func(t *testing.T) {
			p, all := frameTimes(t, fixture(t, tc.name))
			if len(all) != tc.frames {
				t.Fatalf("%d frames stepping forward, want %d", len(all), tc.frames)
			}
			// From the last frame back to the first, then stopping there.
			for i := len(all) - 2; i >= 0; i-- {
				if got := step(t, p, -1); got != all[i] {
					t.Fatalf("step back to frame %d showed %v, want %v", i, got, all[i])
				}
			}
			if got := step(t, p, -1); got != all[0] {
				t.Fatalf("step back at the first frame showed %v", got)
			}
			// Alternating, with steps asked for together adding up.
			i := 0
			for _, n := range []int{5, -2, 3, -1, 1, -4, 20, -3, -3, 7, -1, 30, -2, 1, 1} {
				i = min(max(i+n, 0), len(all)-1)
				if got := step(t, p, n); got != all[i] {
					t.Fatalf("step %+d to frame %d showed %v, want %v", n, i, got, all[i])
				}
			}
			p.Step(-1)
			p.Step(-1)
			p.Step(-1)
			i -= 3
			if got := settle(t, p); got != all[i] {
				t.Fatalf("three steps back showed %v, want %v", got, all[i])
			}
			// Stepping forward stops at the last frame.
			if got := step(t, p, len(all)+5); got != all[len(all)-1] {
				t.Fatalf("stepping past the end showed %v", got)
			}
			if p.Position() != all[len(all)-1] {
				t.Errorf("position %v at the last frame %v", p.Position(), all[len(all)-1])
			}
		})
	}
}

func TestStepBackwardHistoryBounds(t *testing.T) {
	p, all := frameTimes(t, fixture(t, "av.mkv"))
	p.hist.mu.Lock()
	p.hist.maxFrames = 5
	p.hist.mu.Unlock()
	for i := len(all) - 2; i >= len(all)-20; i-- {
		if got := step(t, p, -1); got != all[i] {
			t.Fatalf("step back showed %v, want %v", got, all[i])
		}
		if n, _ := p.hist.stats(); n > 5 {
			t.Fatalf("history holds %d frames, bound 5", n)
		}
	}
	// A byte bound below one picture still keeps two.
	p.hist.mu.Lock()
	p.hist.maxFrames, p.hist.maxBytes = historyFrames, 1
	p.hist.mu.Unlock()
	p.hist.clear()
	for i := len(all) - 21; i >= len(all)-26; i-- {
		if got := step(t, p, -1); got != all[i] {
			t.Fatalf("step back showed %v, want %v", got, all[i])
		}
		if n, _ := p.hist.stats(); n > 2 {
			t.Fatalf("history holds %d frames over its byte bound", n)
		}
	}
}

func TestSeekDropsHistory(t *testing.T) {
	p := open(t, fixture(t, "av.mkv"), nil)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	p.Seek(1500*time.Millisecond, true)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	step(t, p, -3)
	if n, _ := p.hist.stats(); n == 0 {
		t.Fatal("stepping back kept no history")
	}
	p.Seek(500*time.Millisecond, true)
	if n, _ := p.hist.stats(); n != 0 {
		t.Fatalf("history holds %d frames after a seek", n)
	}
	st := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	at := st.Frame.PTS - p.start
	if at < 480*time.Millisecond || at > 520*time.Millisecond {
		t.Fatalf("seek showed %v", at)
	}
	if got := step(t, p, -1); got != at-40*time.Millisecond {
		t.Fatalf("step back after the seek showed %v, want %v", got, at-40*time.Millisecond)
	}
}

func TestResumeAfterStepBackward(t *testing.T) {
	p := open(t, fixture(t, "av.mkv"), nil)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	p.Seek(1300*time.Millisecond, true)
	run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	at := step(t, p, -8)
	if p.Position() != at {
		t.Fatalf("position %v on the frame at %v", p.Position(), at)
	}
	p.SetPaused(false)
	if n, _ := p.hist.stats(); n != 0 {
		t.Errorf("history holds %d frames while playing", n)
	}
	// Playback starts from the chosen frame, with the sound there too.
	st := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	if got := st.Frame.PTS - p.start; got != at {
		t.Fatalf("resumed at %v, want %v", got, at)
	}
	began := time.Now()
	last := st.Frame.PTS
	st = run(t, p, 2*time.Second, func(s Status) bool {
		if s.Frame != nil {
			if s.Frame.PTS <= last {
				t.Fatalf("frame %v after %v", s.Frame.PTS, last)
			}
			last = s.Frame.PTS
		}
		return time.Since(began) > 300*time.Millisecond
	})
	if moved := st.Position - at; moved < 200*time.Millisecond || moved > 400*time.Millisecond {
		t.Errorf("moved %v in 0.3 s from %v", moved, at)
	}
}

func TestSnapshot(t *testing.T) {
	p := open(t, fixture(t, "av.mkv"), nil)
	p.SetOutputSize(40, 20)
	p.Seek(time.Second, true)
	st := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	if b := st.Frame.Image.Bounds(); b.Dx() != 40 {
		t.Fatalf("frame %v for a 40×20 view", b)
	}
	for _, tc := range []struct{ w, h, ww, wh int }{
		{0, 0, 160, 90}, {80, 0, 80, 45}, {0, 30, 53, 30}, {100, 100, 100, 56}, {1000, 1000, 160, 90},
	} {
		img, err := p.Snapshot(tc.w, tc.h)
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != tc.ww || b.Dy() != tc.wh {
			t.Errorf("snapshot in %d×%d is %v, want %d×%d", tc.w, tc.h, b, tc.ww, tc.wh)
		}
	}
	// A frame the history gave snapshots too.
	step(t, p, -2)
	if img, err := p.Snapshot(0, 0); err != nil || img.Bounds().Dx() != 160 {
		t.Fatalf("snapshot after stepping back: %v %v", img, err)
	}
	t.Logf("decoded on %q", p.HardwareDevice())
}

func TestSnapshotRotated(t *testing.T) {
	p := open(t, fixture(t, "rotated.mp4"), nil)
	st := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	img, err := p.Snapshot(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 96 {
		t.Fatalf("snapshot %v, want 64×96", b)
	}
	// Converted the same way, the snapshot is the frame shown.
	if !bytes.Equal(img.Pix, st.Frame.Image.Pix) {
		t.Error("the snapshot differs from the frame shown")
	}
	if img, _ = p.Snapshot(32, 0); img.Bounds().Dx() != 32 || img.Bounds().Dy() != 48 {
		t.Errorf("shrunk snapshot %v, want 32×48", img.Bounds())
	}
}

func TestSnapshotWithoutFrame(t *testing.T) {
	p := New(Options{OpenOutput: silent})
	defer p.Close()
	if _, err := p.Snapshot(0, 0); err == nil {
		t.Error("a snapshot before any frame")
	}
}
