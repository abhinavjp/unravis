package server

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer/sqlserver"
)

// BBX-5-16: the database lane drawn from the case's Blackbox sampling. The fake
// world of blackbox_demo.go serves the recordings, so no share, service or port
// is touched. The window is 2026-10-20 10:00-11:00; the recording starts 08:30.

const bbLaneWindowStart = "2026-10-20T10:00:00Z"
const bbLaneWindowEnd = "2026-10-20T11:00:00Z"

// bbLaneSamples makes one sample every 15 s from start to end (inclusive). sql
// gives the SQLHost group of a sample; nil leaves the group out entirely.
func bbLaneSamples(start, end time.Time, sql func(time.Time) *blackbox.SQLHostGroup) []blackbox.Sample {
	var out []blackbox.Sample
	seq := uint64(0)
	for t := start; !t.After(end); t = t.Add(15 * time.Second) {
		seq++
		out = append(out, blackbox.Sample{V: 1, Run: "run-1", Seq: seq, T: t.UTC(), CPU: f64(25), SQLHost: sql(t)})
	}
	return out
}

// bbSQLGroup is a SQLHost group of one instance per rate (MSSQLSERVER, NAMED, ...).
func bbSQLGroup(rates ...float64) *blackbox.SQLHostGroup {
	g := &blackbox.SQLHostGroup{}
	for i, r := range rates {
		v := r
		name := "MSSQLSERVER"
		if i > 0 {
			name = "NAMED"
		}
		g.Instances = append(g.Instances, blackbox.SQLHostInstance{Instance: name, BatchRequestsPerSec: &v})
	}
	return g
}

// bbLaneRec builds a recording of the given samples as a simBB the fake world
// serves: the manifest lists the hourly files and the first and last sample.
func bbLaneRec(name string, samples []blackbox.Sample) *simBB {
	bb := &simBB{files: map[string][]byte{}, dropAt: map[string]int{}}
	byHour := map[time.Time]*bytes.Buffer{}
	counts := map[time.Time]int{}
	firstOf, lastOf := map[time.Time]time.Time{}, map[time.Time]time.Time{}
	var hours []time.Time
	for _, s := range samples {
		line, _ := json.Marshal(s)
		h := s.T.UTC().Truncate(time.Hour)
		buf := byHour[h]
		if buf == nil {
			buf = &bytes.Buffer{}
			byHour[h] = buf
			hours = append(hours, h)
		}
		buf.Write(line)
		buf.WriteByte('\n')
		counts[h]++
		if firstOf[h].IsZero() {
			firstOf[h] = s.T
		}
		lastOf[h] = s.T
	}
	first, last := samples[0].T, samples[len(samples)-1].T
	m := blackbox.Manifest{
		Format: blackbox.FormatVersion, Name: name, Version: "0.9.0", Host: strings.ToUpper(name),
		IntervalSeconds: 15, Created: first, Updated: last, State: "running",
		Runs:        []blackbox.RunEntry{{ID: "run-1", Start: first, Version: "0.9.0"}},
		FirstSample: &first, LastSample: &last,
	}
	for _, h := range hours {
		fn := blackbox.FileNameFor(h)
		data := byHour[h].Bytes()
		bb.files[fn] = data
		m.Files = append(m.Files, blackbox.FileEntry{Name: fn, Hour: h, Bytes: int64(len(data)), Samples: counts[h], First: firstOf[h], Last: lastOf[h]})
	}
	bb.man = m
	return bb
}

// bbLaneEnv saves one Blackbox "web-b" serving rec and opens a case that uses it.
func bbLaneEnv(t *testing.T, rec *simBB) (*bbEnv, string) {
	t.Helper()
	b := newBBEnv(t, nil)
	b.world.put(share, rec)
	b.mustAdd(share, "")
	return b, b.caseWith("web-b")
}

// bbLaneSQLRec is a recording whose every sample carries 100 batch requests/s.
func bbLaneSQLRec() *simBB {
	return bbLaneRec("web-b", bbLaneSamples(invT0.Add(-30*time.Minute), invT0.Add(3*time.Hour),
		func(time.Time) *blackbox.SQLHostGroup { return bbSQLGroup(100) }))
}

func TestBlackboxSQLLanePresent(t *testing.T) {
	// Two instances: 100 + 50 batch requests/s together. Four 15 s samples a
	// minute give (100+50) x 15 x 4 = 9000 a minute, 540000 over the hour.
	rec := bbLaneRec("web-b", bbLaneSamples(invT0.Add(-30*time.Minute), invT0.Add(3*time.Hour),
		func(time.Time) *blackbox.SQLHostGroup { return bbSQLGroup(100, 50) }))
	b, id := bbLaneEnv(t, rec)
	before := tracingFingerprint(t, b.e.store, id)
	out, raw := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || lane.State != calltrace.StateEvents || !lane.Complete || lane.Truncated {
		t.Fatalf("lane %s %s complete %v truncated %v", lane.ReasonCode, lane.State, lane.Complete, lane.Truncated)
	}
	if lane.Origin != tracingOriginCase || lane.Consistency != tracingConsStored || lane.Disabled != "" {
		t.Fatalf("origin %s consistency %s disabled %q", lane.Origin, lane.Consistency, lane.Disabled)
	}
	if len(lane.Sources) != 1 || lane.Sources[0].SourceID != "blackbox" {
		t.Fatalf("sources %+v, want one source keyed blackbox", lane.Sources)
	}
	if want := "Blackbox sampling every 15 s since 20 Oct 2026"; lane.Sources[0].Label != want {
		t.Fatalf("label %q, want %q", lane.Sources[0].Label, want)
	}
	if lane.Scanned != 540000 || tracingBucketSum(lane) != 540000 {
		t.Fatalf("scanned %d sum %d, want 540000", lane.Scanned, tracingBucketSum(lane))
	}
	if b0 := heldBucketAt(t, lane, invT0.Add(time.Hour).Unix()); b0.Count != 9000 {
		t.Fatalf("first bucket count %d, want 9000", b0.Count)
	}
	if !strings.Contains(raw, `"reasonCode":"blackbox"`) || !strings.Contains(raw, `"sourceId":"blackbox"`) {
		t.Fatal("wire output lacks the blackbox source key")
	}
	if tracingFingerprint(t, b.e.store, id) != before {
		t.Fatal("tracing changed the case")
	}
	// The lane stays behind the app auth: no token, no answer.
	req := httptest.NewRequest(http.MethodPost, tracingPath(id), strings.NewReader(tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd)))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("Content-Type", "application/json")
	if res := b.e.serve(req); res.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", res.Code)
	}
}

func TestBlackboxSQLLaneAbsentFallsThrough(t *testing.T) {
	// No SQLHost group at all: the lane stays not-recorded, never a zero lane.
	plain := bbLaneRec("web-b", bbLaneSamples(invT0.Add(-30*time.Minute), invT0.Add(3*time.Hour),
		func(time.Time) *blackbox.SQLHostGroup { return nil }))
	b, id := bbLaneEnv(t, plain)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode == tracingReasonBlackbox || lane.State != calltrace.StateNoData || lane.Since != nil || tracingBucketSum(lane) != 0 {
		t.Fatalf("absent lane %s %s since %v sum %d, want unchanged no-data", lane.ReasonCode, lane.State, lane.Since, tracingBucketSum(lane))
	}
	// A case that names no Blackbox is unchanged too.
	plainCase := b.e.newCase("no blackbox")
	out, _ = tracingPost(t, b.e, plainCase, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	if lane = tracingLaneOf(t, out, calltrace.LaneSQL); lane.ReasonCode != "" || lane.State != calltrace.StateNoData {
		t.Fatalf("no-tag lane %s %s", lane.ReasonCode, lane.State)
	}

	// SQLHost present but the batch-request reading missing (another counter is
	// recorded): still a fall-through, the missing reading is never a 0.
	v := 5.0
	other := bbLaneRec("web-b", bbLaneSamples(invT0.Add(-30*time.Minute), invT0.Add(3*time.Hour),
		func(time.Time) *blackbox.SQLHostGroup {
			return &blackbox.SQLHostGroup{Instances: []blackbox.SQLHostInstance{{Instance: "MSSQLSERVER", UserConnections: &v}}}
		}))
	b2, id2 := bbLaneEnv(t, other)
	out, _ = tracingPost(t, b2.e, id2, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	if lane = tracingLaneOf(t, out, calltrace.LaneSQL); lane.ReasonCode == tracingReasonBlackbox || lane.State != calltrace.StateNoData || lane.Since != nil {
		t.Fatalf("missing-reading lane %s %s since %v", lane.ReasonCode, lane.State, lane.Since)
	}

	// The existing chain still runs: a Query Store file fills the lane even
	// though the case names a Blackbox without SQL counters.
	imp := seedTracingImport(t, b.e.store, id, sqlserver.QSName, "export.csv",
		[]evidence.Event{qsFileTestEvent(invT0.Add(time.Hour), "100", 20, 90)})
	body := `{"sourceIds":["` + tracingImportID(imp.ID) + `"],"incident":{"start":"` + bbLaneWindowStart + `","end":"` + bbLaneWindowEnd + `"}}`
	out, _ = tracingPost(t, b.e, id, body)
	if lane = tracingLaneOf(t, out, calltrace.LaneSQL); lane.ReasonCode != tracingReasonQSFile || tracingBucketSum(lane) != 100 {
		t.Fatalf("qs-file fall-through %s sum %d", lane.ReasonCode, tracingBucketSum(lane))
	}
}

func TestBlackboxSQLLaneNoText(t *testing.T) {
	b, id := bbLaneEnv(t, bbLaneSQLRec())
	out, raw := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox {
		t.Fatalf("reason %s, want blackbox", lane.ReasonCode)
	}
	// Numeric activity and the fixed label only: no events, no query text, no
	// instance names, no error text, no copy beyond the label.
	if len(lane.Events) != 0 || lane.Hidden != 0 || lane.Reason != "" {
		t.Fatalf("events %d hidden %d reason %q", len(lane.Events), lane.Hidden, lane.Reason)
	}
	for _, s := range lane.Sources {
		if s.Label != "Blackbox sampling every 15 s since 20 Oct 2026" {
			t.Fatalf("source label %q is not the fixed sentence", s.Label)
		}
	}
	for _, banned := range []string{"MSSQLSERVER", "NAMED", "SELECT", "batchRequestsPerSec", "could not be read"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("answer carries text %q", banned)
		}
	}
	for _, bk := range lane.Buckets {
		if bk.AvgMs != nil || bk.P50Ms != nil || bk.P95Ms != nil || bk.MaxMs != nil || bk.BusyMs != nil {
			t.Fatalf("bucket invents figures the sampling never recorded: %+v", bk)
		}
	}
	// A Blackbox that cannot be read adds no error text: the lane falls through.
	b.world.drop(share)
	out, raw = tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	if lane = tracingLaneOf(t, out, calltrace.LaneSQL); lane.ReasonCode == tracingReasonBlackbox || lane.Reason != "" {
		t.Fatalf("unreadable lane %s reason %q", lane.ReasonCode, lane.Reason)
	}
	if strings.Contains(raw, "could not be read") || strings.Contains(raw, "Blackbox web-b") {
		t.Fatal("unreadable Blackbox leaked error text into the tracing answer")
	}
}

func TestBlackboxSQLLaneSince(t *testing.T) {
	first := invT0.Add(-30 * time.Minute) // 08:30, before the window
	b, id := bbLaneEnv(t, bbLaneSQLRec())
	out, raw := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.Since == nil || !lane.Since.Equal(first) {
		t.Fatalf("since %v, want the first sample %v", lane.Since, first)
	}
	if !strings.Contains(raw, `"since":"2026-10-20T08:30:00Z"`) {
		t.Fatal("wire output lacks the since timestamp")
	}
	if !strings.Contains(lane.Sources[0].Label, "since 20 Oct 2026") {
		t.Fatalf("label %q lacks the first sample date", lane.Sources[0].Label)
	}
	// A later window keeps the same since: it is the recording's first sample.
	out, _ = tracingPost(t, b.e, id, tracingWindowBodyFor("2026-10-20T11:00:00Z", "2026-10-20T11:30:00Z"))
	if lane = tracingLaneOf(t, out, calltrace.LaneSQL); lane.Since == nil || !lane.Since.Equal(first) {
		t.Fatalf("later window since %v, want %v", lane.Since, first)
	}
}

// The per-sample rate sums only the instances that recorded the reading; a
// missing or invalid reading is never a stand-in zero.
func TestBlackboxSQLRateMissingNeverZero(t *testing.T) {
	if _, ok := bbSQLRate(blackbox.Sample{}); ok {
		t.Fatal("a sample without SQLHost has a rate")
	}
	nilRate := &blackbox.SQLHostGroup{Instances: []blackbox.SQLHostInstance{{Instance: "MSSQLSERVER"}}}
	if _, ok := bbSQLRate(blackbox.Sample{SQLHost: nilRate}); ok {
		t.Fatal("a missing reading has a rate")
	}
	v, bad, nan := 100.0, -1.0, math.NaN()
	mixed := &blackbox.SQLHostGroup{Instances: []blackbox.SQLHostInstance{
		{Instance: "MSSQLSERVER", BatchRequestsPerSec: &v},
		{Instance: "NAMED"},
		{Instance: "NEG", BatchRequestsPerSec: &bad},
		{Instance: "NAN", BatchRequestsPerSec: &nan},
	}}
	rate, ok := bbSQLRate(blackbox.Sample{SQLHost: mixed})
	if !ok || rate != 100 {
		t.Fatalf("rate %v ok %v, want 100 true", rate, ok)
	}
}

// BBX-5-16 fix (finding 2): a read that lost files is incomplete, never a zero
// lane. The samples read before the drop are kept; the lane carries the host
// lane's gap warning and no instance or error text.
func TestBlackboxSQLLaneIncompleteReport(t *testing.T) {
	// A file that drops part way through (a share that went away).
	rec := bbLaneSQLRec()
	fn := blackbox.FileNameFor(invT0.Add(time.Hour).Truncate(time.Hour)) // the 10:00 hour
	rec.dropAt[fn] = len(rec.files[fn]) / 2
	b, id := bbLaneEnv(t, rec)
	out, raw := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || lane.Complete {
		t.Fatalf("dropped lane %s complete %v, want blackbox incomplete", lane.ReasonCode, lane.Complete)
	}
	if lane.Reason != bbLaneGapWarning {
		t.Fatalf("reason %q, want the gap warning %q", lane.Reason, bbLaneGapWarning)
	}
	if lane.Scanned <= 0 || tracingBucketSum(lane) != lane.Scanned {
		t.Fatalf("scanned %d sum %d, want the samples read before the drop", lane.Scanned, tracingBucketSum(lane))
	}
	for _, bk := range lane.Buckets {
		if bk.Complete {
			t.Fatalf("bucket %+v complete after a dropped file", bk)
		}
		if bk.Count < 0 {
			t.Fatalf("negative count %d", bk.Count)
		}
	}
	for _, banned := range []string{"MSSQLSERVER", "NAMED", "network name", "connection dropped"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("answer carries instance/error text %q", banned)
		}
	}

	// A file the manifest lists but that is gone any more: the hour it held is a
	// gap, never a zero. The window spans two hours so the read hour still draws.
	rec2 := bbLaneSQLRec()
	fn2 := blackbox.FileNameFor(invT0.Add(2 * time.Hour).Truncate(time.Hour)) // the 11:00 hour
	delete(rec2.files, fn2)
	b2, id2 := bbLaneEnv(t, rec2)
	out2, _ := tracingPost(t, b2.e, id2, tracingWindowBodyFor(bbLaneWindowStart, "2026-10-20T12:00:00Z"))
	lane2 := tracingLaneOf(t, out2, calltrace.LaneSQL)
	if lane2.ReasonCode != tracingReasonBlackbox || lane2.Complete {
		t.Fatalf("missing lane %s complete %v, want blackbox incomplete", lane2.ReasonCode, lane2.Complete)
	}
	if lane2.Reason != bbLaneGapWarning {
		t.Fatalf("reason %q, want the gap warning", lane2.Reason)
	}
	if lane2.Scanned != 360000 {
		t.Fatalf("scanned %d, want 360000 from the hour that was read", lane2.Scanned)
	}
	missingHour := invT0.Add(2 * time.Hour)
	for _, bk := range lane2.Buckets {
		if bk.Complete {
			t.Fatalf("bucket %+v complete after a missing file", bk)
		}
		if !bk.Start.Before(missingHour) && bk.Count != 0 {
			t.Fatalf("missing-hour bucket %+v invents activity", bk)
		}
		if bk.Count < 0 {
			t.Fatalf("negative count %d", bk.Count)
		}
	}
}

// BBX-5-16 fix (finding 2): time the recording does not cover is a gap, never
// zero activity. The recording stops at 10:30 but the window runs to 11:00.
func TestBlackboxSQLLanePartialCoverage(t *testing.T) {
	covEnd := invT0.Add(90 * time.Minute) // 10:30, the recording's last sample
	rec := bbLaneRec("web-b", bbLaneSamples(invT0.Add(-30*time.Minute), covEnd,
		func(time.Time) *blackbox.SQLHostGroup { return bbSQLGroup(100) }))
	b, id := bbLaneEnv(t, rec)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || lane.Complete {
		t.Fatalf("partial lane %s complete %v, want blackbox incomplete", lane.ReasonCode, lane.Complete)
	}
	covered, gaps := 0, 0
	for _, bk := range lane.Buckets {
		if !bk.End.After(covEnd) {
			// Fully inside the recording: real activity, called complete.
			if !bk.Complete || bk.Count <= 0 {
				t.Fatalf("covered bucket %+v, want complete with activity", bk)
			}
			covered++
		} else {
			// Beyond the recording: a gap, never a claimed zero.
			if bk.Complete {
				t.Fatalf("uncovered bucket %+v called complete (drawn as zero)", bk)
			}
			gaps++
		}
		if bk.Count < 0 {
			t.Fatalf("negative count %d", bk.Count)
		}
	}
	if covered == 0 || gaps == 0 {
		t.Fatalf("covered %d gaps %d, want both a covered and an uncovered stretch", covered, gaps)
	}
}

// BBX-5-16 fix (finding 3): an absurd per-instance reading is skipped, never
// summed; the cap itself is still a valid reading.
func TestBlackboxSQLRateAbsurdSkipped(t *testing.T) {
	huge, over, edge, good := 1e300, bbSQLMaxRate+1, bbSQLMaxRate, 100.0
	inf := math.Inf(1)
	only := &blackbox.SQLHostGroup{Instances: []blackbox.SQLHostInstance{{Instance: "MSSQLSERVER", BatchRequestsPerSec: &huge}}}
	if _, has := bbSQLRate(blackbox.Sample{SQLHost: only}); has {
		t.Fatal("a 1e300 reading has a rate")
	}
	mixed := &blackbox.SQLHostGroup{Instances: []blackbox.SQLHostInstance{
		{Instance: "MSSQLSERVER", BatchRequestsPerSec: &huge},
		{Instance: "NAMED", BatchRequestsPerSec: &over},
		{Instance: "INF", BatchRequestsPerSec: &inf},
		{Instance: "OK", BatchRequestsPerSec: &good},
	}}
	rate, has := bbSQLRate(blackbox.Sample{SQLHost: mixed})
	if !has || rate != 100 {
		t.Fatalf("rate %v has %v, want 100 true (absurd readings skipped)", rate, has)
	}
	atCap := &blackbox.SQLHostGroup{Instances: []blackbox.SQLHostInstance{{Instance: "MSSQLSERVER", BatchRequestsPerSec: &edge}}}
	if r, has := bbSQLRate(blackbox.Sample{SQLHost: atCap}); !has || r != bbSQLMaxRate {
		t.Fatalf("rate %v has %v, want the cap %v true", r, has, bbSQLMaxRate)
	}
}

// BBX-5-16 fix (finding 3): an untrusted rate times interval can never overflow
// int64 into a negative count. A 1e300 reading is skipped, a huge manifest
// interval is clamped, and a count beyond the sane cap is skipped — all zero-safe.
func TestBlackboxSQLLaneOverflowSafe(t *testing.T) {
	// A 1e300 instance beside a real one: only the real one draws (100 req/s).
	rec := bbLaneRec("web-b", bbLaneSamples(invT0.Add(-30*time.Minute), invT0.Add(3*time.Hour),
		func(time.Time) *blackbox.SQLHostGroup { return bbSQLGroup(1e300, 100) }))
	b, id := bbLaneEnv(t, rec)
	out, _ := tracingPost(t, b.e, id, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane := tracingLaneOf(t, out, calltrace.LaneSQL)
	if lane.ReasonCode != tracingReasonBlackbox || !lane.Complete {
		t.Fatalf("lane %s complete %v, want blackbox complete", lane.ReasonCode, lane.Complete)
	}
	if lane.Scanned != 360000 || tracingBucketSum(lane) != 360000 {
		t.Fatalf("scanned %d sum %d, want 360000", lane.Scanned, tracingBucketSum(lane))
	}
	for _, bk := range lane.Buckets {
		if bk.Count < 0 {
			t.Fatalf("negative count %d from a 1e300 reading", bk.Count)
		}
	}

	// A huge manifest interval is clamped to the fixed 15 s cadence, never
	// amplified into an overflow.
	huge := bbLaneSQLRec()
	huge.man.IntervalSeconds = 1 << 62
	b2, id2 := bbLaneEnv(t, huge)
	out2, _ := tracingPost(t, b2.e, id2, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane2 := tracingLaneOf(t, out2, calltrace.LaneSQL)
	if lane2.ReasonCode != tracingReasonBlackbox || lane2.Scanned != 360000 {
		t.Fatalf("huge-interval lane %s scanned %d, want blackbox 360000", lane2.ReasonCode, lane2.Scanned)
	}
	for _, bk := range lane2.Buckets {
		if bk.Count < 0 {
			t.Fatalf("negative count %d from a huge interval", bk.Count)
		}
	}

	// A reading at the per-instance cap with the longest interval still makes a
	// count beyond the sane cap: it is skipped, never a negative int64.
	capped := bbLaneRec("web-b", bbLaneSamples(invT0.Add(-30*time.Minute), invT0.Add(3*time.Hour),
		func(time.Time) *blackbox.SQLHostGroup { return bbSQLGroup(bbSQLMaxRate) }))
	capped.man.IntervalSeconds = bbSQLMaxInterval
	b3, id3 := bbLaneEnv(t, capped)
	out3, _ := tracingPost(t, b3.e, id3, tracingWindowBodyFor(bbLaneWindowStart, bbLaneWindowEnd))
	lane3 := tracingLaneOf(t, out3, calltrace.LaneSQL)
	if lane3.ReasonCode != tracingReasonBlackbox {
		t.Fatalf("capped lane %s, want blackbox", lane3.ReasonCode)
	}
	if lane3.Scanned != 0 || tracingBucketSum(lane3) != 0 {
		t.Fatalf("capped scanned %d sum %d, want 0 (skipped, zero-safe)", lane3.Scanned, tracingBucketSum(lane3))
	}
	for _, bk := range lane3.Buckets {
		if bk.Count < 0 {
			t.Fatalf("negative count %d from an out-of-range float", bk.Count)
		}
	}
}