package pipeline

import (
	"math"
	"time"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// demux reads d's packets into the queues of the streams being decoded,
// waiting while they hold enough or d feeds none, and carries out seeks.
func (p *Pipeline) demux(d *input) {
	defer p.wg.Done()
	f := d.in.Format
	streams := f.Streams()
	main := d == p.inputs[0]
	for {
		p.mu.Lock()
		for !p.stopped() && d.seekReq == nil && (d.eof || d.hold || !p.feeds(d) || p.queuesFull(d)) {
			p.cond.Wait()
		}
		if p.stopped() {
			p.mu.Unlock()
			return
		}
		req := d.seekReq
		d.seekReq = nil
		gen, video, audio := p.gen, p.videoStream, p.audioStream
		feedsAudio := p.audioInput() == d
		p.mu.Unlock()

		if req != nil {
			d.interrupt.Store(false)
			p.doSeek(d, req)
			continue
		}

		pkt := ffmpeg.NewPacket()
		err := f.ReadPacket(pkt)
		if err != nil {
			pkt.Free()
			if p.stopped() {
				return
			}
			p.mu.Lock()
			pending := d.seekReq != nil
			p.mu.Unlock()
			if pending {
				continue // a seek interrupted the read
			}
			if !ffmpeg.IsEOF(err) {
				if rerr := d.in.ReadErr(); rerr != nil {
					err = rerr
				}
				p.emit(Event{Kind: EventError, Err: err})
			}
			p.mu.Lock()
			if p.gen == gen {
				d.eof = true
			}
			p.mu.Unlock()
			eof := item{eof: true, gen: gen}
			if main && video >= 0 {
				p.vq.put(eof)
			}
			if feedsAudio && audio >= 0 {
				p.aq.put(eof)
			}
			continue
		}
		idx := pkt.StreamIndex()
		id := d.base + idx
		if ts := packetTime(pkt); ts != ffmpeg.NoPTS && (id == video || id == audio) && idx < len(streams) {
			t := streams[idx].TimeBase().Duration(ts)
			p.mu.Lock()
			if p.gen == gen {
				d.buffered = max(d.buffered, t)
			}
			p.mu.Unlock()
		}
		switch {
		case main && id == video:
			p.vq.put(item{pkt: pkt, gen: gen})
		case id == audio:
			p.aq.put(item{pkt: pkt, gen: gen})
		case p.subs[id] != nil:
			p.decodeSubtitle(id, streams[idx].TimeBase(), pkt)
			pkt.Free()
		default:
			pkt.Free()
		}
	}
}

// queuesFull reports the queues holding enough for d to wait: too many
// bytes together, or every queue d feeds enough packets and time. It is
// called with p.mu held.
func (p *Pipeline) queuesFull(d *input) bool {
	var bytes int
	full := true
	add := func(q *packetQueue, feeder *input) {
		n, b, dur := q.stats()
		bytes += b
		if feeder == d && (n < minQueuePackets || dur < minQueueTime) {
			full = false
		}
	}
	if p.videoStream >= 0 {
		add(p.vq, p.inputs[0])
	}
	if p.audioStream >= 0 {
		add(p.aq, p.audioInput())
	}
	return bytes > maxQueueBytes || full
}

// doSeek seeks d to the keyframe at or before the target and flushes the
// queues it feeds for the request's generation. An external audio input
// only flushes for a history fill, which needs no sound.
func (p *Pipeline) doSeek(d *input, req *seekRequest) {
	main := d == p.inputs[0]
	p.mu.Lock()
	feedsAudio := p.audioInput() == d
	if !main && req.back != nil && p.gen == req.gen {
		d.hold = true
	}
	p.mu.Unlock()
	if !main && !feedsAudio {
		return
	}
	if main || req.back == nil {
		f := d.in.Format
		idx, ts := -1, int64(req.target/time.Microsecond)
		if main && p.adaptive != nil {
			// HLS and DASH seek by the playing track's segments, finding
			// its keyframes; the default stream may be another variant's.
			p.mu.Lock()
			idx = p.videoStream
			if idx < 0 && p.audioStream >= 0 && p.audioStream < len(f.Streams()) {
				idx = p.audioStream
			}
			p.mu.Unlock()
			if idx >= 0 {
				ts = f.Streams()[idx].TimeBase().Units(req.target)
			}
		}
		if err := f.Seek(idx, math.MinInt64, ts, ts, 0); err != nil {
			// Some inputs seek only forward of the target.
			f.Seek(idx, math.MinInt64, ts, math.MaxInt64, 0)
		}
	}
	marker := item{flush: true, gen: req.gen, target: req.target, exact: req.exact, vdrop: req.vdrop, back: req.back}
	if main {
		p.vq.flush(marker)
		for _, s := range p.subs {
			s.Flush()
		}
	}
	if feedsAudio {
		p.aq.flush(marker)
	}
}

// decodeSubtitle turns a text subtitle packet into cues of its track.
func (p *Pipeline) decodeSubtitle(track int, tb ffmpeg.Rational, pkt *ffmpeg.Packet) {
	rects, startMS, endMS, err := p.subs[track].DecodeSubtitle(pkt)
	if err != nil || len(rects) == 0 {
		return
	}
	ts := pkt.PTS()
	if ts == ffmpeg.NoPTS {
		return
	}
	base := tb.Duration(ts)
	start := base + time.Duration(startMS)*time.Millisecond
	end := base + tb.Duration(pkt.DurationTS())
	if endMS > startMS && endMS != math.MaxUint32 {
		end = base + time.Duration(endMS)*time.Millisecond
	}
	for _, r := range rects {
		text := r.Text
		if r.ASS != "" {
			text = assText(r.ASS, 9)
		}
		p.cues.add(track, Cue{Start: start, End: end, Text: text})
	}
}
