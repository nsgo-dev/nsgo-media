package ffmpeg

import (
	"math"
	"runtime"
	"time"
	"unsafe"
)

// The pointer types below stand for FFmpeg's structs; their memory is
// FFmpeg's, and their methods read and write fields at the offsets
// layout.go generated.
type (
	FormatContext   struct{ _ [0]byte }
	Stream          struct{ _ [0]byte }
	CodecParameters struct{ _ [0]byte }
	Chapter         struct{ _ [0]byte }
	Packet          struct{ _ [0]byte }
	Frame           struct{ _ [0]byte }
	Codec           struct{ _ [0]byte }
	CodecContext    struct{ _ [0]byte }
)

// MediaType is AVMediaType.
type MediaType int32

const (
	MediaVideo      MediaType = 0
	MediaAudio      MediaType = 1
	MediaData       MediaType = 2
	MediaSubtitle   MediaType = 3
	MediaAttachment MediaType = 4
)

// NoPTS is AV_NOPTS_VALUE, a timestamp that is not known.
const NoPTS int64 = math.MinInt64

// TimeBase is AV_TIME_BASE, the microseconds a format's duration counts.
const TimeBase = 1000000

const (
	logQuiet = -8

	SeekBackward = 1
	SeekAny      = 4

	dispositionDefault     = 1 << 0
	dispositionForced      = 1 << 6
	dispositionHearingImp  = 1 << 7
	dispositionAttachedPic = 1 << 10

	DiscardDefault = 0
	DiscardAll     = 48

	pktFlagKey        = 1
	frameFlagKey      = 1 << 1
	pktDataDisplay    = 5 // AV_PKT_DATA_DISPLAYMATRIX
	hwConfigMethodD   = 1 // AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX
	fmtFlagCustomIO   = 0x80
	ctxFlagUnseekable = 2 // AVFMTCTX_UNSEEKABLE
	optSearchChild    = 1
	seekSize          = 0x10000 // AVSEEK_SIZE
	seekForce         = 0x20000 // AVSEEK_FORCE

	colorRangeMPEG      = 1
	colorRangeJPEG      = 2
	ColorSpaceBT709     = 1
	ColorSpaceBT2020NCL = 9
	ColorSpaceBT2020CL  = 10

	PictTypeNone int32 = 0
	PictTypeI    int32 = 1

	codecFlagLowDelay         = 1 << 19
	codecFlagGlobalHeader     = 1 << 22
	codecCapVariableFrameSize = 1 << 16
	threadTypeSlice           = 2

	ColorPrimariesBT709       = 1
	ColorPrimariesUnspecified = 2
	ColorPrimariesBT2020      = 9

	ColorTrcBT709       = 1
	ColorTrcUnspecified = 2
	ColorTrcSMPTE2084   = 16 // PQ (HDR10)
	ColorTrcARIBSTDB67  = 18 // HLG
)

// Rational is AVRational.
type Rational struct{ Num, Den int32 }

// Duration is ts in r's units.
func (r Rational) Duration(ts int64) time.Duration {
	if r.Den == 0 {
		return 0
	}
	return time.Duration(float64(ts) * float64(r.Num) / float64(r.Den) * float64(time.Second))
}

// Units is d in r's units, rounded down.
func (r Rational) Units(d time.Duration) int64 {
	if r.Num == 0 {
		return 0
	}
	return int64(math.Floor(d.Seconds() * float64(r.Den) / float64(r.Num)))
}

// Float is r as a number, 0 for a zero denominator.
func (r Rational) Float() float64 {
	if r.Den == 0 {
		return 0
	}
	return float64(r.Num) / float64(r.Den)
}

func at(p unsafe.Pointer, off uintptr) unsafe.Pointer { return unsafe.Add(p, off) }
func i32(p unsafe.Pointer, off uintptr) int32         { return *(*int32)(at(p, off)) }
func i64(p unsafe.Pointer, off uintptr) int64         { return *(*int64)(at(p, off)) }
func pp(p unsafe.Pointer, off uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(at(p, off)) }
func rat(p unsafe.Pointer, off uintptr) Rational      { return *(*Rational)(at(p, off)) }

// gostring copies a NUL-terminated C string.
func gostring(p unsafe.Pointer) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

// dict copies an AVDictionary into a map.
func dict(d unsafe.Pointer) map[string]string {
	if d == nil {
		return nil
	}
	m := map[string]string{}
	for e := avDictIterate(d, nil); e != nil; e = avDictIterate(d, e) {
		m[gostring(pp(e, offAVDictionaryEntryKey))] = gostring(pp(e, offAVDictionaryEntryValue))
	}
	return m
}

// FormatContext

func (c *FormatContext) p() unsafe.Pointer { return unsafe.Pointer(c) }

// Streams are the input's streams.
func (c *FormatContext) Streams() []*Stream {
	n := int(uint32(i32(c.p(), offAVFormatContextNbStreams)))
	arr := pp(c.p(), offAVFormatContextStreams)
	if n == 0 || arr == nil {
		return nil
	}
	return unsafe.Slice((**Stream)(arr), n)
}

// Chapters are the input's chapters.
func (c *FormatContext) Chapters() []*Chapter {
	n := int(uint32(i32(c.p(), offAVFormatContextNbChapters)))
	arr := pp(c.p(), offAVFormatContextChapters)
	if n == 0 || arr == nil {
		return nil
	}
	return unsafe.Slice((**Chapter)(arr), n)
}

// Duration is the input's duration, 0 when unknown.
func (c *FormatContext) Duration() time.Duration {
	d := i64(c.p(), offAVFormatContextDuration)
	if d == NoPTS || d < 0 {
		return 0
	}
	return time.Duration(d) * time.Microsecond
}

// StartTime is the first timestamp's time, 0 when unknown.
func (c *FormatContext) StartTime() time.Duration {
	d := i64(c.p(), offAVFormatContextStartTime)
	if d == NoPTS {
		return 0
	}
	return time.Duration(d) * time.Microsecond
}

// Seekable reports the demuxer able to seek, which a live stream's is not.
func (c *FormatContext) Seekable() bool {
	return i32(c.p(), offAVFormatContextCtxFlags)&ctxFlagUnseekable == 0
}

// BitRate is the input's total bit rate, 0 when unknown.
func (c *FormatContext) BitRate() int64 { return i64(c.p(), offAVFormatContextBitRate) }

// Metadata is the container's metadata.
func (c *FormatContext) Metadata() map[string]string {
	return dict(pp(c.p(), offAVFormatContextMetadata))
}

// FormatName is the demuxer's short name, such as "matroska,webm".
func (c *FormatContext) FormatName() string {
	f := pp(c.p(), offAVFormatContextIformat)
	if f == nil {
		return ""
	}
	return gostring(pp(f, offAVInputFormatName))
}

// ReadPacket reads the next packet into p.
func (c *FormatContext) ReadPacket(p *Packet) error { return check(avReadFrame(c, p)) }

// Seek seeks to ts in stream's time base, or microseconds for stream -1,
// within [minTS, maxTS].
func (c *FormatContext) Seek(stream int, minTS, ts, maxTS int64, flags int) error {
	return check(avformatSeekFile(c, int32(stream), minTS, ts, maxTS, int32(flags)))
}

// Stream

func (s *Stream) p() unsafe.Pointer { return unsafe.Pointer(s) }

func (s *Stream) Index() int                  { return int(i32(s.p(), offAVStreamIndex)) }
func (s *Stream) ID() int                     { return int(i32(s.p(), offAVStreamId)) }
func (s *Stream) TimeBase() Rational          { return rat(s.p(), offAVStreamTimeBase) }
func (s *Stream) StartTime() int64            { return i64(s.p(), offAVStreamStartTime) }
func (s *Stream) DurationTS() int64           { return i64(s.p(), offAVStreamDuration) }
func (s *Stream) FrameCount() int64           { return i64(s.p(), offAVStreamNbFrames) }
func (s *Stream) SampleAspectRatio() Rational { return rat(s.p(), offAVStreamSampleAspectRatio) }
func (s *Stream) AvgFrameRate() Rational      { return rat(s.p(), offAVStreamAvgFrameRate) }
func (s *Stream) RealFrameRate() Rational     { return rat(s.p(), offAVStreamRFrameRate) }
func (s *Stream) Metadata() map[string]string { return dict(pp(s.p(), offAVStreamMetadata)) }
func (s *Stream) Params() *CodecParameters {
	return (*CodecParameters)(pp(s.p(), offAVStreamCodecpar))
}

func (s *Stream) disposition() int32 { return i32(s.p(), offAVStreamDisposition) }

// Default reports the container marking the stream as its default.
func (s *Stream) Default() bool { return s.disposition()&dispositionDefault != 0 }

// Forced reports a subtitle stream marked forced.
func (s *Stream) Forced() bool { return s.disposition()&dispositionForced != 0 }

// AttachedPicture reports a video stream that is only cover art.
func (s *Stream) AttachedPicture() bool { return s.disposition()&dispositionAttachedPic != 0 }

// SetDiscard sets which of the stream's packets the demuxer drops.
func (s *Stream) SetDiscard(d int32) { *(*int32)(at(s.p(), offAVStreamDiscard)) = d }

// Duration is the stream's duration, 0 when unknown.
func (s *Stream) Duration() time.Duration {
	d := s.DurationTS()
	if d == NoPTS || d < 0 {
		return 0
	}
	return s.TimeBase().Duration(d)
}

// CodecParameters

func (c *CodecParameters) p() unsafe.Pointer { return unsafe.Pointer(c) }

func (c *CodecParameters) Type() MediaType {
	return MediaType(i32(c.p(), offAVCodecParametersCodecType))
}
func (c *CodecParameters) CodecID() int32  { return i32(c.p(), offAVCodecParametersCodecId) }
func (c *CodecParameters) Format() int32   { return i32(c.p(), offAVCodecParametersFormat) }
func (c *CodecParameters) BitRate() int64  { return i64(c.p(), offAVCodecParametersBitRate) }
func (c *CodecParameters) Profile() int32  { return i32(c.p(), offAVCodecParametersProfile) }
func (c *CodecParameters) Width() int      { return int(i32(c.p(), offAVCodecParametersWidth)) }
func (c *CodecParameters) Height() int     { return int(i32(c.p(), offAVCodecParametersHeight)) }
func (c *CodecParameters) SampleRate() int { return int(i32(c.p(), offAVCodecParametersSampleRate)) }
func (c *CodecParameters) Channels() int {
	return int(i32(c.p(), offAVCodecParametersChLayout+offAVChannelLayoutNbChannels))
}
func (c *CodecParameters) SampleAspectRatio() Rational {
	return rat(c.p(), offAVCodecParametersSampleAspectRatio)
}

// CodecName is the codec's short name, such as "h264".
func (c *CodecParameters) CodecName() string { return avcodecGetName(c.CodecID()) }

// Rotation is the clockwise rotation in degrees, a multiple of 90, the
// stream's display matrix asks the picture to be shown at.
func (c *CodecParameters) Rotation() int {
	sd := avPacketSideDataGet(pp(c.p(), offAVCodecParametersCodedSideData),
		i32(c.p(), offAVCodecParametersNbCodedSideData), pktDataDisplay)
	if sd == nil || i64(sd, offAVPacketSideDataSize) < 36 {
		return 0
	}
	m := unsafe.Slice((*int32)(pp(sd, offAVPacketSideDataData)), 9)
	return displayRotation(m)
}

// displayRotation reads a display matrix as av_display_rotation_get does.
// That returns the counterclockwise angle -atan2(...), so the clockwise
// one is atan2(...) itself, snapped here to a quarter turn.
func displayRotation(m []int32) int {
	sx := math.Hypot(float64(m[0]), float64(m[3]))
	sy := math.Hypot(float64(m[1]), float64(m[4]))
	if sx == 0 || sy == 0 {
		return 0
	}
	cw := math.Atan2(float64(m[1])/sy, float64(m[0])/sx) * 180 / math.Pi
	r := int(math.Round(cw/90)) * 90
	return ((r % 360) + 360) % 360
}

// Chapter

func (c *Chapter) p() unsafe.Pointer { return unsafe.Pointer(c) }

func (c *Chapter) ID() int64                   { return i64(c.p(), offAVChapterId) }
func (c *Chapter) TimeBase() Rational          { return rat(c.p(), offAVChapterTimeBase) }
func (c *Chapter) Start() int64                { return i64(c.p(), offAVChapterStart) }
func (c *Chapter) End() int64                  { return i64(c.p(), offAVChapterEnd) }
func (c *Chapter) Metadata() map[string]string { return dict(pp(c.p(), offAVChapterMetadata)) }

// Packet

// NewPacket allocates an empty packet.
func NewPacket() *Packet { return avPacketAlloc() }

func (p *Packet) ptr() unsafe.Pointer { return unsafe.Pointer(p) }

// Free frees the packet and its data.
func (p *Packet) Free() { avPacketFree(&p) }

// Unref drops the packet's data, keeping it for reuse.
func (p *Packet) Unref() { avPacketUnref(p) }

func (p *Packet) PTS() int64       { return i64(p.ptr(), offAVPacketPts) }
func (p *Packet) DTS() int64       { return i64(p.ptr(), offAVPacketDts) }
func (p *Packet) Size() int        { return int(i32(p.ptr(), offAVPacketSize)) }
func (p *Packet) StreamIndex() int { return int(i32(p.ptr(), offAVPacketStreamIndex)) }
func (p *Packet) DurationTS() int64 {
	return i64(p.ptr(), offAVPacketDuration)
}
func (p *Packet) Pos() int64 { return i64(p.ptr(), offAVPacketPos) }

// SetDuration sets the packet's duration in its time base.
func (p *Packet) SetDuration(d int64) { *(*int64)(at(p.ptr(), offAVPacketDuration)) = d }
func (p *Packet) Key() bool           { return i32(p.ptr(), offAVPacketFlags)&pktFlagKey != 0 }

// Data is the packet's bytes, valid until it is unreferenced.
func (p *Packet) Data() []byte {
	d := pp(p.ptr(), offAVPacketData)
	if d == nil {
		return nil
	}
	return unsafe.Slice((*byte)(d), p.Size())
}

// SetData replaces the packet's payload with a copy of b.
func (p *Packet) SetData(b []byte) error {
	p.Unref()
	if err := check(avNewPacket(p, int32(len(b)))); err != nil {
		return err
	}
	copy(p.Data(), b)
	return nil
}

// Frame

// NewFrame allocates an empty frame.
func NewFrame() *Frame { return avFrameAlloc() }

func (f *Frame) p() unsafe.Pointer { return unsafe.Pointer(f) }

// Free frees the frame and its buffers.
func (f *Frame) Free() { avFrameFree(&f) }

// Unref drops the frame's buffers, keeping it for reuse.
func (f *Frame) Unref() { avFrameUnref(f) }

// MoveTo moves the frame's buffers into dst, leaving f empty.
func (f *Frame) MoveTo(dst *Frame) { avFrameMoveRef(dst, f) }

// Clone is a new frame referencing f's buffers, nil when out of memory.
func (f *Frame) Clone() *Frame { return avFrameClone(f) }

func (f *Frame) Width() int      { return int(i32(f.p(), offAVFrameWidth)) }
func (f *Frame) Height() int     { return int(i32(f.p(), offAVFrameHeight)) }
func (f *Frame) Format() int32   { return i32(f.p(), offAVFrameFormat) }
func (f *Frame) PTS() int64      { return i64(f.p(), offAVFramePts) }
func (f *Frame) NbSamples() int  { return int(i32(f.p(), offAVFrameNbSamples)) }
func (f *Frame) SampleRate() int { return int(i32(f.p(), offAVFrameSampleRate)) }
func (f *Frame) Channels() int {
	return int(i32(f.p(), offAVFrameChLayout+offAVChannelLayoutNbChannels))
}
func (f *Frame) BestEffortTimestamp() int64 { return i64(f.p(), offAVFrameBestEffortTimestamp) }
func (f *Frame) DurationTS() int64          { return i64(f.p(), offAVFrameDuration) }
func (f *Frame) Key() bool                  { return i32(f.p(), offAVFrameFlags)&frameFlagKey != 0 }
func (f *Frame) FullRange() bool            { return i32(f.p(), offAVFrameColorRange) == colorRangeJPEG }
func (f *Frame) ColorSpace() int32          { return i32(f.p(), offAVFrameColorspace) }
func (f *Frame) ColorTrc() int32            { return i32(f.p(), offAVFrameColorTrc) }
func (f *Frame) ColorPrimaries() int32      { return i32(f.p(), offAVFrameColorPrimaries) }

// HDR reports a picture with a high dynamic range transfer characteristic
// (PQ / HDR10 or HLG).
func (f *Frame) HDR() bool {
	t := f.ColorTrc()
	return t == ColorTrcSMPTE2084 || t == ColorTrcARIBSTDB67
}

// Hardware reports a frame in a hardware surface, which Transfer copies
// into memory.
func (f *Frame) Hardware() bool { return pp(f.p(), offAVFrameHwFramesCtx) != nil }

// SetPTS sets the frame's timestamp.
func (f *Frame) SetPTS(ts int64) { *(*int64)(at(f.p(), offAVFramePts)) = ts }

// SetDuration sets the frame's duration in its time base.
func (f *Frame) SetDuration(d int64) { *(*int64)(at(f.p(), offAVFrameDuration)) = d }

// AllocAudio allocates buffers for n samples of sampleFmt at rate with
// channels in the default layout.
func (f *Frame) AllocAudio(n int, sampleFmt int32, rate, channels int) error {
	f.Unref()
	p := f.p()
	*(*int32)(at(p, offAVFrameFormat)) = sampleFmt
	*(*int32)(at(p, offAVFrameSampleRate)) = int32(rate)
	*(*int32)(at(p, offAVFrameNbSamples)) = int32(n)
	avChannelLayoutDefault(f.layout(), int32(channels))
	return check(avFrameGetBuffer(f, 0))
}

// SetPictType sets the frame's picture type (PictTypeNone or PictTypeI).
func (f *Frame) SetPictType(t int32) { *(*int32)(at(f.p(), offAVFramePictType)) = t }

// AllocBuffer allocates buffers for a software video frame of w×h pixels
// in pixFmt.
func (f *Frame) AllocBuffer(w, h int, pixFmt int32) error {
	f.Unref()
	p := f.p()
	*(*int32)(at(p, offAVFrameWidth)) = int32(w)
	*(*int32)(at(p, offAVFrameHeight)) = int32(h)
	*(*int32)(at(p, offAVFrameFormat)) = pixFmt
	return check(avFrameGetBuffer(f, 0))
}

// MakeWritable ensures the frame's buffers can be written in place.
func (f *Frame) MakeWritable() error { return check(avFrameMakeWritable(f)) }

// SetColor sets the frame's color space and range.
func (f *Frame) SetColor(colorspace int32, fullRange bool) {
	p := f.p()
	*(*int32)(at(p, offAVFrameColorspace)) = colorspace
	if fullRange {
		*(*int32)(at(p, offAVFrameColorRange)) = colorRangeJPEG
	} else {
		*(*int32)(at(p, offAVFrameColorRange)) = colorRangeMPEG
	}
}

// CopyColor gives f src's color space, range, transfer characteristic
// and primaries.
func (f *Frame) CopyColor(src *Frame) {
	for _, off := range []uintptr{offAVFrameColorRange, offAVFrameColorspace, offAVFrameColorTrc, offAVFrameColorPrimaries} {
		*(*int32)(at(f.p(), off)) = i32(src.p(), off)
	}
}

// SetPlane sets the i-th plane pointer and line size directly, for
// wrapping caller-owned buffers without copying. Clear it before freeing.
func (f *Frame) SetPlane(i int, data unsafe.Pointer, stride int) {
	*(*unsafe.Pointer)(at(f.p(), offAVFrameData+uintptr(i)*8)) = data
	*(*int32)(at(f.p(), offAVFrameLinesize+uintptr(i)*4)) = int32(stride)
}

// SetGeometry sets a wrapper frame's width, height and pixel format.
func (f *Frame) SetGeometry(w, h int, pixFmt int32) {
	p := f.p()
	*(*int32)(at(p, offAVFrameWidth)) = int32(w)
	*(*int32)(at(p, offAVFrameHeight)) = int32(h)
	*(*int32)(at(p, offAVFrameFormat)) = pixFmt
}

// Plane is the frame's i-th data pointer and its line size.
func (f *Frame) Plane(i int) (unsafe.Pointer, int) {
	return pp(f.p(), offAVFrameData+uintptr(i)*8), int(i32(f.p(), offAVFrameLinesize+uintptr(i)*4))
}

func (f *Frame) extendedData() *unsafe.Pointer {
	return (*unsafe.Pointer)(pp(f.p(), offAVFrameExtendedData))
}
func (f *Frame) layout() unsafe.Pointer { return at(f.p(), offAVFrameChLayout) }

// TransferTo copies a hardware frame's picture into dst, in memory.
func (f *Frame) TransferTo(dst *Frame) error {
	if err := check(avHwframeTransferData(dst, f, 0)); err != nil {
		return err
	}
	*(*int64)(at(dst.p(), offAVFramePts)) = f.PTS()
	*(*int64)(at(dst.p(), offAVFrameBestEffortTimestamp)) = f.BestEffortTimestamp()
	*(*int32)(at(dst.p(), offAVFrameColorRange)) = i32(f.p(), offAVFrameColorRange)
	*(*int32)(at(dst.p(), offAVFrameColorspace)) = f.ColorSpace()
	*(*int32)(at(dst.p(), offAVFrameColorTrc)) = f.ColorTrc()
	*(*int32)(at(dst.p(), offAVFrameColorPrimaries)) = f.ColorPrimaries()
	return nil
}

// PixelFormat is the number of a pixel format FFmpeg names, -1 if none.
func PixelFormat(name string) int32 { return avGetPixFmt(name) }

// PixelFormatName is a pixel format's name.
func PixelFormatName(f int32) string { return avGetPixFmtName(f) }

// SampleFormat is the number of a sample format FFmpeg names, -1 if none.
func SampleFormat(name string) int32 { return avGetSampleFmt(name) }

// keep stops the garbage collector from finishing with v before here.
func keep(v any) { runtime.KeepAlive(v) }
