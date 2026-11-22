package pipeline

import (
	"encoding/binary"
	"time"

	"github.com/oneo-me/nsgo-media/internal/audioout"
	"github.com/oneo-me/nsgo-media/internal/ffmpeg"
)

// Passthrough is the codecs whose audio passes undecoded, in IEC 61937
// bursts, to a receiver that decodes it, such as an amplifier on HDMI or
// S/PDIF.
type Passthrough int

const (
	PassAC3 Passthrough = 1 << iota
	PassEAC3
	// PassDTS passes DTS' core; enhanced DTS, such as DTS-HD, plays as
	// PCM.
	PassDTS
)

// passCodecs are the codecs each flag passes, by FFmpeg's name.
var passCodecs = map[string]Passthrough{"ac3": PassAC3, "eac3": PassEAC3, "dts": PassDTS}

// passes reports codec being one of c.
func (c Passthrough) passes(codec string) bool { return c&passCodecs[codec] != 0 }

// Fallback is why audio plays as PCM though passthrough was asked for
// its codec.
type Fallback int

const (
	FallbackNone Fallback = iota
	// FallbackRate is a rate other than 1, which only PCM plays.
	FallbackRate
	// FallbackEnhancedDTS is a DTS stream with extensions to its core,
	// such as DTS-HD, DTS-ES or DTS 96/24.
	FallbackEnhancedDTS
	// FallbackDevice is the device or the system not taking the stream,
	// refusing it or failing to start it; Output.Err says why.
	FallbackDevice
)

// Output is what the audio output plays.
type Output struct {
	// Passthrough is set while Codec passes through in IEC 61937 bursts.
	Passthrough bool
	Codec       string
	// Rate and Channels are the device stream's: the PCM's, or the IEC
	// 61937 carrier's, 2 channels at the codec's rate or four times it
	// for E-AC-3.
	Rate, Channels int
	Fallback       Fallback
	Err            error
}

// DTS profiles of FFmpeg's (AV_PROFILE_DTS*): the core, and above it the
// enhanced ones.
const (
	profileDTS     = 20
	profileUnknown = -99
)

// iecFormat is the IEC 61937 stream the audio track s plays as, with its
// first packet data, or nil and why it plays as PCM.
func (p *Pipeline) iecFormat(s *ffmpeg.Stream, data []byte) (*audioout.IEC, Fallback) {
	par := s.Params()
	codec := par.CodecName()
	if !Passthrough(p.pass.Load()).passes(codec) {
		return nil, FallbackNone
	}
	if p.Rate() != 1 {
		return nil, FallbackRate
	}
	if codec == "dts" {
		if prof := par.Profile(); prof != profileDTS && prof != profileUnknown || enhancedDTS(data) {
			return nil, FallbackEnhancedDTS
		}
	}
	return &audioout.IEC{Codec: codec, SampleRate: par.SampleRate(), Channels: par.Channels()}, FallbackNone
}

const (
	dtsCoreSync = 0x7ffe8001
	dtsExssSync = 0x64582025
)

// enhancedDTS reports a DTS frame carrying more than its core: an
// extension substream, alone as DTS Express' or after the core as
// DTS-HD's, or an XCh, X96 or XXCh extension in the core, as DTS-ES and
// DTS 96/24 carry.
func enhancedDTS(data []byte) bool {
	if len(data) < 11 {
		return false
	}
	be := binary.BigEndian
	switch be.Uint32(data) {
	case dtsExssSync:
		return true
	case dtsCoreSync:
	default:
		return false
	}
	if data[10]&0x10 != 0 { // EXT_AUDIO
		switch data[10] >> 5 { // EXT_AUDIO_ID
		case 0, 2, 6:
			return true
		}
	}
	size := (int(data[5]&0x03)<<12 | int(data[6])<<4 | int(data[7])>>4) + 1
	size = (size + 3) &^ 3
	return len(data) >= size+4 && be.Uint32(data[size:]) == dtsExssSync
}

// pauseUnit is a pause burst of IEC 61937: a 4-frame repetition period
// whose payload gives its gap's length in the encoded audio's samples.
// Gaps in a stream are filled with it, as receivers expect.
func pauseUnit(f *audioout.IEC) []byte {
	u := make([]byte, 16)
	le := binary.LittleEndian
	le.PutUint16(u[0:], 0xF872) // Pa
	le.PutUint16(u[2:], 0x4E1F) // Pb
	le.PutUint16(u[4:], 3)      // Pc: a pause
	le.PutUint16(u[6:], 32)     // Pd: 32 bits of payload
	le.PutUint16(u[8:], uint16(max(4*f.SampleRate/max(f.CarrierRate(), 1), 1)))
	return u
}

// held stands for the device while it changes: nothing plays, so the
// clock holds.
type held struct{ rate, channels int }

func (h held) Rate() int     { return h.rate }
func (h held) Channels() int { return h.channels }
func (held) Played() int64   { return 0 }
func (held) SetPaused(bool)  {}
func (held) Close()          {}

// device is the open output and the IEC 61937 stream it carries, nil for
// PCM.
type device struct {
	out audioout.Output
	iec *audioout.IEC
}

func (p *Pipeline) output() audioout.Output { return p.dev.Load().out }

func sameIEC(a, b *audioout.IEC) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// useOutput makes the device play f's IEC 61937 stream, or PCM for nil,
// opening it again when it plays another, and returns the error that
// made it play PCM instead. The ring is flushed, so nothing it holds is
// lost. It is called on the audio decoder's goroutine.
func (p *Pipeline) useOutput(f *audioout.IEC) error {
	cur := p.dev.Load()
	if sameIEC(cur.iec, f) || p.stopped() {
		return nil
	}
	// The old device lets go first: an exclusive stream needs it free.
	p.dev.Store(&device{out: held{cur.out.Rate(), cur.out.Channels()}, iec: cur.iec})
	cur.out.Close()
	var out audioout.Output
	var err error
	if f != nil {
		p.ring.reset(f.CarrierRate(), 2, f)
		out, err = p.openIEC(*f, p.ring.fillIEC)
		if err != nil || out == nil {
			f, out = nil, nil
		}
	}
	if f == nil {
		p.ring.reset(p.pcmRate, p.pcmChannels, nil)
		out, _ = p.openOutput(p.ring.fill)
		if out == nil {
			out = audioout.NewNull(p.pcmRate, p.pcmChannels, p.ring.fill)
		}
		if out.Rate() != p.pcmRate || out.Channels() != p.pcmChannels {
			p.pcmRate, p.pcmChannels = out.Rate(), out.Channels()
			p.ring.reset(p.pcmRate, p.pcmChannels, nil)
		}
	}
	p.outMu.Lock()
	// A PCM device pauses; an IEC 61937 stream sends pause bursts instead,
	// so the receiver stays locked to it.
	if f == nil {
		out.SetPaused(p.outPaused)
	}
	p.dev.Store(&device{out: out, iec: f})
	p.outMu.Unlock()
	if p.stopped() {
		out.Close()
	}
	return err
}

// setPausedOutput pauses or resumes a PCM device.
func (p *Pipeline) setPausedOutput(paused bool) {
	p.outMu.Lock()
	p.outPaused = paused
	if d := p.dev.Load(); d.iec == nil {
		d.out.SetPaused(paused)
	}
	p.outMu.Unlock()
}

func (p *Pipeline) setOutput(o Output) {
	p.outMu.Lock()
	p.outInfo = o
	p.outMu.Unlock()
}

// AudioOutput is what the audio output plays.
func (p *Pipeline) AudioOutput() Output {
	p.outMu.Lock()
	defer p.outMu.Unlock()
	return p.outInfo
}

// SetPassthrough passes the audio of codecs undecoded to a receiver, or
// none for 0. When that changes how the playing track plays, its sound
// starts again from the position, the picture holding for a moment.
func (p *Pipeline) SetPassthrough(c Passthrough) {
	old := Passthrough(p.pass.Swap(int64(c)))
	if p.opened.Load() && old.passes(p.audioCodec()) != c.passes(p.audioCodec()) {
		p.restartAudio()
	}
}

// audioCodec is the playing audio track's codec, empty for none.
func (p *Pipeline) audioCodec() string {
	if id := p.AudioTrack(); id >= 0 {
		return p.track(id).Codec
	}
	return ""
}

// restartAudio decodes the audio track again from the position, deciding
// again how it plays.
func (p *Pipeline) restartAudio() {
	p.reopenAudio.Store(true)
	p.seek(p.Position(), true)
}

// burstDuration is how long the frames of an IEC 61937 chunk play.
func burstDuration(c *chunk) time.Duration {
	return time.Duration(float64(len(c.data)/4) * c.perFrame)
}
