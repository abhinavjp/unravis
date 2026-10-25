package mel

import (
	"strings"
	"time"

	"api-audit-explorer/internal/importer"
)

// ProbeTime implements importer.TimeProber for the JSON console formatter: the
// "Timestamp" member of an entry that Parse would accept. A timestamp without
// an offset is host-local and needs opts.Host.TZ. The console and stdout
// importers have no prober: their lines carry no full date.
func (*jsonImp) ProbeTime(line []byte, opts importer.Options) (time.Time, bool) {
	jl, ok := decodeLine(line)
	if !ok || jl.Timestamp == nil {
		return time.Time{}, false
	}
	var loc *time.Location
	if opts.Host != nil {
		loc = opts.Host.TZ
	}
	t, _, why := parseStamp(strings.TrimSpace(*jl.Timestamp), loc, nil)
	if why != "" {
		return time.Time{}, false
	}
	return t, true
}

var _ importer.TimeProber = (*jsonImp)(nil)
