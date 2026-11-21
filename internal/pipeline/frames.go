package pipeline

import (
	"image"
	"sync"
	"time"
	"unsafe"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// Frame is a decoded picture in RGBA, premultiplied as it is opaque. Its
// pixels are memory FFmpeg's converter writes; they stay valid until the
// pipeline is closed or the frame goes back to the pool.
type Frame struct {
	Image *image.RGBA
	PTS   time.Duration
	gen   uint64
	mem   unsafe.Pointer
	size  int
	// src is the decoded picture, in memory, the frame was converted
	// from, which snapshots convert again at the source's size.
	src *ffmpeg.Frame
	// loose is a frame outside the pool, which the presenter converted
	// from the history; releasing it frees it.
	loose bool
}

// freeSource drops the decoded picture the frame holds.
func (f *Frame) freeSource() {
	if f.src != nil {
		f.src.Free()
		f.src = nil
	}
}

// framePool bounds the frames of a player: those queued for display,
// the one shown and the one retiring. get waits while all are out.
type framePool struct {
	mu     sync.Mutex
	cond   *sync.Cond
	free   []*Frame
	out    int
	limit  int
	closed bool
}

func newFramePool(limit int) *framePool {
	p := &framePool{limit: limit}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// get returns a frame of w×h, or nil once closed or when abort reports
// true while it waits.
func (p *framePool) get(w, h int, abort func() bool) *Frame {
	size := w * h * 4
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		if p.closed || abort() {
			return nil
		}
		for i, f := range p.free {
			if f.size == size {
				p.free = append(p.free[:i], p.free[i+1:]...)
				p.out++
				f.Image.Rect = image.Rect(0, 0, w, h)
				f.Image.Stride = w * 4
				return f
			}
		}
		if p.out+len(p.free) < p.limit || len(p.free) > 0 {
			// A free frame of another size is given up for this one.
			if len(p.free) > 0 {
				ffmpeg.Free(p.free[0].mem)
				p.free = p.free[1:]
			}
			mem := ffmpeg.Alloc(size)
			if mem == nil {
				return nil
			}
			p.out++
			return &Frame{mem: mem, size: size, Image: &image.RGBA{
				Pix: unsafe.Slice((*byte)(mem), size), Stride: w * 4, Rect: image.Rect(0, 0, w, h),
			}}
		}
		p.cond.Wait()
	}
}

func (p *framePool) put(f *Frame) {
	if f == nil {
		return
	}
	f.freeSource()
	p.mu.Lock()
	p.out--
	if p.closed {
		ffmpeg.Free(f.mem)
	} else {
		p.free = append(p.free, f)
	}
	p.cond.Broadcast()
	p.mu.Unlock()
}

// wakeAll wakes waiting gets so they check their abort.
func (p *framePool) wakeAll() {
	p.mu.Lock()
	p.cond.Broadcast()
	p.mu.Unlock()
}

// close frees the free frames; frames still out are freed as they come
// back.
func (p *framePool) close() {
	p.mu.Lock()
	p.closed = true
	for _, f := range p.free {
		ffmpeg.Free(f.mem)
	}
	p.free = nil
	p.cond.Broadcast()
	p.mu.Unlock()
}

// release gives a frame back: to the pool, or freed when loose.
func (p *Pipeline) release(f *Frame) {
	switch {
	case f == nil:
	case f.loose:
		f.freeSource()
		ffmpeg.Free(f.mem)
	default:
		p.pool.put(f)
	}
}

// frameQueue holds decoded frames in display order for the presenter.
type frameQueue struct {
	mu     sync.Mutex
	frames []*Frame
	eof    uint64 // the generation whose last frame is queued, 0 for none
}

func (q *frameQueue) push(f *Frame) {
	q.mu.Lock()
	q.frames = append(q.frames, f)
	q.mu.Unlock()
}

func (q *frameQueue) setEOF(gen uint64) {
	q.mu.Lock()
	q.eof = gen
	q.mu.Unlock()
}

// drain removes every frame for the pool.
func (q *frameQueue) drain(pool *framePool) {
	q.mu.Lock()
	frames := q.frames
	q.frames = nil
	q.eof = 0
	q.mu.Unlock()
	for _, f := range frames {
		pool.put(f)
	}
}
