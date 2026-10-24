package store

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func queryForTest(now time.Time) audit.Query {
	return audit.Query{
		SourceIDs: []string{"source"}, Start: now.Add(-15 * time.Minute), End: now,
		BaselineStart: now.Add(-30 * time.Minute), BaselineEnd: now.Add(-15 * time.Minute),
		Kind: "synchronous", Outcome: "success", Metric: "total", BucketSeconds: 10,
	}
}

func addFakeSource(t *testing.T, s *Store, collection string) audit.Source {
	t.Helper()
	src, err := s.AddSource(context.Background(), audit.SourceInput{Label: collection, URI: "mongodb://host", Username: "reader", Password: "secret", Database: "db", Collection: collection})
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func TestAnalyzeReturnsSanitizedPartialSourceFailure(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, errors.New("mongodb://reader:secret@host:27017/db: network error")
	}
	q := queryForTest(time.Now().UTC().Truncate(time.Second))
	q.SourceIDs = []string{source.ID}
	analysis, err := s.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Sources) != 1 || analysis.Sources[0].OK {
		t.Fatalf("source failure not represented: %#v", analysis.Sources)
	}
	if strings.Contains(analysis.Sources[0].Error, "secret") || strings.Contains(analysis.Sources[0].Error, "mongodb://") {
		t.Fatal("driver error leaked connection details")
	}
}

func TestAnalyzeCapsAggregateRowsAcrossCalls(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	docs := make([]any, audit.MaxSummaryRows+1)
	for i := range docs {
		docs[i] = bucketDocument{Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", State: "valid", At: time.Now().UTC(), Count: 1}
	}
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: docs}, nil
	}
	q := queryForTest(time.Now().UTC().Truncate(time.Second))
	q.SourceIDs = []string{source.ID}
	_, err := s.Analyze(context.Background(), q)
	if !errors.Is(err, audit.ErrLimit) {
		t.Fatalf("row cap error = %v, want audit.ErrLimit", err)
	}
}

func TestAnalyzeCancellationDoesNotWaitForGlobalAdmission(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	for i := 0; i < cap(s.global); i++ {
		s.global <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := queryForTest(time.Now().UTC().Truncate(time.Second))
	q.SourceIDs = []string{source.ID}
	start := time.Now()
	analysis, err := s.Analyze(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Fatal("canceled query waited for global admission")
	}
	if len(analysis.Sources) != 1 || analysis.Sources[0].OK {
		t.Fatalf("canceled source not marked failed: %#v", analysis.Sources)
	}
	for i := 0; i < cap(s.global); i++ {
		<-s.global
	}
}

func TestAnalyzeFastSourcesFinishWhileSlowSourceIsRunning(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	slow := addFakeSource(t, s, "slow")
	fast := addFakeSource(t, s, "fast")
	slowRelease := make(chan struct{})
	fastDone := make(chan struct{}, 2)
	handle.aggregateFn = func(ctx context.Context, _, collection string, _ mongo.Pipeline) (mongoCursor, error) {
		if collection == "slow" {
			<-slowRelease
			return nil, errors.New("slow source unavailable")
		}
		fastDone <- struct{}{}
		return &fakeCursor{}, nil
	}
	q := queryForTest(time.Now().UTC().Truncate(time.Second))
	q.SourceIDs = []string{slow.ID, fast.ID}
	type response struct {
		analysis audit.Analysis
		err      error
	}
	done := make(chan response, 1)
	go func() {
		analysis, err := s.Analyze(context.Background(), q)
		done <- response{analysis: analysis, err: err}
	}()

	for range 2 {
		select {
		case <-fastDone:
		case <-time.After(time.Second):
			close(slowRelease)
			t.Fatal("fast source did not complete both queries while slow source was blocked")
		}
	}
	select {
	case <-done:
		close(slowRelease)
		t.Fatal("analysis returned before the blocked source finished")
	default:
	}
	close(slowRelease)
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if len(result.analysis.Sources) != 2 || result.analysis.Sources[0].OK || !result.analysis.Sources[1].OK {
			t.Fatalf("unexpected per-source results: %#v", result.analysis.Sources)
		}
	case <-time.After(time.Second):
		t.Fatal("analysis did not finish after releasing slow source")
	}
}
func TestAnalyzePreservesWrappedLegacyLimit(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	first := addFakeSource(t, s, "limited")
	second := addFakeSource(t, s, "sibling")
	handle.aggregateFn = func(ctx context.Context, _, collection string, _ mongo.Pipeline) (mongoCursor, error) {
		if collection == "limited" {
			return nil, errors.Join(audit.ErrLimit, audit.ErrLegacyScanLimit)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	q := queryForTest(time.Now().UTC())
	q.SourceIDs = []string{first.ID, second.ID}
	_, err := s.Analyze(context.Background(), q)
	if !errors.Is(err, audit.ErrLimit) || !errors.Is(err, audit.ErrLegacyScanLimit) {
		t.Fatalf("lost limit classification: %v", err)
	}
}
func TestAnalyzeAdmissionDeadlineIsNotDatabaseTimeout(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	src := addFakeSource(t, s, "audit")
	for range cap(s.global) {
		s.global <- struct{}{}
	}
	defer func() {
		for range cap(s.global) {
			<-s.global
		}
	}()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	q := queryForTest(time.Now().UTC())
	q.SourceIDs = []string{src.ID}
	a, err := s.Analyze(ctx, q)
	if err != nil || len(a.Sources) != 1 || a.Sources[0].ErrorCode != "capacity_timeout" {
		t.Fatalf("admission classification: %v %#v", err, a.Sources)
	}
}

func TestAnalyzeAllowsFiveMinutesAndHonorsShorterCallerDeadline(t *testing.T) {
	for _, shorter := range []bool{false, true} {
		t.Run(fmt.Sprint(shorter), func(t *testing.T) {
			var handle *fakeClient
			s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
				handle = &fakeClient{}
				return handle, nil
			})
			src := addFakeSource(t, s, "audit")
			handle.aggregateFn = func(ctx context.Context, _, _ string, _ mongo.Pipeline) (mongoCursor, error) {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Error("query deadline missing")
				}
				remaining := time.Until(deadline)
				if shorter {
					if remaining > time.Second {
						t.Error("caller deadline extended")
					}
				} else if remaining < 299*time.Second || remaining > 300*time.Second {
					t.Errorf("query budget %v, want five minutes", remaining)
				}
				return &fakeCursor{}, nil
			}
			ctx := context.Background()
			if shorter {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			}
			q := queryForTest(time.Now().UTC())
			q.SourceIDs = []string{src.ID}
			if _, err := s.Analyze(ctx, q); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestAnalyzeReportsPerWindowTimings(t *testing.T) {
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	first := addFakeSource(t, s, "first")
	second := addFakeSource(t, s, "second")
	now := time.Now().UTC().Truncate(time.Second)
	q := queryForTest(now)
	q.SourceIDs = []string{first.ID, second.ID}
	rowsFor := map[string]int{"first": 3, "second": 1}
	handle.aggregateFn = func(_ context.Context, _, collection string, _ mongo.Pipeline) (mongoCursor, error) {
		docs := make([]any, rowsFor[collection])
		for i := range docs {
			docs[i] = bucketDocument{Controller: "Payroll", Action: fmt.Sprint("Run", i), Kind: "synchronous", Outcome: "success", State: "valid", At: now.Add(-time.Minute), Bin: audit.BinIndex(10), Count: 1, SumMs: 10, MinMs: 10, MaxMs: 10}
		}
		return &fakeCursor{docs: docs}, nil
	}
	analysis, err := s.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Timings) != 4 {
		t.Fatalf("timings = %#v, want 4 rows", analysis.Timings)
	}
	want := []audit.SourceTiming{
		{SourceID: first.ID, Window: "current", Rows: 3},
		{SourceID: first.ID, Window: "reference", Rows: 3},
		{SourceID: second.ID, Window: "current", Rows: 1},
		{SourceID: second.ID, Window: "reference", Rows: 1},
	}
	for i, timing := range analysis.Timings {
		if timing.ReadMs < 0 {
			t.Errorf("row %d ReadMs = %d", i, timing.ReadMs)
		}
		timing.ReadMs = 0
		if timing != want[i] {
			t.Errorf("row %d = %#v, want %#v", i, timing, want[i])
		}
	}
}

// exactAnalysisFixture runs Analyze over a fixed fixture and returns its JSON with
// the run-dependent parts (the random source id, read times) normalized and the
// provenance removed, so it can be compared with the output from before provenance.
var cohortKeyPattern = regexp.MustCompile(`Wy[IJ][A-Za-z0-9_-]+`)

func exactAnalysisFixture(t *testing.T, mode string) (string, audit.Analysis) {
	t.Helper()
	var handle *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		handle = &fakeClient{}
		return handle, nil
	})
	source := addFakeSource(t, s, "audit")
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	calls := 0
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		calls++
		at := now.Add(-10 * time.Minute)
		if calls == 2 {
			at = now.Add(-25 * time.Minute)
		}
		return &fakeCursor{docs: []any{
			bucketDocument{Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", State: "valid", At: at, Bin: 49, Count: 3, SumMs: 300, MinMs: 99, MaxMs: 105},
			bucketDocument{Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", State: "valid", At: at.Add(10 * time.Second), Bin: 49, Count: 2, SumMs: 204, MinMs: 100, MaxMs: 104},
		}}, nil
	}
	q := queryForTest(now)
	q.SourceIDs = []string{source.ID}
	q.QueryMode = mode
	analysis, err := s.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatal(err)
	}
	delete(generic, "provenance")
	if timings, ok := generic["timings"].([]any); ok {
		for _, item := range timings {
			item.(map[string]any)["readMs"] = 0
		}
	}
	normalized, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(normalized), source.ID, "SRC")
	return cohortKeyPattern.ReplaceAllString(text, "KEY"), analysis
}

// exactAnalysisGolden is the fixture's output from before provenance was added,
// with the source id and cohort keys normalized.
const exactAnalysisGolden = `{"apis":[{"action":"Run","controller":"Payroll","current":{"clientErrorRate":0,"completionRate":1,"count":5,"events":5,"histogram":[{"bin":49,"count":5}],"maxMs":105,"meanMs":100.8,"minMs":99,"p50":{"lowerMs":99,"upperMs":105,"valueMs":102},"p90":{"lowerMs":99,"upperMs":105,"valueMs":102},"p95":{"lowerMs":99,"upperMs":105,"valueMs":102},"serverErrorRate":0},"currentEvents":5,"key":"KEY","kind":"synchronous","medianDeltaMs":0,"medianRatio":null,"outcome":"success","p95DeltaMs":0,"p95Ratio":null,"reference":{"clientErrorRate":0,"completionRate":1,"count":5,"events":5,"histogram":[{"bin":49,"count":5}],"maxMs":105,"meanMs":100.8,"minMs":99,"p50":{"lowerMs":99,"upperMs":105,"valueMs":102},"p90":{"lowerMs":99,"upperMs":105,"valueMs":102},"p95":{"lowerMs":99,"upperMs":105,"valueMs":102},"serverErrorRate":0},"referenceEvents":5,"sourceId":"SRC","state":"insufficient_data","totalTimeMs":504}],"approximate":true,"loadWindows":[{"at":"2026-10-22T11:50:00Z","count":3,"timing":{"count":3,"histogram":[{"bin":49,"count":3}],"maxMs":105,"meanMs":100,"minMs":99,"p50":{"lowerMs":99,"upperMs":105,"valueMs":102},"p90":{"lowerMs":99,"upperMs":105,"valueMs":102},"p95":{"lowerMs":99,"upperMs":105,"valueMs":102}}},{"at":"2026-10-22T11:50:10Z","count":2,"timing":{"count":2,"histogram":[{"bin":49,"count":2}],"maxMs":104,"meanMs":102,"minMs":100,"p50":{"lowerMs":100,"upperMs":104,"valueMs":102},"p90":{"lowerMs":100,"upperMs":104,"valueMs":102},"p95":{"lowerMs":100,"upperMs":104,"valueMs":102}}}],"overview":[{"clientErrors":0,"completed":5,"completionRate":1,"delta":{},"events":5,"incomplete":0,"invalid":0,"kind":"synchronous","p50":{"lowerMs":99,"upperMs":105,"valueMs":102},"p90":{"lowerMs":99,"upperMs":105,"valueMs":102},"p95":{"lowerMs":99,"upperMs":105,"valueMs":102},"perMinute":0.3333333333333333,"reference":{"clientErrors":0,"completed":5,"events":5,"incomplete":0,"invalid":0,"perMinute":0.3333333333333333,"serverErrors":0,"unknownOutcome":0},"serverErrors":0,"state":"insufficient_data","unknownOutcome":0}],"quality":{"incomplete":0,"invalid":0,"unknownOutcome":0},"query":{"action":"","baselineEnd":"2026-10-22T11:45:00Z","baselineStart":"2026-10-22T11:30:00Z","bucketSeconds":10,"controller":"","end":"2026-10-22T12:00:00Z","kind":"synchronous","metric":"total","outcome":"success","sourceIds":["SRC"],"start":"2026-10-22T11:45:00Z"},"requestVolume":[{"action":"Run","at":"2026-10-22T11:50:00Z","controller":"Payroll","count":3},{"action":"Run","at":"2026-10-22T11:50:10Z","controller":"Payroll","count":2}],"rollups":[{"action":"Run","completed":5,"completionRate":1,"completionShare":1,"controller":"Payroll","events":5,"failures":0,"kind":"synchronous","perMinute":0.3333333333333333,"referenceCompleted":5,"referenceEvents":5,"referenceFailures":0,"referenceRequests":5,"requests":5}],"sources":[{"error":"","indexWarning":"","ok":true,"sourceId":"SRC","version":"7.0.1"}],"timeline":[{"key":"KEY","points":[{"at":"2026-10-22T11:50:00Z","count":3,"p50":{"lowerMs":99,"upperMs":105,"valueMs":102},"p90":{"lowerMs":99,"upperMs":105,"valueMs":102},"p95":{"lowerMs":99,"upperMs":105,"valueMs":102},"p95Ratio":null},{"at":"2026-10-22T11:50:10Z","count":2,"p50":{"lowerMs":100,"upperMs":104,"valueMs":102},"p90":{"lowerMs":100,"upperMs":104,"valueMs":102},"p95":{"lowerMs":100,"upperMs":104,"valueMs":102},"p95Ratio":null}]}],"timings":[{"readMs":0,"rows":2,"sourceId":"SRC","window":"current"},{"readMs":0,"rows":2,"sourceId":"SRC","window":"reference"}],"warnings":["Latency quantiles are estimated from logarithmic histogram buckets; compared does not mean healthy.","KEY has fewer than 20 valid samples in current or reference","Legacy records without RequestInitiatedAt use CreatedOn when it contains a BSON date."]}`

func TestExactModeAnalysisUnchangedExceptProvenance(t *testing.T) {
	for _, mode := range []string{"", audit.QueryModeExact} {
		got, _ := exactAnalysisFixture(t, mode)
		if mode == audit.QueryModeExact {
			// An explicit exact mode is part of the query echo; nothing else moves.
			if !strings.Contains(got, `"queryMode":"exact"`) {
				t.Fatalf("explicit exact mode must be echoed in the query: %s", got)
			}
			got = strings.Replace(got, `"queryMode":"exact",`, "", 1)
		}
		if got != exactAnalysisGolden {
			t.Fatalf("mode %q: exact output changed apart from provenance\n got: %s\nwant: %s", mode, got, exactAnalysisGolden)
		}
	}
}

// hintFixture is a store with one modern source whose aggregate answers the
// calibration with calibration docs and every other pipeline with an empty cursor.
// It records each pipeline it is given.
type hintFixture struct {
	store     *Store
	source    audit.Source
	handle    *fakeClient
	pipelines []mongo.Pipeline
}

func newHintFixture(t *testing.T, legacy bool, calibration []any) *hintFixture {
	t.Helper()
	s, source, handle := recordStore(t, legacy)
	f := &hintFixture{store: s, source: source, handle: handle}
	handle.aggregateFn = func(_ context.Context, _, _ string, p mongo.Pipeline) (mongoCursor, error) {
		f.pipelines = append(f.pipelines, p)
		if isCalibration(p) {
			return &fakeCursor{docs: calibration}, nil
		}
		return &fakeCursor{}, nil
	}
	return f
}

// idMatches counts the pipelines whose first stage is a $match on _id, and
// reports whether any pipeline other than the calibration read lacks one.
func (f *hintFixture) idMatches() (with, without int) {
	for _, p := range f.pipelines {
		if isCalibration(p) {
			continue
		}
		if first, ok := p[0][0].Value.(bson.D); ok && p[0][0].Key == "$match" && len(first) > 0 && first[0].Key == "_id" {
			with++
		} else {
			without++
		}
	}
	return with, without
}

func (f *hintFixture) calibrations() int {
	n := 0
	for _, p := range f.pipelines {
		if isCalibration(p) {
			n++
		}
	}
	return n
}

func TestAnalyzeExactModeHasNoIDStage(t *testing.T) {
	for _, mode := range []string{"", audit.QueryModeExact} {
		f := newHintFixture(t, false, nil)
		now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
		q := queryForTest(now)
		q.SourceIDs, q.QueryMode = []string{f.source.ID}, mode
		a, err := f.store.Analyze(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		with, without := f.idMatches()
		if with != 0 || without != 2 || f.calibrations() != 0 {
			t.Fatalf("mode %q: exact must add no _id stage and not calibrate (with=%d without=%d cal=%d)", mode, with, without, f.calibrations())
		}
		if len(a.Provenance) != 2 || a.Provenance[0].Window != "current" || a.Provenance[1].Window != "reference" {
			t.Fatalf("mode %q: provenance = %+v", mode, a.Provenance)
		}
		for _, pv := range a.Provenance {
			if pv.SourceID != f.source.ID || pv.QueryMode != audit.QueryModeExact || pv.DocsExamined != nil || pv.Completeness != audit.RanToEnd || pv.Margin != nil || pv.PartialReason != "" || pv.Note != "" {
				t.Fatalf("mode %q: pv = %+v", mode, pv)
			}
			if pv.ScanClass != audit.ScanNarrow || pv.MaxTimeMs != 10000 {
				t.Fatalf("mode %q: pv = %+v", mode, pv)
			}
		}
		if !a.Provenance[0].Start.Equal(q.Start) || !a.Provenance[0].End.Equal(q.End) || !a.Provenance[1].Start.Equal(q.BaselineStart) || !a.Provenance[1].End.Equal(q.BaselineEnd) {
			t.Fatalf("mode %q: windows = %+v", mode, a.Provenance)
		}
	}
}

func TestAnalyzeHintModePrependsIDStageAndLabels(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f := newHintFixture(t, false, calibrationDocs(t, now.Add(-2*time.Hour), 1000))
	q := queryForTest(now)
	q.SourceIDs, q.QueryMode = []string{f.source.ID}, audit.QueryModeHint
	a, err := f.store.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	with, without := f.idMatches()
	if with != 2 || without != 0 || f.calibrations() != 1 {
		t.Fatalf("hint must prepend an _id stage to both windows after one calibration (with=%d without=%d cal=%d)", with, without, f.calibrations())
	}
	// The time $or stays in the second stage, untouched.
	for _, p := range f.pipelines {
		if isCalibration(p) {
			continue
		}
		second, ok := p[1][0].Value.(bson.D)
		if p[1][0].Key != "$match" || !ok || second[0].Key != "$or" {
			t.Fatalf("the original time match must follow the hint stage: %v", p[1])
		}
	}
	want := "bounded by margin 159 s, calibrated on 1000 indexed rows; rows outside 159 s would be missed"
	if len(a.Provenance) != 2 {
		t.Fatalf("provenance = %+v", a.Provenance)
	}
	for _, pv := range a.Provenance {
		if pv.Completeness != audit.HintBounded || pv.QueryMode != audit.QueryModeHint || pv.Margin == nil || pv.Margin.Label != want || pv.Note != "" {
			t.Fatalf("pv = %+v", pv)
		}
		if pv.ScanClass != audit.ScanNarrow || pv.DocsExamined != nil {
			t.Fatalf("pv = %+v", pv)
		}
	}
}

func TestAnalyzeHintFallsBackToExactWithNote(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f := newHintFixture(t, false, nil)
	q := queryForTest(now)
	q.SourceIDs, q.QueryMode = []string{f.source.ID}, audit.QueryModeHint
	a, err := f.store.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if with, without := f.idMatches(); with != 0 || without != 2 {
		t.Fatalf("a failed calibration must run exact (with=%d without=%d)", with, without)
	}
	if len(a.Provenance) != 2 {
		t.Fatalf("provenance = %+v", a.Provenance)
	}
	for _, pv := range a.Provenance {
		if pv.QueryMode != audit.QueryModeExact || pv.Margin != nil || pv.Completeness != audit.RanToEnd {
			t.Fatalf("pv = %+v", pv)
		}
		// The note is the reason alone; the page wraps it in the fallback sentence.
		if pv.Note != "calibration needs at least 1000 rows" || pv.Note != audit.HintReason(audit.ErrTooFewCalibrationRows) {
			t.Fatalf("note = %q", pv.Note)
		}
	}
}

func TestAnalyzeHintOnLegacyRunsExactWithNote(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	f := newHintFixture(t, true, nil)
	q := queryForTest(now)
	q.SourceIDs, q.QueryMode = []string{f.source.ID}, audit.QueryModeHint
	a, err := f.store.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if f.calibrations() != 0 {
		t.Fatal("legacy must not calibrate")
	}
	if with, _ := f.idMatches(); with != 0 {
		t.Fatal("legacy must not use a hint stage")
	}
	if len(a.Provenance) == 0 || a.Provenance[0].QueryMode != audit.QueryModeExact || a.Provenance[0].Note != "not available on MongoDB 3.4" {
		t.Fatalf("provenance = %+v", a.Provenance)
	}
}

func TestAnalyzeTimeoutProvenancePartial(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s, source, handle := recordStore(t, false)
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, context.DeadlineExceeded
	}
	q := queryForTest(now)
	q.SourceIDs = []string{source.ID}
	a, err := s.Analyze(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Sources) != 1 || a.Sources[0].ErrorCode != "query_timeout" {
		t.Fatalf("sources = %+v", a.Sources)
	}
	if len(a.Provenance) == 0 {
		t.Fatal("a failed source must still carry provenance")
	}
	pv := a.Provenance[0]
	if pv.SourceID != source.ID || pv.Window != "current" || pv.Completeness != audit.Partial || pv.PartialReason != "timeout" {
		t.Fatalf("pv = %+v", pv)
	}
}

func TestAnalyzeReadErrorProvenancePartialError(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s, source, handle := recordStore(t, false)
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, errors.New("network trouble")
	}
	q := queryForTest(now)
	q.SourceIDs = []string{source.ID}
	a, _ := s.Analyze(context.Background(), q)
	if len(a.Provenance) == 0 || a.Provenance[0].PartialReason != "error" || a.Provenance[0].Completeness != audit.Partial {
		t.Fatalf("provenance = %+v", a.Provenance)
	}
}
