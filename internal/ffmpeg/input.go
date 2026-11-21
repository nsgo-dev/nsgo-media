package ffmpeg

import (
	"errors"
	"fmt"
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

// Opener opens a resource a demuxer asks for by URL, such as an HLS
// playlist's segments or keys, as a Source read through Go. Options are
// what the demuxer passes with the request, such as "offset" and
// "end_offset" for a byte range. A Source that is an io.Closer is closed
// when the demuxer closes it.
type Opener func(url string, options map[string]string) (Source, error)

// InputOptions are how OpenInput opens its input.
type InputOptions struct {
	// Format names the demuxer, such as "hls", instead of probing.
	Format string
	// Options are the demuxer's options, such as hls' "allowed_extensions".
	Options map[string]string
	// Open, when not nil, opens every resource the demuxer opens itself.
	Open Opener
}

// ioState is what the C callbacks reach through their opaque value: an
// input's own, or one of the resources its Opener opened.
type ioState struct {
	src       Source
	interrupt *atomic.Bool
	err       error // the last read error that was not EOF
	open      Opener
	children  map[unsafe.Pointer]uintptr // the AVIOContexts open opened, to their states
}

var (
	ioMu     sync.Mutex
	ioStates = map[uintptr]*ioState{}
	ioNext   uintptr

	callbacksOnce sync.Once
	readCB        uintptr
	seekCB        uintptr
	interruptCB   uintptr
	ioOpenCB      uintptr
	ioCloseCB     uintptr
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
	// int io_open(AVFormatContext *s, AVIOContext **pb, const char *url,
	// int flags, AVDictionary **options)
	ioOpenCB = purego.NewCallback(func(s, pbOut, url unsafe.Pointer, flags int32, options unsafe.Pointer) uintptr {
		id := *(*uintptr)(at(s, offAVFormatContextOpaque))
		st := ioState_(id)
		if st == nil || st.open == nil || st.interrupt.Load() {
			return errCode(ErrExit)
		}
		var opts map[string]string
		if options != nil {
			opts = dict(*(*unsafe.Pointer)(options))
		}
		src, err := st.open(gostring(url), opts)
		if err != nil {
			if st.interrupt.Load() {
				return errCode(ErrExit)
			}
			return errCode(Error(-5)) // AVERROR(EIO)
		}
		pb, cid := newIOContext(src, st.interrupt)
		if pb == nil {
			closeSource(src)
			return errCode(Error(-12)) // AVERROR(ENOMEM)
		}
		ioMu.Lock()
		st.children[pb] = cid
		ioMu.Unlock()
		*(*unsafe.Pointer)(pbOut) = pb
		return 0
	})
	// int io_close2(AVFormatContext *s, AVIOContext *pb)
	ioCloseCB = purego.NewCallback(func(s, pb unsafe.Pointer) uintptr {
		id := *(*uintptr)(at(s, offAVFormatContextOpaque))
		ioMu.Lock()
		st := ioStates[id]
		var cid uintptr
		if st != nil {
			cid = st.children[pb]
			delete(st.children, pb)
		}
		ioMu.Unlock()
		if cid != 0 {
			freeIOContext(pb, cid)
		}
		return 0
	})
}

const ioBufferSize = 1 << 16

// newIOContext makes an AVIOContext reading src, and its state.
func newIOContext(src Source, interrupt *atomic.Bool) (unsafe.Pointer, uintptr) {
	ioMu.Lock()
	ioNext++
	id := ioNext
	ioStates[id] = &ioState{src: src, interrupt: interrupt}
	ioMu.Unlock()
	buf := avMalloc(ioBufferSize)
	pb := avioAllocContext(buf, ioBufferSize, 0, id, readCB, 0, seekCB)
	if pb == nil {
		avFree(buf)
		ioMu.Lock()
		delete(ioStates, id)
		ioMu.Unlock()
		return nil, 0
	}
	// A demuxer seeks to a byte range's start after opening it, which
	// would otherwise read the range's first bytes away.
	if s, ok := src.(interface{ Start() int64 }); ok {
		*(*int64)(at(pb, offAVIOContextPos)) = s.Start()
	}
	return pb, id
}

// freeIOContext frees an AVIOContext newIOContext made and closes its
// source.
func freeIOContext(pb unsafe.Pointer, id uintptr) {
	if buf := pp(pb, offAVIOContextBuffer); buf != nil {
		avFree(buf)
		*(*unsafe.Pointer)(at(pb, offAVIOContextBuffer)) = nil
	}
	avioContextFree(&pb)
	ioMu.Lock()
	st := ioStates[id]
	delete(ioStates, id)
	ioMu.Unlock()
	if st != nil {
		closeSource(st.src)
	}
}

func closeSource(src Source) {
	if c, ok := src.(io.Closer); ok {
		c.Close()
	}
}

func errCode(e Error) uintptr { return uintptr(int64(e)) }

func ioState_(id uintptr) *ioState {
	ioMu.Lock()
	defer ioMu.Unlock()
	return ioStates[id]
}

// HasDemuxer reports the loaded FFmpeg having the demuxer name, such as
// "dash", which libraries built without libxml2 lack.
func HasDemuxer(name string) bool {
	return Load() == nil && avFindInputFormat(name) != nil
}

// Open opens url, or src when it is not nil (url then only names it), and
// reads its streams' information. Setting interrupt makes any blocking
// call on the input return ErrExit.
func Open(url string, src Source, interrupt *atomic.Bool) (*Input, error) {
	return OpenInput(url, src, interrupt, InputOptions{})
}

// OpenInput is Open with the demuxer, its options and an Opener for the
// resources it opens itself.
func OpenInput(url string, src Source, interrupt *atomic.Bool, o InputOptions) (*Input, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	callbacksOnce.Do(makeCallbacks)
	ioMu.Lock()
	ioNext++
	in := &Input{id: ioNext}
	st := &ioState{src: src, interrupt: interrupt, open: o.Open, children: map[unsafe.Pointer]uintptr{}}
	ioStates[in.id] = st
	ioMu.Unlock()

	var format ptr
	if o.Format != "" {
		if format = avFindInputFormat(o.Format); format == nil {
			in.release()
			return nil, fmt.Errorf("ffmpeg: no %s demuxer", o.Format)
		}
	}
	ctx := avformatAllocContext()
	if ctx == nil {
		in.release()
		return nil, errors.New("ffmpeg: out of memory")
	}
	cb := at(ctx.p(), offAVFormatContextInterruptCallback)
	*(*uintptr)(at(cb, offAVIOInterruptCBCallback)) = interruptCB
	*(*uintptr)(at(cb, offAVIOInterruptCBOpaque)) = in.id
	if o.Open != nil {
		*(*uintptr)(at(ctx.p(), offAVFormatContextOpaque)) = in.id
		*(*uintptr)(at(ctx.p(), offAVFormatContextIoOpen)) = ioOpenCB
		*(*uintptr)(at(ctx.p(), offAVFormatContextIoClose2)) = ioCloseCB
	}
	if src != nil {
		buf := avMalloc(ioBufferSize)
		in.pb = avioAllocContext(buf, ioBufferSize, 0, in.id, readCB, 0, seekCB)
		if in.pb == nil {
			avFree(buf)
			avformatCloseInput(&ctx)
			in.release()
			return nil, errors.New("ffmpeg: out of memory")
		}
		*(*unsafe.Pointer)(at(ctx.p(), offAVFormatContextPb)) = in.pb
		*(*int32)(at(ctx.p(), offAVFormatContextFlags)) |= fmtFlagCustomIO
	}
	var opts ptr
	for k, v := range o.Options {
		avDictSet(&opts, k, v, 0)
	}
	err := check(avformatOpenInput(&ctx, url, format, &opts))
	avDictFree(&opts)
	if err != nil {
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

// release forgets the input's state, closing what its Opener opened that
// the demuxer left open.
func (in *Input) release() {
	ioMu.Lock()
	st := ioStates[in.id]
	delete(ioStates, in.id)
	var left map[unsafe.Pointer]uintptr
	if st != nil {
		left, st.children = st.children, nil
	}
	ioMu.Unlock()
	for pb, id := range left {
		freeIOContext(pb, id)
	}
}
