// Command dtsext follows each DTS core frame of a raw .dts stream with an
// extension substream, as DTS-HD lays out its frames:
//
//	go run ./testdata/dtsext core.dts hd.dts
//
// The substream is 16 bytes: its sync word and a header giving its size,
// with no assets, so decoders play the core and ignore it.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: dtsext CORE.dts OUT.dts")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const (
	coreSync = 0x7ffe8001
	exssSync = 0x64582025
)

func run(in, out string) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	// Sync word, user bits 0, index 0, a short header 16 bytes long and a
	// substream 16 bytes long, no static fields; the rest is padding.
	exss := make([]byte, 16)
	binary.BigEndian.PutUint32(exss, exssSync)
	var bits uint64
	bits = 0<<56 | 0<<54 | 0<<53 | 15<<45 | 15<<29
	binary.BigEndian.PutUint64(exss[4:], bits)
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
