package mediafile

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

// DecodeAudio decodes the first audio track of the file at path into
// float32 samples interleaved by channel, at rate with channels in
// FFmpeg's default layout for that count, resampling and remixing as
// needed.
func DecodeAudio(path string, rate, channels int) ([]float32, error) {
	if rate <= 0 || channels < 1 || channels > 8 {
		return nil, fmt.Errorf("mediafile: decode to %d Hz and %d channels; give a rate and 1 to 8 channels", rate, channels)
	}
	in, err := ffmpeg.Open(path, nil, new(atomic.Bool))
	if err != nil {
		return nil, err
	}
	defer in.Close()
	var st *ffmpeg.Stream
	for _, s := range in.Format.Streams() {
		if s.Params().Type() == ffmpeg.MediaAudio {
			st = s
			break
		}
	}
	if st == nil {
		return nil, errors.New("mediafile: the file has no audio track")
	}
	dec, err := ffmpeg.NewDecoder(st.Params(), st.TimeBase(), 0, "", 0)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	res := ffmpeg.NewResampler(rate, channels)
	defer res.Close()
	pkt, frame := ffmpeg.NewPacket(), ffmpeg.NewFrame()
	if pkt == nil || frame == nil {
		return nil, errors.New("mediafile: out of memory")
	}
	defer pkt.Free()
	defer frame.Free()
	var out []float32
	receive := func() error {
		for {
			err := dec.Receive(frame)
			if ffmpeg.IsAgain(err) || ffmpeg.IsEOF(err) {
				return nil
			}
			if err != nil {
				return err
			}
			samples, err := res.Convert(frame)
			frame.Unref()
			if err != nil {
				return err
			}
			out = append(out, samples...)
		}
	}
	for {
		err := in.Format.ReadPacket(pkt)
		if ffmpeg.IsEOF(err) {
			break
		}
		if err != nil {
			return nil, err
		}
		if pkt.StreamIndex() != st.Index() {
			pkt.Unref()
			continue
		}
		for {
			err = dec.Send(pkt)
			if !ffmpeg.IsAgain(err) {
				break
			}
			// The decoder gives out frames before it takes more.
			if err := receive(); err != nil {
				return nil, err
			}
		}
		pkt.Unref()
		// A damaged packet is skipped, as a player skips it.
		if err != nil && !errors.Is(err, ffmpeg.ErrInvalidData) {
			return nil, err
		}
		if err := receive(); err != nil {
			return nil, err
		}
	}
	if err := dec.Send(nil); err != nil && !ffmpeg.IsEOF(err) {
		return nil, err
	}
	if err := receive(); err != nil {
		return nil, err
	}
	rest, err := res.Flush()
	if err != nil {
		return nil, err
	}
	return append(out, rest...), nil
}
