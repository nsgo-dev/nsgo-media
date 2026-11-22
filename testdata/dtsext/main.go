// Command dtsext follows each DTS core frame of a raw .dts stream with an
// extension substream, as DTS-HD lays out its frames:
//
//	go run ./testdata/dtsext core.dts hd.dts
//	go run ./testdata/dtsext -ma core.dts ma.dts
//
// The substream is 16 bytes: its sync word and a header giving its size,
// with no assets, so decoders play the core and ignore it. With -ma it
// declares one lossless (XLL) asset, as DTS-HD Master Audio's does, so
// FFmpeg's parser reports the stream as Master Audio; the asset is 256
// bytes of padding, which decoders drop to play the core.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
)

func main() {
	ma := flag.Bool("ma", false, "declare a lossless asset, as DTS-HD Master Audio")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: dtsext [-ma] CORE.dts OUT.dts")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), flag.Arg(1), *ma); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const (
	coreSync = 0x7ffe8001
	exssSync = 0x64582025
	xllSize  = 256
)

func run(in, out string, ma bool) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	exss := emptySubstream()
	if ma {
		exss = losslessSubstream()
	}
	var buf bytes.Buffer
	for len(data) > 0 {
		if len(data) < 10 || binary.BigEndian.Uint32(data) != coreSync {
			return fmt.Errorf("no core frame at %d bytes from the end", len(data))
		}
		size := int(data[5]&0x03)<<12 | int(data[6])<<4 | int(data[7])>>4 + 1
		if size > len(data) {
			return fmt.Errorf("frame of %d bytes past the end", size)
		}
		buf.Write(data[:size])
		buf.Write(exss)
		data = data[size:]
	}
	return os.WriteFile(out, buf.Bytes(), 0o644)
}

// emptySubstream is a substream with no assets: its sync word, user bits
// 0, index 0, a short header 16 bytes long and a substream 16 bytes long,
// no static fields; the rest is padding.
func emptySubstream() []byte {
	exss := make([]byte, 16)
	binary.BigEndian.PutUint32(exss, exssSync)
	binary.BigEndian.PutUint64(exss[4:], 15<<45|15<<29)
	return exss
}

// losslessSubstream is a substream of one asset of xllSize bytes coded
// with XLL alone, its header closed by its CRC.
func losslessSubstream() []byte {
	const header = 19 // bytes, the CRC's 2 among them
	var w bitWriter
	w.put(exssSync, 32)
	w.put(0, 8)                 // user defined bits
	w.put(0, 2)                 // substream index
	w.put(0, 1)                 // a short header
	w.put(header-1, 8)          // header size
	w.put(header+xllSize-1, 16) // substream size
	w.put(0, 1)                 // no static fields: one presentation, one asset
	w.put(xllSize-1, 16)        // the asset's size
	// The asset's descriptor, 6 bytes.
	w.put(6-1, 9)        // descriptor size
	w.put(0, 3)          // asset index
	w.put(0, 1)          // no dynamic range
	w.put(0, 1)          // no dialog normalization
	w.put(0, 2)          // coding mode: components
	w.put(0x200, 12)     // the XLL component
	w.put(xllSize-1, 16) // its size
	w.put(0, 1)          // no XLL sync word
	w.put(0, 3)          // DTS-HD stream ID
	w.put(0, 1)          // no backward compatible core asset
	w.align()
	b := w.bytes()
	crc := crc16(b[5:])
	b = append(b, byte(crc>>8), byte(crc))
	if len(b) != header {
		panic(fmt.Sprintf("header of %d bytes", len(b)))
	}
	return append(b, make([]byte, xllSize)...)
}

type bitWriter struct {
	buf []byte
	n   int // bits written
}

func (w *bitWriter) put(v uint64, bits int) {
	for i := bits - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		if v>>i&1 != 0 {
			w.buf[w.n/8] |= 0x80 >> (w.n % 8)
		}
		w.n++
	}
}

func (w *bitWriter) align() { w.n = (w.n + 7) &^ 7 }

func (w *bitWriter) bytes() []byte { return w.buf }

// crc16 is DTS' CRC of headers: CCITT's polynomial from 0xffff, which
// reads 0 over the bytes followed by it.
func crc16(b []byte) uint16 {
	crc := uint16(0xffff)
	for _, c := range b {
		crc ^= uint16(c) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
