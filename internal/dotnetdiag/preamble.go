package dotnetdiag

import (
	"encoding/binary"
	"math"
)

// PreambleLen is the number of stream bytes ParsePreamble reads: the nettrace
// magic, the FastSerialization header and the whole Trace object header.
const PreambleLen = 102

// Preamble is what the fixed 102-byte start of a nettrace stream says.
type Preamble struct {
	Version      uint32 // Trace object version, 4 or 5
	MinReader    uint32 // minimum reader version the writer asks for
	SyncTime     [8]uint16
	QPCSync      uint64
	QPCFreq      uint64 // 1 to MaxInt64
	PointerSize  uint32 // 4 or 8
	PID          uint32 // always > 0
	Processors   uint32
	SamplingRate uint32
}

const (
	nettraceMagic = "Nettrace"
	fastSerHeader = "!FastSerialization.1"
	traceTypeName = "Trace"
)

// ParsePreamble reads the fixed layout at fixed offsets from the first 102
// bytes of a stream. Fewer bytes, a wrong literal, a wrong tag, a type name of
// another length, a version outside 4..5, a pointer size other than 4 or 8, a
// zero clock frequency or a process id of 0 is ReasonBadStream. Code holds the
// offending number for numeric fields and the byte offset for literals, tags and
// the clock frequency. Block parsing is not part of this step; bytes after offset 102 are
// ignored.
func ParsePreamble(b []byte) (Preamble, error) {
	var p Preamble
	if len(b) < PreambleLen {
		return p, newErr(ReasonBadStream, uint32(len(b))) //nolint:gosec // len(b) < PreambleLen (102) in this branch
	}
	b = b[:PreambleLen]
	bad := func(code uint32) (Preamble, error) { return Preamble{}, newErr(ReasonBadStream, code) }
	le := binary.LittleEndian

	if string(b[0:8]) != nettraceMagic {
		return bad(0)
	}
	if le.Uint32(b[8:12]) != uint32(len(fastSerHeader)) {
		return bad(8)
	}
	if string(b[12:32]) != fastSerHeader {
		return bad(12)
	}
	// begin object, begin type object, null reference
	for i, want := range [3]byte{5, 5, 1} {
		if b[32+i] != want {
			return bad(uint32(32 + i))
		}
	}
	p.Version = le.Uint32(b[35:39])
	if p.Version < 4 || p.Version > 5 {
		return bad(p.Version)
	}
	p.MinReader = le.Uint32(b[39:43])
	if n := le.Uint32(b[43:47]); n != uint32(len(traceTypeName)) {
		return bad(n)
	}
	if string(b[47:52]) != traceTypeName {
		return bad(47)
	}
	if b[52] != 6 { // end of the type object
		return bad(52)
	}
	for i := range p.SyncTime {
		p.SyncTime[i] = le.Uint16(b[53+2*i:])
	}
	p.QPCSync = le.Uint64(b[69:77])
	p.QPCFreq = le.Uint64(b[77:85])
	if p.QPCFreq == 0 || p.QPCFreq > math.MaxInt64 {
		return bad(77) // the byte offset of the clock frequency
	}
	p.PointerSize = le.Uint32(b[85:89])
	if p.PointerSize != 4 && p.PointerSize != 8 {
		return bad(p.PointerSize)
	}
	pid := le.Uint32(b[89:93])
	if pid == 0 || pid > math.MaxInt32 { // a process id is a positive int32 in the stream
		return bad(pid)
	}
	p.PID = pid
	p.Processors = le.Uint32(b[93:97])
	p.SamplingRate = le.Uint32(b[97:101])
	if b[101] != 6 { // end of the Trace object
		return bad(101)
	}
	return p, nil
}
