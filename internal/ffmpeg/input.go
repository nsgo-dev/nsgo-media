package ffmpeg

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Input is an opened media source: its demuxer, and the Go reader and
// interrupt behind it.
type Input struct {
	Format *FormatContext
	pb     unsafe.Pointer // the custom AVIOContext, nil when FFmpeg reads a file itself
	id     uintptr
}

// Source is what Open reads through Go instead of a URL FFmpeg opens: an
// HTTP body, or any other seekable stream. Size, when it returns a
// positive length, lets FFmpeg seek from the end.
type Source interface {
	io.ReadSeeker
	Size() int64
}

// ioState is what the C callbacks reach through their opaque value.
type ioState struct {
	src       Source
	interrupt *atomic.Bool
	err       error // the last read error that was not EOF
}

var (
	ioMu     sync.Mutex
	ioStates = map[uintptr]*ioState{}
	ioNext   uintptr

	callbacksOnce sync.Once
	readCB        uintptr
	seekCB        uintptr
	interruptCB   uintptr
)

// The callbacks are made once: purego cannot free them, and their number
// is limited.
func makeCallbacks() {
	readCB = purego.NewCallback(func(opaque uintptr, buf unsafe.Pointer, size uintptr) uintptr {
		st := ioState_(opaque)
		if st == nil {
			return errCode(ErrExit)
		}
		if st.interrupt.Load() {
			return errCode(ErrExit)
		}
		n, err := st.src.Read(unsafe.Slice((*byte)(buf), int(int32(size))))
		if n > 0 {
			return uintptr(n)
		}
		if err == nil || errors.Is(err, io.EOF) {
			return errCode(ErrEOF)
		}
		st.err = err
		return errCode(Error(-5)) // AVERROR(EIO)
	})
	seekCB = purego.NewCallback(func(opaque uintptr, offset uintptr, whence uintptr) uintptr {
		st := ioState_(opaque)
		if st == nil {
			return errCode(ErrExit)
		}
		w := int(int32(whence)) &^ seekForce
		if w&seekSize != 0 {
			if n := st.src.Size(); n > 0 {
				return uintptr(n)
			}
			return errCode(Error(-38)) // AVERROR(ENOSYS)
		}
		pos, err := st.src.Seek(int64(offset), w)
		if err != nil {
			st.err = err
			return errCode(Error(-5))
		}
		return uintptr(pos)
	})
	interruptCB = purego.NewCallback(func(opaque uintptr) uintptr {
		st := ioState_(opaque)
		if st == nil || st.interrupt.Load() {
			return 1
		}
		return 0
	})
}

func errCode(e Error) uintptr { return uintptr(int64(e)) }

func ioState_(id uintptr) *ioState {
	ioMu.Lock()
	defer ioMu.Unlock()
	return ioStates[id]
}

// Open opens url, or src when it is not nil (url then only names it), and
// reads its streams' information. Setting interrupt makes any blocking
// call on the input return ErrExit.
func Open(url string, src Source, interrupt *atomic.Bool) (*Input, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	callbacksOnce.Do(makeCallbacks)
	ioMu.Lock()
	ioNext++
	in := &Input{id: ioNext}
	st := &ioState{src: src, interrupt: interrupt}
	ioStates[in.id] = st
	ioMu.Unlock()

	ctx := avformatAllocContext()
	if ctx == nil {
		in.release()
		return nil, errors.New("ffmpeg: out of memory")
	}
	cb := at(ctx.p(), offAVFormatContextInterruptCallback)
	*(*uintptr)(at(cb, offAVIOInterruptCBCallback)) = interruptCB
	*(*uintptr)(at(cb, offAVIOInterruptCBOpaque)) = in.id
	if src != nil {
		const size = 1 << 16
		buf := avMalloc(size)
		in.pb = avioAllocContext(buf, size, 0, in.id, readCB, 0, seekCB)
		if in.pb == nil {
			avFree(buf)
			avformatCloseInput(&ctx)
			in.release()
			return nil, errors.New("ffmpeg: out of memory")
		}
		*(*unsafe.Pointer)(at(ctx.p(), offAVFormatContextPb)) = in.pb
		*(*int32)(at(ctx.p(), offAVFormatContextFlags)) |= fmtFlagCustomIO
	}
	if err := check(avformatOpenInput(&ctx, url, nil, nil)); err != nil {
		// avformat_open_input frees the context when it fails.
		in.Close()
		return nil, in.wrap(err, st)
	}
	in.Format = ctx
	if err := check(avformatFindStreamInfo(ctx, nil)); err != nil {
		in.Close()
		return nil, in.wrap(err, st)
	}
	return in, nil
}

// wrap prefers the Go reader's own error to the code FFmpeg made of it.
func (in *Input) wrap(err error, st *ioState) error {
	if st.err != nil {
		return st.err
	}
	return err
}

// ReadErr is the last error the Go source returned, if any.
func (in *Input) ReadErr() error {
	if st := ioState_(in.id); st != nil {
		return st.err
	}
	return nil
}

// Close closes the demuxer and the custom I/O.
func (in *Input) Close() {
	if in.Format != nil {
		avformatCloseInput(&in.Format)
	}
	if in.pb != nil {
		if buf := pp(in.pb, offAVIOContextBuffer); buf != nil {
			avFree(buf)
			*(*unsafe.Pointer)(at(in.pb, offAVIOContextBuffer)) = nil
		}
		avioContextFree(&in.pb)
	}
	in.release()
}

func (in *Input) release() {
	ioMu.Lock()
	delete(ioStates, in.id)
	ioMu.Unlock()
}
