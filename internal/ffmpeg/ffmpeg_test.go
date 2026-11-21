package ffmpeg

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func mustLoad(t *testing.T) {
	t.Helper()
	if err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadChecksVersions(t *testing.T) {
	mustLoad(t)
	if v := Version(); v == "" {
		t.Fatal("no version")
	}
	if err := checkVersions(); err != nil {
		t.Fatal(err)
	}
	t.Logf("FFmpeg %s from %q", Version(), Dir())
}

func TestOpenReadsStreamsAndChapters(t *testing.T) {
	mustLoad(t)
	in, err := Open(fixture(t, "av.mkv"), nil, new(atomic.Bool))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	f := in.Format
	if d := f.Duration(); d < 1900*time.Millisecond || d > 2100*time.Millisecond {
		t.Errorf("duration %v, want about 2s", d)
	}
	if !strings.Contains(f.FormatName(), "matroska") {
		t.Errorf("format %q", f.FormatName())
	}
	if f.Metadata()["title"] != "NSGo Media fixture" {
		t.Errorf("metadata %v", f.Metadata())
	}
	streams := f.Streams()
	if len(streams) != 4 {
		t.Fatalf("%d streams, want 4", len(streams))
	}
	v := streams[0].Params()
	if v.Type() != MediaVideo || v.CodecName() != "h264" || v.Width() != 160 || v.Height() != 90 {
		t.Errorf("video stream %v %s %dx%d", v.Type(), v.CodecName(), v.Width(), v.Height())
	}
	if fr := streams[0].AvgFrameRate().Float(); fr < 24.9 || fr > 25.1 {
		t.Errorf("frame rate %v", fr)
	}
	a := streams[2].Params()
	if a.Type() != MediaAudio || a.SampleRate() != 44100 || a.Channels() != 1 || streams[2].Metadata()["language"] != "jpn" {
		t.Errorf("second audio %v %d Hz %d ch %v", a.Type(), a.SampleRate(), a.Channels(), streams[2].Metadata())
	}
	if s := streams[3].Params(); s.Type() != MediaSubtitle || s.CodecName() != "subrip" {
		t.Errorf("subtitle %v %s", s.Type(), s.CodecName())
	}
	ch := f.Chapters()
	if len(ch) != 2 {
		t.Fatalf("%d chapters", len(ch))
	}
	if ch[1].TimeBase().Duration(ch[1].Start()) != time.Second || ch[1].Metadata()["title"] != "Two" {
		t.Errorf("chapter 2 at %v named %q", ch[1].TimeBase().Duration(ch[1].Start()), ch[1].Metadata()["title"])
	}
}

func TestRotation(t *testing.T) {
	mustLoad(t)
	in, err := Open(fixture(t, "rotated.mp4"), nil, new(atomic.Bool))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if r := in.Format.Streams()[0].Params().Rotation(); r != 90 {
		t.Fatalf("rotation %d, want 90 clockwise", r)
	}
}

// memSource is a Source over bytes, as an HTTP body is in the pipeline.
type memSource struct{ *bytes.Reader }

func (m memSource) Size() int64 { return m.Reader.Size() }

func TestDecodeVideoAndAudioFromGoSource(t *testing.T) {
	mustLoad(t)
	data, err := os.ReadFile(fixture(t, "av.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	in, err := Open("av.mkv", memSource{bytes.NewReader(data)}, new(atomic.Bool))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	streams := in.Format.Streams()
	vdec, err := NewDecoder(streams[0].Params(), streams[0].TimeBase(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer vdec.Close()
	adec, err := NewDecoder(streams[1].Params(), streams[1].TimeBase(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer adec.Close()
	sdec, err := NewDecoder(streams[3].Params(), streams[3].TimeBase(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer sdec.Close()

	pkt, frame := NewPacket(), NewFrame()
	defer pkt.Free()
	defer frame.Free()
	var scaler Scaler
	defer scaler.Close()
	res := NewResampler(48000, 2)
	defer res.Close()
	buf := Alloc(160 * 90 * 4)
	defer Free(buf)

	videoFrames, audioSamples := 0, 0
	var texts []string
	var lastPTS int64 = -1
	for {
		err := in.Format.ReadPacket(pkt)
		if IsEOF(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch pkt.StreamIndex() {
		case 0:
			if err := vdec.Send(pkt); err != nil {
				t.Fatal(err)
			}
			for vdec.Receive(frame) == nil {
				if frame.Width() != 160 || frame.Height() != 90 {
					t.Fatalf("frame %dx%d", frame.Width(), frame.Height())
				}
				ts := frame.BestEffortTimestamp()
				if ts <= lastPTS {
					t.Fatalf("timestamps not increasing: %d after %d", ts, lastPTS)
				}
				lastPTS = ts
				if err := scaler.Scale(frame, buf, 160*4, 160, 90); err != nil {
					t.Fatal(err)
				}
				videoFrames++
				frame.Unref()
			}
		case 1:
			if err := adec.Send(pkt); err != nil {
				t.Fatal(err)
			}
			for adec.Receive(frame) == nil {
				s, err := res.Convert(frame)
				if err != nil {
					t.Fatal(err)
				}
				audioSamples += len(s) / 2
				frame.Unref()
			}
		case 3:
			rects, _, _, err := sdec.DecodeSubtitle(pkt)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rects {
				texts = append(texts, r.ASS)
			}
		}
		pkt.Unref()
	}
	// Draining gives the frames the decoder held back for reordering.
	if err := vdec.Send(nil); err != nil {
		t.Fatal(err)
	}
	for vdec.Receive(frame) == nil {
		videoFrames++
		frame.Unref()
	}
	if videoFrames != 50 {
		t.Errorf("%d video frames, want 50", videoFrames)
	}
	if audioSamples < 80000 {
		t.Errorf("%d audio samples, want about 96000", audioSamples)
	}
	if len(texts) != 2 || !strings.HasSuffix(texts[0], "Hello") {
		t.Errorf("subtitles %q", texts)
	}
	// testsrc2's picture is not black: the conversion wrote pixels.
	px := unsafe.Slice((*byte)(buf), 160*90*4)
	var sum int
	for i := 0; i < len(px); i += 4 {
		sum += int(px[i]) + int(px[i+1]) + int(px[i+2])
		if px[i+3] != 255 {
			t.Fatalf("alpha %d at %d", px[i+3], i)
		}
	}
	if sum == 0 {
		t.Error("the converted picture is black")
	}
}

func TestTempoKeepsLengthProportional(t *testing.T) {
	mustLoad(t)
	tempo, err := NewTempo(48000, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer tempo.Close()
	in := make([]float32, 4800*2)
	for i := range in {
		in[i] = float32(i%100) / 100
	}
	var out []float32
	for range 20 {
		if out, err = tempo.Process(in, out); err != nil {
			t.Fatal(err)
		}
	}
	// 2 s of input at double speed is about 1 s, less what atempo holds.
	got := len(out) / 2
	if got < 40000 || got > 50000 {
		t.Fatalf("%d samples out, want about 48000", got)
	}
}

func TestDisplayRotation(t *testing.T) {
	const one = 1 << 16
	for _, c := range []struct {
		m    []int32
		want int
	}{
		{[]int32{one, 0, 0, 0, one, 0, 0, 0, 1 << 30}, 0},
		{[]int32{0, one, 0, -one, 0, 0, 0, 0, 1 << 30}, 90},
		{[]int32{-one, 0, 0, 0, -one, 0, 0, 0, 1 << 30}, 180},
		{[]int32{0, -one, 0, one, 0, 0, 0, 0, 1 << 30}, 270},
	} {
		if got := displayRotation(c.m); got != c.want {
			t.Errorf("rotation of %v = %d, want %d", c.m, got, c.want)
		}
	}
}
