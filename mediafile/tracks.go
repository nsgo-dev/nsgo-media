package mediafile

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"math"
	"runtime"
	"slices"
	"strings"
	"unsafe"

	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

// encoder is what the tracks drive of an FFmpeg encoder.
type encoder interface {
	Send(*ffmpeg.Frame) error
	Receive(*ffmpeg.Packet) error
}

// send gives enc f (nil drains it) and muxes the packets it gives out
// into stream, those without a duration lasting dur when it is not 0.
func send(m *ffmpeg.Muxer, enc encoder, stream int, pkt *ffmpeg.Packet, f *ffmpeg.Frame, dur int64) error {
	for {
		err := enc.Send(f)
		if err == nil {
			break
		}
		if !ffmpeg.IsAgain(err) {
			return err
		}
		// The encoder gives out packets before it takes more.
		if err := receive(m, enc, stream, pkt, dur); err != nil {
			return err
		}
	}
	return receive(m, enc, stream, pkt, dur)
}

func receive(m *ffmpeg.Muxer, enc encoder, stream int, pkt *ffmpeg.Packet, dur int64) error {
	for {
		err := enc.Receive(pkt)
		if ffmpeg.IsAgain(err) || ffmpeg.IsEOF(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if pkt.DurationTS() == 0 {
			// Frame-threaded encoders, as ProRes', give none, and MOV's
			// last sample would last nothing.
			pkt.SetDuration(dur)
		}
		if err := m.Write(stream, pkt); err != nil {
			pkt.Unref()
			return err
		}
	}
}

// videoTrack converts frames to the encoder's pixel format and encodes them.
type videoTrack struct {
	spec       videoSpec
	enc        *ffmpeg.Encoder
	stream     int
	frame, src *ffmpeg.Frame
	pkt        *ffmpeg.Packet
	scaler     ffmpeg.Scaler
	rgba       int32
	w, h       int
	alpha      bool
	buf        []byte // frames converted to the alpha the encoder takes
	pts        int64
}

func newVideoTrack(m *ffmpeg.Muxer, v VideoStream) (*videoTrack, error) {
	den := v.RateDen
	if den == 0 {
		den = 1
	}
	rate := ffmpeg.Rational{Num: int32(v.RateNum), Den: int32(den)}
	fps := rate.Float()
	q := v.Quality
	if q == 0 {
		q = 80
	}
	specs := videoEncoders(v.Codec, v.Alpha)
	if v.Encoder != "" {
		specs = slices.DeleteFunc(specs, func(s videoSpec) bool { return s.name != v.Encoder })
		if len(specs) == 0 {
			return nil, fmt.Errorf("mediafile: %s is no %s encoder on this platform", v.Encoder, v.Codec)
		}
	}
	t := &videoTrack{w: v.Width, h: v.Height, alpha: v.Alpha}
	var errs []string
	for _, s := range specs {
		if !ffmpeg.HasEncoder(s.name) {
			errs = append(errs, s.name+": not in the libraries")
			continue
		}
		bitrate := int64(v.Bitrate)
		if bitrate == 0 && s.constQ == nil {
			bitrate = estimateBitrate(v.Codec, q, v.Width, v.Height, fps)
		}
		opts, gq, br := s.encoderOptions(q, bitrate)
		enc, err := ffmpeg.NewEncoder(ffmpeg.EncoderConfig{
			Name: s.name, HWDevice: s.hwDevice, HWFormat: s.hwFormat, PixFmt: s.pixFmt,
			Width: v.Width, Height: v.Height, Rate: rate, BitRate: br, Quality: gq,
			GOPSize: max(1, int(math.Round(2*fps))), ColorSpace: ffmpeg.ColorSpaceBT709,
			File: true, BFrames: s.bFrames, GlobalHeader: m.GlobalHeader(), Options: opts,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", s.name, err))
			continue
		}
		t.spec, t.enc = s, enc
		break
	}
	if t.enc == nil {
		return nil, fmt.Errorf("mediafile: no %s encoder opened: %s", v.Codec, strings.Join(errs, "; "))
	}
	var err error
	if t.stream, err = m.AddStream(t.enc, t.spec.tag); err != nil {
		t.close()
		return nil, err
	}
	t.frame, t.src, t.pkt = ffmpeg.NewFrame(), ffmpeg.NewFrame(), ffmpeg.NewPacket()
	if t.frame == nil || t.src == nil || t.pkt == nil {
		t.close()
		return nil, errors.New("mediafile: out of memory")
	}
	if err := t.frame.AllocBuffer(v.Width, v.Height, ffmpeg.PixelFormat(t.spec.pixFmt)); err != nil {
		t.close()
		return nil, err
	}
	t.frame.SetColor(ffmpeg.ColorSpaceBT709, false)
	t.rgba = ffmpeg.PixelFormat("rgba")
	return t, nil
}

func (t *videoTrack) write(m *ffmpeg.Muxer, img image.Image) error {
	pix, stride, err := t.pixels(img)
	if err != nil {
		return err
	}
	if err := t.frame.MakeWritable(); err != nil {
		return err
	}
	t.src.SetGeometry(t.w, t.h, t.rgba)
	t.src.SetPlane(0, unsafe.Pointer(&pix[0]), stride)
	err = t.scaler.Convert(t.frame, t.src)
	t.src.SetPlane(0, nil, 0)
	runtime.KeepAlive(pix)
	if err != nil {
		return err
	}
	t.frame.SetPTS(t.pts)
	t.frame.SetDuration(1)
	t.pts++
	return send(m, t.enc, t.stream, t.pkt, t.frame, 1)
}

// pixels are img's RGBA rows, straight alpha when the track keeps alpha
// and premultiplied, as over black, when it does not.
func (t *videoTrack) pixels(img image.Image) ([]byte, int, error) {
	b := img.Bounds()
	if b.Dx() != t.w || b.Dy() != t.h {
		return nil, 0, fmt.Errorf("mediafile: a frame of %dx%d in a %dx%d video", b.Dx(), b.Dy(), t.w, t.h)
	}
	switch m := img.(type) {
	case *image.RGBA:
		pix := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		if !t.alpha {
			return pix, m.Stride, nil
		}
		return t.convert(pix, m.Stride, unpremultiply), t.w * 4, nil
	case *image.NRGBA:
		pix := m.Pix[m.PixOffset(b.Min.X, b.Min.Y):]
		if t.alpha {
			return pix, m.Stride, nil
		}
		return t.convert(pix, m.Stride, premultiply), t.w * 4, nil
	}
	dst := &image.RGBA{Pix: t.buffer(), Stride: t.w * 4, Rect: image.Rect(0, 0, t.w, t.h)}
	draw.Draw(dst, dst.Rect, img, b.Min, draw.Src)
	if t.alpha {
		unpremultiply(dst.Pix, dst.Pix)
	}
	return dst.Pix, dst.Stride, nil
}

func (t *videoTrack) buffer() []byte {
	if n := t.w * t.h * 4; len(t.buf) != n {
		t.buf = make([]byte, n)
	}
	return t.buf
}

// convert runs fn over each row of pix into the track's buffer.
func (t *videoTrack) convert(pix []byte, stride int, fn func(dst, src []byte)) []byte {
	buf, row := t.buffer(), t.w*4
	for y := range t.h {
		fn(buf[y*row:(y+1)*row], pix[y*stride:y*stride+row])
	}
	return buf
}

func premultiply(dst, src []byte) {
	for i := 0; i+3 < len(src); i += 4 {
		a := uint32(src[i+3])
		if a == 255 {
			copy(dst[i:i+4], src[i:i+4])
			continue
		}
		dst[i] = byte((uint32(src[i])*a + 127) / 255)
		dst[i+1] = byte((uint32(src[i+1])*a + 127) / 255)
		dst[i+2] = byte((uint32(src[i+2])*a + 127) / 255)
		dst[i+3] = byte(a)
	}
}

func unpremultiply(dst, src []byte) {
	for i := 0; i+3 < len(src); i += 4 {
		a := uint32(src[i+3])
		switch a {
		case 255:
			copy(dst[i:i+4], src[i:i+4])
			continue
		case 0:
			dst[i], dst[i+1], dst[i+2], dst[i+3] = 0, 0, 0, 0
			continue
		}
		dst[i] = byte(min(255, (uint32(src[i])*255+a/2)/a))
		dst[i+1] = byte(min(255, (uint32(src[i+1])*255+a/2)/a))
		dst[i+2] = byte(min(255, (uint32(src[i+2])*255+a/2)/a))
		dst[i+3] = byte(a)
	}
}

func (t *videoTrack) flush(m *ffmpeg.Muxer) error {
	return send(m, t.enc, t.stream, t.pkt, nil, 1)
}

func (t *videoTrack) close() {
	t.scaler.Close()
	for _, f := range []**ffmpeg.Frame{&t.frame, &t.src} {
		if *f != nil {
			(*f).Free()
			*f = nil
		}
	}
	if t.pkt != nil {
		t.pkt.Free()
		t.pkt = nil
	}
	if t.enc != nil {
		t.enc.Close()
		t.enc = nil
	}
}

// audioTrack resamples interleaved float32 samples where the codec needs
// another rate, cuts them into the encoder's frames and encodes them.
type audioTrack struct {
	enc            *ffmpeg.Encoder
	stream         int
	channels, rate int
	format         int32
	planar, s16    bool
	frameSize      int
	conv           *ffmpeg.RateConverter
	pending        []float32 // samples at the encoder's rate not yet in a frame
	frame          *ffmpeg.Frame
	pkt            *ffmpeg.Packet
	pts            int64
}

func newAudioTrack(m *ffmpeg.Muxer, container string, a AudioStream) (*audioTrack, error) {
	name, perChannel := audioCodec(container)
	if !ffmpeg.HasEncoder(name) {
		return nil, fmt.Errorf("mediafile: the FFmpeg libraries have no %s encoder", name)
	}
	t := &audioTrack{channels: a.Channels, rate: a.SampleRate, format: -1}
	if rates := ffmpeg.EncoderSampleRates(name); rates != nil && !slices.Contains(rates, t.rate) {
		t.rate = 48000
	}
	formats := ffmpeg.EncoderSampleFormats(name)
	for _, want := range []string{"flt", "fltp", "s16"} {
		if f := ffmpeg.SampleFormat(want); formats == nil || slices.Contains(formats, f) {
			t.format = f
			break
		}
	}
	if t.format < 0 {
		return nil, fmt.Errorf("mediafile: %s takes no float or 16-bit samples", name)
	}
	t.planar = ffmpeg.SampleFormatPlanar(t.format)
	t.s16 = ffmpeg.SampleFormatName(t.format) == "s16"
	bitrate := a.Bitrate
	if bitrate <= 0 {
		bitrate = perChannel * a.Channels
	}
	var err error
	t.enc, err = ffmpeg.NewAudioEncoder(ffmpeg.AudioEncoderConfig{
		Name: name, SampleRate: t.rate, Channels: a.Channels, SampleFormat: t.format,
		BitRate: int64(bitrate), GlobalHeader: m.GlobalHeader(),
	})
	if err != nil {
		return nil, err
	}
	if t.stream, err = m.AddStream(t.enc, ""); err != nil {
		t.close()
		return nil, err
	}
	if t.rate != a.SampleRate {
		if t.conv, err = ffmpeg.NewRateConverter(a.SampleRate, t.rate, a.Channels); err != nil {
			t.close()
			return nil, err
		}
	}
	if t.frameSize = t.enc.FrameSize(); t.frameSize <= 0 {
		t.frameSize = 1024
	}
	t.frame, t.pkt = ffmpeg.NewFrame(), ffmpeg.NewPacket()
	if t.frame == nil || t.pkt == nil {
		t.close()
		return nil, errors.New("mediafile: out of memory")
	}
	return t, nil
}

// write encodes samples' whole frames and keeps the rest for the next
// call; final encodes the rest too, in a shorter last frame, and drains
// the encoder.
func (t *audioTrack) write(m *ffmpeg.Muxer, samples []float32, final bool) error {
	if t.conv != nil {
		var err error
		if samples, err = t.conv.Convert(samples); err != nil {
			return err
		}
	}
	t.pending = append(t.pending, samples...)
	rest := t.pending
	for len(rest) >= t.frameSize*t.channels || final && len(rest) > 0 {
		n := min(t.frameSize, len(rest)/t.channels)
		if err := t.encode(m, rest[:n*t.channels]); err != nil {
			return err
		}
		rest = rest[n*t.channels:]
	}
	t.pending = t.pending[:copy(t.pending, rest)]
	if final {
		return send(m, t.enc, t.stream, t.pkt, nil, 0)
	}
	return nil
}

// encode encodes one frame of samples.
func (t *audioTrack) encode(m *ffmpeg.Muxer, samples []float32) error {
	n := len(samples) / t.channels
	// A new buffer each frame, as the encoder may keep the last.
	if err := t.frame.AllocAudio(n, t.format, t.rate, t.channels); err != nil {
		return err
	}
	switch {
	case t.planar:
		for c := range t.channels {
			p, _ := t.frame.Plane(c)
			plane := unsafe.Slice((*float32)(p), n)
			for i := range n {
				plane[i] = samples[i*t.channels+c]
			}
		}
	case t.s16:
		p, _ := t.frame.Plane(0)
		out := unsafe.Slice((*int16)(p), len(samples))
		for i, s := range samples {
			out[i] = int16(math.Round(float64(max(-1, min(1, s))) * 32767))
		}
	default:
		p, _ := t.frame.Plane(0)
		copy(unsafe.Slice((*float32)(p), len(samples)), samples)
	}
	t.frame.SetPTS(t.pts)
	t.pts += int64(n)
	err := send(m, t.enc, t.stream, t.pkt, t.frame, 0)
	t.frame.Unref()
	return err
}

func (t *audioTrack) close() {
	if t.conv != nil {
		t.conv.Close()
		t.conv = nil
	}
	if t.frame != nil {
		t.frame.Free()
		t.frame = nil
	}
	if t.pkt != nil {
		t.pkt.Free()
		t.pkt = nil
	}
	if t.enc != nil {
		t.enc.Close()
		t.enc = nil
	}
}
