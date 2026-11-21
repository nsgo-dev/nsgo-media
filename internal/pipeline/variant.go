package pipeline

import (
	"fmt"
	"time"
)

// Adapting the variant to the bandwidth.
const (
	adaptEvery     = 2 * time.Second  // how often the bandwidth is weighed
	upSwitchAfter  = 10 * time.Second // the least time before a higher variant
	bandwidthShare = 0.8              // of the estimate a variant may take
)

// chooseVariant is the highest of vs, ascending, that fits the estimated
// bits per second, or the lowest; with no estimate yet, the lowest.
func chooseVariant(vs []Track, bps float64) Track {
	best := vs[0]
	for _, v := range vs[1:] {
		if float64(v.Bandwidth) <= bps*bandwidthShare {
			best = v
		}
	}
	return best
}

// audioFor is the audio track to play with video track id: cur, unless
// cur belongs to another variant, whose own audio is then played.
func (p *Pipeline) audioFor(id, cur int) int {
	v, a := p.track(id), p.track(cur)
	if cur < 0 || v.Bandwidth == 0 || a.Bandwidth == 0 || a.Bandwidth == v.Bandwidth {
		return cur
	}
	best := cur
	for _, t := range p.info.Tracks {
		if t.Kind != Audio || t.Bandwidth != v.Bandwidth {
			continue
		}
		if best == cur || t.Language == a.Language && p.track(best).Language != a.Language {
			best = t.ID
		}
	}
	return best
}

// SelectVariant plays the variant of video track id, one of
// Info.Variants, from the current position, or with -1 lets the
// bandwidth choose it.
func (p *Pipeline) SelectVariant(id int) error {
	vs := p.info.Variants()
	if id < 0 {
		p.mu.Lock()
		p.auto = len(vs) > 1
		p.mu.Unlock()
		p.adaptAt = time.Time{}
		return nil
	}
	found := false
	for _, v := range vs {
		found = found || v.ID == id
	}
	if !found {
		return fmt.Errorf("pipeline: no variant %d", id)
	}
	p.mu.Lock()
	p.auto = false
	p.mu.Unlock()
	p.switchVariant(id)
	return nil
}

// AutoVariant reports the bandwidth choosing the variant.
func (p *Pipeline) AutoVariant() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.auto
}

// Bandwidth is the estimated rate segments arrive at, in bits per second,
// 0 before one arrived or for a source that is not HLS or DASH.
func (p *Pipeline) Bandwidth() float64 {
	if p.adaptive == nil {
		return 0
	}
	return p.adaptive.bw.estimate()
}

// switchVariant plays video track id, and the audio of its variant, from
// the shown position. The decoders change at the seek's flush.
func (p *Pipeline) switchVariant(id int) {
	p.mu.Lock()
	if id == p.videoStream {
		p.mu.Unlock()
		return
	}
	p.videoStream = id
	audio := p.audioFor(id, p.audioStream)
	audioChanged := audio != p.audioStream
	p.audioStream = audio
	p.setDiscards()
	p.mu.Unlock()
	p.setVideoParams(id)
	p.reopenVideo.Store(true)
	if audioChanged {
		p.reopenAudio.Store(true)
	}
	p.switchedAt = time.Now()
	p.seek(p.Position(), true)
}

// adapt weighs the bandwidth while playing and changes the variant when
// another fits it better: a lower one at once, a higher one only a while
// after the last change. It runs on the engine's goroutine.
func (p *Pipeline) adapt(now time.Time) {
	if now.Sub(p.adaptAt) < adaptEvery {
		return
	}
	p.adaptAt = now
	p.mu.Lock()
	auto, paused, cur := p.auto, p.paused, p.videoStream
	p.mu.Unlock()
	if !auto || paused || !p.running || p.adaptive == nil {
		return
	}
	bps := p.adaptive.bw.estimate()
	if bps <= 0 {
		return
	}
	next := chooseVariant(p.info.Variants(), bps)
	if next.ID == cur || next.Bandwidth > p.track(cur).Bandwidth && now.Sub(p.switchedAt) < upSwitchAfter {
		return
	}
	p.switchVariant(next.ID)
}
