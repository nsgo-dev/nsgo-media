// Command bitmapsubs writes the bitmap subtitle fixtures' sources:
//
//	go run ./testdata/bitmapsubs sup testdata/pgs.sup
//	go run ./testdata/bitmapsubs idx testdata/vobsub.sub > testdata/vobsub.idx
//
// sup writes a PGS stream on a 160×90 canvas (see README.md for its
// display sets). idx indexes the subpicture packets of an MPEG program
// stream FFmpeg muxed, as a VobSub .idx does.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: bitmapsubs sup|idx FILE")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "sup":
		err = os.WriteFile(os.Args[2], pgs(), 0o644)
	case "idx":
		err = idx(os.Args[2])
	default:
		err = fmt.Errorf("unknown command %s", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const canvasW, canvasH = 160, 90

// object is a filled box of one palette entry.
type object struct {
	id         int
	x, y, w, h int
	color      byte
}

var (
	objA = object{id: 0, x: 60, y: 70, w: 40, h: 12, color: 1}
	objB = object{id: 1, x: 8, y: 8, w: 20, h: 10, color: 2}
	objC = object{id: 0, x: 20, y: 60, w: 30, h: 12, color: 1}
	objD = object{id: 0, x: 100, y: 40, w: 24, h: 16, color: 2}
)

// pgs is the display sets at their times in milliseconds: A, then A
// and B updating it, a clear, C shown long, a clear, D, a clear.
func pgs() []byte {
	var b bytes.Buffer
	n := 0
	set := func(ms int, state byte, objs []object, define []object) {
		pts := uint32(ms * 90)
		n++
		pcs := []byte{0, 0, 0, 0, 0x10, byte(n >> 8), byte(n), state, 0, 0, byte(len(objs))}
		binary.BigEndian.PutUint16(pcs[0:], canvasW)
		binary.BigEndian.PutUint16(pcs[2:], canvasH)
		for i, o := range objs {
			pcs = append(pcs, 0, byte(o.id), byte(i), 0, byte(o.x>>8), byte(o.x), byte(o.y>>8), byte(o.y))
		}
		segment(&b, pts, 0x16, pcs)
		wds := []byte{byte(len(objs))}
		for i, o := range objs {
			wds = append(wds, byte(i), byte(o.x>>8), byte(o.x), byte(o.y>>8), byte(o.y), byte(o.w>>8), byte(o.w), byte(o.h>>8), byte(o.h))
		}
		segment(&b, pts, 0x17, wds)
		if len(define) > 0 {
			// Entry 1 is opaque white, 2 translucent red (Y, Cr, Cb, alpha).
			segment(&b, pts, 0x14, []byte{0, 0, 1, 235, 128, 128, 255, 2, 81, 240, 90, 128})
		}
		for _, o := range define {
			rle := encodeRLE(o)
			ods := []byte{0, byte(o.id), 0, 0xC0}
			l := len(rle) + 4
			ods = append(ods, byte(l>>16), byte(l>>8), byte(l), byte(o.w>>8), byte(o.w), byte(o.h>>8), byte(o.h))
			segment(&b, pts, 0x15, append(ods, rle...))
		}
		segment(&b, pts, 0x80, nil)
	}
	const epoch, normal = 0x80, 0x00
	set(500, epoch, []object{objA}, []object{objA})
	set(1000, normal, []object{objA, objB}, []object{objB})
	set(1500, normal, nil, nil)
	set(2000, epoch, []object{objC}, []object{objC})
	set(6000, normal, nil, nil)
	set(7200, epoch, []object{objD}, []object{objD})
	set(7700, normal, nil, nil)
	return b.Bytes()
}

func segment(b *bytes.Buffer, pts uint32, typ byte, data []byte) {
	h := []byte{'P', 'G', 0, 0, 0, 0, 0, 0, 0, 0, typ, byte(len(data) >> 8), byte(len(data))}
	binary.BigEndian.PutUint32(h[2:], pts)
	b.Write(h)
	b.Write(data)
}

// encodeRLE is PGS' run-length coding of o's box, one run per line.
func encodeRLE(o object) []byte {
	var out []byte
	for range o.h {
		out = append(out, 0, 0xC0|byte(o.w>>8), byte(o.w), o.color, 0, 0)
	}
	return out
}

// idx writes the .idx of sub: the canvas, a palette and each subpicture
// packet's time and the position of the pack holding it.
func idx(sub string) error {
	data, err := os.ReadFile(sub)
	if err != nil {
		return err
	}
	fmt.Printf("# VobSub index file, v7\nsize: %dx%d\norg: 0, 0\n", canvasW, canvasH)
	fmt.Println("palette: 000000, ebebeb, f03c3c, 808080, 000000, 000000, 000000, 000000, 000000, 000000, 000000, 000000, 000000, 000000, 000000, 000000")
	fmt.Println("langidx: 0\nid: en, index: 0")
	pack := -1
	for i := 0; i+4 <= len(data); i++ {
		if data[i] != 0 || data[i+1] != 0 || data[i+2] != 1 {
			continue
		}
		switch data[i+3] {
		case 0xBA:
			pack = i
		case 0xBD: // private stream 1: a PES header with a PTS, then the substream
			hl := int(data[i+8])
			if data[i+7]&0x80 == 0 || data[i+9+hl] != 0x20 {
				continue
			}
			p := data[i+9:]
			pts := int64(p[0]>>1&7)<<30 | int64(p[1])<<22 | int64(p[2]>>1)<<15 | int64(p[3])<<7 | int64(p[4]>>1)
			ms := pts / 90
			fmt.Printf("timestamp: %02d:%02d:%02d:%03d, filepos: %09x\n", ms/3600000, ms/60000%60, ms/1000%60, ms%1000, pack)
		}
	}
	return nil
}
