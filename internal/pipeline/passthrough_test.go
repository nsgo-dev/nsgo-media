package pipeline

import (
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo-media/internal/audioout"
	"github.com/nsgo-dev/nsgo-media/internal/ffmpeg"
)

// endpoint is a mock IEC 61937 device: it takes the streams refuse lets
// through, plays them paced as a device would, and records what it sent.
type endpoint struct {
	refuse func(audioout.IEC) error

	mu     sync.Mutex
	opened []audioout.IEC
	data   []byte // what the last stream sent
	open   int    // streams not closed
}

func (e *endpoint) openIEC(f audioout.IEC, fill audioout.FillIEC) (audioout.Output, error) {
	if e.refuse != nil {
		if err := e.refuse(f); err != nil {
			return nil, err
		}
	}
	e.mu.Lock()
	e.opened = append(e.opened, f)
	e.data = nil
	e.open++
	e.mu.Unlock()
	var closed atomic.Bool
	out := audioout.NewNullIEC(f, func(dst []byte) {
		fill(dst)
		if closed.Load() {
			panic("filled after Close")
		}
		e.mu.Lock()
		e.data = append(e.data, dst...)
		e.mu.Unlock()
	})
	return &endpointOutput{Output: out, e: e, closed: &closed}, nil
}

type endpointOutput struct {
	audioout.Output
	e      *endpoint
	closed *atomic.Bool
	once   sync.Once
}

func (o *endpointOutput) Close() {
	o.once.Do(func() {
		o.Output.Close()
		o.closed.Store(true)
		o.e.mu.Lock()
		o.e.open--
		o.e.mu.Unlock()
	})
}

// sent is what the last stream sent from byte from on, and its length.
func (e *endpoint) sent(from int) ([]byte, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if from > len(e.data) {
		from = len(e.data)
	}
	return append([]byte(nil), e.data[from:]...), len(e.data)
}

func (e *endpoint) streams() ([]audioout.IEC, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]audioout.IEC(nil), e.opened...), e.open
}

// bursts reads an IEC 61937 stream as its bursts, failing unless each
// follows the last without a break: data bursts of dataType every
// period bytes, pause bursts every 16. It returns how many of each start
// at byte from or after it.
func bursts(t *testing.T, s []byte, dataType uint16, period, from int) (data, pauses int) {
	t.Helper()
	le := binary.LittleEndian
	for i := 0; i+8 <= len(s); {
		if le.Uint16(s[i:]) != 0xF872 || le.Uint16(s[i+2:]) != 0x4E1F {
			t.Fatalf("no burst preamble at byte %d of %d", i, len(s))
		}
		at := i
		switch typ := le.Uint16(s[i+4:]) & 0x1f; typ {
		case 3:
			if le.Uint16(s[i+6:]) != 32 {
				t.Fatalf("pause burst with %d bits", le.Uint16(s[i+6:]))
			}
			i += 16
			if at >= from {
				pauses++
			}
		case dataType:
			i += period
			if at >= from {
				data++
			}
		default:
			t.Fatalf("burst of data type %d at byte %d", typ, i)
		}
	}
	return data, pauses
}

func openPassthrough(t *testing.T, e *endpoint, pass Passthrough) *Pipeline {
	t.Helper()
	return openPassthroughOf(t, "passthrough.mkv", e, pass)
}

func openPassthroughOf(t *testing.T, name string, e *endpoint, pass Passthrough) *Pipeline {
	t.Helper()
	p := New(Options{OpenOutput: silent, OpenPassthrough: e.openIEC, Passthrough: pass})
	if _, err := p.Open(fixture(t, name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// playFor plays p for d, ticking its presenter, and returns the last
// status.
func playFor(t *testing.T, p *Pipeline, d time.Duration) Status {
	t.Helper()
	p.SetPaused(false)
	until := time.Now().Add(d)
	return run(t, p, d+3*time.Second, func(Status) bool { return time.Now().After(until) })
}

func waitOutput(t *testing.T, p *Pipeline, cond func(Output) bool) Output {
	t.Helper()
	var o Output
	run(t, p, 3*time.Second, func(Status) bool {
		o = p.AudioOutput()
		return cond(o)
	})
	return o
}

// TestPassthroughSendsBursts plays each codec through and checks the
// device's format and stream: whole bursts of the codec, pause bursts
// only in its gaps, and the sound staying the clock the picture follows.
func TestPassthroughSendsBursts(t *testing.T) {
	if !ffmpeg.HasMuxer("spdif") {
		t.Skip("the loaded FFmpeg has no spdif muxer")
	}
	for _, tc := range []struct {
		track    int
		codec    string
		rate     int
		dataType uint16
		period   int
	}{
		{1, "ac3", 48000, 1, 1536 * 4},
		{2, "eac3", 192000, 21, 6144 * 4},
		{3, "dts", 48000, 11, 512 * 4},
	} {
		t.Run(tc.codec, func(t *testing.T) {
			e := &endpoint{}
			p := openPassthrough(t, e, PassAC3|PassEAC3|PassDTS)
			if tc.track != p.AudioTrack() {
				if err := p.SelectAudio(tc.track); err != nil {
					t.Fatal(err)
				}
			}
			o := waitOutput(t, p, func(o Output) bool { return o.Passthrough })
			if o.Codec != tc.codec || o.Rate != tc.rate || o.Channels != 2 || o.Fallback != FallbackNone {
				t.Fatalf("output %+v", o)
			}
			began := time.Now()
			st := playFor(t, p, 1200*time.Millisecond)
			elapsed := time.Since(began)
			if d := elapsed - st.Position; d < -100*time.Millisecond || d > 300*time.Millisecond {
				t.Errorf("position %v after %v of playing", st.Position, elapsed)
			}
			if d := shownPTS(p) - st.Position; d < -60*time.Millisecond || d > 60*time.Millisecond {
				t.Errorf("frame %v shows at %v", shownPTS(p), st.Position)
			}
			s, _ := e.sent(0)
			data, _ := bursts(t, s, tc.dataType, tc.period, 0)
			if data < 20 {
				t.Fatalf("%d data bursts", data)
			}
		})
	}
}

// TestPassthroughMutedSendsPauses checks muting sends pause bursts in the
// stream's place while the clock goes on, and unmuting the stream again.
func TestPassthroughMutedSendsPauses(t *testing.T) {
	if !ffmpeg.HasMuxer("spdif") {
		t.Skip("the loaded FFmpeg has no spdif muxer")
	}
	e := &endpoint{}
	p := openPassthrough(t, e, PassAC3)
	waitOutput(t, p, func(o Output) bool { return o.Passthrough })
	playFor(t, p, 300*time.Millisecond)
	p.SetMuted(true)
	// What is in the device's buffer and the burst being sent go first.
	playFor(t, p, 100*time.Millisecond)
	_, from := e.sent(0)
	before := p.Position()
	playFor(t, p, 400*time.Millisecond)
	if p.Position()-before < 300*time.Millisecond {
		t.Fatalf("the clock went from %v to %v muted", before, p.Position())
	}
	s, _ := e.sent(0)
	if data, pauses := bursts(t, s, 1, 1536*4, from); data > 0 || pauses < 1000 {
		t.Fatalf("muted: %d data bursts, %d pause bursts", data, pauses)
	}
	p.SetMuted(false)
	playFor(t, p, 300*time.Millisecond)
	s, _ = e.sent(0)
	if data, _ := bursts(t, s, 1, 1536*4, from); data < 3 {
		t.Fatalf("%d data bursts after unmuting", data)
	}
}

// TestPassthroughFallsBack checks each case that plays PCM though
// passthrough was asked for, and that the sound still plays then.
func TestPassthroughFallsBack(t *testing.T) {
	if !ffmpeg.HasMuxer("spdif") {
		t.Skip("the loaded FFmpeg has no spdif muxer")
	}
	refused := errors.New("the device takes no such format")
	for _, tc := range []struct {
		name     string
		pass     Passthrough
		track    int
		rate     float64
		refuse   func(audioout.IEC) error
		fallback Fallback
	}{
		{"codec not passed", PassDTS, 2, 1, nil, FallbackNone},
		{"rate", PassAC3, 1, 2, nil, FallbackRate},
		{"enhanced DTS", PassDTS, 4, 1, nil, FallbackEnhancedDTS},
		{"device refuses", PassAC3, 1, 1, func(audioout.IEC) error { return refused }, FallbackDevice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &endpoint{refuse: tc.refuse}
			p := New(Options{OpenOutput: silent, OpenPassthrough: e.openIEC, Passthrough: tc.pass})
			p.SetRate(tc.rate)
			if _, err := p.Open(fixture(t, "passthrough.mkv")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.Close)
			if tc.track != p.AudioTrack() {
				p.SelectAudio(tc.track)
			}
			st := playFor(t, p, 500*time.Millisecond)
			o := p.AudioOutput()
			if o.Passthrough || o.Fallback != tc.fallback || o.Rate != 48000 || o.Channels != 2 {
				t.Fatalf("output %+v", o)
			}
			if tc.fallback == FallbackDevice && !errors.Is(o.Err, refused) {
				t.Fatalf("error %v", o.Err)
			}
			if st.Position < 300*time.Millisecond {
				t.Fatalf("PCM played to %v", st.Position)
			}
			if streams, _ := e.streams(); len(streams) != 0 {
				t.Fatalf("streams %+v opened", streams)
			}
		})
	}
}

// TestPassthroughFollowsChanges checks the stream through seeking,
// changing the rate and the track, and closing.
func TestPassthroughFollowsChanges(t *testing.T) {
	if !ffmpeg.HasMuxer("spdif") {
		t.Skip("the loaded FFmpeg has no spdif muxer")
	}
	e := &endpoint{}
	p := New(Options{OpenOutput: silent, OpenPassthrough: e.openIEC, Passthrough: PassAC3 | PassEAC3})
	if _, err := p.Open(fixture(t, "passthrough.mkv")); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, p, func(o Output) bool { return o.Passthrough && o.Codec == "ac3" })
	playFor(t, p, 200*time.Millisecond)

	// A seek goes on in the same stream from the target.
	p.Seek(time.Second, true)
	st := playFor(t, p, 300*time.Millisecond)
	if st.Position < time.Second+150*time.Millisecond || st.Position > time.Second+600*time.Millisecond {
		t.Fatalf("at %v after seeking to 1 s and playing 300 ms", st.Position)
	}
	s, _ := e.sent(0)
	bursts(t, s, 1, 1536*4, 0)

	// Another rate plays PCM, and back at 1 the stream passes again.
	p.SetRate(1.5)
	if o := waitOutput(t, p, func(o Output) bool { return !o.Passthrough }); o.Fallback != FallbackRate {
		t.Fatalf("at 1.5×: %+v", o)
	}
	if _, open := e.streams(); open != 0 {
		t.Fatal("the stream stayed open at 1.5×")
	}
	p.SetRate(1)
	waitOutput(t, p, func(o Output) bool { return o.Passthrough })

	// Another track opens its own stream.
	p.Seek(0, false)
	p.SelectAudio(2)
	o := waitOutput(t, p, func(o Output) bool { return o.Codec == "eac3" })
	if o.Rate != 192000 {
		t.Fatalf("E-AC-3 output %+v", o)
	}
	playFor(t, p, 300*time.Millisecond)
	s, _ = e.sent(0)
	if data, _ := bursts(t, s, 21, 6144*4, 0); data < 3 {
		t.Fatalf("%d E-AC-3 bursts", data)
	}
	// Turning passthrough off plays PCM.
	p.SetPassthrough(0)
	if o := waitOutput(t, p, func(o Output) bool { return !o.Passthrough }); o.Fallback != FallbackNone {
		t.Fatalf("passthrough off: %+v", o)
	}
	p.SetPassthrough(PassEAC3)
	waitOutput(t, p, func(o Output) bool { return o.Passthrough })

	// No sound gives the device back to PCM.
	p.SelectAudio(-1)
	if o := waitOutput(t, p, func(o Output) bool { return !o.Passthrough }); o.Rate != 48000 {
		t.Fatalf("no sound: %+v", o)
	}
	if _, open := e.streams(); open != 0 {
		t.Fatal("the stream stayed open with no sound")
	}

	streams, _ := e.streams()
	if len(streams) != 4 || streams[0].Codec != "ac3" || streams[2].Codec != "eac3" || streams[2].Channels != 6 {
		t.Fatalf("streams %+v", streams)
	}
	p.Close()
	if _, open := e.streams(); open != 0 {
		t.Fatalf("%d streams open after Close", open)
	}
}

// refuseHBR is a device that takes no stream of the high bit rate.
var errNoHBR = errors.New("the device takes no high bit rate")

func refuseHBR(f audioout.IEC) error {
	if f.HBR() {
		return errNoHBR
	}
	return nil
}

// TestPassthroughSendsHBR plays hbr.mkv's DTS-HD Master Audio and TrueHD
// tracks through on HDMI's high bit rate carrier, 8 channels at 192 kHz,
// and checks the stream: whole DTS type IV bursts and MAT frames, pause
// bursts only in their gaps, and the sound staying the clock.
func TestPassthroughSendsHBR(t *testing.T) {
	if !ffmpeg.HasMuxer("spdif") {
		t.Skip("the loaded FFmpeg has no spdif muxer")
	}
	for _, tc := range []struct {
		track    int
		codec    string
		dataType uint16
		period   int
		min      int // of 112 DTS frames or 60 MAT frames in 1.2 s
	}{
		{1, "dtshd", 17, 32768, 90},
		{2, "truehd", 22, 61440, 50},
	} {
		t.Run(tc.codec, func(t *testing.T) {
			e := &endpoint{}
			p := openPassthroughOf(t, "hbr.mkv", e, PassDTS|PassDTSHD|PassTrueHD)
			if tc.track != p.AudioTrack() {
				if err := p.SelectAudio(tc.track); err != nil {
					t.Fatal(err)
				}
			}
			o := waitOutput(t, p, func(o Output) bool { return o.Passthrough && o.Codec == tc.codec })
			if o.Rate != 192000 || o.Channels != 8 || o.Fallback != FallbackNone {
				t.Fatalf("output %+v", o)
			}
			began := time.Now()
			st := playFor(t, p, 1200*time.Millisecond)
			elapsed := time.Since(began)
			if d := elapsed - st.Position; d < -100*time.Millisecond || d > 300*time.Millisecond {
				t.Errorf("position %v after %v of playing", st.Position, elapsed)
			}
			if d := shownPTS(p) - st.Position; d < -60*time.Millisecond || d > 60*time.Millisecond {
				t.Errorf("frame %v shows at %v", shownPTS(p), st.Position)
			}
			s, _ := e.sent(0)
			if len(s)%16 != 0 {
				t.Fatalf("%d bytes, not whole frames of 8 channels", len(s))
			}
			data, _ := bursts(t, s, tc.dataType, tc.period, 0)
			if data < tc.min {
				t.Fatalf("%d data bursts", data)
			}
			streams, _ := e.streams()
			if streams[len(streams)-1] != (audioout.IEC{Codec: tc.codec, SampleRate: 48000, Channels: 2}) {
				t.Fatalf("streams %+v", streams)
			}
		})
	}
}

// TestPassthroughHBRFallsBack checks what DTS-HD and TrueHD play as where
// the high bit rate does not pass: DTS-HD's core where the device takes
// no high bit rate and DTS passes, and PCM otherwise, with the reason.
func TestPassthroughHBRFallsBack(t *testing.T) {
	if !ffmpeg.HasMuxer("spdif") {
		t.Skip("the loaded FFmpeg has no spdif muxer")
	}
	refused := errors.New("the device takes no such format")
	for _, tc := range []struct {
		name     string
		pass     Passthrough
		track    int
		rate     float64
		refuse   func(audioout.IEC) error
		codec    string // passing through, empty for PCM
		fallback Fallback
		err      error
	}{
		{"DTS-HD without HBR passes its core", PassDTS | PassDTSHD, 1, 1, refuseHBR, "dts", FallbackDevice, errNoHBR},
		{"DTS-HD without HBR or DTS", PassDTSHD, 1, 1, refuseHBR, "", FallbackDevice, errNoHBR},
		{"DTS-HD refused", PassDTS | PassDTSHD, 1, 1, func(audioout.IEC) error { return refused }, "", FallbackDevice, refused},
		{"DTS-HD not passed", PassDTS, 1, 1, nil, "", FallbackEnhancedDTS, nil},
		{"DTS-HD at another rate", PassDTS | PassDTSHD, 1, 2, nil, "", FallbackRate, nil},
		{"TrueHD without HBR", PassTrueHD | PassDTS, 2, 1, refuseHBR, "", FallbackDevice, errNoHBR},
		{"TrueHD at another rate", PassTrueHD, 2, 0.5, nil, "", FallbackRate, nil},
		{"TrueHD not passed", PassDTS | PassDTSHD, 2, 1, nil, "", FallbackNone, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &endpoint{refuse: tc.refuse}
			p := New(Options{OpenOutput: silent, OpenPassthrough: e.openIEC, Passthrough: tc.pass})
			p.SetRate(tc.rate)
			if _, err := p.Open(fixture(t, "hbr.mkv")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.Close)
			if tc.track != p.AudioTrack() {
				p.SelectAudio(tc.track)
			}
			st := playFor(t, p, 500*time.Millisecond)
			o := p.AudioOutput()
			if o.Passthrough != (tc.codec != "") || o.Codec != tc.codec || o.Fallback != tc.fallback || !errors.Is(o.Err, tc.err) {
				t.Fatalf("output %+v", o)
			}
			if o.Rate != 48000 || o.Channels != 2 {
				t.Fatalf("output %+v", o)
			}
			if st.Position < 300*time.Millisecond*time.Duration(tc.rate*4)/4 {
				t.Fatalf("played to %v", st.Position)
			}
			streams, open := e.streams()
			if tc.codec != "" {
				if len(streams) != 1 || streams[0].Codec != tc.codec || open != 1 {
					t.Fatalf("streams %+v, %d open", streams, open)
				}
				s, _ := e.sent(0)
				if data, _ := bursts(t, s, 11, 512*4, 0); data < 20 {
					t.Fatalf("%d bursts of the core", data)
				}
			} else if len(streams) != 0 {
				t.Fatalf("streams %+v opened", streams)
			}
		})
	}
}

// TestPassthroughHBRFollowsChanges checks the high bit rate's stream
// through muting, seeking, changing the track and the rate, and closing.
func TestPassthroughHBRFollowsChanges(t *testing.T) {
	if !ffmpeg.HasMuxer("spdif") {
		t.Skip("the loaded FFmpeg has no spdif muxer")
	}
	e := &endpoint{}
	p := openPassthroughOf(t, "hbr.mkv", e, PassDTS|PassDTSHD|PassTrueHD)
	waitOutput(t, p, func(o Output) bool { return o.Codec == "dtshd" })
	playFor(t, p, 200*time.Millisecond)

	// Muted, a burst's time passes in pause bursts.
	p.SetMuted(true)
	playFor(t, p, 100*time.Millisecond)
	_, from := e.sent(0)
	before := p.Position()
	playFor(t, p, 300*time.Millisecond)
	if p.Position()-before < 200*time.Millisecond {
		t.Fatalf("the clock went from %v to %v muted", before, p.Position())
	}
	s, _ := e.sent(0)
	if data, pauses := bursts(t, s, 17, 32768, from); data > 0 || pauses < 1000 {
		t.Fatalf("muted: %d data bursts, %d pause bursts", data, pauses)
	}
	p.SetMuted(false)

	// A seek goes on in the same stream from the target.
	p.Seek(time.Second, true)
	st := playFor(t, p, 300*time.Millisecond)
	if st.Position < time.Second+150*time.Millisecond || st.Position > time.Second+600*time.Millisecond {
		t.Fatalf("at %v after seeking to 1 s and playing 300 ms", st.Position)
	}
	s, _ = e.sent(0)
	if data, _ := bursts(t, s, 17, 32768, from); data < 10 {
		t.Fatalf("%d bursts after unmuting and seeking", data)
	}
	if streams, _ := e.streams(); len(streams) != 1 {
		t.Fatalf("streams %+v", streams)
	}

	// Turning DTS-HD's passthrough off plays it as PCM, as enhanced DTS.
	p.SetPassthrough(PassDTS | PassTrueHD)
	if o := waitOutput(t, p, func(o Output) bool { return !o.Passthrough }); o.Fallback != FallbackEnhancedDTS {
		t.Fatalf("DTS-HD's passthrough off: %+v", o)
	}
	p.SetPassthrough(PassDTS | PassDTSHD | PassTrueHD)
	waitOutput(t, p, func(o Output) bool { return o.Codec == "dtshd" })

	// TrueHD's MAT frames start anew after a seek.
	p.Seek(0, false)
	p.SelectAudio(2)
	waitOutput(t, p, func(o Output) bool { return o.Codec == "truehd" })
	playFor(t, p, 300*time.Millisecond)
	p.Seek(500*time.Millisecond, true)
	playFor(t, p, 400*time.Millisecond)
	s, _ = e.sent(0)
	if data, _ := bursts(t, s, 22, 61440, 0); data < 10 {
		t.Fatalf("%d MAT frames", data)
	}

	// Another rate plays PCM, and back at 1 TrueHD passes again.
	p.SetRate(2)
	if o := waitOutput(t, p, func(o Output) bool { return !o.Passthrough }); o.Fallback != FallbackRate {
		t.Fatalf("at 2×: %+v", o)
	}
	if _, open := e.streams(); open != 0 {
		t.Fatal("the stream stayed open at 2×")
	}
	p.SetRate(1)
	waitOutput(t, p, func(o Output) bool { return o.Passthrough })

	streams, _ := e.streams()
	codecs := ""
	for _, f := range streams {
		codecs += f.Codec + " "
	}
	if codecs != "dtshd dtshd truehd truehd " {
		t.Fatalf("streams %s", codecs)
	}
	p.Close()
	if _, open := e.streams(); open != 0 {
		t.Fatalf("%d streams open after Close", open)
	}
}

func TestDTSHD(t *testing.T) {
	core := []byte{0x7f, 0xfe, 0x80, 0x01, 0xfc, 0x3c, 0x1a, 0xb0, 0xb5, 0x20, 0x01, 0x38}
	size := (int(core[5]&0x03)<<12 | int(core[6])<<4 | int(core[7])>>4) + 1
	hd := append(append(append([]byte(nil), core...), make([]byte, (size+3)&^3-len(core))...), 0x64, 0x58, 0x20, 0x25)
	if dtsHD(profileDTS, core) || !dtsHD(profileUnknown, hd) || !dtsHD(profileDTSMA, core) {
		t.Error("a core is DTS-HD, or DTS-HD is not")
	}
	es := append([]byte(nil), core...)
	es[10] = 0x10 // XCh
	if dtsHD(profileUnknown, es) || dtsHD(70, []byte{0x64, 0x58, 0x20, 0x25}) {
		t.Error("DTS-ES or DTS Express is DTS-HD")
	}
}

func TestEnhancedDTS(t *testing.T) {
	core := []byte{0x7f, 0xfe, 0x80, 0x01, 0xfc, 0x3c, 0x1a, 0xb0, 0xb5, 0x20, 0x01, 0x38}
	if enhancedDTS(core) {
		t.Error("a core frame header is enhanced")
	}
	es := append([]byte(nil), core...)
	es[10] = 0x10 // XCh
	if !enhancedDTS(es) {
		t.Error("DTS-ES is not enhanced")
	}
	if !enhancedDTS([]byte{0x64, 0x58, 0x20, 0x25, 0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Error("an extension substream is not enhanced")
	}
}
