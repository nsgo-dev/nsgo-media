package pipeline

import (
	"math"
	"time"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// demux reads packets into the queues of the streams being decoded,
// waiting while they hold enough, and carries out seeks.
func (p *Pipeline) demux() {
	defer p.wg.Done()
	f := p.in.Format
	streams := f.Streams()
	for {
		p.mu.Lock()
		for !p.stopped() && p.seekReq == nil && (p.demuxEOF || p.queuesFull()) {
			p.cond.Wait()
		}
		if p.stopped() {
			p.mu.Unlock()
			return
		}
		req := p.seekReq
		p.seekReq = nil
		gen, audio := p.gen, p.audioStream
		p.mu.Unlock()

		if req != nil {
			p.interrupt.Store(false)
			p.doSeek(req)
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
			pending := p.seekReq != nil
			p.mu.Unlock()
			if pending {
				continue // a seek interrupted the read
			}
			if !ffmpeg.IsEOF(err) {
				if rerr := p.in.ReadErr(); rerr != nil {
					err = rerr
				}
				p.mu.Lock()
				p.readErr = err
				p.mu.Unlock()
				p.emit(Event{Kind: EventError, Err: err})
			}
			p.mu.Lock()
			if p.gen == gen {
				p.demuxEOF = true
			}
			p.mu.Unlock()
			eof := item{eof: true, gen: gen}
			if p.videoStream >= 0 {
				p.vq.put(eof)
			}
			if audio >= 0 {
				p.aq.put(eof)
			}
			continue
		}
		idx := pkt.StreamIndex()
		if ts := packetTime(pkt); ts != ffmpeg.NoPTS && (idx == p.videoStream || idx == audio) && idx < len(streams) {
			t := streams[idx].TimeBase().Duration(ts)
			p.mu.Lock()
			if p.gen == gen {
				p.buffered = max(p.buffered, t)
			}
			p.mu.Unlock()
		}
		switch {
		case idx == p.videoStream:
			p.vq.put(item{pkt: pkt, gen: gen})
		case idx == audio:
			p.aq.put(item{pkt: pkt, gen: gen})
		case p.subs[idx] != nil:
			p.decodeSubtitle(idx, streams[idx].TimeBase(), pkt)
			pkt.Free()
		default:
			pkt.Free()
		}
	}
}

// queuesFull reports the queues holding enough: too many bytes together,
// or every queue enough packets and time. It is called with p.mu held.
func (p *Pipeline) queuesFull() bool {
	var bytes int
	full := true
	for _, q := range p.activeQueues() {
		n, b, d := q.stats()
		bytes += b
		if n < minQueuePackets || d < minQueueTime {
			full = false
		}
	}
	return bytes > maxQueueBytes || full
}

func (p *Pipeline) activeQueues() []*packetQueue {
	var qs []*packetQueue
	if p.videoStream >= 0 {
		qs = append(qs, p.vq)
	}
	if p.audioStream >= 0 {
		qs = append(qs, p.aq)
	}
	return qs
}

// doSeek seeks the demuxer to the keyframe at or before the target and
// flushes every queue for the request's generation.
func (p *Pipeline) doSeek(req *seekRequest) {
	f := p.in.Format
	us := int64(req.target / time.Microsecond)
	if err := f.Seek(-1, math.MinInt64, us, us, 0); err != nil {
		// Some inputs seek only forward of the target.
		f.Seek(-1, math.MinInt64, us, math.MaxInt64, 0)
	}
	marker := item{flush: true, gen: req.gen, target: req.target, exact: req.exact}
	p.vq.flush(marker)
	p.aq.flush(marker)
	for _, d := range p.subs {
		d.Flush()
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
