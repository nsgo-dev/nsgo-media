package codec

import (
	"maps"
	"runtime"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// encoderSpec defines one FFmpeg encoder backend and its low-latency defaults.
type encoderSpec struct {
	codec           string
	name            string
	hardware        bool
	hwDevice        string
	hwFormat        string
	pixFmt          string
	options         map[string]string
	reopenOnBitRate bool // true if avctx.bit_rate changes require reopening
}

// platformEncoders returns candidate encoders for the current OS, ordered
// with preferred hardware encoders first and software fallback last.
func platformEncoders(codec string) []encoderSpec {
	if codec != H264 {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return []encoderSpec{
			{
				codec:    H264,
				name:     "h264_videotoolbox",
				hardware: true,
				pixFmt:   "nv12",
				options: map[string]string{
					"realtime":   "1",
					"prio_speed": "1",
					"allow_sw":   "0",
				},
			},
			{
				codec:    H264,
				name:     "h264_videotoolbox",
				hardware: false,
				pixFmt:   "nv12",
				options: map[string]string{
					"realtime":   "1",
					"prio_speed": "1",
					"allow_sw":   "1",
				},
			},
		}
	case "windows":
		return []encoderSpec{
			{
				codec:    H264,
				name:     "h264_nvenc",
				hardware: true,
				pixFmt:   "nv12",
				options: map[string]string{
					"preset":         "p1",
					"tune":           "ull",
					"zerolatency":    "1",
					"delay":          "0",
					"forced-idr":     "1",
					"repeat-headers": "1",
				},
			},
			{
				codec:    H264,
				name:     "h264_qsv",
				hardware: true,
				pixFmt:   "nv12",
				options: map[string]string{
					"preset":      "veryfast",
					"async_depth": "1",
					"forced_idr":  "1",
				},
			},
			{
				codec:    H264,
				name:     "h264_amf",
				hardware: true,
				pixFmt:   "nv12",
				options: map[string]string{
					"usage":                 "ultralowlatency",
					"quality":               "speed",
					"header_insertion_mode": "idr",
				},
			},
			{
				codec:           H264,
				name:            "h264_mf",
				hardware:        true,
				pixFmt:          "nv12",
				reopenOnBitRate: true,
				options: map[string]string{
					"scenario":     "display_remoting",
					"rate_control": "ld_vbr",
					"hw_encoding":  "1",
				},
			},
			{
				codec:           H264,
				name:            "h264_mf",
				hardware:        false,
				pixFmt:          "nv12",
				reopenOnBitRate: true,
				options: map[string]string{
					"rate_control": "cbr",
					"hw_encoding":  "0",
				},
			},
		}
	case "linux":
		return []encoderSpec{
			{
				codec:           H264,
				name:            "h264_vaapi",
				hardware:        true,
				hwDevice:        "vaapi",
				hwFormat:        "vaapi",
				pixFmt:          "nv12",
				reopenOnBitRate: true,
				options: map[string]string{
					"async_depth": "1",
					"rc_mode":     "CBR",
				},
			},
			{
				codec:           H264,
				name:            "libopenh264",
				hardware:        false,
				pixFmt:          "yuv420p",
				reopenOnBitRate: true,
				options: map[string]string{
					"allow_skip_frames": "0",
					"rc_mode":           "bitrate",
				},
			},
		}
	default:
		return nil
	}
}

func platformHWDecodeDevice() string {
	switch runtime.GOOS {
	case "darwin":
		return "videotoolbox"
	case "windows":
		return "d3d11va"
	case "linux":
		return "vaapi"
	default:
		return ""
	}
}

// Encoders probes the machine's available video encoders, ordered with
// preferred hardware encoders first and software fallbacks after.
// Each reported encoder has been verified to open on the current system.
func Encoders() ([]Capability, error) {
	if err := ffmpeg.Load(); err != nil {
		return nil, err
	}
	var out []Capability
	seenHW := map[string]bool{}
	for _, spec := range platformEncoders(H264) {
		if !ffmpeg.HasEncoder(spec.name) {
			continue
		}
		// Avoid listing the same encoder name twice when its hardware mode
		// already succeeded (e.g. h264_videotoolbox or h264_mf).
		if !spec.hardware && seenHW[spec.name] {
			continue
		}
		enc, err := openSpec(spec, 256, 256, 60, 1_000_000)
		if err != nil {
			continue
		}
		enc.Close()
		if spec.hardware {
			seenHW[spec.name] = true
		}
		out = append(out, Capability{
			Codec:    spec.codec,
			Name:     spec.name,
			Hardware: spec.hardware,
		})
	}
	return out, nil
}

// Decoders probes the machine's available video decoders, ordered with
// hardware acceleration first and software fallback after.
func Decoders() ([]Capability, error) {
	if err := ffmpeg.Load(); err != nil {
		return nil, err
	}
	var out []Capability
	if !ffmpeg.HasDecoder(H264) {
		return out, nil
	}
	if hw := platformHWDecodeDevice(); hw != "" && ffmpeg.DecoderSupportsHW(H264, hw) {
		out = append(out, Capability{
			Codec:    H264,
			Name:     hw,
			Hardware: true,
		})
	}
	out = append(out, Capability{
		Codec:    H264,
		Name:     H264,
		Hardware: false,
	})
	return out, nil
}

func openSpec(spec encoderSpec, w, h, fps int, bitRate int64) (*ffmpeg.Encoder, error) {
	opts := make(map[string]string, len(spec.options))
	maps.Copy(opts, spec.options)
	return ffmpeg.NewEncoder(ffmpeg.EncoderConfig{
		Name:       spec.name,
		HWDevice:   spec.hwDevice,
		HWFormat:   spec.hwFormat,
		PixFmt:     spec.pixFmt,
		Width:      w,
		Height:     h,
		FPS:        fps,
		BitRate:    bitRate,
		GOPSize:    fps * 2,
		ColorSpace: ffmpeg.ColorSpaceBT709,
		FullRange:  false,
		Options:    opts,
	})
}
