package pipeline

import "time"

// Status is what a tick found.
type Status struct {
	// Frame is a new frame to show, nil when the shown one stays.
	Frame *Frame
	// Position is the playing position from the start.
	Position  time.Duration
	Buffering bool
	Ended     bool
}

// Tick runs the presenter for a display frame: it starts playback once
// the first frame and enough sound are ready, picks the newest frame the
// clock has reached, dropping late ones, notices buffering and the end,
// and starts over at the end when looping. The player calls it on the
// engine's goroutine at every display frame.
func (p *Pipeline) Tick() Status {
	var st Status
	if p.in == nil || p.stopped() {
		return st
	}
	gen := p.genA.Load()
	// The frame shown before the last one went out in a commit that ran,
	// so its pixels may be written again.
	if p.retired != nil {
		p.pool.put(p.retired)
		p.retired = nil
	}
	hasVideo := p.videoStream >= 0
	p.mu.Lock()
	hasAudio := p.audioStream >= 0
	paused, demuxEOF, loop := p.paused, p.demuxEOF, p.loop
	p.mu.Unlock()

	queued, firstAudio, haveAudio, audioEOF, starved := p.ring.status()
	p.frames.mu.Lock()
	fq := p.frames.frames
	// Frames of an older generation are dropped wherever they are met.
	for len(fq) > 0 && fq[0].gen != gen {
		p.pool.put(fq[0])
		fq = fq[1:]
	}
	videoEOF := p.frames.eof == gen
	p.frames.frames = fq
	p.frames.mu.Unlock()

	videoReady := !hasVideo || len(fq) > 0 || videoEOF
	audioReady := !hasAudio || queued >= 100*time.Millisecond || audioEOF

	if p.waitFirst {
		if !videoReady || !audioReady {
			st.Position = p.Position()
			return st
		}
		start := p.target
		if !p.exact {
			switch {
			case len(fq) > 0:
				start = fq[0].PTS
			case haveAudio:
				start = firstAudio
			}
		}
		if len(fq) > 0 {
			st.Frame = p.take()
		}
		p.waitFirst = false
		p.begin(start, paused)
	} else if p.stepReq {
		if len(fq) > 0 {
			st.Frame = p.take()
			p.stepReq = false
			p.begin(st.Frame.PTS, true)
		}
	}

	pos := p.clock(hasAudio)
	if !p.waitFirst && !paused {
		// Buffering: playback ran out of data the demuxer has yet to read.
		if p.running && !demuxEOF {
			out := hasAudio && starved && queued == 0 || hasVideo && !hasAudio && len(p.frames.snapshot()) == 0 && !videoEOF
			if out {
				p.running = false
				p.ring.stop()
				p.wall.setRunning(false, p.Rate())
				if !p.buffering {
					p.buffering = true
					p.emit(Event{Kind: EventBuffering, On: true})
				}
			}
		}
		if !p.running && p.buffering {
			if p.enoughQueued() || demuxEOF {
				p.buffering = false
				p.emit(Event{Kind: EventBuffering, On: false})
				p.begin(pos, false)
			}
		}
	}

	if st.Frame == nil && p.running && !paused {
		st.Frame = p.due(pos)
	}
	p.lastPos = pos
	st.Position = p.Position()
	st.Buffering = p.buffering

	// The end: everything decoded has played.
	if !p.waitFirst && demuxEOF {
		p.frames.mu.Lock()
		videoDone := !hasVideo || p.frames.eof == gen && len(p.frames.frames) == 0
		p.frames.mu.Unlock()
		audioDone := !hasAudio || p.ring.drained(p.out.Played())
		if videoDone && audioDone && !p.ended {
			if loop {
				p.Seek(0, false)
				return st
			}
			p.mu.Lock()
			p.ended = true
			p.mu.Unlock()
			p.running = false
			p.wall.setRunning(false, p.Rate())
			p.emit(Event{Kind: EventEnded})
		}
	}
	p.mu.Lock()
	st.Ended = p.ended
	p.mu.Unlock()
	return st
}

// begin starts the clock at t: running unless paused, and with audio
// dropping what precedes t.
func (p *Pipeline) begin(t time.Duration, paused bool) {
	p.running = true
	p.wall.set(t)
	p.wall.setRunning(!paused, p.Rate())
	p.ring.start(t)
	p.lastPos = t
}

func (p *Pipeline) clock(hasAudio bool) time.Duration {
	if p.waitFirst {
		return p.target
	}
	if hasAudio {
		return p.ring.position(p.out.Played())
	}
	return p.wall.now()
}

// take removes the first queued frame and shows it.
func (p *Pipeline) take() *Frame {
	p.frames.mu.Lock()
	f := p.frames.frames[0]
	p.frames.frames = p.frames.frames[1:]
	p.frames.mu.Unlock()
	p.show(f)
	return f
}

// due shows the newest queued frame at or before pos, dropping the ones
// before it.
func (p *Pipeline) due(pos time.Duration) *Frame {
	const early = 2 * time.Millisecond
	p.frames.mu.Lock()
	var f *Frame
	for len(p.frames.frames) > 0 && p.frames.frames[0].PTS <= pos+early {
		if f != nil {
			p.pool.put(f)
		}
		f = p.frames.frames[0]
		p.frames.frames = p.frames.frames[1:]
	}
	p.frames.mu.Unlock()
	if f != nil {
		p.show(f)
	}
	return f
}

func (p *Pipeline) show(f *Frame) {
	if p.shown != nil {
		p.retired = p.shown
	}
	p.shown = f
	p.pool.wakeAll()
}

// Shown is the frame showing, nil before the first.
func (p *Pipeline) Shown() *Frame { return p.shown }

// enoughQueued reports the packet queues holding enough to resume after
// buffering.
func (p *Pipeline) enoughQueued() bool {
	ok := true
	if p.videoStream >= 0 {
		_, _, d := p.vq.stats()
		ok = ok && d >= resumeTime
	}
	if p.AudioTrack() >= 0 {
		_, _, d := p.aq.stats()
		ok = ok && d >= resumeTime
	}
	return ok
}

func (q *frameQueue) snapshot() []*Frame {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.frames
}
