package audit

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestSQLBusyValidate(t *testing.T) {
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	ok := SQLBusyInput{Start: t0, End: t0.Add(time.Hour), BucketSeconds: 60}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	for _, tc := range []struct {
		name string
		in   SQLBusyInput
	}{
		{"end_equals_start", SQLBusyInput{Start: t0, End: t0, BucketSeconds: 60}},
		{"end_before_start", SQLBusyInput{Start: t0, End: t0.Add(-time.Hour), BucketSeconds: 60}},
		{"over_31_days", SQLBusyInput{Start: t0, End: t0.Add(32 * 24 * time.Hour), BucketSeconds: 900}},
		{"step_not_allowed", SQLBusyInput{Start: t0, End: t0.Add(time.Hour), BucketSeconds: 120}},
		{"step_zero", SQLBusyInput{Start: t0, End: t0.Add(time.Hour), BucketSeconds: 0}},
		{"step_negative", SQLBusyInput{Start: t0, End: t0.Add(time.Hour), BucketSeconds: -60}},
		{"too_many_steps", SQLBusyInput{Start: t0, End: t0.Add(121 * time.Minute), BucketSeconds: 60}},
		{"too_many_steps_15min", SQLBusyInput{Start: t0, End: t0.Add(31 * 24 * time.Hour), BucketSeconds: 900}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.in.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err=%v, want ErrInvalidInput", err)
			}
		})
	}
	// Exactly 120 steps is allowed; a partly filled last step counts as a step.
	if err := (SQLBusyInput{Start: t0, End: t0.Add(2 * time.Hour), BucketSeconds: 60}).Validate(); err != nil {
		t.Fatalf("120 steps rejected: %v", err)
	}
	if err := (SQLBusyInput{Start: t0, End: t0.Add(2*time.Hour + time.Second), BucketSeconds: 60}).Validate(); err == nil {
		t.Fatal("121 steps accepted")
	}
}

func TestSQLBusyDemoMatchesActivityLightTotals(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	in := SQLBusyInput{Start: anchor.Add(-24 * time.Hour), End: anchor, BucketSeconds: 900}
	res, err := d.SQLBusy(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Recorded {
		t.Fatal("demo must say the number is recorded")
	}
	if len(res.Bins) == 0 {
		t.Fatal("demo answer has no bins")
	}
	rows := demoActivityRows(t, ActivityInput{SourceIDs: []string{"demo-west", "demo-east"}, Start: in.Start, End: in.End, Limit: 100})
	step := time.Duration(in.BucketSeconds) * time.Second
	wantBusy, wantCalls := map[time.Time]float64{}, map[time.Time]int64{}
	for _, r := range rows {
		if r.InitiatedAt == nil || r.InitiatedAt.Before(in.Start) || !r.InitiatedAt.Before(in.End) {
			continue
		}
		if r.Light == nil || r.Light.SQLMs == nil {
			continue
		}
		k := in.Start.Add(r.InitiatedAt.Sub(in.Start) / step * step)
		wantBusy[k] += *r.Light.SQLMs
		wantCalls[k]++
	}
	if len(wantBusy) != len(res.Bins) {
		t.Fatalf("bins=%d, activity rows give %d steps", len(res.Bins), len(wantBusy))
	}
	var prev time.Time
	for _, b := range res.Bins {
		if b.Calls == 0 {
			t.Fatalf("bin %v has 0 calls", b.Start)
		}
		if !prev.IsZero() && !b.Start.After(prev) {
			t.Fatalf("bins not in time order at %v", b.Start)
		}
		prev = b.Start
		if b.Calls != wantCalls[b.Start] {
			t.Fatalf("bin %v calls=%d want %d", b.Start, b.Calls, wantCalls[b.Start])
		}
		if math.Abs(b.BusyMs-wantBusy[b.Start]) > 1e-6 {
			t.Fatalf("bin %v busy=%v want %v", b.Start, b.BusyMs, wantBusy[b.Start])
		}
	}
}

func TestSQLBusyDemoRejectsBadInputAndCancelledContext(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	if _, err := d.SQLBusy(context.Background(), SQLBusyInput{Start: anchor, End: anchor, BucketSeconds: 60}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v, want ErrInvalidInput", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.SQLBusy(ctx, SQLBusyInput{Start: anchor.Add(-time.Hour), End: anchor, BucketSeconds: 60}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}
