package audioout

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// An IEC 61937 stream on Linux is a PulseAudio stream of a passthrough
// encoding, which PipeWire's server for PulseAudio answers too. The
// simple API takes only PCM, so it uses the asynchronous one on a
// threaded main loop, polling the states under its lock instead of
// registering callbacks. The sink takes the stream only where its
// passthrough formats are enabled (pactl set-sink-formats, or PipeWire's
// IEC958 codecs); otherwise the stream fails as not supported. DTS-HD and
// TrueHD, from PulseAudio 13, are HDMI's high bit rate: 8 channels at
// 192 kHz.

const (
	paContextReady      = 4
	paContextFailed     = 5
	paContextTerminated = 6
	paStreamReady       = 2
	paStreamFailed      = 3
	paStreamTerminated  = 4
	// PA_STREAM_INTERPOLATE_TIMING | PA_STREAM_AUTO_TIMING_UPDATE |
	// PA_STREAM_ADJUST_LATENCY
	paStreamFlags = 0x0002 | 0x0008 | 0x2000
)

// paEncodings are pa_encoding_t's passthrough values, by codec.
var paEncodings = map[string]int32{"ac3": 2, "eac3": 3, "dts": 5, "truehd": 7, "dtshd": 8}

var (
	paaOnce sync.Once
	paaErr  error

	paMainloopNew    func() uintptr
	paMainloopAPI    func(m uintptr) uintptr
	paMainloopStart  func(m uintptr) int32
	paMainloopStop   func(m uintptr)
	paMainloopFree   func(m uintptr)
	paMainloopLock   func(m uintptr)
	paMainloopUnlock func(m uintptr)

	paContextNew        func(api uintptr, name string) uintptr
	paContextConnect    func(c uintptr, server uintptr, flags int32, api uintptr) int32
	paContextGetState   func(c uintptr) int32
	paContextErrno      func(c uintptr) int32
	paContextDisconnect func(c uintptr)
	paContextUnref      func(c uintptr)
	paStrerror          func(code int32) string

	paFormatInfoNew      func() uintptr
	paFormatInfoFree     func(f uintptr)
	paFormatInfoSetRate  func(f uintptr, rate int32)
	paFormatInfoSetChans func(f uintptr, channels int32)

	paStreamNewExtended     func(c uintptr, name string, formats *uintptr, n uint32, props uintptr) uintptr
	paStreamConnectPlayback func(s uintptr, dev uintptr, attr unsafe.Pointer, flags int32, volume, sync uintptr) int32
	paStreamGetState        func(s uintptr) int32
	paStreamWritableSize    func(s uintptr) uintptr
	paStreamWrite           func(s uintptr, data unsafe.Pointer, n uintptr, free uintptr, offset int64, seek int32) int32
	paStreamGetLatency      func(s uintptr, usec *uint64, negative *int32) int32
	paStreamCork            func(s uintptr, b int32, cb, user uintptr) uintptr
	paStreamDisconnect      func(s uintptr) int32
	paStreamUnref           func(s uintptr)
	paOperationUnref        func(o uintptr)
)

func loadPulseAsync() error {
	paaOnce.Do(func() {
		lib, err := purego.Dlopen("libpulse.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			paaErr = err
			return
		}
		for name, fn := range map[string]any{
			"pa_threaded_mainloop_new":     &paMainloopNew,
			"pa_threaded_mainloop_get_api": &paMainloopAPI,
			"pa_threaded_mainloop_start":   &paMainloopStart,
			"pa_threaded_mainloop_stop":    &paMainloopStop,
			"pa_threaded_mainloop_free":    &paMainloopFree,
			"pa_threaded_mainloop_lock":    &paMainloopLock,
			"pa_threaded_mainloop_unlock":  &paMainloopUnlock,
			"pa_context_new":               &paContextNew,
			"pa_context_connect":           &paContextConnect,
			"pa_context_get_state":         &paContextGetState,
			"pa_context_errno":             &paContextErrno,
			"pa_context_disconnect":        &paContextDisconnect,
			"pa_context_unref":             &paContextUnref,
			"pa_strerror":                  &paStrerror,
			"pa_format_info_new":           &paFormatInfoNew,
			"pa_format_info_free":          &paFormatInfoFree,
			"pa_format_info_set_rate":      &paFormatInfoSetRate,
			"pa_format_info_set_channels":  &paFormatInfoSetChans,
			"pa_stream_new_extended":       &paStreamNewExtended,
			"pa_stream_connect_playback":   &paStreamConnectPlayback,
			"pa_stream_get_state":          &paStreamGetState,
			"pa_stream_writable_size":      &paStreamWritableSize,
			"pa_stream_write":              &paStreamWrite,
			"pa_stream_get_latency":        &paStreamGetLatency,
			"pa_stream_cork":               &paStreamCork,
			"pa_stream_disconnect":         &paStreamDisconnect,
			"pa_stream_unref":              &paStreamUnref,
			"pa_operation_unref":           &paOperationUnref,
		} {
			purego.RegisterLibFunc(fn, lib, name)
		}
	})
	return paaErr
}

// pulseIEC is a passthrough stream on a threaded main loop.
type pulseIEC struct {
	ml, ctx, s uintptr
	fill       FillIEC
	rate       int
	frameBytes int
	mu         sync.Mutex
	paused     bool
	closed     bool
	written    int64 // frames written to the server
	latency    int64 // frames the server held at the last reading
	done       chan struct{}
	wg         sync.WaitGroup
}

func openIEC(f IEC, fill FillIEC) (Output, error) {
	enc, ok := paEncodings[f.Codec]
	if !ok {
		return nil, fmt.Errorf("audioout: no PulseAudio passthrough for %s", f.Codec)
	}
	if err := loadPulseAsync(); err != nil {
		return nil, err
	}
	p := &pulseIEC{fill: fill, rate: f.CarrierRate(), frameBytes: 2 * f.CarrierChannels(), done: make(chan struct{})}
	if p.ml = paMainloopNew(); p.ml == 0 {
		return nil, errors.New("audioout: pa_threaded_mainloop_new failed")
	}
	if paMainloopStart(p.ml) < 0 {
		paMainloopFree(p.ml)
		return nil, errors.New("audioout: pa_threaded_mainloop_start failed")
	}
	if err := p.connect(enc, f); err != nil {
		p.free()
		return nil, err
	}
	p.wg.Add(1)
	go p.run()
	return p, nil
}

// connect connects the context and the stream, waiting for each to be
// ready or fail.
func (p *pulseIEC) connect(enc int32, f IEC) error {
	paMainloopLock(p.ml)
	p.ctx = paContextNew(paMainloopAPI(p.ml), "NSGo Media")
	ok := p.ctx != 0 && paContextConnect(p.ctx, 0, 0, 0) >= 0
	paMainloopUnlock(p.ml)
	if !ok {
		return errors.New("audioout: pa_context_connect failed")
	}
	if err := p.wait(func() (bool, error) {
		switch st := paContextGetState(p.ctx); st {
		case paContextReady:
			return true, nil
		case paContextFailed, paContextTerminated:
			return false, fmt.Errorf("audioout: PulseAudio: %s", paStrerror(paContextErrno(p.ctx)))
		}
		return false, nil
	}); err != nil {
		return err
	}

	paMainloopLock(p.ml)
	fi := paFormatInfoNew()
	*(*int32)(pointer(fi)) = enc // pa_format_info.encoding
	// PulseAudio takes the encoded rate and makes E-AC-3's carrier of it;
	// the high bit rate's is its carrier's.
	rate := f.SampleRate
	if f.HBR() {
		rate = f.CarrierRate()
	}
	paFormatInfoSetRate(fi, int32(rate))
	paFormatInfoSetChans(fi, int32(f.CarrierChannels()))
	p.s = paStreamNewExtended(p.ctx, "Playback", &fi, 1, 0)
	paFormatInfoFree(fi)
	var code int32
	if p.s != 0 {
		// pa_buffer_attr: a 100 ms target, the rest the server's choice.
		none := ^uint32(0)
		attr := struct{ maxlength, tlength, prebuf, minreq, fragsize uint32 }{none, uint32(p.rate * p.frameBytes / 10), none, none, none}
		code = paStreamConnectPlayback(p.s, 0, unsafe.Pointer(&attr), paStreamFlags, 0, 0)
	}
	paMainloopUnlock(p.ml)
	if p.s == 0 || code < 0 {
		return fmt.Errorf("audioout: PulseAudio stream: %s", paStrerror(paContextErrno(p.ctx)))
	}
	return p.wait(func() (bool, error) {
		switch paStreamGetState(p.s) {
		case paStreamReady:
			return true, nil
		case paStreamFailed, paStreamTerminated:
			return false, fmt.Errorf("audioout: PulseAudio passthrough of %s: %s", f.Codec, paStrerror(paContextErrno(p.ctx)))
		}
		return false, nil
	})
}

// wait polls check under the main loop's lock until it is done, for up
// to 2 s.
func (p *pulseIEC) wait(check func() (bool, error)) error {
	for range 200 {
		paMainloopLock(p.ml)
		done, err := check()
		paMainloopUnlock(p.ml)
		if done || err != nil {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("audioout: PulseAudio did not answer")
}

func (p *pulseIEC) run() {
	defer p.wg.Done()
	buf := make([]byte, p.rate*p.frameBytes/10)
	t := time.NewTicker(5 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-t.C:
		}
		p.mu.Lock()
		paused := p.paused
		p.mu.Unlock()
		paMainloopLock(p.ml)
		if paStreamGetState(p.s) != paStreamReady {
			paMainloopUnlock(p.ml)
			continue
		}
		n := 0
		if !paused {
			n = min(int(paStreamWritableSize(p.s)), len(buf))
			n -= n % p.frameBytes
		}
		if n > 0 {
			p.fill(buf[:n])
			if paStreamWrite(p.s, unsafe.Pointer(&buf[0]), uintptr(n), 0, 0, 0) < 0 {
				n = 0
			}
		}
		var usec uint64
		var neg int32
		ok := paStreamGetLatency(p.s, &usec, &neg) == 0
		paMainloopUnlock(p.ml)
		p.mu.Lock()
		p.written += int64(n / p.frameBytes)
		if ok {
			p.latency = int64(usec) * int64(p.rate) / 1000000
			if neg != 0 {
				p.latency = 0
			}
		}
		p.mu.Unlock()
	}
}

func (p *pulseIEC) Rate() int     { return p.rate }
func (p *pulseIEC) Channels() int { return p.frameBytes / 2 }

func (p *pulseIEC) Played() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return max(p.written-p.latency, 0)
}

func (p *pulseIEC) SetPaused(paused bool) {
	p.mu.Lock()
	changed := paused != p.paused && !p.closed
	p.paused = paused
	p.mu.Unlock()
	if changed {
		paMainloopLock(p.ml)
		if op := paStreamCork(p.s, boolInt(paused), 0, 0); op != 0 {
			paOperationUnref(op)
		}
		paMainloopUnlock(p.ml)
	}
}

func boolInt(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func (p *pulseIEC) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	close(p.done)
	p.wg.Wait()
	p.free()
}

// free disconnects the stream and the context and frees the main loop.
func (p *pulseIEC) free() {
	paMainloopLock(p.ml)
	if p.s != 0 {
		paStreamDisconnect(p.s)
		paStreamUnref(p.s)
		p.s = 0
	}
	if p.ctx != 0 {
		paContextDisconnect(p.ctx)
		paContextUnref(p.ctx)
		p.ctx = 0
	}
	paMainloopUnlock(p.ml)
	paMainloopStop(p.ml)
	paMainloopFree(p.ml)
}
