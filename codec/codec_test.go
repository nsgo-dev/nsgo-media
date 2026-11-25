package codec_test

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/oneo-me/nsgo-media/codec"
)

func makeBGRAFrame(w, h, step int) codec.Frame {
	stride := w * 4
	buf := make([]byte, stride*h)
	for y := range h {
		for x := range w {
			off := y*stride + x*4
			buf[off+0] = byte((x + step*7) & 0xff)         // B
			buf[off+1] = byte((y + step*5) & 0xff)         // G
			buf[off+2] = byte(((x ^ y) + step*11) & 0xff)  // R
			buf[off+3] = 0xff                              // A
		}
	}
	return codec.Frame{Format: codec.BGRA, Width: w, Height: h, Data: buf, Stride: stride}
}

func makeNV12Frame(w, h, step int) codec.Frame {
	yPlane := make([]byte, w*h)
	uvPlane := make([]byte, w*(h/2))
	for y := range h {
		for x := range w {
			yPlane[y*w+x] = byte(16 + ((x + y + step*9) % 219))
		}
	}
	for y := range h / 2 {
		for x := range w / 2 {
			uvPlane[y*w+x*2] = byte(16 + ((x*2 + step*3) % 224))
			uvPlane[y*w+x*2+1] = byte(16 + ((y*2 + step*5) % 224))
		}
	}
	return codec.Frame{Format: codec.NV12, Width: w, Height: h, Data: yPlane, Stride: w, UV: uvPlane, UVStride: w}
}

func TestCapabilities(t *testing.T) {
	encs, err := codec.Encoders()
	if err != nil {
		t.Fatalf("Encoders: %v", err)
	}
	if len(encs) == 0 {
		t.Fatal("Encoders returned no available H.264 encoder")
	}
	decs, err := codec.Decoders()
	if err != nil {
		t.Fatalf("Decoders: %v", err)
	}
	if len(decs) == 0 {
		t.Fatal("Decoders returned no available H.264 decoder")
	}
	t.Logf("encoders: %+v", encs)
	t.Logf("decoders: %+v", decs)
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format codec.PixelFormat
		w, h   int
		swDec  bool
	}{
		{"BGRA_640x360_hw", codec.BGRA, 640, 360, false},
		{"NV12_640x360_hw", codec.NV12, 640, 360, false},
		{"BGRA_1280x720_sw", codec.BGRA, 1280, 720, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := codec.NewEncoder(codec.EncoderConfig{
				Codec:   codec.H264,
				Width:   tc.w,
				Height:  tc.h,
				FPS:     60,
				BitRate: 4_000_000,
			})
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			defer enc.Close()

			dec, err := codec.NewDecoder(codec.DecoderConfig{
				Codec:        codec.H264,
				SoftwareOnly: tc.swDec,
			})
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			defer dec.Close()

			t.Logf("encoder=%s (hw=%v), decoder_hw=%v", enc.Name(), enc.Hardware(), dec.Hardware())

			decoded := 0
			for i := range 10 {
				var f codec.Frame
				if tc.format == codec.BGRA {
					f = makeBGRAFrame(tc.w, tc.h, i)
				} else {
					f = makeNV12Frame(tc.w, tc.h, i)
				}
				forceKey := i == 0 || i == 5
				au, err := enc.Encode(f, forceKey)
				if err != nil {
					t.Fatalf("frame %d Encode: %v", i, err)
				}
				if len(au.Data) == 0 {
					t.Fatalf("frame %d produced empty access unit (B-frame buffering?)", i)
				}
				if forceKey && !au.Keyframe {
					t.Errorf("frame %d requested keyframe, got non-keyframe", i)
				}
				img, err := dec.Decode(au.Data)
				if err != nil {
					t.Fatalf("frame %d Decode: %v", i, err)
				}
				if img != nil {
					if img.Bounds().Dx() != tc.w || img.Bounds().Dy() != tc.h {
						t.Fatalf("frame %d decoded %dx%d, want %dx%d", i, img.Bounds().Dx(), img.Bounds().Dy(), tc.w, tc.h)
					}
					decoded++
				}
			}
			if decoded == 0 {
				t.Fatal("no frames decoded")
			}
		})
	}
}

func TestDropRecoveryAndBitRateChange(t *testing.T) {
	const w, h = 640, 360
	enc, err := codec.NewEncoder(codec.EncoderConfig{
		Codec:   codec.H264,
		Width:   w,
		Height:  h,
		FPS:     60,
		BitRate: 4_000_000,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	dec, err := codec.NewDecoder(codec.DecoderConfig{Codec: codec.H264})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()

	// Encode frame 0 (keyframe) and decode it.
	au0, err := enc.Encode(makeBGRAFrame(w, h, 0), true)
	if err != nil {
		t.Fatalf("Encode 0: %v", err)
	}
	if _, err := dec.Decode(au0.Data); err != nil {
		t.Fatalf("Decode 0: %v", err)
	}

	// Simulate dropping frames 1..3 (never sent to decoder), then change bit
	// rate while running and request a keyframe to recover.
	for i := 1; i <= 3; i++ {
		if _, err := enc.Encode(makeBGRAFrame(w, h, i), false); err != nil {
			t.Fatalf("Encode %d: %v", i, err)
		}
	}
	if err := enc.SetBitRate(1_500_000); err != nil {
		t.Fatalf("SetBitRate: %v", err)
	}
	dec.Flush()

	auRec, err := enc.Encode(makeBGRAFrame(w, h, 4), true)
	if err != nil {
		t.Fatalf("Encode recovery: %v", err)
	}
	if !auRec.Keyframe {
		t.Error("recovery frame was not a keyframe")
	}
	img, err := dec.Decode(auRec.Data)
	if err != nil {
		t.Fatalf("Decode recovery: %v", err)
	}
	if img == nil || img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("recovery decode gave %v, want %dx%d", img, w, h)
	}
}

func TestSoftwareFallbackEncoder(t *testing.T) {
	const w, h = 320, 180
	enc, err := codec.NewEncoder(codec.EncoderConfig{
		Codec:        codec.H264,
		Width:        w,
		Height:       h,
		FPS:          30,
		BitRate:      1_000_000,
		SoftwareOnly: true,
	})
	if err != nil {
		t.Fatalf("NewEncoder(SoftwareOnly): %v", err)
	}
	defer enc.Close()
	if enc.Hardware() {
		t.Errorf("SoftwareOnly encoder reported Hardware() = true (%s)", enc.Name())
	}
	dec, err := codec.NewDecoder(codec.DecoderConfig{Codec: codec.H264, SoftwareOnly: true})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()

	var gotKey, gotImg bool
	// Some software encoders (e.g. Windows Media Foundation's software MFT)
	// prime several frames before emitting the first packet.
	for i := range 32 {
		au, err := enc.Encode(makeNV12Frame(w, h, i), i == 0)
		if err != nil {
			t.Fatalf("Encode %d: %v", i, err)
		}
		if len(au.Data) == 0 {
			continue
		}
		t.Logf("SoftwareOnly %s emitted frame %d (len=%d key=%v)", enc.Name(), i, len(au.Data), au.Keyframe)
		if au.Keyframe {
			gotKey = true
		}
		img, err := dec.Decode(au.Data)
		if err != nil {
			t.Fatalf("Decode %d: %v", i, err)
		}
		if img != nil {
			if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
				t.Fatalf("decoded %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), w, h)
			}
			gotImg = true
			break
		}
	}
	if !gotKey || !gotImg {
		t.Fatalf("SoftwareOnly encoder %s produced gotKey=%v gotImg=%v", enc.Name(), gotKey, gotImg)
	}
}

func TestAllAvailableEncoders(t *testing.T) {
	const w, h = 640, 360
	encs, err := codec.Encoders()
	if err != nil {
		t.Fatalf("Encoders: %v", err)
	}
	for _, cap := range encs {
		t.Run(cap.Name, func(t *testing.T) {
			enc, err := codec.NewEncoder(codec.EncoderConfig{
				Codec:        cap.Codec,
				Name:         cap.Name,
				SoftwareOnly: !cap.Hardware,
				Width:        w,
				Height:       h,
				FPS:          60,
				BitRate:      4_000_000,
			})
			if err != nil {
				t.Fatalf("NewEncoder(%s): %v", cap.Name, err)
			}
			defer enc.Close()

			dec, err := codec.NewDecoder(codec.DecoderConfig{Codec: cap.Codec})
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			defer dec.Close()

			var gotKey, gotImg bool
			for i := range 8 {
				au, err := enc.Encode(makeNV12Frame(w, h, i), i == 0)
				if err != nil {
					t.Fatalf("frame %d Encode: %v", i, err)
				}
				if len(au.Data) == 0 {
					continue
				}
				if au.Keyframe {
					gotKey = true
				}
				img, err := dec.Decode(au.Data)
				if err != nil {
					t.Fatalf("frame %d Decode: %v", i, err)
				}
				if img != nil {
					if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
						t.Fatalf("decoded %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), w, h)
					}
					gotImg = true
				}
			}
			if !gotKey || !gotImg {
				t.Fatalf("encoder %s: gotKey=%v gotImg=%v", cap.Name, gotKey, gotImg)
			}
		})
	}
}

func TestMeasureEncodeLatency(t *testing.T) {
	const w, h = 1920, 1080
	const warmup = 10
	const samples = 60

	enc, err := codec.NewEncoder(codec.EncoderConfig{
		Codec:   codec.H264,
		Width:   w,
		Height:  h,
		FPS:     60,
		BitRate: 12_000_000,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()

	// Pre-generate NV12 frames so we measure encoding, not test-frame generation.
	frames := make([]codec.Frame, 4)
	for i := range frames {
		frames[i] = makeNV12Frame(w, h, i)
	}
	for i := range warmup {
		if _, err := enc.Encode(frames[i%len(frames)], i == 0); err != nil {
			t.Fatalf("warmup %d: %v", i, err)
		}
	}

	durs := make([]time.Duration, 0, samples)
	var totalBytes int
	for i := range samples {
		start := time.Now()
		au, err := enc.Encode(frames[i%len(frames)], false)
		d := time.Since(start)
		if err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		durs = append(durs, d)
		totalBytes += len(au.Data)
	}
	slices.Sort(durs)
	var sum time.Duration
	for _, d := range durs {
		sum += d
	}
	mean := sum / time.Duration(len(durs))
	p50 := durs[len(durs)/2]
	p95 := durs[int(math.Ceil(float64(len(durs))*0.95))-1]
	t.Logf("1080p NV12 encode latency (%s, hw=%v): mean=%s p50=%s p95=%s min=%s max=%s avgBytes=%d",
		enc.Name(), enc.Hardware(),
		fmtDur(mean), fmtDur(p50), fmtDur(p95), fmtDur(durs[0]), fmtDur(durs[len(durs)-1]),
		totalBytes/samples)
}

func fmtDur(d time.Duration) string {
	return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
}
