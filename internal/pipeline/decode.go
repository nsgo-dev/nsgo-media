package pipeline

import (
	"errors"
	"image"
	"math"
	"time"
	"unsafe"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// decodeVideo decodes the video queue, of track stream, into RGBA frames
// for the presenter.
func (p *Pipeline) decodeVideo(dec *ffmpeg.Decoder, stream int) {
	defer p.wg.Done()
	defer func() {
		if dec != nil {
			dec.Close()
		}
	}()
	if dec == nil {
		return
	}
	frame := ffmpeg.NewFrame()
	defer frame.Free()
	var scaler ffmpeg.Scaler
	defer scaler.Close()
	gen := p.genA.Load()
	vdrop := time.Duration(-1)
	var back *backFill
	backDone := false
	failures := 0

	// take moves the decoded picture into a frame of its own, in memory.
	take := func() (*ffmpeg.Frame, error) {
		src := ffmpeg.NewFrame()
		if frame.Hardware() {
			err := frame.TransferTo(src)
			frame.Unref()
			if err != nil {
				src.Free()
				return nil, errHardware
			}
			return src, nil
		}
		frame.MoveTo(src)
		return src, nil
	}

	// fill keeps a frame of a history fill, ending it at the shown frame.
	fill := func(pts time.Duration) error {
		if backDone || pts < back.from {
			frame.Unref()
			return nil
		}
		if pts <= back.end {
			src, err := take()
			if err != nil {
				return err
			}
			if !p.hist.push(back.id, pts, src) {
				src.Free()
			}
		} else {
			frame.Unref()
		}
		if pts >= back.end {
			p.endFill(back, gen)
			backDone = true
		}
		return nil
	}

	// receive converts and queues each frame the decoder has ready.
	receive := func() error {
		for {
			err := dec.Receive(frame)
			if ffmpeg.IsAgain(err) || ffmpeg.IsEOF(err) {
				return nil
			}
			if err != nil {
				return err
			}
			ts := frame.BestEffortTimestamp()
			if ts == ffmpeg.NoPTS {
				ts = frame.PTS()
			}
			pts := p.vp.Load().tb.Duration(ts)
			if back != nil {
				if err := fill(pts); err != nil {
					return err
				}
				continue
			}
			if vdrop >= 0 && pts < vdrop {
				frame.Unref()
				continue
			}
			src, err := take()
			if err != nil {
				return err
			}
			out := p.convert(&scaler, src, pts, gen)
			if out == nil {
				src.Free()
				return nil // closed, or a seek made the frame stale
			}
			out.src = src
			failures = 0
			p.frames.push(out)
		}
	}

	for {
		it, ok := p.vq.get()
		if !ok {
			return
		}
		if it.flush {
			// Another variant plays from the seek on.
			if p.reopenVideo.Swap(false) {
				if d, err := p.openVideoDecoder(dec.HWDevice != ""); err == nil {
					dec.Close()
					dec, stream = d, p.video()
					scaler.Close()
					scaler = ffmpeg.Scaler{}
				}
			}
			dec.Flush()
			gen, vdrop, back, backDone = it.gen, it.vdrop, it.back, false
			continue
		}
		// A packet of a track the decoder is not for came in as the
		// variant changed.
		if it.gen != gen || backDone || it.pkt != nil && it.pkt.StreamIndex() != stream {
			freeItem(it)
			continue
		}
		var err error
		if it.eof {
			// Draining gives the frames held back for reordering.
			if err = dec.Send(nil); err == nil {
				err = receive()
			}
			dec.Flush()
			if back == nil {
				p.frames.setEOF(gen)
			} else if !backDone {
				p.endFill(back, gen)
				backDone = true
			}
		} else {
			err = dec.Send(it.pkt)
			if ffmpeg.IsAgain(err) {
				err = nil
			}
			if err == nil || errors.Is(err, ffmpeg.ErrInvalidData) {
				err = receive()
			}
			freeItem(it)
		}
		if err == nil || errors.Is(err, ffmpeg.ErrInvalidData) {
			continue
		}
		// A hardware decoder that fails gives way to software decoding,
		// which starts again from the keyframe before the shown frame.
		if dec.HWDevice != "" && (errors.Is(err, errHardware) || failures > 2) {
			if sw, serr := p.openVideoDecoder(false); serr == nil {
				dec.Close()
				dec = sw
				p.emit(Event{Kind: EventHardware, Device: ""})
				p.seekForFallback()
				continue
			}
		}
		failures++
	}
}

// endFill ends a history fill and stops the demuxers reading for it.
func (p *Pipeline) endFill(back *backFill, gen uint64) {
	p.hist.finish(back.id)
	p.mu.Lock()
	if p.gen == gen {
		for _, d := range p.inputs {
			d.hold = true
		}
	}
	p.mu.Unlock()
}

var errHardware = errors.New("pipeline: hardware frame transfer failed")

func (p *Pipeline) frameDuration() time.Duration { return p.frameDurationOf(p.video()) }

func (p *Pipeline) frameDurationOf(id int) time.Duration {
	if fr := p.track(id).FrameRate; fr > 0 {
		return time.Duration(float64(time.Second) / fr)
	}
	return 40 * time.Millisecond
}

// seekForFallback restarts decoding at the shown position after the
// decoder changed.
func (p *Pipeline) seekForFallback() {
	p.mu.Lock()
	t := p.lastPos
	p.gen++
	gen := p.gen
	p.genA.Store(gen)
	p.requestSeek(&seekRequest{target: t, exact: true, vdrop: t - p.frameDurationOf(p.videoStream)/2, gen: gen})
	p.mu.Unlock()
	p.ring.flush(gen, t)
}

// convert scales and rotates a decoded picture into a pool frame, at the
// output size: the displayed size, shrunk to cover the view's.
func (p *Pipeline) convert(s *ffmpeg.Scaler, f *ffmpeg.Frame, pts time.Duration, gen uint64) *Frame {
	ow, oh := p.outputSize(f.Width(), f.Height())
	rot := p.vp.Load().rot
	// The converter writes the unrotated picture.
	cw, ch := turned(ow, oh, rot)
	stale := func() bool { return p.genA.Load() != gen || p.stopped() }
	out := p.pool.get(ow, oh, stale)
	if out == nil {
		return nil
	}
	if stale() {
		p.pool.put(out)
		return nil
	}
	dst := out
	if rot != 0 {
		if dst = p.pool.get(cw, ch, stale); dst == nil {
			p.pool.put(out)
			return nil
		}
	}
	if err := s.Scale(f, dst.mem, cw*4, cw, ch); err != nil {
		p.pool.put(out)
		if dst != out {
			p.pool.put(dst)
		}
		return nil
	}
	if rot != 0 {
		rotate(out.Image.Pix, dst.Image.Pix, cw, ch, rot)
		p.pool.put(dst)
	}
	out.PTS, out.gen = pts, gen
	return out
}

// displayed is the shape a w×h picture shows at: its pixels' aspect
// ratio, then its rotation.
func (p *Pipeline) displayed(w, h int) (dw, dh float64) {
	vp := p.vp.Load()
	dw, dh = float64(w)*vp.sar, float64(h)
	if vp.sar <= 0 {
		dw = float64(w)
	}
	if vp.rot == 90 || vp.rot == 270 {
		dw, dh = dh, dw
	}
	return dw, dh
}

// outputSize is the size a w×h picture converts to for display: its
// displayed size, shrunk to cover the largest view.
func (p *Pipeline) outputSize(w, h int) (ow, oh int) {
	dw, dh := p.displayed(w, h)
	p.mu.Lock()
	vw, vh := p.outW, p.outH
	p.mu.Unlock()
	scale := 1.0
	if vw > 0 && vh > 0 {
		scale = math.Min(1, math.Max(float64(vw)/dw, float64(vh)/dh))
	}
	return max(int(math.Round(dw*scale)), 2), max(int(math.Round(dh*scale)), 2)
}

// turned is a w×h picture's size before a rotation by deg.
func turned(w, h, deg int) (int, int) {
	if deg == 90 || deg == 270 {
		return h, w
	}
	return w, h
}

// paint converts f to RGBA of ow×oh, rotated, in memory from
// ffmpeg.Alloc the caller frees.
func (p *Pipeline) paint(s *ffmpeg.Scaler, f *ffmpeg.Frame, ow, oh int) (unsafe.Pointer, error) {
	rot := p.vp.Load().rot
	cw, ch := turned(ow, oh, rot)
	out := ffmpeg.Alloc(ow * oh * 4)
	if out == nil {
		return nil, errNoMemory
	}
	dst := out
	if rot != 0 {
		if dst = ffmpeg.Alloc(cw * ch * 4); dst == nil {
			ffmpeg.Free(out)
			return nil, errNoMemory
		}
		defer ffmpeg.Free(dst)
	}
	if err := s.Scale(f, dst, cw*4, cw, ch); err != nil {
		ffmpeg.Free(out)
		return nil, err
	}
	if rot != 0 {
		rotate(unsafe.Slice((*byte)(out), ow*oh*4), unsafe.Slice((*byte)(dst), cw*ch*4), cw, ch, rot)
	}
	return out, nil
}

var errNoMemory = errors.New("pipeline: out of memory")

// newLooseFrame makes a frame outside the pool of mem, w×h RGBA.
func newLooseFrame(mem unsafe.Pointer, w, h int) *Frame {
	size := w * h * 4
	return &Frame{mem: mem, size: size, loose: true, Image: &image.RGBA{
		Pix: unsafe.Slice((*byte)(mem), size), Stride: w * 4, Rect: image.Rect(0, 0, w, h),
	}}
}

// rotate turns src, w×h RGBA, clockwise by deg into dst.
func rotate(dst, src []byte, w, h, deg int) {
	d := u32(dst)
	s := u32(src)
	switch deg {
	case 90: // dst is h×w; src (x, y) goes to (h-1-y, x)
		for y := 0; y < h; y++ {
			row := s[y*w : (y+1)*w]
			for x, v := range row {
				d[x*h+h-1-y] = v
			}
		}
	case 180:
		n := w * h
		for i, v := range s[:n] {
			d[n-1-i] = v
		}
	case 270: // src (x, y) goes to (y, w-1-x)
		for y := 0; y < h; y++ {
			row := s[y*w : (y+1)*w]
			for x, v := range row {
				d[(w-1-x)*h+y] = v
			}
		}
	}
}

func u32(b []byte) []uint32 {
	return unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(b))), len(b)/4)
}

// decodeAudio decodes the audio queue into device samples for the ring.
func (p *Pipeline) decodeAudio(dec *ffmpeg.Decoder) {
	defer p.wg.Done()
	defer func() {
		if dec != nil {
			dec.Close()
		}
	}()
	frame := ffmpeg.NewFrame()
	defer frame.Free()
	res := ffmpeg.NewResampler(p.out.Rate(), p.out.Channels())
	defer res.Close()
	var tempo *ffmpeg.Tempo
	tempoRate := 1.0
	defer func() {
		if tempo != nil {
			tempo.Close()
		}
	}()
	outRate, ch := p.out.Rate(), p.out.Channels()
	gen := p.genA.Load()
	dropBefore := time.Duration(-1)
	var tb ffmpeg.Rational
	streamIdx := -1 // the audio stream's index in its input
	if dec != nil {
		_, s := p.stream(p.AudioTrack())
		tb, streamIdx = s.TimeBase(), s.Index()
	}
	anchored := false
	idle := false          // a history fill needs no sound
	var next time.Duration // the media time the tempo's next output starts at
	var stretched []float32

	process := func() bool {
		for {
			err := dec.Receive(frame)
			if err != nil {
				return true
			}
			ts := frame.BestEffortTimestamp()
			if ts == ffmpeg.NoPTS {
				ts = frame.PTS()
			}
			pts := tb.Duration(ts)
			samples, err := res.Convert(frame)
			frame.Unref()
			if err != nil || len(samples) == 0 {
				continue
			}
			if dropBefore >= 0 {
				skip := int((dropBefore - pts) * time.Duration(outRate) / time.Second)
				if skip > 0 {
					if skip*ch >= len(samples) {
						continue
					}
					samples, pts = samples[skip*ch:], dropBefore
				}
				dropBefore = -1
			}
			rate := p.Rate()
			if rate != tempoRate {
				if tempo != nil {
					tempo.Close()
					tempo = nil
				}
				if rate != 1 {
					tempo, _ = ffmpeg.NewTempo(outRate, ch, rate)
				}
				tempoRate, anchored = rate, false
			}
			perFrame := float64(time.Second) / float64(outRate)
			var buf []float32
			if tempo != nil {
				if !anchored {
					next, anchored = pts, true
				}
				stretched, _ = tempo.Process(samples, stretched[:0])
				buf = append([]float32(nil), stretched...)
				perFrame *= rate
				pts = next
				next += time.Duration(float64(len(buf)/ch) * perFrame)
			} else {
				buf = append([]float32(nil), samples...)
			}
			if len(buf) == 0 {
				continue
			}
			if !p.ring.push(gen, chunk{samples: buf, pts: pts, perFrame: perFrame}) {
				return !p.stopped()
			}
		}
	}

	for {
		it, ok := p.aq.get()
		if !ok {
			return
		}
		if it.flush {
			if p.reopenAudio.Swap(false) {
				if dec != nil {
					dec.Close()
					dec = nil
				}
				if id := p.AudioTrack(); id >= 0 {
					if d, err := p.openAudioDecoder(id); err == nil {
						dec = d
						_, s := p.stream(id)
						tb, streamIdx = s.TimeBase(), s.Index()
					}
				}
			}
			if dec != nil {
				dec.Flush()
			}
			res.Reset()
			if tempo != nil {
				tempo.Close()
				tempo, tempoRate = nil, 1
			}
			gen, anchored, idle = it.gen, false, it.back != nil
			dropBefore = -1
			if it.exact {
				dropBefore = it.target
			}
			continue
		}
		if dec == nil || it.gen != gen || idle || it.pkt != nil && it.pkt.StreamIndex() != streamIdx {
			freeItem(it)
			continue
		}
		if it.eof {
			if dec.Send(nil) == nil {
				process()
			}
			dec.Flush()
			p.ring.setEOF(gen)
			continue
		}
		if err := dec.Send(it.pkt); err == nil || ffmpeg.IsAgain(err) {
			process()
		}
		freeItem(it)
	}
}
