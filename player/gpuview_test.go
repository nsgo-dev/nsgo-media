package player

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/nsgo-dev/nsgo/gpu"

	"github.com/oneo-me/nsgo-media/internal/pipeline"
)

func TestYUVShaderCompiles(t *testing.T) {
	m, err := gpu.CompileShader(gpu.ShaderDesc{Source: yuvWGSL})
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasVertexEntry("vs_main") || !m.HasFragmentEntry("fs_main") {
		t.Fatal("the shader lacks its entry points")
	}
}

// convert applies m to sampled (y, cb, cr) as the shader does.
func convert(m [12]float32, y, cb, cr float64) [3]float64 {
	var rgb [3]float64
	for i := range 3 {
		r := m[i*4 : i*4+4]
		rgb[i] = float64(r[0])*y + float64(r[1])*cb + float64(r[2])*cr + float64(r[3])
	}
	return rgb
}

func near(t *testing.T, what string, got [3]float64, want [3]float64) {
	t.Helper()
	for i := range 3 {
		if math.Abs(got[i]-want[i]) > 0.004 {
			t.Errorf("%s = %.4f, want %.4f", what, got, want)
			return
		}
	}
}

func TestYUVMatrix(t *testing.T) {
	// 8-bit limited range: 16 is black, 235 white, 128 no colour.
	hd := yuvMatrix(&pipeline.GPUPicture{Height: 1080, ColorSpace: 1})
	near(t, "black", convert(hd, 16/255.0, 128/255.0, 128/255.0), [3]float64{0, 0, 0})
	near(t, "white", convert(hd, 235/255.0, 128/255.0, 128/255.0), [3]float64{1, 1, 1})
	// BT.709 red is Y 63, Cb 102, Cr 240.
	near(t, "BT.709 red", convert(hd, 63/255.0, 102/255.0, 240/255.0), [3]float64{1, 0, 0})
	// BT.601 red is Y 81, Cb 90, Cr 240; untagged SD pictures are BT.601.
	sd := yuvMatrix(&pipeline.GPUPicture{Height: 480, ColorSpace: 2})
	near(t, "BT.601 red", convert(sd, 81/255.0, 90/255.0, 240/255.0), [3]float64{1, 0, 0})
	// Full range uses every code value.
	full := yuvMatrix(&pipeline.GPUPicture{Height: 1080, ColorSpace: 1, FullRange: true})
	near(t, "full-range white", convert(full, 1, 128/255.0, 128/255.0), [3]float64{1, 1, 1})
	// P010 holds 10 bits at the top of 16: 64 is black, 940 white.
	deep := yuvMatrix(&pipeline.GPUPicture{Height: 2160, ColorSpace: 9, Deep: true})
	p010 := func(v float64) float64 { return v * 64 / 65535 }
	near(t, "10-bit black", convert(deep, p010(64), p010(512), p010(512)), [3]float64{0, 0, 0})
	near(t, "10-bit white", convert(deep, p010(940), p010(512), p010(512)), [3]float64{1, 1, 1})
}

func TestQuadsTurnClockwise(t *testing.T) {
	// The texture coordinates showing at the surface's top left corner,
	// the first vertex of each rotation's quad.
	want := [4][2]float32{{0, 0}, {0, 1}, {1, 1}, {1, 0}}
	for r, w := range want {
		at := r * 6 * 16
		pos := [2]float32{f32(quadData[at:]), f32(quadData[at+4:])}
		uv := [2]float32{f32(quadData[at+8:]), f32(quadData[at+12:])}
		if pos != [2]float32{-1, 1} || uv != w {
			t.Errorf("rotation %d° shows %v at %v, want %v at the top left", r*90, uv, pos, w)
		}
	}
}

func f32(b []byte) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b)) }
