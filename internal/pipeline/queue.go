package pipeline

import (
	"sync"
	"time"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// item is what a packet queue carries: a packet, or a marker that the
// stream ended or that a seek flushed what came before.
type item struct {
	pkt   *ffmpeg.Packet
	gen   uint64
	flush bool
	eof   bool
	// target and exact are a flush's seek: decoders drop what precedes
	// target when exact.
	target time.Duration
	exact  bool
}

// packetQueue is one stream's packets between the demuxer and its
// decoder. The demuxer bounds all queues together (see demux), so put
// never waits; get waits for a packet or for the queue to close.
type packetQueue struct {
	mu    sync.Mutex
	cond  *sync.Cond
	items []item
	bytes int
	n     int // packets, not markers
	tb    ffmpeg.Rational
	// head and tail are the first queued packet's timestamp and the
	// latest one's, in tb, which give the time the queue holds.
	head, tail int64
	closed     bool
	// wake is called after a get, so the demuxer can read more.
	wake func()
}

func newPacketQueue(tb ffmpeg.Rational, wake func()) *packetQueue {
	q := &packetQueue{tb: tb, wake: wake, head: ffmpeg.NoPTS}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *packetQueue) put(it item) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		freeItem(it)
		return
	}
	if it.pkt != nil {
		q.bytes += it.pkt.Size()
		q.n++
		if ts := packetTime(it.pkt); ts != ffmpeg.NoPTS {
			if q.head == ffmpeg.NoPTS {
				q.head, q.tail = ts, ts
			}
			q.tail = max(q.tail, ts)
		}
	}
	q.items = append(q.items, it)
	q.cond.Signal()
	q.mu.Unlock()
}

// flush drops every packet and queues a flush marker for gen.
func (q *packetQueue) flush(marker item) {
	q.mu.Lock()
	for _, it := range q.items {
		freeItem(it)
	}
	q.items = q.items[:0]
	q.bytes, q.n, q.head, q.tail = 0, 0, ffmpeg.NoPTS, 0
	if !q.closed {
		q.items = append(q.items, marker)
	}
	q.cond.Signal()
	q.mu.Unlock()
}

// get waits for the next item; ok is false once the queue is closed.
func (q *packetQueue) get() (it item, ok bool) {
	q.mu.Lock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		q.mu.Unlock()
		return item{}, false
	}
	it = q.items[0]
	q.items[0] = item{}
	q.items = q.items[1:]
	if it.pkt != nil {
		q.bytes -= it.pkt.Size()
		q.n--
		q.head = ffmpeg.NoPTS
		for _, next := range q.items {
			if next.pkt != nil {
				q.head = packetTime(next.pkt)
				break
			}
		}
	}
	q.mu.Unlock()
	if q.wake != nil {
		q.wake()
	}
	return it, true
}

// stats are the queue's packets, bytes and the time they span.
func (q *packetQueue) stats() (n, bytes int, dur time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	n = q.n
	if n > 0 && q.head != ffmpeg.NoPTS && q.tail > q.head {
		dur = q.tb.Duration(q.tail - q.head)
	}
	return n, q.bytes, dur
}

// setTimeBase changes the time base, for a queue whose stream changed.
func (q *packetQueue) setTimeBase(tb ffmpeg.Rational) {
	q.mu.Lock()
	q.tb = tb
	q.mu.Unlock()
}

// packetTime is a packet's presentation time, or its decoding time.
func packetTime(p *ffmpeg.Packet) int64 {
	if ts := p.PTS(); ts != ffmpeg.NoPTS {
		return ts
	}
	return p.DTS()
}

func (q *packetQueue) close() {
	q.mu.Lock()
	q.closed = true
	for _, it := range q.items {
		freeItem(it)
	}
	q.items = nil
	q.cond.Broadcast()
	q.mu.Unlock()
}

func freeItem(it item) {
	if it.pkt != nil {
		it.pkt.Free()
	}
}
