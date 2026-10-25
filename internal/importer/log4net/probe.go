package log4net

import (
	"sync"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/importer"
)

// patternCache keeps the headers compiled from an owner-supplied pattern, so a
// probe loop does not recompile it for every line. The cache is small: a case
// supplies one pattern per file.
var patternCache struct {
	mu sync.Mutex
	m  map[string]*header
}

func compiledPattern(p string) (*header, bool) {
	patternCache.mu.Lock()
	defer patternCache.mu.Unlock()
	if h, ok := patternCache.m[p]; ok {
		return h, h != nil
	}
	h, why := compile(p)
	if why != "" {
		h = nil
	}
	if patternCache.m == nil || len(patternCache.m) >= 16 {
		patternCache.m = map[string]*header{}
	}
	patternCache.m[p] = h
	return h, h != nil
}

// ProbeTime implements importer.TimeProber. With a supplied pattern it reads
// the date under that pattern; otherwise under the first default pattern the
// line matches. Host-local dates need opts.Host.TZ, as in Parse. Lines that are
// not a header (continuations) report false.
func (*textImp) ProbeTime(line []byte, opts importer.Options) (time.Time, bool) {
	text := string(line)
	var (
		h  *header
		sm []string
	)
	if p := opts.Supplied[SuppliedPattern]; p != "" {
		ch, ok := compiledPattern(p)
		if !ok {
			return time.Time{}, false
		}
		if sm = ch.re.FindStringSubmatch(text); sm != nil {
			h = ch
		}
	} else {
		for _, c := range defaultHeaders {
			if sm = c.re.FindStringSubmatch(text); sm != nil {
				h = c
				break
			}
		}
	}
	if h == nil || h.date <= 0 || h.date >= len(sm) {
		return time.Time{}, false
	}
	rule := h.rule
	if h.local && logTimeUTC(opts) {
		rule = clock.RuleUTCField
	}
	var loc *time.Location
	if rule == clock.RuleHostLocal {
		if opts.Host == nil || opts.Host.TZ == nil {
			return time.Time{}, false
		}
		loc = opts.Host.TZ
	}
	t, why := h.when(sm[h.date], rule, loc)
	if why != "" {
		return time.Time{}, false
	}
	return t, true
}

var _ importer.TimeProber = (*textImp)(nil)
