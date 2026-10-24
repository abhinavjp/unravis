package rules

import (
	"fmt"
	"sort"
	"strings"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/resource"
)

// usedMetrics are the metric ids the rules read; their coverage is checked
// first and listed per host.
var usedMetrics = []string{
	resource.HostCPU, resource.HostMemAvailable, resource.HostDiskSecPerTx,
	resource.ProcCPU, resource.ProcPrivate, resource.ProcWS,
	resource.CLRGCTime, resource.CLRGen2, resource.NetGCPause,
	resource.NetTPQueue, resource.ASPNetQueued, resource.KestrelQueued,
	resource.RedisMem, resource.RedisBlocked, resource.MongoQueue,
}

// coverageFirst lists the coverage of every used series per host and reports
// each series under MinCoverage as a gap. Such a series never gives a negative
// and is left out of the episode shares and the RC1 ranking.
func (e *eval) coverageFirst() {
	for _, id := range usedMetrics {
		for _, s := range e.idx.Series(id, resource.Filter{}) {
			cov := e.idx.Coverage(s)
			label := seriesLabel(s)
			seen := e.covLabels[s.Host]
			if seen == nil {
				seen = map[string]bool{}
				e.covLabels[s.Host] = seen
			}
			if seen[label] {
				label += " from " + displayName(s.Source)
			}
			seen[label] = true
			e.coverage[s.Host] = append(e.coverage[s.Host], Coverage{Metric: label, Fraction: roundTo(cov, 3)})
			e.hosts[s.Host] = true
			if cov < e.th.MinCoverage {
				e.gap(evidence.NotObservedGap(fmt.Sprintf("%d %% of samples present for %s on %s", int(cov*100), label, s.Host)))
			}
		}
	}
}

func roundTo(v float64, places int) float64 {
	p := 1.0
	for i := 0; i < places; i++ {
		p *= 10
	}
	return float64(int64(v*p+0.5)) / p
}

// covered reports whether s has enough samples to count.
func (e *eval) covered(s resource.Series) bool {
	return e.idx.Coverage(s) >= e.th.MinCoverage
}

// condition is one sample test of RC3, RC4, RC5, RC6 or RC9.
type condition struct {
	rule       string
	metric     string
	title      string
	share      string // EpisodeShare condition, "%s" is the host
	thresholds string
	minSamples int                                   // qualifying samples per series, per episode or slot
	test       func(resource.Sample) (float64, bool) // the value shown, and whether it qualifies
	lower      bool                                  // the extreme sample is the lowest, not the highest
}

// runCondition writes, per episode and series, one MetricSampleOverlap row for
// the extreme qualifying sample (Correlation), and per host one EpisodeShare
// row (Symptom) over the series with enough coverage.
func (e *eval) runCondition(c condition) {
	if c.minSamples < 1 {
		c.minSamples = 1
	}
	byHost := map[string][]resource.Series{}
	var hosts []string
	for _, s := range e.idx.Series(c.metric, resource.Filter{}) {
		if _, ok := byHost[s.Host]; !ok {
			hosts = append(hosts, s.Host)
		}
		byHost[s.Host] = append(byHost[s.Host], s)
	}
	sort.Strings(hosts)
	thr := c.thresholds + "; " + e.commonThresholds()
	for _, host := range hosts {
		epHit := map[int]bool{}
		hostSlots := map[int64]bool{}
		var shareIDs []string
		anyCovered := false
		for _, s := range byHost[host] {
			ok := e.covered(s)
			anyCovered = anyCovered || ok
			slots := slotCounter{}
			perEp := map[int][]resource.Sample{}
			for _, sm := range s.Samples {
				if _, q := c.test(sm); !q {
					continue
				}
				if ok {
					e.slots.hit(slots, sm.Start, sm.End)
				}
				for i := range e.eps {
					if e.inEpisode(i, sm.Start, sm.End) {
						perEp[i] = append(perEp[i], sm)
					}
				}
			}
			for i, sms := range perEp {
				if len(sms) < c.minSamples {
					continue
				}
				best := sms[0]
				bv, _ := c.test(best)
				for _, sm := range sms[1:] {
					v, _ := c.test(sm)
					if (c.lower && v < bv) || (!c.lower && v > bv) {
						best, bv = sm, v
					}
				}
				e.add(Row{ID: c.rule, Host: host, Title: c.title + " sample in a stall", Label: audit.EvidenceCorrelation,
					Wording:    evidence.MetricSampleOverlap(sampleSource(s.Source), 1, seriesMetric(s), value(bv, s.Metric.Unit)),
					Thresholds: thr, Evidence: []string{best.EvidenceID}, Episode: e.episode(i)})
				if ok {
					epHit[i] = true
					for _, sm := range sms {
						shareIDs = append(shareIDs, sm.EvidenceID)
					}
				}
			}
			for i, n := range slots {
				if n >= c.minSamples {
					hostSlots[i] = true
				}
			}
		}
		if !anyCovered || len(e.eps) == 0 {
			continue
		}
		e.add(Row{ID: c.rule, Host: host, Title: c.title + " in stalls and outside", Label: audit.EvidenceSymptom,
			Wording:    evidence.EpisodeShare(strings.ReplaceAll(c.share, "%s", host), len(epHit), len(e.eps), len(hostSlots), e.slots.total),
			Thresholds: thr, Evidence: shareIDs})
	}
}

// pointShare writes one EpisodeShare row per host for point events (log
// entries): an episode counts when an event falls in one of its stalls, a
// comparison window when an event falls in it.
func (e *eval) pointShare(rule, title, share, thresholds string, evs []evidence.Event) {
	byHost := map[string][]evidence.Event{}
	var hosts []string
	for _, ev := range evs {
		if _, ok := byHost[ev.Host]; !ok {
			hosts = append(hosts, ev.Host)
		}
		byHost[ev.Host] = append(byHost[ev.Host], ev)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		if len(e.eps) == 0 {
			continue
		}
		epHit := map[int]bool{}
		slots := slotCounter{}
		var ids []string
		for _, ev := range byHost[host] {
			e.slots.hit(slots, ev.Start, ev.End)
			for i := range e.eps {
				if e.inEpisode(i, ev.Start, ev.End) {
					epHit[i] = true
					ids = append(ids, ev.EvidenceID)
				}
			}
		}
		e.add(Row{ID: rule, Host: host, Title: title, Label: audit.EvidenceSymptom,
			Wording:    evidence.EpisodeShare(strings.ReplaceAll(share, "%s", host), len(epHit), len(e.eps), slots.count(1), e.slots.total),
			Thresholds: thresholds + "; " + e.commonThresholds(), Evidence: ids})
	}
}

// above is a test for "value above min".
func above(min float64) func(resource.Sample) (float64, bool) {
	return func(s resource.Sample) (float64, bool) { return s.Value, s.Value > min }
}

// atLeast is a test for "value at or above min".
func atLeast(min float64) func(resource.Sample) (float64, bool) {
	return func(s resource.Sample) (float64, bool) { return s.Value, s.Value >= min }
}
