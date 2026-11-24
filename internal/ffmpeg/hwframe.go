package ffmpeg

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// framesContext is a hardware frame's AVHWFramesContext, nil for a frame
// in memory.
func (f *Frame) framesContext() unsafe.Pointer {
	ref := pp(f.p(), offAVFrameHwFramesCtx)
	if ref == nil {
		return nil
	}
	return pp(ref, offAVBufferRefData)
}

// SoftwareFormat is the pixel format a hardware frame's surface holds,
// such as nv12 or p010le, -1 for a frame in memory.
func (f *Frame) SoftwareFormat() int32 {
	fc := f.framesContext()
	if fc == nil {
		return -1
	}
	return i32(fc, offAVHWFramesContextSwFormat)
}

// D3D11Surface is where a D3D11VA frame decoded: a slice of a texture
// array on the decoder's device.
type D3D11Surface struct {
	Texture unsafe.Pointer // ID3D11Texture2D
	Index   int            // the array slice
	Device  unsafe.Pointer // ID3D11Device
	Context unsafe.Pointer // ID3D11DeviceContext, used under Lock
	dev     unsafe.Pointer // AVD3D11VADeviceContext
}

// D3D11 is a D3D11VA frame's surface; ok is false for other frames.
func (f *Frame) D3D11() (s D3D11Surface, ok bool) {
	fc := f.framesContext()
	if fc == nil || f.Format() != avGetPixFmt("d3d11") {
		return s, false
	}
	dc := pp(fc, offAVHWFramesContextDeviceCtx)
	hw := pp(dc, offAVHWDeviceContextHwctx)
	tex, _ := f.Plane(0)
	s = D3D11Surface{
		Texture: tex,
		Index:   int(uintptr(pp(f.p(), offAVFrameData+8))),
		Device:  pp(hw, offAVD3D11VADeviceContextDevice),
		Context: pp(hw, offAVD3D11VADeviceContextDeviceContext),
		dev:     hw,
	}
	return s, s.Texture != nil && s.Device != nil && s.Context != nil
}

// Lock takes the lock FFmpeg holds while it uses the device's context.
func (s D3D11Surface) Lock() {
	purego.SyscallN(uintptr(pp(s.dev, offAVD3D11VADeviceContextLock)), uintptr(pp(s.dev, offAVD3D11VADeviceContextLockCtx)))
}

// Unlock releases what Lock took.
func (s D3D11Surface) Unlock() {
	purego.SyscallN(uintptr(pp(s.dev, offAVD3D11VADeviceContextUnlock)), uintptr(pp(s.dev, offAVD3D11VADeviceContextLockCtx)))
}

// VAAPI is a VAAPI frame's display and surface; ok is false for other
// frames.
func (f *Frame) VAAPI() (display unsafe.Pointer, surface uint32, ok bool) {
	fc := f.framesContext()
	if fc == nil || f.Format() != avGetPixFmt("vaapi") {
		return nil, 0, false
	}
	hw := pp(pp(fc, offAVHWFramesContextDeviceCtx), offAVHWDeviceContextHwctx)
	// The surface's ID is the frame's fourth data pointer.
	return pp(hw, offAVVAAPIDeviceContextDisplay), uint32(uintptr(pp(f.p(), offAVFrameData+3*8))), true
}
