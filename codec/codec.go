// Package codec provides low-latency video encoding, decoding and hardware
// capability probing on top of NSGo Media's bundled FFmpeg libraries.
//
// Encoders are configured for interactive streaming: no B-frames, low-delay
// settings, on-demand keyframes with inline parameter sets (Annex B), and
// dynamic bit-rate updates. Decoders prefer platform hardware acceleration
// and fall back to software automatically.
package codec

import "fmt"

// Codec names match FFmpeg's short codec names, for use in capability
// negotiation handshakes.
const (
	H264 = "h264"
)

// PixelFormat identifies the layout of an uncompressed input Frame.
type PixelFormat int

const (
	// BGRA is packed 32-bit BGRA (4 bytes per pixel: B, G, R, A).
	BGRA PixelFormat = iota
	// NV12 is semi-planar 4:2:0 YUV: a full-resolution Y plane followed by
	// a half-resolution interleaved UV plane.
	NV12
)

// String returns the pixel format's name.
func (f PixelFormat) String() string {
	switch f {
	case BGRA:
		return "bgra"
	case NV12:
		return "nv12"
	default:
		return fmt.Sprintf("PixelFormat(%d)", int(f))
	}
}

// Frame is an uncompressed video frame passed to Encoder.Encode.
type Frame struct {
	Format PixelFormat
	Width  int
	Height int
	// Data holds BGRA pixels or, for NV12, the Y plane (and optionally the
	// interleaved UV plane immediately after Stride*Height when UV is nil).
	Data []byte
	// Stride is the byte stride of Data rows. When zero, it defaults to
	// Width*4 for BGRA and Width for NV12.
	Stride int
	// UV optionally holds the NV12 interleaved chroma plane separately from
	// Data. Ignored for BGRA.
	UV []byte
	// UVStride is the byte stride of UV rows. When zero, it defaults to
	// Stride (or Width).
	UVStride int
}

// AccessUnit is one encoded video frame in Annex B format. Keyframes carry
// inline SPS/PPS so a decoder can recover immediately after dropped frames.
type AccessUnit struct {
	Data     []byte
	Keyframe bool
	PTS      int64
}

// Capability describes an encoder or decoder available on this machine.
type Capability struct {
	// Codec is the standard codec name, such as H264 ("h264").
	Codec string
	// Name is the underlying FFmpeg codec or hardware backend name
	// (e.g. "h264_videotoolbox", "h264_nvenc", "h264_vaapi", "libopenh264").
	Name string
	// Hardware reports whether the implementation uses hardware acceleration.
	Hardware bool
}
