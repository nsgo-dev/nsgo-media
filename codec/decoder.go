package codec

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"unsafe"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// DecoderConfig configures a low-latency video decoder.
type DecoderConfig struct {
	// Codec is the stream codec name; defaults to H264 when empty.
	Codec string
	// SoftwareOnly skips hardware acceleration and decodes in software.
	SoftwareOnly bool
}

// Decoder decodes Annex B video access units into RGBA images ready for an
// NSGo view (core.ImageSource{Pixels: img}). It prefers hardware decoding
// and falls back to software automatically on failure.
type Decoder struct {
	codec   string
	dec     *ffmpeg.Decoder
	pkt     *ffmpeg.Packet
	frame   *ffmpeg.Frame
	swFrame *ffmpeg.Frame
	scaler  ffmpeg.Scaler
	closed  bool
}

// NewDecoder opens a low-latency video decoder, preferring the platform's
// hardware decoder and falling back to software when unavailable.
func NewDecoder(cfg DecoderConfig) (*Decoder, error) {
	if cfg.Codec == "" {
		cfg.Codec = H264
	}
	if cfg.Codec != H264 {
		return nil, fmt.Errorf("codec: unsupported codec %q", cfg.Codec)
	}
	if err := ffmpeg.Load(); err != nil {
		return nil, err
	}

	var dec *ffmpeg.Decoder
	var err error
	if !cfg.SoftwareOnly {
		if hw := platformHWDecodeDevice(); hw != "" {
			dec, err = ffmpeg.NewRawDecoder(cfg.Codec, 1, hw)
		}
	}
	if dec == nil {
		dec, err = ffmpeg.NewRawDecoder(cfg.Codec, 1, "")
		if err != nil {
			return nil, err
		}
	}

	pkt := ffmpeg.NewPacket()
	frame := ffmpeg.NewFrame()
	swFrame := ffmpeg.NewFrame()
	if pkt == nil || frame == nil || swFrame == nil {
		dec.Close()
		if pkt != nil {
			pkt.Free()
		}
		if frame != nil {
			frame.Free()
		}
		if swFrame != nil {
			swFrame.Free()
		}
		return nil, errors.New("codec: out of memory")
	}

	return &Decoder{
		codec:   cfg.Codec,
		dec:     dec,
		pkt:     pkt,
		frame:   frame,
		swFrame: swFrame,
	}, nil
}

// Hardware reports whether the decoder is currently using hardware acceleration.
func (d *Decoder) Hardware() bool { return d.dec != nil && d.dec.HWDevice != "" }

// Flush drops buffered frames inside the decoder (e.g. after packet loss
// before feeding a fresh keyframe).
func (d *Decoder) Flush() {
	if !d.closed && d.dec != nil {
		d.dec.Flush()
	}
}

// Decode feeds one Annex B access unit and returns the decoded picture as
// an *image.RGBA, or (nil, nil) if no picture is ready yet.
func (d *Decoder) Decode(unit []byte) (*image.RGBA, error) {
	if d.closed {
		return nil, errors.New("codec: decoder is closed")
	}
	if len(unit) == 0 {
		return nil, nil
	}
	img, err := d.decodeOnce(unit)
	if err != nil && d.dec.HWDevice != "" {
		// Hardware decode failed: fall back to software and retry this unit.
		if fbErr := d.fallbackToSoftware(); fbErr == nil {
			return d.decodeOnce(unit)
		}
	}
	return img, err
}

func (d *Decoder) decodeOnce(unit []byte) (*image.RGBA, error) {
	if err := d.pkt.SetData(unit); err != nil {
		return nil, err
	}
	defer d.pkt.Unref()
	if err := d.dec.Send(d.pkt); err != nil && !errors.Is(err, ffmpeg.ErrAgain) {
		return nil, err
	}
	d.frame.Unref()
	err := d.dec.Receive(d.frame)
	if errors.Is(err, ffmpeg.ErrAgain) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer d.frame.Unref()

	src := d.frame
	if d.frame.Hardware() {
		d.swFrame.Unref()
		if err := d.frame.TransferTo(d.swFrame); err != nil {
			return nil, err
		}
		defer d.swFrame.Unref()
		src = d.swFrame
	}

	w, h := src.Width(), src.Height()
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := d.scaler.Scale(src, unsafe.Pointer(&img.Pix[0]), img.Stride, w, h); err != nil {
		return nil, err
	}
	runtime.KeepAlive(img.Pix)
	return img, nil
}

func (d *Decoder) fallbackToSoftware() error {
	sw, err := ffmpeg.NewRawDecoder(d.codec, 1, "")
	if err != nil {
		return err
	}
	d.dec.Close()
	d.dec = sw
	return nil
}

// Close releases the decoder and its FFmpeg resources.
func (d *Decoder) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	d.scaler.Close()
	if d.swFrame != nil {
		d.swFrame.Free()
		d.swFrame = nil
	}
	if d.frame != nil {
		d.frame.Free()
		d.frame = nil
	}
	if d.pkt != nil {
		d.pkt.Free()
		d.pkt = nil
	}
	if d.dec != nil {
		d.dec.Close()
		d.dec = nil
	}
	return nil
}
