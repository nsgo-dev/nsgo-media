package player

import (
	"math"

	"github.com/nsgo-dev/nsgo/core"
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
// picture, as do views placed after it: the picture is an ordinary image
// view on every desktop, never a native window.
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

// viewSize is one mounted view's size in pixels, for the converter.
type viewSize struct{ w, h int }

// Render implements core.Node.
func (v View) Render(cx *core.Scope) core.Node {
	p := v.Player
	size := &viewSize{}
	p.views[size] = struct{}{}
	cx.Cleanup(func() {
		delete(p.views, size)
		p.updateOutputSize()
	})
	box := core.NewSignal(cx, core.Size{})
	style := v.Style
	if style.Overflow == 0 {
		style.Overflow = core.Hidden
	}
	picture := core.Image{
		SourceFunc: func() core.ImageSource {
			f := p.frame.Get()
			if f.img == nil {
				return core.ImageSource{}
			}
			return core.ImageSource{Pixels: f.img, Version: f.version}
		},
		ResizeMode: core.Stretch,
		StyleFunc: func() core.Style {
			fit := v.Fit
			if v.FitFunc != nil {
				fit = v.FitFunc()
			}
			pw, ph := p.VideoSize()
			x, y, w, h := fitRect(box.Get(), float64(pw), float64(ph), fit)
			return core.Style{
				Position: core.Absolute,
				Left:     core.Pt(core.Points(x)), Top: core.Pt(core.Points(y)),
				Width: core.Pt(core.Points(w)), Height: core.Pt(core.Points(h)),
			}
		},
		ElementsHidden: true,
	}
	return core.View{
		Class:    v.Class,
		Style:    style,
		Children: append([]core.Node{picture}, v.Children...),
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
