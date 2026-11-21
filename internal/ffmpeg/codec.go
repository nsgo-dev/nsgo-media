package ffmpeg

import (
	"errors"
	"fmt"
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
// type ("videotoolbox", "d3d11va", "vaapi"). It fails when the codec has no
// decoder or the device cannot be made for it.
func NewDecoder(par *CodecParameters, tb Rational, threads int, hwDevice string) (*Decoder, error) {
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
		// Frames the pipeline holds in its queue stay in the decoder's pool.
		*(*int32)(at(c, offAVCodecContextExtraHwFrames)) = 4
	}
	if err := check(avcodecOpen2(d.ctx, codec, nil)); err != nil {
		d.Close()
		return nil, fmt.Errorf("ffmpeg: open %s decoder: %w", par.CodecName(), err)
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

// SubtitleRect is one text of a decoded subtitle: plain text, or an ASS
// event line.
type SubtitleRect struct {
	Text string
	ASS  string
}

// DecodeSubtitle decodes a subtitle packet. It returns no rects for a
// packet that holds none, and times in milliseconds from the packet's
// timestamp.
func (d *Decoder) DecodeSubtitle(p *Packet) (rects []SubtitleRect, start, end uint32, err error) {
	var sub [sizeAVSubtitle]byte
	sp := unsafe.Pointer(&sub[0])
	var got int32
	if err := check(avcodecDecodeSubtitle2(d.ctx, sp, &got, p)); err != nil {
		return nil, 0, 0, err
	}
	if got == 0 {
		return nil, 0, 0, nil
	}
	defer avsubtitleFree(sp)
	n := int(uint32(i32(sp, offAVSubtitleNumRects)))
	if arr := pp(sp, offAVSubtitleRects); arr != nil && n > 0 {
		for _, r := range unsafe.Slice((*unsafe.Pointer)(arr), n) {
			rects = append(rects, SubtitleRect{Text: gostring(pp(r, offAVSubtitleRectText)), ASS: gostring(pp(r, offAVSubtitleRectAss))})
		}
	}
	keep(sub)
	return rects, uint32(i32(sp, offAVSubtitleStartDisplayTime)), uint32(i32(sp, offAVSubtitleEndDisplayTime)), nil
}
