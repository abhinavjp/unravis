package store

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
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
