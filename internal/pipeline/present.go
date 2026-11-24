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
	if p.adaptive != nil {
		p.adapt(time.Now())
	}
	gen := p.genA.Load()
	// The frame shown before the last one went out in a commit that ran,
	// so its pixels may be written again.
	if p.retired != nil {
		p.release(p.retired)
		p.retired = nil
	}
	p.mu.Lock()
	// A picture not decoded holds back neither the start nor the end.
	hasVideo := p.videoStream >= 0 && !p.audioOnly.Load()
	hasAudio := p.audioStream >= 0
	paused, demuxEOF, loop := p.paused, p.demuxEOF(), p.loop
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
	} else if p.steps != 0 && paused {
		st.Frame = p.step(videoEOF)
	}

	pos := p.clock(hasAudio, paused)
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
		audioDone := !hasAudio || p.ring.drained(p.output().Played())
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
	p.smooth.valid = false
	p.wall.set(t)
	p.wall.setRunning(!paused, p.Rate())
	p.ring.start(t)
	p.lastPos = t
}

func (p *Pipeline) clock(hasAudio, paused bool) time.Duration {
	if p.waitFirst {
		return p.target
	}
	if paused {
		// Paused, the clock stays where it stopped or a step put it.
		p.smooth.valid = false
		return p.lastPos
	}
	if !hasAudio {
		return p.wall.now()
	}
	// Devices tell their played position in steps of a buffer or a poll.
	// Between steps the clock runs on at the rate, and it eases toward
	// the device's position, jumping to it only when far off.
	a := p.ring.position(p.output().Played())
	now := time.Now()
	if !p.smooth.valid || !p.running || p.Paused() {
		p.smooth = smoothClock{pos: a, at: now, valid: true}
		return a
	}
	pred := p.smooth.pos + time.Duration(float64(now.Sub(p.smooth.at))*p.Rate())
	if diff := a - pred; diff > maxClockDrift || diff < -maxClockDrift {
		pred = a
	} else {
		pred += diff / 8
	}
	p.smooth = smoothClock{pos: pred, at: now, valid: true}
	return pred
}

// smoothClock is the clock's last reading, which the next extends.
type smoothClock struct {
	pos   time.Duration
	at    time.Time
	valid bool
}

const maxClockDrift = 40 * time.Millisecond

// take removes the first queued frame and shows it.
func (p *Pipeline) take() *Frame {
	p.frames.mu.Lock()
	f := p.frames.frames[0]
	p.frames.frames = p.frames.frames[1:]
	p.frames.mu.Unlock()
	p.show(f)
	return f
}

// due shows the newest queued frame due by pos, dropping the ones before
// it.
func (p *Pipeline) due(pos time.Duration) *Frame {
	// A frame shows a little before its time. When the display's frames
	// fall at the frames' boundaries, jitter makes two frames due in one
	// display frame and none in the next, so each such skip moves the
	// lead by a few milliseconds, around a frame, until the phases part.
	early := p.lead
	p.frames.mu.Lock()
	var f *Frame
	for len(p.frames.frames) > 0 && p.frames.frames[0].PTS <= pos+early {
		if f != nil {
			p.release(f)
			p.dropped++
			if p.halfFrame > 0 {
				p.lead = (p.lead + 3*time.Millisecond) % (2 * p.halfFrame)
			}
		}
		f = p.frames.frames[0]
		p.frames.frames = p.frames.frames[1:]
	}
	if f == nil && len(p.frames.frames) == 0 && p.shown != nil && pos > p.shown.PTS+2*p.halfFrame+early {
		p.empty++
	}
	p.frames.mu.Unlock()
	if f != nil {
		p.show(f)
	}
	return f
}

// Drops counts the frames skipped because a later one was due too, and
// the display frames that found no decoded frame when one was due.
func (p *Pipeline) Drops() (late, empty int) { return p.dropped, p.empty }

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
	if p.video() >= 0 {
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
