package audit

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

// Two opt-in rules (PatternInput.AuditSeries) that read the same rows:
//
//   - B6s, the audit-create series: per day in the display zone, the median and
//     p95 audit-create delay and the share of request time spent in it (the sum
//     of audit-create time over the sum of total time).
//   - B6f, the fixed-duration stall: audit-create delays of at least
//     stallBandMinMs that cluster inside a band of stallBandWidthMs, on at least
//     stallBandMinDays days.
//
// Both read every request in the window, as B6g does, and follow it: only
// synchronous rows are used, Hangfire rows are left out and counted (their
// audit-create time does not include the insert), at most gapRowCap rows are read
// per source (a cut read is partial and says so), and on a live source the
// window is at most 24 hours and the read must be a narrow scan. A refusal is a
// warning here, not an error, so the other rules in the report stay.
const (
	// stallBandMinMs is the shortest delay looked at for a fixed duration.
	stallBandMinMs = 20000
	// stallBandWidthMs is the widest band a fixed-duration cluster may fill.
	stallBandWidthMs = 5000
	// stallBandMinCount and stallBandMinDays are the fewest delays and distinct
	// days in a band.
	stallBandMinCount = 5
	stallBandMinDays  = 2
)

// AuditSeriesDay is one local day. N counts the synchronous rows with an
// audit-create time. P50Ms and P95Ms are null when N is under MinSampleCount.
// InsertShare is the sum of audit-create time over the sum of total time for the
// rows that have both; it is null when those rows number under MinSampleCount.
// PartDay is true when the window cuts the day short.
type AuditSeriesDay struct {
	Date        string   `json:"date"`
	N           int      `json:"n"`
	P50Ms       *float64 `json:"p50Ms"`
	P95Ms       *float64 `json:"p95Ms"`
	InsertShare *float64 `json:"insertShare"`
	PartDay     bool     `json:"partDay,omitempty"`
}

// StallBand is the fixed-duration cluster. FromMs and ToMs are the smallest and
// largest delay in it. Count is the delays in the band, Days the distinct local
// days they fall on, and OfLong all the delays of at least stallBandMinMs.
type StallBand struct {
	FromMs float64  `json:"fromMs"`
	ToMs   float64  `json:"toMs"`
	Count  int      `json:"count"`
	Days   []string `json:"days"`
	OfLong int      `json:"ofLong"`
}

// AuditSeries is B6s and B6f. Band is null when no cluster passes. PartialReason
// says why Partial is true.
type AuditSeries struct {
	Zone             string           `json:"zone"`
	Days             []AuditSeriesDay `json:"days"`
	Band             *StallBand       `json:"band"`
	ExcludedHangfire int              `json:"excludedHangfire"`
	WithoutAudit     int              `json:"withoutAudit"`
	Partial          bool             `json:"partial"`
	PartialReason    string           `json:"partialReason,omitempty"`
}

// auditPoint is one synchronous row with an audit-create time. total is the
// request's total time, or negative when the row has none.
type auditPoint struct {
	at    time.Time
	audit float64
	total float64
}

// buildAuditSeries reads the window once per source and folds the rows.
func buildAuditSeries(ctx context.Context, repo Repository, in PatternInput, p PatternParams, r PatternReport) (*AuditSeries, []Finding, []Provenance, []string, error) {
	loc, err := LoadZone(in.TimeZone)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	live := isLiveRead(r.Provenance)
	refused := func(why string) (*AuditSeries, []Finding, []Provenance, []string, error) {
		return nil, nil, nil, []string{"The audit-create series and the fixed-duration check did not run: " + why}, nil
	}
	if live && in.End.Sub(in.Start) > liveReadMaxWindow {
		return refused(fmt.Sprintf("they read every request in the window, so on a live source the window can be at most %d hours.", int(liveReadMaxWindow/time.Hour)))
	}

	var (
		points           []auditPoint
		prov             []Provenance
		warnings         []string
		excludedHangfire int
		withoutAudit     int
		failedSources    int
		cutSources       int
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
		if live && !allNarrow(merged) {
			return refused("the read of every request in the window would not be a narrow scan.")
		}
		prov = append(prov, merged...)
		if cut {
			cutSources++
		}
		for _, row := range rows {
			switch {
			case row.Kind == "hangfire":
				excludedHangfire++
			case row.Kind == "synchronous" && row.InitiatedAt != nil:
				if !usableMs(row.AuditCreateTimeMs) {
					withoutAudit++
					continue
				}
				total := -1.0
				if usableMs(row.DurationMs) && *row.DurationMs > 0 {
					total = *row.DurationMs
				}
				points = append(points, auditPoint{at: *row.InitiatedAt, audit: *row.AuditCreateTimeMs, total: total})
			}
		}
	}

	series := &AuditSeries{Zone: loc.String(), Days: []AuditSeriesDay{}, ExcludedHangfire: excludedHangfire, WithoutAudit: withoutAudit}
	switch {
	case failedSources > 0:
		series.Partial, series.PartialReason = true, "a read failed"
		warnings = append(warnings, fmt.Sprintf("The audit-create series could not read %d of %d %s (the source returned an error). It is partial.",
			failedSources, len(in.SourceIDs), plural(len(in.SourceIDs), "source", "sources")))
	case cutSources > 0:
		series.Partial, series.PartialReason = true, fmt.Sprintf("row cap reached (%d rows per source)", gapRowCap)
		warnings = append(warnings, fmt.Sprintf("The audit-create series read only the newest %d rows per source. It is partial and its counts are lower bounds.", gapRowCap))
	}
	if excludedHangfire > 0 {
		warnings = append(warnings, fmt.Sprintf("%d Hangfire %s left out of the audit-create series: their audit-create time does not include the insert.",
			excludedHangfire, plural(excludedHangfire, "row was", "rows were")))
	}
	if withoutAudit > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s with no audit-create time left out of the audit-create series.", withoutAudit, plural(withoutAudit, "row", "rows")))
	}

	series.Days = auditSeriesDays(points, loc, in.Start, in.End)
	band := stallBandOf(points, loc)
	series.Band = band
	return series, stallBandFindings(in, p, series, points), prov, warnings, nil
}

// auditSeriesDays folds the points into local days, oldest first. A day with no
// point is not listed.
func auditSeriesDays(points []auditPoint, loc *time.Location, start, end time.Time) []AuditSeriesDay {
	type acc struct {
		audit      []float64
		sumAudit   float64
		sumTotal   float64
		sharedRows int
		first      time.Time
	}
	byDay := map[string]*acc{}
	for _, pt := range points {
		local := pt.at.In(loc)
		date := local.Format("2006-01-02")
		a := byDay[date]
		if a == nil {
			a = &acc{first: local}
			byDay[date] = a
		}
		a.audit = append(a.audit, pt.audit)
		if pt.total > 0 {
			a.sumAudit += pt.audit
			a.sumTotal += pt.total
			a.sharedRows++
		}
	}
	dates := make([]string, 0, len(byDay))
	for d := range byDay {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	out := make([]AuditSeriesDay, 0, len(dates))
	for _, date := range dates {
		a := byDay[date]
		day := AuditSeriesDay{Date: date, N: len(a.audit)}
		y, m, d := a.first.Date()
		dayStart := time.Date(y, m, d, 0, 0, 0, 0, loc)
		dayEnd := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
		day.PartDay = dayStart.Before(start) || dayEnd.After(end)
		if day.N >= MinSampleCount {
			p50, p95 := medianOf(a.audit), nearestRank(a.audit, 0.95)
			day.P50Ms, day.P95Ms = &p50, &p95
		}
		if a.sharedRows >= MinSampleCount && a.sumTotal > 0 {
			share := math.Round(a.sumAudit/a.sumTotal*1000) / 1000
			day.InsertShare = &share
		}
		out = append(out, day)
	}
	return out
}

// stallBandOf finds the best fixed-duration cluster: among the delays of at
// least stallBandMinMs, the set inside a band of stallBandWidthMs that has the
// most delays (then the most days, then the lowest band) with at least
// stallBandMinCount delays on at least stallBandMinDays distinct local days. It
// returns nil when no band passes.
func stallBandOf(points []auditPoint, loc *time.Location) *StallBand {
	var long []auditPoint
	for _, pt := range points {
		if pt.audit >= stallBandMinMs {
			long = append(long, pt)
		}
	}
	sort.SliceStable(long, func(i, j int) bool { return long[i].audit < long[j].audit })
	var best *StallBand
	for i := range long {
		j := i
		for j < len(long) && long[j].audit-long[i].audit <= stallBandWidthMs {
			j++
		}
		count := j - i
		if count < stallBandMinCount {
			continue
		}
		days := map[string]struct{}{}
		for _, pt := range long[i:j] {
			days[pt.at.In(loc).Format("2006-01-02")] = struct{}{}
		}
		if len(days) < stallBandMinDays {
			continue
		}
		if best != nil && (count < best.Count || (count == best.Count && len(days) <= len(best.Days))) {
			continue
		}
		list := make([]string, 0, len(days))
		for d := range days {
			list = append(list, d)
		}
		sort.Strings(list)
		best = &StallBand{FromMs: long[i].audit, ToMs: long[j-1].audit, Count: count, Days: list, OfLong: len(long)}
	}
	return best
}

// stallBandFindings prints the band finding, or a "not observed" line when there
// were enough long delays to look at and none clustered.
func stallBandFindings(in PatternInput, p PatternParams, series *AuditSeries, points []auditPoint) []Finding {
	id := seriesEvidenceID("B6f", in.SourceIDs, in.Start, in.End, series.Zone)
	if b := series.Band; b != nil {
		note := ""
		if series.Partial {
			note = " Only part of the window was read, so the count is a lower bound."
		}
		return []Finding{{
			Rule: "B6f", Label: EvidenceSymptom, N: b.Count, LowN: b.Count < LowNRows, EvidenceID: id,
			Headline: fmt.Sprintf("Observed: %d audit-create delays fell between %s s and %s s, a span of %s s, on %d %s (%s). That is %d of the %d delays of %d s or more. Audit create is measured from the client side and covers setup work and the database write, not just the write.%s",
				b.Count, fmtSeconds(b.FromMs), fmtSeconds(b.ToMs), fmtSeconds(b.ToMs-b.FromMs), len(b.Days), plural(len(b.Days), "day", "days"),
				joinDays(b.Days), b.Count, b.OfLong, stallBandMinMs/1000, note),
		}}
	}
	long := 0
	for _, pt := range points {
		if pt.audit >= stallBandMinMs {
			long++
		}
	}
	if long < stallBandMinCount || series.Partial {
		return nil
	}
	return []Finding{{
		Rule: "B6f", Label: EvidenceSymptom, N: long, LowN: long < LowNRows, EvidenceID: id,
		Headline: fmt.Sprintf("Not observed: %d audit-create delays of %d s or more did not cluster in a band of %d s on %d or more days.",
			long, stallBandMinMs/1000, stallBandWidthMs/1000, stallBandMinDays),
	}}
}

// joinDays prints at most four dates and counts the rest.
func joinDays(days []string) string {
	const shown = 4
	out := ""
	for i, d := range days {
		if i == shown {
			out += ", and " + strconv.Itoa(len(days)-shown) + " more"
			break
		}
		if i > 0 {
			out += ", "
		}
		out += d
	}
	return out
}
