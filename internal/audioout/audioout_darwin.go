package audioout

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The macOS backend is an AudioQueue of float32 stereo at 48 kHz, which
// converts to the device's own rate. Its callback refills each buffer the
// queue has played, on the queue's own thread, and its timeline counts the
// samples the device played.

const (
	aqRate       = 48000
	aqChannels   = 2
	aqBuffers    = 3
	aqBufFrames  = 1024
	formatLPCM   = 0x6c70636d // 'lpcm'
	flagFloat    = 1
	flagPacked   = 8
	offAQBufData = 8  // AudioQueueBuffer.mAudioData
	offAQBufSize = 16 // AudioQueueBuffer.mAudioDataByteSize
)

var (
	aqOnce     sync.Once
	aqErr      error
	aqCallback uintptr

	audioQueueNewOutput      func(format unsafe.Pointer, cb uintptr, user uintptr, runLoop, mode uintptr, flags uint32, out *uintptr) int32
	audioQueueAllocateBuffer func(q uintptr, size uint32, out *uintptr) int32
	audioQueueEnqueueBuffer  func(q, buf uintptr, n uint32, descs uintptr) int32
	audioQueueStart          func(q uintptr, at uintptr) int32
	audioQueuePause          func(q uintptr) int32
	audioQueueStop           func(q uintptr, immediate bool) int32
	audioQueueDispose        func(q uintptr, immediate bool) int32
	audioQueueGetCurrentTime func(q uintptr, timeline uintptr, ts unsafe.Pointer, discontinuity *bool) int32

	aqMu     sync.Mutex
	aqQueues = map[uintptr]*audioQueue{}
	aqNextID uintptr
)

func loadAudioToolbox() error {
	aqOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/AudioToolbox.framework/AudioToolbox", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			aqErr = err
			return
		}
		for name, fn := range map[string]any{
			"AudioQueueNewOutput":      &audioQueueNewOutput,
			"AudioQueueAllocateBuffer": &audioQueueAllocateBuffer,
			"AudioQueueEnqueueBuffer":  &audioQueueEnqueueBuffer,
			"AudioQueueStart":          &audioQueueStart,
			"AudioQueuePause":          &audioQueuePause,
			"AudioQueueStop":           &audioQueueStop,
			"AudioQueueDispose":        &audioQueueDispose,
			"AudioQueueGetCurrentTime": &audioQueueGetCurrentTime,
		} {
			purego.RegisterLibFunc(fn, lib, name)
		}
		aqCallback = purego.NewCallback(func(user, q, buf uintptr) uintptr {
			aqMu.Lock()
			a := aqQueues[user]
			aqMu.Unlock()
			if a != nil {
				a.refill(buf)
			}
			return 0
		})
	})
	return aqErr
}

type audioQueue struct {
	id     uintptr
	q      uintptr
	fill   Fill
	mu     sync.Mutex
	closed bool
	paused bool
	played int64
}

func open(fill Fill) (Output, error) {
	if err := loadAudioToolbox(); err != nil {
		return nil, err
	}
	// AudioStreamBasicDescription
	var asbd struct {
		sampleRate                               float64
		formatID, formatFlags, bytesPerPacket    uint32
		framesPerPacket, bytesPerFrame, channels uint32
		bitsPerChannel, reserved                 uint32
	}
	asbd.sampleRate = aqRate
	asbd.formatID = formatLPCM
	asbd.formatFlags = flagFloat | flagPacked
	asbd.bytesPerFrame = 4 * aqChannels
	asbd.bytesPerPacket = asbd.bytesPerFrame
	asbd.framesPerPacket = 1
	asbd.channels = aqChannels
	asbd.bitsPerChannel = 32

	a := &audioQueue{fill: fill}
	aqMu.Lock()
	aqNextID++
	a.id = aqNextID
	aqQueues[a.id] = a
	aqMu.Unlock()
	if st := audioQueueNewOutput(unsafe.Pointer(&asbd), aqCallback, a.id, 0, 0, 0, &a.q); st != 0 {
		a.forget()
		return nil, fmt.Errorf("audioout: AudioQueueNewOutput: OSStatus %d", st)
	}
	for range aqBuffers {
		var buf uintptr
		if st := audioQueueAllocateBuffer(a.q, aqBufFrames*4*aqChannels, &buf); st != 0 {
			a.Close()
			return nil, fmt.Errorf("audioout: AudioQueueAllocateBuffer: OSStatus %d", st)
		}
		a.refill(buf)
	}
	if st := audioQueueStart(a.q, 0); st != 0 {
		a.Close()
		return nil, fmt.Errorf("audioout: AudioQueueStart: OSStatus %d", st)
	}
	return a, nil
}

// refill fills a played buffer and gives it back to the queue.
func (a *audioQueue) refill(buf uintptr) {
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return
	}
	b := pointer(buf)
	data := unsafe.Slice((*float32)(*(*unsafe.Pointer)(unsafe.Add(b, offAQBufData))), aqBufFrames*aqChannels)
	a.fill(data)
	*(*uint32)(unsafe.Add(b, offAQBufSize)) = uint32(len(data) * 4)
	audioQueueEnqueueBuffer(a.q, buf, 0, 0)
}

func (a *audioQueue) Rate() int     { return aqRate }
func (a *audioQueue) Channels() int { return aqChannels }

func (a *audioQueue) Played() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return a.played
	}
	var ts [64]byte // AudioTimeStamp, mSampleTime first
	if audioQueueGetCurrentTime(a.q, 0, unsafe.Pointer(&ts[0]), nil) == 0 {
		if s := int64(*(*float64)(unsafe.Pointer(&ts[0]))); s > a.played {
			a.played = s
		}
	}
	return a.played
}

func (a *audioQueue) SetPaused(p bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || p == a.paused {
		return
	}
	a.paused = p
	if p {
		audioQueuePause(a.q)
	} else {
		audioQueueStart(a.q, 0)
	}
}

func (a *audioQueue) Close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	q := a.q
	a.mu.Unlock()
	if q != 0 {
		audioQueueStop(q, true)
		audioQueueDispose(q, true)
	}
	a.forget()
}

func (a *audioQueue) forget() {
	aqMu.Lock()
	delete(aqQueues, a.id)
	aqMu.Unlock()
}
