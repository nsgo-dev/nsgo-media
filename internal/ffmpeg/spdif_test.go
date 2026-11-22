package ffmpeg

import (
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
			k, err := NewPacker(s.Params())
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
