package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
)

// chunkFake wraps auditFake for the chunked audit read. before runs first in every
// Rollup call (n is the 1-based call number); failAt fails that call with failErr;
// badSourceAt answers that call with a failed source (its counts are kept).
type chunkFake struct {
	*auditFake
	before      func(n int)
	failAt      int
	failErr     error
	badSourceAt int
}

func (f *chunkFake) Rollup(ctx context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	n := len(f.plans) + 1
	if f.before != nil {
		f.before(n)
	}
	if f.failAt == n {
		f.plans = append(f.plans, plan)
		return audit.RollupSlots{}, f.failErr
	}
	slots, err := f.auditFake.Rollup(ctx, plan)
	if err == nil && f.badSourceAt == n {
		slots.Sources = []audit.SourceResult{{SourceID: "a1", OK: false, ErrorCode: "query_timeout", Error: "driver text hunter2"}}
	}
	return slots, err
}

// hourlyRows is one row at the start of every hour of [from, to), plus one 5xx and
// one row 1 ns before the hour, so rows sit on and next to every slot edge.
func hourlyRows(from time.Time, hours int) []auditRow {
	var rows []auditRow
	for i := 0; i < hours; i++ {
		h := from.Truncate(time.Hour).Add(time.Duration(i) * time.Hour)
		rows = append(rows, auditRow{h, ""}, auditRow{h.Add(-time.Nanosecond), ""}, auditRow{h.Add(30 * time.Minute), audit.StatusServerError})
	}
	return rows
}

// wantCells is the expected count and 5xx count per axis cell start, worked out
// from the rows alone (each row in the one-hour slot that holds it, the slot in
// the cell that holds it), for rows inside [start, end).
func wantCells(axis tracingAxis, rows []auditRow) (counts, errs map[int64]int64, total int64) {
	counts, errs = map[int64]int64{}, map[int64]int64{}
	for _, r := range rows {
		if r.at.Before(axis.Start) || !r.at.Before(axis.End) {
			continue
		}
		slot := r.at.Truncate(time.Hour)
		k := (slot.UnixMilli() - axis.AxisStart.UnixMilli()) / axis.WidthMs
		from, _, _, ok := axis.Bucket(int(k))
		if !ok {
			continue
		}
		counts[from.UnixMilli()]++
		if r.class == audit.StatusServerError {
			errs[from.UnixMilli()]++
		}
		total++
	}
	return
}

func checkChunkPlans(t *testing.T, name string, axis tracingAxis, plans []audit.RollupPlan, wantCalls int) {
	t.Helper()
	if wantCalls > 0 && len(plans) != wantCalls {
		t.Fatalf("%s: %d Rollup calls, want %d", name, len(plans), wantCalls)
	}
	if len(plans) == 0 {
		t.Fatalf("%s: no Rollup calls", name)
	}
	if !plans[0].From.Equal(axis.Start) || !plans[len(plans)-1].To.Equal(axis.End) {
		t.Errorf("%s: reads [%v, %v), want [%v, %v)", name, plans[0].From, plans[len(plans)-1].To, axis.Start, axis.End)
	}
	for i, p := range plans {
		if d := p.To.Sub(p.From); d <= 0 || d > audit.LiveRollupMaxWindow {
			t.Errorf("%s: call %d spans %v", name, i, d)
		}
		if p.Width != time.Hour {
			t.Errorf("%s: call %d width %v", name, i, p.Width)
		}
		if i > 0 && !p.From.Equal(plans[i-1].To) {
			t.Errorf("%s: call %d starts %v, previous ended %v", name, i, p.From, plans[i-1].To)
		}
		if i > 0 && p.From.UnixMilli()%p.Width.Milliseconds() != 0 {
			t.Errorf("%s: cut %v is not on the slot grid", name, p.From)
		}
	}
}

func checkChunkTotals(t *testing.T, name string, axis tracingAxis, rows []auditRow, got auditLaneRead) {
	t.Helper()
	wantCount, wantErrs, total := wantCells(axis, rows)
	if !got.Complete || got.ReasonCode != "" || got.Reason != "" {
		t.Fatalf("%s: read = complete %v code %q reason %q", name, got.Complete, got.ReasonCode, got.Reason)
	}
	var sum int64
	for _, b := range got.Buckets {
		if b.Count != wantCount[b.Start.UnixMilli()] || b.Errors != wantErrs[b.Start.UnixMilli()] {
			t.Errorf("%s: bucket %v count %d errors %d, want %d and %d", name, b.Start, b.Count, b.Errors, wantCount[b.Start.UnixMilli()], wantErrs[b.Start.UnixMilli()])
		}
		sum += b.Count
	}
	if sum != total || got.Scanned != total || len(got.Buckets) != len(wantCount) {
		t.Errorf("%s: sum %d scanned %d buckets %d, want %d rows in %d cells", name, sum, got.Scanned, len(got.Buckets), total, len(wantCount))
	}
	if got.ChunksRead != got.ChunksTotal || !got.ReadThrough.Equal(axis.End) {
		t.Errorf("%s: read %d of %d through %v", name, got.ChunksRead, got.ChunksTotal, got.ReadThrough)
	}
}

func TestAuditLaneChunksElevenDays(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	axis := newTracingAxis(start, start.Add(11*24*time.Hour))
	rows := hourlyRows(start, 11*24)
	f := &chunkFake{auditFake: &auditFake{rows: rows}}
	got := readAuditLane(context.Background(), f, []string{"a1", "a2"}, axis, auditClock(auditT0))
	checkChunkPlans(t, "11 d", axis, f.plans, 11)
	checkChunkTotals(t, "11 d", axis, rows, got)
	if got.ChunksTotal != 11 || len(got.Sources) != 2 || !got.Sources[0].OK || !got.Sources[1].OK {
		t.Errorf("total %d sources %+v", got.ChunksTotal, got.Sources)
	}
	for _, p := range f.plans {
		if strings.Join(p.SourceIDs, ",") != "a1,a2" {
			t.Errorf("ids %v", p.SourceIDs)
		}
	}

	// The same window starting inside a slot: the first chunk is cut back to the
	// slot grid, every other cut is on it, and no row is counted twice.
	s2 := time.Date(2026, 3, 1, 10, 7, 30, 0, time.UTC)
	axis = newTracingAxis(s2, s2.Add(11*24*time.Hour))
	rows = hourlyRows(s2, 11*24+2)
	f = &chunkFake{auditFake: &auditFake{rows: rows}}
	got = readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	checkChunkPlans(t, "11 d unaligned", axis, f.plans, 12)
	checkChunkTotals(t, "11 d unaligned", axis, rows, got)
}

func TestAuditLaneChunks186Days(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	axis := newTracingAxis(start, start.Add(audit.MaxTraceWindow))
	rows := hourlyRows(start, 186*24)
	f := &chunkFake{auditFake: &auditFake{rows: rows}}
	got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	checkChunkPlans(t, "186 d", axis, f.plans, 186)
	checkChunkTotals(t, "186 d", axis, rows, got)
	if len(got.Buckets) == 0 || got.ChunksTotal != 186 {
		t.Errorf("buckets %d chunks %d", len(got.Buckets), got.ChunksTotal)
	}
}

func TestAuditLaneChunksSingleCallUpTo24h(t *testing.T) {
	for _, d := range []time.Duration{time.Minute, 23 * time.Hour, 24 * time.Hour} {
		axis := newTracingAxis(auditT0, auditT0.Add(d))
		f := &chunkFake{auditFake: &auditFake{}}
		got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
		if len(f.plans) != 1 || !got.Complete || got.ChunksTotal != 1 || got.ChunksRead != 1 {
			t.Errorf("%v: %d calls, complete %v, chunks %d of %d", d, len(f.plans), got.Complete, got.ChunksRead, got.ChunksTotal)
		}
		lane := auditLaneToWire(got, nil)
		raw, _ := json.Marshal(lane)
		if strings.Contains(string(raw), "chunk") || strings.Contains(string(raw), "readThrough") {
			t.Errorf("%v: single-call lane adds chunk fields: %s", d, raw)
		}
	}
	axis := newTracingAxis(auditT0, auditT0.Add(24*time.Hour+time.Minute))
	f := &chunkFake{auditFake: &auditFake{}}
	readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	if len(f.plans) != 2 {
		t.Errorf("24 h 1 min: %d calls, want 2", len(f.plans))
	}
}

// softClock steps a fake clock that readAuditLane uses for its soft deadline.
func softClock(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	cur := at
	old := auditSoftNow
	auditSoftNow = func() time.Time { return cur }
	t.Cleanup(func() { auditSoftNow = old })
	return &cur
}

func TestAuditLaneSoftDeadlineReturnsPartial(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	axis := newTracingAxis(start, start.Add(11*24*time.Hour))
	rows := hourlyRows(start, 11*24)
	base := time.Now()
	clk := softClock(t, base)
	ctx, cancel := context.WithDeadline(context.Background(), base.Add(100*time.Second))
	defer cancel()
	// Each chunk takes 30 s: the soft stop is at 80 s, so 3 chunks start (at 0, 30
	// and 60 s) and the 4th does not (90 s).
	f := &chunkFake{auditFake: &auditFake{rows: rows}, before: func(int) { *clk = clk.Add(30 * time.Second) }}
	got := readAuditLane(ctx, f, []string{"a1"}, axis, auditClock(auditT0))
	if len(f.plans) != 3 || got.ChunksRead != 3 || got.ChunksTotal != 11 {
		t.Fatalf("%d calls, read %d of %d", len(f.plans), got.ChunksRead, got.ChunksTotal)
	}
	if got.Complete || got.ReasonCode != "timeout" || got.Reason != tracingAuditPartialTimeoutMsg {
		t.Errorf("complete %v code %q reason %q", got.Complete, got.ReasonCode, got.Reason)
	}
	through := start.Add(3 * 24 * time.Hour)
	if !got.ReadThrough.Equal(through) {
		t.Errorf("read through %v, want %v", got.ReadThrough, through)
	}
	wantCount, _, _ := wantCells(axis, rows)
	var read, unread int
	for _, b := range got.Buckets {
		if b.Start.Before(through) && !b.End.After(through) {
			read++
			if b.Count != wantCount[b.Start.UnixMilli()] || b.Count == 0 {
				t.Errorf("read bucket %v count %d, want %d", b.Start, b.Count, wantCount[b.Start.UnixMilli()])
			}
		} else {
			unread++
			if b.Count != 0 {
				t.Errorf("unread bucket %v holds %d", b.Start, b.Count)
			}
		}
	}
	if read == 0 || unread == 0 {
		t.Fatalf("read cells %d unread cells %d", read, unread)
	}
	// Every cell of the window is listed once.
	cells := 0
	for k := 0; k < axis.Buckets; k++ {
		if _, _, _, ok := axis.Bucket(k); ok {
			cells++
		}
	}
	if len(got.Buckets) != cells {
		t.Errorf("%d buckets, %d cells", len(got.Buckets), cells)
	}

	lane := auditLaneToWire(got, nil)
	if lane.Complete || lane.State != calltrace.StateIncomplete || lane.ChunksRead != 3 || lane.ChunksTotal != 11 || lane.ReadThrough == nil || !lane.ReadThrough.Equal(through) {
		t.Errorf("lane %+v", lane)
	}
	for i, b := range lane.Buckets {
		wantDone := !got.Buckets[i].End.After(through)
		if b.Complete != wantDone {
			t.Errorf("wire bucket %v complete %v, want %v", b.Start, b.Complete, wantDone)
		}
	}
	raw, _ := json.Marshal(lane)
	for _, want := range []string{`"chunksTotal":11`, `"chunksRead":3`, `"readThrough":"2026-03-04T00:00:00Z"`, `"reasonCode":"timeout"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("lane lacks %s: %s", want, raw)
		}
	}

	// The deadline passing inside a chunk ends the read the same way.
	f = &chunkFake{auditFake: &auditFake{rows: rows}, failAt: 3, failErr: context.DeadlineExceeded}
	got = readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	if got.ChunksRead != 2 || got.Complete || got.ReasonCode != "timeout" || got.Reason != tracingAuditPartialTimeoutMsg {
		t.Errorf("deadline in chunk 3: read %d code %q reason %q", got.ChunksRead, got.ReasonCode, got.Reason)
	}

	// At least the first chunk is always read, even when the budget is spent.
	*clk = base.Add(time.Hour)
	f = &chunkFake{auditFake: &auditFake{rows: rows}}
	got = readAuditLane(ctx, f, []string{"a1"}, axis, auditClock(auditT0))
	if len(f.plans) != 1 || got.ChunksRead != 1 || got.Complete || got.ReasonCode != "timeout" {
		t.Errorf("spent budget: %d calls, read %d, code %q", len(f.plans), got.ChunksRead, got.ReasonCode)
	}
}

func TestAuditLaneChunkErrorKeepsEarlierChunks(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	axis := newTracingAxis(start, start.Add(11*24*time.Hour))
	rows := hourlyRows(start, 11*24)
	const secret = "mongodb://u:hunter2@host"
	wantCount, _, _ := wantCells(axis, rows)
	for _, c := range []struct {
		name string
		err  error
		code string
		text string
	}{
		{"random", errors.New(secret), "error", tracingAuditPartialErrorMsg},
		{"row limit", audit.ErrRollupGroupLimit, "rowcap", tracingAuditPartialRowCapMsg},
		{"docs examined", audit.ErrDocsExaminedBudget, "timeout", tracingAuditPartialTimeoutMsg},
	} {
		f := &chunkFake{auditFake: &auditFake{rows: rows}, failAt: 3, failErr: c.err}
		got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
		if len(f.plans) != 3 {
			t.Errorf("%s: %d calls, want 3 (no chunk after the failed one)", c.name, len(f.plans))
		}
		if got.Complete || got.ReasonCode != c.code || got.Reason != c.text || got.ChunksRead != 2 || got.ChunksTotal != 11 {
			t.Errorf("%s: complete %v code %q reason %q read %d of %d", c.name, got.Complete, got.ReasonCode, got.Reason, got.ChunksRead, got.ChunksTotal)
		}
		if strings.Contains(got.Reason, "hunter2") {
			t.Errorf("%s: reason holds error text", c.name)
		}
		through := start.Add(2 * 24 * time.Hour)
		if !got.ReadThrough.Equal(through) {
			t.Errorf("%s: read through %v", c.name, got.ReadThrough)
		}
		var sum int64
		for _, b := range got.Buckets {
			if b.End.After(through) {
				if b.Count != 0 {
					t.Errorf("%s: unread bucket %v holds %d", c.name, b.Start, b.Count)
				}
				continue
			}
			if b.Count != wantCount[b.Start.UnixMilli()] {
				t.Errorf("%s: bucket %v count %d, want %d", c.name, b.Start, b.Count, wantCount[b.Start.UnixMilli()])
			}
			sum += b.Count
		}
		if sum == 0 || got.Scanned != sum {
			t.Errorf("%s: kept %d scanned %d", c.name, sum, got.Scanned)
		}
	}

	// A first chunk that fails is the empty, incomplete answer of today.
	f := &chunkFake{auditFake: &auditFake{rows: rows}, failAt: 1, failErr: errors.New(secret)}
	got := readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	if got.Complete || len(got.Buckets) != 0 || got.ReasonCode != "error" || got.Reason != tracingAuditErrorMsg || len(f.plans) != 1 || got.ChunksTotal != 11 || got.ChunksRead != 0 {
		t.Errorf("chunk 1 failure: %+v, %d calls", got, len(f.plans))
	}

	// A chunk with a failed source keeps its counts, stops the read and is not "read".
	f = &chunkFake{auditFake: &auditFake{rows: rows}, badSourceAt: 3}
	got = readAuditLane(context.Background(), f, []string{"a1"}, axis, auditClock(auditT0))
	if len(f.plans) != 3 || got.Complete || got.ReasonCode != "timeout" || got.ChunksRead != 2 || got.Reason != tracingAuditPartialTimeoutMsg {
		t.Fatalf("failed source: %d calls, complete %v code %q read %d", len(f.plans), got.Complete, got.ReasonCode, got.ChunksRead)
	}
	if len(got.Sources) != 1 || got.Sources[0].OK || got.Sources[0].ErrorCode != "query_timeout" || got.Sources[0].Error != "" {
		t.Errorf("sources %+v", got.Sources)
	}
	_, _, all3 := wantCells(newTracingAxis(start, start.Add(3*24*time.Hour)), rows)
	if got.Scanned != all3 {
		t.Errorf("scanned %d, want %d (the failed chunk's counts are kept)", got.Scanned, all3)
	}
}

func TestAuditLaneChunksCancelStopsPromptly(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	axis := newTracingAxis(start, start.Add(60*24*time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &chunkFake{auditFake: &auditFake{rows: hourlyRows(start, 60*24)}, before: func(n int) {
		if n == 2 {
			cancel() // the client goes away during the second chunk
		}
	}}
	got := readAuditLane(ctx, f, []string{"a1"}, axis, auditClock(auditT0))
	if len(f.plans) != 2 {
		t.Errorf("%d calls after the cancel, want 2", len(f.plans))
	}
	if got.Complete || got.ReasonCode != "error" || got.Reason != "" {
		t.Errorf("cancelled read = complete %v code %q reason %q", got.Complete, got.ReasonCode, got.Reason)
	}
}

func TestTracingRouteReadsWindowOver24hInChunks(t *testing.T) {
	rows := []auditRow{
		{time.Date(2001, 2, 3, 4, 30, 0, 0, time.UTC), ""},
		{time.Date(2001, 2, 4, 4, 0, 0, 0, time.UTC), audit.StatusServerError}, // on a cut
		{time.Date(2001, 2, 4, 3, 59, 59, 0, time.UTC), ""},
		{time.Date(2001, 2, 5, 12, 0, 0, 0, time.UTC), ""},
	}
	f := &auditFake{sources: auditSources("a1"), rows: rows}
	e, id := auditHandlerEnv(t, f)
	body := `{"incident":{"start":"2001-02-03T04:00:00Z","end":"2001-02-06T04:00:00Z"}}`
	out, raw := tracingPost(t, e, id, body)
	if len(f.plans) != 3 {
		t.Fatalf("%d Rollup calls, want 3", len(f.plans))
	}
	for i, p := range f.plans {
		if p.To.Sub(p.From) > audit.LiveRollupMaxWindow || (i > 0 && !p.From.Equal(f.plans[i-1].To)) {
			t.Errorf("call %d: %+v", i, p)
		}
	}
	lane := tracingLaneOf(t, out, "audit")
	if !lane.Complete || lane.ReasonCode != "" || lane.Scanned != 4 || tracingBucketSum(lane) != 4 || lane.ChunksTotal != 3 || lane.ChunksRead != 3 || lane.ReadThrough != nil {
		t.Errorf("audit lane complete %v code %q scanned %d sum %d chunks %d of %d through %v", lane.Complete, lane.ReasonCode, lane.Scanned, tracingBucketSum(lane), lane.ChunksRead, lane.ChunksTotal, lane.ReadThrough)
	}
	for _, want := range []string{`"chunksTotal":3`, `"chunksRead":3`} {
		if !strings.Contains(raw, want) {
			t.Errorf("body lacks %s", want)
		}
	}
	if strings.Contains(raw, `"readThrough"`) {
		t.Errorf("a complete lane carries readThrough: %s", raw)
	}

	// Chunk 2 fails: 200, the lane keeps chunk 1 and says how far it read.
	f2 := &chunkFake{auditFake: &auditFake{sources: auditSources("a1"), rows: rows}, failAt: 2, failErr: errors.New("mongodb://u:hunter2@host")}
	e, id = auditHandlerEnv(t, f2)
	out, raw = tracingPost(t, e, id, body)
	lane = tracingLaneOf(t, out, "audit")
	if lane.Complete || lane.ReasonCode != "error" || lane.ChunksRead != 1 || lane.ChunksTotal != 3 || lane.ReadThrough == nil || strings.Contains(raw, "hunter2") {
		t.Errorf("failed chunk 2: lane %+v", lane)
	}
}

// dropSourceFake answers the first chunk with every planned source OK and every
// later chunk with a1 only: a2 was read in chunk 1 but not after it.
type dropSourceFake struct {
	plans []audit.RollupPlan
}

func (f *dropSourceFake) Rollup(_ context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	f.plans = append(f.plans, plan)
	sources := []audit.SourceResult{{SourceID: "a1", OK: true}, {SourceID: "a2", OK: true}}
	if len(f.plans) > 1 {
		sources = sources[:1]
	}
	return audit.RollupSlots{Sources: sources}, nil
}

func TestAuditLaneSourcesReadDropsSourceMissingFromLaterChunk(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	axis := newTracingAxis(start, start.Add(3*24*time.Hour))
	f := &dropSourceFake{}
	read := readAuditLane(context.Background(), f, []string{"a1", "a2"}, axis, auditClock(auditT0))
	if len(f.plans) != 2 {
		t.Fatalf("%d Rollup calls, want 2 (the chunk missing a2 stops the read)", len(f.plans))
	}
	if read.Complete {
		t.Fatalf("read = %+v, want incomplete", read)
	}
	if len(read.Sources) != 2 {
		t.Fatalf("sources %+v, want a1 and a2", read.Sources)
	}
	if !read.Sources[0].OK || read.Sources[0].SourceID != "a1" {
		t.Errorf("a1 = %+v, want OK", read.Sources[0])
	}
	if read.Sources[1].OK || read.Sources[1].SourceID != "a2" || read.Sources[1].ErrorCode != auditCodeError {
		t.Errorf("a2 = %+v, want not OK with code error", read.Sources[1])
	}
	refs := []tracingRef{{SourceID: "a1", Label: "A1"}, {SourceID: "a2", Label: "A2"}}
	lane := auditLaneToWire(read, refs)
	if len(lane.SourcesRead) != 1 || lane.SourcesRead[0] != (tracingRef{SourceID: "a1", Label: "A1"}) {
		t.Errorf("sourcesRead %+v, want only a1", lane.SourcesRead)
	}
}
