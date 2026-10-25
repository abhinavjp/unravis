package nlog

import (
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/importer"
)

// ProbeTime implements importer.TimeProber for the default NLog layout. The
// header time is host-local unless the owner stated UTC, so a nil host or time
// zone (and a time inside a DST fold or gap) reports false, as the importer
// would reject the line.
func (*textImp) ProbeTime(line []byte, opts importer.Options) (time.Time, bool) {
	if len(line) == 0 || line[0] < '0' || line[0] > '9' {
		return time.Time{}, false
	}
	sm := headerRe.FindSubmatch(line)
	if sm == nil {
		return time.Time{}, false
	}
	rule := clock.RuleUTCField
	var loc *time.Location
	if !logTimeUTC(opts) {
		if opts.Host == nil || opts.Host.TZ == nil {
			return time.Time{}, false
		}
		rule, loc = clock.RuleHostLocal, opts.Host.TZ
	}
	t, why := when(string(sm[1]), rule, loc)
	if why != "" {
		return time.Time{}, false
	}
	return t, true
}

var _ importer.TimeProber = (*textImp)(nil)
