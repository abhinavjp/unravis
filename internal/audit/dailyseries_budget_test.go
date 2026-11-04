package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

// slowDailyFake answers like dailyFake but each day takes pause, and it gives
// up as soon as the context ends, the way a real read does.
type slowDailyFake struct {
	*dailyFake
	pause time.Duration
}

func (f *slowDailyFake) Analyze(ctx context.Context, q Query) (Analysis, error) {
	select {
	case <-time.After(f.pause):
	case <-ctx.Done():
		return Analysis{}, ctx.Err()
	}
	return f.dailyFake.Analyze(ctx, q)
}

func slowSeriesInput() EndpointDailyInput {
	return EndpointDailyInput{
		SourceIDs: []string{"s1"}, Operation: "Orders.Submit",
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, MaxSeriesDays),
	}
}

func slowSeriesFake(pause time.Duration) *slowDailyFake {
	return &slowDailyFake{
		dailyFake: newDailyFake([]string{"Orders.Submit"}, func(time.Time, string) []float64 { return constant(30, 500) }),
		pause:     pause,
	}
}

// steppedFake answers like dailyFake. Each day advances the fake series clock by
// step, and the day with index failAt (when failAt >= 0) waits until the context
// ends and fails with its error, the way a real read does when the deadline passes.
type steppedFake struct {
	*dailyFake
	now    *time.Time
	step   time.Duration
	failAt int
	calls  int
}

func (f *steppedFake) Analyze(ctx context.Context, q Query) (Analysis, error) {
	n := f.calls
	f.calls++
	if n == f.failAt {
		<-ctx.Done()
		return Analysis{}, ctx.Err()
	}
	*f.now = f.now.Add(f.step)
	return f.dailyFake.Analyze(ctx, q)
}

// steppedSeries swaps the series clock for one that moves only when a day is
// read, and returns the fake that moves it. The clock starts at base.
func steppedSeries(t *testing.T, base time.Time, step time.Duration, failAt int) *steppedFake {
	t.Helper()
	now := base
	old := seriesNow
	seriesNow = func() time.Time { return now }
	t.Cleanup(func() { seriesNow = old })
	return &steppedFake{
		dailyFake: newDailyFake([]string{"Orders.Submit"}, func(time.Time, string) []float64 { return constant(30, 500) }),
		now:       &now, step: step, failAt: failAt,
	}
}

// A series that cannot finish inside its deadline stops once 80 percent of it has
// passed and returns the days it finished, flagged partial (PF-B-04). The clock is
// stepped, not real: a 1 s budget with 100 ms days stops before the day that
// starts at 800 ms, so exactly 8 days finish. A 99 percent threshold would read 10.
func TestEndpointDailyStopsAtSoftDeadlineWithPartialDays(t *testing.T) {
	const budget = time.Second
	base := time.Now().Add(time.Hour) // far ahead, so the real deadline never ends the read
	ctx, cancel := context.WithDeadline(context.Background(), base.Add(budget))
	defer cancel()
	fake := steppedSeries(t, base, budget/10, -1)
	out, err := RunEndpointDaily(ctx, fake, slowSeriesInput())
	if err != nil {
		t.Fatalf("a series that runs out of time must return its days, got %v", err)
	}
	if !out.Partial || out.DaysPlanned != MaxSeriesDays {
		t.Fatalf("partial %v, planned %d, want true and %d", out.Partial, out.DaysPlanned, MaxSeriesDays)
	}
	if n := len(out.Days); n != 8 {
		t.Fatalf("%d days, want exactly 8 (the soft stop is at 80 percent of the budget)", n)
	}
	// The finished days are the oldest ones, in order, with real quantiles.
	for i, d := range out.Days {
		want := slowSeriesInput().Start.AddDate(0, 0, i).Format("2006-01-02")
		if d.Date != want || d.P95Ms == nil {
			t.Fatalf("day %d = %+v, want date %s with a p95", i, d, want)
		}
	}
	if len(out.Warnings) == 0 {
		t.Error("no warning for the shortened series")
	}
	if len(out.Provenance) == 0 {
		t.Fatal("no provenance")
	}
	for _, p := range out.Provenance {
		if !p.End.Equal(out.Days[len(out.Days)-1].End) {
			t.Errorf("provenance ends %v, want the end of the last finished day %v", p.End, out.Days[len(out.Days)-1].End)
		}
	}
}

// A day that fails because the deadline passed during its read keeps the days
// before it. The series clock stands still, so the soft stop cannot end the read:
// only the deadline can, and exactly the three days before the failing one remain.
func TestEndpointDailyDeadlineInsideADayKeepsFinishedDays(t *testing.T) {
	base := time.Now()
	ctx, cancel := context.WithDeadline(context.Background(), base.Add(100*time.Millisecond))
	defer cancel()
	fake := steppedSeries(t, base, 0, 3)
	out, err := RunEndpointDaily(ctx, fake, slowSeriesInput())
	if err != nil {
		t.Fatalf("err = %v, want the finished days", err)
	}
	if !out.Partial || len(out.Days) != 3 {
		t.Fatalf("%d days, partial %v, want exactly 3 and partial", len(out.Days), out.Partial)
	}
	for i, d := range out.Days {
		if want := slowSeriesInput().Start.AddDate(0, 0, i).Format("2006-01-02"); d.Date != want {
			t.Errorf("day %d = %s, want %s", i, d.Date, want)
		}
	}
	if fake.calls != 4 {
		t.Errorf("%d reads, want 4 (three finished and the one that hit the deadline)", fake.calls)
	}
}

// With no deadline nothing is cut, and the JSON flag is left out.
func TestEndpointDailyCompleteIsNotPartial(t *testing.T) {
	in := slowSeriesInput()
	in.End = in.Start.AddDate(0, 0, 4)
	out, err := RunEndpointDaily(context.Background(), slowSeriesFake(time.Millisecond), in)
	if err != nil || out.Partial || out.DaysPlanned != 0 || len(out.Days) != 4 {
		t.Fatalf("%+v %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err = RunEndpointDaily(ctx, slowSeriesFake(time.Millisecond), in)
	if err != nil || out.Partial || len(out.Days) != 4 {
		t.Fatalf("with a long deadline: %+v %v", out, err)
	}
}

// A request that was cancelled (the tab closed) stops the series at once and is
// an error, not a partial answer.
func TestEndpointDailyCancelStopsTheRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	fake := slowSeriesFake(20 * time.Millisecond)
	start := time.Now()
	_, err := RunEndpointDaily(ctx, fake, slowSeriesInput())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("took %v after the cancel", took)
	}
	fake.mu.Lock()
	n := len(fake.queries)
	fake.mu.Unlock()
	if n >= MaxSeriesDays {
		t.Errorf("%d days were read after the request ended", n)
	}
}

// A series with no finished day when the deadline passes is an error.
func TestEndpointDailyDeadlineBeforeFirstDayIsAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := RunEndpointDaily(ctx, slowSeriesFake(500*time.Millisecond), slowSeriesInput()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}
