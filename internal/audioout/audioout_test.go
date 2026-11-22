package audioout

import (
	"math"
	"sync/atomic"
	"testing"
	"time"
)

// TestDevicePlays plays a quiet tone on the default device and checks the
// device's count of played frames advances at its rate, and holds while
// paused. Machines without a device fall back to the null output.
func TestDevicePlays(t *testing.T) {
	var phase float64
	var calls atomic.Int64
	var out Output
	fill := func(dst []float32) {
		calls.Add(1)
		ch := 2
		if out != nil {
			ch = out.Channels()
		}
		for i := 0; i < len(dst); i += ch {
			v := float32(0.05 * math.Sin(phase))
			phase += 2 * math.Pi * 440 / 48000
			for c := 0; c < ch; c++ {
				dst[i+c] = v
			}
		}
	}
	out, err := Open(fill)
	if err != nil {
		t.Logf("no device, using the null output: %v", err)
	}
	defer out.Close()
	start := out.Played()
	time.Sleep(500 * time.Millisecond)
	played := out.Played() - start
	want := int64(out.Rate()) / 2
	if played < want/2 || played > want*3/2 {
		t.Fatalf("played %d frames in 0.5 s at %d Hz", played, out.Rate())
	}
	if calls.Load() == 0 {
		t.Fatal("fill was never called")
	}
	out.SetPaused(true)
	time.Sleep(100 * time.Millisecond)
	held := out.Played()
	time.Sleep(200 * time.Millisecond)
	if d := out.Played() - held; d > int64(out.Rate())/50 {
		t.Fatalf("played %d frames while paused", d)
	}
	out.SetPaused(false)
	time.Sleep(200 * time.Millisecond)
	if out.Played() <= held {
		t.Fatal("did not resume")
	}
	t.Logf("%T at %d Hz, %d channels", out, out.Rate(), out.Channels())
}

// TestDeviceIEC opens the default device for an AC-3 stream, and for
// DTS-HD and TrueHD on HDMI's high bit rate. A device driving no receiver
// refuses them, which is what the test checks on most machines; one that
// takes a stream plays pause bursts at its carrier's rate.
func TestDeviceIEC(t *testing.T) {
	pause := []byte{0x72, 0xF8, 0x1F, 0x4E, 3, 0, 32, 0, 4, 0, 0, 0, 0, 0, 0, 0}
	for _, tc := range []struct {
		f              IEC
		rate, channels int
	}{
		{IEC{Codec: "ac3", SampleRate: 48000, Channels: 6}, 48000, 2},
		{IEC{Codec: "dtshd", SampleRate: 48000, Channels: 6}, 192000, 8},
		{IEC{Codec: "truehd", SampleRate: 48000, Channels: 8}, 192000, 8},
	} {
		t.Run(tc.f.Codec, func(t *testing.T) {
			var calls atomic.Int64
			fill := func(dst []byte) {
				calls.Add(1)
				for i := range dst {
					dst[i] = pause[i%len(pause)]
				}
			}
			out, err := OpenIEC(tc.f, fill)
			if err != nil {
				t.Logf("refused: %v", err)
				// PCM still opens after the refusal.
				pcm, err := Open(func(dst []float32) { clear(dst) })
				if err != nil {
					t.Logf("no PCM device: %v", err)
				}
				pcm.Close()
				return
			}
			defer out.Close()
			if out.Rate() != tc.rate || out.Channels() != tc.channels {
				t.Fatalf("carrier %d Hz, %d channels", out.Rate(), out.Channels())
			}
			time.Sleep(500 * time.Millisecond)
			if played := out.Played(); played < int64(tc.rate)/4 || calls.Load() == 0 {
				t.Fatalf("played %d frames in 0.5 s", played)
			}
			t.Logf("%T passes %s through", out, tc.f.Codec)
		})
	}
}
