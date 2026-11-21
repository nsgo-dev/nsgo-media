package pipeline

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// Source is what a pipeline reads: a file path or an HTTP(S) URL, and
// the headers every HTTP request for it sends.
type Source struct {
	Location string
	Header   http.Header
}

// input is one opened source and its demuxer's state. The first input is
// the main source; an external audio source is a second one, which feeds
// only the audio queue.
type input struct {
	in        *ffmpeg.Input
	http      *httpSource
	interrupt atomic.Bool
	base      int // the track ID of its first stream

	// Under the pipeline's lock.
	seekReq  *seekRequest
	eof      bool
	hold     bool          // a history fill finished, or needs no sound
	buffered time.Duration // the latest timestamp demuxed
}

// open opens src as an input of p, which Close interrupts and frees.
func (p *Pipeline) open(src Source, base int) (*input, error) {
	d := &input{base: base}
	p.mu.Lock()
	p.inputs = append(p.inputs, d)
	p.mu.Unlock()
	if p.closing.Load() {
		return nil, errClosed
	}
	var source ffmpeg.Source
	if isURL(src.Location) {
		h, err := newHTTPSource(p.ctx, src.Location, src.Header)
		if err != nil {
			if p.closing.Load() {
				return nil, errClosed
			}
			return nil, err
		}
		d.http, source = h, h
	}
	in, err := ffmpeg.Open(src.Location, source, &d.interrupt)
	if err != nil {
		if p.closing.Load() {
			return nil, errClosed
		}
		return nil, err
	}
	d.in = in
	return d, nil
}

// stream is the stream of the track id and the input holding it.
func (p *Pipeline) stream(id int) (*input, *ffmpeg.Stream) {
	for i := len(p.inputs) - 1; i >= 0; i-- {
		d := p.inputs[i]
		if d.in == nil || id < d.base {
			continue
		}
		if s := d.in.Format.Streams(); id-d.base < len(s) {
			return d, s[id-d.base]
		}
		return nil, nil
	}
	return nil, nil
}

// audioInput is the input the audio queue reads from: the one holding
// the audio track, or the main one without audio. It is called with p.mu
// held.
func (p *Pipeline) audioInput() *input {
	if p.audioStream >= 0 {
		if d, _ := p.stream(p.audioStream); d != nil {
			return d
		}
	}
	return p.inputs[0]
}

// feeds reports d reading for a stream being decoded. It is called with
// p.mu held.
func (p *Pipeline) feeds(d *input) bool {
	return d == p.inputs[0] && p.videoStream >= 0 || d == p.audioInput()
}

// demuxEOF reports every input being read having reached its end. It is
// called with p.mu held.
func (p *Pipeline) demuxEOF() bool {
	for _, d := range p.inputs {
		if p.feeds(d) && !d.eof {
			return false
		}
	}
	return true
}

// requestSeek has every input seek for req, and stops any read blocking.
// It is called with p.mu held.
func (p *Pipeline) requestSeek(req *seekRequest) {
	for _, d := range p.inputs {
		d.seekReq = req
		d.eof, d.hold, d.buffered = false, false, req.target
		d.interrupt.Store(true)
	}
	p.cond.Broadcast()
}
