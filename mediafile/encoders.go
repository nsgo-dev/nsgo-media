package mediafile

import (
	"maps"
	"math"
	"runtime"
	"strconv"
)

// videoSpec is one FFmpeg video encoder and how a file is encoded with it.
type videoSpec struct {
	name     string
	hardware bool
	hwDevice string // a device frames are uploaded to, such as "vaapi"
	hwFormat string
	pixFmt   string
	bFrames  int
	options  map[string]string
	// constQ sets constant quality q (1–100): the options it adds and the
	// encoder's global_quality. Nil when the encoder has no such mode, so
	// a bit rate estimated from q stands in.
	constQ func(q int) (map[string]string, int)
	// rateOptions are added when encoding at a bit rate.
	rateOptions map[string]string
	// noRate marks an encoder whose profile sets its rate, as ProRes'.
	noRate bool
	// tag is the codec tag in MP4 and MOV, such as "hvc1".
	tag string
}

// crf maps quality q (1–100) to a CRF-like scale whose best is 0 and
// worst top: 80 is about x264's 19 on H.264's 0–51.
func crf(q, top int) int {
	return min(top, max(0, int(math.Round(float64(top)*(1-float64(q)/127)))))
}

func crfOption(key string, top int) func(int) (map[string]string, int) {
	return func(q int) (map[string]string, int) {
		return map[string]string{key: strconv.Itoa(crf(q, top))}, 0
	}
}

// globalQuality takes constant quality as the encoder's global_quality,
// on a CRF scale up to top, or q itself when top is 0.
func globalQuality(top int) func(int) (map[string]string, int) {
	return func(q int) (map[string]string, int) {
		if top == 0 {
			return nil, q
		}
		return nil, crf(q, top)
	}
}

// estimateBitrate is the bit rate standing in for quality q where an
// encoder has no constant-quality mode: bits per pixel a frame rising with
// q, about 0.15 at 80 for H.264, less for HEVC and VP9.
func estimateBitrate(codec string, q, w, h int, fps float64) int64 {
	bpp := 0.0019 * float64(q)
	if codec == HEVC || codec == VP9 {
		bpp *= 0.6
	}
	return int64(bpp * float64(w*h) * fps)
}

// videoEncoders are codec's encoders on this platform, hardware first.
func videoEncoders(codec string, alpha bool) []videoSpec {
	switch codec {
	case ProRes, ProRes4444:
		pix, profile := "yuv422p10le", "hq"
		if codec == ProRes4444 {
			pix, profile = "yuv444p10le", "4444"
			if alpha {
				pix = "yuva444p10le"
			}
		}
		return []videoSpec{{name: "prores_ks", pixFmt: pix, noRate: true, options: map[string]string{"profile": profile, "vendor": "apl0"}}}
	case VP9:
		pix := "yuv420p"
		if alpha {
			pix = "yuva420p"
		}
		return []videoSpec{{
			name: "libvpx-vp9", pixFmt: pix,
			options: map[string]string{"deadline": "good", "cpu-used": "3", "row-mt": "1"},
			constQ:  crfOption("crf", 63),
		}}
	case H264, HEVC:
		return hardwareEncoders(codec)
	}
	return nil
}

func hardwareEncoders(codec string) []videoSpec {
	prefix := map[string]string{H264: "h264_", HEVC: "hevc_"}[codec]
	tag := ""
	if codec == HEVC {
		// Apple's players take HEVC tagged hvc1, its parameter sets in
		// the sample description.
		tag = "hvc1"
	}
	// B-frames where every device of the encoder's takes them: HEVC's
	// need newer NVIDIA and AMD chips than H.264's.
	bFrames := 2
	if codec == HEVC {
		bFrames = 0
	}
	var specs []videoSpec
	switch runtime.GOOS {
	case "darwin", "ios":
		// Constant quality is Apple Silicon Macs' alone.
		var q func(int) (map[string]string, int)
		if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
			q = globalQuality(0)
		}
		// FFmpeg delays H.264's decoding timestamps by one frame for its
		// B-frames, but VideoToolbox reorders them two deep, which gives
		// packets decoded after they show; HEVC's are delayed by two.
		vtB := 0
		if codec == HEVC {
			vtB = 2
		}
		specs = []videoSpec{
			{name: prefix + "videotoolbox", hardware: true, pixFmt: "nv12", bFrames: vtB, options: map[string]string{"allow_sw": "0"}, constQ: q},
			{name: prefix + "videotoolbox", pixFmt: "nv12", bFrames: vtB, options: map[string]string{"allow_sw": "1"}, constQ: q},
		}
	case "windows":
		specs = []videoSpec{
			{
				name: prefix + "nvenc", hardware: true, pixFmt: "nv12", bFrames: bFrames,
				options: map[string]string{"preset": "p5", "tune": "hq", "rc": "vbr"},
				constQ:  crfOption("cq", 51),
			},
			{
				name: prefix + "qsv", hardware: true, pixFmt: "nv12", bFrames: 2,
				options: map[string]string{"preset": "medium"},
				constQ:  globalQuality(51),
			},
			{
				name: prefix + "amf", hardware: true, pixFmt: "nv12", bFrames: bFrames,
				options: map[string]string{"usage": "transcoding", "quality": "balanced"},
				constQ: func(q int) (map[string]string, int) {
					qp := strconv.Itoa(crf(q, 51))
					return map[string]string{"rc": "cqp", "qp_i": qp, "qp_p": qp, "qp_b": qp}, 0
				},
				rateOptions: map[string]string{"rc": "vbr_peak"},
			},
			{
				name: prefix + "mf", hardware: true, pixFmt: "nv12",
				options:     map[string]string{"hw_encoding": "1"},
				constQ:      mfQuality,
				rateOptions: map[string]string{"rate_control": "u_vbr"},
			},
			{
				name: prefix + "mf", pixFmt: "nv12",
				options:     map[string]string{"hw_encoding": "0"},
				constQ:      mfQuality,
				rateOptions: map[string]string{"rate_control": "u_vbr"},
			},
		}
	case "linux":
		specs = []videoSpec{{
			name: prefix + "vaapi", hardware: true, hwDevice: "vaapi", hwFormat: "vaapi", pixFmt: "nv12", bFrames: 2,
			constQ: globalQuality(51),
		}}
		if codec == H264 {
			specs = append(specs, videoSpec{
				name: "libopenh264", pixFmt: "yuv420p",
				options: map[string]string{"rc_mode": "bitrate", "allow_skip_frames": "0"},
			})
		}
	}
	for i := range specs {
		// VAAPI's drivers may write parameter sets of their own, unlike
		// the extradata, which hvc1 would keep alone; hev1 keeps them.
		if specs[i].hwDevice != "vaapi" {
			specs[i].tag = tag
		}
	}
	return specs
}

func mfQuality(q int) (map[string]string, int) {
	return map[string]string{"rate_control": "quality", "quality": strconv.Itoa(q)}, 0
}

// encoderOptions are spec's options for quality q or bitrate.
func (s videoSpec) encoderOptions(q int, bitrate int64) (opts map[string]string, globalQ int, rate int64) {
	opts = maps.Clone(s.options)
	if opts == nil {
		opts = map[string]string{}
	}
	if s.noRate {
		return opts, 0, 0
	}
	if bitrate <= 0 && s.constQ != nil {
		extra, gq := s.constQ(q)
		maps.Copy(opts, extra)
		return opts, gq, 0
	}
	maps.Copy(opts, s.rateOptions)
	return opts, 0, bitrate
}

// audioCodec is the audio encoder of a container and its default bit rate
// a channel.
func audioCodec(container string) (name string, perChannel int) {
	if container == WebM {
		return "libopus", 64_000
	}
	return "aac", 96_000
}
