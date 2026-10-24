package audit

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

// Rule B6g (incident-gap addendum §1.2): is audit create slower for a request
// that follows a quiet gap? It is opt-in (PatternInput.IdleGaps) because it reads
// every row in the window, not only the stalls.
//
// Only synchronous (Web API) rows are used, for the gaps and for the audit-create
// times alike. A Hangfire row stores its creation time before the insert, so its
// audit-create time leaves the insert out; those rows are left out and counted.
// The gap of a row is its start minus the start of the row before it in the same
// source, so gaps are measured across every server writing to that source. For
// that reason the label is always Symptom.
const (
	// MaxGapRows caps the rows read per source. It is a proposal until the lab
	// measurement of the full-window read is done (addendum §1.2, R14).
	MaxGapRows = 20000

	// idleReferenceS is the upper edge of the reference band, in seconds.
	idleReferenceS = 30
	// idleRaisedLift is the lift at or above which the rule says "raised".
	idleRaisedLift = 5.0
	// idleFalsifyLift and idleFalsifyRows say "not observed": the lift is at most
	// idleFalsifyLift with at least idleFalsifyRows rows in the gap band.
	idleFalsifyLift = 1.5
	idleFalsifyRows = 50
)

// gapRowCap is the row cap in use. Tests lower it.
var gapRowCap = MaxGapRows

// idleBandFromS are the lower edges of the gap bands, in seconds. A band runs up
// to the next edge; the last band has no upper edge.
var idleBandFromS = []int{0, idleReferenceS, 60, 240, 600}

// idleLiftGapsS are the gaps G, in seconds, that the lift is given for.
var idleLiftGapsS = []int{60, 240, 600}

func idleBandOf(gap time.Duration) int {
	band := 0
	for i, from := range idleBandFromS {
		if gap >= time.Duration(from)*time.Second {
			band = i
		}
	}
	return band
}

// fmtGap prints a gap in seconds as "30 s" or "10 min".
func fmtGap(seconds int) string {
	if seconds%60 == 0 && seconds >= 60 {
		return strconv.Itoa(seconds/60) + " min"
	}
	return strconv.Itoa(seconds) + " s"
}

// gapCandidate is one gap G with its lift and the audit-create times behind it.
type gapCandidate struct {
	gS    int
	value float64
	pool  []float64
}

// buildIdleGaps runs B6g. It returns the table, at most one finding, the
// provenance of its reads and warnings. A live window over 24 hours, and a live
// read that is not a narrow scan, fail closed with ErrBroadScanRefused: the
// caller asked for this rule by name, so it is told it was refused. Any other
// read failure and a row cap leave the result partial, with the lift withheld.
func buildIdleGaps(ctx context.Context, repo Repository, in PatternInput, p PatternParams, r PatternReport) (*IdleGaps, []Finding, []Provenance, []string, error) {
	live := isLiveRead(r.Provenance)
	if live && in.End.Sub(in.Start) > liveReadMaxWindow {
		return nil, nil, nil, nil, fmt.Errorf("%w: quiet-gap analysis reads every request in the window, so on a live source the window can be at most %d hours",
			ErrBroadScanRefused, int(liveReadMaxWindow/time.Hour))
	}

	samples := make([][]float64, len(idleBandFromS))
	var (
		prov              []Provenance
		warnings          []string
		excludedHangfire  int
		withoutAudit      int
		partial           bool
		partialReason     string
		failedSources     int
		cutSources        int
		rowsRead, rowsUse int
	)
	for _, src := range in.SourceIDs {
		q := patternQuery(PatternInput{SourceIDs: []string{src}, Start: in.Start, End: in.End}, []string{src})
		if live {
			q.QueryMode = QueryModeHint
		}
		rows, cut, readProv, err := readCappedPages(ctx, PageInput{Query: q, Limit: patternAuditPage, Order: OrderNewest}, repo.Requests, gapRowCap)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, nil, nil, ctx.Err()
			}
			failedSources++
			continue
		}
		merged := mergeGapProvenance(readProv, cut)
		prov = append(prov, merged...)
		if live && !allNarrow(merged) {
			return nil, nil, nil, nil, fmt.Errorf("%w: the read of every request in the window would not be a narrow scan", ErrBroadScanRefused)
		}
		if cut {
			cutSources++
		}
		rowsRead += len(rows)
		used, hangfire, noAudit := foldGapRows(rows, samples)
		rowsUse += used
		excludedHangfire += hangfire
		withoutAudit += noAudit
	}

	switch {
	case failedSources > 0:
		partial, partialReason = true, "a read failed"
		warnings = append(warnings, fmt.Sprintf("Quiet-gap analysis could not read %d of %d %s (the source returned an error). It is partial and the lift is withheld.",
			failedSources, len(in.SourceIDs), plural(len(in.SourceIDs), "source", "sources")))
	case cutSources > 0:
		partial, partialReason = true, fmt.Sprintf("row cap reached (%d rows per source)", gapRowCap)
		warnings = append(warnings, fmt.Sprintf("Quiet-gap analysis read only the newest %d rows per source. It is partial and the lift is withheld.", gapRowCap))
	}
	if excludedHangfire > 0 {
		warnings = append(warnings, fmt.Sprintf("%d Hangfire %s left out of the quiet-gap analysis: their audit-create time does not include the insert.",
			excludedHangfire, plural(excludedHangfire, "row was", "rows were")))
	}
	if withoutAudit > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s with no audit-create time left out of the quiet-gap analysis.", withoutAudit, plural(withoutAudit, "row", "rows")))
	}

	table, candidates := idleTable(samples, p, partial)
	table.ExcludedHangfire, table.Partial, table.PartialReason = excludedHangfire, partial, partialReason
	return table, idleFindings(in, p, candidates, samples[0]), prov, warnings, nil
}

// foldGapRows adds one source's rows to the band samples. A row is used when it is
// synchronous, has a start and has a row before it; rows with no usable
// audit-create time are counted and left out of the samples but still end a gap.
func foldGapRows(rows []RequestRecord, samples [][]float64) (used, hangfire, withoutAudit int) {
	var sync []RequestRecord
	for _, r := range rows {
		switch {
		case r.Kind == "hangfire":
			hangfire++
		case r.Kind == "synchronous" && r.InitiatedAt != nil:
			sync = append(sync, r)
		}
	}
	sort.SliceStable(sync, func(i, j int) bool {
		if !sync[i].InitiatedAt.Equal(*sync[j].InitiatedAt) {
			return sync[i].InitiatedAt.Before(*sync[j].InitiatedAt)
		}
		return sync[i].ID < sync[j].ID
	})
	// The first row has no known gap and is not used.
	for i := 1; i < len(sync); i++ {
		r := sync[i]
		if !usableMs(r.AuditCreateTimeMs) {
			withoutAudit++
			continue
		}
		band := idleBandOf(sync[i].InitiatedAt.Sub(*sync[i-1].InitiatedAt))
		samples[band] = append(samples[band], *r.AuditCreateTimeMs)
		used++
	}
	return used, hangfire, withoutAudit
}

// idleTable builds the bands and the lifts. Statistics of a band with fewer than
// MinSampleCount rows are null. A lift is withheld when the result is partial,
// when either side has fewer than MinSampleCount rows, or by nature never.
//
// A reference band with no slow row would make the ratio infinite, so it counts
// half a row there: the lift is then the smallest the data allow, never a larger one.
func idleTable(samples [][]float64, p PatternParams, partial bool) (*IdleGaps, []gapCandidate) {
	slowOf := func(values []float64) int {
		n := 0
		for _, v := range values {
			if v >= p.AuditStallMs {
				n++
			}
		}
		return n
	}
	table := &IdleGaps{Bands: make([]IdleBand, 0, len(idleBandFromS)), Lift: make([]IdleLift, 0, len(idleLiftGapsS))}
	for i, from := range idleBandFromS {
		band := IdleBand{FromS: from, N: len(samples[i])}
		if i+1 < len(idleBandFromS) {
			to := idleBandFromS[i+1]
			band.ToS = &to
		}
		if band.N >= MinSampleCount {
			median, p95 := medianOf(samples[i]), nearestRank(samples[i], 0.95)
			rate := float64(slowOf(samples[i])) / float64(band.N)
			band.MedianMs, band.P95Ms, band.SlowRate = &median, &p95, &rate
		}
		table.Bands = append(table.Bands, band)
	}

	ref := samples[0]
	var candidates []gapCandidate
	for _, g := range idleLiftGapsS {
		var pool []float64
		for i, from := range idleBandFromS {
			if from >= g {
				pool = append(pool, samples[i]...)
			}
		}
		lift := IdleLift{GS: g}
		if partial || len(pool) < MinSampleCount || len(ref) < MinSampleCount {
			lift.Withheld = true
		} else {
			refRate := math.Max(float64(slowOf(ref)), 0.5) / float64(len(ref))
			value := math.Round(float64(slowOf(pool))/float64(len(pool))/refRate*100) / 100
			lift.Value = &value
			candidates = append(candidates, gapCandidate{gS: g, value: value, pool: pool})
		}
		table.Lift = append(table.Lift, lift)
	}
	return table, candidates
}

// idleFindings prints the finding: raised after quiet gaps for the G with the
// largest lift (the longer gap on a tie), or "not observed" for the longest G
// whose gap band has enough rows and a lift near 1. It prints nothing when
// neither holds.
func idleFindings(in PatternInput, p PatternParams, candidates []gapCandidate, ref []float64) []Finding {
	var raised *gapCandidate
	for i := range candidates {
		c := &candidates[i]
		if c.value >= idleRaisedLift && (raised == nil || c.value >= raised.value) {
			raised = c
		}
	}
	if raised != nil {
		n := len(raised.pool)
		return []Finding{{
			Rule: "B6g", Label: EvidenceSymptom, N: n, LowN: n < LowNRows, EvidenceID: patternEvidenceID("B6g", in, p),
			Headline: fmt.Sprintf("Raised after quiet gaps. After quiet gaps of at least %s, audit create took %s s at the median (n = %d). After gaps under %d s it took %s s (n = %d). Audit create is measured from the client side and covers setup work and the database write, not just the write.",
				fmtGap(raised.gS), fmtSeconds(medianOf(raised.pool)), n, idleReferenceS, fmtSeconds(medianOf(ref)), len(ref)),
		}}
	}
	var flat *gapCandidate
	for i := range candidates {
		c := &candidates[i]
		if c.value <= idleFalsifyLift && len(c.pool) >= idleFalsifyRows && (flat == nil || c.gS > flat.gS) {
			flat = c
		}
	}
	if flat != nil {
		n := len(flat.pool)
		return []Finding{{
			Rule: "B6g", Label: EvidenceSymptom, N: n, LowN: n < LowNRows, EvidenceID: patternEvidenceID("B6g", in, p),
			Headline: fmt.Sprintf("Not observed: audit create was not slower after quiet gaps of at least %s in this window.", fmtGap(flat.gS)),
		}}
	}
	return nil
}

// medianOf is the middle value, or the mean of the two middle values.
func medianOf(values []float64) float64 {
	s := append([]float64(nil), values...)
	sort.Float64s(s)
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// nearestRank is the value at rank ceil(q * n) of the sorted values.
func nearestRank(values []float64, q float64) float64 {
	s := append([]float64(nil), values...)
	sort.Float64s(s)
	if len(s) == 0 {
		return 0
	}
	rank := int(math.Ceil(q*float64(len(s)))) - 1
	if rank < 0 {
		rank = 0
	}
	return s[rank]
}

// mergeGapProvenance folds the many pages of one source into one provenance row
// per source and window, so a 20 000 row read does not print 200 rows. Counts are
// summed, the scan class is the widest seen, and a cut read is marked partial.
func mergeGapProvenance(pages []Provenance, cut bool) []Provenance {
	var out []Provenance
	at := map[string]int{}
	for _, p := range pages {
		if p.DocsExamined != nil {
			v := *p.DocsExamined
			p.DocsExamined = &v
		}
		key := p.SourceID + "|" + p.Window
		i, seen := at[key]
		if !seen {
			at[key] = len(out)
			out = append(out, p)
			continue
		}
		m := &out[i]
		m.DocsReturned += p.DocsReturned
		m.SampleSize += p.SampleSize
		m.ElapsedMs += p.ElapsedMs
		if m.DocsExamined != nil && p.DocsExamined != nil {
			*m.DocsExamined += *p.DocsExamined
		}
		if p.ScanClass != ScanNarrow {
			m.ScanClass = p.ScanClass
		}
		if p.Completeness == Partial {
			m.Completeness, m.PartialReason = Partial, p.PartialReason
		}
	}
	if cut {
		for i := range out {
			out[i].Completeness, out[i].PartialReason = Partial, "rowcap"
		}
	}
	return out
}
