package ffmpeg

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Packer wraps a stream's encoded audio packets in IEC 61937 bursts, as
// S/PDIF and HDMI carry AC-3, E-AC-3, DTS, DTS-HD and TrueHD to a
// receiver that decodes them, through FFmpeg's spdif muxer. The bursts
// are 16-bit little-endian words, which a carrier plays as PCM frames: 2
// channels, or 8 for HDMI's high bit rate.
type Packer struct {
	ctx *FormatContext
	pb  unsafe.Pointer
	id  uintptr
	out []byte
}

var (
	packMu      sync.Mutex
	packers     = map[uintptr]*Packer{}
	packNext    uintptr
	packOnce    sync.Once
	packWriteCB uintptr
)

// HasMuxer reports the loaded FFmpeg having the muxer name, such as
// "spdif".
func HasMuxer(name string) bool {
	return Load() == nil && avGuessFormat(name, nil, nil) != nil
}

// NewPacker makes a packer for the stream whose parameters are par: AC-3,
// E-AC-3, DTS or TrueHD, whose access units it gathers into MAT frames.
// A DTS stream passes its core alone unless dtsHDRate is set: then whole
// frames, DTS-HD's extension substream with the core, pass as DTS type
// IV bursts at that IEC 61937 rate, 768000 for HDMI's high bit rate.
func NewPacker(par *CodecParameters, dtsHDRate int) (*Packer, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	packOnce.Do(func() {
		// int write_packet(void *opaque, const uint8_t *buf, int buf_size)
		packWriteCB = purego.NewCallback(func(opaque uintptr, buf unsafe.Pointer, size uintptr) uintptr {
			packMu.Lock()
			k := packers[opaque]
			packMu.Unlock()
			n := int(int32(size))
			if k != nil && n > 0 {
				k.out = append(k.out, unsafe.Slice((*byte)(buf), n)...)
			}
			return uintptr(n)
		})
	})
	k := &Packer{}
	if err := check(avformatAllocOutputCtx2(&k.ctx, nil, "spdif", nil)); err != nil {
		return nil, fmt.Errorf("ffmpeg: no spdif muxer: %w", err)
	}
	packMu.Lock()
	packNext++
	k.id = packNext
	packers[k.id] = k
	packMu.Unlock()
	st := avformatNewStream(k.ctx, nil)
	if st == nil {
		k.Close()
		return nil, errors.New("ffmpeg: out of memory")
	}
	if err := check(avcodecParametersCopy(st.Params(), par)); err != nil {
		k.Close()
		return nil, err
	}
	buf := avMalloc(ioBufferSize)
	k.pb = avioAllocContext(buf, ioBufferSize, 1, k.id, 0, packWriteCB, 0)
	if k.pb == nil {
		avFree(buf)
		k.Close()
		return nil, errors.New("ffmpeg: out of memory")
	}
	*(*unsafe.Pointer)(at(k.ctx.p(), offAVFormatContextPb)) = k.pb
	var opts ptr
	if dtsHDRate > 0 {
		avDictSet(&opts, "dtshd_rate", strconv.Itoa(dtsHDRate), 0)
	}
	err := check(avformatWriteHeader(k.ctx, &opts))
	avDictFree(&opts)
	if err != nil {
		k.Close()
		return nil, fmt.Errorf("ffmpeg: spdif cannot carry %s: %w", par.CodecName(), err)
	}
	return k, nil
}

// Pack wraps p, which becomes the packer's to change, and returns the
// bursts it completed, valid until the next call: none while E-AC-3
// gathers the frames of one burst, or TrueHD the 24 access units of a
// MAT frame. The packet's stream index and
// timestamps are changed for the muxer.
func (k *Packer) Pack(p *Packet) ([]byte, error) {
	k.out = k.out[:0]
	*(*int32)(at(p.ptr(), offAVPacketStreamIndex)) = 0
	*(*int64)(at(p.ptr(), offAVPacketPts)) = NoPTS
	*(*int64)(at(p.ptr(), offAVPacketDts)) = NoPTS
	err := check(avWriteFrame(k.ctx, p))
	avioFlush(k.pb)
	return k.out, err
}

// Close frees the packer.
func (k *Packer) Close() {
	if k.ctx != nil {
		// The muxer's own I/O is the packer's, which it frees itself.
		*(*unsafe.Pointer)(at(k.ctx.p(), offAVFormatContextPb)) = nil
		avformatFreeContext(k.ctx)
		k.ctx = nil
	}
	if k.pb != nil {
		if buf := pp(k.pb, offAVIOContextBuffer); buf != nil {
			avFree(buf)
			*(*unsafe.Pointer)(at(k.pb, offAVIOContextBuffer)) = nil
		}
		avioContextFree(&k.pb)
	}
	packMu.Lock()
	delete(packers, k.id)
	packMu.Unlock()
}
