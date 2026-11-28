package pipeline

import (
	"encoding/binary"
	"time"

	"github.com/nsgo-dev/nsgo-media/internal/audioout"
	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

// Passthrough is the codecs whose audio passes undecoded, in IEC 61937
// bursts, to a receiver that decodes it, such as an amplifier on HDMI or
// S/PDIF.
type Passthrough int

const (
	PassAC3 Passthrough = 1 << iota
	PassEAC3
	// PassDTS passes DTS' core: DTS-HD's too where PassDTSHD is not set
	// or the device takes no high bit rate. Other enhanced DTS, such as
	// DTS-ES, 96/24 or Express, plays as PCM.
	PassDTS
	// PassDTSHD passes DTS-HD's whole frames, core and extension
	// substream, on HDMI's high bit rate carrier.
	PassDTSHD
	// PassTrueHD passes TrueHD, with whatever its stream carries, such as
	// Atmos, in MAT frames on HDMI's high bit rate carrier.
	PassTrueHD
)

// passCodecs are the flags that decide how a codec plays, by FFmpeg's
// name.
var passCodecs = map[string]Passthrough{"ac3": PassAC3, "eac3": PassEAC3, "dts": PassDTS | PassDTSHD, "truehd": PassTrueHD}

// Fallback is why audio plays otherwise than passthrough asked: as PCM,
// or DTS-HD as its core.
type Fallback int

const (
	FallbackNone Fallback = iota
	// FallbackRate is a rate other than 1, which only PCM plays.
	FallbackRate
	// FallbackEnhancedDTS is a DTS stream with extensions to its core that
	// do not pass, such as DTS-HD without PassDTSHD, DTS-ES, DTS 96/24 or
	// DTS Express.
	FallbackEnhancedDTS
	// FallbackDevice is the device or the system not taking the stream,
	// refusing it or failing to start it; Output.Err says why. DTS-HD then
	// passes its core when PassDTS is set.
	FallbackDevice
)

// Output is what the audio output plays.
type Output struct {
	// Passthrough is set while Codec passes through in IEC 61937 bursts:
	// "ac3", "eac3", "dts" (DTS' core), "dtshd" or "truehd".
	Passthrough bool
	Codec       string
	// Rate and Channels are the device stream's: the PCM's, or the IEC
	// 61937 carrier's, 2 channels at the codec's rate or four times it
	// for E-AC-3, and 8 at 192 kHz for DTS-HD and TrueHD.
	Rate, Channels int
	Fallback       Fallback
	Err            error
}

// DTS profiles of FFmpeg's (AV_PROFILE_DTS*): the core, DTS-HD's, and
// the unknown one.
const (
	profileDTS     = 20
	profileDTSHRA  = 50
	profileDTSMA   = 60
	profileDTSMAX  = 61
	profileDTSIMAX = 62
	profileUnknown = -99
)

// iecFormats are the IEC 61937 streams the audio track s may play as,
// with its first packet data, the first that opens playing; none and why
// it plays as PCM otherwise.
func (p *Pipeline) iecFormats(s *ffmpeg.Stream, data []byte) ([]audioout.IEC, Fallback) {
	par := s.Params()
	codec := par.CodecName()
	pass := Passthrough(p.pass.Load())
	hd := false
	if codec == "dts" {
		prof := par.Profile()
		hd = pass&PassDTSHD != 0 && dtsHD(prof, data)
		if !hd && pass&PassDTS != 0 && (prof != profileDTS && prof != profileUnknown || enhancedDTS(data)) {
			if p.Rate() != 1 {
				return nil, FallbackRate
			}
			return nil, FallbackEnhancedDTS
		}
	}
	if pass&passCodecs[codec]&^PassDTSHD == 0 && !hd {
		return nil, FallbackNone
	}
	if p.Rate() != 1 {
		return nil, FallbackRate
	}
	f := audioout.IEC{Codec: codec, SampleRate: par.SampleRate(), Channels: par.Channels()}
	if !hd {
		return []audioout.IEC{f}, FallbackNone
	}
	fs := []audioout.IEC{{Codec: "dtshd", SampleRate: f.SampleRate, Channels: f.Channels}}
	if pass&PassDTS != 0 {
		fs = append(fs, f)
	}
	return fs, FallbackNone
}

// dtsHDRate is the IEC 61937 rate the spdif muxer packs f's DTS type IV
// bursts at, 0 for those of DTS' core.
func dtsHDRate(f audioout.IEC) int {
	if f.Codec == "dtshd" {
		return f.CarrierRate() * f.CarrierChannels() / 2
	}
	return 0
}

const (
	dtsCoreSync = 0x7ffe8001
	dtsExssSync = 0x64582025
)

// dtsHD reports a DTS stream of profile prof, whose first packet is data,
// being DTS-HD with a core: High Resolution or Master Audio, whose frames
// are a core and an extension substream.
func dtsHD(prof int32, data []byte) bool {
	switch prof {
	case profileDTSHRA, profileDTSMA, profileDTSMAX, profileDTSIMAX:
		// A first packet may be a stray substream before the first core,
		// as DTS-HD streams begin with.
		return true
	}
	return coreExss(data)
}

// coreExss reports data being a DTS core frame followed by an extension
// substream, as DTS-HD lays them out.
func coreExss(data []byte) bool {
	be := binary.BigEndian
	if len(data) < 11 || be.Uint32(data) != dtsCoreSync {
		return false
	}
	size := (int(data[5]&0x03)<<12 | int(data[6])<<4 | int(data[7])>>4) + 1
	size = (size + 3) &^ 3
	return len(data) >= size+4 && be.Uint32(data[size:]) == dtsExssSync
}

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
	return coreExss(data)
}

// pauseUnit is a pause burst of IEC 61937: a repetition period of 4
// stereo frames, 16 bytes, one frame of the high bit rate's carrier,
// whose payload gives its gap's length in the encoded audio's samples.
// Gaps in a stream are filled with it, as receivers expect.
func pauseUnit(f *audioout.IEC) []byte {
	u := make([]byte, 16)
	le := binary.LittleEndian
	le.PutUint16(u[0:], 0xF872)                          // Pa
	le.PutUint16(u[2:], 0x4E1F)                          // Pb
	le.PutUint16(u[4:], 3)                               // Pc: a pause
	le.PutUint16(u[6:], 32)                              // Pd: 32 bits of payload
	iecRate := f.CarrierRate() * f.CarrierChannels() / 2 // of 2 words a frame
	le.PutUint16(u[8:], uint16(max(4*f.SampleRate/max(iecRate, 1), 1)))
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

// useOutput makes the device play the first of fs' IEC 61937 streams
// that opens, or PCM when none does or fs is empty, opening it again
// unless it plays fs' first already. It returns the stream it plays, nil
// for PCM, and the error of the first that failed to open. The ring is
// flushed, so nothing it holds is lost. It is called on the audio
// decoder's goroutine.
func (p *Pipeline) useOutput(fs []audioout.IEC) (*audioout.IEC, error) {
	cur := p.dev.Load()
	if len(fs) == 0 && cur.iec == nil || len(fs) > 0 && cur.iec != nil && *cur.iec == fs[0] || p.stopped() {
		return cur.iec, nil
	}
	// The old device lets go first: an exclusive stream needs it free.
	p.dev.Store(&device{out: held{cur.out.Rate(), cur.out.Channels()}, iec: cur.iec})
	cur.out.Close()
	var out audioout.Output
	var f *audioout.IEC
	var first error
	for _, c := range fs {
		p.ring.reset(c.CarrierRate(), c.CarrierChannels(), &c)
		o, err := p.openIEC(c, p.ring.fillIEC)
		if err == nil && o != nil {
			out, f = o, &c
			break
		}
		if first == nil {
			first = err
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
	return f, first
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
	if p.opened.Load() && (old^c)&p.audioPass() != 0 {
		p.restartAudio()
	}
}

// audioPass is the flags that decide how the playing audio track plays:
// its codec's, or once its first packet decided, a DTS core's PassDTS
// alone.
func (p *Pipeline) audioPass() Passthrough {
	if m := Passthrough(p.passMask.Load()); m != 0 {
		return m
	}
	if id := p.AudioTrack(); id >= 0 {
		return passCodecs[p.track(id).Codec]
	}
	return 0
}

// restartAudio decodes the audio track again from the position, deciding
// again how it plays.
func (p *Pipeline) restartAudio() {
	p.reopenAudio.Store(true)
	p.seek(p.Position(), true)
}

// burstDuration is how long the frames of an IEC 61937 chunk, of ch
// carrier channels, play.
func burstDuration(c *chunk, ch int) time.Duration {
	return time.Duration(float64(len(c.data)/(2*ch)) * c.perFrame)
}
