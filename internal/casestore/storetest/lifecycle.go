package storetest

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func lifecycler(t *testing.T, s casestore.Store) casestore.Lifecycle {
	t.Helper()
	l, ok := s.(casestore.Lifecycle)
	if !ok {
		t.Fatalf("%T does not implement casestore.Lifecycle", s)
	}
	return l
}

func listed(t *testing.T, e *env, id casestore.CaseID) (casestore.CaseInfo, bool) {
	t.Helper()
	all, err := e.store.List(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range all {
		if i.ID == id {
			return i, true
		}
	}
	return casestore.CaseInfo{}, false
}

// reopenContract: Closed to Open, once; the story gets an entry for each move;
// an open handle sees the change; the retention clock stops.
func reopenContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	l := lifecycler(t, e.store)
	c := e.newCase("reopen me")
	id := c.Info().ID

	if err := l.ReopenCase(e.ctx, id); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Fatalf("Reopen of an open case: %v, want ErrCaseChanged", err)
	}
	if err := e.store.CloseCase(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("data"), emitAll(ev(c, "1", "k", "h", 0))); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Fatalf("import into closed case: %v", err)
	}
	e.clock.add(1)
	if err := l.ReopenCase(e.ctx, id); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if info := c.Info(); info.Closed != nil || casestore.StateOf(info) != casestore.StateOpen {
		t.Errorf("open handle after Reopen: %+v", info)
	}
	if info, ok := listed(t, e, id); !ok || info.Closed != nil {
		t.Errorf("List after Reopen: %+v %v", info, ok)
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("data"), emitAll(ev(c, "1", "k", "h", 0))); err != nil {
		t.Fatalf("import after Reopen: %v", err)
	}
	// A second Reopen (a double press) is refused, and writes nothing.
	before := len(c.Info().History)
	if err := l.ReopenCase(e.ctx, id); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("second Reopen: %v, want ErrCaseChanged", err)
	}
	if after := len(c.Info().History); after != before {
		t.Errorf("a refused Reopen wrote history: %d -> %d", before, after)
	}
	var kinds []string
	for _, h := range c.Info().History {
		kinds = append(kinds, h.Kind)
	}
	if got := strings.Join(kinds, ","); got != "created,closed,reopened" {
		t.Errorf("history = %q, want created,closed,reopened", got)
	}
	// Closed again and reopened again: history grows, in order.
	if err := e.store.CloseCase(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := l.ReopenCase(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if h := c.Info().History; len(h) != 5 || h[3].Kind != casestore.HistoryClosed || h[4].Kind != casestore.HistoryReopened {
		t.Errorf("history after second cycle: %+v", h)
	}
	// Retention: the stamp is gone, so Sweep keeps the case however old.
	if err := e.store.CloseCase(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := l.ReopenCase(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if gone, err := e.store.Sweep(e.ctx, t0.Add(10*casestore.Retention)); err != nil || len(gone) != 0 {
		t.Errorf("sweep of a reopened case: %v %v", gone, err)
	}
	if err := l.ReopenCase(e.ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Reopen of an unknown case: %v", err)
	}
	if err := l.ReopenCase(e.ctx, "nope"); !errors.Is(err, casestore.ErrBadID) {
		t.Errorf("Reopen of a bad id: %v", err)
	}
}

// createForContract: one request id, one case.
func createForContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	l := lifecycler(t, e.store)
	rid := "req-0001-aaaa"

	a, existed, err := l.CreateFor(e.ctx, "First", rid)
	if err != nil || existed || a.RequestID != rid {
		t.Fatalf("first CreateFor: %+v %v %v", a, existed, err)
	}
	b, existed, err := l.CreateFor(e.ctx, "First", rid)
	if err != nil || !existed || b.ID != a.ID {
		t.Fatalf("repeat CreateFor: %+v %v %v", b, existed, err)
	}
	// A repeat with another title still names the same case: the id decides.
	if c, existed, err := l.CreateFor(e.ctx, "Other title", rid); err != nil || !existed || c.ID != a.ID || c.Title != "First" {
		t.Errorf("repeat with a new title: %+v %v %v", c, existed, err)
	}
	if d, existed, err := l.CreateFor(e.ctx, "Second", "req-0002-bbbb"); err != nil || existed || d.ID == a.ID {
		t.Errorf("another id: %+v %v %v", d, existed, err)
	}
	// No id means a new case every time, as Create.
	n1, _, _ := l.CreateFor(e.ctx, "Plain", "")
	n2, _, _ := l.CreateFor(e.ctx, "Plain", "")
	if n1.ID == n2.ID || n1.RequestID != "" {
		t.Errorf("empty request id: %v %v", n1, n2)
	}
	for _, bad := range []string{"short", strings.Repeat("a", 65), "has space 1234", "semi;colon-12", "ünicode-12345"} {
		if _, _, err := l.CreateFor(e.ctx, "Bad", bad); err == nil {
			t.Errorf("request id %q accepted", bad)
		}
	}
	// A closed case is returned as it is (the wizard then lists the rest as retries).
	if err := e.store.CloseCase(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if c, existed, err := l.CreateFor(e.ctx, "First", rid); err != nil || !existed || c.ID != a.ID || c.Closed == nil {
		t.Errorf("repeat on a closed case: %+v %v %v", c, existed, err)
	}
	// A case being archived is not handed back: M18.
	if err := l.SetMark(e.ctx, a.ID, casestore.MarkArchiving); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.CreateFor(e.ctx, "First", rid); !errors.Is(err, casestore.ErrAlreadyMade) {
		t.Errorf("repeat on an archiving case: %v, want ErrAlreadyMade", err)
	}
	if err := l.SetMark(e.ctx, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	// Erased: the id is free again, a new case is made.
	if err := e.store.Erase(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if c, existed, err := l.CreateFor(e.ctx, "First", rid); err != nil || existed || c.ID == a.ID {
		t.Errorf("after erase: %+v %v %v", c, existed, err)
	}

	// A double press that races: many calls, one case.
	var wg sync.WaitGroup
	ids := make([]casestore.CaseID, 12)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, _, err := l.CreateFor(e.ctx, "Racer", "req-race-0000")
			if err != nil {
				t.Errorf("racer %d: %v", i, err)
			}
			ids[i] = c.ID
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("racing CreateFor made more than one case: %v", ids)
		}
	}
	all, _ := e.store.List(e.ctx)
	n := 0
	for _, i := range all {
		if i.RequestID == "req-race-0000" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d cases carry the racing request id, want 1", n)
	}
}

// markContract: a mark is kept, shown, and gives the right refusal to a move.
func markContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	l := lifecycler(t, e.store)
	c := e.newCase("marked")
	id := c.Info().ID

	if m, err := l.Mark(e.ctx, id); err != nil || m != "" {
		t.Fatalf("fresh Mark = %q %v", m, err)
	}
	if err := l.SetMark(e.ctx, id, "sleeping"); err == nil {
		t.Error("unknown mark accepted")
	}
	if err := l.SetMark(e.ctx, id, casestore.MarkArchiving); err != nil {
		t.Fatal(err)
	}
	if m, _ := l.Mark(e.ctx, id); m != casestore.MarkArchiving {
		t.Errorf("Mark = %q", m)
	}
	if c.Info().Mark != casestore.MarkArchiving {
		t.Error("an open handle does not show the mark")
	}
	if info, _ := listed(t, e, id); info.Mark != casestore.MarkArchiving || casestore.StateOf(info) != casestore.StateArchiving {
		t.Errorf("List does not show the mark: %+v", info)
	}
	// Archiving: Reopen is a change, not a refusal by mark.
	if err := e.store.CloseCase(e.ctx, id); err != nil {
		t.Fatalf("Close under an archiving mark: %v", err)
	}
	if err := l.ReopenCase(e.ctx, id); !errors.Is(err, casestore.ErrCaseChanged) {
		t.Errorf("Reopen while archiving: %v, want ErrCaseChanged", err)
	}
	for mark, want := range map[string]error{casestore.MarkFreeing: casestore.ErrFreeing, casestore.MarkErasing: casestore.ErrErasing} {
		if err := l.SetMark(e.ctx, id, mark); err != nil {
			t.Fatal(err)
		}
		if err := l.ReopenCase(e.ctx, id); !errors.Is(err, want) {
			t.Errorf("Reopen under %s: %v, want %v", mark, err, want)
		}
	}
	if err := l.SetMark(e.ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if err := l.ReopenCase(e.ctx, id); err != nil {
		t.Errorf("Reopen after the mark cleared: %v", err)
	}
	if err := l.SetMark(e.ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaa", casestore.MarkArchiving); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("SetMark on an unknown case: %v", err)
	}
}
