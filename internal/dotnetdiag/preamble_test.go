package dotnetdiag

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

// buildPreambleFrom writes a 102-byte preamble from its fields. The bytes are
// SYNTHETIC: built from the layout in the BL-110 spec (3.3) and the NetTrace
// format document, not captured from a runtime. A later PR adds real bytes.
func buildPreambleFrom(p Preamble) []byte {
	le := binary.LittleEndian
	b := []byte(nettraceMagic)
	b = le.AppendUint32(b, 20)
	b = append(b, fastSerHeader...)
	b = append(b, 5, 5, 1)
	b = le.AppendUint32(b, p.Version)
	b = le.AppendUint32(b, p.MinReader)
	b = le.AppendUint32(b, 5)
	b = append(b, traceTypeName...)
	b = append(b, 6)
	for _, v := range p.SyncTime {
		b = le.AppendUint16(b, v)
	}
	b = le.AppendUint64(b, uint64(p.QPCSync))
	b = le.AppendUint64(b, uint64(p.QPCFreq))
	b = le.AppendUint32(b, p.PointerSize)
	b = le.AppendUint32(b, p.PID)
	b = le.AppendUint32(b, p.Processors)
	b = le.AppendUint32(b, p.SamplingRate)
	return append(b, 6)
}

func buildPreamble(pid, ptr, ver uint32) []byte {
	return buildPreambleFrom(goodPreamble(pid, ptr, ver))
}

func goodPreamble(pid, ptr, ver uint32) Preamble {
	return Preamble{
		Version: ver, MinReader: 4,
		SyncTime: [8]uint16{2026, 11, 5, 21, 10, 30, 15, 250},
		QPCSync:  123456789, QPCFreq: 10_000_000,
		PointerSize: ptr, PID: pid, Processors: 8, SamplingRate: 1000,
	}
}

func isBadStream(err error) bool {
	var de *Error
	return errors.As(err, &de) && de.Reason == ReasonBadStream
}

func TestParsePreambleGolden(t *testing.T) {
	b := buildPreamble(4321, 8, 4)
	if len(b) != PreambleLen {
		t.Fatalf("builder made %d bytes, want %d", len(b), PreambleLen)
	}
	p, err := ParsePreamble(b)
	if err != nil {
		t.Fatal(err)
	}
	if p != goodPreamble(4321, 8, 4) {
		t.Fatalf("got %+v", p)
	}
	// Extra bytes after the preamble (the first block) are ignored.
	if _, err := ParsePreamble(append(b, make([]byte, 500)...)); err != nil {
		t.Fatal(err)
	}
	if p, err := ParsePreamble(buildPreamble(7, 4, 5)); err != nil || p.Version != 5 || p.PointerSize != 4 {
		t.Fatalf("v5/ptr4: %+v %v", p, err)
	}
}

func TestParsePreambleRejects(t *testing.T) {
	good := buildPreamble(4321, 8, 4)
	type tc struct {
		name string
		in   []byte
	}
	var cs []tc
	cs = append(cs, tc{"empty", nil}, tc{"short by 1", good[:PreambleLen-1]})
	// every literal byte flipped once: magic, length, header text
	for i := 0; i < 32; i++ {
		b := append([]byte(nil), good...)
		b[i] ^= 0x01
		cs = append(cs, tc{fmt.Sprintf("literal byte %d", i), b})
	}
	// each tag wrong
	for _, off := range []int{32, 33, 34, 52, 101} {
		b := append([]byte(nil), good...)
		b[off] ^= 0x10
		cs = append(cs, tc{fmt.Sprintf("tag at %d", off), b})
	}
	// type name text and length
	{
		b := append([]byte(nil), good...)
		b[47] = 'X'
		cs = append(cs, tc{"type name text", b})
		for _, n := range []uint32{4, 6, 0, 0xFFFFFFFF} {
			b = append([]byte(nil), good...)
			binary.LittleEndian.PutUint32(b[43:], n)
			cs = append(cs, tc{fmt.Sprintf("type name length %d", n), b})
		}
	}
	mod := func(name string, f func(p *Preamble)) {
		p := goodPreamble(4321, 8, 4)
		f(&p)
		cs = append(cs, tc{name, buildPreambleFrom(p)})
	}
	mod("version 3", func(p *Preamble) { p.Version = 3 })
	mod("version 6", func(p *Preamble) { p.Version = 6 })
	mod("version 0", func(p *Preamble) { p.Version = 0 })
	for _, ps := range []uint32{0, 2, 16, 0xFFFFFFFF} {
		ps := ps
		mod(fmt.Sprintf("pointer size %d", ps), func(p *Preamble) { p.PointerSize = ps })
	}
	mod("qpc frequency 0", func(p *Preamble) { p.QPCFreq = 0 })
	mod("qpc frequency negative", func(p *Preamble) { p.QPCFreq = -1 })
	mod("process id 0", func(p *Preamble) { p.PID = 0 })
	mod("process id negative", func(p *Preamble) { p.PID = 0x80000000 })

	for _, c := range cs {
		if _, err := ParsePreamble(c.in); !isBadStream(err) {
			t.Errorf("%s: err %v, want bad-stream", c.name, err)
		}
	}
}

func FuzzParsePreamble(f *testing.F) {
	f.Add(buildPreamble(4321, 8, 4))
	f.Add(buildPreamble(1, 4, 5))
	f.Add(buildPreamble(4321, 8, 4)[:50])
	f.Add([]byte("Nettrace"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		p, err := ParsePreamble(in)
		if err != nil {
			if !isBadStream(err) {
				t.Fatalf("error %v is not bad-stream", err)
			}
			return
		}
		if p.Version < 4 || p.Version > 5 || (p.PointerSize != 4 && p.PointerSize != 8) ||
			p.QPCFreq <= 0 || p.PID == 0 || p.PID > 0x7FFFFFFF {
			t.Fatalf("accepted out-of-range fields %+v", p)
		}
		back, err := ParsePreamble(buildPreambleFrom(p))
		if err != nil || back != p {
			t.Fatalf("round trip: %+v %v", back, err)
		}
	})
}
