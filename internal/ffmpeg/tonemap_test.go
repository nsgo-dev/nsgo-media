package ffmpeg

import (
	"sync/atomic"
	"testing"
	"unsafe"
)

func decodeFirstHDRFrame(t *testing.T, name string) (trc, primaries, space int32, hdr bool, rgba []byte) {
	t.Helper()
	mustLoad(t)
	in, err := Open(fixture(t, name), nil, new(atomic.Bool))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	st := in.Format.Streams()[0]
	dec, err := NewDecoder(st.Params(), st.TimeBase(), 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	pkt, frame := NewPacket(), NewFrame()
	defer pkt.Free()
	defer frame.Free()
	var scaler Scaler
	defer scaler.Close()

	for {
		err := in.Format.ReadPacket(pkt)
		if IsEOF(err) {
			_ = dec.Send(nil)
		} else if err != nil {
			t.Fatal(err)
		} else {
			if pkt.StreamIndex() != st.Index() {
				pkt.Unref()
				continue
			}
			_ = dec.Send(pkt)
			pkt.Unref()
		}
		if err := dec.Receive(frame); err == nil {
			w, h := frame.Width(), frame.Height()
			buf := Alloc(w * h * 4)
			defer Free(buf)
			if err := scaler.Scale(frame, buf, w*4, w, h); err != nil {
				t.Fatal(err)
			}
			out := make([]byte, w*h*4)
			copy(out, unsafe.Slice((*byte)(buf), len(out)))
			return frame.ColorTrc(), frame.ColorPrimaries(), frame.ColorSpace(), frame.HDR(), out
		} else if IsEOF(err) {
			t.Fatalf("%s: no frame decoded", name)
		}
	}
}

func pixelAt(rgba []byte, w, x, y int) (r, g, b, a uint8) {
	o := (y*w + x) * 4
	return rgba[o], rgba[o+1], rgba[o+2], rgba[o+3]
}

func TestHDRToneMapClips(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wantTrc int32
	}{
		{"hdr10.mkv", ColorTrcSMPTE2084},
		{"hlg.mkv", ColorTrcARIBSTDB67},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trc, pri, space, hdr, rgba := decodeFirstHDRFrame(t, tc.name)
			if !hdr || trc != tc.wantTrc || pri != ColorPrimariesBT2020 || space != ColorSpaceBT2020NCL {
				t.Fatalf("metadata hdr=%v trc=%d pri=%d space=%d, want hdr=true trc=%d pri=%d space=%d",
					hdr, trc, pri, space, tc.wantTrc, ColorPrimariesBT2020, ColorSpaceBT2020NCL)
			}
			// Top-left (40, 22): 203-nit reference white, tone-mapped bright (not dull gray ~148).
			wr, wg, wb, wa := pixelAt(rgba, 160, 40, 22)
			if wr < 210 || wg < 210 || wb < 210 || wa != 255 {
				t.Errorf("reference white = (%d,%d,%d,%d), want >= 210", wr, wg, wb, wa)
			}
			// Top-right (120, 22): 1000-nit peak highlight, brighter than reference white.
			pr, pg, pb, _ := pixelAt(rgba, 160, 120, 22)
			if pr < 245 || pg < 245 || pb < 245 || pr <= wr {
				t.Errorf("peak white = (%d,%d,%d), want >= 245 and > ref white (%d)", pr, pg, pb, wr)
			}
			// Bottom-left (40, 67): black.
			kr, kg, kb, _ := pixelAt(rgba, 160, 40, 67)
			if kr > 10 || kg > 10 || kb > 10 {
				t.Errorf("black = (%d,%d,%d), want <= 10", kr, kg, kb)
			}
			// Bottom-middle (100, 67): mid-gray, strictly between black and reference white.
			mr, mg, mb, _ := pixelAt(rgba, 160, 100, 67)
			if mr < 80 || mr > 175 || mg < 80 || mg > 175 || mb < 80 || mb > 175 {
				t.Errorf("mid-gray = (%d,%d,%d), want in [80, 175]", mr, mg, mb)
			}
			// Bottom-right (140, 67): BT.2020 red, saturated red after gamut mapping.
			rr, rg, rb, _ := pixelAt(rgba, 160, 140, 67)
			if rr < 200 || rg > 40 || rb > 40 {
				t.Errorf("BT.2020 red = (%d,%d,%d), want R>=200, G<=40, B<=40", rr, rg, rb)
			}
		})
	}
}

func TestSDRClipIsNotHDR(t *testing.T) {
	_, _, _, hdr, _ := decodeFirstHDRFrame(t, "av.mkv")
	if hdr {
		t.Fatal("av.mkv reported HDR=true, want false")
	}
}
