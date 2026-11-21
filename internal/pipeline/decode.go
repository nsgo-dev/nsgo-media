package pipeline

import (
	"errors"
	"math"
	"time"
	"unsafe"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// decodeVideo decodes the video queue into RGBA frames for the presenter.
func (p *Pipeline) decodeVideo(dec *ffmpeg.Decoder) {
	defer p.wg.Done()
	defer func() {
		if dec != nil {
			dec.Close()
		}
	}()
	if dec == nil {
		return
	}
	frame, sw := ffmpeg.NewFrame(), ffmpeg.NewFrame()
	defer frame.Free()
	defer sw.Free()
	var scaler ffmpeg.Scaler
	defer scaler.Close()
	gen := p.genA.Load()
	dropBefore := time.Duration(-1)
	failures := 0

	// emit converts and queues each frame the decoder has ready.
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
			pts := p.videoTB.Duration(ts)
			if dropBefore >= 0 && pts < dropBefore-p.frameDuration()/2 {
				frame.Unref()
				continue
			}
			src := frame
			if frame.Hardware() {
				if err := frame.TransferTo(sw); err != nil {
					frame.Unref()
					return errHardware
				}
				frame.Unref()
				src = sw
			}
			out := p.convert(&scaler, src, pts, gen)
			src.Unref()
			if out == nil {
				return nil // closed, or a seek made the frame stale
			}
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
			dec.Flush()
			gen = it.gen
			dropBefore = -1
			if it.exact {
				dropBefore = it.target
			}
			continue
		}
		if it.gen != gen {
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
			p.frames.setEOF(gen)
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

var errHardware = errors.New("pipeline: hardware frame transfer failed")

func (p *Pipeline) frameDuration() time.Duration {
	if fr := p.track(p.videoStream).FrameRate; fr > 0 {
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
	p.seekReq = &seekRequest{target: t, exact: true, gen: gen}
	p.demuxEOF = false
	p.cond.Broadcast()
	p.mu.Unlock()
	p.interrupt.Store(true)
	p.ring.flush(gen, t)
}

// convert scales and rotates a decoded picture into a pool frame, at the
// output size: the displayed size, shrunk to cover the view's.
func (p *Pipeline) convert(s *ffmpeg.Scaler, f *ffmpeg.Frame, pts time.Duration, gen uint64) *Frame {
	w, h := f.Width(), f.Height()
	// The displayed shape: pixel aspect ratio, then rotation.
	dw, dh := float64(w)*p.videoSAR, float64(h)
	if p.videoSAR <= 0 {
		dw = float64(w)
	}
	rot := p.videoRot
	if rot == 90 || rot == 270 {
		dw, dh = dh, dw
	}
	p.mu.Lock()
	vw, vh := p.outW, p.outH
	p.mu.Unlock()
	scale := 1.0
	if vw > 0 && vh > 0 {
		scale = math.Min(1, math.Max(float64(vw)/dw, float64(vh)/dh))
	}
	ow, oh := max(int(math.Round(dw*scale)), 2), max(int(math.Round(dh*scale)), 2)
	// The converter writes the unrotated picture.
	cw, ch := ow, oh
	if rot == 90 || rot == 270 {
		cw, ch = oh, ow
	}
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
	if dec != nil {
		tb = p.in.Format.Streams()[p.AudioTrack()].TimeBase()
	}
	anchored := false
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
						tb = p.in.Format.Streams()[id].TimeBase()
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
			gen, anchored = it.gen, false
			dropBefore = -1
			if it.exact {
				dropBefore = it.target
			}
			continue
		}
		if dec == nil || it.gen != gen {
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
