package applog

import (
	"sort"
	"time"

	"api-audit-explorer/internal/evidence"
)

// rateKey is one log-rate bucket: a UTC minute × level × logger label ×
// family (plan §2.3). The time rule is part of the key so a bucket never
// mixes two rules.
type rateKey struct {
	minute                      int64 // Unix minutes, UTC
	level, logger, family, rule string
}

type rateAcc struct {
	count    int64
	line     int64 // first source line in the bucket (provenance anchor)
	severity string
}

// rateOrdinalBase keeps log-rate ordinals clear of the per-line ordinals of
// Add (0..3), so no two events of one import share an evidence id.
const rateOrdinalBase = 16

// maxRateBuckets bounds the bucket map when the caller set no event limit.
const maxRateBuckets = 2_000_000

// count adds one line to its bucket. It reports whether a new bucket was made.
func (s *Sink) count(k rateKey, line int64, sev string) bool {
	if a, ok := s.rates[k]; ok {
		a.count++
		return false
	}
	s.rates[k] = &rateAcc{count: 1, line: line, severity: sev}
	return true
}

// flushRates emits one log-rate point per bucket, in a fixed order: minute,
// level, logger, family, rule. A bucket's Start and End are its minute.
func (s *Sink) flushRates() error {
	keys := make([]rateKey, 0, len(s.rates))
	for k := range s.rates {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.minute != b.minute {
			return a.minute < b.minute
		}
		if a.level != b.level {
			return a.level < b.level
		}
		if a.logger != b.logger {
			return a.logger < b.logger
		}
		if a.family != b.family {
			return a.family < b.family
		}
		return a.rule < b.rule
	})
	for i, k := range keys {
		a := s.rates[k]
		t := time.Unix(k.minute*60, 0).UTC()
		v := float64(a.count)
		attrs := map[string]string{AttrFamily: k.family}
		if k.level != "" {
			attrs[AttrLevel] = k.level
		}
		if k.logger != "" {
			attrs[AttrLogger] = k.logger
		}
		ev := evidence.Event{
			EvidenceID: evidence.EventID(s.opts.CaseID, s.src, s.opts.ImportNonce, a.line, rateOrdinalBase+i),
			Source:     s.src, Kind: KindLogRate, Host: s.host,
			Start: t, End: t, Value: &v, Unit: "count",
			Severity: a.severity, Line: a.line, TZRule: k.rule, Attrs: attrs,
		}
		if err := s.send(ev); err != nil {
			return err
		}
	}
	s.rates = map[rateKey]*rateAcc{}
	return nil
}
