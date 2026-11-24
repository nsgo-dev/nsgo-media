package pipeline

import (
	"errors"
	"image"
	"math"
	"unsafe"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// Snapshot converts the frame showing again from its decoded picture: at
// the source's size, its pixels' shape and rotation applied, whatever
// size views draw it at. A width or height above 0 shrinks it to fit,
// keeping its shape; it never grows. It runs on the caller's goroutine.
func (p *Pipeline) Snapshot(w, h int) (*image.RGBA, error) {
	f := p.shown
	if f == nil || f.src == nil {
		return nil, errors.New("pipeline: no frame is showing")
	}
	dw, dh := p.displayed(f.src.Width(), f.src.Height())
	scale := 1.0
	if w > 0 {
		scale = math.Min(scale, float64(w)/dw)
	}
	if h > 0 {
		scale = math.Min(scale, float64(h)/dh)
	}
	ow, oh := max(int(math.Round(dw*scale)), 1), max(int(math.Round(dh*scale)), 1)
	src := f.src
	if src.Hardware() {
		// A picture on the GPU is copied to memory for its snapshot.
		m := ffmpeg.NewFrame()
		defer m.Free()
		if err := src.TransferTo(m); err != nil {
			return nil, err
		}
		src = m
	}
	var s ffmpeg.Scaler
	defer s.Close()
	mem, err := p.paint(&s, src, ow, oh)
	if err != nil {
		return nil, err
	}
	defer ffmpeg.Free(mem)
	img := image.NewRGBA(image.Rect(0, 0, ow, oh))
	copy(img.Pix, unsafe.Slice((*byte)(mem), len(img.Pix)))
	return img, nil
}
