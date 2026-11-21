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
// up every few milliseconds and closes it.

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
)

const (
	clsctxAll             = 0x17
	eRender               = 0
	eConsole              = 0
	sharedMode            = 0
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

type wasapi struct {
	fill   Fill
	rate   int
	mu     sync.Mutex
	paused bool
	closed bool
	played int64
	done   chan struct{}
	ready  chan error
	wg     sync.WaitGroup
}

func open(fill Fill) (Output, error) {
	w := &wasapi{fill: fill, done: make(chan struct{}), ready: make(chan error, 1)}
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
	buf := make([]float32, 0, bufFrames*wasapiChannels)
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
				buf = buf[:avail*wasapiChannels]
				w.fill(buf)
				copy(unsafe.Slice((*float32)(pointer(data)), len(buf)), buf)
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

// start activates the default render device's audio client in shared mode
// with float32 stereo at the mix rate.
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
	if hr := call(device, 3, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&client))); failed(hr) {
		return 0, 0, 0, fmt.Errorf("audioout: IAudioClient: 0x%x", uint32(hr))
	}
	var mix uintptr
	if hr := call(client, 8, uintptr(unsafe.Pointer(&mix))); failed(hr) {
		return client, 0, 0, fmt.Errorf("audioout: GetMixFormat: 0x%x", uint32(hr))
	}
	w.rate = int(*(*uint32)(pointer(mix + 4)))
	coTaskMemFree.Call(mix)

	// WAVEFORMATEXTENSIBLE, packed.
	var f [40]byte
	le := binary.LittleEndian
	le.PutUint16(f[0:], 0xFFFE)
	le.PutUint16(f[2:], wasapiChannels)
	le.PutUint32(f[4:], uint32(w.rate))
	le.PutUint32(f[8:], uint32(w.rate*wasapiChannels*4))
	le.PutUint16(f[12:], wasapiChannels*4)
	le.PutUint16(f[14:], 32)
	le.PutUint16(f[16:], 22)
	le.PutUint16(f[18:], 32)
	le.PutUint32(f[20:], speakerFrontLeftRight)
	le.PutUint32(f[24:], subtypeIEEEFloat.d1)
	le.PutUint16(f[28:], subtypeIEEEFloat.d2)
	le.PutUint16(f[30:], subtypeIEEEFloat.d3)
	copy(f[32:], subtypeIEEEFloat.d4[:])
	if hr := call(client, 3, sharedMode, autoConvertPCM|srcDefaultQuality, uintptr(bufferDuration/100), 0, uintptr(unsafe.Pointer(&f[0])), 0); failed(hr) {
		return client, 0, 0, fmt.Errorf("audioout: IAudioClient.Initialize: 0x%x", uint32(hr))
	}
	if hr := call(client, 4, uintptr(unsafe.Pointer(&bufFrames))); failed(hr) {
		return client, 0, 0, fmt.Errorf("audioout: GetBufferSize: 0x%x", uint32(hr))
	}
	if hr := call(client, 14, uintptr(unsafe.Pointer(&iidIAudioRenderClient)), uintptr(unsafe.Pointer(&render))); failed(hr) {
		return client, 0, 0, fmt.Errorf("audioout: IAudioRenderClient: 0x%x", uint32(hr))
	}
	return client, render, bufFrames, nil
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
