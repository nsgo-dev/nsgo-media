package ffmpeg

import (
	"bytes"
	"encoding/binary"
	"sync/atomic"
	"testing"
)

// TestPackerWrapsBursts packs each track of passthrough.mkv and checks
// the IEC 61937 bursts: their preamble, data type and payload length,
// the payload being the packet, and one burst per repetition period.
func TestPackerWrapsBursts(t *testing.T) {
	mustLoad(t)
	if !HasMuxer("spdif") {
		t.Skip("the loaded FFmpeg has no spdif muxer")
	}
	for _, tc := range []struct {
		name     string
		stream   int
		codec    string
		burst    int    // bytes of one burst, its repetition period
		dataType uint16 // Pc's data type
		bytesPd  bool   // Pd counts bytes, not bits
		core     int    // bytes of each packet the payload carries, 0 for all
	}{
		{"ac3", 1, "ac3", 1536 * 4, 1, false, 0},
		{"eac3", 2, "eac3", 6144 * 4, 21, true, 0},
		{"dts", 3, "dts", 512 * 4, 11, false, 0},
		{"dts with an extension", 4, "dts", 512 * 4, 11, false, 428}, // only the core of a frame with an extension
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := Open(fixture(t, "passthrough.mkv"), nil, new(atomic.Bool))
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			s := in.Format.Streams()[tc.stream]
			if s.Params().CodecName() != tc.codec {
				t.Fatalf("stream %d is %s", tc.stream, s.Params().CodecName())
			}
			k, err := NewPacker(s.Params(), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer k.Close()
			pkt := NewPacket()
			defer pkt.Free()
			bursts := 0
			for in.Format.ReadPacket(pkt) == nil {
				if pkt.StreamIndex() != tc.stream {
					pkt.Unref()
					continue
				}
				data := append([]byte(nil), pkt.Data()...)
				out, err := k.Pack(pkt)
				pkt.Unref()
				if err != nil {
					t.Fatal(err)
				}
				if len(out) != tc.burst {
					t.Fatalf("burst of %d bytes, want %d", len(out), tc.burst)
				}
				le := binary.LittleEndian
				if le.Uint16(out) != 0xF872 || le.Uint16(out[2:]) != 0x4E1F {
					t.Fatalf("preamble % x", out[:4])
				}
				if typ := le.Uint16(out[4:]) & 0x1f; typ != tc.dataType {
					t.Fatalf("data type %d, want %d", typ, tc.dataType)
				}
				n := int(le.Uint16(out[6:]))
				if !tc.bytesPd {
					n /= 8
				}
				want := len(data)
				if tc.core > 0 {
					want = tc.core
				}
				if n != want {
					t.Fatalf("payload of %d bytes, want %d", n, want)
				}
				// The payload is the packet in 16-bit little-endian words.
				for i := 0; i+1 < want; i += 2 {
					if out[8+i] != data[i+1] || out[9+i] != data[i] {
						t.Fatalf("payload differs at %d", i)
					}
				}
				bursts++
			}
			if bursts < 60 {
				t.Fatalf("%d bursts", bursts)
			}
		})
	}
}

// words turns a burst's payload of 16-bit little-endian words back into
// the bytes they carry.
func words(b []byte) []byte {
	out := make([]byte, len(b)&^1)
	for i := 0; i+1 < len(b); i += 2 {
		out[i], out[i+1] = b[i+1], b[i]
	}
	return out
}

// packTrack packs the packets of hbr.mkv's stream, returning each
// packet's data and what Pack returned for it.
func packTrack(t *testing.T, stream int, codec string, dtsHDRate int) (packets, outs [][]byte) {
	t.Helper()
	mustLoad(t)
	if !HasMuxer("spdif") {
		t.Skip("the loaded FFmpeg has no spdif muxer")
	}
	in, err := Open(fixture(t, "hbr.mkv"), nil, new(atomic.Bool))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	s := in.Format.Streams()[stream]
	if s.Params().CodecName() != codec {
		t.Fatalf("stream %d is %s", stream, s.Params().CodecName())
	}
	k, err := NewPacker(s.Params(), dtsHDRate)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	pkt := NewPacket()
	defer pkt.Free()
	for in.Format.ReadPacket(pkt) == nil {
		if pkt.StreamIndex() != stream {
			pkt.Unref()
			continue
		}
		data := append([]byte(nil), pkt.Data()...)
		out, err := k.Pack(pkt)
		pkt.Unref()
		if err != nil {
			t.Fatal(err)
		}
		packets, outs = append(packets, data), append(outs, append([]byte(nil), out...))
	}
	return packets, outs
}

// TestPackerWrapsDTSHD packs hbr.mkv's DTS-HD Master Audio track at the
// high bit rate: one DTS type IV burst a frame, 32768 bytes (2048 frames
// of the 8-channel carrier, a 512-sample frame at 48 kHz), whose payload
// is the type IV start code, the frame's size and the whole frame, its
// core and extension substream.
func TestPackerWrapsDTSHD(t *testing.T) {
	packets, outs := packTrack(t, 1, "dts", 768000)
	le := binary.LittleEndian
	start := []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0xfe, 0xfe}
	for i, out := range outs {
		data := packets[i]
		if len(out) != 32768 {
			t.Fatalf("packet %d: burst of %d bytes", i, len(out))
		}
		if le.Uint16(out) != 0xF872 || le.Uint16(out[2:]) != 0x4E1F {
			t.Fatalf("preamble % x", out[:4])
		}
		// Pc: DTS type IV (17), subtype 4 for a period of 8192 stereo frames.
		if pc := le.Uint16(out[4:]); pc != 17|4<<8 {
			t.Fatalf("Pc %#x", pc)
		}
		n := len(start) + 2 + len(data)
		if pd := int(le.Uint16(out[6:])); pd < n || pd&0xf != 8 {
			t.Fatalf("Pd %d for %d bytes", pd, n)
		}
		payload := words(out[8 : 8+n+1])
		if !bytes.Equal(payload[:len(start)], start) || int(binary.BigEndian.Uint16(payload[len(start):])) != len(data) {
			t.Fatalf("type IV header % x", payload[:12])
		}
		if !bytes.Equal(payload[12:12+len(data)], data) {
			t.Fatalf("packet %d is not the payload", i)
		}
		if binary.BigEndian.Uint32(data[(428+3)&^3:]) != 0x64582025 {
			t.Fatalf("packet %d has no extension substream after its core", i)
		}
	}
	if len(outs) < 150 {
		t.Fatalf("%d bursts", len(outs))
	}
}

// TestPackerWrapsTrueHD packs hbr.mkv's TrueHD track into MAT frames:
// a burst of 61440 bytes every 24 access units, its payload 61424 bytes
// with the MAT codes at their places, and the access units following
// each other through the frames in order, with only padding between.
func TestPackerWrapsTrueHD(t *testing.T) {
	packets, outs := packTrack(t, 2, "truehd", 0)
	le := binary.LittleEndian
	matStart := []byte{0x07, 0x9E, 0x00, 0x03, 0x84, 0x01, 0x01, 0x01, 0x80, 0x00, 0x56, 0xA5, 0x3B, 0xF4, 0x81, 0x83, 0x49, 0x80, 0x77, 0xE0}
	matMiddle := []byte{0xC3, 0xC1, 0x42, 0x49, 0x3B, 0xFA, 0x82, 0x83, 0x49, 0x80, 0x77, 0xE0}
	matEnd := []byte{0xC3, 0xC2, 0xC0, 0xC4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x97, 0x11}
	const frameSize = 61424
	var stream []byte // the frames' payloads without their codes
	last := -1        // the packet the last burst came with
	bursts := 0
	for i, out := range outs {
		if len(out) == 0 {
			continue
		}
		if len(out) != 61440 {
			t.Fatalf("packet %d: burst of %d bytes", i, len(out))
		}
		if le.Uint16(out) != 0xF872 || le.Uint16(out[2:]) != 0x4E1F || le.Uint16(out[4:])&0x1f != 22 || le.Uint16(out[6:]) != frameSize {
			t.Fatalf("burst header % x", out[:8])
		}
		if last >= 0 && i-last != 24 {
			t.Fatalf("a burst after %d access units", i-last)
		}
		last = i
		bursts++
		mat := words(out[8 : 8+frameSize])
		for _, c := range []struct {
			at   int
			code []byte
		}{{0, matStart}, {30708, matMiddle}, {frameSize - len(matEnd), matEnd}} {
			if !bytes.Equal(mat[c.at:c.at+len(c.code)], c.code) {
				t.Fatalf("burst %d: no MAT code at %d", bursts, c.at)
			}
		}
		stream = append(stream, mat[len(matStart):30708]...)
		stream = append(stream, mat[30708+len(matMiddle):frameSize-len(matEnd)]...)
	}
	if bursts < len(packets)/24-1 {
		t.Fatalf("%d bursts for %d access units", bursts, len(packets))
	}
	// Every access unit of the complete frames, in order, with zeros
	// between them.
	at := 0
	for i, data := range packets[:bursts*24-1] {
		j := bytes.Index(stream[at:], data)
		if j < 0 {
			t.Fatalf("access unit %d is not in the frames after byte %d", i, at)
		}
		if bytes.ContainsFunc(stream[at:at+j], func(r rune) bool { return r != 0 }) {
			t.Fatalf("data before access unit %d", i)
		}
		at += j + len(data)
	}
}
