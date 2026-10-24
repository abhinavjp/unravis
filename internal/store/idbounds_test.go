package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// calibrationDocs yields n rows: row i starts base+i s, and its ObjectId is
// (i%100) s away from that start, before it for odd i and after it for even i.
func calibrationDocs(t *testing.T, base time.Time, n int) []any {
	t.Helper()
	docs := make([]any, 0, n)
	for i := 0; i < n; i++ {
		ria := base.Add(time.Duration(i) * time.Second)
		offset := time.Duration(i%100) * time.Second
		if i%2 == 1 {
			offset = -offset
		}
		raw, err := bson.Marshal(bson.D{{Key: "_id", Value: bson.NewObjectIDFromTimestamp(ria.Add(offset))}, {Key: "RequestInitiatedAt", Value: ria}})
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, bson.Raw(raw))
	}
	return docs
}

func isCalibration(p mongo.Pipeline) bool { return len(p) > 0 && p[0][0].Key == "$sort" }

func idStore(t *testing.T, legacy bool) (*Store, *profile, *fakeClient, func()) {
	t.Helper()
	s, src, handle := recordStore(t, legacy)
	p, release, err := s.leaseSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, handle, release
}

func TestCalibrationPipelineUsesIDIndexShape(t *testing.T) {
	raw, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: calibrationPipeline()}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"pipeline":[{"$sort":{"_id":-1}},{"$limit":10000},{"$project":{"_id":1,"RequestInitiatedAt":1}}]}`
	if string(raw) != want {
		t.Fatalf("calibration pipeline = %s\nwant %s", raw, want)
	}
}

func TestIDMarginComputesFromObjectIDOffsets(t *testing.T) {
	s, p, handle, release := idStore(t, false)
	defer release()
	base := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	docs := calibrationDocs(t, base, 1000)
	// A row without a date RequestInitiatedAt is not part of the sample.
	noDate, _ := bson.Marshal(bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "RequestInitiatedAt", Value: "not a date"}})
	docs = append(docs, bson.Raw(noDate))
	handle.aggregateFn = func(ctx context.Context, _, _ string, pipeline mongo.Pipeline) (mongoCursor, error) {
		if !isCalibration(pipeline) {
			t.Fatalf("unexpected pipeline %v", pipeline)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > audit.InteractiveQueryBudget {
			t.Fatalf("calibration must run under the interactive budget, deadline=%v ok=%v", deadline, ok)
		}
		return &fakeCursor{docs: docs}, nil
	}
	m, err := s.idMargin(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	// Absolute offsets are 0..99 s, ten rows each, so nearest-rank p99.9 of 1000
	// rows is 99 s; pad 60 s gives 159 s.
	if m.ObservedMs != 99000 || m.PadMs != 60000 || m.MarginMs != 159000 || m.Percentile != 99.9 {
		t.Fatalf("margin = %+v", m)
	}
	if m.SampleSize != 1000 || !m.SampleFrom.Equal(base) || !m.SampleTo.Equal(base.Add(999*time.Second)) || !m.CalibratedAt.Equal(now) {
		t.Fatalf("sample = %+v", m)
	}
	want := "bounded by margin 159 s, calibrated on 1000 indexed rows; rows outside 159 s would be missed"
	if m.Label != want {
		t.Fatalf("label = %q", m.Label)
	}
}

func TestIDMarginCachedWithinTTL(t *testing.T) {
	s, p, handle, release := idStore(t, false)
	defer release()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	docs := calibrationDocs(t, now.Add(-time.Hour), 1000)
	calls := 0
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		calls++
		return &fakeCursor{docs: docs}, nil
	}
	first, err := s.idMargin(context.Background(), p)
	if err != nil || calls != 1 {
		t.Fatalf("first: err=%v calls=%d", err, calls)
	}
	now = now.Add(audit.IDMarginTTL - time.Second)
	second, err := s.idMargin(context.Background(), p)
	if err != nil || calls != 1 || second.MarginMs != first.MarginMs || !second.CalibratedAt.Equal(first.CalibratedAt) {
		t.Fatalf("second must come from the cache: err=%v calls=%d", err, calls)
	}
	now = now.Add(2 * time.Second)
	third, err := s.idMargin(context.Background(), p)
	if err != nil || calls != 2 || !third.CalibratedAt.Equal(now) {
		t.Fatalf("after the TTL it must recalibrate: err=%v calls=%d calibratedAt=%v", err, calls, third.CalibratedAt)
	}
}

func TestIDMarginTooFewRows(t *testing.T) {
	s, p, handle, release := idStore(t, false)
	defer release()
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return &fakeCursor{docs: calibrationDocs(t, time.Now().UTC().Add(-time.Hour), audit.IDMinCalibrationRows-1)}, nil
	}
	if _, err := s.idMargin(context.Background(), p); !errors.Is(err, audit.ErrTooFewCalibrationRows) {
		t.Fatalf("err = %v, want ErrTooFewCalibrationRows", err)
	}
	// A failure is not cached.
	s.marginMu.Lock()
	cached := len(s.margins)
	s.marginMu.Unlock()
	if cached != 0 {
		t.Fatal("a failed calibration must not be cached")
	}
}

func TestIDMarginNotAvailableOnLegacy(t *testing.T) {
	s, p, handle, release := idStore(t, true)
	defer release()
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		t.Fatal("legacy must not run the calibration aggregate")
		return nil, nil
	}
	_, err := s.idMargin(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "not available on MongoDB 3.4") {
		t.Fatalf("err = %v", err)
	}
}

func TestIDMarginAggregateErrorIsReturned(t *testing.T) {
	s, p, handle, release := idStore(t, false)
	defer release()
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		return nil, errors.New("boom")
	}
	if _, err := s.idMargin(context.Background(), p); err == nil {
		t.Fatal("aggregate error must fail the calibration")
	}
}

func TestIDHintStageBoundsRoundOutward(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 700*int(time.Millisecond), time.UTC)
	end := time.Date(2026, 10, 22, 12, 15, 0, 200*int(time.Millisecond), time.UTC)
	stage := idHintStage(start, end, 61*time.Second)
	if len(stage) != 1 || stage[0].Key != "$match" {
		t.Fatalf("stage = %v", stage)
	}
	match := stage[0].Value.(bson.D)
	if len(match) != 1 || match[0].Key != "_id" {
		t.Fatalf("match = %v", match)
	}
	bounds := match[0].Value.(bson.D)
	if len(bounds) != 2 || bounds[0].Key != "$gte" || bounds[1].Key != "$lt" {
		t.Fatalf("bounds = %v", bounds)
	}
	lower := bounds[0].Value.(bson.ObjectID).Timestamp().UTC()
	upper := bounds[1].Value.(bson.ObjectID).Timestamp().UTC()
	// 12:00:00.700 - 61 s = 11:58:59.700, floored to the second.
	if want := time.Date(2026, 10, 22, 11, 58, 59, 0, time.UTC); !lower.Equal(want) {
		t.Fatalf("lower = %v, want %v", lower, want)
	}
	// 12:15:00.200 + 61 s = 12:16:01.200, floored, plus one second, exclusive.
	if want := time.Date(2026, 10, 22, 12, 16, 2, 0, time.UTC); !upper.Equal(want) {
		t.Fatalf("upper = %v, want %v", upper, want)
	}
}

func TestWithIDHintPrependsWithoutTouchingTheRest(t *testing.T) {
	original := mongo.Pipeline{bson.D{{Key: "$match", Value: bson.D{{Key: "x", Value: 1}}}}, bson.D{{Key: "$limit", Value: 5}}}
	stage := bson.D{{Key: "$match", Value: bson.D{{Key: "_id", Value: 1}}}}
	out := withIDHint(original, stage)
	if len(out) != 3 || out[0][0].Key != "$match" || out[0][0].Value.(bson.D)[0].Key != "_id" || out[1][0].Value.(bson.D)[0].Key != "x" {
		t.Fatalf("out = %v", out)
	}
	if len(original) != 2 {
		t.Fatal("the input pipeline must not be modified")
	}
}

func TestResolveHintOnlyInHintMode(t *testing.T) {
	s, p, handle, release := idStore(t, false)
	defer release()
	handle.aggregateFn = func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
		t.Fatal("exact mode must not calibrate")
		return nil, nil
	}
	for _, mode := range []string{"", audit.QueryModeExact} {
		if m, note := s.resolveHint(context.Background(), p, mode, ""); m != nil || note != "" {
			t.Fatalf("mode %q: margin=%v note=%q", mode, m, note)
		}
	}
}

