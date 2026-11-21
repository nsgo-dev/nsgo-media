// Package pipeline plays a media source: a demuxer fills bounded packet
// queues, decoders on their own goroutines turn them into RGBA frames and
// device audio, and a presenter the engine calls picks the frame the clock
// has reached. Audio's played position is the clock; without audio a
// monotonic clock stands in. Seeks raise a generation that drops whatever
// an older one left in flight.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oneo-me/nsgo-media/internal/audioout"
	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// Bounds of what one pipeline holds.
const (
	maxQueueBytes   = 48 << 20 // packets of every stream together
	minQueuePackets = 25       // a stream's queue is full past this many packets
	minQueueTime    = 2 * time.Second
	queuedFrames    = 3 // decoded frames waiting for display
	resumeTime      = time.Second
)

// EventKind tells events apart.
type EventKind int

const (
	// EventBuffering is the pipeline starting (On) or stopping waiting
	// for data while playing.
	EventBuffering EventKind = iota
	// EventEnded is playback reaching the end without looping.
	EventEnded
	// EventError is a failure that stopped playback, in Err.
	EventError
	// EventHardware is video decoding moving to or off a hardware device
	// (Device, empty for software).
	EventHardware
)

// Event is what the pipeline tells as it plays, from its workers.
type Event struct {
	Kind   EventKind
	On     bool
	Err    error
	Device string
}

// Options configure a pipeline.
type Options struct {
	// Software turns hardware video decoding off.
	Software bool
	// OpenOutput opens the audio device; nil opens the system's default.
	OpenOutput func(audioout.Fill) (audioout.Output, error)
	// OnEvent receives events on the pipeline's goroutines; it must not
	// block.
	OnEvent func(Event)
}

// Pipeline is one opened source.
type Pipeline struct {
	opts    Options
	closing atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc
	openMu  sync.Mutex
	wg      sync.WaitGroup

	in     *ffmpeg.Input // the main input's
	inputs []*input      // the main source, then an external audio one
	info   *Info
	start  time.Duration // the first timestamp, which positions count from

	vq, aq      *packetQueue
	pool        *framePool
	frames      frameQueue
	cues        cueStore
	subs        map[int]*ffmpeg.Decoder // the demuxer's subtitle decoders
	ring        *audioRing
	out         audioout.Output
	videoStream int // -1 for none
	videoTB     ffmpeg.Rational
	videoRot    int
	videoSAR    float64
	hwDevice    atomic.Value // string

	genA atomic.Uint64 // gen, for workers to read without the lock
	rate atomic.Uint64 // float64 bits

	mu          sync.Mutex
	cond        *sync.Cond // the demuxer waits on it
	gen         uint64
	audioStream int // -1 for none
	paused      bool
	loop        bool
	outW, outH  int
	externalID  int

	// The presenter's state, on the engine's goroutine.
	shown, retired *Frame
	waitFirst      bool
	target         time.Duration
	exact          bool
	running        bool
	buffering      bool
	ended          bool
	wall           wallClock
	lastPos        time.Duration
	smooth         smoothClock
	halfFrame      time.Duration
	dropped, empty int
	lead           time.Duration // how early a frame shows
	reopenAudio    atomic.Bool

	// Stepping, on the engine's goroutine.
	hist        *frameHistory
	steps       int  // frames to step, backward when negative
	detached    bool // the decoders do not go on from the shown frame
	filling     uint64
	fillID      uint64 // the last fill, and what it was for
	fillGen     uint64
	fillFor     time.Duration
	fillSpan    time.Duration
	fillAtStart bool
	stepScaler  ffmpeg.Scaler
}

// seekRequest is a seek for the demuxers and, in its flush marker, what
// the decoders drop: audio before target when exact, video before vdrop
// (none when negative), or with back all but a history fill.
type seekRequest struct {
	target time.Duration
	exact  bool
	vdrop  time.Duration
	back   *backFill
	gen    uint64
}

// New makes a pipeline; Open opens its source.
func New(opts Options) *Pipeline {
	// Generation 1 is the first; 0 marks no generation.
	p := &Pipeline{opts: opts, videoStream: -1, audioStream: -1, paused: true, gen: 1, hist: newFrameHistory()}
	p.genA.Store(1)
	p.cond = sync.NewCond(&p.mu)
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.rate.Store(math.Float64bits(1))
	p.hwDevice.Store("")
	return p
}

var errClosed = errors.New("pipeline: closed")

// Open opens src, a file path or an HTTP(S) URL, reads its information and
// starts decoding, paused at the start. Close from another goroutine makes
// it return.
func (p *Pipeline) Open(src string) (*Info, error) {
	return p.OpenSources(Source{Location: src}, nil)
}

// OpenSources opens src as Open does. With audio, it also opens that
// source and plays its first audio track with src's picture: the two are
// demuxed apart on one timeline, as DASH serves them, and their audio
// tracks follow src's, External.
func (p *Pipeline) OpenSources(src Source, audio *Source) (*Info, error) {
	p.openMu.Lock()
	defer p.openMu.Unlock()
	if p.closing.Load() {
		return nil, errClosed
	}
	main, err := p.open(src, 0)
	if err != nil {
		return nil, err
	}
	in := main.in
	p.in = in
	p.info = readInfo(in.Format)
	p.start = in.Format.StartTime()
	p.externalID = len(in.Format.Streams())

	streams := in.Format.Streams()
	p.videoStream = pickTrack(p.info.Tracks, Video)
	p.audioStream = pickTrack(p.info.Tracks, Audio)
	if audio != nil {
		ext, err := p.open(*audio, p.externalID)
		if err != nil {
			return nil, fmt.Errorf("pipeline: external audio: %w", err)
		}
		var tracks []Track
		ai := readInfo(ext.in.Format)
		for _, t := range ai.Tracks {
			if t.Kind == Audio {
				t.ID += ext.base
				t.External = true
				tracks = append(tracks, t)
			}
		}
		if len(tracks) == 0 {
			return nil, errors.New("pipeline: external audio: no audio track")
		}
		p.info.Tracks = append(p.info.Tracks, tracks...)
		if p.info.Duration == 0 {
			p.info.Duration = ai.Duration
		}
		p.audioStream = pickTrack(tracks, Audio)
		p.externalID += len(ext.in.Format.Streams())
	}
	p.subs = map[int]*ffmpeg.Decoder{}
	for _, t := range p.info.Tracks {
		if t.Kind == Subtitle {
			s := streams[t.ID]
			if d, err := ffmpeg.NewDecoder(s.Params(), s.TimeBase(), 1, ""); err == nil {
				p.subs[t.ID] = d
			}
		}
	}
	p.setDiscards()

	wake := func() {
		p.mu.Lock()
		p.cond.Broadcast()
		p.mu.Unlock()
	}
	p.vq = newPacketQueue(ffmpeg.Rational{Num: 1, Den: 1}, wake)
	p.aq = newPacketQueue(ffmpeg.Rational{Num: 1, Den: 1}, wake)
	// The queue, the shown and the retiring frame, the one being decoded
	// and a rotation's scratch frame.
	p.pool = newFramePool(queuedFrames + 4)

	var vdec *ffmpeg.Decoder
	if p.videoStream >= 0 {
		s := streams[p.videoStream]
		p.videoTB = s.TimeBase()
		p.vq.setTimeBase(p.videoTB)
		t := p.track(p.videoStream)
		p.videoRot, p.videoSAR = t.Rotation, t.AspectRatio
		p.halfFrame = p.frameDuration() / 2
		p.lead = min(p.halfFrame, 8*time.Millisecond)
		vdec, err = p.openVideoDecoder(!p.opts.Software)
		if err != nil {
			p.videoStream = -1
			p.setDiscards()
		}
	}

	openOut := p.opts.OpenOutput
	if openOut == nil {
		openOut = audioout.Open
	}
	p.ring = newAudioRing(p.gen)
	out, _ := openOut(p.ring.fill)
	if out == nil {
		out = audioout.NewNull(48000, 2, p.ring.fill)
	}
	p.out = out
	p.ring.setFormat(out.Rate(), out.Channels())
	out.SetPaused(true)
	var adec *ffmpeg.Decoder
	if p.audioStream >= 0 {
		if adec, err = p.openAudioDecoder(p.audioStream); err != nil {
			p.audioStream = -1
			p.setDiscards()
		}
	}

	if p.closing.Load() {
		if vdec != nil {
			vdec.Close()
		}
		if adec != nil {
			adec.Close()
		}
		return nil, errClosed
	}
	p.waitFirst = true
	p.target = p.start
	p.lastPos = p.start
	p.wall.set(p.start)
	p.wg.Add(2 + len(p.inputs))
	for _, d := range p.inputs {
		go p.demux(d)
	}
	go p.decodeVideo(vdec)
	go p.decodeAudio(adec)
	return p.info, nil
}

// pickTrack is the first default track of kind, or the first of it.
func pickTrack(tracks []Track, kind Kind) int {
	id, def := -1, false
	for _, t := range tracks {
		if t.Kind == kind && (id < 0 || t.Default && !def) {
			id, def = t.ID, t.Default
		}
	}
	return id
}

func (p *Pipeline) track(id int) Track {
	for _, t := range p.info.Tracks {
		if t.ID == id {
			return t
		}
	}
	return Track{}
}

// setDiscards has the demuxers drop the streams nothing decodes.
func (p *Pipeline) setDiscards() {
	for _, d := range p.inputs {
		if d.in == nil {
			continue
		}
		for _, s := range d.in.Format.Streams() {
			i := d.base + s.Index()
			if i == p.videoStream || i == p.audioStream || p.subs[i] != nil {
				s.SetDiscard(ffmpeg.DiscardDefault)
			} else {
				s.SetDiscard(ffmpeg.DiscardAll)
			}
		}
	}
}

func hardwareDevice() string {
	switch runtime.GOOS {
	case "darwin":
		return "videotoolbox"
	case "windows":
		return "d3d11va"
	case "linux":
		return "vaapi"
	}
	return ""
}

func (p *Pipeline) openVideoDecoder(hardware bool) (*ffmpeg.Decoder, error) {
	s := p.in.Format.Streams()[p.videoStream]
	if hardware {
		if dev := hardwareDevice(); dev != "" {
			if d, err := ffmpeg.NewDecoder(s.Params(), s.TimeBase(), 0, dev); err == nil {
				p.hwDevice.Store(dev)
				return d, nil
			}
		}
	}
	p.hwDevice.Store("")
	return ffmpeg.NewDecoder(s.Params(), s.TimeBase(), 0, "")
}

func (p *Pipeline) openAudioDecoder(id int) (*ffmpeg.Decoder, error) {
	_, s := p.stream(id)
	p.aq.setTimeBase(s.TimeBase())
	return ffmpeg.NewDecoder(s.Params(), s.TimeBase(), 1, "")
}

// HardwareDevice is the device video decodes on, empty for software.
func (p *Pipeline) HardwareDevice() string { return p.hwDevice.Load().(string) }

func (p *Pipeline) emit(e Event) {
	if p.opts.OnEvent != nil && !p.closing.Load() {
		p.opts.OnEvent(e)
	}
}

// Close stops every worker, waits for them and frees everything. It may
// be called while Open runs; no event follows it.
func (p *Pipeline) Close() {
	if p.closing.Swap(true) {
		return
	}
	p.cancel()
	p.mu.Lock()
	for _, d := range p.inputs {
		d.interrupt.Store(true)
	}
	p.cond.Broadcast()
	p.mu.Unlock()
	p.openMu.Lock()
	defer p.openMu.Unlock()
	if p.vq != nil {
		p.vq.close()
		p.aq.close()
	}
	if p.ring != nil {
		p.ring.close()
	}
	if p.pool != nil {
		p.pool.wakeAll()
	}
	if p.out != nil {
		p.out.Close()
	}
	p.wg.Wait()
	for _, d := range p.subs {
		d.Close()
	}
	if p.pool != nil {
		p.frames.drain(p.pool)
		p.release(p.shown)
		p.release(p.retired)
		p.shown, p.retired = nil, nil
		p.pool.close()
	}
	p.hist.clear()
	p.stepScaler.Close()
	for _, d := range p.inputs {
		if d.in != nil {
			d.in.Close()
		}
		if d.http != nil {
			d.http.Close()
		}
	}
}

func (p *Pipeline) stopped() bool { return p.closing.Load() }

// Commands. They may be called from any goroutine, but the presenter's
// state they touch belongs to the engine's, where the player calls them.

// SetPaused pauses or resumes playback. Resuming at the end starts over;
// resuming on a frame stepping backward showed decodes again from it.
func (p *Pipeline) SetPaused(paused bool) {
	p.mu.Lock()
	ended := p.ended
	p.paused = paused
	p.mu.Unlock()
	switch {
	case paused:
	case ended:
		p.Seek(0, false)
	case (p.detached || p.filling != 0) && p.shown != nil:
		p.seekTo(p.shown.PTS, true, p.shown.PTS)
	default:
		p.steps = 0
		p.hist.clear()
	}
	p.wall.setRunning(!paused && p.running, p.Rate())
	p.ring.setPaused(paused)
	p.out.SetPaused(paused)
}

// Paused reports whether playback is paused.
func (p *Pipeline) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

// Seek moves to t from the start: to the keyframe at or before it, or
// with exact to the frame showing at t.
func (p *Pipeline) Seek(t time.Duration, exact bool) {
	if p.info.Duration > 0 {
		t = min(t, p.info.Duration)
	}
	t = max(t, 0) + p.start
	vdrop := time.Duration(-1)
	if exact {
		vdrop = t - p.frameDuration()/2
	}
	p.seekTo(t, exact, vdrop)
}

// seekTo seeks to the media time t, dropping video before vdrop, and
// waits for the first frame to start the clock there.
func (p *Pipeline) seekTo(t time.Duration, exact bool, vdrop time.Duration) {
	gen := p.restart(t, exact, vdrop, nil)
	p.ring.flush(gen, t)
	p.hist.clear()
	p.steps, p.detached, p.filling = 0, false, 0
	p.waitFirst, p.running, p.buffering = true, false, false
	p.target, p.exact, p.lastPos = t, exact, t
	p.smooth.valid = false
	p.wall.set(t)
}

// restart has the demuxer seek to target in a new generation and drops
// the frames queued, returning the generation.
func (p *Pipeline) restart(target time.Duration, exact bool, vdrop time.Duration, back *backFill) uint64 {
	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.genA.Store(gen)
	// A blocking read stops for the seek.
	p.requestSeek(&seekRequest{target: target, exact: exact, vdrop: vdrop, back: back, gen: gen})
	p.ended = false
	p.mu.Unlock()
	p.frames.drain(p.pool)
	p.pool.wakeAll()
	return gen
}

// SetRate sets the speed, 0.25 to 4, keeping the pitch.
func (p *Pipeline) SetRate(r float64) {
	r = min(max(r, 0.25), 4)
	p.wall.rebase(r)
	p.rate.Store(math.Float64bits(r))
}

// Rate is the speed.
func (p *Pipeline) Rate() float64 { return math.Float64frombits(p.rate.Load()) }

// SetVolume sets the volume, 0 to 1, and SetMuted silences the sound.
func (p *Pipeline) SetVolume(v float64) { p.ring.setVolume(min(max(v, 0), 1)) }
func (p *Pipeline) SetMuted(m bool)     { p.ring.setMuted(m) }

// SetLoop makes playback start over at the end.
func (p *Pipeline) SetLoop(loop bool) {
	p.mu.Lock()
	p.loop = loop
	p.mu.Unlock()
}

// SetOutputSize is the largest size in pixels the picture is drawn at,
// so frames are converted no larger than that; 0 is the source's size.
func (p *Pipeline) SetOutputSize(w, h int) {
	p.mu.Lock()
	p.outW, p.outH = w, h
	p.mu.Unlock()
}

// VideoTrack is the playing video track's ID, -1 for none.
func (p *Pipeline) VideoTrack() int { return p.videoStream }

// AudioTrack is the playing audio track's ID, -1 for none.
func (p *Pipeline) AudioTrack() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.audioStream
}

// SelectAudio plays the audio track id, or none for -1, from the current
// position.
func (p *Pipeline) SelectAudio(id int) error {
	if id >= 0 && p.track(id).Kind != Audio {
		return fmt.Errorf("pipeline: no audio track %d", id)
	}
	pos := p.Position()
	p.mu.Lock()
	if id == p.audioStream {
		p.mu.Unlock()
		return nil
	}
	p.audioStream = id
	p.setDiscards()
	p.mu.Unlock()
	p.reopenAudio.Store(true)
	p.Seek(pos, true)
	return nil
}

// AddSubtitles adds an SRT, WebVTT or ASS file as a subtitle track.
func (p *Pipeline) AddSubtitles(path string) (Track, error) {
	cues, err := ParseSubtitleFile(path)
	if err != nil {
		return Track{}, err
	}
	p.mu.Lock()
	id := p.externalID
	p.externalID++
	p.mu.Unlock()
	for i := range cues {
		cues[i].Start += p.start
		cues[i].End += p.start
	}
	p.cues.set(id, cues)
	t := Track{ID: id, Kind: Subtitle, Codec: subtitleCodec(path), External: true, Title: baseName(path)}
	return t, nil
}

// Cues are the subtitle track's cues showing at the current position,
// with times from the start.
func (p *Pipeline) Cues(track int) []Cue {
	cues := p.cues.at(track, p.lastPos)
	for i := range cues {
		cues[i].Start -= p.start
		cues[i].End -= p.start
	}
	return cues
}

// Buffered is how far from the start the demuxers have read: the least
// of the inputs being read.
func (p *Pipeline) Buffered() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := time.Duration(-1)
	for _, d := range p.inputs {
		if !p.feeds(d) {
			continue
		}
		v := d.buffered - p.start
		if d.eof && p.info.Duration > 0 {
			v = p.info.Duration
		}
		if b < 0 || v < b {
			b = v
		}
	}
	return max(b, 0)
}

// Position is the playing position from the start, as of the last Tick.
func (p *Pipeline) Position() time.Duration { return max(p.lastPos-p.start, 0) }
