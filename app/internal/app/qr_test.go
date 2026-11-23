package app

import (
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// TestQRImage reads back the QR code drawn of a sign-in link.
func TestQRImage(t *testing.T) {
	const link = "https://example.com/sign-in/scan?key=e4841e2ddc02d6e254cc8cfc4bd0c05a&from=app"
	img := qrImage(link)
	if img == nil {
		t.Fatal("no QR code")
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	got, err := qrcode.NewQRCodeReader().Decode(bmp, map[gozxing.DecodeHintType]any{gozxing.DecodeHintType_PURE_BARCODE: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetText() != link {
		t.Errorf("reads %q", got.GetText())
	}
}
