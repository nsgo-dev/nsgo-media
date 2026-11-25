package ffmpeg

import (
	"errors"
	"runtime"
	"unsafe"
)

const (
	swsFastBilinear = 1 << 0
	swsBilinear     = 1 << 1

	colorSpaceRGB         = 0
	colorSpaceUnspecified = 2
	colorSpaceBT709       = 1
)

// Scaler converts video frames to RGBA at a size through swscale's frame
// API, which follows each frame's color space and range and splits the
// work over the CPU's cores.
type Scaler struct {
	ctx     unsafe.Pointer
	dst     *Frame
	rgba    int32
	rgba64  int32
	w, h    int // the last output size, to choose the filter
	hdrBuf  unsafe.Pointer
	hdrSize int
}

// Scale converts f into dst, w×h pixels with rows of stride bytes of RGBA,
// in memory from Alloc. HDR frames (PQ and HLG) are tone-mapped to sRGB.
func (s *Scaler) Scale(f *Frame, dst unsafe.Pointer, stride, w, h int) error {
	if s.ctx == nil {
		s.rgba = avGetPixFmt("rgba")
		s.rgba64 = avGetPixFmt("rgba64le")
		s.ctx = swsAllocContext()
		s.dst = NewFrame()
		if s.ctx == nil || s.dst == nil {
			return errors.New("ffmpeg: out of memory")
		}
		avOptSetInt(s.ctx, "threads", int64(min(runtime.NumCPU(), 8)), 0)
	}
	if w != s.w || h != s.h {
		flags := int64(swsBilinear)
		if w == f.Width() && h == f.Height() {
			flags = swsFastBilinear
		}
		avOptSetInt(s.ctx, "sws_flags", flags, 0)
		s.w, s.h = w, h
	}
	hdr := f.HDR()
	if f.ColorSpace() == colorSpaceUnspecified {
		if hdr {
			*(*int32)(at(f.p(), offAVFrameColorspace)) = ColorSpaceBT2020NCL
		} else if f.Height() >= 720 {
			// Untagged HD pictures are BT.709 in practice; swscale would take
			// them for BT.601.
			*(*int32)(at(f.p(), offAVFrameColorspace)) = colorSpaceBT709
		}
	}
	outPtr, outStride, outFmt := dst, stride, s.rgba
	if hdr {
		need := w * h * 8
		if need > s.hdrSize {
			if s.hdrBuf != nil {
				avFree(s.hdrBuf)
			}
			s.hdrBuf = avMalloc(uintptr(need))
			if s.hdrBuf == nil {
				s.hdrSize = 0
				return errors.New("ffmpeg: out of memory")
			}
			s.hdrSize = need
		}
		outPtr, outStride, outFmt = s.hdrBuf, w*8, s.rgba64
	}
	d := s.dst.p()
	*(*unsafe.Pointer)(at(d, offAVFrameData)) = outPtr
	*(*int32)(at(d, offAVFrameLinesize)) = int32(outStride)
	*(*int32)(at(d, offAVFrameWidth)) = int32(w)
	*(*int32)(at(d, offAVFrameHeight)) = int32(h)
	*(*int32)(at(d, offAVFrameFormat)) = outFmt
	*(*int32)(at(d, offAVFrameColorspace)) = colorSpaceRGB
	*(*int32)(at(d, offAVFrameColorRange)) = colorRangeJPEG
	if hdr {
		*(*int32)(at(d, offAVFrameColorTrc)) = f.ColorTrc()
		*(*int32)(at(d, offAVFrameColorPrimaries)) = f.ColorPrimaries()
	} else {
		*(*int32)(at(d, offAVFrameColorTrc)) = ColorTrcUnspecified
		*(*int32)(at(d, offAVFrameColorPrimaries)) = ColorPrimariesUnspecified
	}
	err := check(swsScaleFrame(s.ctx, s.dst, f))
	*(*unsafe.Pointer)(at(d, offAVFrameData)) = nil
	if err != nil {
		return errors.New("ffmpeg: cannot convert " + PixelFormatName(f.Format()) + " to RGBA: " + err.Error())
	}
	if hdr {
		toneMapRGBA64(
			unsafe.Slice((*byte)(dst), stride*h), stride,
			unsafe.Slice((*byte)(s.hdrBuf), w*h*8), w, h,
			f.ColorTrc(), f.ColorPrimaries(),
		)
	}
	return nil
}

// Convert scales and converts src into dst, both already configured with
// their geometry, pixel format and plane buffers.
func (s *Scaler) Convert(dst, src *Frame) error {
	if s.ctx == nil {
		s.ctx = swsAllocContext()
		if s.ctx == nil {
			return errors.New("ffmpeg: out of memory")
		}
		avOptSetInt(s.ctx, "threads", int64(min(runtime.NumCPU(), 8)), 0)
	}
	if dst.Width() != s.w || dst.Height() != s.h {
		flags := int64(swsBilinear)
		if dst.Width() == src.Width() && dst.Height() == src.Height() {
			flags = swsFastBilinear
		}
		avOptSetInt(s.ctx, "sws_flags", flags, 0)
		s.w, s.h = dst.Width(), dst.Height()
	}
	if err := check(swsScaleFrame(s.ctx, dst, src)); err != nil {
		return errors.New("ffmpeg: cannot convert " + PixelFormatName(src.Format()) + " to " + PixelFormatName(dst.Format()) + ": " + err.Error())
	}
	return nil
}

// Close frees the context.
func (s *Scaler) Close() {
	if s.ctx != nil {
		swsFreeContext(s.ctx)
		s.ctx = nil
	}
	if s.dst != nil {
		s.dst.Free()
		s.dst = nil
	}
	if s.hdrBuf != nil {
		avFree(s.hdrBuf)
		s.hdrBuf = nil
		s.hdrSize = 0
	}
}

// Alloc allocates n bytes FFmpeg may write, aligned for its SIMD code.
func Alloc(n int) unsafe.Pointer { return avMalloc(uintptr(n)) }

// Free frees memory from Alloc.
func Free(p unsafe.Pointer) { avFree(p) }
