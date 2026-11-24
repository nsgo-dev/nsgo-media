package player

import (
	"math"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/gpu"

	"github.com/oneo-me/nsgo-media/internal/pipeline"
)

// Fit is how the picture fills its view.
type Fit int

const (
	// Contain shows the whole picture, leaving bars.
	Contain Fit = iota
	// Cover fills the view, cropping what overflows.
	Cover
	// Fill stretches the picture to the view.
	Fill
)

// View draws a player's picture in its box. Children draw above the
// picture, as do views placed after it: the picture is an image view, or
// on Windows and Linux a GPU view for pictures decoded on the GPU, never
// a native window.
type View struct {
	Player *Player
	Fit    Fit
	// FitFunc, when set, gives the fit instead of Fit, again as what it
	// reads changes.
	FitFunc  func() Fit
	Style    core.Style
	Class    string
	Children []core.Node
	TestID   string
}

// viewSize is one mounted view: its size in pixels, for the converter,
// and its GPU view, nil where it has none.
type viewSize struct {
	w, h int
	gpu  *gpuPicture
	kind pipeline.GPUKind // the pictures its GPU view imports
}

// Render implements core.Node.
func (v View) Render(cx *core.Scope) core.Node {
	p := v.Player
	size := &viewSize{}
	p.views[size] = struct{}{}
	cx.Cleanup(func() {
		delete(p.views, size)
		if size.gpu != nil {
			size.gpu.drop()
		}
		p.updateOutputSize()
	})
	box := core.NewSignal(cx, core.Size{})
	style := v.Style
	if style.Overflow == 0 {
		style.Overflow = core.Hidden
	}
	place := func() core.Style {
		fit := v.Fit
		if v.FitFunc != nil {
			fit = v.FitFunc()
		}
		b := p.PictureBounds(box.Get(), fit)
		return core.Style{
			Position: core.Absolute,
			Left:     core.Pt(b.Origin.X), Top: core.Pt(b.Origin.Y),
			Width: core.Pt(b.Size.Width), Height: core.Pt(b.Size.Height),
		}
	}
	// Pictures in memory show in the image, above the GPU view, which
	// shows those on the GPU while the image is empty.
	picture := core.Image{
		SourceFunc: func() core.ImageSource {
			f := p.frame.Get()
			if f.img == nil {
				return core.ImageSource{}
			}
			return core.ImageSource{Pixels: f.img, Version: f.version}
		},
		ResizeMode:     core.Stretch,
		StyleFunc:      place,
		ElementsHidden: true,
	}
	children := []core.Node{picture}
	if gpuTarget(cx.Env()) {
		g := &gpuPicture{p: p, cx: cx, size: size}
		size.gpu = g
		children = []core.Node{gpu.View(cx, gpu.ViewProps{
			StyleFunc: place,
			OnReady: func(dev *gpu.Device) {
				size.kind = g.ready(dev)
				p.updateGPU()
			},
			OnDeviceLost: func(gpu.DeviceLostEvent) {
				g.lost()
				size.kind = pipeline.GPUNone
				p.updateGPU()
			},
			OnError: func(gpu.Error) {
				// An import the backend refused: pictures go through memory.
				g.kind, size.kind = pipeline.GPUNone, pipeline.GPUNone
				p.updateGPU()
			},
		}), picture}
	}
	return core.View{
		Class:    v.Class,
		Style:    style,
		Children: append(children, v.Children...),
		TestID:   v.TestID,
		OnLayout: func(b core.Bounds) {
			box.Set(b.Size)
			ratio := float64(cx.Env().PixelRatio())
			size.w = int(math.Ceil(float64(b.Size.Width) * ratio))
			size.h = int(math.Ceil(float64(b.Size.Height) * ratio))
			p.updateOutputSize()
		},
	}
}

// PictureBounds is where a View of size box draws the picture with fit,
// in its points: what an app draws over the picture, such as a cue's
// bitmaps placed with Cue.Place, goes in the same box. Cover's bounds
// overflow the box.
func (p *Player) PictureBounds(box core.Size, fit Fit) core.Bounds {
	pw, ph := p.VideoSize()
	x, y, w, h := fitRect(box, float64(pw), float64(ph), fit)
	return core.Bounds{
		Origin: core.Point{X: core.Points(x), Y: core.Points(y)},
		Size:   core.Size{Width: core.Points(w), Height: core.Points(h)},
	}
}

// fitRect places a picture of pw×ph in a box by fit, in points.
func fitRect(box core.Size, pw, ph float64, fit Fit) (x, y, w, h float64) {
	bw, bh := float64(box.Width), float64(box.Height)
	if fit == Fill || pw <= 0 || ph <= 0 || bw <= 0 || bh <= 0 {
		return 0, 0, bw, bh
	}
	s := math.Min(bw/pw, bh/ph)
	if fit == Cover {
		s = math.Max(bw/pw, bh/ph)
	}
	w, h = pw*s, ph*s
	return (bw - w) / 2, (bh - h) / 2, w, h
}

// updateGPU has hardware pictures go to the GPU when every view imports
// them.
func (p *Player) updateGPU() {
	kind := pipeline.GPUNone
	for s := range p.views {
		if s.kind == pipeline.GPUNone || kind != pipeline.GPUNone && s.kind != kind {
			kind = pipeline.GPUNone
			break
		}
		kind = s.kind
	}
	if p.pl != nil {
		p.pl.SetGPU(kind)
	}
}

// drawGPU has the GPU views draw the frame showing.
func (p *Player) drawGPU(f *pipeline.Frame) {
	for s := range p.views {
		if s.gpu != nil {
			s.gpu.draw(f)
		}
	}
}

// updateOutputSize converts frames no larger than the largest view
// needs.
func (p *Player) updateOutputSize() {
	w, h := 0, 0
	for s := range p.views {
		w, h = max(w, s.w), max(h, s.h)
	}
	if p.pl != nil {
		p.pl.SetOutputSize(w, h)
	}
}
