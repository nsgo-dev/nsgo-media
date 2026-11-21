package pipeline

import (
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// TestMeasurePlayback plays NSGO_MEDIA_MEASURE (a file or URL) for up to
// 10 s as a 60 Hz display shows it, converting frames at
// NSGO_MEDIA_MEASURE_SIZE (WxH, default the source's size), and reports the
// frames shown and dropped, the decoder and the CPU time used. It is for
// real machines and skipped without the variable.
func TestMeasurePlayback(t *testing.T) {
	src := os.Getenv("NSGO_MEDIA_MEASURE")
	if src == "" {
		t.Skip("set NSGO_MEDIA_MEASURE to a video to measure")
	}
	p := New(Options{OpenOutput: silent, Software: os.Getenv("NSGO_MEDIA_MEASURE_SOFTWARE") != ""})
	defer p.Close()
	info, err := p.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if s := os.Getenv("NSGO_MEDIA_MEASURE_SIZE"); s != "" {
		var w, h int
		for i := range s {
			if s[i] == 'x' {
				w, _ = strconv.Atoi(s[:i])
				h, _ = strconv.Atoi(s[i+1:])
			}
		}
		p.SetOutputSize(w, h)
	}
	v := p.track(p.VideoTrack())
	run(t, p, 5*time.Second, func(s Status) bool { return s.Frame != nil })
	p.SetPaused(false)
	length := min(10*time.Second, info.Duration-time.Second)
	cpu0 := cpuTime()
	began := time.Now()
	shown := 0
	tick := time.NewTicker(time.Second / 60)
	defer tick.Stop()
	var last Status
	for time.Since(began) < length {
		<-tick.C
		last = p.Tick()
		if last.Frame != nil {
			shown++
		}
	}
	wall := time.Since(began)
	cpu := cpuTime() - cpu0
	expected := int(last.Position.Seconds() * v.FrameRate)
	if v.FrameRate > 60 {
		expected = int(last.Position.Seconds() * 60)
	}
	size := "?"
	if f := p.Shown(); f != nil {
		size = strconv.Itoa(f.Image.Rect.Dx()) + "x" + strconv.Itoa(f.Image.Rect.Dy())
	}
	late, empty := p.Drops()
	t.Logf("%d late frames skipped, %d display frames found none decoded", late, empty)
	t.Logf("%s %dx%d@%.0f on %s/%s, decoder %q, frames at %s: played %v in %v, %d of %d frames shown (%d dropped), CPU %.0f%% of one core",
		v.Codec, v.Width, v.Height, v.FrameRate, runtime.GOOS, runtime.GOARCH, p.HardwareDevice(), size,
		last.Position.Round(time.Millisecond), wall.Round(time.Millisecond), shown, expected, max(expected-shown, 0),
		100*cpu.Seconds()/wall.Seconds())
}
