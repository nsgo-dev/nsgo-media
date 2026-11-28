package ffmpeg

import (
	"errors"
	"fmt"
	"unsafe"
)

// Resampler converts decoded audio to interleaved float32 at a rate and
// channel count, rebuilding its context when the source's format changes.
type Resampler struct {
	ctx                   unsafe.Pointer
	inFmt, inRate, inChan int32
	outRate, outChan      int32
	flt                   int32
	buf                   unsafe.Pointer
	bufSamples            int
}

// NewResampler converts to rate and channels.
func NewResampler(rate, channels int) *Resampler {
	return &Resampler{outRate: int32(rate), outChan: int32(channels)}
}

// Convert converts f and returns the samples, interleaved, valid until the
// next call.
func (r *Resampler) Convert(f *Frame) ([]float32, error) {
	if r.flt == 0 {
		r.flt = avGetSampleFmt("flt")
	}
	inFmt, inRate, inChan := f.Format(), int32(f.SampleRate()), int32(f.Channels())
	if r.ctx == nil || inFmt != r.inFmt || inRate != r.inRate || inChan != r.inChan {
		if r.ctx != nil {
			swrFree(&r.ctx)
		}
		var out [sizeAVChannelLayout]byte
		avChannelLayoutDefault(unsafe.Pointer(&out[0]), r.outChan)
		if err := check(swrAllocSetOpts2(&r.ctx, unsafe.Pointer(&out[0]), r.flt, r.outRate, f.layout(), inFmt, inRate, 0, nil)); err != nil {
			return nil, err
		}
		if err := check(swrInit(r.ctx)); err != nil {
			swrFree(&r.ctx)
			return nil, fmt.Errorf("ffmpeg: resampler: %w", err)
		}
		r.inFmt, r.inRate, r.inChan = inFmt, inRate, inChan
	}
	n := f.NbSamples()
	maxOut := int(int64(n)*int64(r.outRate)/int64(max(inRate, 1))) + 256
	if maxOut > r.bufSamples {
		if r.buf != nil {
			avFree(r.buf)
		}
		r.buf = avMalloc(uintptr(maxOut * int(r.outChan) * 4))
		r.bufSamples = maxOut
	}
	out := r.buf
	got := swrConvert(r.ctx, &out, int32(maxOut), f.extendedData(), int32(n))
	if got < 0 {
		return nil, Error(got)
	}
	return unsafe.Slice((*float32)(r.buf), int(got)*int(r.outChan)), nil
}

// Flush returns the samples the resampler still holds, valid until the
// next call.
func (r *Resampler) Flush() ([]float32, error) {
	if r.ctx == nil {
		return nil, nil
	}
	maxOut := int(swrGetDelay(r.ctx, int64(r.outRate))) + 256
	if maxOut > r.bufSamples {
		if r.buf != nil {
			avFree(r.buf)
		}
		r.buf = avMalloc(uintptr(maxOut * int(r.outChan) * 4))
		r.bufSamples = maxOut
	}
	out := r.buf
	got := swrConvert(r.ctx, &out, int32(maxOut), nil, 0)
	if got < 0 {
		return nil, Error(got)
	}
	return unsafe.Slice((*float32)(r.buf), int(got)*int(r.outChan)), nil
}

// Reset drops what the resampler holds, as after a seek.
func (r *Resampler) Reset() {
	if r.ctx != nil {
		swrFree(&r.ctx)
	}
}

// Close frees the resampler.
func (r *Resampler) Close() {
	r.Reset()
	if r.buf != nil {
		avFree(r.buf)
		r.buf = nil
	}
}

// RateConverter changes interleaved float32 audio's sample rate.
type RateConverter struct {
	ctx        unsafe.Pointer
	channels   int
	ratio      float64
	buf        unsafe.Pointer
	bufSamples int
}

// NewRateConverter converts channels of audio from inRate to outRate.
func NewRateConverter(inRate, outRate, channels int) (*RateConverter, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	r := &RateConverter{channels: channels, ratio: float64(outRate) / float64(inRate)}
	var layout [sizeAVChannelLayout]byte
	avChannelLayoutDefault(unsafe.Pointer(&layout[0]), int32(channels))
	flt := avGetSampleFmt("flt")
	l := unsafe.Pointer(&layout[0])
	if err := check(swrAllocSetOpts2(&r.ctx, l, flt, int32(outRate), l, flt, int32(inRate), 0, nil)); err != nil {
		return nil, err
	}
	if err := check(swrInit(r.ctx)); err != nil {
		swrFree(&r.ctx)
		return nil, fmt.Errorf("ffmpeg: resampler: %w", err)
	}
	return r, nil
}

// Convert converts in, interleaved, and returns what the converter gives
// out, valid until the next call; nil in drains what it holds.
func (r *RateConverter) Convert(in []float32) ([]float32, error) {
	n := len(in) / r.channels
	maxOut := int(float64(n)*r.ratio) + 256
	if maxOut > r.bufSamples {
		if r.buf != nil {
			avFree(r.buf)
		}
		r.buf = avMalloc(uintptr(maxOut * r.channels * 4))
		if r.buf == nil {
			r.bufSamples = 0
			return nil, errors.New("ffmpeg: out of memory")
		}
		r.bufSamples = maxOut
	}
	out := r.buf
	var src *unsafe.Pointer
	var plane unsafe.Pointer
	if n > 0 {
		plane = unsafe.Pointer(&in[0])
		src = &plane
	}
	got := swrConvert(r.ctx, &out, int32(maxOut), src, int32(n))
	keep(in)
	if got < 0 {
		return nil, Error(got)
	}
	return unsafe.Slice((*float32)(r.buf), int(got)*r.channels), nil
}

// Close frees the converter.
func (r *RateConverter) Close() {
	if r.ctx != nil {
		swrFree(&r.ctx)
	}
	if r.buf != nil {
		avFree(r.buf)
		r.buf = nil
	}
}

// Tempo changes interleaved float32 audio's speed while keeping its pitch,
// through FFmpeg's atempo filter.
type Tempo struct {
	graph, src, sink unsafe.Pointer
	rate, channels   int
	in, out          *Frame
	flt              int32
	pts              int64
}

// NewTempo makes a filter speeding audio of rate and channels up by
// factor, from 0.25 to 4.
func NewTempo(rate, channels int, factor float64) (*Tempo, error) {
	if err := Load(); err != nil {
		return nil, err
	}
	t := &Tempo{rate: rate, channels: channels, flt: avGetSampleFmt("flt")}
	t.graph = avfilterGraphAlloc()
	if t.graph == nil {
		return nil, errors.New("ffmpeg: out of memory")
	}
	layout := "stereo"
	if channels == 1 {
		layout = "mono"
	} else if channels != 2 {
		layout = fmt.Sprintf("%dc", channels)
	}
	srcArgs := fmt.Sprintf("sample_rate=%d:sample_fmt=flt:channel_layout=%s:time_base=1/%d", rate, layout, rate)
	if err := check(avfilterGraphCreateFilt(&t.src, avfilterGetByName("abuffer"), "in", srcArgs, nil, t.graph)); err != nil {
		t.Close()
		return nil, fmt.Errorf("ffmpeg: abuffer: %w", err)
	}
	if err := check(avfilterGraphCreateFilt(&t.sink, avfilterGetByName("abuffersink"), "out", "", nil, t.graph)); err != nil {
		t.Close()
		return nil, fmt.Errorf("ffmpeg: abuffersink: %w", err)
	}
	// atempo takes 0.5 to 100 in one instance, so slower rates chain two.
	var factors []float64
	switch {
	case factor < 0.5:
		factors = []float64{0.5, factor / 0.5}
	default:
		factors = []float64{factor}
	}
	prev := t.src
	for i, f := range factors {
		var ctx unsafe.Pointer
		if err := check(avfilterGraphCreateFilt(&ctx, avfilterGetByName("atempo"), fmt.Sprintf("tempo%d", i), fmt.Sprintf("tempo=%f", f), nil, t.graph)); err != nil {
			t.Close()
			return nil, fmt.Errorf("ffmpeg: atempo: %w", err)
		}
		if err := check(avfilterLink(prev, 0, ctx, 0)); err != nil {
			t.Close()
			return nil, err
		}
		prev = ctx
	}
	if err := check(avfilterLink(prev, 0, t.sink, 0)); err != nil {
		t.Close()
		return nil, err
	}
	if err := check(avfilterGraphConfig(t.graph, nil)); err != nil {
		t.Close()
		return nil, fmt.Errorf("ffmpeg: audio filter: %w", err)
	}
	t.in, t.out = NewFrame(), NewFrame()
	return t, nil
}

// Process feeds samples in and appends what the filter gives out to dst.
func (t *Tempo) Process(samples []float32, dst []float32) ([]float32, error) {
	n := len(samples) / t.channels
	if n > 0 {
		f := t.in.p()
		*(*int32)(at(f, offAVFrameFormat)) = t.flt
		*(*int32)(at(f, offAVFrameSampleRate)) = int32(t.rate)
		*(*int32)(at(f, offAVFrameNbSamples)) = int32(n)
		*(*int64)(at(f, offAVFramePts)) = t.pts
		t.pts += int64(n)
		avChannelLayoutDefault(t.in.layout(), int32(t.channels))
		if err := check(avFrameGetBuffer(t.in, 0)); err != nil {
			return dst, err
		}
		data, _ := t.in.Plane(0)
		copy(unsafe.Slice((*float32)(data), len(samples)), samples)
		if err := check(avBuffersrcAddFrame(t.src, t.in, 0)); err != nil {
			t.in.Unref()
			return dst, err
		}
	}
	for {
		err := check(avBuffersinkGetFrame(t.sink, t.out))
		if IsAgain(err) || IsEOF(err) {
			return dst, nil
		}
		if err != nil {
			return dst, err
		}
		data, _ := t.out.Plane(0)
		dst = append(dst, unsafe.Slice((*float32)(data), t.out.NbSamples()*t.channels)...)
		t.out.Unref()
	}
}

// Close frees the filter.
func (t *Tempo) Close() {
	if t.graph != nil {
		avfilterGraphFree(&t.graph)
	}
	if t.in != nil {
		t.in.Free()
		t.in = nil
	}
	if t.out != nil {
		t.out.Free()
		t.out = nil
	}
}
