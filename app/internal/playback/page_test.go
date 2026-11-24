package playback

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/oneo-me/nsgo-media/app/internal/store"
)

// TestMain gives the platform the main thread under nsgo test -target,
// where the blank import of nsgo shows the page in a real window.
func TestMain(m *testing.M) { nsgotest.Main(m) }

// clip is one of NSGo Media's test clips.
func clip(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/oneo-me/nsgo-media").Output()
	if err != nil {
		t.Skipf("no NSGo Media module: %v", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "testdata", name)
}

// start shows a page playing src, with positions kept in a new folder.
func start(t *testing.T, src Source, positions *store.Positions) *nsgotest.App {
	t.Helper()
	if positions == nil {
		positions, _ = store.OpenPositions(filepath.Join(t.TempDir(), "positions.json"))
	}
	return nsgotest.Start(t, func(cx *core.Scope) core.Node {
		return core.View{Style: core.Style{FlexGrow: 1}, Children: []core.Node{
			Page{Source: func() Source { return src }, Positions: positions, OnClose: func() {}},
		}}
	}, nsgotest.Size(800, 500), nsgotest.Environment(core.Environment{Locale: "en-US"}))
}

// play runs display frames in real time, as the decoders run in it, until
// cond holds.
func play(t *testing.T, app *nsgotest.App, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out\n%s", app.Dump())
		}
		app.Advance(16 * time.Millisecond)
		time.Sleep(8 * time.Millisecond)
	}
}

func TestPagePlaysAndSeeks(t *testing.T) {
	app := start(t, Source{URL: clip(t, "av.mkv")}, nil)
	app.Find("text:av.mkv")
	text := func(id string) string { return app.Find("id:" + id).Text }
	// The time left shows the duration once pressed.
	play(t, app, 5*time.Second, func() bool { return strings.HasPrefix(text("duration"), "−0:0") && text("position") != "0:00" })
	app.Press("id:duration")
	if d := text("duration"); d != "0:02" {
		t.Fatalf("duration %q", d)
	}

	// Space pauses, and the arrow keys seek back to the start.
	app.Key("space")
	app.Key("left")
	play(t, app, 2*time.Second, func() bool { return text("position") == "0:00" })
	app.Find("label:Play")
}

func TestPageOffersToResume(t *testing.T) {
	src := Source{Key: "clip", URL: clip(t, "av.mkv")}
	positions, _ := store.OpenPositions(filepath.Join(t.TempDir(), "positions.json"))
	if err := positions.Set("clip", time.Second); err != nil {
		t.Fatal(err)
	}
	app := start(t, src, positions)
	app.Press("text:Resume at 0:01")
	app.WaitForGone("id:resume", time.Second)
	if nsgotest.Target() == "" {
		// Headless, the clock stands still but for the seek.
		play(t, app, 5*time.Second, func() bool { return app.Find("id:position").Text == "0:01" })
	}
}

func TestPageRefusesAnInvalidSource(t *testing.T) {
	app := start(t, Source{URL: "ftp://example.com/a.mkv"}, nil)
	app.Find("text:This file or URL cannot be played.")
}

func TestPageRetriesFromLastPosition(t *testing.T) {
	data, err := os.ReadFile(clip(t, "bitmap.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	cut := len(data) * 2 / 3
	gate := make(chan struct{})
	var failOpen, failMid atomic.Bool
	failMid.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failOpen.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		var off int
		if rng, ok := strings.CutPrefix(r.Header.Get("Range"), "bytes="); ok {
			start, _, _ := strings.Cut(rng, "-")
			off, _ = strconv.Atoi(start)
		}
		if !failMid.Load() || off >= cut {
			http.ServeContent(w, r, "bitmap.mkv", time.Time{}, bytes.NewReader(data))
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, len(data)-1, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)-off))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[off:cut])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-gate:
			if h, ok := w.(http.Hijacker); ok {
				if conn, _, err := h.Hijack(); err == nil {
					conn.Close()
				}
			}
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)

	app := start(t, Source{Key: "clip", URL: srv.URL + "/bitmap.mkv"}, nil)
	text := func(id string) string { return app.Find("id:" + id).Text }
	play(t, app, 5*time.Second, func() bool { return app.Exists("id:position") && text("position") == "0:01" })
	close(gate)
	play(t, app, 5*time.Second, func() bool { return app.Exists("id:error-retry") })

	// A retry that fails before opening keeps the position for the next try.
	failOpen.Store(true)
	app.Press("id:error-retry")
	play(t, app, 5*time.Second, func() bool {
		return app.Exists("id:error-detail") && strings.Contains(text("error-detail"), "503")
	})

	failOpen.Store(false)
	failMid.Store(false)
	app.Press("id:error-retry")
	play(t, app, 5*time.Second, func() bool {
		return !app.Exists("id:error-retry") && !app.Exists("id:loading")
	})
	app.Key("space")
	play(t, app, 2*time.Second, func() bool { return app.Exists("id:position") })
	if got := text("position"); got < "0:01" {
		t.Fatalf("retried at %q, want >= 0:01", got)
	}
	if app.Exists("id:resume") {
		t.Fatal("retrying offered to resume instead of continuing")
	}
}

func TestPagePlaysSeparateAudio(t *testing.T) {
	app := start(t, Source{URL: clip(t, "dash-video.mp4"), Audio: &Stream{URL: clip(t, "dash-audio.m4a")}}, nil)
	play(t, app, 5*time.Second, func() bool { return app.Find("id:position").Text != "0:00" })
	// One audio track and no subtitles leave nothing to choose.
	if app.Exists("id:tracks") {
		t.Fatal("the page offers a choice of one track")
	}
}

// TestPageFullScreenShortcuts needs a window to put in full screen.
func TestPageFullScreenShortcuts(t *testing.T) {
	if nsgotest.Target() == "" {
		t.Skip("full screen needs a desktop target")
	}
	app := start(t, Source{URL: clip(t, "av.mkv")}, nil)
	app.Key("f")
	app.WaitFor("label:Exit Full Screen", 5*time.Second)
	// macOS ignores a toggle while the window animates into full screen.
	until := time.Now().Add(2 * time.Second)
	play(t, app, 3*time.Second, func() bool { return time.Now().After(until) })
	app.Key("escape")
	app.WaitFor("label:/^Full Screen$/", 5*time.Second)
	app.Find("id:back")
}

// startWith shows a page playing src with handlers.
func startWith(t *testing.T, pg Page) *nsgotest.App {
	t.Helper()
	pg.Positions, _ = store.OpenPositions(filepath.Join(t.TempDir(), "positions.json"))
	pg.OnClose = func() {}
	return nsgotest.Start(t, func(cx *core.Scope) core.Node {
		return core.View{Style: core.Style{FlexGrow: 1}, Children: []core.Node{pg}}
	}, nsgotest.Size(800, 500), nsgotest.Environment(core.Environment{Locale: "en-US"}))
}

func TestPageOffersQualitiesAndReports(t *testing.T) {
	src := Source{Key: "clip", URL: clip(t, "av.mkv"), Quality: "hd", Qualities: []Quality{{"hd", "HD"}, {"sd", "SD"}}}
	var asked string
	var reported []time.Duration
	app := startWith(t, Page{
		Source:     func() Source { return src },
		OnQuality:  func(id string, _ time.Duration) { asked = id },
		OnPosition: func(key string, pos, _ time.Duration) { reported = append(reported, pos) },
	})
	app.Press("id:quality")
	app.Press("id:quality-1")
	if asked != "sd" {
		t.Fatalf("asked for quality %q", asked)
	}
	// Pausing saves the position, which the handler hears.
	play(t, app, 5*time.Second, func() bool { return app.Find("id:position").Text != "0:00" })
	app.Press("id:play")
	app.WaitUntil(time.Second, func() bool { return len(reported) > 0 })
}

func TestPageStartsAtStart(t *testing.T) {
	positions, _ := store.OpenPositions(filepath.Join(t.TempDir(), "positions.json"))
	positions.Set("clip", time.Second)
	app := start(t, Source{Key: "clip", URL: clip(t, "av.mkv"), Start: time.Second}, positions)
	// Starting at a position offers no other.
	if app.Exists("id:resume") {
		t.Fatal("the page offers to resume")
	}
	if nsgotest.Target() == "" {
		play(t, app, 5*time.Second, func() bool { return app.Find("id:position").Text == "0:01" })
	}
}

func TestPageDownloadsSubtitles(t *testing.T) {
	var referer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referer = r.Header.Get("Referer")
		w.Write([]byte("WEBVTT\n\n00:00.000 --> 00:05.000\nHello\n"))
	}))
	t.Cleanup(srv.Close)
	src := Source{URL: clip(t, "av.mkv"), Subtitles: []Subtitle{{Label: "English", Stream: Stream{URL: srv.URL + "/sub", Headers: http.Header{"Referer": {"https://example.com/"}}}}}}
	app := start(t, src, nil)
	play(t, app, 5*time.Second, func() bool { return app.Exists("text:English") || openSubtitles(app) })
	if referer != "https://example.com/" {
		t.Fatalf("Referer %q", referer)
	}
}

// openSubtitles opens the subtitle menu, reporting whether it lists a
// downloaded track.
func openSubtitles(app *nsgotest.App) bool {
	if !app.Exists("id:tracks") {
		return false
	}
	if !app.Exists("id:subtitle-off") {
		app.Press("id:tracks")
	}
	return app.Exists("id:menu >> text:English")
}

func TestPageTitlesAPart(t *testing.T) {
	src := Source{URL: clip(t, "av.mkv"), Title: "Lake – Ridge", Video: "Lake", Part: 1,
		Parts: []Part{{Label: "P1", Title: "Dawn"}, {Label: "P2", Title: "Ridge"}}}
	var asked = -1
	app := startWith(t, Page{Source: func() Source { return src }, OnPart: func(i int) { asked = i }})
	if got := app.Find("id:title").Text; got != "Ridge" {
		t.Fatalf("title %q", got)
	}
	if got := app.Find("id:subtitle").Text; got != "P2 · Lake" {
		t.Fatalf("subtitle %q", got)
	}
	// The last part has no next one; the menu over the parts plays another.
	if app.Exists("id:next") {
		t.Fatal("the last part offers a next one")
	}
	app.Press("id:parts")
	app.Press("id:part-0")
	if asked != 0 {
		t.Fatalf("asked for part %d", asked)
	}
}

// TestControlsBlurWithoutSystemGlass draws the controls as Liquid Glass
// only where the system draws it, and elsewhere a tinted blur.
func TestControlsBlurWithoutSystemGlass(t *testing.T) {
	if nsgotest.Target() != "" {
		t.Skip("the system is the target's")
	}
	for os, glass := range map[core.OS]bool{core.OSMacOS: true, core.OSWindows: false, core.OSLinux: false} {
		positions, _ := store.OpenPositions(filepath.Join(t.TempDir(), "positions.json"))
		src := Source{URL: clip(t, "av.mkv")}
		app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
			return core.View{Style: core.Style{FlexGrow: 1}, Children: []core.Node{
				Page{Source: func() Source { return src }, Positions: positions, OnClose: func() {}},
			}}
		}, nsgotest.Size(800, 500), nsgotest.Environment(core.Environment{Locale: "en-US", OS: os}))
		st := app.Find("id:controls").Style
		if (st.Glass != core.GlassNone) != glass || (st.Background == core.RGBA(0xffffff, 0.1)) == glass {
			t.Errorf("controls on %v: glass %v, background %v", os, st.Glass, st.Background)
		}
	}
}
