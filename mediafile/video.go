package mediafile

import (
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

// ErrClosed is what a VideoWriter's methods return after Close or Abort.
var ErrClosed = errors.New("mediafile: the video writer is closed")

// VideoWriter writes a video file. Its frames and samples go in through
// WriteVideo and WriteAudio, which are safe to call from more than one
// goroutine but are ordered as they are called. The file is written beside
// its path under a temporary name, and Close moves it to the path when it
// is finished; Abort removes it, so no unfinished file is left at the path.
type VideoWriter struct {
	mu        sync.Mutex
	path, tmp string
	mux       *ffmpeg.Muxer
	video     *videoTrack
	audio     *audioTrack
	err       error // the first error, after which nothing is written
	closed    bool
}

// CreateVideo starts writing the video file cfg describes at path.
func CreateVideo(path string, cfg VideoConfig) (*VideoWriter, error) {
	container, err := containerOf(path, cfg.Container)
	if err != nil {
		return nil, err
	}
	v := cfg.Video
	if v.Codec == "" {
		v.Codec = H264
		if container == WebM {
			v.Codec = VP9
		}
	}
	if err := checkVideo(container, v); err != nil {
		return nil, err
	}
	if a := cfg.Audio; a != nil && (a.SampleRate <= 0 || a.Channels < 1 || a.Channels > 8) {
		return nil, fmt.Errorf("mediafile: audio of %d Hz and %d channels; give a rate and 1 to 8 channels", a.SampleRate, a.Channels)
	}
	if err := ffmpeg.Load(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(abs), "."+filepath.Base(abs)+".*.part")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	f.Close()
	w := &VideoWriter{path: abs, tmp: tmp}
	fail := func(err error) (*VideoWriter, error) {
		w.release()
		os.Remove(tmp)
		return nil, err
	}
	if w.mux, err = ffmpeg.NewMuxer("file:"+tmp, container); err != nil {
		return fail(err)
	}
	if w.video, err = newVideoTrack(w.mux, v); err != nil {
		return fail(err)
	}
	if cfg.Audio != nil {
		if w.audio, err = newAudioTrack(w.mux, container, *cfg.Audio); err != nil {
			return fail(err)
		}
	}
	var opts map[string]string
	if container == MP4 {
		// The index goes before the media, so players start before the
		// whole file is read.
		opts = map[string]string{"movflags": "+faststart"}
	}
	if err := w.mux.Start(opts); err != nil {
		return fail(err)
	}
	return w, nil
}

func containerOf(path, container string) (string, error) {
	if container != "" {
		switch c := strings.ToLower(container); c {
		case MP4, MOV, WebM:
			return c, nil
		}
		return "", fmt.Errorf("mediafile: unknown container %q; use mp4, mov or webm", container)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v":
		return MP4, nil
	case ".mov":
		return MOV, nil
	case ".webm":
		return WebM, nil
	}
	return "", fmt.Errorf("mediafile: no container for %q; name it .mp4, .m4v, .mov or .webm, or set Container", filepath.Base(path))
}

// containerCodecs are the video codecs each container takes.
var containerCodecs = map[string][]string{
	MP4:  {H264, HEVC},
	MOV:  {H264, HEVC, ProRes, ProRes4444},
	WebM: {VP9},
}

func checkVideo(container string, v VideoStream) error {
	if !slices.Contains(containerCodecs[container], v.Codec) {
		return fmt.Errorf("mediafile: %s does not take %q video; it takes %s", container, v.Codec, strings.Join(containerCodecs[container], ", "))
	}
	if v.Alpha && v.Codec != ProRes4444 && v.Codec != VP9 {
		return fmt.Errorf("mediafile: %s keeps no alpha; use prores4444 or vp9", v.Codec)
	}
	if v.Width <= 0 || v.Height <= 0 || v.Width%2 != 0 || v.Height%2 != 0 {
		return fmt.Errorf("mediafile: size %dx%d must be positive and even", v.Width, v.Height)
	}
	if v.RateNum <= 0 || v.RateDen < 0 || v.RateNum > math.MaxInt32 || v.RateDen > math.MaxInt32 {
		return fmt.Errorf("mediafile: frame rate %d/%d must be positive", v.RateNum, v.RateDen)
	}
	if v.Quality < 0 || v.Quality > 100 || v.Bitrate < 0 {
		return fmt.Errorf("mediafile: quality %d must be 1 to 100, and bit rate %d not negative", v.Quality, v.Bitrate)
	}
	return nil
}

// Encoder is the FFmpeg encoder writing the video, such as
// "h264_videotoolbox".
func (w *VideoWriter) Encoder() string { return w.video.spec.name }

// Hardware reports the video encoder running on hardware.
func (w *VideoWriter) Hardware() bool { return w.video.spec.hardware }

// WriteVideo encodes the next frame, which is the stream's size:
// *image.RGBA is premultiplied and *image.NRGBA straight alpha, as Go's
// image package has them; any other image is drawn into an RGBA first.
// Without Alpha, the frame shows as if over black.
func (w *VideoWriter) WriteVideo(img image.Image) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usable(); err != nil {
		return err
	}
	w.err = w.video.write(w.mux, img)
	return w.err
}

// WriteAudio encodes samples, interleaved by channel, as the next of the
// audio track's; each call may hold any number of them. It fails when the
// file has no audio track.
func (w *VideoWriter) WriteAudio(samples []float32) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.usable(); err != nil {
		return err
	}
	if w.audio == nil {
		return errors.New("mediafile: the file has no audio track")
	}
	if len(samples)%w.audio.channels != 0 {
		return fmt.Errorf("mediafile: %d samples are not whole frames of %d channels", len(samples), w.audio.channels)
	}
	w.err = w.audio.write(w.mux, samples, false)
	return w.err
}

func (w *VideoWriter) usable() error {
	if w.closed {
		return ErrClosed
	}
	return w.err
}

// Close drains the encoders, writes the file's trailer and moves the file
// to its path. When it or an earlier write failed, it removes the file and
// returns that error.
func (w *VideoWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	err := w.err
	if err == nil {
		err = w.video.flush(w.mux)
	}
	if err == nil && w.audio != nil {
		err = w.audio.write(w.mux, nil, true)
	}
	if err == nil {
		err = w.mux.Finish()
	}
	if cerr := w.release(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(w.tmp, w.path)
	}
	if err != nil {
		os.Remove(w.tmp)
	}
	return err
}

// Abort stops writing without finishing the file and removes it.
func (w *VideoWriter) Abort() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	w.release()
	return os.Remove(w.tmp)
}

// release frees the tracks and closes the muxer's file.
func (w *VideoWriter) release() error {
	w.closed = true
	if w.video != nil {
		w.video.close()
	}
	if w.audio != nil {
		w.audio.close()
	}
	var err error
	if w.mux != nil {
		err = w.mux.Close()
		w.mux = nil
	}
	return err
}
