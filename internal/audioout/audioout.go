// Package audioout plays interleaved float32 PCM on the system's default
// output device, with one backend per system: CoreAudio's AudioQueue on
// macOS and iOS, WASAPI on Windows and PulseAudio (or PipeWire's server for
// it) on Linux. It also passes IEC 61937 streams through to a receiver
// that decodes them on the desktops: CoreAudio's encoded formats with the
// device hogged, WASAPI's exclusive mode and PulseAudio's passthrough
// formats. Watch reports what iOS does to the output under the app.
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

// FillIEC writes the next frames of an IEC 61937 stream into dst, all of
// it, as Fill does: 16-bit little-endian stereo, 4 bytes a frame.
type FillIEC func(dst []byte)

// IEC is an IEC 61937 stream's format: the codec its bursts carry and the
// encoded audio's rate and channels, which some systems are told.
type IEC struct {
	Codec      string // "ac3", "eac3" or "dts"
	SampleRate int
	Channels   int
}

// CarrierRate is the rate of the 16-bit stereo frames carrying the
// bursts: the encoded rate, four times it for E-AC-3.
func (f IEC) CarrierRate() int {
	if f.Codec == "eac3" {
		return f.SampleRate * 4
	}
	return f.SampleRate
}

// Output is an open device stream.
type Output interface {
	// Rate and Channels are the stream's format, which Fill writes; an
	// IEC 61937 stream's are its carrier's, 2 channels.
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

// OpenIEC opens the default output device for f's IEC 61937 stream. It
// fails when the device, or the system's setting for it, takes no such
// stream, or another program holds it; the caller then plays PCM.
func OpenIEC(f IEC, fill FillIEC) (Output, error) { return openIEC(f, fill) }

// null is an output without a device: a goroutine asks for frames at the
// rate a device would and drops them.
type null struct {
	rate, channels int
	frameBytes     int
	fill           func([]byte)
	mu             sync.Mutex
	played         int64
	paused         bool
	done           chan struct{}
	wg             sync.WaitGroup
}

// NewNull returns an output that plays nothing at rate and channels, for
// machines without a device and for tests.
func NewNull(rate, channels int, fill Fill) Output {
	return newNull(rate, channels, 4*channels, floatBytes(fill))
}

// NewNullIEC returns an output that drops f's IEC 61937 stream at its
// carrier's rate, for tests.
func NewNullIEC(f IEC, fill FillIEC) Output { return newNull(f.CarrierRate(), 2, 4, fill) }

func newNull(rate, channels, frameBytes int, fill func([]byte)) Output {
	n := &null{rate: rate, channels: channels, frameBytes: frameBytes, fill: fill, done: make(chan struct{})}
	n.wg.Add(1)
	go n.run()
	return n
}

func (n *null) run() {
	defer n.wg.Done()
	const period = 10 * time.Millisecond
	frames := n.rate * int(period) / int(time.Second)
	buf := make([]byte, frames*n.frameBytes)
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

// floatBytes has fill write float32 samples into a byte buffer.
func floatBytes(fill Fill) func([]byte) {
	return func(dst []byte) {
		if len(dst) >= 4 {
			fill(unsafe.Slice((*float32)(unsafe.Pointer(unsafe.SliceData(dst))), len(dst)/4))
		}
	}
}

// pointer turns an address a C callback passed as an integer back into a
// pointer.
func pointer(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }
