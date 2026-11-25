package ffmpeg

import (
	"errors"
	"fmt"
	"image"
	"unsafe"
)

// Decoder is an opened decoder for one stream.
type Decoder struct {
	ctx *CodecContext
	hw  unsafe.Pointer // the hardware device's AVBufferRef, nil for software
	// HWDevice is the device type it decodes on, empty for software.
	HWDevice string
}

// NewDecoder opens a decoder for par's codec, with threads threads (0 for
// FFmpeg's choice) and, when hwDevice names one, on that hardware device
// type ("videotoolbox", "d3d11va", "vaapi"), whose pool keeps extraHW
// surfaces beyond what decoding needs for frames the caller holds. It
// fails when the codec has no decoder or the device cannot be made for it.
func NewDecoder(par *CodecParameters, tb Rational, threads int, hwDevice string, extraHW int) (*Decoder, error) {
	codec := avcodecFindDecoder(par.CodecID())
	if codec == nil {
		return nil, fmt.Errorf("ffmpeg: no decoder for %s", par.CodecName())
	}
	d := &Decoder{}
	if hwDevice != "" {
		typ := avHwdeviceFindType(hwDevice)
		if typ <= 0 || !codecSupportsDevice(codec, typ) {
			return nil, fmt.Errorf("ffmpeg: %s cannot decode on %s", par.CodecName(), hwDevice)
		}
		if err := check(avHwdeviceCtxCreate(&d.hw, typ, nil, nil, 0)); err != nil {
			return nil, fmt.Errorf("ffmpeg: %s device: %w", hwDevice, err)
		}
		d.HWDevice = hwDevice
	}
	d.ctx = avcodecAllocContext3(codec)
	if d.ctx == nil {
		d.Close()
		return nil, errors.New("ffmpeg: out of memory")
	}
	if err := check(avcodecParametersToCtx(d.ctx, par)); err != nil {
		d.Close()
		return nil, err
	}
	c := unsafe.Pointer(d.ctx)
	*(*Rational)(at(c, offAVCodecContextPktTimebase)) = tb
	*(*int32)(at(c, offAVCodecContextThreadCount)) = int32(threads)
	if d.hw != nil {
		*(*unsafe.Pointer)(at(c, offAVCodecContextHwDeviceCtx)) = avBufferRef(d.hw)
		*(*int32)(at(c, offAVCodecContextExtraHwFrames)) = int32(extraHW)
	}
	if err := check(avcodecOpen2(d.ctx, codec, nil)); err != nil {
		d.Close()
		return nil, fmt.Errorf("ffmpeg: open %s decoder: %w", par.CodecName(), err)
	}
	return d, nil
}

// NewRawDecoder opens a low-latency decoder by codec name (such as "h264")
// without container parameters, for raw access-unit streams. When hwDevice
// names a hardware device type ("videotoolbox", "d3d11va", "vaapi"), it
// opens on that device.
func NewRawDecoder(codecName string, threads int, hwDevice string) (*Decoder, error) {
	codec := avcodecFindDecoderByNm(codecName)
	if codec == nil {
		return nil, fmt.Errorf("ffmpeg: no decoder for %s", codecName)
	}
	d := &Decoder{}
	if hwDevice != "" {
		typ := avHwdeviceFindType(hwDevice)
		if typ <= 0 || !codecSupportsDevice(codec, typ) {
			return nil, fmt.Errorf("ffmpeg: %s cannot decode on %s", codecName, hwDevice)
		}
		if err := check(avHwdeviceCtxCreate(&d.hw, typ, nil, nil, 0)); err != nil {
			return nil, fmt.Errorf("ffmpeg: %s device: %w", hwDevice, err)
		}
		d.HWDevice = hwDevice
	}
	d.ctx = avcodecAllocContext3(codec)
	if d.ctx == nil {
		d.Close()
		return nil, errors.New("ffmpeg: out of memory")
	}
	c := unsafe.Pointer(d.ctx)
	*(*int32)(at(c, offAVCodecContextFlags)) |= codecFlagLowDelay
	*(*int32)(at(c, offAVCodecContextThreadCount)) = int32(threads)
	*(*int32)(at(c, offAVCodecContextThreadType)) = threadTypeSlice
	if d.hw != nil {
		*(*unsafe.Pointer)(at(c, offAVCodecContextHwDeviceCtx)) = avBufferRef(d.hw)
	}
	if err := check(avcodecOpen2(d.ctx, codec, nil)); err != nil {
		d.Close()
		return nil, fmt.Errorf("ffmpeg: open %s decoder: %w", codecName, err)
	}
	return d, nil
}

func codecSupportsDevice(c *Codec, typ int32) bool {
	for i := int32(0); ; i++ {
		cfg := avcodecGetHWConfig(c, i)
		if cfg == nil {
			return false
		}
		if i32(cfg, offAVCodecHWConfigMethods)&hwConfigMethodD != 0 && i32(cfg, offAVCodecHWConfigDeviceType) == typ {
			return true
		}
	}
}

// Send gives the decoder a packet; nil drains it. ErrAgain means frames
// must be received first.
func (d *Decoder) Send(p *Packet) error { return check(avcodecSendPacket(d.ctx, p)) }

// Receive takes the next decoded frame into f. ErrAgain means it needs
// more packets; ErrEOF that it is drained.
func (d *Decoder) Receive(f *Frame) error { return check(avcodecReceiveFrame(d.ctx, f)) }

// Flush drops the decoder's buffered packets and frames, as after a seek.
func (d *Decoder) Flush() { avcodecFlushBuffers(d.ctx) }

// Close frees the decoder and its device.
func (d *Decoder) Close() {
	if d.ctx != nil {
		avcodecFreeContext(&d.ctx)
	}
	if d.hw != nil {
		avBufferUnref(&d.hw)
	}
}

// Subtitle is a decoded subtitle: what shows from Start to End
// milliseconds after PTS, replacing what its stream showed before. No
// rects clear the screen, as PGS and DVB end a subtitle.
type Subtitle struct {
	// PTS is in microseconds, NoPTS when unknown.
	PTS        int64
	Start, End uint32 // End is math.MaxUint32 when unknown
	Rects      []SubtitleRect
}

// SubtitleRect is one part of a decoded subtitle: plain text, an ASS
// event line, or a bitmap at X, Y on the stream's canvas.
type SubtitleRect struct {
	Text string
	ASS  string
	X, Y int
	// Image is a bitmap's pixels, premultiplied as image.RGBA is; nil
	// for text.
	Image *image.RGBA
}

const subtitleBitmap = 1 // SUBTITLE_BITMAP

// DecodeSubtitle decodes a subtitle packet, returning nil for a packet
// that completes none.
func (d *Decoder) DecodeSubtitle(p *Packet) (*Subtitle, error) {
	var sub [sizeAVSubtitle]byte
	sp := unsafe.Pointer(&sub[0])
	var got int32
	if err := check(avcodecDecodeSubtitle2(d.ctx, sp, &got, p)); err != nil {
		return nil, err
	}
	if got == 0 {
		return nil, nil
	}
	defer avsubtitleFree(sp)
	s := &Subtitle{
		PTS:   i64(sp, offAVSubtitlePts),
		Start: uint32(i32(sp, offAVSubtitleStartDisplayTime)),
		End:   uint32(i32(sp, offAVSubtitleEndDisplayTime)),
	}
	n := int(uint32(i32(sp, offAVSubtitleNumRects)))
	if arr := pp(sp, offAVSubtitleRects); arr != nil && n > 0 {
		for _, r := range unsafe.Slice((*unsafe.Pointer)(arr), n) {
			if i32(r, offAVSubtitleRectType) == subtitleBitmap {
				if img := bitmap(r); img != nil {
					s.Rects = append(s.Rects, SubtitleRect{X: int(i32(r, offAVSubtitleRectX)), Y: int(i32(r, offAVSubtitleRectY)), Image: img})
				}
				continue
			}
			s.Rects = append(s.Rects, SubtitleRect{Text: gostring(pp(r, offAVSubtitleRectText)), ASS: gostring(pp(r, offAVSubtitleRectAss))})
		}
	}
	keep(sub)
	return s, nil
}

// bitmap converts a bitmap rect's palette indices to premultiplied RGBA,
// nil for an empty one.
func bitmap(r unsafe.Pointer) *image.RGBA {
	w, h := int(i32(r, offAVSubtitleRectW)), int(i32(r, offAVSubtitleRectH))
	colors := int(i32(r, offAVSubtitleRectNbColors))
	pix, pal := pp(r, offAVSubtitleRectData), pp(r, offAVSubtitleRectData+8)
	stride := int(i32(r, offAVSubtitleRectLinesize))
	if w <= 0 || h <= 0 || pix == nil || pal == nil || stride < w || colors <= 0 {
		return nil
	}
	colors = min(colors, 256)
	// Each entry is AV_PIX_FMT_RGB32: 0xAARRGGBB in a native uint32.
	var lut [256][4]byte
	for i, c := range unsafe.Slice((*uint32)(pal), colors) {
		a := c >> 24
		lut[i] = [4]byte{byte((c >> 16 & 0xff) * a / 255), byte((c >> 8 & 0xff) * a / 255), byte((c & 0xff) * a / 255), byte(a)}
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	src := unsafe.Slice((*byte)(pix), stride*(h-1)+w)
	for y := range h {
		row := img.Pix[y*img.Stride:]
		for x, v := range src[y*stride : y*stride+w] {
			copy(row[x*4:x*4+4], lut[v][:])
		}
	}
	return img
}

// Size is the decoder's picture size, which a subtitle decoder sets to
// its canvas once a packet tells it; 0 until then.
func (d *Decoder) Size() (w, h int) {
	c := unsafe.Pointer(d.ctx)
	return int(i32(c, offAVCodecContextWidth)), int(i32(c, offAVCodecContextHeight))
}
