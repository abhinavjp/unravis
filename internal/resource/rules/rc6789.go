package rules

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer/iis"
	"api-audit-explorer/internal/importer/mongodlog"
	"api-audit-explorer/internal/join"
	"api-audit-explorer/internal/resource"
)

/* ------------------------------------------------------------ overlap joins */

// spanMs is the length of an event in ms: DurationMs when set, else End-Start.
func spanMs(ev evidence.Event) float64 {
	if ev.DurationMs != nil {
		return *ev.DurationMs
	}
	return float64(ev.End.Sub(ev.Start)) / float64(time.Millisecond)
}

// overlap joins the events of the given kinds with the stalls, per host,
// source and kind. A unique match is OverlapUnique (Correlation); a shared
// single candidate is OverlapShared and several candidates OverlapAmbiguous
// (Symptom). Only events with a length qualify. It returns, per host, the
// episodes that had a candidate.
func (e *eval) overlap(rule string, kinds []string, thresholds string) map[string]map[int]bool {
	hit := map[string]map[int]bool{}
	if len(e.stalls) == 0 {
		return hit
	}
	type key struct{ host, source, kind string }
	groups := map[key][]evidence.Event{}
	var keys []key
	for _, k := range kinds {
		for _, ev := range e.events[k] {
			g := key{ev.Host, ev.Source, ev.Kind}
			if _, ok := groups[g]; !ok {
				keys = append(keys, g)
			}
			groups[g] = append(groups[g], ev)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.host != b.host {
			return a.host < b.host
		}
		if a.source != b.source {
			return a.source < b.source
		}
		return a.kind < b.kind
	})
	thr := thresholds + "; events with a length only; time tolerance 1 s"
	qualifies := func(ev evidence.Event) bool { return spanMs(ev) > 0 }
	for _, g := range keys {
		evs := groups[g]
		byID := map[string]evidence.Event{}
		for _, ev := range evs {
			byID[ev.EvidenceID] = ev
		}
		matches := join.Overlap(e.stalls, evs, join.Tolerance{Resolution: stallTolerance}, qualifies)
		shared := map[string]int{}
		for _, m := range matches {
			if len(m.Candidates) == 1 {
				shared[m.Candidates[0]]++
			}
		}
		for i, m := range matches {
			if len(m.Candidates) == 0 {
				continue
			}
			ep := e.epOf[e.stalls[i].ID]
			if hit[g.host] == nil {
				hit[g.host] = map[int]bool{}
			}
			hit[g.host][ep] = true
			r := Row{ID: rule, Host: g.host, Thresholds: thr, Evidence: m.Candidates, Episode: e.episode(ep)}
			switch {
			case m.Class == evidence.JoinUnique:
				ev := byID[m.Candidates[0]]
				r.Title = displayName(g.source) + " " + eventPhrase(g.kind) + " in a stall"
				r.Label = audit.EvidenceCorrelation
				r.Wording = evidence.OverlapUnique(displayName(g.source), eventPhrase(g.kind), math.Round(spanMs(ev))/1000)
			case len(m.Candidates) == 1:
				r.Title = displayName(g.source) + " " + eventPhrase(g.kind) + " shared by stalls"
				r.Label = audit.EvidenceSymptom
				r.Wording = evidence.OverlapShared(max(shared[m.Candidates[0]], 2))
			default:
				r.Title = displayName(g.source) + " " + eventPhrase(g.kind) + "s in a stall"
				r.Label = audit.EvidenceSymptom
				r.Wording = evidence.OverlapAmbiguous(len(m.Candidates))
			}
			e.add(r)
		}
	}
	return hit
}

/* ---------------------------------------------------------------- RC6 */

var slowlogThresholdRe = regexp.MustCompile(`^commands slower than (\d+) microseconds$`)

// redisSources are the Redis importers.
var redisSources = []string{"redis-slowlog", "redis-latency", "redis-info", "redis-log"}

// rc6 joins Redis slow commands and latency events with the stalls, tests
// Redis blocked clients, and words the absence of a slow command per episode.
func (e *eval) rc6() {
	hit := e.overlap("RC6", []string{"slow-command", "latency-spike"}, "Redis SLOWLOG and latency monitor events overlapping a stall")
	e.runCondition(condition{rule: "RC6", metric: resource.RedisBlocked, title: "Redis blocked clients",
		share: "Redis blocked clients above 0 on %s", thresholds: "Redis blocked clients above 0", test: above(0)})

	slowlog := e.importsOf("redis-slowlog")
	for host, recs := range e.importsOf(redisSources...) {
		if host != "" && len(slowlog[host]) == 0 && len(recs) > 0 {
			e.gap(evidence.NotObservedGap("no Redis SLOWLOG import for " + host))
		}
	}
	hosts := make([]string, 0, len(slowlog))
	for h := range slowlog {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		recs := slowlog[host]
		for _, r := range recs {
			for _, g := range r.KnownGaps {
				e.gap(g)
			}
		}
		idGap := e.slowlogIDGap(host)
		for i, ep := range e.eps {
			if hit[host][i] {
				continue
			}
			wording := slowlogAbsence(host, recs, ep, idGap)
			e.add(Row{ID: "RC6", Host: host, Title: "No Redis SLOWLOG entry in this episode", Label: audit.EvidenceSymptom,
				Wording:    wording,
				Thresholds: "threshold wording only when slowlog-log-slower-than is stated, the SLOWLOG coverage holds the episode and the ids have no gap",
				Episode:    e.episode(i)})
		}
	}
}

// slowlogAbsence words the absence of a slow command in ep for host: the
// threshold wording when one import states its threshold and covers the
// episode and the ids have no gap, else the gap wording naming the first thing
// missing in the import that starts first.
func slowlogAbsence(host string, recs []casestore.ImportRecord, ep Episode, idGap bool) string {
	sorted := append([]casestore.ImportRecord(nil), recs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].Quality.CoverageFirst, sorted[j].Quality.CoverageFirst
		if a == nil || b == nil {
			return a != nil
		}
		return a.Before(*b)
	})
	first := ""
	for _, r := range sorted {
		q := r.Quality
		reason := ""
		m := slowlogThresholdRe.FindStringSubmatch(q.Threshold)
		switch {
		case m == nil:
			reason = "slowlog-log-slower-than not stated for " + host
		case q.CoverageFirst == nil || q.CoverageLast == nil:
			reason = "the Redis SLOWLOG coverage on " + host + " is unknown"
		case q.CoverageFirst.After(ep.Start):
			reason = "the Redis SLOWLOG coverage on " + host + " starts at " + stamp(*q.CoverageFirst) + ", after this interval starts"
		case q.CoverageLast.Before(ep.End):
			reason = "the Redis SLOWLOG coverage on " + host + " ends at " + stamp(*q.CoverageLast) + ", before this interval ends"
		case idGap:
			reason = "the Redis SLOWLOG ids on " + host + " have a gap, so entries may be missing"
		}
		if reason == "" {
			return evidence.NotObservedThreshold(displayName("redis-slowlog"), "commands slower than "+m[1]+" µs (execution time only)")
		}
		if first == "" {
			first = reason
		}
	}
	return evidence.NotObservedGap(first)
}

// slowlogIDGap reports whether the SLOWLOG ids seen on host skip a number.
func (e *eval) slowlogIDGap(host string) bool {
	var ids []int64
	seen := map[int64]bool{}
	for _, ev := range e.events["slow-command"] {
		if ev.Host != host || ev.Source != "redis-slowlog" {
			continue
		}
		id, err := strconv.ParseInt(ev.Attrs["id"], 10, 64)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i := 1; i < len(ids); i++ {
		if ids[i]-ids[i-1] > 1 {
			return true
		}
	}
	return false
}

/* ---------------------------------------------------------------- RC7 */

// rc7 compares ERROR entries per minute inside stall episodes with the 95th
// percentile of the minutes outside them, and lists the busiest log families.
func (e *eval) rc7() {
	logs := e.importsOf(appLogSources...)
	others := e.importsOf("win-counters", "win-procsnap", "dotnet-counters")
	for host := range others {
		if host != "" && len(logs[host]) == 0 {
			e.gap(evidence.NotObservedGap("no app log for " + host))
		}
	}
	perHost := map[string]map[int64]float64{}
	ids := map[string]map[int64][]string{}
	families := map[string]map[string]float64{}
	for _, ev := range e.events["log-rate"] {
		if ev.Value == nil {
			continue
		}
		if f := ev.Attrs["family"]; f != "" && f != "other" {
			if families[ev.Host] == nil {
				families[ev.Host] = map[string]float64{}
			}
			families[ev.Host][f] += *ev.Value
		}
		if ev.Severity != "error" && ev.Severity != "critical" {
			continue
		}
		minute := ev.Start.Unix() / 60
		if perHost[ev.Host] == nil {
			perHost[ev.Host] = map[int64]float64{}
			ids[ev.Host] = map[int64][]string{}
		}
		perHost[ev.Host][minute] += *ev.Value
		ids[ev.Host][minute] = append(ids[ev.Host][minute], ev.EvidenceID)
	}
	var hosts []string
	for h := range families {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		type fam struct {
			name string
			n    float64
		}
		var list []fam
		for f, n := range families[h] {
			list = append(list, fam{f, n})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].n != list[j].n {
				return list[i].n > list[j].n
			}
			return list[i].name < list[j].name
		})
		if len(list) > e.th.TopN {
			list = list[:e.th.TopN]
		}
		var parts []string
		for _, f := range list {
			parts = append(parts, f.name+" ("+num(f.n)+")")
		}
		e.add(Row{ID: "RC7", Host: h, Title: "Busiest log families", Label: audit.EvidenceSymptom,
			Wording:    evidence.TopInWindow("log entries by family on "+h, strings.Join(parts, ", ")),
			Thresholds: fmt.Sprintf("top %d families by entries in the window", e.th.TopN)})
	}

	if e.w.From.IsZero() || e.w.To.IsZero() || len(e.eps) == 0 {
		return
	}
	firstMin, lastMin := e.w.From.Unix()/60, e.w.To.Unix()/60
	if e.w.To.Unix()%60 == 0 {
		lastMin-- // the window end is the start of a minute outside it
	}
	if lastMin < firstMin {
		return
	}
	// Minutes inside episodes widened by Quiet.
	var inside [][2]int64
	for _, ep := range e.eps {
		lo := ep.Start.Add(-e.th.Quiet).Unix() / 60
		hi := ep.End.Add(e.th.Quiet).Unix() / 60
		lo, hi = max(lo, firstMin), min(hi, lastMin)
		if hi < lo {
			continue
		}
		if k := len(inside); k > 0 && lo <= inside[k-1][1]+1 {
			inside[k-1][1] = max(inside[k-1][1], hi)
			continue
		}
		inside = append(inside, [2]int64{lo, hi})
	}
	isInside := func(m int64) bool {
		for _, r := range inside {
			if m >= r[0] && m <= r[1] {
				return true
			}
		}
		return false
	}
	outside := lastMin - firstMin + 1
	for _, r := range inside {
		outside -= r[1] - r[0] + 1
	}
	if outside <= 0 {
		return
	}
	var hostsRate []string
	for h := range perHost {
		hostsRate = append(hostsRate, h)
	}
	sort.Strings(hostsRate)
	for _, h := range hostsRate {
		var vals []float64
		for m, v := range perHost[h] {
			if m >= firstMin && m <= lastMin && !isInside(m) {
				vals = append(vals, v)
			}
		}
		sort.Float64s(vals)
		p95 := 0.0
		rank := int64(math.Ceil(0.95 * float64(outside)))
		zeros := outside - int64(len(vals))
		if rank > zeros {
			p95 = vals[rank-zeros-1]
		}
		epHit := map[int]bool{}
		slots := slotCounter{}
		var evIDs []string
		for m, v := range perHost[h] {
			if v <= p95 {
				continue
			}
			start := time.Unix(m*60, 0).UTC()
			end := start.Add(time.Minute)
			e.slots.hit(slots, start, end)
			for i := range e.eps {
				if e.inEpisode(i, start, end) {
					epHit[i] = true
					evIDs = append(evIDs, ids[h][m]...)
				}
			}
		}
		e.add(Row{ID: "RC7", Host: h, Title: "ERROR entries per minute in stalls and outside", Label: audit.EvidenceSymptom,
			Wording: evidence.EpisodeShare(fmt.Sprintf("ERROR entries above %s/min on %s", num(p95), h),
				len(epHit), len(e.eps), slots.count(1), e.slots.total),
			Thresholds: "above the 95th percentile (nearest rank, empty minutes counted) of ERROR and CRITICAL entries per minute outside stall episodes; " + e.commonThresholds(),
			Evidence:   evIDs})
	}
}

/* ---------------------------------------------------------------- RC8 */

var reasonRe = regexp.MustCompile(`\(reason: ([^)]*)\)`)

// markerPhrase names a marker for MarkerBefore ("... after a <phrase> on host").
func markerPhrase(m resource.Marker) string {
	key := m.Proc.Key()
	switch m.Kind {
	case "apppool-recycle":
		p := "recycle event of the application pool"
		if r := reasonRe.FindStringSubmatch(m.Text); r != nil {
			p += " (reason: " + r[1] + ")"
		}
		return p
	case "apppool-idle-shutdown":
		return "shutdown event of the idle application pool"
	case "worker-process-failure":
		return "worker process failure event"
	case "host-unexpected-shutdown":
		return "shutdown event marked unexpected"
	case "app-start":
		return "start entry of the application"
	case "app-session":
		return "session entry of the application"
	case "app-stop":
		return "stop entry of the application"
	case "process-start":
		return withKey("process start", key)
	case "process-gone":
		return withKey("process exit window", key)
	case "redis-start":
		return "Redis start"
	case "redis-stop":
		return "Redis stop"
	case "mongod-start":
		return "mongod start"
	case "mongod-stop":
		return "mongod stop"
	case resource.KindNewPID:
		return withKey("new process id", key)
	case resource.KindUptimeReset:
		return withKey("drop in an uptime counter", key)
	}
	return "marker (" + strings.ReplaceAll(m.Kind, "-", " ") + ")"
}

func withKey(p, key string) string {
	if key == "" {
		return p
	}
	return p + " for " + key
}

// rc8 lists the markers that end at most MarkerLead before an episode starts
// (MarkerBefore, Correlation). The recycle-event coverage gap is always shown.
func (e *eval) rc8() {
	recycleGap := ""
	for _, r := range e.imports {
		for _, g := range r.KnownGaps {
			if strings.Contains(g, "logEventOnRecycle") {
				e.gap(g)
				recycleGap = g
			}
		}
	}
	if recycleGap == "" {
		e.gap(iis.RecycleGap(""))
	}
	thr := "marker ending " + duration(e.th.MarkerLead) + " or less before the episode starts"
	for _, m := range e.idx.Markers() {
		for i, ep := range e.eps {
			if m.End.After(ep.Start) || ep.Start.Sub(m.End) > e.th.MarkerLead {
				continue
			}
			host := m.Host
			if host == "" {
				host = "an unnamed host"
			}
			e.add(Row{ID: "RC8", Host: m.Host, Title: "Marker before an episode", Label: audit.EvidenceCorrelation,
				Wording:    evidence.MarkerBefore(duration(ep.Start.Sub(m.End)), markerPhrase(m), host),
				Thresholds: thr, Evidence: m.EvidenceIDs, Episode: e.episode(i)})
		}
	}
}

/* ---------------------------------------------------------------- RC9 */

// rc9 joins MongoDB slow and running operations with the stalls, tests the
// MongoDB queue, and words the absence of a slow operation per episode for
// each mongod.log import.
func (e *eval) rc9() {
	hit := e.overlap("RC9", []string{"slow-op", "inflight-op"}, "MongoDB slow and running operations overlapping a stall")
	e.runCondition(condition{rule: "RC9", metric: resource.MongoQueue, title: "MongoDB queue",
		share: "MongoDB queue length above 0 on %s", thresholds: "MongoDB queue length above 0", test: above(0)})

	logs := e.importsOf(mongodlog.Name)
	hosts := make([]string, 0, len(logs))
	for h := range logs {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		for i, ep := range e.eps {
			if hit[host][i] {
				continue
			}
			// The first import that supports the threshold wording wins;
			// else the gap wording of the first import.
			wording := ""
			for _, r := range logs[host] {
				w := mongodlog.Absence(r.Quality, join.Interval{Start: ep.Start, End: ep.End})
				if wording == "" || mongodlog.AbsenceGap(r.Quality, join.Interval{Start: ep.Start, End: ep.End}) == "" {
					wording = w
				}
			}
			e.add(Row{ID: "RC9", Host: host, Title: "No mongod.log slow operation in this episode", Label: audit.EvidenceSymptom,
				Wording:    wording,
				Thresholds: "threshold wording only when the mongod version, slowms, the offset rule and full coverage of the episode are known",
				Episode:    e.episode(i)})
		}
	}
}
