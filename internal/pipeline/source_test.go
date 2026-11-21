package pipeline

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func openPair(t *testing.T, video, audio Source) *Pipeline {
	t.Helper()
	p := New(Options{OpenOutput: silent})
	t.Cleanup(p.Close)
	if _, err := p.OpenSources(video, &audio); err != nil {
		t.Fatal(err)
	}
	return p
}

// serveFiles serves testdata's files, refusing requests without header.
func serveFiles(t *testing.T, header http.Header, seen *sync.Map) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k := range header {
			if r.Header.Get(k) != header.Get(k) {
				seen.Store("refused", true)
				http.Error(w, "missing "+k, http.StatusForbidden)
				return
			}
		}
		seen.Store(r.URL.Path, true)
		data, err := os.ReadFile(fixture(t, strings.TrimPrefix(r.URL.Path, "/")))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, r.URL.Path, time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPHeaders(t *testing.T) {
	header := http.Header{
		"Referer":    {"https://www.example.com/"},
		"User-Agent": {"nsgo-media-test"},
		"Cookie":     {"SESSDATA=1"},
	}
	var seen sync.Map
	srv := serveFiles(t, header, &seen)
	video := Source{Location: srv.URL + "/dash-video.mp4", Header: header}
	audio := Source{Location: srv.URL + "/dash-audio.m4a", Header: header}

	p := openPair(t, video, audio)
	p.SetPaused(false)
	p.Seek(time.Second, true)
	run(t, p, 4*time.Second, func(s Status) bool { return s.Ended })
	if _, ok := seen.Load("refused"); ok {
		t.Error("a request went without the headers")
	}
	for _, path := range []string{"/dash-video.mp4", "/dash-audio.m4a"} {
		if _, ok := seen.Load(path); !ok {
			t.Errorf("%s was not read", path)
		}
	}

	for name, srcs := range map[string][2]Source{
		"video": {{Location: video.Location}, audio},
		"audio": {video, {Location: audio.Location, Header: http.Header{"Referer": header["Referer"]}}},
	} {
		p := New(Options{OpenOutput: silent})
		_, err := p.OpenSources(srcs[0], &srcs[1])
		p.Close()
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Errorf("%s without its headers opened: %v", name, err)
		}
	}
}

func TestExternalAudioInSync(t *testing.T) {
	p := openPair(t, Source{Location: fixture(t, "dash-video.mp4")}, Source{Location: fixture(t, "dash-audio.m4a")})
	if len(p.info.Tracks) != 2 {
		t.Fatalf("tracks %+v", p.info.Tracks)
	}
	if a := p.info.Tracks[1]; a.Kind != Audio || !a.External || a.ID != 1 || p.AudioTrack() != 1 {
		t.Fatalf("audio track %+v, playing %d", a, p.AudioTrack())
	}

	// synced checks a frame and the sound start together.
	synced := func(at time.Duration) {
		t.Helper()
		st := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
		_, first, have, _, _ := p.ring.status()
		if pts := st.Frame.PTS - p.start; pts < at-40*time.Millisecond || pts > at+40*time.Millisecond {
			t.Errorf("frame %v at %v", pts, at)
		}
		if !have || first-p.start < at-40*time.Millisecond || first-p.start > at+40*time.Millisecond {
			t.Errorf("sound starts at %v (%v), frame at %v", first-p.start, have, at)
		}
	}
	synced(0)
	p.Seek(1200*time.Millisecond, true)
	synced(1200 * time.Millisecond)
	// A step back reads only the video; resuming there seeks the sound too.
	p.Step(-1)
	st := run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	if pts := st.Frame.PTS - p.start; pts != 1160*time.Millisecond {
		t.Errorf("stepped back to %v", pts)
	}
	p.SetPaused(false)
	p.SetPaused(true)
	synced(1160 * time.Millisecond)

	// While playing, the frame shown is the one the sound has reached.
	check := func(s Status) {
		if s.Frame != nil {
			if d := s.Frame.PTS - p.start - s.Position; d < -50*time.Millisecond || d > 50*time.Millisecond {
				t.Errorf("frame %v shown at %v", s.Frame.PTS-p.start, s.Position)
			}
		}
	}
	p.Seek(200*time.Millisecond, true)
	p.SetPaused(false)
	run(t, p, 2*time.Second, func(s Status) bool { check(s); return s.Position > 300*time.Millisecond })
	// The sound queued at 1× plays out first.
	p.SetRate(2)
	began := time.Now()
	run(t, p, time.Second, func(s Status) bool { check(s); return time.Since(began) > 350*time.Millisecond })
	began, from := time.Now(), p.Position()
	st = run(t, p, 2*time.Second, func(s Status) bool { check(s); return time.Since(began) > 400*time.Millisecond })
	if moved := st.Position - from; moved < 600*time.Millisecond || moved > 1000*time.Millisecond {
		t.Errorf("moved %v in 0.4 s at 2×", moved)
	}
	run(t, p, 3*time.Second, func(s Status) bool { check(s); return s.Ended })
}

func TestBufferedIsTheLesser(t *testing.T) {
	p := openPair(t, Source{Location: fixture(t, "dash-video.mp4")}, Source{Location: fixture(t, "dash-audio.m4a")})
	run(t, p, 2*time.Second, func(s Status) bool { return s.Frame != nil })
	deadline := time.Now().Add(2 * time.Second)
	for p.Buffered() != p.info.Duration {
		if time.Now().After(deadline) {
			t.Fatalf("buffered %v of %v", p.Buffered(), p.info.Duration)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Hold keeps the demuxers from reading on.
	set := func(i int, b time.Duration) {
		p.mu.Lock()
		d := p.inputs[i]
		d.eof, d.hold, d.buffered = false, true, p.start+b
		p.mu.Unlock()
	}
	set(1, 700*time.Millisecond)
	if b := p.Buffered(); b != 700*time.Millisecond {
		t.Errorf("buffered %v with the audio read to 0.7 s", b)
	}
	set(0, 500*time.Millisecond)
	if b := p.Buffered(); b != 500*time.Millisecond {
		t.Errorf("buffered %v with the video read to 0.5 s", b)
	}
}
