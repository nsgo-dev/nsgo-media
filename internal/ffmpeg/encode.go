package ffmpeg

import (
	"errors"
	"fmt"
	"slices"
	"unsafe"
)

// HasEncoder reports whether FFmpeg has an encoder with name compiled in.
func HasEncoder(name string) bool { return avcodecFindEncoderByNm(name) != nil }

// HasDecoder reports whether FFmpeg has a decoder with name compiled in.
func HasDecoder(name string) bool { return avcodecFindDecoderByNm(name) != nil }

// DecoderSupportsHW reports whether decoderName supports hwDevice and that
// hardware device can be opened on this machine.
func DecoderSupportsHW(decoderName, hwDevice string) bool {
	codec := avcodecFindDecoderByNm(decoderName)
	if codec == nil {
		return false
	}
	typ := avHwdeviceFindType(hwDevice)
	if typ <= 0 || !codecSupportsDevice(codec, typ) {
		return false
	}
	var hw unsafe.Pointer
	if check(avHwdeviceCtxCreate(&hw, typ, nil, nil, 0)) != nil {
		return false
	}
	avBufferUnref(&hw)
	return true
}

// EncoderConfig configures a video encoder: a low-latency one, or one
// tuned for a file when File is set.
type EncoderConfig struct {
	Name       string // FFmpeg encoder name, e.g. "h264_videotoolbox"
	HWDevice   string // optional hardware device for frame upload, e.g. "vaapi"
	HWFormat   string // hardware pixel format when HWDevice is set, e.g. "vaapi"
	PixFmt     string // software pixel format fed in, e.g. "nv12" or "yuv420p"
	Width      int
	Height     int
	FPS        int
	BitRate    int64
	GOPSize    int
	ColorSpace int32
	FullRange  bool
	Options    map[string]string
	// Rate is the frame rate, such as 30000/1001; when zero, FPS frames a
	// second.
	Rate Rational
	// File tunes the encoder for a file instead of latency: up to BFrames
	// B-frames, no low-delay flag and no cap on the rate, and the BT.709
	// primaries and transfer tagged with a BT.709 ColorSpace.
	File    bool
	BFrames int
	// GlobalHeader puts the parameter sets in the codec's extradata, as
	// a muxer whose GlobalHeader is set wants.
	GlobalHeader bool
	// Quality, when positive, is the encoder's global_quality.
	Quality int
	// Threads is the encoder's thread count, 0 for FFmpeg's choice.
	Threads int
}

// Encoder is an opened FFmpeg video encoder.
type Encoder struct {
	ctx      *CodecContext
	hwDev    unsafe.Pointer // AVBufferRef, non-nil when HWDevice is used
	hwFrames unsafe.Pointer // AVBufferRef, non-nil when HWDevice is used
	hwFrame  *Frame         // reusable hardware surface for upload
	name     string
}

// NewEncoder opens a video encoder: with no B-frames for low latency,
// unless cfg.File tunes it for a file.
func NewEncoder(cfg EncoderConfig) (*Encoder, error) {
	codec := avcodecFindEncoderByNm(cfg.Name)
	if codec == nil {
		return nil, fmt.Errorf("ffmpeg: no encoder %q", cfg.Name)
	}
	swFmt := avGetPixFmt(cfg.PixFmt)
	if swFmt < 0 {
		return nil, fmt.Errorf("ffmpeg: unknown pixel format %q", cfg.PixFmt)
	}
	e := &Encoder{name: cfg.Name}
	e.ctx = avcodecAllocContext3(codec)
	if e.ctx == nil {
		return nil, errors.New("ffmpeg: out of memory")
	}
	fps := cfg.FPS
	if fps <= 0 {
		fps = 60
	}
	rate := cfg.Rate
	if rate.Num <= 0 || rate.Den <= 0 {
		rate = Rational{Num: int32(fps), Den: 1}
	}
	c := unsafe.Pointer(e.ctx)
	*(*int32)(at(c, offAVCodecContextWidth)) = int32(cfg.Width)
	*(*int32)(at(c, offAVCodecContextHeight)) = int32(cfg.Height)
	*(*Rational)(at(c, offAVCodecContextTimeBase)) = Rational{Num: rate.Den, Den: rate.Num}
	*(*Rational)(at(c, offAVCodecContextFramerate)) = rate
	if cfg.File {
		*(*int32)(at(c, offAVCodecContextMaxBFrames)) = int32(cfg.BFrames)
	} else {
		*(*int32)(at(c, offAVCodecContextMaxBFrames)) = 0
		*(*int32)(at(c, offAVCodecContextFlags)) |= codecFlagLowDelay
	}
	if cfg.GlobalHeader {
		*(*int32)(at(c, offAVCodecContextFlags)) |= codecFlagGlobalHeader
	}
	if cfg.GOPSize > 0 {
		*(*int32)(at(c, offAVCodecContextGopSize)) = int32(cfg.GOPSize)
	}
	if cfg.BitRate > 0 {
		*(*int64)(at(c, offAVCodecContextBitRate)) = cfg.BitRate
		if !cfg.File {
			*(*int64)(at(c, offAVCodecContextRcMaxRate)) = cfg.BitRate
			*(*int32)(at(c, offAVCodecContextRcBufferSize)) = int32(min(cfg.BitRate, 1<<30))
		}
	}
	if cfg.Quality > 0 {
		*(*int32)(at(c, offAVCodecContextGlobalQuality)) = int32(cfg.Quality)
	}
	if cfg.Threads != 0 {
		*(*int32)(at(c, offAVCodecContextThreadCount)) = int32(cfg.Threads)
	}
	if cfg.ColorSpace != 0 {
		*(*int32)(at(c, offAVCodecContextColorspace)) = cfg.ColorSpace
		if cfg.File && cfg.ColorSpace == ColorSpaceBT709 {
			*(*int32)(at(c, offAVCodecContextColorPrimaries)) = ColorPrimariesBT709
			*(*int32)(at(c, offAVCodecContextColorTrc)) = ColorTrcBT709
		}
	}
	if cfg.FullRange {
		*(*int32)(at(c, offAVCodecContextColorRange)) = colorRangeJPEG
	} else {
		*(*int32)(at(c, offAVCodecContextColorRange)) = colorRangeMPEG
	}
	if cfg.HWDevice != "" {
		typ := avHwdeviceFindType(cfg.HWDevice)
		if typ <= 0 {
			e.Close()
			return nil, fmt.Errorf("ffmpeg: unknown hardware device %q", cfg.HWDevice)
		}
		if err := check(avHwdeviceCtxCreate(&e.hwDev, typ, nil, nil, 0)); err != nil {
			e.Close()
			return nil, fmt.Errorf("ffmpeg: %s device: %w", cfg.HWDevice, err)
		}
		hwFmt := avGetPixFmt(cfg.HWFormat)
		if hwFmt < 0 {
			e.Close()
			return nil, fmt.Errorf("ffmpeg: unknown hardware pixel format %q", cfg.HWFormat)
		}
		e.hwFrames = avHwframeCtxAlloc(e.hwDev)
		if e.hwFrames == nil {
			e.Close()
			return nil, errors.New("ffmpeg: out of memory")
		}
		fc := pp(e.hwFrames, offAVBufferRefData)
		*(*int32)(at(fc, offAVHWFramesContextFormat)) = hwFmt
		*(*int32)(at(fc, offAVHWFramesContextSwFormat)) = swFmt
		*(*int32)(at(fc, offAVHWFramesContextWidth)) = int32(cfg.Width)
		*(*int32)(at(fc, offAVHWFramesContextHeight)) = int32(cfg.Height)
		*(*int32)(at(fc, offAVHWFramesContextInitialPoolSize)) = 8
		if err := check(avHwframeCtxInit(e.hwFrames)); err != nil {
			e.Close()
			return nil, fmt.Errorf("ffmpeg: init %s frames: %w", cfg.HWDevice, err)
		}
		*(*int32)(at(c, offAVCodecContextPixFmt)) = hwFmt
		*(*unsafe.Pointer)(at(c, offAVCodecContextHwDeviceCtx)) = avBufferRef(e.hwDev)
		*(*unsafe.Pointer)(at(c, offAVCodecContextHwFramesCtx)) = avBufferRef(e.hwFrames)
		e.hwFrame = NewFrame()
		if e.hwFrame == nil {
			e.Close()
			return nil, errors.New("ffmpeg: out of memory")
		}
	} else {
		*(*int32)(at(c, offAVCodecContextPixFmt)) = swFmt
	}
	var opts unsafe.Pointer
	for k, v := range cfg.Options {
		avDictSet(&opts, k, v, 0)
	}
	err := check(avcodecOpen2(e.ctx, codec, &opts))
	avDictFree(&opts)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("ffmpeg: open %s encoder: %w", cfg.Name, err)
	}
	return e, nil
}

// Name is the FFmpeg encoder name.
func (e *Encoder) Name() string { return e.name }

// TimeBase is the time base of the frames' and packets' timestamps.
func (e *Encoder) TimeBase() Rational {
	return rat(unsafe.Pointer(e.ctx), offAVCodecContextTimeBase)
}

// FrameSize is the samples an audio encoder takes in each frame, 0 when
// it takes any number.
func (e *Encoder) FrameSize() int {
	if i32(pp(unsafe.Pointer(e.ctx), offAVCodecContextCodec), offAVCodecCapabilities)&codecCapVariableFrameSize != 0 {
		return 0
	}
	return int(i32(unsafe.Pointer(e.ctx), offAVCodecContextFrameSize))
}

// Send submits a software frame (or nil to flush). When the encoder uses a
// hardware frame pool (such as VAAPI), Send uploads f to a hardware surface.
func (e *Encoder) Send(f *Frame) error {
	if f == nil || e.hwFrames == nil {
		return check(avcodecSendFrame(e.ctx, f))
	}
	e.hwFrame.Unref()
	if err := check(avHwframeGetBuffer(e.hwFrames, e.hwFrame, 0)); err != nil {
		return err
	}
	if err := check(avHwframeTransferData(e.hwFrame, f, 0)); err != nil {
		e.hwFrame.Unref()
		return err
	}
	e.hwFrame.SetPTS(f.PTS())
	e.hwFrame.SetPictType(i32(f.p(), offAVFramePictType))
	err := check(avcodecSendFrame(e.ctx, e.hwFrame))
	e.hwFrame.Unref()
	return err
}

// Receive reads the next encoded packet into p. ErrAgain means more frames
// are needed; ErrEOF that the encoder is drained.
func (e *Encoder) Receive(p *Packet) error { return check(avcodecReceivePacket(e.ctx, p)) }

// SetBitRate updates the encoder context's target bit rate in bits per second.
func (e *Encoder) SetBitRate(bps int64) {
	c := unsafe.Pointer(e.ctx)
	*(*int64)(at(c, offAVCodecContextBitRate)) = bps
	*(*int64)(at(c, offAVCodecContextRcMaxRate)) = bps
	*(*int32)(at(c, offAVCodecContextRcBufferSize)) = int32(min(bps, 1<<30))
}

// Close frees the encoder and any hardware contexts it owns.
func (e *Encoder) Close() {
	if e.hwFrame != nil {
		e.hwFrame.Free()
		e.hwFrame = nil
	}
	if e.ctx != nil {
		avcodecFreeContext(&e.ctx)
	}
	if e.hwFrames != nil {
		avBufferUnref(&e.hwFrames)
	}
	if e.hwDev != nil {
		avBufferUnref(&e.hwDev)
	}
}

// AudioEncoderConfig configures an audio encoder.
type AudioEncoderConfig struct {
	Name         string // FFmpeg encoder name, such as "aac" or "libopus"
	SampleRate   int
	Channels     int
	SampleFormat int32 // one of EncoderSampleFormats
	BitRate      int64
	// GlobalHeader puts the codec's setup in its extradata, as a muxer
	// whose GlobalHeader is set wants.
	GlobalHeader bool
	Options      map[string]string
}

// NewAudioEncoder opens an audio encoder for cfg.
func NewAudioEncoder(cfg AudioEncoderConfig) (*Encoder, error) {
	codec := avcodecFindEncoderByNm(cfg.Name)
	if codec == nil {
		return nil, fmt.Errorf("ffmpeg: no encoder %q", cfg.Name)
	}
	e := &Encoder{name: cfg.Name}
	e.ctx = avcodecAllocContext3(codec)
	if e.ctx == nil {
		return nil, errors.New("ffmpeg: out of memory")
	}
	c := unsafe.Pointer(e.ctx)
	*(*int32)(at(c, offAVCodecContextSampleFmt)) = cfg.SampleFormat
	*(*int32)(at(c, offAVCodecContextSampleRate)) = int32(cfg.SampleRate)
	*(*Rational)(at(c, offAVCodecContextTimeBase)) = Rational{Num: 1, Den: int32(cfg.SampleRate)}
	avChannelLayoutDefault(at(c, offAVCodecContextChLayout), int32(cfg.Channels))
	if cfg.BitRate > 0 {
		*(*int64)(at(c, offAVCodecContextBitRate)) = cfg.BitRate
	}
	if cfg.GlobalHeader {
		*(*int32)(at(c, offAVCodecContextFlags)) |= codecFlagGlobalHeader
	}
	var opts unsafe.Pointer
	for k, v := range cfg.Options {
		avDictSet(&opts, k, v, 0)
	}
	err := check(avcodecOpen2(e.ctx, codec, &opts))
	avDictFree(&opts)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("ffmpeg: open %s encoder: %w", cfg.Name, err)
	}
	return e, nil
}

const (
	codecConfigSampleRate   = 2 // AV_CODEC_CONFIG_SAMPLE_RATE
	codecConfigSampleFormat = 3 // AV_CODEC_CONFIG_SAMPLE_FORMAT
)

// supportedConfig is the int values the encoder name lists for config,
// nil when it takes any or has none.
func supportedConfig(name string, config int32) []int32 {
	codec := avcodecFindEncoderByNm(name)
	if codec == nil {
		return nil
	}
	var out ptr
	var n int32
	if check(avcodecGetSupportedCfg(nil, codec, config, 0, &out, &n)) != nil || out == nil || n <= 0 {
		return nil
	}
	return slices.Clone(unsafe.Slice((*int32)(out), n))
}

// EncoderSampleFormats are the sample formats the audio encoder name
// takes, nil when it takes any.
func EncoderSampleFormats(name string) []int32 { return supportedConfig(name, codecConfigSampleFormat) }

// EncoderSampleRates are the sample rates the audio encoder name takes,
// nil when it takes any.
func EncoderSampleRates(name string) []int {
	var rates []int
	for _, r := range supportedConfig(name, codecConfigSampleRate) {
		rates = append(rates, int(r))
	}
	return rates
}

// SampleFormatName is a sample format's name, such as "fltp".
func SampleFormatName(f int32) string { return avGetSampleFmtName(f) }

// SampleFormatPlanar reports a sample format keeping each channel in a
// plane of its own.
func SampleFormatPlanar(f int32) bool { return avSampleFmtIsPlanar(f) != 0 }
