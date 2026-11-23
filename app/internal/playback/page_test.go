package playback

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
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
	play(t, app, 5*time.Second, func() bool { return text("duration") == "0:02" && text("position") != "0:00" })

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
