package audioout

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The Linux backend is a PulseAudio simple stream of float32 stereo at
// 48 kHz, which PipeWire's server answers too. A goroutine fills a period
// and writes it, the write blocking while the server's buffer is full.

const (
	paRate       = 48000
	paChannels   = 2
	paPeriod     = 480 // frames, 10 ms
	paStreamPlay = 1   // PA_STREAM_PLAYBACK
	paFloat32LE  = 5   // PA_SAMPLE_FLOAT32LE
)

var (
	paOnce sync.Once
	paErr  error

	paSimpleNew        func(server uintptr, name string, dir int32, dev uintptr, stream string, ss, cmap, attr unsafe.Pointer, err *int32) uintptr
	paSimpleWrite      func(s uintptr, data unsafe.Pointer, n uintptr, err *int32) int32
	paSimpleGetLatency func(s uintptr, err *int32) uint64
	paSimpleFree       func(s uintptr)
)

func loadPulse() error {
	paOnce.Do(func() {
		lib, err := purego.Dlopen("libpulse-simple.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			paErr = err
			return
		}
		for name, fn := range map[string]any{
			"pa_simple_new":         &paSimpleNew,
			"pa_simple_write":       &paSimpleWrite,
			"pa_simple_get_latency": &paSimpleGetLatency,
			"pa_simple_free":        &paSimpleFree,
		} {
			purego.RegisterLibFunc(fn, lib, name)
		}
	})
	return paErr
}

type pulse struct {
	s       uintptr
	fill    Fill
	mu      sync.Mutex
	cond    *sync.Cond
	paused  bool
	closed  bool
	written int64 // frames written to the server
	latency int64 // frames the server held at the last write
	wg      sync.WaitGroup
}

func open(fill Fill) (Output, error) {
	if err := loadPulse(); err != nil {
		return nil, err
	}
	ss := struct {
		format   uint32
		rate     uint32
		channels uint8
	}{paFloat32LE, paRate, paChannels}
	// pa_buffer_attr: a 40 ms target keeps the device near the clock.
	none := ^uint32(0)
	attr := struct{ maxlength, tlength, prebuf, minreq, fragsize uint32 }{none, paRate * paChannels * 4 * 40 / 1000, none, none, none}
	var code int32
	s := paSimpleNew(0, "NSGo Media", paStreamPlay, 0, "Playback", unsafe.Pointer(&ss), nil, unsafe.Pointer(&attr), &code)
	if s == 0 {
		return nil, fmt.Errorf("audioout: pa_simple_new: error %d", code)
	}
	p := &pulse{s: s, fill: fill}
	p.cond = sync.NewCond(&p.mu)
	p.wg.Add(1)
	go p.run()
	return p, nil
}

func (p *pulse) run() {
	defer p.wg.Done()
	buf := make([]float32, paPeriod*paChannels)
	for {
		p.mu.Lock()
		for p.paused && !p.closed {
			p.cond.Wait()
		}
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return
		}
		p.fill(buf)
		var code int32
		if paSimpleWrite(p.s, unsafe.Pointer(&buf[0]), uintptr(len(buf)*4), &code) < 0 {
			return
		}
		lat := paSimpleGetLatency(p.s, &code)
		p.mu.Lock()
		p.written += paPeriod
		p.latency = int64(lat) * paRate / 1000000
		p.mu.Unlock()
	}
}

func (p *pulse) Rate() int     { return paRate }
func (p *pulse) Channels() int { return paChannels }

func (p *pulse) Played() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.paused && !p.closed {
		var code int32
		p.latency = int64(paSimpleGetLatency(p.s, &code)) * paRate / 1000000
	}
	return max(p.written-p.latency, 0)
}

func (p *pulse) SetPaused(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if paused == p.paused {
		return
	}
	// The server plays out the few milliseconds it holds, so every frame
	// Fill wrote is played and Played stays Fill's count.
	p.paused = paused
	p.cond.Broadcast()
}

func (p *pulse) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	p.wg.Wait()
	paSimpleFree(p.s)
}
