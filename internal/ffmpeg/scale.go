package ffmpeg

import (
	"errors"
	"unsafe"
)

const (
	swsFastBilinear = 1 << 0
	swsBilinear     = 1 << 1
	swsCSITU709     = 1
	swsCSDefault    = 5
	swsCSBT2020     = 9
)

// Scaler converts video frames to RGBA at a size, keeping its swscale
// context while the source and the size stay the same.
type Scaler struct {
	ctx                unsafe.Pointer
	srcW, srcH, srcFmt int32
	dstW, dstH         int32
	colorSpace         int32
	fullRange          bool
	rgba               int32
}

// Scale converts f into dst, w×h pixels with rows of stride bytes of RGBA,
// in memory C may write (the pipeline's frame buffers come from Alloc).
func (s *Scaler) Scale(f *Frame, dst unsafe.Pointer, stride, w, h int) error {
	if s.rgba == 0 {
		s.rgba = avGetPixFmt("rgba")
	}
	sw, sh, sf := int32(f.Width()), int32(f.Height()), f.Format()
	cs, full := f.ColorSpace(), f.FullRange()
	if s.ctx == nil || sw != s.srcW || sh != s.srcH || sf != s.srcFmt || int32(w) != s.dstW || int32(h) != s.dstH {
		flags := int32(swsBilinear)
		if int32(w) == sw && int32(h) == sh {
			flags = swsFastBilinear
		}
		s.ctx = swsGetCachedContext(s.ctx, sw, sh, sf, int32(w), int32(h), s.rgba, flags, nil, nil, nil)
		if s.ctx == nil {
			return errors.New("ffmpeg: cannot convert " + PixelFormatName(sf) + " to RGBA")
		}
		s.srcW, s.srcH, s.srcFmt, s.dstW, s.dstH = sw, sh, sf, int32(w), int32(h)
		s.colorSpace, s.fullRange = -1, !full
	}
	if cs != s.colorSpace || full != s.fullRange {
		// swscale assumes BT.601 limited range unless told otherwise.
		table := int32(swsCSDefault)
		switch cs {
		case ColorSpaceBT709:
			table = swsCSITU709
		case ColorSpaceBT2020NCL, ColorSpaceBT2020CL:
			table = swsCSBT2020
		case 2: // unspecified: HD pictures are BT.709 in practice
			if sh >= 720 {
				table = swsCSITU709
			}
		}
		srcRange := int32(0)
		if full {
			srcRange = 1
		}
		swsSetColorspaceDetails(s.ctx, swsGetCoefficients(table), srcRange, swsGetCoefficients(swsCSDefault), 1, 0, 1<<16, 1<<16)
		s.colorSpace, s.fullRange = cs, full
	}
	dstData := [4]unsafe.Pointer{dst}
	dstStride := [4]int32{int32(stride)}
	if swsScale(s.ctx, f.dataArray(), f.linesizeArray(), 0, sh, &dstData[0], &dstStride[0]) <= 0 {
		return errors.New("ffmpeg: scaling failed")
	}
	return nil
}

// Close frees the context.
func (s *Scaler) Close() {
	if s.ctx != nil {
		swsFreeContext(s.ctx)
		s.ctx = nil
	}
}

// Alloc allocates n bytes FFmpeg may write, aligned for its SIMD code.
func Alloc(n int) unsafe.Pointer { return avMalloc(uintptr(n)) }

// Free frees memory from Alloc.
func Free(p unsafe.Pointer) { avFree(p) }
