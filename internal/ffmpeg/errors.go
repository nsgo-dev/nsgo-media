package ffmpeg

import (
	"errors"
	"fmt"
	"runtime"
)

// Error is a negative error code an FFmpeg function returned.
type Error int32

func (e Error) Error() string {
	var buf [256]byte
	if avStrerror == nil || avStrerror(int32(e), &buf[0], uintptr(len(buf))) < 0 {
		return fmt.Sprintf("ffmpeg error %d", int32(e))
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return "ffmpeg: " + string(buf[:n])
}

func tag(a, b, c, d byte) Error {
	return Error(-int32(uint32(a) | uint32(b)<<8 | uint32(c)<<16 | uint32(d)<<24))
}

var (
	// ErrEOF is AVERROR_EOF: the input or a decoder has nothing more.
	ErrEOF = tag('E', 'O', 'F', ' ')
	// ErrExit is AVERROR_EXIT: an interrupt callback stopped a call.
	ErrExit = tag('E', 'X', 'I', 'T')
	// ErrAgain is AVERROR(EAGAIN): a decoder needs input or output first.
	ErrAgain = Error(-eagain())
	// ErrInvalidData is AVERROR_INVALIDDATA.
	ErrInvalidData = tag('I', 'N', 'D', 'A')
)

// eagain is EAGAIN in the C library FFmpeg was built against.
func eagain() int32 {
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		return 35
	}
	return 11
}

func check(code int32) error {
	if code >= 0 {
		return nil
	}
	return Error(code)
}

// IsEOF reports err being ErrEOF.
func IsEOF(err error) bool { return errors.Is(err, ErrEOF) }

// IsAgain reports err being ErrAgain.
func IsAgain(err error) bool { return errors.Is(err, ErrAgain) }
