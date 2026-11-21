package pipeline

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// Bounds of the decoded pictures kept for stepping backward.
const (
	historyFrames = 48
	historyBytes  = 128 << 20
	// fillFrames is the most frames one fill reads before the shown one.
	fillFrames = 16
)

// frameHistory keeps decoded pictures, in memory and at the source's
// size, of a run of frames before the shown one, so stepping backward
// finds the previous frame without decoding. Only stepping backward fills
// it: the video decoder decodes a window before the shown frame into it.
// Its entries are one unbroken run: every frame from the first entry's
// time to the run's end. Seeking and resuming clear it, so a frame of an
// older timeline never shows.
type frameHistory struct {
	mu        sync.Mutex
	id        uint64 // the fill pushes are for; others are dropped
	entries   []histEntry
	bytes     int
	maxFrames int
	maxBytes  int
	to        time.Duration // the run's end
	start     bool          // nothing comes before the first entry
	done      bool          // the last fill finished

	// The fill in flight.
	fillEnd   time.Duration
	fillStart bool
	frontLost bool // the fill evicted entries from the run's front
}

type histEntry struct {
	pts   time.Duration
	src   *ffmpeg.Frame
	bytes int
}

func newFrameHistory() *frameHistory {
	return &frameHistory{maxFrames: historyFrames, maxBytes: historyBytes, done: true}
}

// begin starts a fill of the frames up to end, which the shown frame is
// at, returning its id. With join the fill comes right before the run,
// whose first entry is at end; otherwise the run is dropped. atStart
// says the fill reads from the source's start.
func (h *frameHistory) begin(end time.Duration, atStart, join bool) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.id++
	if !join {
		h.freeLocked()
		h.to = end
	}
	h.fillEnd, h.fillStart, h.frontLost, h.done = end, atStart, false, false
	return h.id
}

// push keeps a decoded picture of fill id; it reports false when the
// picture is not kept, so the caller frees it.
func (h *frameHistory) push(id uint64, pts time.Duration, src *ffmpeg.Frame) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id != h.id || h.done {
		return false
	}
	i := sort.Search(len(h.entries), func(i int) bool { return h.entries[i].pts >= pts })
	if i < len(h.entries) && h.entries[i].pts == pts {
		return false
	}
	e := histEntry{pts: pts, src: src, bytes: pictureBytes(src)}
	h.entries = append(h.entries, histEntry{})
	copy(h.entries[i+1:], h.entries[i:])
	h.entries[i] = e
	h.bytes += e.bytes
	// Over a bound, entries go first from the far end of the run joined,
	// then from the fill's front, which is farthest from the shown frame.
	// Two stay, so a step always has a frame to go to.
	for (len(h.entries) > h.maxFrames || h.bytes > h.maxBytes) && len(h.entries) > 2 {
		k := 0
		if last := len(h.entries) - 1; h.entries[last].pts > h.fillEnd {
			k = last
		} else {
			h.frontLost = true
		}
		h.bytes -= h.entries[k].bytes
		h.entries[k].src.Free()
		h.entries = append(h.entries[:k], h.entries[k+1:]...)
		if k > 0 {
			h.to = h.entries[k-1].pts
		}
	}
	return true
}

// finish ends fill id: every frame up to its end was decoded.
func (h *frameHistory) finish(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == h.id && !h.done {
		h.done = true
		h.start = h.fillStart && !h.frontLost
	}
}

// finished reports fill id over, or given up for a newer one.
func (h *frameHistory) finished(id uint64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return id != h.id || h.done
}

// covers reports t in the run, which then holds every frame around it.
func (h *frameHistory) coversLocked(t time.Duration) bool {
	return h.done && len(h.entries) > 0 && t >= h.entries[0].pts && t <= h.to
}

// prev is the frame before the one at t. It reports atStart when the run
// holds t and nothing comes before it.
func (h *frameHistory) prev(t time.Duration) (e histEntry, found, atStart bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.coversLocked(t) {
		return e, false, false
	}
	i := sort.Search(len(h.entries), func(i int) bool { return h.entries[i].pts >= t })
	if i == 0 {
		return e, false, h.start
	}
	return h.entries[i-1], true, false
}

// next is the frame after the one at t, when the run holds it.
func (h *frameHistory) next(t time.Duration) (e histEntry, found bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.coversLocked(t) {
		return e, false
	}
	i := sort.Search(len(h.entries), func(i int) bool { return h.entries[i].pts > t })
	if i == len(h.entries) {
		return e, false
	}
	return h.entries[i], true
}

// first reports the run starting with the frame at t.
func (h *frameHistory) first(t time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.coversLocked(t) && h.entries[0].pts == t
}

// clear drops every entry and any fill in flight.
func (h *frameHistory) clear() {
	h.mu.Lock()
	h.id++
	h.freeLocked()
	h.done = true
	h.mu.Unlock()
}

func (h *frameHistory) freeLocked() {
	for _, e := range h.entries {
		e.src.Free()
	}
	h.entries, h.bytes, h.start = nil, 0, false
}

// stats are the entries and their bytes.
func (h *frameHistory) stats() (n, bytes int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.entries), h.bytes
}

// pictureBytes estimates a decoded picture's memory: each plane's rows at
// the picture's height, more than subsampled planes take.
func pictureBytes(f *ffmpeg.Frame) int {
	n := 0
	for i := range 4 {
		if d, ls := f.Plane(i); d != nil {
			n += max(ls, -ls) * f.Height()
		}
	}
	return n
}

// backFill is a flush's history fill: the video decoder keeps the frames
// from from to end, the shown frame's time, and stops there.
type backFill struct {
	id        uint64
	from, end time.Duration
}

// noTime precedes every timestamp.
const noTime = time.Duration(math.MinInt64)

// Step steps n frames while paused, forward for n > 0 and backward for
// n < 0. Steps asked for before the last ones showed add up, and stepping
// stops at the first and the last frame.
func (p *Pipeline) Step(n int) {
	if p.Paused() && p.videoStream >= 0 {
		p.steps += n
	}
}

// step carries out the steps asked for, at most one frame shown a tick.
func (p *Pipeline) step(videoEOF bool) *Frame {
	if p.shown == nil {
		return nil
	}
	if p.filling != 0 {
		if p.genA.Load() != p.fillGen {
			// The decoder changed and decodes again from the shown frame.
			p.filling, p.steps, p.detached = 0, 0, false
			return nil
		}
		if !p.hist.finished(p.filling) {
			return nil
		}
		p.filling = 0
	}
	cur := p.shown.PTS
	var pick *ffmpeg.Frame
	for p.steps < 0 {
		e, found, atStart := p.hist.prev(cur)
		if !found {
			// A fill from the start that left nothing before cur stops
			// too, as when it decoded no frame at all.
			if atStart || pick == nil && p.fillFor == cur && p.fillAtStart && p.hist.finished(p.fillID) {
				p.steps = 0
			}
			break
		}
		pick, cur = e.src, e.pts
		p.steps++
	}
	// The decoders go on from a frame of the history only after a seek;
	// until then the history gives the next frames it holds.
	for p.steps > 0 && p.detached {
		e, found := p.hist.next(cur)
		if !found {
			break
		}
		pick, cur = e.src, e.pts
		p.steps--
	}
	if pick != nil {
		f := p.looseFrame(pick, cur)
		if f == nil {
			p.steps = 0
			return nil
		}
		p.show(f)
		p.begin(cur, true)
		p.detached = true
		return f
	}
	switch {
	case p.steps < 0:
		p.fillBefore(cur)
	case p.steps > 0 && p.detached:
		// Decode on from the shown frame, dropping it and what precedes.
		gen := p.restart(cur, true, cur+1, nil)
		p.ring.flush(gen, cur)
		p.detached = false
	case p.steps > 0:
		return p.stepQueued(videoEOF)
	}
	return nil
}

// fillBefore has the video decoder decode a window of frames before cur
// into the history. A window that held none is read again twice as long.
func (p *Pipeline) fillBefore(cur time.Duration) {
	n := fillFrames
	if b := pictureBytes(p.shown.src); b > 0 {
		p.hist.mu.Lock()
		n = min(n, p.hist.maxFrames/2, p.hist.maxBytes/(2*b))
		p.hist.mu.Unlock()
	}
	span := time.Duration(max(n, 2)) * p.frameDuration()
	if p.fillFor == cur && p.fillSpan > 0 {
		span = 2 * p.fillSpan
	}
	from, atStart := cur-span, false
	seekTo := from
	if from <= p.start {
		from, seekTo, atStart = noTime, p.start, true
	}
	id := p.hist.begin(cur, atStart, p.hist.first(cur))
	gen := p.restart(seekTo, false, -1, &backFill{id: id, from: from, end: cur})
	p.ring.flush(gen, cur)
	p.filling, p.fillID, p.fillGen, p.fillFor, p.fillSpan, p.fillAtStart = id, id, gen, cur, span, atStart
	p.detached = true
}

// stepQueued shows the queued frame the forward steps reach.
func (p *Pipeline) stepQueued(videoEOF bool) *Frame {
	var f *Frame
	p.frames.mu.Lock()
	for p.steps > 0 && len(p.frames.frames) > 0 {
		p.release(f)
		f = p.frames.frames[0]
		p.frames.frames = p.frames.frames[1:]
		p.steps--
	}
	empty := len(p.frames.frames) == 0
	p.frames.mu.Unlock()
	if p.steps > 0 && empty && videoEOF {
		p.steps = 0 // the last frame
	}
	if f != nil {
		p.show(f)
		p.begin(f.PTS, true)
	}
	return f
}

// looseFrame converts a picture of the history for display.
func (p *Pipeline) looseFrame(src *ffmpeg.Frame, pts time.Duration) *Frame {
	ow, oh := p.outputSize(src.Width(), src.Height())
	mem, err := p.paint(&p.stepScaler, src, ow, oh)
	if err != nil {
		return nil
	}
	f := newLooseFrame(mem, ow, oh)
	f.PTS, f.gen, f.src = pts, p.genA.Load(), src.Clone()
	return f
}
