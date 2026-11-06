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
		sumMs  float64 // average x executions
		maxMs  float64
		filled bool
	}
	cells := map[int]*acc{}
	var total int64
	asOf := sqlRuns[0].Run.At
	label := sqlRuns[0].Report.Label
	for _, lr := range sqlRuns {
		if lr.source() != source {
			continue
		}
		for _, p := range lr.Report.Lane.Series {
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
	for i := range lane.Buckets {
		a := cells[byStart[lane.Buckets[i].Start]]
		if a == nil || a.execs == 0 {
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
