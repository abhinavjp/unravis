package audit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withPatternWorkers runs fn with the worker cap set to n.
func withPatternWorkers(t *testing.T, n int, fn func()) {
	t.Helper()
	old := patternWorkers
	patternWorkers = n
	defer func() { patternWorkers = old }()
	fn()
}

// patternPerSourceCoverage is a CoverageProber whose result differs by source.
type patternPerSourceCoverage struct {
	*patternFakeRepo
	errs  map[string]error
	calls atomic.Int64
}

func (f *patternPerSourceCoverage) Slowest(ctx context.Context, input PageInput) (Page, error) {
	return patternFakeSlow{f.patternFakeRepo}.Slowest(ctx, input)
}

func (f *patternPerSourceCoverage) Coverage(_ context.Context, in CoverageInput) (CoverageReport, error) {
	f.calls.Add(1)
	if err := f.errs[in.SourceID]; err != nil {
		return CoverageReport{}, err
	}
	n := int64(len(in.SourceID)) * 10
	return CoverageReport{SourceID: in.SourceID, Total: CoverageRow{Kind: "all", Docs: 100 + n, RequestInitiatedAt: FieldCoverage{NonNull: 90 + n}, AccountID: FieldCoverage{NonNull: 40}}}, nil
}

// manySourceRepo has stall and audit rows for eight sources, one of them past
// the row cap, with rows that are missing fields and rows that tie on start.
func manySourceRepo() (*patternFakeRepo, PatternInput) {
	repo := &patternFakeRepo{results: []SourceResult{{SourceID: "s1", OK: true}, {SourceID: "s2", OK: false}}, listed: []Source{{ID: "s2", Label: "Second"}}}
	in := patternInput()
	in.SourceIDs = nil
	for i := 1; i <= MaxSources; i++ {
		src := fmt.Sprintf("s%d", i)
		in.SourceIDs = append(in.SourceIDs, src)
		n := 6 + i
		if i == 3 {
			n = MaxPatternRows + 30
		}
		for j := 0; j < n; j++ {
			// Starts overlap across sources so the merged order depends on the tie breaks.
			s := stallRow(fmt.Sprintf("%s-%04d", src, j), time.Duration(j%40)*20*time.Second, 50000+float64(j), j%4)
			s.SourceID = src
			repo.slow = append(repo.slow, s)
			a := auditRow(fmt.Sprintf("%s-a%04d", src, j), time.Duration(j%25)*time.Minute, 20000, 9000+float64(j%5)*1000)
			a.SourceID = src
			repo.audit = append(repo.audit, a)
		}
		bad := stallRow(src+"-nostart", 0, 60000, 1)
		bad.SourceID, bad.InitiatedAt = src, nil
		repo.slow = append(repo.slow, bad)
	}
	return repo, in
}

func TestRunPatternsParallelMatchesSerial(t *testing.T) {
	coverageErrs := []map[string]error{
		nil,
		{"s4": fmt.Errorf("%w: live", ErrBroadScanRefused)},
		{"s2": errors.New("socket text"), "s6": fmt.Errorf("%w: live", ErrBroadScanRefused)},
	}
	for ci, errs := range coverageErrs {
		t.Run(fmt.Sprintf("coverage case %d", ci), func(t *testing.T) {
			var serial PatternReport
			withPatternWorkers(t, 1, func() {
				repo, in := manySourceRepo()
				r, err := RunPatterns(context.Background(), &patternPerSourceCoverage{patternFakeRepo: repo, errs: errs}, in)
				if err != nil {
					t.Fatal(err)
				}
				serial = r
			})
			if len(serial.Stalls) == 0 || len(serial.AuditStalls) == 0 || !serial.Truncated || len(serial.Provenance) < 2*MaxSources || serial.Coverage == nil {
				t.Fatalf("serial reference is too thin to prove anything: stalls %d audit %d truncated %v prov %d", len(serial.Stalls), len(serial.AuditStalls), serial.Truncated, len(serial.Provenance))
			}
			for run := 0; run < 15; run++ {
				repo, in := manySourceRepo()
				got, err := RunPatterns(context.Background(), &patternPerSourceCoverage{patternFakeRepo: repo, errs: errs}, in)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(serial, got) {
					t.Fatalf("run %d: parallel report differs from serial\nserial:   %+v\nparallel: %+v", run, serial, got)
				}
			}
		})
	}
}

// patternGauge counts reads in flight overall and per source.
type patternGauge struct {
	mu        sync.Mutex
	inflight  int
	maxTotal  int
	perSource map[string]int
	maxPer    int
}

func (g *patternGauge) enter(src string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.perSource == nil {
		g.perSource = map[string]int{}
	}
	g.inflight++
	g.perSource[src]++
	g.maxTotal = max(g.maxTotal, g.inflight)
	g.maxPer = max(g.maxPer, g.perSource[src])
}

func (g *patternGauge) leave(src string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inflight--
	g.perSource[src]--
}

func TestRunPatternsParallelismIsBounded(t *testing.T) {
	repo, in := manySourceRepo()
	var g patternGauge
	hook := func(ctx context.Context, input PageInput) error {
		src := input.Query.SourceIDs[0]
		g.enter(src)
		defer g.leave(src)
		select {
		case <-time.After(10 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	repo.slowHook, repo.auditHook = hook, hook
	if _, err := RunPatterns(context.Background(), patternFakeSlow{repo}, in); err != nil {
		t.Fatal(err)
	}
	if g.maxTotal > MaxPatternWorkers {
		t.Fatalf("%d reads in flight, cap %d", g.maxTotal, MaxPatternWorkers)
	}
	if g.maxTotal < 2 {
		t.Fatalf("sources were read one at a time (max in flight %d)", g.maxTotal)
	}
	if g.maxPer != PerSourceConcurrency {
		t.Fatalf("one source had %d reads in flight, want %d", g.maxPer, PerSourceConcurrency)
	}
}

func TestRunPatternsErrorsMatchSerialOrder(t *testing.T) {
	errStall1, errStall3 := errors.New("stall s1"), errors.New("stall s3")
	errAudit0, errAudit2 := errors.New("audit s0"), errors.New("audit s2")
	cases := []struct {
		name  string
		stall map[string]error
		audit map[string]error
		want  error
	}{
		{"first stall error in source order", map[string]error{"s4": errStall3, "s2": errStall1}, nil, errStall1},
		{"stall error beats an earlier audit error", map[string]error{"s4": errStall3}, map[string]error{"s1": errAudit0}, errStall3},
		{"first audit error in source order", nil, map[string]error{"s3": errAudit2, "s1": errAudit0}, errAudit0},
		{"limit error keeps its class", map[string]error{"s5": fmt.Errorf("%w: rows", ErrLimit)}, nil, ErrLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, workers := range []int{1, MaxPatternWorkers} {
				for run := 0; run < 10; run++ {
					withPatternWorkers(t, workers, func() {
						repo, in := manySourceRepo()
						repo.slowHook = func(_ context.Context, p PageInput) error { return tc.stall[p.Query.SourceIDs[0]] }
						repo.auditHook = func(_ context.Context, p PageInput) error { return tc.audit[p.Query.SourceIDs[0]] }
						_, err := RunPatterns(context.Background(), patternFakeSlow{repo}, in)
						if !errors.Is(err, tc.want) {
							t.Fatalf("workers %d run %d: err %v, want %v", workers, run, err, tc.want)
						}
					})
				}
			}
		})
	}
}

func TestRunPatternsErrorStopsLaterSources(t *testing.T) {
	// With one worker the failing first source must stop the rest from starting.
	withPatternWorkers(t, 1, func() {
		repo, in := manySourceRepo()
		boom := errors.New("boom")
		repo.slowHook = func(_ context.Context, p PageInput) error {
			if p.Query.SourceIDs[0] == "s1" {
				return boom
			}
			return nil
		}
		if _, err := RunPatterns(context.Background(), patternFakeSlow{repo}, in); !errors.Is(err, boom) {
			t.Fatalf("err %v", err)
		}
		if len(repo.slowCalls) != 1 || len(repo.auditCalls) != 0 {
			t.Fatalf("slow calls %d audit calls %d, want the failure to stop later reads", len(repo.slowCalls), len(repo.auditCalls))
		}
	})
}

func TestRunPatternsCanceledMidRead(t *testing.T) {
	for _, phase := range []string{"stall", "audit"} {
		t.Run(phase, func(t *testing.T) {
			repo, in := manySourceRepo()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var inflight atomic.Int64
			started := make(chan struct{}, 64)
			block := func(ctx context.Context, _ PageInput) error {
				inflight.Add(1)
				defer inflight.Add(-1)
				started <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}
			if phase == "stall" {
				repo.slowHook = block
			} else {
				repo.auditHook = block
			}
			go func() {
				<-started
				cancel()
			}()
			done := make(chan error, 1)
			go func() {
				_, err := RunPatterns(ctx, patternFakeSlow{repo}, in)
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err %v, want context.Canceled", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("RunPatterns did not return after cancel")
			}
			if n := inflight.Load(); n != 0 {
				t.Fatalf("%d reads still running after RunPatterns returned", n)
			}
		})
	}
}

func TestRunPatternsCanceledBeforeStartingEverySource(t *testing.T) {
	// More sources than workers, every worker busy, then cancel: the sources that
	// never started must not turn into a partial report.
	repo, in := manySourceRepo()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var n atomic.Int64
	repo.slowHook = func(ctx context.Context, _ PageInput) error {
		if n.Add(1) == int64(MaxPatternWorkers) {
			cancel()
		}
		<-ctx.Done()
		return nil // a driver that swallows the cancel must still not yield a report
	}
	if _, err := RunPatterns(ctx, patternFakeSlow{repo}, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
}

func TestRunPatternsRecoversFromReadPanic(t *testing.T) {
	const secret = "secret internal detail"
	for _, phase := range []string{"stall", "audit"} {
		for _, workers := range []int{1, MaxPatternWorkers} {
			t.Run(fmt.Sprintf("%s/%d workers", phase, workers), func(t *testing.T) {
				withPatternWorkers(t, workers, func() {
					repo, in := manySourceRepo()
					boom := func(_ context.Context, p PageInput) error {
						if p.Query.SourceIDs[0] == "s2" {
							panic(secret)
						}
						return nil
					}
					if phase == "stall" {
						repo.slowHook = boom
					} else {
						repo.auditHook = boom
					}
					_, err := RunPatterns(context.Background(), patternFakeSlow{repo}, in)
					if !errors.Is(err, ErrPatternReadPanic) {
						t.Fatalf("err %v, want ErrPatternReadPanic", err)
					}
					if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "goroutine") {
						t.Fatalf("error leaks panic details: %q", err)
					}
				})
			})
		}
	}
}

func TestRunBoundedRecoversPanicsAndStopsStarting(t *testing.T) {
	var ran atomic.Int64
	started, err := runBounded(context.Background(), 50, 1, func(i int) {
		ran.Add(1)
		if i == 3 {
			panic(errors.New("boom"))
		}
	})
	if !errors.Is(err, ErrPatternReadPanic) || !started {
		t.Fatalf("started %v, err %v", started, err)
	}
	if n := ran.Load(); n != 4 {
		t.Fatalf("%d calls ran after a panic at index 3 with one worker, want 4", n)
	}

	// Several panics at once: the lowest index is reported, and every call that
	// started returns. A panic stops later calls from starting, so how many start
	// depends on timing; the first always does.
	var began, done atomic.Int64
	_, err = runBounded(context.Background(), 4, 4, func(i int) {
		began.Add(1)
		defer done.Add(1)
		panic(i)
	})
	if !errors.Is(err, ErrPatternReadPanic) || !strings.Contains(err.Error(), "source 1") {
		t.Fatalf("err %v, want the first source's panic", err)
	}
	if began.Load() < 1 || done.Load() != began.Load() {
		t.Fatalf("%d calls started, %d finished, want every started call to finish", began.Load(), done.Load())
	}
}
