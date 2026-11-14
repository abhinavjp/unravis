package server

import (
	"context"
	"sort"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
)

// The SQL lane of the Timeline is drawn from imported SQL files when there are
// any. When none were imported, it is drawn from the Query Store series saved
// with the case's lane days (a live SQL read, kept as a stored copy), so the
// Timeline itself never opens a connection.

const (
	tracingReasonHeldSQL = "held-sql"
	tracingHeldSQLMsg    = "Drawn from the Query Store figures saved with this case on the date shown. Each bar is the statements run in that interval; the line is their average time."
)

const (
	tracingReasonLayeredSQL = "layered-sql"
	tracingLayeredSQLMsg    = "Bars, the line and the diamonds are the Query Store figures saved with this case. The slow statements from the imported SQL files are kept as the lane's events. The two are never added together."
)

// layerSQLLane puts the imported SQL files' slow-statement events on top of the
// saved Query Store day: the Query Store figures stay the bars and the line, the
// imported events stay the markers, and the sources of both are named. Counts of
// the two are never added together. It applies only to an imported lane that has
// events; any other state keeps the imported lane as it is.
func layerSQLLane(imported, held tracingLane) (tracingLane, bool) {
	if imported.State != calltrace.StateEvents || len(imported.Events) == 0 || imported.Disabled != "" {
		return imported, false
	}
	out := held
	out.Events = imported.Events
	out.Hidden = imported.Hidden
	out.Truncated = imported.Truncated
	out.Complete = held.Complete && imported.Complete
	out.ReasonCode, out.Reason = tracingReasonLayeredSQL, tracingLayeredSQLMsg
	out.Sources = append(append(make([]tracingRef, 0, len(held.Sources)+len(imported.Sources)), imported.Sources...), held.Sources...)
	// The imported files' time range stays on the lane the owner sees.
	if out.ImportedFrom == nil {
		out.ImportedFrom = imported.ImportedFrom
	}
	if out.ImportedTo == nil {
		out.ImportedTo = imported.ImportedTo
	}
	if !out.ImportedPartial {
		out.ImportedPartial = imported.ImportedPartial
	}
	return out, true
}

// heldSQLBlockSeconds is the length, in seconds, of the blocks a saved Query Store day was kept in: the
// interval the day recorded when it has one, else the smallest gap between saved blocks when that is a
// whole Query Store length. Zero means not known (a single block with no recorded interval).
func heldSQLBlockSeconds(intervalMin int, ts []int64) int {
	if intervalMin > 0 {
		return intervalMin * 60
	}
	sorted := append([]int64(nil), ts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	gap := int64(0)
	for i := 1; i < len(sorted); i++ {
		if d := sorted[i] - sorted[i-1]; d > 0 && (gap == 0 || d < gap) {
			gap = d
		}
	}
	switch gap {
	case 60, 300, 600, 900, 1800, 3600, 86400:
		return int(gap)
	}
	return 0
}

// tracingHeldSQLLane builds the SQL lane from the saved lane days of the case
// that cover the window, or reports false when the case holds none. Only the
// newest saved source is used: two servers' counts must not be added together.
func tracingHeldSQLLane(ctx context.Context, c casestore.Case, axis tracingAxis, now time.Time) (tracingLane, bool) {
	hs, ok := healthStoreOf(c)
	if !ok {
		return tracingLane{}, false
	}
	held, err := laneHeld(ctx, hs)
	if err != nil {
		return tracingLane{}, false
	}
	var sqlRuns []laneRun
	for _, lr := range held {
		if lr.Run.Kind == "sql" && lr.Report.Lane != nil && len(lr.Report.Lane.Series) > 0 {
			sqlRuns = append(sqlRuns, lr)
		}
	}
	if len(sqlRuns) == 0 {
		return tracingLane{}, false
	}
	// laneHeld is newest first: keep the runs of the newest run's source.
	source := sqlRuns[0].source()
	type acc struct {
		execs  int64
		sumMs  float64 // average x executions (the busy time, in ms)
		maxMs  float64
		filled bool
	}
	cells := map[int]*acc{}
	// seen holds each T already counted. The runs are newest first, so the newest run's value wins for a
	// shared T (including 0, with no fall back to the older); a duplicate T is skipped before it touches
	// cells, total or allT. Two saved days of one source can share a T because their incident windows differ
	// while their reference times overlap (DATA-03). Keyed by exact T, never by bucket: different T in one
	// bucket are legitimate and still add.
	seen := map[int64]struct{}{}
	var total int64
	asOf := sqlRuns[0].Run.At
	label := sqlRuns[0].Report.Label
	var allT []int64
	intervalMin := 0
	for _, lr := range sqlRuns {
		if lr.source() != source {
			continue
		}
		if qs := lr.Report.Lane.QueryStore; intervalMin == 0 && qs != nil && qs.IntervalMinutes > 0 {
			intervalMin = qs.IntervalMinutes
		}
		for _, p := range lr.Report.Lane.Series {
			if _, dup := seen[p.T]; dup {
				continue
			}
			seen[p.T] = struct{}{}
			allT = append(allT, p.T)
			ms := p.T * 1000
			if ms < axis.Start.UnixMilli() || ms >= axis.End.UnixMilli() {
				continue
			}
			k := int((ms - axis.AxisStart.UnixMilli()) / axis.WidthMs)
			a := cells[k]
			if a == nil {
				a = &acc{}
				cells[k] = a
			}
			a.execs += p.Execs
			a.sumMs += p.AvgMs * float64(p.Execs)
			a.maxMs = max(a.maxMs, p.MaxMs)
			a.filled = true
			total += p.Execs
		}
	}
	if total == 0 {
		return tracingLane{}, false
	}
	perCell := make(map[int]int64, len(cells))
	for k, a := range cells {
		perCell[k] = a.execs
	}
	lane := tracingLane{
		Key: calltrace.LaneSQL, Label: "SQL Server", State: calltrace.StateEvents,
		Origin: tracingOriginCase, AsOf: asOf.UTC(), Complete: true, Consistency: tracingConsStored,
		ReasonCode: tracingReasonHeldSQL, Reason: tracingHeldSQLMsg, Scanned: total,
		Sources: []tracingRef{{SourceID: "sql-held", Label: label + " · Query Store"}},
		Buckets: tracingBuckets(axis, perCell, nil), Events: []tracingEvent{},
	}
	byStart := map[time.Time]int{}
	for k := 0; k < axis.Buckets; k++ {
		if from, _, _, ok := axis.Bucket(k); ok {
			byStart[from] = k
		}
	}
	lane.BlockSeconds = heldSQLBlockSeconds(intervalMin, allT)
	for i := range lane.Buckets {
		a := cells[byStart[lane.Buckets[i].Start]]
		if a == nil {
			continue
		}
		// A saved point was read here: busyMs is 0 when nothing ran, never nil. It is the busy time of the
		// whole saved block, not spread here; the web model spreads it over the block using blockSeconds.
		busy := a.sumMs
		lane.Buckets[i].BusyMs = &busy
		if a.execs == 0 {
			continue
		}
		avg := a.sumMs / float64(a.execs)
		lane.Buckets[i].TimedCount = a.execs
		lane.Buckets[i].AvgMs = &avg
		if a.maxMs > 0 {
			mx := a.maxMs
			lane.Buckets[i].MaxMs = &mx
		}
	}
	sort.Slice(lane.Buckets, func(i, j int) bool { return lane.Buckets[i].Start.Before(lane.Buckets[j].Start) })
	_ = now
	return lane, true
}
