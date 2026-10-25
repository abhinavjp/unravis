package schedule

import (
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
)

// ProbeTime implements importer.TimeProber for the two layouts Parse reads (a
// log4net-pattern header and a MEL console header with a time stamp prefix).
// The time is host-local unless the owner stated UTC, so a nil host or time
// zone (and a time inside a DST fold or gap) reports false, as Parse would
// reject the line. Any logger counts: the time of a line is read whether or not
// the line becomes an event. A continuation line, a header with no time stamp,
// a line that is not a whole header and an unknown level report false. The line
// is not kept.
func (*Quartz) ProbeTime(line []byte, opts importer.Options) (time.Time, bool) {
	if len(line) == 0 || line[0] < '0' || line[0] > '9' || len(line) > quartzMaxHeadLine {
		return time.Time{}, false
	}
	text := string(line)
	var val, levelWord string
	if m := quartzLogRe.FindStringSubmatch(text); m != nil {
		val, levelWord = m[1], m[3]
	} else if m := quartzMelRe.FindStringSubmatch(text); m != nil {
		val, levelWord = m[1], m[2]
	} else {
		return time.Time{}, false
	}
	if _, _, ok := applog.Level(levelWord); !ok {
		return time.Time{}, false
	}
	rule := clock.RuleUTCField
	var loc *time.Location
	if !logTimeIsUTC(opts) {
		if opts.Host == nil || opts.Host.TZ == nil {
			return time.Time{}, false
		}
		rule, loc = clock.RuleHostLocal, opts.Host.TZ
	}
	t, why := quartzWhen(val, rule, loc)
	if why != "" {
		return time.Time{}, false
	}
	return t, true
}

var _ importer.TimeProber = (*Quartz)(nil)
