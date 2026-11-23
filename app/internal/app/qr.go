package app

import (
	"image"
	"image/color"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"github.com/nsgo-dev/nsgo/core"
)

// qrSize is the side of a sign-in QR code in points.
const qrSize = 200

// qrCode shows a sign-in QR code of text, dark on white in any theme, as
// phones scan that best.
func qrCode(_ *core.Scope, text string) core.Node {
	img := qrImage(text)
	if img == nil {
		return nil
	}
	return core.Image{
		TestID:   "sign-in-code",
		Label:    text,
		Source:   core.ImageSource{Pixels: img, Size: core.Size{Width: qrSize, Height: qrSize}},
		Sampling: core.ImageSamplingNearest,
		Style:    core.Style{Width: core.Pt(qrSize), Height: core.Pt(qrSize)},
	}
}

// qrImage is the QR code of text, a pixel to a module with the quiet
// zone around it, or nil when text does not fit one.
func qrImage(text string) *image.RGBA {
	m, err := qrcode.NewQRCodeWriter().Encode(text, gozxing.BarcodeFormat_QR_CODE, 0, 0,
		map[gozxing.EncodeHintType]any{gozxing.EncodeHintType_MARGIN: 4})
	if err != nil {
		return nil
	}
	w, h := m.GetWidth(), m.GetHeight()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{255, 255, 255, 255}
			if m.Get(x, y) {
				c = color.RGBA{0, 0, 0, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}
