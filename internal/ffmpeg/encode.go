package ffmpeg

import (
	"errors"
	"fmt"
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

// EncoderConfig configures a low-latency video encoder.
type EncoderConfig struct {
	Name       string            // FFmpeg encoder name, e.g. "h264_videotoolbox"
	HWDevice   string            // optional hardware device for frame upload, e.g. "vaapi"
	HWFormat   string            // hardware pixel format when HWDevice is set, e.g. "vaapi"
	PixFmt     string            // software pixel format fed in, e.g. "nv12" or "yuv420p"
	Width      int
	Height     int
	FPS        int
	BitRate    int64
	GOPSize    int
	ColorSpace int32
	FullRange  bool
	Options    map[string]string
}

// Encoder is an opened FFmpeg video encoder.
type Encoder struct {
	ctx      *CodecContext
	hwDev    unsafe.Pointer // AVBufferRef, non-nil when HWDevice is used
	hwFrames unsafe.Pointer // AVBufferRef, non-nil when HWDevice is used
	hwFrame  *Frame         // reusable hardware surface for upload
	name     string
}

// NewEncoder opens a video encoder with no B-frames for low-latency encoding.
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
	c := unsafe.Pointer(e.ctx)
	*(*int32)(at(c, offAVCodecContextWidth)) = int32(cfg.Width)
	*(*int32)(at(c, offAVCodecContextHeight)) = int32(cfg.Height)
	*(*Rational)(at(c, offAVCodecContextTimeBase)) = Rational{Num: 1, Den: int32(fps)}
	*(*Rational)(at(c, offAVCodecContextFramerate)) = Rational{Num: int32(fps), Den: 1}
	*(*int32)(at(c, offAVCodecContextMaxBFrames)) = 0
	*(*int32)(at(c, offAVCodecContextFlags)) |= codecFlagLowDelay
	if cfg.GOPSize > 0 {
		*(*int32)(at(c, offAVCodecContextGopSize)) = int32(cfg.GOPSize)
	}
	if cfg.BitRate > 0 {
		*(*int64)(at(c, offAVCodecContextBitRate)) = cfg.BitRate
		*(*int64)(at(c, offAVCodecContextRcMaxRate)) = cfg.BitRate
		*(*int32)(at(c, offAVCodecContextRcBufferSize)) = int32(min(cfg.BitRate, 1<<30))
	}
	if cfg.ColorSpace != 0 {
		*(*int32)(at(c, offAVCodecContextColorspace)) = cfg.ColorSpace
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
