package pipeline

import (
	"errors"
	"time"
	"unsafe"

	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

// GPUKind is how pictures reach a view on the GPU without a copy through
// memory.
type GPUKind int32

const (
	// GPUNone converts every picture to RGBA in memory.
	GPUNone GPUKind = iota
	// GPUD3D11 shares Direct3D 11 textures through NT handles (Windows,
	// D3D11VA).
	GPUD3D11
	// GPUDMABuf exports DMA-BUF planes (Linux, VAAPI).
	GPUDMABuf
	// GPUUpload hands the views HDR pictures' planes in memory, which
	// they upload each frame (macOS, until it imports VideoToolbox's
	// surfaces); SDR pictures convert to RGBA as with GPUNone.
	GPUUpload
)

// GPUPicture is a decoded picture on the GPU: a luma plane and a chroma
// plane at half its size, which a view imports and converts to RGB.
type GPUPicture struct {
	Kind GPUKind
	// Width and Height are the luma plane's size.
	Width, Height int
	// Deep pictures have 16-bit planes holding 10 bits (P010); others 8
	// (NV12).
	Deep bool
	// ColorSpace, ColorTrc and ColorPrimaries are FFmpeg's AVColorSpace,
	// AVColorTransferCharacteristic and AVColorPrimaries; FullRange says
	// the samples use every code value.
	ColorSpace     int32
	ColorTrc       int32
	ColorPrimaries int32
	FullRange      bool
	// Rotation is how far the picture turns clockwise to show, in degrees.
	Rotation int

	// Handle is the shared texture's NT handle (GPUD3D11), and ID tells
	// apart the textures one handle value names over time.
	Handle uintptr
	ID     uint64
	// Planes are the luma and chroma planes' DMA-BUFs (GPUDMABuf), or
	// their memory (GPUUpload).
	Planes [2]GPUPlane
}

// GPUPlane is one plane of a picture exported as DMA-BUF, or in memory.
type GPUPlane struct {
	FD       int
	Offset   uint32
	Pitch    uint32
	FourCC   uint32
	Modifier uint64
	// Data is the plane's rows, Pitch bytes apart (GPUUpload). It stays
	// valid while the frame is held or showing.
	Data []byte
}

var errNoGPU = errors.New("pipeline: pictures cannot go to the GPU here")

// SetGPU makes hardware pictures go to the views on the GPU as kind,
// GPUNone converting them to RGBA. Every view drawing the player must
// import kind. Pictures decoded in memory, those of the history and
// pictures of another device are converted as before.
func (p *Pipeline) SetGPU(kind GPUKind) { p.gpuKind.Store(int32(kind)) }

// gpuKindOf is the kind a hardware device's pictures go to the GPU as.
func gpuKindOf(device string) GPUKind {
	switch device {
	case "d3d11va":
		return GPUD3D11
	case "vaapi":
		return GPUDMABuf
	}
	return GPUNone
}

// toGPU queues the decoded picture in frame for the views on the GPU. It
// reports false, leaving frame as it is, when the picture is to be
// converted in memory instead; a nil frame with true means it was
// dropped, as the pipeline closed or a seek made it stale.
func (p *Pipeline) toGPU(frame *ffmpeg.Frame, device string, pts time.Duration, gen uint64) (*Frame, bool) {
	kind := GPUKind(p.gpuKind.Load())
	if kind == GPUNone || kind == GPUUpload || kind != gpuKindOf(device) || !frame.Hardware() {
		return nil, false
	}
	deep := false
	switch ffmpeg.PixelFormatName(frame.SoftwareFormat()) {
	case "nv12":
	case "p010le":
		deep = true
	default:
		return nil, false
	}
	stale := func() bool { return p.genA.Load() != gen || p.stopped() }
	out := p.pool.get(0, 0, stale)
	if out == nil {
		frame.Unref()
		return nil, true
	}
	src := ffmpeg.NewFrame()
	frame.MoveTo(src)
	*out.GPU = GPUPicture{
		Kind: kind, Width: src.Width(), Height: src.Height(), Deep: deep,
		ColorSpace: src.ColorSpace(), ColorTrc: src.ColorTrc(), ColorPrimaries: src.ColorPrimaries(),
		FullRange: src.FullRange(), Rotation: p.vp.Load().rot,
	}
	if err := out.gpu.export(src, out.GPU); err != nil {
		// The views get pictures in memory from here on.
		p.gpuKind.CompareAndSwap(int32(kind), int32(GPUNone))
		src.MoveTo(frame)
		src.Free()
		p.pool.put(out)
		return nil, false
	}
	out.PTS, out.gen, out.src = pts, gen, src
	return out, true
}

// toUpload queues src, a decoded picture in memory, for the views to
// upload when they take GPUUpload and it is HDR: its planes as NV12 or
// P010, converted to P010 from other formats. It reports false, leaving
// src as it is, when src is to be converted to RGBA instead; a nil frame
// with true means it was dropped and src freed.
func (p *Pipeline) toUpload(s *ffmpeg.Scaler, src *ffmpeg.Frame, pts time.Duration, gen uint64) (*Frame, bool) {
	if GPUKind(p.gpuKind.Load()) != GPUUpload || !src.HDR() {
		return nil, false
	}
	planar := src
	deep := true
	switch ffmpeg.PixelFormatName(src.Format()) {
	case "nv12":
		deep = false
	case "p010le":
	default:
		planar = ffmpeg.NewFrame()
		if planar.AllocBuffer(src.Width(), src.Height(), ffmpeg.PixelFormat("p010le")) != nil {
			planar.Free()
			return nil, false
		}
		planar.CopyColor(src)
		if s.Convert(planar, src) != nil {
			planar.Free()
			return nil, false
		}
	}
	stale := func() bool { return p.genA.Load() != gen || p.stopped() }
	out := p.pool.get(0, 0, stale)
	if out == nil {
		if planar != src {
			planar.Free()
		}
		src.Free()
		return nil, true
	}
	if planar != src {
		src.Free()
	}
	w, h := planar.Width(), planar.Height()
	*out.GPU = GPUPicture{
		Kind: GPUUpload, Width: w, Height: h, Deep: deep,
		ColorSpace: planar.ColorSpace(), ColorTrc: planar.ColorTrc(), ColorPrimaries: planar.ColorPrimaries(),
		FullRange: planar.FullRange(), Rotation: p.vp.Load().rot,
	}
	for i, rows := range []int{h, (h + 1) / 2} {
		data, pitch := planar.Plane(i)
		out.GPU.Planes[i] = GPUPlane{Pitch: uint32(pitch), Data: unsafe.Slice((*byte)(data), pitch*rows)}
	}
	// Snapshots convert the planes again.
	out.PTS, out.gen, out.src = pts, gen, planar
	return out, true
}
