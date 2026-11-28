package pipeline

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

func fourcc(a, b, c, d byte) uint32 {
	return uint32(a) | uint32(b)<<8 | uint32(c)<<16 | uint32(d)<<24
}

// The DRM formats of an NV12 and a P010 picture's planes, and of the
// pictures whole.
var (
	drmR8     = fourcc('R', '8', ' ', ' ')
	drmGR88   = fourcc('G', 'R', '8', '8')
	drmR16    = fourcc('R', '1', '6', ' ')
	drmGR1616 = fourcc('G', 'R', '3', '2')
	drmNV12   = fourcc('N', 'V', '1', '2')
	drmP010   = fourcc('P', '0', '1', '0')
)

const (
	vaMemTypeDRMPrime2     = 0x40000000 // VA_SURFACE_ATTRIB_MEM_TYPE_DRM_PRIME_2
	vaExportReadOnly       = 0x1        // VA_EXPORT_SURFACE_READ_ONLY
	vaExportSeparateLayers = 0x4        // VA_EXPORT_SURFACE_SEPARATE_LAYERS
)

// vaDRMPrime is libva's VADRMPRIMESurfaceDescriptor.
type vaDRMPrime struct {
	fourcc, width, height uint32
	numObjects            uint32
	objects               [4]struct {
		fd       int32
		size     uint32
		modifier uint64
	}
	numLayers uint32
	layers    [4]struct {
		format    uint32
		numPlanes uint32
		object    [4]uint32
		offset    [4]uint32
		pitch     [4]uint32
	}
}

// libva, the system's, which FFmpeg's VAAPI decoder loaded already.
var va struct {
	once          sync.Once
	err           error
	syncSurface   func(dpy unsafe.Pointer, surface uint32) int32
	exportSurface func(dpy unsafe.Pointer, surface, memType, flags uint32, desc *vaDRMPrime) int32
}

func loadVA() error {
	va.once.Do(func() {
		h, err := purego.Dlopen("libva.so.2", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			va.err = err
			return
		}
		defer func() {
			if r := recover(); r != nil {
				va.err = fmt.Errorf("pipeline: libva: %v", r)
			}
		}()
		purego.RegisterLibFunc(&va.syncSurface, h, "vaSyncSurface")
		purego.RegisterLibFunc(&va.exportSurface, h, "vaExportSurfaceHandle")
	})
	return va.err
}

// gpuState holds the DMA-BUFs a VAAPI surface exported as, while the
// frame is out of the pool.
type gpuState struct {
	fds []int
}

var errDRMLayout = errors.New("pipeline: the surface exports as no NV12 or P010 planes")

// export exports the surface's planes as DMA-BUFs with libva, as FFmpeg
// maps a surface to DRM objects when it is built with libdrm.
func (s *gpuState) export(src *ffmpeg.Frame, pic *GPUPicture) error {
	dpy, surface, ok := src.VAAPI()
	if !ok {
		return errNoGPU
	}
	if err := loadVA(); err != nil {
		return err
	}
	if st := va.syncSurface(dpy, surface); st != 0 {
		return fmt.Errorf("pipeline: vaSyncSurface: %d", st)
	}
	var d vaDRMPrime
	if st := va.exportSurface(dpy, surface, vaMemTypeDRMPrime2, vaExportReadOnly|vaExportSeparateLayers, &d); st != 0 {
		return fmt.Errorf("pipeline: vaExportSurfaceHandle: %d", st)
	}
	for _, o := range d.objects[:min(d.numObjects, 4)] {
		s.fds = append(s.fds, int(o.fd))
	}
	luma, chroma, whole := drmR8, drmGR88, drmNV12
	if pic.Deep {
		luma, chroma, whole = drmR16, drmGR1616, drmP010
	}
	type plane struct{ layer, index int }
	var planes [2]plane
	switch l := d.layers; {
	// A layer for each plane, as the export was asked for.
	case d.numLayers == 2 && l[0].numPlanes == 1 && l[1].numPlanes == 1 && l[0].format == luma && l[1].format == chroma:
		planes = [2]plane{{0, 0}, {1, 0}}
	case d.numLayers == 1 && l[0].numPlanes == 2 && l[0].format == whole:
		planes = [2]plane{{0, 0}, {0, 1}}
	default:
		s.unmap()
		return errDRMLayout
	}
	for i, pl := range planes {
		l := d.layers[pl.layer]
		obj := l.object[pl.index]
		if obj >= d.numObjects || obj >= 4 {
			s.unmap()
			return errDRMLayout
		}
		pic.Planes[i] = GPUPlane{
			FD: int(d.objects[obj].fd), Offset: l.offset[pl.index], Pitch: l.pitch[pl.index],
			FourCC: [2]uint32{luma, chroma}[i], Modifier: d.objects[obj].modifier,
		}
	}
	return nil
}

// unmap closes the DMA-BUFs; what imported them keeps its own reference.
func (s *gpuState) unmap() {
	for _, fd := range s.fds {
		syscall.Close(fd)
	}
	s.fds = s.fds[:0]
}

func (s *gpuState) free() { s.unmap() }

// gpuSurfaces are the hardware surfaces frames on the GPU hold: one for
// each frame of the pool.
const gpuSurfaces = poolFrames
