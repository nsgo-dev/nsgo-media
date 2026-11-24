package pipeline

import (
	"testing"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

func TestHeldFrameParksUntilReleased(t *testing.T) {
	if err := ffmpeg.Load(); err != nil {
		t.Skip(err)
	}
	p := &Pipeline{pool: newFramePool(2)}
	never := func() bool { return false }
	f := p.pool.get(0, 0, never)
	if f == nil || f.GPU == nil || f.Image != nil {
		t.Fatalf("a frame for the GPU = %+v, want a picture and no image", f)
	}
	release := p.Hold(f)
	p.pool.put(f)
	if p.pool.out != 1 || len(p.pool.free) != 0 || !f.parked {
		t.Fatalf("a held frame given back: out %d, free %d, parked %v; want it kept out", p.pool.out, len(p.pool.free), f.parked)
	}
	release()
	release() // a second call does nothing
	if p.pool.out != 0 || len(p.pool.free) != 1 || f.parked || f.holds != 0 {
		t.Fatalf("after the hold: out %d, free %d, parked %v, holds %d; want it back in the pool", p.pool.out, len(p.pool.free), f.parked, f.holds)
	}
	// A frame for the GPU is reused for the next, and an RGBA one gives it up.
	if g := p.pool.get(0, 0, never); g != f {
		t.Fatal("the free frame for the GPU was not reused")
	}
	p.pool.put(f)
	if g := p.pool.get(4, 2, never); g == f || g.Image == nil || len(g.Image.Pix) != 32 {
		t.Fatalf("an RGBA frame = %+v", g)
	}
	p.pool.close()
}

func TestHeldFrameReleasedAfterClose(t *testing.T) {
	p := &Pipeline{pool: newFramePool(2)}
	f := p.pool.get(0, 0, func() bool { return false })
	release := p.Hold(f)
	p.pool.put(f)
	p.pool.close()
	release()
	if p.pool.out != 0 || len(p.pool.free) != 0 {
		t.Fatalf("after close: out %d, free %d; want the frame freed", p.pool.out, len(p.pool.free))
	}
}

func TestGPUKindOfDevice(t *testing.T) {
	for dev, want := range map[string]GPUKind{"d3d11va": GPUD3D11, "vaapi": GPUDMABuf, "videotoolbox": GPUNone, "": GPUNone} {
		if got := gpuKindOf(dev); got != want {
			t.Errorf("gpuKindOf(%q) = %d, want %d", dev, got, want)
		}
	}
}
