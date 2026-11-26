package releasehold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func mustAdd(t *testing.T, s Store, n NewHold, now time.Time) Hold {
	t.Helper()
	h, err := s.Add(n, now)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return h
}

func TestMissingStoreHasNoHolds(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "not-made")}
	got, err := s.Active("unravis", t0)
	if err != nil || len(got) != 0 {
		t.Fatalf("Active on a missing store = %v, %v; want none, nil", got, err)
	}
}

func TestStateWindow(t *testing.T) {
	h := Hold{Start: t0, End: t0.Add(time.Hour)}
	cases := []struct {
		at   time.Time
		want string
	}{
		{t0.Add(-time.Second), StateUpcoming},
		{t0, StateActive},
		{t0.Add(time.Hour - time.Second), StateActive},
		{t0.Add(time.Hour), StateEnded},
	}
	for _, c := range cases {
		if got := h.StateAt(c.at); got != c.want {
			t.Errorf("StateAt(%s) = %s, want %s", c.at, got, c.want)
		}
	}
	h.LiftedAt = t0.Add(10 * time.Minute)
	if got := h.StateAt(t0.Add(5 * time.Minute)); got != StateActive {
		t.Errorf("before the lift: %s, want active", got)
	}
	if got := h.StateAt(t0.Add(10 * time.Minute)); got != StateLifted {
		t.Errorf("at the lift: %s, want lifted", got)
	}
}

func TestActiveMatchesProjectAndAll(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	a := mustAdd(t, s, NewHold{Project: "alpha", Reason: "audit", Owner: "pat", End: t0.Add(2 * time.Hour)}, t0)
	all := mustAdd(t, s, NewHold{Project: AllProjects, Reason: "holiday", Owner: "sam", End: t0.Add(time.Hour)}, t0)
	mustAdd(t, s, NewHold{Project: "beta", Reason: "later", Owner: "sam", Start: t0.Add(3 * time.Hour), End: t0.Add(4 * time.Hour)}, t0)

	got, err := s.Active("alpha", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != all.ID || got[1].ID != a.ID {
		t.Fatalf("alpha: %+v; want the all hold (ends first) then the alpha hold", got)
	}
	got, _ = s.Active("beta", t0.Add(time.Minute))
	if len(got) != 1 || got[0].ID != all.ID {
		t.Fatalf("beta: %+v; want only the all hold (its own starts later)", got)
	}
	got, _ = s.Active("beta", t0.Add(3*time.Hour+time.Minute))
	if len(got) != 1 || got[0].Project != "beta" {
		t.Fatalf("beta in its window: %+v", got)
	}
	got, _ = s.Active("", t0.Add(time.Minute))
	if len(got) != 2 {
		t.Fatalf("any project: %d holds, want 2", len(got))
	}
	got, _ = s.Active("gamma", t0.Add(90*time.Minute))
	if len(got) != 0 {
		t.Fatalf("gamma after the all hold ended: %+v", got)
	}
}

func TestAddRefuses(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	ok := NewHold{Project: "alpha", Reason: "r", Owner: "o", End: t0.Add(time.Hour)}
	bad := map[string]func(n *NewHold){
		"project":      func(n *NewHold) { n.Project = "Alpha Beta" },
		"no reason":    func(n *NewHold) { n.Reason = "  " },
		"long reason":  func(n *NewHold) { n.Reason = strings.Repeat("x", MaxReason+1) },
		"line break":   func(n *NewHold) { n.Reason = "a\nb" },
		"ansi":         func(n *NewHold) { n.Owner = "a\x1b[31mb" },
		"no owner":     func(n *NewHold) { n.Owner = "" },
		"no end":       func(n *NewHold) { n.End = time.Time{} },
		"past end":     func(n *NewHold) { n.End = t0.Add(-time.Minute) },
		"end now":      func(n *NewHold) { n.End = t0 },
		"end <= start": func(n *NewHold) { n.Start = t0.Add(2 * time.Hour) },
	}
	for name, change := range bad {
		n := ok
		change(&n)
		if _, err := s.Add(n, t0); err == nil {
			t.Errorf("%s: Add accepted %+v", name, n)
		}
	}
	if hs, _ := s.List(); len(hs) != 0 {
		t.Fatalf("a refused Add saved something: %+v", hs)
	}
}

func TestLift(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	h := mustAdd(t, s, NewHold{Project: "alpha", Reason: "r", Owner: "pat", End: t0.Add(time.Hour)}, t0)
	if _, err := s.Lift("h-nope", "sam", "", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v, want ErrNotFound", err)
	}
	if _, err := s.Lift(h.ID, "", "", t0); err == nil {
		t.Fatal("lift without a name was accepted")
	}
	l, err := s.Lift(h.ID, "sam", "done early", t0.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if l.LiftedBy != "sam" || l.LiftReason != "done early" || !l.LiftedAt.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("lifted = %+v", l)
	}
	if got, _ := s.Active("alpha", t0.Add(11*time.Minute)); len(got) != 0 {
		t.Fatalf("still active after lift: %+v", got)
	}
	if _, err := s.Lift(h.ID, "sam", "", t0.Add(12*time.Minute)); err == nil || !strings.Contains(err.Error(), "already lifted") {
		t.Fatalf("second lift: %v", err)
	}
	h2 := mustAdd(t, s, NewHold{Project: "alpha", Reason: "r", Owner: "pat", End: t0.Add(time.Hour)}, t0)
	if _, err := s.Lift(h2.ID, "sam", "", t0.Add(2*time.Hour)); err == nil || !strings.Contains(err.Error(), "already ended") {
		t.Fatalf("lift after the end: %v", err)
	}
}

func TestBadStoreFailsClosed(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      "{not json",
		"newer version": `{"version": 99, "holds": []}`,
		"no version":    `{"holds": []}`,
		"entry no end":  `{"version": 1, "holds": [{"id": "h-1", "project": "all"}]}`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		s := Store{Dir: dir}
		if _, err := s.Active("alpha", t0); err == nil {
			t.Errorf("%s: Active read the store without an error", name)
		}
		if _, err := s.Add(NewHold{Project: "alpha", Reason: "r", Owner: "o", End: t0.Add(time.Hour)}, t0); err == nil {
			t.Errorf("%s: Add wrote over a store it could not read", name)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, FileName)); string(b) != body { //nolint:gosec // G304: a file in the test's own temp folder
			t.Errorf("%s: the store was changed", name)
		}
	}
}

func TestUnknownFieldsAreKept(t *testing.T) {
	dir := t.TempDir()
	body := `{"version": 1, "extra": true, "holds": [{"id": "h-1", "project": "all", "reason": "r", "owner": "o",
		"start": "2026-11-27T11:00:00Z", "end": "2026-11-27T13:00:00Z", "newField": "x"}]}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := (Store{Dir: dir}).Active("alpha", t0)
	if err != nil || len(got) != 1 {
		t.Fatalf("Active = %v, %v; want the one hold", got, err)
	}
}

func TestConcurrentAddLosesNothing(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Add(NewHold{Project: "alpha", Reason: "r", Owner: "o", End: t0.Add(time.Hour)}, t0)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	hs, err := s.List()
	if err != nil || len(hs) != n {
		t.Fatalf("List = %d holds, %v; want %d", len(hs), err, n)
	}
	if _, err := os.Stat(s.Path() + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("the lock file was left behind: %v", err)
	}
}

func TestStaleLockIsTaken(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	p := s.Path() + ".lock"
	if err := os.WriteFile(p, []byte("1 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, NewHold{Project: "alpha", Reason: "r", Owner: "o", End: time.Now().Add(time.Hour)}, time.Now())
}
