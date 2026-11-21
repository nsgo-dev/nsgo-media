// Package player is NSGo Media's public API: a Player opened on a media
// source, its commands, its state as NSGo signals, and View, the component
// that draws its picture. The app builds every control itself.
//
// A Player belongs to the scope that made it and to its engine's
// goroutine: call its methods from components, effects and event handlers.
// It decodes on its own goroutines and closes when its scope is disposed.
package player

import (
	"errors"
	"image"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/oneo-me/nsgo-media/internal/audioout"
	"github.com/oneo-me/nsgo-media/internal/pipeline"
)

// Player plays one source at a time.
type Player struct {
	cx *core.Scope
	pl *pipeline.Pipeline
	// seq tells an Open's result apart from a later Open's or Close's.
	seq       uint64
	stopFrame func()
	views     map[*viewSize]struct{}
	software  bool
	// openOutput replaces the audio device in tests.
	openOutput func(audioout.Fill) (audioout.Output, error)

	// What the app asked for, applied when the source opens.
	wantPlay   bool
	pendSeek   *pendingSeek
	rateV      float64
	volumeV    float64
	mutedV     bool
	loopV      bool
	pendSubs   []string
	subtitleID int

	state      *core.Signal[State]
	err        *core.Signal[error]
	info       *core.Signal[*Info]
	position   *core.Signal[time.Duration]
	buffered   *core.Signal[time.Duration]
	frame      *core.Signal[frameRef]
	audioTrack *core.Signal[int]
	videoTrack *core.Signal[int]
	subTrack   *core.Signal[int]
	cues       *core.Signal[[]Cue]
	rate       *core.Signal[float64]
	volume     *core.Signal[float64]
	muted      *core.Signal[bool]
	loop       *core.Signal[bool]
	hardware   *core.Signal[string]
}

type pendingSeek struct {
	t    time.Duration
	mode SeekMode
}

// frameRef is the picture a view shows: a buffer and its version.
type frameRef struct {
	img     *image.RGBA
	version uint64
}

// Option configures a player.
type Option func(*Player)

// Software turns hardware video decoding off.
func Software() Option { return func(p *Player) { p.software = true } }

// New makes a player owned by cx, closed when cx is disposed.
func New(cx *core.Scope, opts ...Option) *Player {
	p := &Player{
		cx:         cx,
		views:      map[*viewSize]struct{}{},
		rateV:      1,
		volumeV:    1,
		subtitleID: -1,
		state:      core.NewSignal(cx, Idle),
		err:        core.NewSignalFunc[error](cx, nil, func(a, b error) bool { return a == nil && b == nil }),
		info:       core.NewSignal[*Info](cx, nil),
		position:   core.NewSignal(cx, time.Duration(0)),
		buffered:   core.NewSignal(cx, time.Duration(0)),
		frame:      core.NewSignal(cx, frameRef{}),
		audioTrack: core.NewSignal(cx, -1),
		videoTrack: core.NewSignal(cx, -1),
		subTrack:   core.NewSignal(cx, -1),
		cues:       core.NewSignalFunc[[]Cue](cx, nil, sameCues),
		rate:       core.NewSignal(cx, 1.0),
		volume:     core.NewSignal(cx, 1.0),
		muted:      core.NewSignal(cx, false),
		loop:       core.NewSignal(cx, false),
		hardware:   core.NewSignal(cx, ""),
	}
	for _, o := range opts {
		o(p)
	}
	cx.Cleanup(p.Close)
	return p
}

// Open opens source, a file path or an http(s) URL, closing what was open.
// The player is Opening until it can show the first frame, then Paused,
// or Playing after Play.
func (p *Player) Open(source string) {
	p.closePipeline()
	p.seq++
	seq := p.seq
	p.err.Set(nil)
	p.info.Set(nil)
	p.position.Set(0)
	p.buffered.Set(0)
	p.cues.Set(nil)
	p.audioTrack.Set(-1)
	p.videoTrack.Set(-1)
	p.subtitleID = -1
	p.subTrack.Set(-1)
	p.hardware.Set("")
	p.pendSeek = nil
	p.pendSubs = nil
	p.state.Set(Opening)

	var pl *pipeline.Pipeline
	pl = pipeline.New(pipeline.Options{
		Software:   p.software,
		OpenOutput: p.openOutput,
		OnEvent: func(e pipeline.Event) {
			p.cx.Post(func() { p.event(seq, e) })
		},
	})
	go func() {
		info, err := pl.Open(source)
		p.cx.Post(func() {
			if seq != p.seq {
				go pl.Close()
				return
			}
			if err != nil {
				pl.Close()
				p.fail(err)
				return
			}
			p.opened(pl, info)
		})
	}()
	// Close during the open stops it: keep the pipeline to close.
	p.pl = pl
}

func (p *Player) opened(pl *pipeline.Pipeline, info *pipeline.Info) {
	p.pl = pl
	p.info.Set(infoOf(info))
	p.audioTrack.Set(pl.AudioTrack())
	p.videoTrack.Set(pl.VideoTrack())
	p.hardware.Set(pl.HardwareDevice())
	pl.SetRate(p.rateV)
	pl.SetVolume(p.volumeV)
	pl.SetMuted(p.mutedV)
	pl.SetLoop(p.loopV)
	p.updateOutputSize()
	for _, path := range p.pendSubs {
		p.addSubtitles(path)
	}
	p.pendSubs = nil
	if s := p.pendSeek; s != nil {
		pl.Seek(s.t, s.mode == Exact)
		p.pendSeek = nil
	}
	pl.SetPaused(!p.wantPlay)
	p.startFrames()
}

// ready reports an opened pipeline to command.
func (p *Player) ready() bool { return p.pl != nil && p.info.GetUntracked() != nil }

func (p *Player) fail(err error) {
	p.err.Set(err)
	p.state.Set(Failed)
}

func (p *Player) event(seq uint64, e pipeline.Event) {
	if seq != p.seq {
		return
	}
	switch e.Kind {
	case pipeline.EventError:
		p.fail(e.Err)
	case pipeline.EventHardware:
		p.hardware.Set(e.Device)
	}
}

// startFrames runs the presenter at every display frame.
func (p *Player) startFrames() {
	if p.stopFrame != nil {
		return
	}
	var loop func(time.Time)
	loop = func(time.Time) {
		p.tick()
		if p.pl != nil {
			p.stopFrame = p.cx.RequestAnimationFrame(loop)
		} else {
			p.stopFrame = nil
		}
	}
	p.stopFrame = p.cx.RequestAnimationFrame(loop)
}

func (p *Player) tick() {
	if !p.ready() {
		return
	}
	st := p.pl.Tick()
	if st.Frame != nil {
		v := p.frame.GetUntracked().version + 1
		p.frame.Set(frameRef{img: st.Frame.Image, version: v})
	}
	p.position.Set(st.Position)
	p.buffered.Set(max(p.pl.Buffered(), st.Position))
	if id := p.subtitleID; id >= 0 {
		p.cues.Set(cuesOf(p.pl.Cues(id)))
	}
	if p.state.GetUntracked() == Failed {
		return
	}
	switch {
	case st.Ended:
		p.wantPlay = false
		p.state.Set(Ended)
	case p.pl.Paused():
		p.state.Set(Paused)
	case st.Buffering:
		p.state.Set(Buffering)
	default:
		p.state.Set(Playing)
	}
}

// Play plays, from the start when it ended.
func (p *Player) Play() {
	p.wantPlay = true
	if p.ready() {
		p.pl.SetPaused(false)
		p.tick()
	}
}

// Pause pauses.
func (p *Player) Pause() {
	p.wantPlay = false
	if p.ready() {
		p.pl.SetPaused(true)
		p.tick()
	}
}

// Stop pauses at the start.
func (p *Player) Stop() {
	p.Pause()
	p.Seek(0, Fast)
}

// Seek moves to t from the start.
func (p *Player) Seek(t time.Duration, mode SeekMode) {
	if !p.ready() {
		p.pendSeek = &pendingSeek{t, mode}
		return
	}
	p.pl.Seek(t, mode == Exact)
	p.position.Set(p.pl.Position())
	if p.state.GetUntracked() == Ended {
		p.state.Set(Paused)
	}
}

// StepForward shows the next frame while paused.
func (p *Player) StepForward() {
	if p.ready() {
		p.pl.Step()
	}
}

// SetRate sets the speed from 0.25 to 4, keeping the pitch.
func (p *Player) SetRate(r float64) {
	p.rateV = min(max(r, 0.25), 4)
	p.rate.Set(p.rateV)
	if p.ready() {
		p.pl.SetRate(p.rateV)
	}
}

// SetVolume sets the volume from 0 to 1.
func (p *Player) SetVolume(v float64) {
	p.volumeV = min(max(v, 0), 1)
	p.volume.Set(p.volumeV)
	if p.ready() {
		p.pl.SetVolume(p.volumeV)
	}
}

// SetMuted silences or restores the sound.
func (p *Player) SetMuted(m bool) {
	p.mutedV = m
	p.muted.Set(m)
	if p.ready() {
		p.pl.SetMuted(m)
	}
}

// SetLoop makes playback start over at the end.
func (p *Player) SetLoop(l bool) {
	p.loopV = l
	p.loop.Set(l)
	if p.ready() {
		p.pl.SetLoop(l)
	}
}

// SelectAudio plays the audio track id, or no sound for -1.
func (p *Player) SelectAudio(id int) error {
	if !p.ready() {
		return errors.New("player: no source is open")
	}
	if err := p.pl.SelectAudio(id); err != nil {
		return err
	}
	p.audioTrack.Set(p.pl.AudioTrack())
	return nil
}

// SelectSubtitle delivers the cues of the subtitle track id, or none for
// -1.
func (p *Player) SelectSubtitle(id int) error {
	if id >= 0 {
		info := p.info.GetUntracked()
		found := false
		if info != nil {
			for _, t := range info.Tracks {
				found = found || t.ID == id && t.Kind == Subtitle
			}
		}
		if !found {
			return errors.New("player: no such subtitle track")
		}
	}
	p.subtitleID = id
	p.subTrack.Set(id)
	if id < 0 {
		p.cues.Set(nil)
	} else if p.ready() {
		p.cues.Set(cuesOf(p.pl.Cues(id)))
	}
	return nil
}

// AddSubtitles adds an SRT, WebVTT or ASS file as a subtitle track of the
// open source, which Info lists and SelectSubtitle selects. It reads the
// file at once; before the source opens it waits for it.
func (p *Player) AddSubtitles(path string) error {
	if !p.ready() {
		if p.pl == nil {
			return errors.New("player: no source is open")
		}
		p.pendSubs = append(p.pendSubs, path)
		return nil
	}
	return p.addSubtitles(path)
}

func (p *Player) addSubtitles(path string) error {
	t, err := p.pl.AddSubtitles(path)
	if err != nil {
		return err
	}
	info := *p.info.GetUntracked()
	info.Tracks = append(append([]Track(nil), info.Tracks...), trackOf(t))
	p.info.Set(&info)
	return nil
}

// Close stops playback and every goroutine of the player, returning once
// they have exited; no signal changes after it. The player may open
// another source.
func (p *Player) Close() {
	p.seq++
	p.closePipeline()
	p.wantPlay = false
	p.info.Set(nil)
	p.state.Set(Idle)
}

func (p *Player) closePipeline() {
	if p.stopFrame != nil {
		p.stopFrame()
		p.stopFrame = nil
	}
	// Views stop pointing at frames before their memory goes.
	p.frame.Set(frameRef{})
	if p.pl != nil {
		p.pl.Close()
		p.pl = nil
	}
}

// State is what the player is doing.
func (p *Player) State() State { return p.state.Get() }

// Err is why the player failed, nil unless State is Failed.
func (p *Player) Err() error { return p.err.Get() }

// Info is the open source's information, nil until it opened.
func (p *Player) Info() *Info { return p.info.Get() }

// Duration is the source's duration, 0 when unknown or not open.
func (p *Player) Duration() time.Duration {
	if i := p.info.Get(); i != nil {
		return i.Duration
	}
	return 0
}

// Position is the playing position, updated at every display frame.
func (p *Player) Position() time.Duration { return p.position.Get() }

// Buffered is how far from the start the source is read: the buffered
// range runs from Position to it.
func (p *Player) Buffered() time.Duration { return p.buffered.Get() }

// AudioTrack and SubtitleTrack are the chosen tracks' IDs, -1 for none.
func (p *Player) AudioTrack() int    { return p.audioTrack.Get() }
func (p *Player) SubtitleTrack() int { return p.subTrack.Get() }

// Cues are the chosen subtitle track's cues showing now, for the app to
// draw.
func (p *Player) Cues() []Cue { return p.cues.Get() }

// Rate, Volume, Muted and Loop are what the setters last set.
func (p *Player) Rate() float64   { return p.rate.Get() }
func (p *Player) Volume() float64 { return p.volume.Get() }
func (p *Player) Muted() bool     { return p.muted.Get() }
func (p *Player) Loop() bool      { return p.loop.Get() }

// HardwareDevice is the device video decodes on: "videotoolbox",
// "d3d11va" or "vaapi", or empty for software decoding.
func (p *Player) HardwareDevice() string { return p.hardware.Get() }

// VideoSize is the size the playing video shows at, in pixels: its
// pixels' shape and rotation applied. It is zero without video.
func (p *Player) VideoSize() (w, h int) {
	id := p.videoTrack.Get()
	info := p.info.Get()
	if info == nil || id < 0 {
		return 0, 0
	}
	for _, t := range info.Tracks {
		if t.ID == id {
			w, h = int(float64(t.Width)*max(t.AspectRatio, 0.01)+0.5), t.Height
			if t.Rotation == 90 || t.Rotation == 270 {
				w, h = h, w
			}
			return w, h
		}
	}
	return 0, 0
}
