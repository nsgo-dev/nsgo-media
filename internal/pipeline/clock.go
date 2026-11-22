package pipeline

import (
	"sync"
	"time"

	"github.com/oneo-me/nsgo-media/internal/audioout"
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

// chunk is decoded audio at the device's rate and channels, or an IEC
// 61937 burst. perFrame is the media time each device frame plays, which
// the rate stretches.
type chunk struct {
	samples  []float32
	data     []byte // a burst's 16-bit stereo frames, instead of samples
	off      int    // frames already played
	pts      time.Duration
	perFrame float64 // nanoseconds
	media    bool    // a burst of the media, not a pause
	silent   bool    // a burst sent as pauses, muted
}

func (c *chunk) frames(ch int) int {
	if c.data != nil {
		return len(c.data)/4 - c.off
	}
	return len(c.samples)/ch - c.off
}

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

	// An IEC 61937 stream's: bursts play whole, gaps are pause bursts.
	iec   *audioout.IEC
	pause []byte // a pause burst
	burst chunk  // the burst being sent
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

// reset makes the ring a new device's, of rate and ch, carrying f's IEC
// 61937 stream unless it is nil: the device has filled nothing yet, and
// the clock holds where the flush put it.
func (r *audioRing) reset(rate, ch int, f *audioout.IEC) {
	r.mu.Lock()
	r.rate, r.ch = rate, ch
	r.capacity = rate * 3 / 10
	r.iec, r.pause, r.burst = f, nil, chunk{}
	if f != nil {
		r.pause = pauseUnit(f)
	}
	r.chunks, r.frames = nil, 0
	r.written = 0
	r.spans = []span{{pts: r.hold}}
	r.cond.Broadcast()
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
	// The burst being sent goes on to its end, holding the clock.
	r.burst.media = false
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
	if r.iec != nil {
		clear(dst) // a PCM device closing as the ring changed
		return
	}
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

// fillIEC is an IEC 61937 device's fill: it sends the queued bursts
// while running and not paused, each to its end, and pause bursts in
// between. Muted, or at no volume, a burst's time passes in pause bursts,
// as its bits cannot be made quieter; muting takes effect at the next
// burst, so none is cut.
func (r *audioRing) fillIEC(dst []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.iec == nil {
		clear(dst)
		return
	}
	total := len(dst) / 4
	play := r.running && !r.paused && !r.closed
	silent := r.muted || r.volume == 0
	for i := 0; i < total; {
		if r.burst.frames(2) <= 0 {
			r.burst = r.nextBurst(play)
			r.burst.silent = r.burst.media && silent
		}
		b := &r.burst
		n := min(total-i, b.frames(2))
		out := dst[i*4 : (i+n)*4]
		if b.silent {
			for k := range out {
				out[k] = r.pause[(b.off*4+k)%len(r.pause)]
			}
		} else {
			copy(out, b.data[b.off*4:])
		}
		if b.media {
			pts := b.pts + time.Duration(float64(b.off)*b.perFrame)
			r.addSpan(span{dev: r.written + int64(i), n: int64(n), pts: pts, perFrame: b.perFrame})
			r.hold = pts + time.Duration(float64(n)*b.perFrame)
		} else {
			r.addSpan(span{dev: r.written + int64(i), n: int64(n), pts: r.hold})
		}
		b.off += n
		i += n
	}
	r.written += int64(total)
	r.cond.Broadcast()
}

// nextBurst takes the next queued burst, dropping those before skipTo,
// or makes a pause burst when there is none or play is false.
func (r *audioRing) nextBurst(play bool) chunk {
	if play {
		for len(r.chunks) > 0 {
			c := r.chunks[0]
			r.chunks = r.chunks[1:]
			r.frames -= c.frames(2)
			if r.skipTo >= 0 && c.pts+burstDuration(&c) <= r.skipTo {
				continue
			}
			r.skipTo = -1
			c.media = true
			return c
		}
		if !r.eof {
			r.starved = true
		}
	}
	return chunk{data: r.pause}
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
