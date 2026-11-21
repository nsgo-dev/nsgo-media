package pipeline

import (
	"sync"
	"time"
)

// wallClock is the clock without audio: a media time running from a
// monotonic instant at the rate, frozen while stopped.
type wallClock struct {
	mu      sync.Mutex
	base    time.Duration
	at      time.Time
	running bool
	rate    float64
}

func (c *wallClock) now() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nowLocked()
}

func (c *wallClock) nowLocked() time.Duration {
	if !c.running {
		return c.base
	}
	return c.base + time.Duration(float64(time.Since(c.at))*c.rate)
}

func (c *wallClock) set(t time.Duration) {
	c.mu.Lock()
	c.base, c.at = t, time.Now()
	c.mu.Unlock()
}

func (c *wallClock) setRunning(running bool, rate float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if running == c.running {
		return
	}
	c.base, c.at = c.nowLocked(), time.Now()
	c.running, c.rate = running, rate
}

func (c *wallClock) rebase(rate float64) {
	c.mu.Lock()
	c.base, c.at = c.nowLocked(), time.Now()
	c.rate = rate
	c.mu.Unlock()
}

// chunk is decoded audio at the device's rate and channels. perFrame is
// the media time each device frame plays, which the rate stretches.
type chunk struct {
	samples  []float32
	off      int // frames already played
	pts      time.Duration
	perFrame float64 // nanoseconds
}

func (c *chunk) frames(ch int) int { return len(c.samples)/ch - c.off }

// span maps device frames [dev, dev+n) to media time from pts, perFrame
// nanoseconds each; perFrame 0 holds pts, as silence does.
type span struct {
	dev      int64
	n        int64
	pts      time.Duration
	perFrame float64
}

func (s span) end() time.Duration { return s.pts + time.Duration(float64(s.n)*s.perFrame) }

// audioRing is the decoded audio between the audio decoder and the
// device, and the timeline of what the device played, which is the clock.
type audioRing struct {
	mu       sync.Mutex
	cond     *sync.Cond
	rate, ch int
	chunks   []chunk
	frames   int // frames queued
	capacity int
	gen      uint64
	written  int64 // device frames filled
	spans    []span
	hold     time.Duration // the media time silence holds
	skipTo   time.Duration // media before this is dropped, -1 for none
	running  bool          // whether filling plays the queue
	paused   bool
	eof      bool // the decoder queued the generation's last samples
	starved  bool // filling ran out while running
	volume   float32
	muted    bool
	closed   bool
}

func newAudioRing(gen uint64) *audioRing {
	r := &audioRing{volume: 1, skipTo: -1, rate: 48000, ch: 2, gen: gen}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *audioRing) setFormat(rate, ch int) {
	r.mu.Lock()
	r.rate, r.ch = rate, ch
	r.capacity = rate * 3 / 10 // 300 ms
	r.mu.Unlock()
}

// push queues samples of gen, waiting while the ring is full. It returns
// false when gen is stale or the ring closed.
func (r *audioRing) push(gen uint64, c chunk) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.frames >= r.capacity && r.gen == gen && !r.closed {
		r.cond.Wait()
	}
	if r.gen != gen || r.closed {
		return false
	}
	r.chunks = append(r.chunks, c)
	r.frames += c.frames(r.ch)
	return true
}

func (r *audioRing) setEOF(gen uint64) {
	r.mu.Lock()
	if r.gen == gen {
		r.eof = true
	}
	r.mu.Unlock()
}

// flush drops the queue for a seek to t: the device plays silence holding
// t until the new generation's samples come.
func (r *audioRing) flush(gen uint64, t time.Duration) {
	r.mu.Lock()
	r.gen = gen
	r.chunks, r.frames = nil, 0
	r.eof, r.starved, r.running = false, false, false
	r.hold, r.skipTo = t, -1
	r.spans = []span{{dev: r.written, pts: t}}
	r.cond.Broadcast()
	r.mu.Unlock()
}

// start plays the queue from media time t, dropping what comes before it.
func (r *audioRing) start(t time.Duration) {
	r.mu.Lock()
	r.running, r.starved = true, false
	r.hold, r.skipTo = t, t
	r.mu.Unlock()
}

// stop holds the clock where it is and plays silence, for buffering.
func (r *audioRing) stop() {
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
}

func (r *audioRing) setPaused(p bool) {
	r.mu.Lock()
	r.paused = p
	r.mu.Unlock()
}

func (r *audioRing) setVolume(v float64) {
	r.mu.Lock()
	r.volume = float32(v)
	r.mu.Unlock()
}

func (r *audioRing) setMuted(m bool) {
	r.mu.Lock()
	r.muted = m
	r.mu.Unlock()
}

func (r *audioRing) close() {
	r.mu.Lock()
	r.closed = true
	r.cond.Broadcast()
	r.mu.Unlock()
}

// status is what the presenter reads: the queued time, the first queued
// media time, whether the generation's samples all came, and whether
// filling ran out since the last call.
func (r *audioRing) status() (queued time.Duration, first time.Duration, haveFirst, eof, starved bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	starved, r.starved = r.starved, false
	if len(r.chunks) > 0 {
		first, haveFirst = r.chunks[0].pts, true
	}
	return time.Duration(r.frames) * time.Second / time.Duration(max(r.rate, 1)), first, haveFirst, r.eof, starved
}

// drained reports the generation's last sample played: the queue is empty
// after its end and the device played everything filled.
func (r *audioRing) drained(played int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.eof && len(r.chunks) == 0 && played >= r.written-int64(r.rate/20)
}

// fill is the device's Fill: it plays the queue while running and not
// paused, and records what each device frame plays.
func (r *audioRing) fill(dst []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := r.ch
	total := len(dst) / ch
	i := 0
	if r.running && !r.paused && !r.closed {
		gain := r.volume
		if r.muted {
			gain = 0
		}
		for i < total && len(r.chunks) > 0 {
			c := &r.chunks[0]
			if r.skipTo >= 0 {
				// Drop what precedes skipTo, as after a step or a seek.
				if c.perFrame > 0 && c.pts < r.skipTo {
					skip := int(float64(r.skipTo-c.pts) / c.perFrame)
					if skip >= c.frames(ch) {
						r.frames -= c.frames(ch)
						r.chunks = r.chunks[1:]
						continue
					}
					c.off += skip
					c.pts += time.Duration(float64(skip) * c.perFrame)
					r.frames -= skip
				}
				r.skipTo = -1
			}
			n := min(total-i, c.frames(ch))
			src := c.samples[c.off*ch : (c.off+n)*ch]
			out := dst[i*ch : (i+n)*ch]
			for k, v := range src {
				out[k] = v * gain
			}
			r.addSpan(span{dev: r.written + int64(i), n: int64(n), pts: c.pts, perFrame: c.perFrame})
			c.off += n
			c.pts += time.Duration(float64(n) * c.perFrame)
			r.hold = c.pts
			r.frames -= n
			i += n
			if c.frames(ch) == 0 {
				r.chunks = r.chunks[1:]
			}
		}
		if i < total && !r.eof {
			r.starved = true
		}
		r.cond.Broadcast()
	}
	if i < total {
		clear(dst[i*ch:])
		r.addSpan(span{dev: r.written + int64(i), n: int64(total - i), pts: r.hold})
	}
	r.written += int64(total)
}

func (r *audioRing) addSpan(s span) {
	if k := len(r.spans) - 1; k >= 0 {
		last := &r.spans[k]
		if last.dev+last.n == s.dev && last.perFrame == s.perFrame &&
			absDur(last.end()-s.pts) < time.Millisecond {
			last.n += s.n
			return
		}
	}
	r.spans = append(r.spans, s)
}

// position is the media time the device's frame played is at, trimming
// the timeline before it.
func (r *audioRing) position(played int64) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.spans) == 0 {
		return r.hold
	}
	k := 0
	for k+1 < len(r.spans) && r.spans[k+1].dev <= played {
		k++
	}
	r.spans = r.spans[k:]
	s := r.spans[0]
	if played <= s.dev {
		return s.pts
	}
	return s.pts + time.Duration(float64(min(played-s.dev, s.n))*s.perFrame)
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
