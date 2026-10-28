package casestore_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// The hooks in these tests are fakes: they stand in for P3's archive recovery and
// P4's free and erase recovery.
func recoveryEnv(t *testing.T) (casestore.Store, casestore.Lifecycle, *casestore.Recoverer) {
	t.Helper()
	st := casestore.NewMemory()
	l := st.(casestore.Lifecycle)
	r, err := casestore.NewRecoverer(st)
	if err != nil {
		t.Fatal(err)
	}
	return st, l, r
}

func mkCase(t *testing.T, st casestore.Store, title string) casestore.CaseID {
	t.Helper()
	info, err := st.Create(context.Background(), title)
	if err != nil {
		t.Fatal(err)
	}
	return info.ID
}

func TestRecoverAllRunsTheHookForMarkedCasesOnly(t *testing.T) {
	ctx := context.Background()
	st, l, r := recoveryEnv(t)
	a, b := mkCase(t, st, "a"), mkCase(t, st, "b")
	mkCase(t, st, "plain")
	for _, id := range []casestore.CaseID{a, b} {
		if err := l.SetMark(ctx, id, casestore.MarkArchiving); err != nil {
			t.Fatal(err)
		}
	}
	var ran []casestore.CaseID
	if err := r.Register(casestore.MarkArchiving, func(ctx context.Context, id casestore.CaseID) error {
		ran = append(ran, id)
		return l.SetMark(ctx, id, "")
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := r.RecoverAll(ctx)
	if err != nil || rep.Marked != 2 || rep.Recovered != 2 || len(rep.Failed) != 0 || len(ran) != 2 {
		t.Fatalf("RecoverAll = %+v %v ran=%v", rep, err, ran)
	}
	// A second run finds nothing to do.
	ran = nil
	if rep, _ = r.RecoverAll(ctx); rep.Marked != 0 || len(ran) != 0 {
		t.Errorf("second RecoverAll = %+v ran=%v", rep, ran)
	}
}

func TestRecoveryFailureKeepsTheMarkAndTheRest(t *testing.T) {
	ctx := context.Background()
	st, l, r := recoveryEnv(t)
	bad, good := mkCase(t, st, "bad"), mkCase(t, st, "good")
	_ = l.SetMark(ctx, bad, casestore.MarkFreeing)
	_ = l.SetMark(ctx, good, casestore.MarkFreeing)
	_ = r.Register(casestore.MarkFreeing, func(ctx context.Context, id casestore.CaseID) error {
		if id == bad {
			return errors.New("disk is busy")
		}
		return l.SetMark(ctx, id, "")
	})
	rep, err := r.RecoverAll(ctx)
	if err != nil || rep.Marked != 2 || rep.Recovered != 1 || len(rep.Failed) != 1 || rep.Failed[0] != bad {
		t.Fatalf("RecoverAll = %+v %v", rep, err)
	}
	if m, _ := l.Mark(ctx, bad); m != casestore.MarkFreeing {
		t.Errorf("failed case lost its mark: %q", m)
	}
	// The case that could not recover still answers "being freed" (M19).
	mark, err := r.RecoverOne(ctx, bad)
	if err == nil || !errors.Is(casestore.CheckMark(mark), casestore.ErrFreeing) {
		t.Errorf("RecoverOne on the failing case: %q %v", mark, err)
	}
}

func TestMarkWithoutHookIsLeftAlone(t *testing.T) {
	ctx := context.Background()
	st, l, r := recoveryEnv(t)
	id := mkCase(t, st, "erasing")
	_ = l.SetMark(ctx, id, casestore.MarkErasing)
	rep, err := r.RecoverAll(ctx)
	if err != nil || rep.Marked != 1 || rep.Recovered != 0 || len(rep.Failed) != 0 {
		t.Fatalf("RecoverAll = %+v %v", rep, err)
	}
	mark, err := r.RecoverOne(ctx, id)
	if err != nil || !errors.Is(casestore.CheckMark(mark), casestore.ErrErasing) {
		t.Errorf("RecoverOne = %q %v, want the erasing mark (M5)", mark, err)
	}
	if err := r.Register("sleeping", nil); err == nil {
		t.Error("an unknown mark was registered")
	}
}

// countingStore counts the mark calls that reach the store, to prove a plain read
// of an unmarked case runs nothing and writes nothing.
type countingStore struct {
	casestore.Store
	marks atomic.Int64
}

func (c *countingStore) Mark(ctx context.Context, id casestore.CaseID) (string, error) {
	c.marks.Add(1)
	return c.Store.(casestore.Lifecycle).Mark(ctx, id)
}
func (c *countingStore) SetMark(ctx context.Context, id casestore.CaseID, m string) error {
	c.marks.Add(1)
	return c.Store.(casestore.Lifecycle).SetMark(ctx, id, m)
}
func (c *countingStore) ReopenCase(ctx context.Context, id casestore.CaseID) error {
	return c.Store.(casestore.Lifecycle).ReopenCase(ctx, id)
}
func (c *countingStore) CreateFor(ctx context.Context, t, rid string) (casestore.CaseInfo, bool, error) {
	return c.Store.(casestore.Lifecycle).CreateFor(ctx, t, rid)
}

func TestUnmarkedCaseCostsNothing(t *testing.T) {
	ctx := context.Background()
	inner := casestore.NewMemory()
	cs := &countingStore{Store: inner}
	r, err := casestore.NewRecoverer(cs)
	if err != nil {
		t.Fatal(err)
	}
	id := mkCase(t, cs, "plain")
	info, _ := inner.List(ctx)
	if mark, err := r.RecoverIfMarked(ctx, info[0]); err != nil || mark != "" {
		t.Fatalf("RecoverIfMarked = %q %v", mark, err)
	}
	page := r.RecoverPage(ctx, info)
	if len(page) != 1 || page[0].ID != id {
		t.Fatalf("RecoverPage = %+v", page)
	}
	if rep, _ := r.RecoverAll(ctx); rep.Marked != 0 {
		t.Errorf("RecoverAll = %+v", rep)
	}
	if n := cs.marks.Load(); n != 0 {
		t.Errorf("%d mark calls reached the store for an unmarked case, want 0", n)
	}
}

func TestRecoverPageRepairsOnlyThePage(t *testing.T) {
	ctx := context.Background()
	st, l, r := recoveryEnv(t)
	onPage, offPage := mkCase(t, st, "on"), mkCase(t, st, "off")
	_ = l.SetMark(ctx, onPage, casestore.MarkArchiving)
	_ = l.SetMark(ctx, offPage, casestore.MarkArchiving)
	var ran []casestore.CaseID
	_ = r.Register(casestore.MarkArchiving, func(ctx context.Context, id casestore.CaseID) error {
		ran = append(ran, id)
		return l.SetMark(ctx, id, "")
	})
	all, _ := st.List(ctx)
	var page []casestore.CaseInfo
	for _, i := range all {
		if i.ID == onPage {
			page = append(page, i)
		}
	}
	out := r.RecoverPage(ctx, page)
	if len(ran) != 1 || ran[0] != onPage || out[0].Mark != "" {
		t.Errorf("RecoverPage ran %v, row mark %q", ran, out[0].Mark)
	}
	if m, _ := l.Mark(ctx, offPage); m != casestore.MarkArchiving {
		t.Errorf("a row off the page was repaired: %q", m)
	}
}

func TestRecoveryRunsOncePerCaseUnderRace(t *testing.T) {
	ctx := context.Background()
	st, l, r := recoveryEnv(t)
	id := mkCase(t, st, "race")
	_ = l.SetMark(ctx, id, casestore.MarkArchiving)
	var runs atomic.Int64
	_ = r.Register(casestore.MarkArchiving, func(ctx context.Context, id casestore.CaseID) error {
		runs.Add(1)
		return l.SetMark(ctx, id, "")
	})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if mark, err := r.RecoverOne(ctx, id); err != nil || mark != "" {
				t.Errorf("RecoverOne = %q %v", mark, err)
			}
		}()
	}
	wg.Wait()
	if n := runs.Load(); n != 1 {
		t.Errorf("hook ran %d times, want 1", n)
	}
}
