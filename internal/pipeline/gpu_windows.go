package pipeline

import (
	"fmt"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

// Vtable slots of the COM methods called.
const (
	comRelease                  = 2
	d3d11DeviceCreateTexture2D  = 5
	d3d11TextureGetDesc         = 10
	d3d11ContextCopySubresource = 46
	dxgiResource1SharedHandle   = 13
	dxgiKeyedMutexAcquireSync   = 8
	dxgiKeyedMutexReleaseSync   = 9
)

const (
	dxgiFormatNV12          = 103
	dxgiFormatP010          = 104
	d3d11BindShaderResource = 0x8
	d3d11MiscSharedKeyed    = 0x100
	d3d11MiscSharedNTHandle = 0x800
	dxgiSharedRead          = 0x80000000
	dxgiSharedWrite         = 1
	waitTimeout             = 0x102 // WAIT_TIMEOUT
)

var (
	iidDXGIResource1  = guid{0x30961379, 0x4609, 0x4a41, [8]byte{0x99, 0x8e, 0x54, 0xfe, 0x56, 0x7e, 0xe0, 0xc1}}
	iidDXGIKeyedMutex = guid{0x9d8e1289, 0xd7b3, 0x465f, [8]byte{0x81, 0x26, 0x25, 0x0e, 0x34, 0x9a, 0xf8, 0x5d}}
)

type guid struct {
	a    uint32
	b, c uint16
	d    [8]byte
}

type texture2DDesc struct {
	width, height, mipLevels, arraySize uint32
	format                              uint32
	sampleCount, sampleQuality          uint32
	usage, bindFlags, cpuFlags, misc    uint32
}

type box struct{ left, top, front, right, bottom, back uint32 }

// call calls method slot of the COM object obj.
func call(obj unsafe.Pointer, slot int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

func release(obj unsafe.Pointer) {
	if obj != nil {
		call(obj, comRelease)
	}
}

func queryInterface(obj unsafe.Pointer, iid *guid) unsafe.Pointer {
	var out unsafe.Pointer
	if failed(call(obj, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))) {
		return nil
	}
	return out
}

var sharedIDs atomic.Uint64

// gpuState is a texture the frame's pictures are copied into on the
// decoder's device, shared through an NT handle with a keyed mutex: a
// D3D11VA decoder writes slices of one texture array, which cannot be
// shared a slice at a time. The copy stays on the GPU.
type gpuState struct {
	tex, mutex unsafe.Pointer
	handle     uintptr
	id         uint64
	device     unsafe.Pointer
	w, h       int
	format     uint32
	used       bool // the decoder took the mutex once, so its key is 1
}

// The keyed mutex goes from the decoder to the views as key 1, and the
// views, which take it each frame they draw, give it back as 1.
const viewKey = 1

func (s *gpuState) export(src *ffmpeg.Frame, pic *GPUPicture) error {
	surf, ok := src.D3D11()
	if !ok {
		return errNoGPU
	}
	format := uint32(dxgiFormatNV12)
	if pic.Deep {
		format = dxgiFormatP010
	}
	// NV12 and P010 textures have even sizes.
	w, h := (pic.Width+1)&^1, (pic.Height+1)&^1
	if s.tex == nil || s.device != surf.Device || s.w != w || s.h != h || s.format != format {
		s.free()
		if err := s.create(surf.Device, w, h, format); err != nil {
			return err
		}
	}
	var in texture2DDesc
	call(surf.Texture, d3d11TextureGetDesc, uintptr(unsafe.Pointer(&in)))
	b := box{right: uint32(min(w, int(in.width))), bottom: uint32(min(h, int(in.height))), back: 1}
	key := uint64(0)
	if s.used {
		key = viewKey
	}
	if hr := call(s.mutex, dxgiKeyedMutexAcquireSync, uintptr(key), 1000); failed(hr) || hr == waitTimeout {
		return fmt.Errorf("pipeline: a view kept the shared texture (%#x)", hr)
	}
	s.used = true
	surf.Lock()
	call(surf.Context, d3d11ContextCopySubresource, uintptr(s.tex), 0, 0, 0, 0,
		uintptr(surf.Texture), uintptr(surf.Index*int(max(in.mipLevels, 1))), uintptr(unsafe.Pointer(&b)))
	surf.Unlock()
	call(s.mutex, dxgiKeyedMutexReleaseSync, viewKey)
	pic.Width, pic.Height = w, h
	pic.Handle, pic.ID = s.handle, s.id
	return nil
}

func (s *gpuState) create(device unsafe.Pointer, w, h int, format uint32) error {
	desc := texture2DDesc{
		width: uint32(w), height: uint32(h), mipLevels: 1, arraySize: 1, format: format, sampleCount: 1,
		bindFlags: d3d11BindShaderResource, misc: d3d11MiscSharedNTHandle | d3d11MiscSharedKeyed,
	}
	var tex unsafe.Pointer
	if hr := call(device, d3d11DeviceCreateTexture2D, uintptr(unsafe.Pointer(&desc)), 0, uintptr(unsafe.Pointer(&tex))); failed(hr) {
		return fmt.Errorf("pipeline: create a shared texture: %#x", hr)
	}
	res := queryInterface(tex, &iidDXGIResource1)
	mutex := queryInterface(tex, &iidDXGIKeyedMutex)
	var handle uintptr
	hr := uintptr(0x80004002) // E_NOINTERFACE
	if res != nil {
		hr = call(res, dxgiResource1SharedHandle, 0, dxgiSharedRead|dxgiSharedWrite, 0, uintptr(unsafe.Pointer(&handle)))
		release(res)
	}
	if failed(hr) || mutex == nil {
		release(mutex)
		release(tex)
		return fmt.Errorf("pipeline: share a texture: %#x", hr)
	}
	*s = gpuState{tex: tex, mutex: mutex, handle: handle, id: sharedIDs.Add(1), device: device, w: w, h: h, format: format}
	return nil
}

// unmap leaves the texture for the frame's next picture.
func (s *gpuState) unmap() {}

func (s *gpuState) free() {
	if s.tex == nil {
		return
	}
	syscall.CloseHandle(syscall.Handle(s.handle))
	release(s.mutex)
	release(s.tex)
	*s = gpuState{}
}

// gpuSurfaces are the hardware surfaces frames on the GPU hold, for
// snapshots: one for each frame of the pool.
const gpuSurfaces = poolFrames
