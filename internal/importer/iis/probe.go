package iis

import (
	"time"

	"api-audit-explorer/internal/importer"
)

// w3cStampLayout is the `date time` pair of a W3C row, both in UTC.
const w3cStampLayout = "2006-01-02 15:04:05"

// ProbeTime implements importer.TimeProber. IIS always writes `date` and
// `time` as the first two fields of a row, so a row starts with
// "yyyy-mm-dd hh:mm:ss"; directives (#Software, #Fields, ...) and anything
// else have no time of their own. The probe needs no header, which is what lets
// the reader land in the middle of a multi-gigabyte file.
func (W3C) ProbeTime(line []byte, _ importer.Options) (time.Time, bool) {
	n := len(w3cStampLayout)
	if len(line) < n || (len(line) > n && line[n] != ' ') {
		return time.Time{}, false
	}
	ts, err := time.Parse(w3cStampLayout, string(line[:n]))
	if err != nil || !saneLogTime(ts) {
		return time.Time{}, false
	}
	return ts.UTC(), true
}

var _ importer.TimeProber = W3C{}
