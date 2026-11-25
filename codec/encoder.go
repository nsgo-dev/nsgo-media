package codec

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// EncoderConfig configures a low-latency video encoder.
type EncoderConfig struct {
	// Codec is the target codec name; defaults to H264 when empty.
	Codec string
	// Width and Height are the picture dimensions in pixels (must be positive
	// and even for 4:2:0 encoding).
	Width  int
	Height int
	// FPS is the nominal frame rate used for rate control; defaults to 60.
	FPS int
	// BitRate is the target bit rate in bits per second; defaults to 8 Mbps.
	BitRate int
	// SoftwareOnly skips hardware encoders and opens a software encoder.
	SoftwareOnly bool
	// Name, when non-empty, forces a specific FFmpeg encoder backend
	// (e.g. "h264_nvenc", "h264_mf", "libopenh264").
	Name string
}

// Encoder encodes uncompressed BGRA or NV12 frames into low-latency video
// access units without B-frames.
type Encoder struct {
	spec    encoderSpec
	width   int
	height  int
	fps     int
	bitRate int64

	enc     *ffmpeg.Encoder
	swFrame *ffmpeg.Frame
	srcWrap *ffmpeg.Frame
	pkt     *ffmpeg.Packet
	scaler  ffmpeg.Scaler

	swPixFmt  int32
	bgraFmt   int32
	nv12Fmt   int32
	pts       int64
	reopenBit bool
	closed    bool
}

// NewEncoder opens a low-latency video encoder for cfg, preferring hardware
// encoders and falling back to available alternatives unless cfg.Name or
// cfg.SoftwareOnly restricts the choice.
func NewEncoder(cfg EncoderConfig) (*Encoder, error) {
	if cfg.Codec == "" {
		cfg.Codec = H264
	}
	if cfg.Codec != H264 {
		return nil, fmt.Errorf("codec: unsupported codec %q", cfg.Codec)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width%2 != 0 || cfg.Height%2 != 0 {
		return nil, fmt.Errorf("codec: dimensions %dx%d must be positive and even", cfg.Width, cfg.Height)
	}
	if cfg.FPS <= 0 {
		cfg.FPS = 60
	}
	if cfg.BitRate <= 0 {
		cfg.BitRate = 8_000_000
	}
	if err := ffmpeg.Load(); err != nil {
		return nil, err
	}

	candidates := selectEncoderSpecs(cfg)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("codec: no matching %s encoder for this platform", cfg.Codec)
	}

	var enc *ffmpeg.Encoder
	var chosen encoderSpec
	var errs []string
	for _, spec := range candidates {
		if !ffmpeg.HasEncoder(spec.name) {
			errs = append(errs, spec.name+": not compiled in")
			continue
		}
		opened, err := openSpec(spec, cfg.Width, cfg.Height, cfg.FPS, int64(cfg.BitRate))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s (hw=%v): %v", spec.name, spec.hardware, err))
			continue
		}
		enc = opened
		chosen = spec
		break
	}
	if enc == nil {
		return nil, fmt.Errorf("codec: failed to open %s encoder: %s", cfg.Codec, strings.Join(errs, "; "))
	}

	swFmt := ffmpeg.PixelFormat(chosen.pixFmt)
	swFrame := ffmpeg.NewFrame()
	srcWrap := ffmpeg.NewFrame()
	pkt := ffmpeg.NewPacket()
	if swFrame == nil || srcWrap == nil || pkt == nil {
		enc.Close()
		if swFrame != nil {
			swFrame.Free()
		}
		if srcWrap != nil {
			srcWrap.Free()
		}
		if pkt != nil {
			pkt.Free()
		}
		return nil, errors.New("codec: out of memory")
	}
	if err := swFrame.AllocBuffer(cfg.Width, cfg.Height, swFmt); err != nil {
		enc.Close()
		swFrame.Free()
		srcWrap.Free()
		pkt.Free()
		return nil, err
	}
	swFrame.SetColor(ffmpeg.ColorSpaceBT709, false)

	return &Encoder{
		spec:     chosen,
		width:    cfg.Width,
		height:   cfg.Height,
		fps:      cfg.FPS,
		bitRate:  int64(cfg.BitRate),
		enc:      enc,
		swFrame:  swFrame,
		srcWrap:  srcWrap,
		pkt:      pkt,
		swPixFmt: swFmt,
		bgraFmt:  ffmpeg.PixelFormat("bgra"),
		nv12Fmt:  ffmpeg.PixelFormat("nv12"),
	}, nil
}

func selectEncoderSpecs(cfg EncoderConfig) []encoderSpec {
	all := platformEncoders(cfg.Codec)
	var out []encoderSpec
	for _, spec := range all {
		if cfg.Name != "" && spec.name != cfg.Name {
			continue
		}
		if cfg.SoftwareOnly && spec.hardware {
			continue
		}
		out = append(out, spec)
	}
	return out
}

// Name returns the FFmpeg encoder backend name (e.g. "h264_videotoolbox").
func (e *Encoder) Name() string { return e.spec.name }

// Hardware reports whether the opened encoder uses hardware acceleration.
func (e *Encoder) Hardware() bool { return e.spec.hardware }

// SetBitRate changes the target bit rate (in bits per second) while running.
func (e *Encoder) SetBitRate(bps int) error {
	if e.closed {
		return errors.New("codec: encoder is closed")
	}
	if bps <= 0 {
		return fmt.Errorf("codec: bit rate %d must be positive", bps)
	}
	e.bitRate = int64(bps)
	e.enc.SetBitRate(e.bitRate)
	if e.spec.reopenOnBitRate {
		e.reopenBit = true
	}
	return nil
}

// Encode encodes frame into a single AccessUnit. Setting keyframe forces an
// IDR keyframe with inline SPS/PPS so a remote decoder can recover after
// packet or frame loss.
func (e *Encoder) Encode(frame Frame, keyframe bool) (AccessUnit, error) {
	if e.closed {
		return AccessUnit{}, errors.New("codec: encoder is closed")
	}
	if frame.Width != e.width || frame.Height != e.height {
		return AccessUnit{}, fmt.Errorf("codec: frame size %dx%d does not match encoder %dx%d",
			frame.Width, frame.Height, e.width, e.height)
	}
	if e.reopenBit {
		opened, err := openSpec(e.spec, e.width, e.height, e.fps, e.bitRate)
		if err != nil {
			return AccessUnit{}, fmt.Errorf("codec: apply bit rate: %w", err)
		}
		e.enc.Close()
		e.enc = opened
		e.reopenBit = false
		keyframe = true
	}

	if err := e.fillInput(frame); err != nil {
		return AccessUnit{}, err
	}
	pts := e.pts
	e.pts++
	e.swFrame.SetPTS(pts)
	if keyframe || pts == 0 {
		e.swFrame.SetPictType(ffmpeg.PictTypeI)
	} else {
		e.swFrame.SetPictType(ffmpeg.PictTypeNone)
	}
	if err := e.enc.Send(e.swFrame); err != nil {
		return AccessUnit{}, err
	}
	err := e.enc.Receive(e.pkt)
	if errors.Is(err, ffmpeg.ErrAgain) {
		return AccessUnit{PTS: pts}, nil
	}
	if err != nil {
		return AccessUnit{}, err
	}
	raw := e.pkt.Data()
	data := make([]byte, len(raw))
	copy(data, raw)
	au := AccessUnit{
		Data:     data,
		Keyframe: e.pkt.Key(),
		PTS:      e.pkt.PTS(),
	}
	e.pkt.Unref()
	return au, nil
}

func (e *Encoder) fillInput(f Frame) error {
	if err := e.swFrame.MakeWritable(); err != nil {
		return err
	}
	switch f.Format {
	case BGRA:
		stride := f.Stride
		if stride == 0 {
			stride = e.width * 4
		}
		if stride < e.width*4 || len(f.Data) < stride*(e.height-1)+e.width*4 {
			return errors.New("codec: BGRA frame buffer too small")
		}
		e.srcWrap.SetGeometry(e.width, e.height, e.bgraFmt)
		e.srcWrap.SetPlane(0, unsafe.Pointer(&f.Data[0]), stride)
		e.srcWrap.SetPlane(1, nil, 0)
		err := e.scaler.Convert(e.swFrame, e.srcWrap)
		e.srcWrap.SetPlane(0, nil, 0)
		runtime.KeepAlive(f.Data)
		return err

	case NV12:
		yStride := f.Stride
		if yStride == 0 {
			yStride = e.width
		}
		uvData := f.UV
		uvStride := f.UVStride
		if uvStride == 0 {
			uvStride = yStride
		}
		if uvData == nil {
			yBytes := yStride * e.height
			if len(f.Data) < yBytes+uvStride*(e.height/2-1)+e.width {
				return errors.New("codec: contiguous NV12 frame buffer too small")
			}
			uvData = f.Data[yBytes:]
		} else {
			if yStride < e.width || len(f.Data) < yStride*(e.height-1)+e.width {
				return errors.New("codec: NV12 Y plane buffer too small")
			}
			if uvStride < e.width || len(uvData) < uvStride*(e.height/2-1)+e.width {
				return errors.New("codec: NV12 UV plane buffer too small")
			}
		}
		if e.swPixFmt == e.nv12Fmt {
			copyPlane(e.swFrame, 0, f.Data, yStride, e.width, e.height)
			copyPlane(e.swFrame, 1, uvData, uvStride, e.width, e.height/2)
			return nil
		}
		e.srcWrap.SetGeometry(e.width, e.height, e.nv12Fmt)
		e.srcWrap.SetColor(ffmpeg.ColorSpaceBT709, false)
		e.srcWrap.SetPlane(0, unsafe.Pointer(&f.Data[0]), yStride)
		e.srcWrap.SetPlane(1, unsafe.Pointer(&uvData[0]), uvStride)
		err := e.scaler.Convert(e.swFrame, e.srcWrap)
		e.srcWrap.SetPlane(0, nil, 0)
		e.srcWrap.SetPlane(1, nil, 0)
		runtime.KeepAlive(f.Data)
		runtime.KeepAlive(uvData)
		return err

	default:
		return fmt.Errorf("codec: unsupported pixel format %v", f.Format)
	}
}

func copyPlane(dst *ffmpeg.Frame, plane int, src []byte, srcStride, rowBytes, rows int) {
	dp, dstStride := dst.Plane(plane)
	dstBytes := unsafe.Slice((*byte)(dp), dstStride*rows)
	for y := range rows {
		copy(dstBytes[y*dstStride:y*dstStride+rowBytes], src[y*srcStride:y*srcStride+rowBytes])
	}
}

// Close releases the encoder and its FFmpeg resources.
func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	e.scaler.Close()
	if e.pkt != nil {
		e.pkt.Free()
		e.pkt = nil
	}
	if e.srcWrap != nil {
		e.srcWrap.Free()
		e.srcWrap = nil
	}
	if e.swFrame != nil {
		e.swFrame.Free()
		e.swFrame = nil
	}
	if e.enc != nil {
		e.enc.Close()
		e.enc = nil
	}
	return nil
}
