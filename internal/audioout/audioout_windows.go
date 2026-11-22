package audioout

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The Windows backend is a shared-mode WASAPI stream of float32 stereo at
// the device's mix rate, which WASAPI converts to the device's own format.
// One goroutine, locked to its thread for COM, opens it, tops its buffer
// up every few milliseconds and closes it. An IEC 61937 stream opens the
// device in exclusive mode with the format of its codec, which the
// device's driver takes only where it drives a receiver that decodes it.

var (
	ole32            = syscall.NewLazyDLL("ole32.dll")
	coInitializeEx   = ole32.NewProc("CoInitializeEx")
	coUninitialize   = ole32.NewProc("CoUninitialize")
	coCreateInstance = ole32.NewProc("CoCreateInstance")
	coTaskMemFree    = ole32.NewProc("CoTaskMemFree")
)

type guid struct {
	d1     uint32
	d2, d3 uint16
	d4     [8]byte
}

var (
	clsidMMDeviceEnumerator = guid{0xBCDE0395, 0xE52F, 0x467C, [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator  = guid{0xA95664D2, 0x9614, 0x4F35, [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioClient         = guid{0x1CB9AD4C, 0xDBFA, 0x4C32, [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidIAudioRenderClient   = guid{0xF294ACFC, 0x3146, 0x4483, [8]byte{0xA7, 0xBF, 0xAD, 0xDC, 0xA7, 0xC2, 0x60, 0xE2}}
	subtypeIEEEFloat        = guid{0x00000003, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}}
	// KSDATAFORMAT_SUBTYPE_IEC61937_*, by codec.
	subtypeIEC = map[string]guid{
		"ac3":  {0x00000092, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}},
		"eac3": {0x0000000A, 0x0CEA, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}},
		"dts":  {0x00000008, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}},
	}
)

const (
	clsctxAll             = 0x17
	eRender               = 0
	eConsole              = 0
	sharedMode            = 0
	exclusiveMode         = 1
	autoConvertPCM        = 0x80000000
	srcDefaultQuality     = 0x08000000
	bufferDuration        = 100 * time.Millisecond
	wasapiChannels        = 2
	speakerFrontLeftRight = 0x3
)

// call calls the method at index of a COM object's vtable.
func call(obj uintptr, index int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(pointer(obj))
	fn := *(*uintptr)(pointer(vtbl + uintptr(index)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func release(obj uintptr) {
	if obj != 0 {
		call(obj, 2)
	}
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

// AUDCLNT_E_* results an exclusive stream meets, by what they mean.
var audclntErrors = map[uint32]string{
	0x88890008: "the device takes no such format",
	0x8889000A: "the device is in use",
	0x8889000E: "exclusive mode is not allowed on the device",
	0x88890019: "the buffer size is not aligned",
}

func hresult(what string, hr uintptr) error {
	if msg, ok := audclntErrors[uint32(hr)]; ok {
		return fmt.Errorf("audioout: %s: %s (0x%x)", what, msg, uint32(hr))
	}
	return fmt.Errorf("audioout: %s: 0x%x", what, uint32(hr))
}

type wasapi struct {
	fill       func([]byte)
	frameBytes int
	iec        *IEC // an exclusive IEC 61937 stream, nil for PCM
	rate       int
	mu         sync.Mutex
	paused     bool
	closed     bool
	played     int64
	done       chan struct{}
	ready      chan error
	wg         sync.WaitGroup
}

func open(fill Fill) (Output, error) {
	return start(&wasapi{fill: floatBytes(fill), frameBytes: 4 * wasapiChannels})
}

func openIEC(f IEC, fill FillIEC) (Output, error) {
	if _, ok := subtypeIEC[f.Codec]; !ok {
		return nil, fmt.Errorf("audioout: no IEC 61937 format for %s", f.Codec)
	}
	return start(&wasapi{fill: fill, frameBytes: 4, iec: &f, rate: f.CarrierRate()})
}

func start(w *wasapi) (Output, error) {
	w.done, w.ready = make(chan struct{}), make(chan error, 1)
	w.wg.Add(1)
	go w.run()
	if err := <-w.ready; err != nil {
		w.wg.Wait()
		return nil, err
	}
	return w, nil
}

func (w *wasapi) run() {
	defer w.wg.Done()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	coInitializeEx.Call(0, 0) // COINIT_MULTITHREADED
	defer coUninitialize.Call()

	client, render, bufFrames, err := w.start()
	if err != nil {
		release(render)
		release(client)
		w.ready <- err
		return
	}
	defer release(client)
	defer release(render)
	w.ready <- nil

	var written int64
	started := false
	buf := make([]byte, 0, int(bufFrames)*w.frameBytes)
	for {
		select {
		case <-w.done:
			call(client, 11) // Stop
			return
		default:
		}
		var padding uint32
		if failed(call(client, 6, uintptr(unsafe.Pointer(&padding)))) {
			return
		}
		w.mu.Lock()
		paused := w.paused
		w.played = max(written-int64(padding), w.played)
		w.mu.Unlock()
		if paused {
			if started {
				call(client, 11) // Stop keeps what is queued
				started = false
			}
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if avail := int(bufFrames) - int(padding); avail > 0 {
			var data uintptr
			if !failed(call(render, 3, uintptr(avail), uintptr(unsafe.Pointer(&data)))) {
				buf = buf[:avail*w.frameBytes]
				w.fill(buf)
				copy(unsafe.Slice((*byte)(pointer(data)), len(buf)), buf)
				call(render, 4, uintptr(avail), 0)
				written += int64(avail)
			}
		}
		if !started {
			call(client, 10) // Start
			started = true
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// start activates the default render device's audio client: in shared
// mode with float32 stereo at the mix rate, or exclusively with the IEC
// 61937 format.
func (w *wasapi) start() (client, render uintptr, bufFrames uint32, err error) {
	var enum uintptr
	if hr, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enum))); failed(hr) {
		return 0, 0, 0, fmt.Errorf("audioout: no device enumerator: 0x%x", uint32(hr))
	}
	defer release(enum)
	var device uintptr
	if hr := call(enum, 4, eRender, eConsole, uintptr(unsafe.Pointer(&device))); failed(hr) {
		return 0, 0, 0, fmt.Errorf("audioout: no default output device: 0x%x", uint32(hr))
	}
	defer release(device)
	activate := func() (uintptr, error) {
		var c uintptr
		if hr := call(device, 3, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&c))); failed(hr) {
			return 0, fmt.Errorf("audioout: IAudioClient: 0x%x", uint32(hr))
		}
		return c, nil
	}
	if client, err = activate(); err != nil {
		return 0, 0, 0, err
	}
	if w.iec != nil {
		client, err = w.initExclusive(client, activate)
		if err != nil {
			return client, 0, 0, err
		}
	} else {
		var mix uintptr
		if hr := call(client, 8, uintptr(unsafe.Pointer(&mix))); failed(hr) {
			return client, 0, 0, fmt.Errorf("audioout: GetMixFormat: 0x%x", uint32(hr))
		}
		w.rate = int(*(*uint32)(pointer(mix + 4)))
		coTaskMemFree.Call(mix)
		f := waveFormat(wasapiChannels, w.rate, 32, speakerFrontLeftRight, subtypeIEEEFloat)
		if hr := call(client, 3, sharedMode, autoConvertPCM|srcDefaultQuality, uintptr(bufferDuration/100), 0, uintptr(unsafe.Pointer(&f[0])), 0); failed(hr) {
			return client, 0, 0, fmt.Errorf("audioout: IAudioClient.Initialize: 0x%x", uint32(hr))
		}
	}
	if hr := call(client, 4, uintptr(unsafe.Pointer(&bufFrames))); failed(hr) {
		return client, 0, 0, fmt.Errorf("audioout: GetBufferSize: 0x%x", uint32(hr))
	}
	if hr := call(client, 14, uintptr(unsafe.Pointer(&iidIAudioRenderClient)), uintptr(unsafe.Pointer(&render))); failed(hr) {
		return client, 0, 0, fmt.Errorf("audioout: IAudioRenderClient: 0x%x", uint32(hr))
	}
	return client, render, bufFrames, nil
}

// initExclusive initializes client exclusively with the IEC 61937 format,
// once more with an aligned buffer when the device asks for one, on a
// client activate makes again.
func (w *wasapi) initExclusive(client uintptr, activate func() (uintptr, error)) (uintptr, error) {
	f := iecFormat(*w.iec)
	fp := uintptr(unsafe.Pointer(&f[0]))
	if hr := call(client, 7, exclusiveMode, fp, 0); hr != 0 { // IsFormatSupported
		return client, hresult("IAudioClient.IsFormatSupported", hr)
	}
	duration := uintptr(bufferDuration / 100)
	hr := call(client, 3, exclusiveMode, 0, duration, 0, fp, 0)
	if uint32(hr) == 0x88890019 { // AUDCLNT_E_BUFFER_SIZE_NOT_ALIGNED
		var frames uint32
		call(client, 4, uintptr(unsafe.Pointer(&frames)))
		release(client)
		var err error
		if client, err = activate(); err != nil {
			return 0, err
		}
		duration = uintptr((10_000_000*int64(frames) + int64(w.rate)/2) / int64(w.rate))
		hr = call(client, 3, exclusiveMode, 0, duration, 0, fp, 0)
	}
	if failed(hr) {
		return client, hresult("IAudioClient.Initialize", hr)
	}
	return client, nil
}

// waveFormat is a WAVEFORMATEXTENSIBLE, packed, with room after it for
// WAVEFORMATEXTENSIBLE_IEC61937's fields.
func waveFormat(channels, rate, bits int, mask uint32, sub guid) [52]byte {
	var f [52]byte
	le := binary.LittleEndian
	le.PutUint16(f[0:], 0xFFFE)
	le.PutUint16(f[2:], uint16(channels))
	le.PutUint32(f[4:], uint32(rate))
	le.PutUint32(f[8:], uint32(rate*channels*bits/8))
	le.PutUint16(f[12:], uint16(channels*bits/8))
	le.PutUint16(f[14:], uint16(bits))
	le.PutUint16(f[16:], 22)
	le.PutUint16(f[18:], uint16(bits))
	le.PutUint32(f[20:], mask)
	le.PutUint32(f[24:], sub.d1)
	le.PutUint16(f[28:], sub.d2)
	le.PutUint16(f[30:], sub.d3)
	copy(f[32:], sub.d4[:])
	return f
}

// iecFormat is the WAVEFORMATEXTENSIBLE_IEC61937 of f: 16-bit stereo at
// the carrier's rate, with the encoded audio's rate and channels.
func iecFormat(f IEC) [52]byte {
	w := waveFormat(2, f.CarrierRate(), 16, speakerFrontLeftRight, subtypeIEC[f.Codec])
	le := binary.LittleEndian
	le.PutUint16(w[16:], 34)
	le.PutUint32(w[40:], uint32(f.SampleRate))
	le.PutUint32(w[44:], uint32(f.Channels))
	return w
}

func (w *wasapi) Rate() int     { return w.rate }
func (w *wasapi) Channels() int { return wasapiChannels }

func (w *wasapi) Played() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.played
}

func (w *wasapi) SetPaused(p bool) {
	w.mu.Lock()
	w.paused = p
	w.mu.Unlock()
}

func (w *wasapi) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	w.mu.Unlock()
	close(w.done)
	w.wg.Wait()
}
