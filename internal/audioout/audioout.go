// Package audioout plays interleaved float32 PCM on the system's default
// output device, with one backend per desktop: CoreAudio's AudioQueue on
// macOS, WASAPI on Windows and PulseAudio (or PipeWire's server for it) on
// Linux.
package audioout

import (
	"sync"
	"time"
	"unsafe"
)

// Fill writes the next samples, interleaved by channel, into dst, all of
// it: silence where there is nothing to play. Backends call it on their own
// thread, so it must return quickly and never wait for the engine.
type Fill func(dst []float32)

// Output is an open device stream.
type Output interface {
	// Rate and Channels are the stream's format, which Fill writes.
	Rate() int
	Channels() int
	// Played is how many frames (samples per channel) the device has
	// played since the stream opened, as near as the system tells; it
	// holds while paused.
	Played() int64
	// SetPaused stops and resumes asking for samples.
	SetPaused(paused bool)
	// Close stops the stream; Fill is not called once it returns.
	Close()
}

// Open opens the default output device, falling back to a silent output
// paced by the clock when there is none.
func Open(fill Fill) (Output, error) {
	out, err := open(fill)
	if err != nil {
		return NewNull(48000, 2, fill), err
	}
	return out, nil
}

// null is an output without a device: a goroutine asks for samples at the
// rate a device would and drops them.
type null struct {
	rate, channels int
	fill           Fill
	mu             sync.Mutex
	played         int64
	paused         bool
	done           chan struct{}
	wg             sync.WaitGroup
}

// NewNull returns an output that plays nothing at rate and channels, for
// machines without a device and for tests.
func NewNull(rate, channels int, fill Fill) Output {
	n := &null{rate: rate, channels: channels, fill: fill, done: make(chan struct{})}
	n.wg.Add(1)
	go n.run()
	return n
}

func (n *null) run() {
	defer n.wg.Done()
	const period = 10 * time.Millisecond
	frames := n.rate * int(period) / int(time.Second)
	buf := make([]float32, frames*n.channels)
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-n.done:
			return
		case <-t.C:
		}
		n.mu.Lock()
		paused := n.paused
		n.mu.Unlock()
		if paused {
			continue
		}
		n.fill(buf)
		n.mu.Lock()
		n.played += int64(frames)
		n.mu.Unlock()
	}
}

func (n *null) Rate() int     { return n.rate }
func (n *null) Channels() int { return n.channels }
func (n *null) Played() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.played
}
func (n *null) SetPaused(p bool) {
	n.mu.Lock()
	n.paused = p
	n.mu.Unlock()
}
func (n *null) Close() {
	select {
	case <-n.done:
	default:
		close(n.done)
	}
	n.wg.Wait()
}

// pointer turns an address a C callback passed as an integer back into a
// pointer.
func pointer(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }
