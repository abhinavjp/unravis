package evtx

import (
	"encoding/binary"
	"errors"
	"time"
	"unicode/utf16"
)

// This file decodes what EvtRender returns for the two render contexts the
// reader uses. It takes the raw buffer, so it builds and is tested on any OS.
//
// EvtRender fills a buffer with an array of EVT_VARIANT values (16 bytes each:
// an 8 byte union, a 4 byte count and a 4 byte type) followed by the data they
// point at, such as string text. A string value holds the address of its text
// inside that same buffer. base is the address the buffer had when it was
// filled, so a pointer becomes an offset and is checked against the buffer
// length: a bad pointer is an error, never a read outside the buffer.

// EVT_VARIANT types the reader accepts.
const (
	varNull     = 0
	varString   = 1
	varByte     = 4
	varUInt16   = 6
	varUInt32   = 8
	varUInt64   = 10
	varFileTime = 17

	varTypeMask = 0x7f
	variantSize = 16
)

// EVT_SYSTEM_PROPERTY_ID values, the index of each System value.
const (
	sysProviderName = 0
	sysEventID      = 2
	sysLevel        = 4
	sysTimeCreated  = 8
	sysRecordID     = 9
	sysChannel      = 14
	sysComputer     = 15
	sysPropertyEnd  = 18
)

var errBadRender = errors.New("evtx: rendered event values are malformed")

type variant struct {
	typ uint32
	raw uint64
}

// parseVariants splits the first count variants out of buf.
func parseVariants(buf []byte, count int) ([]variant, error) {
	if count < 0 || count*variantSize > len(buf) {
		return nil, errBadRender
	}
	out := make([]variant, count)
	for i := range out {
		b := buf[i*variantSize : (i+1)*variantSize]
		out[i] = variant{
			typ: binary.LittleEndian.Uint32(b[12:16]) & varTypeMask,
			raw: binary.LittleEndian.Uint64(b[0:8]),
		}
	}
	return out, nil
}

// stringAt reads the NUL-terminated UTF-16 text a string variant points at.
func stringAt(buf []byte, base, ptr uint64) (string, error) {
	if ptr < base || ptr-base >= uint64(len(buf)) {
		return "", errBadRender
	}
	off := int(ptr - base)
	if off%2 != 0 {
		return "", errBadRender
	}
	var units []uint16
	for ; off+2 <= len(buf); off += 2 {
		u := binary.LittleEndian.Uint16(buf[off : off+2])
		if u == 0 {
			return string(utf16.Decode(units)), nil
		}
		if len(units) >= 4096 {
			return "", errBadRender
		}
		units = append(units, u)
	}
	return "", errBadRender // no terminator inside the buffer
}

func (v variant) text(buf []byte, base uint64) (string, error) {
	switch v.typ {
	case varNull:
		return "", nil
	case varString:
		if v.raw == 0 {
			return "", nil
		}
		return stringAt(buf, base, v.raw)
	}
	return "", errBadRender
}

func (v variant) uint() (uint64, bool) {
	switch v.typ {
	case varNull:
		return 0, true
	case varByte:
		return v.raw & 0xff, true
	case varUInt16:
		return v.raw & 0xffff, true
	case varUInt32:
		return v.raw & 0xffffffff, true
	case varUInt64:
		return v.raw, true
	}
	return 0, false
}

const filetimeToUnixSeconds = 11644473600

// fileTime converts a FILETIME (100 ns ticks since 1601) to UTC.
func fileTime(ft uint64) (time.Time, bool) {
	if ft == 0 {
		return time.Time{}, false
	}
	sec := int64(ft/10_000_000) - filetimeToUnixSeconds
	ns := int64(ft%10_000_000) * 100
	return time.Unix(sec, ns).UTC(), true
}

// decodeSystem builds a Record from the values of an EvtRenderContextSystem
// render. buf is the render buffer, base its address, count the property count.
func decodeSystem(buf []byte, base uint64, count int) (Record, error) {
	if count < sysComputer+1 {
		return Record{}, errBadRender
	}
	vs, err := parseVariants(buf, count)
	if err != nil {
		return Record{}, err
	}
	var r Record
	if r.Provider, err = vs[sysProviderName].text(buf, base); err != nil {
		return Record{}, err
	}
	if r.Channel, err = vs[sysChannel].text(buf, base); err != nil {
		return Record{}, err
	}
	if r.Computer, err = vs[sysComputer].text(buf, base); err != nil {
		return Record{}, err
	}
	id, ok := vs[sysEventID].uint()
	if !ok || id > 0xffff {
		return Record{}, errBadRender
	}
	r.EventID = uint32(id)
	lvl, ok := vs[sysLevel].uint()
	if !ok || lvl > 0xff {
		return Record{}, errBadRender
	}
	r.Level = uint8(lvl)
	rid, ok := vs[sysRecordID].uint()
	if !ok {
		return Record{}, errBadRender
	}
	r.RecordID = rid
	if vs[sysTimeCreated].typ != varFileTime {
		return Record{}, errBadRender
	}
	if r.Time, ok = fileTime(vs[sysTimeCreated].raw); !ok {
		return Record{}, errBadRender
	}
	return r, nil
}

// decodeAppPool reads the one value of the AppPoolID values context. A missing
// or non-text value gives an empty name; the event keeps its row.
func decodeAppPool(buf []byte, base uint64, count int) string {
	vs, err := parseVariants(buf, count)
	if err != nil || len(vs) < 1 || vs[0].typ != varString {
		return ""
	}
	s, err := vs[0].text(buf, base)
	if err != nil {
		return ""
	}
	return cleanText(s)
}
