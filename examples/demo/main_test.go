package main

import (
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

	// Stop goes back to the start.
	app.Press("id:stop")
	play(t, app, 2*time.Second, func() bool { return timeText() == "0:00 / 0:02" })
}
