//go:build darwin && !ios

package audioout

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// An IEC 61937 stream on macOS hogs the default output device, turns its
// mixing off and sets one of its streams to an encoded physical format,
// which only digital outputs such as S/PDIF list. An IOProc then writes
// the carrier's frames straight into the device's buffer. Closing gives
// the device back as it was. Outputs listing only linear PCM, as
// built-in speakers and most HDMI ports do, are refused: their PCM would
// sound the bursts as noise.

const (
	scopeGlobal   = 0x676c6f62 // 'glob'
	scopeOutput   = 0x6f757470 // 'outp'
	systemObject  = 1          // kAudioObjectSystemObject
	propDefault   = 0x644f7574 // 'dOut' kAudioHardwarePropertyDefaultOutputDevice
	propStreams   = 0x73746d23 // 'stm#'
	propHog       = 0x6f696e6b // 'oink'
	propMixing    = 0x6d69783f // 'mix?'
	propLatency   = 0x6c746e63 // 'ltnc'
	propSafety    = 0x73616674 // 'saft'
	propPhysical  = 0x70667420 // 'pft '
	propPhysicals = 0x70667461 // 'pfta'
	sizeASBD      = 40
	sizeRangedFmt = 56 // AudioStreamRangedDescription
)

// encodedFormats are the format IDs of a stream that carries IEC 61937:
// kAudioFormat60958AC3, its variants and kAudioFormatAC3.
var encodedFormats = map[uint32]bool{0x63616333: true, 0x49414333: true, 0x69616333: true, 0x61632d33: true}

var (
	halOnce sync.Once
	halErr  error
	halProc uintptr

	audioObjectGetPropertyData     func(id uint32, addr unsafe.Pointer, qsize uint32, q unsafe.Pointer, size *uint32, data unsafe.Pointer) int32
	audioObjectGetPropertyDataSize func(id uint32, addr unsafe.Pointer, qsize uint32, q unsafe.Pointer, size *uint32) int32
	audioObjectSetPropertyData     func(id uint32, addr unsafe.Pointer, qsize uint32, q unsafe.Pointer, size uint32, data unsafe.Pointer) int32
	audioDeviceCreateIOProcID      func(dev uint32, proc uintptr, client uintptr, id *uintptr) int32
	audioDeviceDestroyIOProcID     func(dev uint32, id uintptr) int32
	audioDeviceStart               func(dev uint32, id uintptr) int32
	audioDeviceStop                func(dev uint32, id uintptr) int32

	halMu      sync.Mutex
	halStreams = map[uintptr]*hogged{}
	halNextID  uintptr
)

func loadHAL() error {
	halOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreAudio.framework/CoreAudio", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			halErr = err
			return
		}
		for name, fn := range map[string]any{
			"AudioObjectGetPropertyData":     &audioObjectGetPropertyData,
			"AudioObjectGetPropertyDataSize": &audioObjectGetPropertyDataSize,
			"AudioObjectSetPropertyData":     &audioObjectSetPropertyData,
			"AudioDeviceCreateIOProcID":      &audioDeviceCreateIOProcID,
			"AudioDeviceDestroyIOProcID":     &audioDeviceDestroyIOProcID,
			"AudioDeviceStart":               &audioDeviceStart,
			"AudioDeviceStop":                &audioDeviceStop,
		} {
			purego.RegisterLibFunc(fn, lib, name)
		}
		// OSStatus (*AudioDeviceIOProc)(AudioObjectID, const AudioTimeStamp *now,
		// const AudioBufferList *in, const AudioTimeStamp *inTime,
		// AudioBufferList *out, const AudioTimeStamp *outTime, void *client)
		halProc = purego.NewCallback(func(dev uintptr, now, in, inTime, out, outTime, client uintptr) uintptr {
			halMu.Lock()
			h := halStreams[client]
			halMu.Unlock()
			if h != nil {
				h.render(now, out, outTime)
			}
			return 0
		})
	})
	return halErr
}

type propAddr struct{ selector, scope, element uint32 }

func getProp(id uint32, sel, scope uint32, data unsafe.Pointer, size uint32) error {
	a := propAddr{sel, scope, 0}
	if st := audioObjectGetPropertyData(id, unsafe.Pointer(&a), 0, nil, &size, data); st != 0 {
		return fmt.Errorf("OSStatus %d", st)
	}
	return nil
}

func setProp(id uint32, sel, scope uint32, data unsafe.Pointer, size uint32) error {
	a := propAddr{sel, scope, 0}
	if st := audioObjectSetPropertyData(id, unsafe.Pointer(&a), 0, nil, size, data); st != 0 {
		return fmt.Errorf("OSStatus %d", st)
	}
	return nil
}

// getArray reads a property that is an array of size-byte elements.
func getArray(id uint32, sel, scope uint32, size int) ([]byte, error) {
	a := propAddr{sel, scope, 0}
	var n uint32
	if st := audioObjectGetPropertyDataSize(id, unsafe.Pointer(&a), 0, nil, &n); st != 0 {
		return nil, fmt.Errorf("OSStatus %d", st)
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if st := audioObjectGetPropertyData(id, unsafe.Pointer(&a), 0, nil, &n, unsafe.Pointer(&buf[0])); st != 0 {
		return nil, fmt.Errorf("OSStatus %d", st)
	}
	return buf[:int(n)/size*size], nil
}

// hogged is an IEC 61937 stream on a hogged device.
type hogged struct {
	id       uintptr
	dev      uint32
	stream   uint32
	index    int // the stream's buffer in the device's buffer list
	fill     FillIEC
	rate     int
	procID   uintptr
	original [sizeASBD]byte // the stream's physical format before
	mixing   uint32
	hadMix   bool
	latency  int64

	mu      sync.Mutex
	paused  bool
	closed  bool
	written int64     // frames written
	base    int64     // frames played at the last render
	at      time.Time // when that was
}

func openIEC(f IEC, fill FillIEC) (Output, error) {
	if err := loadHAL(); err != nil {
		return nil, err
	}
	h := &hogged{fill: fill, rate: f.CarrierRate()}
	if err := getProp(systemObject, propDefault, scopeGlobal, unsafe.Pointer(&h.dev), 4); err != nil || h.dev == 0 {
		return nil, errors.New("audioout: no default output device")
	}
	streams, err := getArray(h.dev, propStreams, scopeOutput, 4)
	if err != nil {
		return nil, fmt.Errorf("audioout: the device's streams: %v", err)
	}
	// A stream with an encoded physical format at the carrier's rate.
	var format []byte
	for i := 0; i < len(streams)/4 && format == nil; i++ {
		s := *(*uint32)(unsafe.Pointer(&streams[i*4]))
		fmts, err := getArray(s, propPhysicals, scopeGlobal, sizeRangedFmt)
		if err != nil {
			continue
		}
		for j := 0; j < len(fmts)/sizeRangedFmt; j++ {
			d := fmts[j*sizeRangedFmt:]
			id := *(*uint32)(unsafe.Pointer(&d[8]))
			lo, hi := *(*float64)(unsafe.Pointer(&d[40])), *(*float64)(unsafe.Pointer(&d[48]))
			rate := *(*float64)(unsafe.Pointer(&d[0]))
			if encodedFormats[id] && (rate == float64(h.rate) || lo <= float64(h.rate) && float64(h.rate) <= hi) {
				format = append([]byte(nil), d[:sizeASBD]...)
				*(*float64)(unsafe.Pointer(&format[0])) = float64(h.rate)
				h.stream, h.index = s, i
				break
			}
		}
	}
	if format == nil {
		return nil, fmt.Errorf("audioout: the device has no encoded format for %s at %d Hz", f.Codec, h.rate)
	}

	pid := int32(syscall.Getpid())
	var owner int32
	if getProp(h.dev, propHog, scopeGlobal, unsafe.Pointer(&owner), 4) == nil && owner != -1 && owner != pid {
		return nil, fmt.Errorf("audioout: process %d holds the device", owner)
	}
	if err := setProp(h.dev, propHog, scopeGlobal, unsafe.Pointer(&pid), 4); err != nil {
		return nil, fmt.Errorf("audioout: hogging the device: %v", err)
	}
	if getProp(h.dev, propMixing, scopeGlobal, unsafe.Pointer(&h.mixing), 4) == nil {
		off := uint32(0)
		h.hadMix = setProp(h.dev, propMixing, scopeGlobal, unsafe.Pointer(&off), 4) == nil
	}
	if err := getProp(h.stream, propPhysical, scopeGlobal, unsafe.Pointer(&h.original[0]), sizeASBD); err != nil {
		h.restore()
		return nil, fmt.Errorf("audioout: the stream's format: %v", err)
	}
	if err := h.setFormat(format); err != nil {
		h.restore()
		return nil, err
	}
	var lat, safety uint32
	getProp(h.dev, propLatency, scopeOutput, unsafe.Pointer(&lat), 4)
	getProp(h.dev, propSafety, scopeOutput, unsafe.Pointer(&safety), 4)
	h.latency = int64(lat + safety)

	halMu.Lock()
	halNextID++
	h.id = halNextID
	halStreams[h.id] = h
	halMu.Unlock()
	if st := audioDeviceCreateIOProcID(h.dev, halProc, h.id, &h.procID); st != 0 {
		h.forget()
		h.restore()
		return nil, fmt.Errorf("audioout: AudioDeviceCreateIOProcID: OSStatus %d", st)
	}
	if st := audioDeviceStart(h.dev, h.procID); st != 0 {
		h.Close()
		return nil, fmt.Errorf("audioout: AudioDeviceStart: OSStatus %d", st)
	}
	return h, nil
}

// setFormat sets the stream's physical format, which the device changes
// to on its own thread, and waits for it.
func (h *hogged) setFormat(format []byte) error {
	if err := setProp(h.stream, propPhysical, scopeGlobal, unsafe.Pointer(&format[0]), sizeASBD); err != nil {
		return fmt.Errorf("audioout: setting the encoded format: %v", err)
	}
	var now [sizeASBD]byte
	for range 100 {
		if getProp(h.stream, propPhysical, scopeGlobal, unsafe.Pointer(&now[0]), sizeASBD) == nil &&
			string(now[:12]) == string(format[:12]) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("audioout: the device did not take the encoded format")
}

// restore gives the device back: its format, its mixing and the hog.
func (h *hogged) restore() {
	if h.original[8] != 0 || h.original[9] != 0 {
		setProp(h.stream, propPhysical, scopeGlobal, unsafe.Pointer(&h.original[0]), sizeASBD)
	}
	if h.hadMix {
		setProp(h.dev, propMixing, scopeGlobal, unsafe.Pointer(&h.mixing), 4)
	}
	none := int32(-1)
	setProp(h.dev, propHog, scopeGlobal, unsafe.Pointer(&none), 4)
}

// render fills the stream's buffer of out, an AudioBufferList.
func (h *hogged) render(now, out, outTime uintptr) {
	list := pointer(out)
	if int(*(*uint32)(list)) <= h.index {
		return
	}
	buf := unsafe.Add(list, 8+16*h.index) // AudioBuffer: channels, size, data
	size := int(*(*uint32)(unsafe.Add(buf, 4)))
	data := *(*unsafe.Pointer)(unsafe.Add(buf, 8))
	if data == nil || size <= 0 {
		return
	}
	dst := unsafe.Slice((*byte)(data), size)
	h.mu.Lock()
	paused, closed := h.paused, h.closed
	h.mu.Unlock()
	if paused || closed {
		clear(dst)
		return
	}
	h.fill(dst)
	// Frames written before this buffer play from its output time on.
	ahead := int64(0)
	if now != 0 && outTime != 0 {
		ahead = int64(*(*float64)(pointer(outTime)) - *(*float64)(pointer(now)))
	}
	h.mu.Lock()
	h.base = max(h.written-ahead-h.latency, h.base)
	h.at = time.Now()
	h.written += int64(size / 4)
	h.mu.Unlock()
}

func (h *hogged) Rate() int     { return h.rate }
func (h *hogged) Channels() int { return 2 }

func (h *hogged) Played() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.at.IsZero() || h.paused || h.closed {
		return h.base
	}
	n := h.base + int64(time.Since(h.at).Seconds()*float64(h.rate))
	return min(n, h.written)
}

func (h *hogged) SetPaused(p bool) {
	h.mu.Lock()
	if p && !h.paused && !h.at.IsZero() {
		h.base = min(h.base+int64(time.Since(h.at).Seconds()*float64(h.rate)), h.written)
	}
	h.paused = p
	h.at = time.Time{}
	h.mu.Unlock()
}

func (h *hogged) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.mu.Unlock()
	audioDeviceStop(h.dev, h.procID)
	audioDeviceDestroyIOProcID(h.dev, h.procID)
	h.forget()
	h.restore()
}

func (h *hogged) forget() {
	halMu.Lock()
	delete(halStreams, h.id)
	halMu.Unlock()
}
