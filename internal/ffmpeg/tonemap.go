package ffmpeg

import (
	"encoding/binary"
	"math"
	"sync"
)

// Tone mapping parameters shared with the GPU shader in player/gpuview.go:
// ITU-R BT.2390 EETF from a 1000-nit mastering peak to 203-nit SDR white
// (ITU-R BT.2408), with BT.2020-to-BT.709 gamut conversion and sRGB OETF.
const (
	pqM1 = 2610.0 / 16384.0
	pqM2 = 2523.0 / 4096.0 * 128.0
	pqC1 = 3424.0 / 4096.0
	pqC2 = 2413.0 / 4096.0 * 32.0
	pqC3 = 2392.0 / 4096.0 * 32.0

	hlgA = 0.17883277
	hlgB = 0.28466892 // 1 - 4*a
	hlgC = 0.55991073 // 0.5 - a*ln(4*a)

	sdrWhiteNits  = 203.0
	hdrPeakNits   = 1000.0
	pqPeakNits    = 10000.0
	sdrWhiteNorm  = sdrWhiteNits / pqPeakNits // 0.0203
	hlgPeakNorm   = hdrPeakNits / pqPeakNits  // 0.1
	sdrWhiteScale = pqPeakNits / sdrWhiteNits // 49.26108...
)

var (
	tmOnce      sync.Once
	pqEOTFLUT   [4096]float32 // 12-bit PQ code -> linear [0, 1] (1.0 = 10000 nits)
	pqScaleLUT  [4096]float32 // 12-bit max PQ code -> scale from linear [0, 1] to SDR linear [0, 1]
	hlgInvLUT   [4096]float32 // 12-bit HLG code -> scene linear [0, 1]
	hlgOOTFLUT  [4096]float32 // 12-bit scene luma -> y^0.2 * 0.1 (1000-nit ref display in 10000-nit units)
	srgbOETFLUT [4096]uint8   // 12-bit SDR linear [0, 1] -> 8-bit sRGB

	bt2390PQMax  float64
	bt2390MaxLum float64
	bt2390KS     float64
	bt2390KneeL  float32 // linear [0, 1] below which BT.2390 is identity
)

func pqEOTF(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	p := math.Pow(v, 1.0/pqM2)
	num := math.Max(p-pqC1, 0)
	den := math.Max(pqC2-pqC3*p, 1e-6)
	return math.Pow(num/den, 1.0/pqM1)
}

func pqOETF(l float64) float64 {
	if l <= 0 {
		return 0
	}
	if l >= 1 {
		return 1
	}
	lm := math.Pow(l, pqM1)
	return math.Pow((pqC1+pqC2*lm)/(1+pqC3*lm), pqM2)
}

func hlgInvOETF(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v <= 0.5 {
		return v * v / 3.0
	}
	return (math.Exp((v-hlgC)/hlgA) + hlgB) / 12.0
}

func srgbOETF(c float64) float64 {
	if c <= 0 {
		return 0
	}
	if c >= 1 {
		return 1
	}
	if c <= 0.0031308 {
		return 12.92 * c
	}
	return 1.055*math.Pow(c, 1.0/2.4) - 0.055
}

// bt2390Linear maps a linear peak luminance l (1.0 = 10000 nits) to SDR
// linear [0, 1] (1.0 = 203 nits) using the ITU-R BT.2390 EETF.
func bt2390Linear(l float64) float64 {
	if l <= 0 {
		return 0
	}
	e1 := pqOETF(l) / bt2390PQMax
	if e1 > 1 {
		e1 = 1
	}
	e2 := e1
	if e1 > bt2390KS {
		t := (e1 - bt2390KS) / (1.0 - bt2390KS)
		t2 := t * t
		t3 := t2 * t
		e2 = (2*t3-3*t2+1)*bt2390KS + (t3-2*t2+t)*(1-bt2390KS) + (-2*t3+3*t2)*bt2390MaxLum
	}
	return pqEOTF(e2*bt2390PQMax) * sdrWhiteScale
}

func initToneMap() {
	tmOnce.Do(func() {
		bt2390PQMax = pqOETF(hlgPeakNorm)
		bt2390MaxLum = pqOETF(sdrWhiteNorm) / bt2390PQMax
		bt2390KS = 1.5*bt2390MaxLum - 0.5
		bt2390KneeL = float32(pqEOTF(bt2390KS * bt2390PQMax))

		for i := range 4096 {
			v := float64(i) / 4095.0
			l := pqEOTF(v)
			pqEOTFLUT[i] = float32(l)
			if l > 0 {
				pqScaleLUT[i] = float32(bt2390Linear(l) / l)
			}
			hlgInvLUT[i] = float32(hlgInvOETF(v))
			if i > 0 {
				hlgOOTFLUT[i] = float32(math.Pow(v, 0.2) * hlgPeakNorm)
			}
			srgbOETFLUT[i] = uint8(math.Round(srgbOETF(v) * 255.0))
		}
	})
}

func toSRGB8(c float32) uint8 {
	if c <= 0 {
		return 0
	}
	if c >= 1 {
		return 255
	}
	return srgbOETFLUT[int(c*4095.0+0.5)]
}

// toneMapRGBA64 tone-maps w×h RGBA64LE HDR pixels (from swscale) into
// 8-bit sRGB RGBA in dst (with row stride bytes).
func toneMapRGBA64(dst []byte, stride int, src []byte, w, h int, trc, primaries int32) {
	initToneMap()
	bt2020 := primaries == ColorPrimariesBT2020 || primaries == ColorPrimariesUnspecified
	hlg := trc == ColorTrcARIBSTDB67

	for y := range h {
		drow := dst[y*stride : y*stride+w*4]
		srow := src[y*w*8 : (y+1)*w*8]
		for x := range w {
			so := x * 8
			ri := binary.LittleEndian.Uint16(srow[so:]) >> 4
			gi := binary.LittleEndian.Uint16(srow[so+2:]) >> 4
			bi := binary.LittleEndian.Uint16(srow[so+4:]) >> 4

			var r, g, b float32
			if hlg {
				r, g, b = hlgInvLUT[ri], hlgInvLUT[gi], hlgInvLUT[bi]
				ys := 0.2627*r + 0.6780*g + 0.0593*b
				if ys > 0 {
					yi := min(int(ys*4095.0+0.5), 4095)
					ootf := hlgOOTFLUT[yi]
					r *= ootf
					g *= ootf
					b *= ootf
					m := max(r, max(g, b))
					var scale float32
					if m <= bt2390KneeL {
						scale = sdrWhiteScale
					} else {
						scale = float32(bt2390Linear(float64(m))) / m
					}
					r *= scale
					g *= scale
					b *= scale
				} else {
					r, g, b = 0, 0, 0
				}
			} else {
				r, g, b = pqEOTFLUT[ri], pqEOTFLUT[gi], pqEOTFLUT[bi]
				mi := max(ri, max(gi, bi))
				scale := pqScaleLUT[mi]
				r *= scale
				g *= scale
				b *= scale
			}

			if bt2020 {
				r7 := 1.6604910*r - 0.5876411*g - 0.0728499*b
				g7 := -0.1245505*r + 1.1328999*g - 0.0083494*b
				b7 := -0.0181508*r - 0.1005789*g + 1.1187297*b
				r, g, b = r7, g7, b7
			}

			do := x * 4
			drow[do] = toSRGB8(r)
			drow[do+1] = toSRGB8(g)
			drow[do+2] = toSRGB8(b)
			drow[do+3] = 255
		}
	}
}
