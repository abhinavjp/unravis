package server

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer/sqlserver"
)

// Wave-12 lane fixes (W12-01/W12-02/W12-03): internal SQL gaps are never
// complete zeros, rate integration uses the observed interval and clipped
// bucket support, and the source selection is honoured. Everything runs on the
// in-memory fake world of blackbox_demo.go: no share, service or port.

// gapSamples makes samples every step from start to end (inclusive). sql gives
// the SQLHost group at a time; nil leaves the group out (the host keeps
// sampling). run names the recorder run.
func gapSamples(start, end time.Time, step time.Duration, run string, sql func(time.Time) *blackbox.SQLHostGroup) []blackbox.Sample {
	var out []blackbox.Sample
	seq := uint64(0)
	for t := start; !t.After(end); t = t.Add(step) {
		seq++
		s := blackbox.Sample{V: 1, Run: run, Seq: seq, T: t.UTC(), CPU: f64(25), SQLHost: sql(t)}
		if s.SQLHost == nil {
			s.Missing = []string{"sqlHost"} // the optional group was skipped
		}
		out = append(out, s)
	}
	return out
}

// gapEnv saves one Blackbox "web-b" serving rec, opens a case that uses it, and
// counts how often the Blackbox source is opened (so a test can prove an
// unselected Blackbox was never read).
func gapEnv(t *testing.T, rec *simBB) (*bbEnv, string, *int32) {
	t.Helper()
	var opens int32
	b := newBBEnv(t, func(d *BlackboxDeps) {
		inner := d.Open
		d.Open = func(loc blackbox.Location, key string) blackbox.Source {
			atomic.AddInt32(&opens, 1)
			return inner(loc, key)
		}
	})
	b.world.put(share, rec)
	b.mustAdd(share, "")
	return b, b.caseWith("web-b"), &opens
}

func sqlAt(rate float64) func(time.Time) *blackbox.SQLHostGroup {
	return func(time.Time) *blackbox.SQLHostGroup { return bbSQLGroup(rate) }
}

// ---- W12-01: internal gaps, skips, instance loss, corruption, newer, cap ----

// An internal SQL gap (the optional group skipped while the host keeps
// sampling) leaves the affected buckets incomplete, never complete zeros.
func TestLaneGapInternalSQLSkipIsNeverCompleteZero(t *testing.T) {
	gapStart, gapEnd := invT0.Add(70*time.Minute), invT0.Add(80*time.Minute) // 10:10-10:20
	rec := bbLaneRec("web-b", gapSamples(invT0.Add(-30*time.Minute), invT0.Add(3*time.Hour), 15*time.Second, "run-1",
		func(t time.Time) *blackbox.SQLHostGroup {
			if !t.Before(gapStart) && t.Before(gapEnd) {
				return nil
			}
			return bbSQLGroup(100)
		}))
	b, id, _ := gapEnv(t, rec)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || lane.Complete {
		t.Fatalf("lane %s complete %v, want blackbox incomplete", lane.ReasonCode, lane.Complete)
	}
	if !strings.Contains(lane.Reason, bbLaneCoverageWarning) {
		t.Fatalf("reason %q lacks the coverage warning", lane.Reason)
	}
	for _, bk := range lane.Buckets {
		switch {
		case bk.End.Before(gapStart) || !bk.Start.Before(gapEnd):
			// Wholly outside the gap: fully observed, complete.
			if !bk.Complete || bk.Count != 6000 {
				t.Fatalf("observed bucket %+v, want 6000 complete", bk)
			}
		case !bk.Start.Before(gapStart) && bk.End.Before(gapEnd):
			// Wholly inside the gap: unobserved, never a complete zero.
			if bk.Complete || bk.Count != 0 {
				t.Fatalf("gap bucket %+v: unobserved time drawn as complete zero", bk)
			}
		default:
			// Straddles a gap edge: partial, never complete.
			if bk.Complete {
				t.Fatalf("edge bucket %+v complete although part was unobserved", bk)
			}
		}
	}
	if b := heldBucketAt(t, lane, invT0.Add(time.Hour).Unix()); b.Count != 6000 || !b.Complete {
		t.Fatalf("observed bucket %+v, want 6000 complete", b)
	}
	// 100 req/s over the 2985 observed seconds of the hour: the span ending at
	// 10:20:00 was measured from the 10:09:45 collect (615 s), not from the
	// skipped 10:19:45 tick, so it is unknown, never a counted 15 s (CORR-3).
	if lane.Scanned != 298500 || tracingBucketSum(lane) != 298500 {
		t.Fatalf("scanned %d sum %d, want 298500", lane.Scanned, tracingBucketSum(lane))
	}
}

// Partial instance loss: one of two instances goes silent mid-window. The sum
// after the loss is a lower bound, so those buckets are partial, not complete.
func TestLaneGapPartialInstanceLossIsPartial(t *testing.T) {
	loss := invT0.Add(90 * time.Minute) // 10:30
	rec := bbLaneRec("web-b", gapSamples(invT0.Add(-30*time.Minute), invT0.Add(3*time.Hour), 15*time.Second, "run-1",
		func(t time.Time) *blackbox.SQLHostGroup {
			if !t.Before(loss) {
				return bbSQLGroup(100) // NAMED disappeared
			}
			return bbSQLGroup(100, 50)
		}))
	b, id, _ := gapEnv(t, rec)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || lane.Complete {
		t.Fatalf("lane %s complete %v, want blackbox incomplete", lane.ReasonCode, lane.Complete)
	}
	for _, bk := range lane.Buckets {
		switch {
		case bk.End.Before(loss):
			if !bk.Complete || bk.Count != 9000 {
				t.Fatalf("whole-coverage bucket %+v, want 9000 complete", bk)
			}
		case !bk.Start.Before(loss):
			if bk.Complete {
				t.Fatalf("bucket %+v complete after an instance was lost", bk)
			}
			if bk.Count != 6000 {
				t.Fatalf("bucket %+v, want the 6000 lower bound", bk)
			}
		default:
			// The 10:29 bucket straddles the loss: 45 s whole (6750) + 15 s
			// lower bound (1500), partial.
			if bk.Complete || bk.Count != 8250 {
				t.Fatalf("straddling bucket %+v, want 8250 partial", bk)
			}
		}
	}
	if lane.Scanned != 449250 {
		t.Fatalf("scanned %d, want 449250 (267750 whole + 181500 lower bound)", lane.Scanned)
	}
}

// A malformed interior line and a newer-version line both downgrade the whole
// read: the lane keeps what was read but no bucket is called complete.
func TestLaneGapCorruptAndNewerLinesDowngrade(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
	}{
		{"corrupt interior", `{"v":1,"run":"run-1","seq":999,"t":"2026-10-20T10:`},
		{"newer version", `{"v":9,"run":"run-1","seq":999,"t":"2026-10-20T10:30:00Z","cpu":5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := bbLaneSQLRec()
			fn := blackbox.FileNameFor(invT0.Add(time.Hour).Truncate(time.Hour))
			lines := bytes.Split(rec.files[fn], []byte("\n"))
			mid := len(lines) / 2
			var buf bytes.Buffer
			for i, ln := range lines {
				if i == mid {
					buf.WriteString(tc.line)
					buf.WriteByte('\n')
				}
				if len(ln) > 0 {
					buf.Write(ln)
					buf.WriteByte('\n')
				}
			}
			rec.files[fn] = buf.Bytes()
			b, id, _ := gapEnv(t, rec)
			out, raw := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
			lane := tracingLaneOf(t, out, calltrace.LaneSQL)
			if lane.ReasonCode != tracingReasonBlackbox || lane.Complete {
				t.Fatalf("lane %s complete %v, want blackbox incomplete", lane.ReasonCode, lane.Complete)
			}
			if !strings.Contains(lane.Reason, bbLaneGapWarning) {
				t.Fatalf("reason %q lacks the gap warning", lane.Reason)
			}
			for _, bk := range lane.Buckets {
				if bk.Complete {
					t.Fatalf("bucket %+v complete after a %s line", bk, tc.name)
				}
			}
			if lane.Scanned <= 0 {
				t.Fatal("the samples read before the bad line were lost")
			}
			for _, banned := range []string{"MSSQLSERVER", "NAMED", `"seq":999`, `"t":"2026-10-20T10:`} {
				if strings.Contains(raw, banned) {
					t.Fatalf("answer carries recording text %q", banned)
				}
			}
		})
	}
}

// 193 overlapping hourly entries: the oldest falls to the reader's file cap and
// the lane SAYS the coverage is incomplete instead of drawing complete zeros.
func TestLaneGap193HourlyEntriesReportOmittedCoverage(t *testing.T) {
	base := invT0 // 09:00
	var samples []blackbox.Sample
	seq := uint64(0)
	for k := 0; k < blackbox.MaxReadFiles+1; k++ {
		h := base.Add(time.Duration(k) * time.Hour)
		for _, at := range []time.Time{h, h.Add(30 * time.Minute)} {
			seq++
			samples = append(samples, blackbox.Sample{V: 1, Run: "run-1", Seq: seq, T: at.UTC(), CPU: f64(25), SQLHost: bbSQLGroup(100)})
		}
	}
	rec := bbLaneRec("web-b", samples)
	rec.man.IntervalSeconds = 1800 // the samples are half an hour apart
	b, id, _ := gapEnv(t, rec)
	end := base.Add(time.Duration(blackbox.MaxReadFiles+1) * time.Hour)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(base.Format(time.RFC3339), end.Format(time.RFC3339)))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || lane.Complete {
		t.Fatalf("lane %s complete %v, want blackbox incomplete over 193 files", lane.ReasonCode, lane.Complete)
	}
	if !strings.Contains(lane.Reason, bbLaneGapWarning) {
		t.Fatalf("reason %q lacks the gap warning for the omitted file", lane.Reason)
	}
	for _, bk := range lane.Buckets {
		if bk.Complete {
			t.Fatalf("bucket %+v complete although the oldest hour was not read", bk)
		}
	}
	if lane.Scanned <= 0 {
		t.Fatal("the 192 read hours drew nothing")
	}
}

// ---- W12-02: observed-interval integration ----

// Known-rate totals under every supported cadence: 100 req/s for one hour is
// 360000 whether the recorder ticks every 5, 15, 30 or 60 seconds.
func TestLaneGapKnownRateTotalsUnderEveryCadence(t *testing.T) {
	for _, cadence := range []int{5, 15, 30, 60} {
		t.Run((time.Duration(cadence) * time.Second).String(), func(t *testing.T) {
			step := time.Duration(cadence) * time.Second
			rec := bbLaneRec("web-b", gapSamples(invT0.Add(-30*time.Minute), invT0.Add(3*time.Hour), step, "run-1", sqlAt(100)))
			rec.man.IntervalSeconds = cadence
			b, id, _ := gapEnv(t, rec)
			out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
			lane := tracingLaneOf(t, out, calltrace.LaneSQL)
			if lane.ReasonCode != tracingReasonBlackbox || !lane.Complete || lane.Truncated {
				t.Fatalf("lane %s complete %v truncated %v", lane.ReasonCode, lane.Complete, lane.Truncated)
			}
			if lane.Scanned != 360000 || tracingBucketSum(lane) != 360000 {
				t.Fatalf("scanned %d sum %d, want 360000 at %v cadence", lane.Scanned, tracingBucketSum(lane), step)
			}
			for _, bk := range lane.Buckets {
				if bk.Count != 6000 || !bk.Complete {
					t.Fatalf("bucket %+v, want 6000 complete", bk)
				}
			}
			if !lane.Estimates {
				t.Fatal("integrated counts not labelled estimates")
			}
		})
	}
}

// A cadence change (budget step-back 15 s -> 60 s) integrates over the OBSERVED
// spans: the effective interval judges the spans, the configured base must not
// scale the counts.
func TestLaneGapCadenceChangeUsesObservedSpans(t *testing.T) {
	change := invT0.Add(90 * time.Minute) // 10:30
	var samples []blackbox.Sample
	samples = append(samples, gapSamples(invT0.Add(-30*time.Minute), change, 15*time.Second, "run-1", sqlAt(100))...)
	samples = append(samples, gapSamples(change.Add(time.Minute), invT0.Add(3*time.Hour), time.Minute, "run-1", sqlAt(100))...)
	rec := bbLaneRec("web-b", samples)
	rec.man.IntervalSeconds = 15
	rec.man.EffectiveIntervalSeconds = 60
	b, id, _ := gapEnv(t, rec)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || !lane.Complete {
		t.Fatalf("lane %s complete %v, want complete across the cadence change", lane.ReasonCode, lane.Complete)
	}
	if lane.Scanned != 360000 || tracingBucketSum(lane) != 360000 {
		t.Fatalf("scanned %d sum %d, want 360000 across the change", lane.Scanned, tracingBucketSum(lane))
	}
}

// Jitter around the cadence still tiles the window: the total is the rate over
// the observed hour, not a multiple of the nominal interval.
func TestLaneGapJitterKeepsKnownTotal(t *testing.T) {
	var samples []blackbox.Sample
	seq := uint64(0)
	jit := []int{2, -1, 1, -2, 0}
	i := 0
	for t := invT0.Add(-30 * time.Minute); !t.After(invT0.Add(3 * time.Hour)); i++ {
		seq++
		samples = append(samples, blackbox.Sample{V: 1, Run: "run-1", Seq: seq, T: t.UTC(), CPU: f64(25), SQLHost: bbSQLGroup(100)})
		t = t.Add(15*time.Second + time.Duration(jit[i%len(jit)])*time.Second)
	}
	rec := bbLaneRec("web-b", samples)
	b, id, _ := gapEnv(t, rec)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || !lane.Complete {
		t.Fatalf("lane %s complete %v, want complete under jitter", lane.ReasonCode, lane.Complete)
	}
	if lane.Scanned != 360000 || tracingBucketSum(lane) != 360000 {
		t.Fatalf("scanned %d sum %d, want 360000 under jitter", lane.Scanned, tracingBucketSum(lane))
	}
}

// A restart leaves the span across it unknown: no count is invented for the
// stopped stretch and its buckets stay incomplete.
func TestLaneGapRestartKeepsUnknownSpanUnknown(t *testing.T) {
	stop, start := invT0.Add(80*time.Minute), invT0.Add(85*time.Minute) // 10:20-10:25
	var samples []blackbox.Sample
	samples = append(samples, gapSamples(invT0.Add(-30*time.Minute), stop, 15*time.Second, "run-1", sqlAt(100))...)
	samples = append(samples, gapSamples(start, invT0.Add(3*time.Hour), 15*time.Second, "run-2", sqlAt(100))...)
	rec := bbLaneRec("web-b", samples)
	b, id, _ := gapEnv(t, rec)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || lane.Complete {
		t.Fatalf("lane %s complete %v, want incomplete across the restart", lane.ReasonCode, lane.Complete)
	}
	// 100 req/s over the 3300 observed seconds; the 300 stopped seconds are a
	// gap, never zero activity and never double-counted.
	if lane.Scanned != 330000 || tracingBucketSum(lane) != 330000 {
		t.Fatalf("scanned %d sum %d, want 330000", lane.Scanned, tracingBucketSum(lane))
	}
	for _, bk := range lane.Buckets {
		if !bk.Start.Before(stop) && bk.Start.Before(start) {
			if bk.Complete || bk.Count != 0 {
				t.Fatalf("stopped bucket %+v, want incomplete gap", bk)
			}
		}
	}
	if b := heldBucketAt(t, lane, invT0.Add(time.Hour).Unix()); !b.Complete || b.Count != 6000 {
		t.Fatalf("bucket before the restart %+v, want 6000 complete", b)
	}
}

// Clipped minute edges: a window starting and ending mid-minute apportions each
// rate span by overlap, so the clipped buckets carry only their share.
func TestLaneGapClippedMinuteEdgesApportion(t *testing.T) {
	rec := bbLaneSQLRec()
	b, id, _ := gapEnv(t, rec)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor("2026-10-20T10:00:30Z", "2026-10-20T10:01:30Z"))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || !lane.Complete {
		t.Fatalf("lane %s complete %v", lane.ReasonCode, lane.Complete)
	}
	if len(lane.Buckets) != 2 {
		t.Fatalf("buckets %d, want the two clipped minutes", len(lane.Buckets))
	}
	for _, bk := range lane.Buckets {
		if !bk.Clipped || !bk.Complete || bk.Count != 3000 {
			t.Fatalf("clipped bucket %+v, want 3000 complete (100 req/s x 30 s)", bk)
		}
	}
	if lane.Scanned != 6000 || tracingBucketSum(lane) != 6000 {
		t.Fatalf("scanned %d sum %d, want 6000", lane.Scanned, tracingBucketSum(lane))
	}
}

// ---- W12-03: selection and evidence preservation ----

// blackboxTagID finds the case's blackbox source tag id.
func blackboxTagID(t *testing.T, store casestore.Store, caseID string) int64 {
	t.Helper()
	c, err := store.Open(context.Background(), casestore.CaseID(caseID))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tags, err := c.(casestore.Tagger).Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range tags {
		if tag.Kind == blackboxTagKind {
			return tag.ID
		}
	}
	t.Fatal("no blackbox tag")
	return 0
}

// An explicit selection of ONLY the import never reads the case's Blackbox and
// never replaces the selected Query Store evidence.
func TestLaneGapSelectedImportOnlyNeverReadsBlackbox(t *testing.T) {
	b, id, opens := gapEnv(t, bbLaneSQLRec())
	imp := seedTracingImport(t, b.e.store, id, sqlserver.QSName, "export.csv",
		[]evidence.Event{qsFileTestEvent(invT0.Add(time.Hour), "100", 20, 90)})
	// The add flow probed the Blackbox once; the tracing read must not open it.
	atomic.StoreInt32(opens, 0)
	body := `{"sourceIds":["` + tracingImportID(imp.ID) + `"],"incident":{"start":"` + bbLaneWindowStart + `","end":"` + bbLaneWindowEnd + `"}}`
	out, _ := tracingPost(t, b.e, id, body)
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonQSFile || tracingBucketSum(lane) != 100 {
		t.Fatalf("lane %s sum %d, want the selected qs-file import", lane.ReasonCode, tracingBucketSum(lane))
	}
	if n := atomic.LoadInt32(opens); n != 0 {
		t.Fatalf("the unselected Blackbox was opened %d times", n)
	}
}

// A full Query Store import plus a PARTIAL Blackbox (default call): the Query
// Store duration evidence wins and stays complete.
func TestLaneGapFullQueryStoreSurvivesPartialBlackbox(t *testing.T) {
	covEnd := invT0.Add(90 * time.Minute) // the recording stops at 10:30
	rec := bbLaneRec("web-b", gapSamples(invT0.Add(-30*time.Minute), covEnd, 15*time.Second, "run-1", sqlAt(100)))
	b, id, _ := gapEnv(t, rec)
	seedTracingImport(t, b.e.store, id, sqlserver.QSName, "export.csv",
		[]evidence.Event{qsFileTestEvent(invT0.Add(time.Hour), "100", 20, 90)})
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonQSFile || !lane.Complete || tracingBucketSum(lane) != 100 {
		t.Fatalf("lane %s complete %v sum %d, want the complete qs-file lane", lane.ReasonCode, lane.Complete, tracingBucketSum(lane))
	}
	if lane.Buckets[0].BusyMs == nil || *lane.Buckets[0].BusyMs != 2000 {
		t.Fatal("the Query Store durations were lost to the recorder")
	}
}

// XE markers plus a complete Blackbox: the bars become the batch estimates, the
// imported markers stay separately attributed, and the two are never summed.
func TestLaneGapXEPlusBlackboxKeepsMarkers(t *testing.T) {
	b, id, _ := gapEnv(t, bbLaneSQLRec())
	imp := seedTracingImport(t, b.e.store, id, "sql-xel", "slow.xel",
		tracingKindEvents("slow-op", invT0.Add(70*time.Minute), 3, time.Minute, nil))
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || !lane.Estimates {
		t.Fatalf("lane %s estimates %v, want blackbox estimates", lane.ReasonCode, lane.Estimates)
	}
	if len(lane.Events) != 3 {
		t.Fatalf("events %d, want the 3 imported XE markers kept", len(lane.Events))
	}
	for _, e := range lane.Events {
		if e.SourceID != tracingImportID(imp.ID) {
			t.Fatalf("marker %s lost its import attribution", e.SourceID)
		}
	}
	hasXE, hasBB := false, false
	for _, s := range lane.Sources {
		if s.SourceID == tracingImportID(imp.ID) {
			hasXE = true
		}
		if s.SourceID == tracingReasonBlackbox {
			hasBB = true
		}
	}
	if !hasXE || !hasBB {
		t.Fatalf("sources %+v, want both the import and the blackbox", lane.Sources)
	}
	// The bars are the machine-wide estimate alone: 360000, not 360003.
	if lane.Scanned != 360000 || tracingBucketSum(lane) != 360000 {
		t.Fatalf("scanned %d sum %d, want 360000 (markers never summed in)", lane.Scanned, tracingBucketSum(lane))
	}
	if !strings.Contains(lane.Reason, bbLaneMarkersNote) {
		t.Fatalf("reason %q lacks the markers note", lane.Reason)
	}
}

// XE markers plus a PARTIAL Blackbox: the imported lane is kept untouched — a
// partial machine-wide estimate never erases counted import evidence.
func TestLaneGapXEPlusPartialBlackboxKeepsImport(t *testing.T) {
	covEnd := invT0.Add(90 * time.Minute)
	rec := bbLaneRec("web-b", gapSamples(invT0.Add(-30*time.Minute), covEnd, 15*time.Second, "run-1", sqlAt(100)))
	b, id, _ := gapEnv(t, rec)
	imp := seedTracingImport(t, b.e.store, id, "sql-xel", "slow.xel",
		tracingKindEvents("slow-op", invT0.Add(70*time.Minute), 3, time.Minute, nil))
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode == tracingReasonBlackbox {
		t.Fatal("a partial Blackbox replaced the counted XE import")
	}
	if len(lane.Events) != 3 || lane.Scanned != 3 {
		t.Fatalf("events %d scanned %d, want the import's 3 markers and counts", len(lane.Events), lane.Scanned)
	}
	for _, s := range lane.Sources {
		if s.SourceID == tracingReasonBlackbox {
			t.Fatal("the partial Blackbox was attributed on the kept import lane")
		}
	}
	_ = imp
}

// Selecting the Blackbox tag explicitly still draws its lane.
func TestLaneGapExplicitBlackboxSelectionDraws(t *testing.T) {
	b, id, _ := gapEnv(t, bbLaneSQLRec())
	tagID := blackboxTagID(t, b.e.store, id)
	body := `{"sourceIds":["` + tracingTagID(tagID) + `"],"incident":{"start":"` + bbLaneWindowStart + `","end":"` + bbLaneWindowEnd + `"}}`
	out, _ := tracingPost(t, b.e, id, body)
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || lane.Scanned != 360000 {
		t.Fatalf("lane %s scanned %d, want the selected blackbox lane", lane.ReasonCode, lane.Scanned)
	}
}
