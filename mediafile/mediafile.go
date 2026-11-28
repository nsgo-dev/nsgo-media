// Package mediafile writes video files from frames and PCM audio, and
// decodes audio files into PCM, on NSGo Media's FFmpeg libraries, so other
// modules encode and decode media without binding FFmpeg themselves.
//
// CreateVideo writes MP4, MOV or WebM: H.264 or HEVC in MP4 and MOV,
// ProRes and ProRes 4444 in MOV, VP9 in WebM, with an AAC track in MP4 and
// MOV or an Opus one in WebM. Encoders are tuned for files, with B-frames
// and lookahead, and the platform's hardware ones come first.
package mediafile

// Containers CreateVideo writes.
const (
	MP4  = "mp4"
	MOV  = "mov"
	WebM = "webm"
)

// Video codecs CreateVideo encodes.
const (
	H264       = "h264"
	HEVC       = "hevc"
	ProRes     = "prores"     // ProRes 422 HQ, 10-bit
	ProRes4444 = "prores4444" // ProRes 4444, 10-bit, with alpha when asked
	VP9        = "vp9"
)

// VideoConfig is what CreateVideo writes.
type VideoConfig struct {
	// Container is MP4, MOV or WebM; empty takes it from the path's
	// extension: .mp4 and .m4v, .mov or .webm.
	Container string
	Video     VideoStream
	// Audio, when not nil, adds an audio track of the samples WriteAudio
	// takes; nil writes none.
	Audio *AudioStream
}

// VideoStream is the video track.
type VideoStream struct {
	// Codec is H264, HEVC, ProRes, ProRes4444 or VP9; empty is H264, or
	// VP9 in WebM.
	Codec string
	// Width and Height are the frames' size in pixels, both even.
	Width, Height int
	// RateNum/RateDen is the frame rate, such as 30000/1001; a zero
	// RateDen is 1.
	RateNum, RateDen int
	// Bitrate, when positive, is the target bit rate in bit/s, and
	// Quality is ignored.
	Bitrate int
	// Quality is a constant quality from 1 (smallest) to 100 (best),
	// like a CRF; 0 with no Bitrate is 80. Encoders without a
	// constant-quality mode take a bit rate estimated from it. ProRes
	// ignores both, as its profile sets its rate.
	Quality int
	// Alpha keeps the frames' alpha, which ProRes4444 and VP9 carry.
	Alpha bool
	// Encoder, when not empty, names the FFmpeg encoder to use, such as
	// "h264_videotoolbox" or "libvpx-vp9", instead of trying hardware
	// encoders first.
	Encoder string
}

// AudioStream is the audio track: AAC in MP4 and MOV, Opus in WebM.
type AudioStream struct {
	// SampleRate is the rate of the samples WriteAudio takes, such as
	// 48000. A rate the codec does not take is resampled to 48 kHz.
	SampleRate int
	// Channels is the samples' channel count, 1 to 8, in FFmpeg's default
	// layout for it.
	Channels int
	// Bitrate is the bit rate in bit/s; 0 is 96 kbit/s a channel for AAC
	// and 64 for Opus.
	Bitrate int
}
