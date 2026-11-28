package ffmpeg

import (
	"errors"
	"fmt"
	"math/bits"
	"unsafe"
)

const (
	avioFlagWrite   = 2    // AVIO_FLAG_WRITE
	fmtGlobalHeader = 0x40 // AVFMT_GLOBALHEADER
)

// Muxer writes encoded streams into a file through one of FFmpeg's
// muxers, such as "mp4", "mov" or "webm".
type Muxer struct {
	ctx     *FormatContext
	pb      unsafe.Pointer
	encTB   []Rational // each stream's encoder time base
	started bool
}

// NewMuxer makes a muxer of format writing path, which Start opens.
func NewMuxer(path, format string) (*Muxer, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	m := &Muxer{}
	// The muxer keeps path as its URL, which MP4's faststart opens again
	// to move the index.
	if err := check(avformatAllocOutputCtx2(&m.ctx, nil, format, path)); err != nil || m.ctx == nil {
		return nil, fmt.Errorf("ffmpeg: no %s muxer", format)
	}
	if err := check(avioOpen(&m.pb, path, avioFlagWrite)); err != nil {
		avformatFreeContext(m.ctx)
		return nil, fmt.Errorf("ffmpeg: open %s: %w", path, err)
	}
	*(*unsafe.Pointer)(at(m.ctx.p(), offAVFormatContextPb)) = m.pb
	return m, nil
}

// GlobalHeader reports the muxer wanting codecs' setup in their
// extradata, as EncoderConfig.GlobalHeader puts it.
func (m *Muxer) GlobalHeader() bool {
	return i32(pp(m.ctx.p(), offAVFormatContextOformat), offAVOutputFormatFlags)&fmtGlobalHeader != 0
}

// AddStream adds a stream of e's packets, with tag as its codec tag when
// not zero (such as "hvc1"), and returns its index.
func (m *Muxer) AddStream(e *Encoder, tag string) (int, error) {
	st := avformatNewStream(m.ctx, nil)
	if st == nil {
		return 0, errors.New("ffmpeg: out of memory")
	}
	if err := check(avcodecParamsFromCtx(st.Params(), e.ctx)); err != nil {
		return 0, err
	}
	if len(tag) == 4 {
		*(*uint32)(at(st.Params().p(), offAVCodecParametersCodecTag)) = uint32(tag[0]) | uint32(tag[1])<<8 | uint32(tag[2])<<16 | uint32(tag[3])<<24
	}
	*(*Rational)(at(st.p(), offAVStreamTimeBase)) = e.TimeBase()
	m.encTB = append(m.encTB, e.TimeBase())
	return st.Index(), nil
}

// Start writes the file's header with the muxer's options, such as
// "movflags".
func (m *Muxer) Start(options map[string]string) error {
	var opts ptr
	for k, v := range options {
		avDictSet(&opts, k, v, 0)
	}
	err := check(avformatWriteHeader(m.ctx, &opts))
	avDictFree(&opts)
	if err != nil {
		return fmt.Errorf("ffmpeg: write header: %w", err)
	}
	m.started = true
	return nil
}

// Write muxes p, a packet of stream's encoder, interleaving it with the
// other streams' by time. It takes p's data, leaving p empty.
func (m *Muxer) Write(stream int, p *Packet) error {
	src, dst := m.encTB[stream], m.ctx.Streams()[stream].TimeBase()
	q := p.ptr()
	for _, off := range []uintptr{offAVPacketPts, offAVPacketDts} {
		*(*int64)(at(q, off)) = rescale(i64(q, off), src, dst)
	}
	if d := i64(q, offAVPacketDuration); d > 0 {
		*(*int64)(at(q, offAVPacketDuration)) = rescale(d, src, dst)
	}
	*(*int32)(at(q, offAVPacketStreamIndex)) = int32(stream)
	return check(avInterleavedWriteFrame(m.ctx, p))
}

// Finish writes what the muxer holds and the file's trailer.
func (m *Muxer) Finish() error {
	if err := check(avInterleavedWriteFrame(m.ctx, nil)); err != nil {
		return err
	}
	return check(avWriteTrailer(m.ctx))
}

// Close closes the file and frees the muxer, whether it finished or not.
func (m *Muxer) Close() error {
	var err error
	if m.pb != nil {
		*(*unsafe.Pointer)(at(m.ctx.p(), offAVFormatContextPb)) = nil
		err = check(avioClosep(&m.pb))
	}
	if m.ctx != nil {
		avformatFreeContext(m.ctx)
		m.ctx = nil
	}
	return err
}

// rescale is ts from src's units in dst's, rounded to the nearest, as
// av_rescale_q does; NoPTS stays.
func rescale(ts int64, src, dst Rational) int64 {
	if ts == NoPTS || src == dst {
		return ts
	}
	num := uint64(int64(src.Num) * int64(dst.Den))
	den := uint64(int64(src.Den) * int64(dst.Num))
	neg := ts < 0
	a := uint64(ts)
	if neg {
		a = uint64(-ts)
	}
	hi, lo := bits.Mul64(a, num)
	lo, carry := bits.Add64(lo, den/2, 0)
	hi += carry
	q, _ := bits.Div64(hi, lo, den)
	if neg {
		return -int64(q)
	}
	return int64(q)
}
