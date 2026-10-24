package rules

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/resource"
)

/* ---------------------------------------------------------------- RC1 */

// rc1 lists, per host, the processes with the highest mean CPU and the highest
// peak private bytes in the window (TopInWindow, Symptom). The CPU list ends
// with the rest of host CPU when both sides are percent of the host.
func (e *eval) rc1() {
	hostsWithHost := map[string]bool{}
	for _, id := range []string{resource.HostCPU, resource.HostMemAvailable, resource.HostDiskSecPerTx} {
		for _, s := range e.idx.Series(id, resource.Filter{}) {
			hostsWithHost[s.Host] = true
		}
	}
	hostsWithProc := map[string]bool{}
	for _, id := range []string{resource.ProcCPU, resource.ProcPrivate, resource.ProcWS} {
		for _, s := range e.idx.Series(id, resource.Filter{}) {
			hostsWithProc[s.Host] = true
		}
	}
	for h := range hostsWithHost {
		if !hostsWithProc[h] {
			e.gap(evidence.NotObservedGap("no per-process counters for " + h))
		}
	}
	thr := fmt.Sprintf("top %d series with %s %% of samples or more", e.th.TopN, num(e.th.MinCoverage*100))

	type entry struct {
		key string
		v   float64
	}
	// Mean process CPU, per host and unit.
	cpu := map[[2]string][]entry{}
	for _, s := range e.idx.Series(resource.ProcCPU, resource.Filter{}) {
		if !e.covered(s) {
			continue
		}
		if m, ok := e.mean(s); ok {
			k := [2]string{s.Host, s.Metric.Unit}
			cpu[k] = append(cpu[k], entry{s.Proc.Key(), m})
		}
	}
	for _, k := range sortedKeys(cpu) {
		list := cpu[k]
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].v != list[j].v {
				return list[i].v > list[j].v
			}
			return list[i].key < list[j].key
		})
		if len(list) > e.th.TopN {
			list = list[:e.th.TopN]
		}
		host, unit := k[0], k[1]
		var parts []string
		sum := 0.0
		for _, x := range list {
			parts = append(parts, x.key+" ("+value(x.v, unit)+")")
			sum += x.v
		}
		rest := "(sum of listed processes only)"
		if unit == "percent-host" {
			for _, hs := range e.idx.Series(resource.HostCPU, resource.Filter{Host: host}) {
				if hs.Metric.Unit != "percent-host" || !e.covered(hs) {
					continue
				}
				if hm, ok := e.mean(hs); ok {
					rest = "all other processes (" + value(math.Max(0, hm-sum), unit) + ")"
					break
				}
			}
		}
		parts = append(parts, rest)
		metric := "mean process CPU on " + host
		if unit == "percent-one-core" {
			metric = "mean process CPU (percent of one core) on " + host
		}
		e.add(Row{ID: "RC1", Host: host, Title: "Top processes by mean CPU", Label: audit.EvidenceSymptom,
			Wording: evidence.TopInWindow(metric, strings.Join(parts, ", ")), Thresholds: thr})
	}

	// Peak private bytes, per host.
	priv := map[[2]string][]entry{}
	ids := map[string][]string{}
	for _, s := range e.idx.Series(resource.ProcPrivate, resource.Filter{}) {
		if !e.covered(s) {
			continue
		}
		if sm, ok := e.peak(s); ok {
			k := [2]string{s.Host, ""}
			priv[k] = append(priv[k], entry{s.Proc.Key(), sm.Value})
			ids[s.Host+"\x00"+s.Proc.Key()] = append(ids[s.Host+"\x00"+s.Proc.Key()], sm.EvidenceID)
		}
	}
	for _, k := range sortedKeys(priv) {
		list := priv[k]
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].v != list[j].v {
				return list[i].v > list[j].v
			}
			return list[i].key < list[j].key
		})
		if len(list) > e.th.TopN {
			list = list[:e.th.TopN]
		}
		var parts, ev []string
		for _, x := range list {
			parts = append(parts, x.key+" ("+bytesText(x.v)+")")
			ev = append(ev, ids[k[0]+"\x00"+x.key]...)
		}
		e.add(Row{ID: "RC1", Host: k[0], Title: "Top processes by peak private bytes", Label: audit.EvidenceSymptom,
			Wording: evidence.TopInWindow("peak private bytes on "+k[0], strings.Join(parts, ", ")), Thresholds: thr, Evidence: ev})
	}
}

func sortedKeys[V any](m map[[2]string]V) [][2]string {
	keys := make([][2]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	return keys
}

// inWindow reports whether a sample time lies in the evaluation window.
func (e *eval) inWindow(t time.Time) bool { return e.w.Contains(t) }

// mean is the mean sample value of s in the window.
func (e *eval) mean(s resource.Series) (float64, bool) {
	sum, n := 0.0, 0
	for _, sm := range s.Samples {
		if e.inWindow(sm.End) {
			sum += sm.Value
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

// peak is the highest sample of s in the window.
func (e *eval) peak(s resource.Series) (resource.Sample, bool) {
	var best resource.Sample
	found := false
	for _, sm := range s.Samples {
		if e.inWindow(sm.End) && (!found || sm.Value > best.Value) {
			best, found = sm, true
		}
	}
	return best, found
}

// lowest is the lowest sample of s in the window.
func (e *eval) lowest(s resource.Series) (resource.Sample, bool) {
	var best resource.Sample
	found := false
	for _, sm := range s.Samples {
		if e.inWindow(sm.End) && (!found || sm.Value < best.Value) {
			best, found = sm, true
		}
	}
	return best, found
}

/* ---------------------------------------------------------------- RC2 */

// rc2 looks for a memory rise in private bytes, working set and Redis used
// memory. Each series is cut into segments at the start and restart markers
// of its host (and process); a segment is tested only when it holds
// MinBuckets samples or more over RiseSpan or more with enough coverage. A
// rise is MetricRose (Symptom); a short segment is a gap line, never a
// negative.
func (e *eval) rc2() {
	thr := fmt.Sprintf("rise of at least %s %% of the first value and at least %s, over %s or more, with %s %% of steps up; segments of %d samples or more, cut at start and restart markers",
		num(e.th.RiseFrac*100), bytesText(e.th.RiseBytes), duration(e.th.RiseSpan), num(e.th.RiseUpFrac*100), e.th.MinBuckets)
	markers := e.idx.Markers()
	for _, id := range []string{resource.ProcPrivate, resource.ProcWS, resource.RedisMem} {
		for _, s := range e.idx.Series(id, resource.Filter{}) {
			subject := s.Host
			if k := s.Proc.Key(); k != "" {
				subject += " " + k
			}
			for _, seg := range segments(s, markers, e.w) {
				if len(seg) == 0 {
					continue
				}
				first, last := seg[0], seg[len(seg)-1]
				span := last.End.Sub(first.End)
				if len(seg) < e.th.MinBuckets || span < e.th.RiseSpan || !e.segmentCovered(s, seg) {
					e.gap(fmt.Sprintf("%s %s: the segment from %s to %s has %d samples over %s; a rise is tested on %d or more samples over %s or more",
						subject, metricName(id), stamp(first.End), stamp(last.End), len(seg), duration(span), e.th.MinBuckets, duration(e.th.RiseSpan)))
					continue
				}
				delta := last.Value - first.Value
				up := 0
				for i := 1; i < len(seg); i++ {
					if seg[i].Value > seg[i-1].Value {
						up++
					}
				}
				if delta < e.th.RiseBytes || delta < e.th.RiseFrac*first.Value ||
					float64(up) < e.th.RiseUpFrac*float64(len(seg)-1) {
					continue
				}
				w := evidence.MetricRose(subject, metricName(id), bytesText(delta), duration(span), bytesText(first.Value), bytesText(last.Value))
				e.add(Row{ID: "RC2", Host: s.Host, Label: audit.EvidenceSymptom,
					Title:      "Memory rise, Theil-Sen slope " + bytesText(theilSen(seg)*float64(time.Hour/time.Second)) + "/h",
					Wording:    w,
					Thresholds: thr, Evidence: []string{first.EvidenceID, last.EvidenceID}})
				e.summary(s.Host, w)
			}
		}
	}
}

// segments cuts the window samples of s at the start and restart markers on
// its host that name no process or name its process. Samples inside an
// interval marker belong to neither side.
func segments(s resource.Series, markers []resource.Marker, w interface{ Contains(time.Time) bool }) [][]resource.Sample {
	var cuts []resource.Marker
	for _, m := range markers {
		if m.Host != s.Host || (m.Edge != resource.EdgeStart && m.Edge != resource.EdgeRestart) {
			continue
		}
		if !m.Proc.IsZero() && !strings.EqualFold(m.Proc.Name, s.Proc.Name) {
			continue
		}
		cuts = append(cuts, m)
	}
	sort.SliceStable(cuts, func(i, j int) bool { return cuts[i].Start.Before(cuts[j].Start) })
	var out [][]resource.Sample
	var cur []resource.Sample
	ci := 0
	for _, sm := range s.Samples {
		if !w.Contains(sm.End) {
			continue
		}
		inside := false
		for ci < len(cuts) && sm.End.After(cuts[ci].Start) {
			if !sm.End.After(cuts[ci].End) {
				inside = true
				break
			}
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			ci++
		}
		if inside {
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, sm)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// segmentCovered reports whether seg holds MinCoverage of the samples its span
// should hold at the series interval.
func (e *eval) segmentCovered(s resource.Series, seg []resource.Sample) bool {
	if s.Interval <= 0 {
		return false
	}
	span := seg[len(seg)-1].End.Sub(seg[0].End)
	want := float64(span/s.Interval) + 1
	return float64(len(seg)) >= e.th.MinCoverage*want
}

// theilSen is the median of the pairwise slopes of seg, per second. Long
// segments are thinned to at most 400 evenly spaced samples first.
func theilSen(seg []resource.Sample) float64 {
	pts := seg
	if len(pts) > 400 {
		thin := make([]resource.Sample, 0, 400)
		for i := 0; i < 400; i++ {
			thin = append(thin, pts[i*(len(pts)-1)/399])
		}
		pts = thin
	}
	var slopes []float64
	for i := 0; i < len(pts); i++ {
		for j := i + 1; j < len(pts); j++ {
			dt := pts[j].End.Sub(pts[i].End).Seconds()
			if dt > 0 {
				slopes = append(slopes, (pts[j].Value-pts[i].Value)/dt)
			}
		}
	}
	if len(slopes) == 0 {
		return 0
	}
	sort.Float64s(slopes)
	n := len(slopes)
	if n%2 == 1 {
		return slopes[n/2]
	}
	return (slopes[n/2-1] + slopes[n/2]) / 2
}

// itoa is strconv.Itoa, kept short for sentences.
func itoa(n int) string { return strconv.Itoa(n) }
