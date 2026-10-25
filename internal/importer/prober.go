package importer

import "time"

// TimeProber is an optional interface of an Importer whose lines carry their
// own timestamp. The local reader of host discovery uses it to seek into a
// file that is too large to read whole: it binary searches byte offsets,
// probes the line found there and lands on the first line of a time window
// without parsing the bytes in between.
//
// ProbeTime reads the time of ONE physical line (no line break, a trailing
// CR already removed, a UTF-8 BOM already removed). It reports false for a
// line that has no time of its own: a directive, a header, a continuation
// line, or a line it does not understand. It never keeps the line, never puts
// it in an error and allocates nothing that outlives the call. opts carries
// the host time zone for formats that log local time (nlog, log4net default,
// mel-json without an offset); a nil Host.TZ makes those report false.
//
// The returned time is UTC and passes the same sanity check the importer
// applies when it parses the line.
type TimeProber interface {
	ProbeTime(line []byte, opts Options) (time.Time, bool)
}

// ProbeFunc returns imp's line-time function bound to opts, or false when imp
// is not a TimeProber. The function is safe for concurrent use only if the
// importer is.
func ProbeFunc(imp Importer, opts Options) (func(line []byte) (time.Time, bool), bool) {
	p, ok := imp.(TimeProber)
	if !ok {
		return nil, false
	}
	return func(line []byte) (time.Time, bool) { return p.ProbeTime(line, opts) }, true
}
