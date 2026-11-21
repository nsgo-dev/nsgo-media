package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/nsgotest"
)

// TestMain gives the platform the main thread under nsgo test -target.
func TestMain(m *testing.M) { nsgotest.Main(m) }

func TestRootStarts(t *testing.T) {
	nsgotest.Start(t, Root)
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

func TestControlsDriveThePlayer(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("..", "..", "testdata", "av.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NSGO_MEDIA_DEMO_SOURCE", src)
	app := nsgotest.Start(t, Root)
	timeText := func() string { return app.Find("id:time").Text }

	// It opens the source and plays.
	play(t, app, 5*time.Second, func() bool { return timeText() == "0:01 / 0:02" })

	// Pausing holds the position.
	app.Press("id:play")
	held := timeText()
	until := time.Now().Add(300 * time.Millisecond)
	play(t, app, time.Second, func() bool { return time.Now().After(until) })
	if timeText() != held {
		t.Fatalf("moved while paused: %s, then %s", held, timeText())
	}

	// Stepping back and forward shows the frames around the position.
	app.Press("id:step-back")
	app.Press("id:step-back")
	app.Press("id:step")
	play(t, app, 2*time.Second, func() bool { return timeText() == "0:01 / 0:02" })

	// Stop goes back to the start.
	app.Press("id:stop")
	play(t, app, 2*time.Second, func() bool { return timeText() == "0:00 / 0:02" })
}

func TestQualityChoosesTheVariant(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(root)))
	defer srv.Close()
	t.Setenv("NSGO_MEDIA_DEMO_SOURCE", srv.URL+"/hls/master.m3u8")
	app := nsgotest.Start(t, Root)
	play(t, app, 10*time.Second, func() bool { return app.Find("id:time").Text == "0:01 / 0:06" })

	app.Press("id:quality")
	app.Press("role:option 90p · 112 kbit/s")
	play(t, app, 5*time.Second, func() bool { return app.Exists("text:90p · 112 kbit/s") && !app.Exists("text:Auto") })
	app.Press("id:quality")
	app.Press("role:option Auto")
	play(t, app, 5*time.Second, func() bool { return app.Exists("text:Auto") })
}
