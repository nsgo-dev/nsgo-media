package mediafile

import (
	"errors"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

func mustLoad(t *testing.T) {
	t.Helper()
	if err := ffmpeg.Load(); err != nil {
		t.Fatal(err)
	}
}

// fill is the colour of every frame's pixels, and alphaFill that of the
// left half of frames with alpha, whose right half is transparent.
var (
	fill      = color.RGBA{R: 200, G: 100, B: 50, A: 255}
	alphaFill = color.NRGBA{R: 40, G: 160, B: 220, A: 128}
)

// frame is the i-th test frame: fill with a bar moving across it, or with
// alpha alphaFill's left half and a transparent right half.
func frame(i, w, h int, alpha bool) image.Image {
	if alpha {
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			for x := range w / 2 {
				img.SetNRGBA(x, y, alphaFill)
			}
		}
		return img
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, fill)
		}
	}
	bar := (i * 8) % w
	for y := range h / 4 {
		for x := bar; x < min(w, bar+8); x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	return img
}

// tone is n frames of a 440 Hz sine at rate with channels, interleaved.
func tone(n, rate, channels int) []float32 {
	s := make([]float32, n*channels)
	for i := range n {
		v := float32(0.3 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		for c := range channels {
			s[i*channels+c] = v
		}
	}
	return s
}

// written is what probe reads back from a file.
type written struct {
	format                string
	videoCodec, pixFmt    string
	width, height, frames int
	videoDuration         time.Duration
	alphaMode             string
	audioCodec            string
	audioRate, audioChans int
	audioDuration         time.Duration
	audioStart            time.Duration
	first                 []byte // the first frame decoded, RGBA
}

func probe(t *testing.T, path string) written {
	t.Helper()
	in, err := ffmpeg.Open(path, nil, new(atomic.Bool))
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer in.Close()
	w := written{format: in.Format.FormatName()}
	var video *ffmpeg.Stream
	for _, s := range in.Format.Streams() {
		par := s.Params()
		switch par.Type() {
		case ffmpeg.MediaVideo:
			video = s
			w.videoCodec, w.width, w.height = par.CodecName(), par.Width(), par.Height()
			w.pixFmt = ffmpeg.PixelFormatName(par.Format())
			w.alphaMode = s.Metadata()["alpha_mode"]
		case ffmpeg.MediaAudio:
			w.audioCodec, w.audioRate, w.audioChans = par.CodecName(), par.SampleRate(), par.Channels()
		}
	}
	if video == nil {
		t.Fatal("no video stream")
	}
	dec, err := ffmpeg.NewDecoder(video.Params(), video.TimeBase(), 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	pkt, f := ffmpeg.NewPacket(), ffmpeg.NewFrame()
	defer pkt.Free()
	defer f.Free()
	// Each stream's first timestamp and the end of its last packet.
	first, end := map[int]int64{}, map[int]int64{}
	got := func() {
		if w.first == nil && dec.Receive(f) == nil {
			if w.pixFmt == "" {
				w.pixFmt = ffmpeg.PixelFormatName(f.Format())
			}
			var s ffmpeg.Scaler
			defer s.Close()
			n := w.width * w.height * 4
			buf := ffmpeg.Alloc(n)
			defer ffmpeg.Free(buf)
			if err := s.Scale(f, buf, w.width*4, w.width, w.height); err != nil {
				t.Fatal(err)
			}
			w.first = append([]byte(nil), unsafe.Slice((*byte)(buf), n)...)
			f.Unref()
		}
	}
	for in.Format.ReadPacket(pkt) == nil {
		i := pkt.StreamIndex()
		if f, ok := first[i]; !ok || pkt.PTS() < f {
			first[i] = pkt.PTS()
		}
		end[i] = max(end[i], pkt.PTS()+pkt.DurationTS())
		if i == video.Index() {
			w.frames++
			if w.first == nil {
				dec.Send(pkt)
				got()
			}
		}
		pkt.Unref()
	}
	if w.first == nil {
		dec.Send(nil)
		got()
	}
	for _, s := range in.Format.Streams() {
		i, tb := s.Index(), s.TimeBase()
		start := first[i]
		if st := s.StartTime(); st != ffmpeg.NoPTS {
			// The start a container's edit list or codec delay gives.
			start = st
		}
		switch s.Params().Type() {
		case ffmpeg.MediaVideo:
			w.videoDuration = tb.Duration(end[i] - start)
		case ffmpeg.MediaAudio:
			w.audioStart, w.audioDuration = tb.Duration(start), tb.Duration(end[i]-start)
		}
	}
	return w
}

func near(a, b, tolerance time.Duration) bool { return a-b <= tolerance && b-a <= tolerance }

// pixelNear reports the first frame's pixel at x, y within 12 of c: the
// decoder takes a small picture whose bitstream has no colour description,
// as Media Foundation's HEVC encoder writes it, for BT.601 where the
// container says BT.709.
func pixelNear(w written, x, y int, c color.NRGBA) bool {
	i := (y*w.width + x) * 4
	p := w.first[i : i+4]
	for k, v := range []uint8{c.R, c.G, c.B, c.A} {
		if d := int(p[k]) - int(v); d < -12 || d > 12 {
			return false
		}
	}
	return true
}

type combo struct {
	name      string
	file      string
	container string
	codec     string
	alpha     bool
	rateNum   int
	rateDen   int
	bitrate   int
	quality   int
	audio     *AudioStream
	// want is the demuxer, video and audio codec read back.
	format, videoCodec, audioCodec string
}

func TestWritesEachCombination(t *testing.T) {
	mustLoad(t)
	stereo := &AudioStream{SampleRate: 48000, Channels: 2}
	for _, c := range []combo{
		{name: "H.264 AAC MP4", file: "a.mp4", rateNum: 30, audio: stereo, format: "mov,mp4", videoCodec: "h264", audioCodec: "aac"},
		{name: "H.264 MP4 at a bit rate", file: "rate.m4v", rateNum: 30000, rateDen: 1001, bitrate: 2_000_000, format: "mov,mp4", videoCodec: "h264"},
		{name: "HEVC AAC MP4", file: "hevc.mp4", codec: HEVC, rateNum: 25, audio: stereo, format: "mov,mp4", videoCodec: "hevc", audioCodec: "aac"},
		{name: "H.264 AAC MOV", file: "a.mov", rateNum: 24, quality: 60, audio: &AudioStream{SampleRate: 44100, Channels: 1}, format: "mov,mp4", videoCodec: "h264", audioCodec: "aac"},
		{name: "ProRes AAC MOV", file: "prores.mov", codec: ProRes, rateNum: 30, audio: stereo, format: "mov,mp4", videoCodec: "prores", audioCodec: "aac"},
		{name: "ProRes 4444 MOV with alpha", file: "alpha.mov", codec: ProRes4444, alpha: true, rateNum: 30, audio: stereo, format: "mov,mp4", videoCodec: "prores", audioCodec: "aac"},
		{name: "VP9 Opus WebM", file: "a.webm", rateNum: 30, audio: stereo, format: "matroska,webm", videoCodec: "vp9", audioCodec: "opus"},
		{name: "VP9 WebM with alpha, 44.1 kHz resampled", file: "alpha.webm", alpha: true, rateNum: 30, audio: &AudioStream{SampleRate: 44100, Channels: 2}, format: "matroska,webm", videoCodec: "vp9", audioCodec: "opus"},
		{name: "VP9 WebM at a bit rate", file: "rate.webm", container: WebM, codec: VP9, rateNum: 60, bitrate: 1_000_000, format: "matroska,webm", videoCodec: "vp9"},
	} {
		t.Run(c.name, func(t *testing.T) { testCombo(t, c) })
	}
}

func testCombo(t *testing.T, c combo) {
	const w, h, frames = 320, 240, 45
	path := filepath.Join(t.TempDir(), c.file)
	den := max(c.rateDen, 1)
	cfg := VideoConfig{
		Container: c.container,
		Video:     VideoStream{Codec: c.codec, Width: w, Height: h, RateNum: c.rateNum, RateDen: c.rateDen, Bitrate: c.bitrate, Quality: c.quality, Alpha: c.alpha},
		Audio:     c.audio,
	}
	vw, err := CreateVideo(path, cfg)
	if err != nil {
		if c.codec == HEVC && strings.Contains(err.Error(), "no hevc encoder opened") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	t.Logf("encoder %s (hardware %v)", vw.Encoder(), vw.Hardware())
	duration := time.Duration(float64(frames) * float64(den) / float64(c.rateNum) * float64(time.Second))
	var sent int
	for i := range frames {
		if err := vw.WriteVideo(frame(i, w, h, c.alpha)); err != nil {
			t.Fatal(err)
		}
		if c.audio != nil {
			// Audio in uneven pieces, as an app's mixer gives it.
			upTo := int(float64(c.audio.SampleRate) * float64(i+1) * float64(den) / float64(c.rateNum))
			if err := vw.WriteAudio(tone(upTo-sent, c.audio.SampleRate, c.audio.Channels)); err != nil {
				t.Fatal(err)
			}
			sent = upTo
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the path holds a file before Close: %v", err)
	}
	if err := vw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := vw.WriteVideo(frame(0, w, h, false)); !errors.Is(err, ErrClosed) {
		t.Errorf("WriteVideo after Close: %v", err)
	}
	if left := leftovers(t, filepath.Dir(path)); len(left) != 1 {
		t.Errorf("the directory holds %v", left)
	}

	got := probe(t, path)
	if !strings.HasPrefix(got.format, c.format) || got.videoCodec != c.videoCodec || got.audioCodec != c.audioCodec {
		t.Errorf("format %q, video %q, audio %q; want %q, %q, %q", got.format, got.videoCodec, got.audioCodec, c.format, c.videoCodec, c.audioCodec)
	}
	if got.width != w || got.height != h || got.frames != frames {
		t.Errorf("%dx%d with %d frames, want %dx%d with %d", got.width, got.height, got.frames, w, h, frames)
	}
	frame := time.Duration(float64(den) / float64(c.rateNum) * float64(time.Second))
	if !near(got.videoDuration, duration, frame/2) {
		t.Errorf("video lasts %v, want %v", got.videoDuration, duration)
	}
	if c.audio != nil {
		if want := map[string]int{"opus": 48000}[c.audioCodec]; want != 0 && got.audioRate != want || want == 0 && got.audioRate != c.audio.SampleRate {
			t.Errorf("audio at %d Hz", got.audioRate)
		}
		if got.audioChans != c.audio.Channels {
			t.Errorf("audio of %d channels, want %d", got.audioChans, c.audio.Channels)
		}
		// In sync: both start at 0 and end together, within an audio frame.
		if !near(got.audioStart, 0, time.Millisecond) || !near(got.audioDuration, duration, 25*time.Millisecond) {
			t.Errorf("audio starts at %v and lasts %v, want 0 and %v", got.audioStart, got.audioDuration, duration)
		}
		pcm, err := DecodeAudio(path, 48000, 2)
		if err != nil {
			t.Fatal(err)
		}
		if d := time.Duration(len(pcm) / 2 * int(time.Second) / 48000); !near(d, duration, 25*time.Millisecond) {
			t.Errorf("DecodeAudio gave %v, want %v", d, duration)
		}
		var peak float32
		for _, s := range pcm[len(pcm)/4 : len(pcm)/2] {
			peak = max(peak, s)
		}
		if peak < 0.2 || peak > 0.4 {
			t.Errorf("the decoded tone peaks at %v, want 0.3", peak)
		}
	}
	switch {
	case c.alpha && c.videoCodec == "prores":
		if !strings.HasPrefix(got.pixFmt, "yuva444") {
			t.Errorf("pixel format %s keeps no alpha", got.pixFmt)
		}
		if !pixelNear(got, w/4, h/2, alphaFill) || got.first[(h/2*w+w*3/4)*4+3] != 0 {
			t.Errorf("first frame's pixels %v and %v, want %v and transparent", got.first[(h/2*w+w/4)*4:][:4], got.first[(h/2*w+w*3/4)*4:][:4], alphaFill)
		}
	case c.alpha:
		// FFmpeg's VP9 decoder drops the alpha WebM keeps beside the picture.
		if got.alphaMode != "1" {
			t.Errorf("alpha_mode %q, want 1", got.alphaMode)
		}
	default:
		if !pixelNear(got, w/2, h*3/4, color.NRGBA{fill.R, fill.G, fill.B, 255}) {
			t.Errorf("first frame's pixel %v, want %v", got.first[(h*3/4*w+w/2)*4:][:4], fill)
		}
	}
}

// TestEachEncoder writes H.264 and HEVC with each of the platform's
// encoders this machine opens, as the fallbacks would.
func TestEachEncoder(t *testing.T) {
	mustLoad(t)
	const w, h, frames = 320, 240, 30
	for _, codec := range []string{H264, HEVC} {
		seen := map[string]bool{}
		for _, s := range videoEncoders(codec, false) {
			if seen[s.name] {
				continue
			}
			seen[s.name] = true
			t.Run(s.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "a.mp4")
				vw, err := CreateVideo(path, VideoConfig{Video: VideoStream{Codec: codec, Encoder: s.name, Width: w, Height: h, RateNum: 30}})
				if err != nil {
					t.Skip(err)
				}
				for i := range frames {
					if err := vw.WriteVideo(frame(i, w, h, false)); err != nil {
						t.Fatal(err)
					}
				}
				if err := vw.Close(); err != nil {
					t.Fatal(err)
				}
				got := probe(t, path)
				if got.videoCodec != codec || got.frames != frames {
					t.Errorf("%s with %d frames, want %s with %d", got.videoCodec, got.frames, codec, frames)
				}
				if !pixelNear(got, w/2, h*3/4, color.NRGBA{fill.R, fill.G, fill.B, 255}) {
					t.Errorf("first frame's pixel %v, want %v", got.first[(h*3/4*w+w/2)*4:][:4], fill)
				}
			})
		}
	}
}

// leftovers are the files in dir.
func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAbortLeavesNoFile(t *testing.T) {
	mustLoad(t)
	for _, file := range []string{"a.mp4", "a.mov", "a.webm"} {
		t.Run(file, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, file)
			vw, err := CreateVideo(path, VideoConfig{
				Video: VideoStream{Width: 64, Height: 64, RateNum: 30},
				Audio: &AudioStream{SampleRate: 48000, Channels: 2},
			})
			if err != nil {
				t.Fatal(err)
			}
			for i := range 10 {
				if err := vw.WriteVideo(frame(i, 64, 64, false)); err != nil {
					t.Fatal(err)
				}
				if err := vw.WriteAudio(tone(1600, 48000, 2)); err != nil {
					t.Fatal(err)
				}
			}
			if err := vw.Abort(); err != nil {
				t.Fatal(err)
			}
			if left := leftovers(t, dir); len(left) != 0 {
				t.Errorf("Abort left %v", left)
			}
			if err := vw.Close(); !errors.Is(err, ErrClosed) {
				t.Errorf("Close after Abort: %v", err)
			}
		})
	}
}

func TestCreateVideoRefuses(t *testing.T) {
	mustLoad(t)
	dir := t.TempDir()
	for name, c := range map[string]struct {
		file string
		cfg  VideoConfig
	}{
		"an unknown extension": {"a.avi", VideoConfig{Video: VideoStream{Width: 64, Height: 64, RateNum: 30}}},
		"H.264 in WebM":        {"a.webm", VideoConfig{Video: VideoStream{Codec: H264, Width: 64, Height: 64, RateNum: 30}}},
		"ProRes in MP4":        {"a.mp4", VideoConfig{Video: VideoStream{Codec: ProRes, Width: 64, Height: 64, RateNum: 30}}},
		"alpha in H.264":       {"a.mov", VideoConfig{Video: VideoStream{Alpha: true, Width: 64, Height: 64, RateNum: 30}}},
		"an odd size":          {"a.mp4", VideoConfig{Video: VideoStream{Width: 63, Height: 64, RateNum: 30}}},
		"no frame rate":        {"a.mp4", VideoConfig{Video: VideoStream{Width: 64, Height: 64}}},
		"a quality above 100":  {"a.mp4", VideoConfig{Video: VideoStream{Width: 64, Height: 64, RateNum: 30, Quality: 101}}},
		"no audio channels":    {"a.mp4", VideoConfig{Video: VideoStream{Width: 64, Height: 64, RateNum: 30}, Audio: &AudioStream{SampleRate: 48000}}},
	} {
		if _, err := CreateVideo(filepath.Join(dir, c.file), c.cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if left := leftovers(t, dir); len(left) != 0 {
		t.Errorf("refusals left %v", left)
	}

	vw, err := CreateVideo(filepath.Join(dir, "a.mp4"), VideoConfig{Video: VideoStream{Width: 64, Height: 64, RateNum: 30}})
	if err != nil {
		t.Fatal(err)
	}
	if err := vw.WriteVideo(image.NewRGBA(image.Rect(0, 0, 32, 32))); err == nil {
		t.Error("a frame of another size was taken")
	}
	if err := vw.WriteAudio(tone(10, 48000, 2)); err == nil {
		t.Error("audio was taken without an audio track")
	}
	// A failed write fails the file, which Close then removes.
	if err := vw.Close(); err == nil {
		t.Error("Close after a failed write gave no error")
	}
	if left := leftovers(t, dir); len(left) != 0 {
		t.Errorf("a failed file left %v", left)
	}
}

func TestDecodeAudioOfFixture(t *testing.T) {
	mustLoad(t)
	pcm, err := DecodeAudio(filepath.Join(testdata(), "av.mkv"), 44100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Duration(len(pcm) * int(time.Second) / 44100); !near(d, 2*time.Second, 100*time.Millisecond) {
		t.Errorf("decoded %v, want about 2s", d)
	}
	if _, err := DecodeAudio(filepath.Join(testdata(), "sub.srt"), 48000, 2); err == nil {
		t.Error("a file without audio decoded")
	}
}

func TestPremultiply(t *testing.T) {
	src := []byte{200, 100, 50, 128, 10, 20, 30, 255, 99, 99, 99, 0}
	pre := make([]byte, len(src))
	premultiply(pre, src)
	if want := []byte{100, 50, 25, 128, 10, 20, 30, 255, 0, 0, 0, 0}; string(pre) != string(want) {
		t.Errorf("premultiplied %v, want %v", pre, want)
	}
	back := make([]byte, len(pre))
	unpremultiply(back, pre)
	if want := []byte{199, 100, 50, 128, 10, 20, 30, 255, 0, 0, 0, 0}; string(back) != string(want) {
		t.Errorf("unpremultiplied %v, want %v", back, want)
	}
}

// testdata is the repository's testdata directory, or the one
// NSGO_MEDIA_TESTDATA names.
func testdata() string {
	if dir := os.Getenv("NSGO_MEDIA_TESTDATA"); dir != "" {
		return dir
	}
	return filepath.Join("..", "testdata")
}
