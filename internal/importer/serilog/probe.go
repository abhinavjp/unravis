package serilog

import (
	"encoding/json"
	"time"

	"api-audit-explorer/internal/importer"
)

// textStampLen is the width of "yyyy-MM-dd HH:mm:ss.fff zzz".
const textStampLen = len("2006-01-02 15:04:05.000 -07:00")

// ProbeTime implements importer.TimeProber: a default-template header line
// carries its own offset; every other line (continuation, exception) has no
// time of its own.
func (*textImp) ProbeTime(line []byte, _ importer.Options) (time.Time, bool) {
	if len(line) < textStampLen || !textHeadStart.Match(line) {
		return time.Time{}, false
	}
	t, err := time.Parse(textLayout, string(line[:textStampLen]))
	if err != nil || !saneTime(t) {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// ProbeTime implements importer.TimeProber: "@t" with an offset or "Z". A
// value without an offset is refused, as the importer refuses it.
func (*clefImp) ProbeTime(line []byte, _ importer.Options) (time.Time, bool) {
	if len(line) == 0 || line[0] != '{' {
		return time.Time{}, false
	}
	var cl struct {
		T json.RawMessage `json:"@t"`
	}
	if json.Unmarshal(line, &cl) != nil {
		return time.Time{}, false
	}
	s, ok := rawString(cl.T)
	if !ok {
		return time.Time{}, false
	}
	t, _, reason := clefTime(s)
	if reason != "" {
		return time.Time{}, false
	}
	return t, true
}

var (
	_ importer.TimeProber = (*textImp)(nil)
	_ importer.TimeProber = (*clefImp)(nil)
)
