package storetest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Contract cases for Case.Each (the streaming read behind the series route) and
// for the owner-supplied settings an import records.

// seedEach imports n events of two kinds on two hosts, one second apart, and
// returns the set of evidence IDs by label.
func seedEach(t *testing.T, e *env, c casestore.Case, n int) map[string]bool {
	t.Helper()
	var all []evidence.Event
	ids := map[string]bool{}
	for i := 0; i < n; i++ {
		kind, host := "alpha", "web-a"
		if i%2 == 1 {
			kind, host = "beta", "web-b"
		}
		x := ev(c, fmt.Sprintf("e%d", i), kind, host, time.Duration(i)*time.Second)
		all = append(all, x)
		ids[x.EvidenceID] = true
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("seed"), emitAll(all...)); err != nil {
		t.Fatal(err)
	}
	return ids
}

func collectEach(t *testing.T, c casestore.Case, q casestore.EventQuery) ([]evidence.Event, error) {
	t.Helper()
	var got []evidence.Event
	err := c.Each(context.Background(), q, func(x evidence.Event) error {
		got = append(got, x)
		return nil
	})
	return got, err
}

func eachStreams(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("each")
	ids := seedEach(t, e, c, 10)

	got, err := collectEach(t, c, casestore.EventQuery{})
	if err != nil || len(got) != 10 {
		t.Fatalf("Each over everything: %d events, %v", len(got), err)
	}
	seen := map[string]bool{}
	last := map[string]time.Time{}
	for _, x := range got {
		if !ids[x.EvidenceID] || seen[x.EvidenceID] {
			t.Errorf("Each delivered an unknown or repeated event %s", x.EvidenceID)
		}
		seen[x.EvidenceID] = true
		if x.Start.Before(last[x.Kind]) {
			t.Errorf("events of kind %s are not in time order", x.Kind)
		}
		last[x.Kind] = x.Start
	}

	// Limit does not apply: Each has no cap of its own but the scan budget.
	if got, err := collectEach(t, c, casestore.EventQuery{Limit: 1}); err != nil || len(got) != 10 {
		t.Errorf("Each ignores Limit: %d events, %v", len(got), err)
	}
	// Filters match what Events returns.
	for name, q := range map[string]casestore.EventQuery{
		"kinds":   {Kinds: []string{"beta"}},
		"hosts":   {Hosts: []string{"web-a"}},
		"sources": {Sources: []string{"contract"}, Kinds: []string{"alpha"}},
		"window":  {Window: importer.Window{From: t0.Add(2 * time.Second), To: t0.Add(5 * time.Second)}},
		"none":    {Hosts: []string{"nobody"}},
	} {
		want, err := c.Events(e.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := collectEach(t, c, q)
		if err != nil {
			t.Errorf("%s: Each: %v", name, err)
			continue
		}
		var a, b []string
		for _, x := range want {
			a = append(a, x.EvidenceID)
		}
		for _, x := range got {
			b = append(b, x.EvidenceID)
		}
		sort.Strings(a)
		sort.Strings(b)
		if strings.Join(a, ",") != strings.Join(b, ",") {
			t.Errorf("%s: Each delivered %d events, Events %d", name, len(b), len(a))
		}
	}
	// The window is inclusive at both ends: seconds 2, 3, 4 and 5 are four events.
	w := importer.Window{From: t0.Add(2 * time.Second), To: t0.Add(5 * time.Second)}
	if got, _ := collectEach(t, c, casestore.EventQuery{Window: w}); len(got) != 4 {
		t.Errorf("inclusive window: %d events, want 4", len(got))
	}
	// Delivered events are copies.
	err = c.Each(e.ctx, casestore.EventQuery{}, func(x evidence.Event) error {
		x.Attrs["a"] = "changed"
		*x.DurationMs = 99
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := c.Events(e.ctx, casestore.EventQuery{})
	for _, x := range again {
		if x.Attrs["a"] != "b" || *x.DurationMs != 5 {
			t.Fatal("Each hands out the store's own memory")
		}
	}
}

func eachBudget(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("budget")
	seedEach(t, e, c, 10)

	for _, tc := range []struct {
		max       int64
		delivered int
		err       error
	}{
		{max: 3, delivered: 3, err: casestore.ErrScanBudget},
		{max: 9, delivered: 9, err: casestore.ErrScanBudget},
		{max: 10, delivered: 10, err: nil},
		{max: 11, delivered: 10, err: nil},
		{max: 0, delivered: 10, err: nil},                           // zero means the default budget
		{max: casestore.MaxScanEvents * 2, delivered: 10, err: nil}, // never raises the default
	} {
		got, err := collectEach(t, c, casestore.EventQuery{MaxScan: tc.max})
		if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) || len(got) != tc.delivered {
			t.Errorf("MaxScan %d: %d events, %v; want %d, %v", tc.max, len(got), err, tc.delivered, tc.err)
		}
	}
	// A time budget stops a slow scan. The first event takes longer than the
	// budget; a store may check the clock only every few hundred events, so the
	// case holds more events than that.
	slow := e.newCase("slow")
	seedEach(t, e, slow, 700)
	delivered := 0
	err := slow.Each(e.ctx, casestore.EventQuery{MaxScanTime: 10 * time.Millisecond}, func(evidence.Event) error {
		if delivered == 0 {
			time.Sleep(40 * time.Millisecond)
		}
		delivered++
		return nil
	})
	if !errors.Is(err, casestore.ErrScanBudget) || delivered == 0 || delivered >= 700 {
		t.Errorf("MaxScanTime 10ms on a slow scan: %d of 700 events, %v; want a partial scan and ErrScanBudget", delivered, err)
	}
}

func eachStops(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("stops")
	seedEach(t, e, c, 10)

	stop := errors.New("caller stops")
	calls := 0
	err := c.Each(e.ctx, casestore.EventQuery{}, func(evidence.Event) error {
		calls++
		if calls == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || calls != 3 {
		t.Errorf("an error from fn: %v after %d calls, want it returned after 3", err, calls)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	cancel()
	calls = 0
	err = c.Each(ctx, casestore.EventQuery{}, func(evidence.Event) error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Errorf("cancelled context: %v after %d calls", err, calls)
	}
	// Cancelling in the middle of a scan stops it.
	ctx, cancel = context.WithCancel(e.ctx)
	defer cancel()
	err = c.Each(ctx, casestore.EventQuery{}, func(evidence.Event) error { cancel(); return nil })
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("cancel during the scan: %v", err)
	}
	// A closed handle refuses.
	c2, err := e.store.Open(e.ctx, c.Info().ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.Close(); err != nil {
		t.Fatal(err)
	}
	err = c2.Each(e.ctx, casestore.EventQuery{}, func(evidence.Event) error { t.Error("fn called on a closed handle"); return nil })
	if !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("Each on a closed handle: %v", err)
	}
	// An empty case is not an error.
	empty := e.newCase("empty")
	if got, err := collectEach(t, empty, casestore.EventQuery{}); err != nil || len(got) != 0 {
		t.Errorf("empty case: %d events, %v", len(got), err)
	}
}

// eachEraseRace: an Erase that arrives while a scan is running either waits for
// the scan or stops it. The scan never panics, never reports success after the
// case is gone, and the case is unreadable afterwards.
func eachEraseRace(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("erase during each")
	id := c.Info().ID
	// More events than the handle is polled for, so a scan that outlives the
	// erase has a chance to notice.
	seedEach(t, e, c, 2000)

	started, release := make(chan struct{}), make(chan struct{})
	var once, relOnce sync.Once
	rel := func() { relOnce.Do(func() { close(release) }) }
	defer rel()
	delivered := 0
	done := make(chan error, 1)
	go func() {
		done <- c.Each(e.ctx, casestore.EventQuery{}, func(evidence.Event) error {
			delivered++
			once.Do(func() { close(started) })
			<-release
			return nil
		})
	}()
	waitFor(t, started, "the scan to start")
	erased := make(chan error, 1)
	go func() { erased <- e.store.Erase(e.ctx, id) }()
	eraseFirst := false
	select {
	case err := <-erased:
		if err != nil {
			t.Errorf("Erase: %v", err)
		}
		eraseFirst = true
	case <-time.After(200 * time.Millisecond): // the store made Erase wait; fine
	}
	rel()
	var eachErr error
	select {
	case eachErr = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Each did not return after the erase")
	}
	if !eraseFirst {
		select {
		case err := <-erased:
			if err != nil {
				t.Errorf("Erase: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Erase did not return")
		}
	}
	if eraseFirst && !errors.Is(eachErr, casestore.ErrNotFound) {
		t.Errorf("Each after the case was erased: %v after %d events, want ErrNotFound", eachErr, delivered)
	}
	if eachErr != nil && !errors.Is(eachErr, casestore.ErrNotFound) {
		t.Errorf("Each: %v", eachErr)
	}
	if _, err := e.store.Open(e.ctx, id); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Open after erase: %v", err)
	}
	err := c.Each(e.ctx, casestore.EventQuery{}, func(evidence.Event) error { t.Error("fn called after the erase"); return nil })
	if !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Each after erase: %v, want ErrNotFound", err)
	}
}

func suppliedRoundTrip(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("supplied")
	supplied := map[string]string{"slowms": "100", "log-time": "utc", "pattern": "%d [%t] %-5p %c - %m%n"}
	m := meta()
	m.Supplied = supplied
	res, err := c.Import(e.ctx, m, strings.NewReader("one"), emitAll(ev(c, "1", "k", "h", 0)))
	if err != nil {
		t.Fatal(err)
	}
	same := func(where string, got map[string]string) {
		t.Helper()
		if len(got) != len(supplied) {
			t.Errorf("%s: %d settings, want %d", where, len(got), len(supplied))
		}
		for k, v := range supplied {
			if got[k] != v {
				t.Errorf("%s: setting %q is not what was supplied", where, k)
			}
		}
	}
	same("record from Import", res.Record.Supplied)
	recs, err := c.Imports(e.ctx)
	if err != nil || len(recs) != 1 {
		t.Fatalf("Imports: %d, %v", len(recs), err)
	}
	same("Imports", recs[0].Supplied)

	// The caller's map and the returned maps are separate copies.
	supplied["slowms"] = "999"
	res.Record.Supplied["slowms"] = "888"
	recs[0].Supplied["slowms"] = "777"
	supplied["slowms"] = "100"
	again, _ := c.Imports(e.ctx)
	same("Imports after edits to copies", again[0].Supplied)

	// Another handle sees the same record.
	c2, err := e.store.Open(e.ctx, c.Info().ID)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	recs2, _ := c2.Imports(e.ctx)
	same("second handle", recs2[0].Supplied)

	// An import without settings records none.
	res2, err := c.Import(e.ctx, meta(), strings.NewReader("two"), emitAll(ev(c, "2", "k", "h", time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Record.Supplied) != 0 {
		t.Errorf("an import without settings records %d", len(res2.Record.Supplied))
	}
	all, _ := c.Imports(e.ctx)
	if len(all) != 2 || len(all[0].Supplied)+len(all[1].Supplied) != len(supplied) {
		t.Errorf("settings are not kept per import: %+v", all)
	}
}

func suppliedRefused(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("supplied refused")
	many := map[string]string{}
	for i := 0; i <= importer.MaxSuppliedKeys; i++ {
		many[fmt.Sprintf("k%d", i)] = "1"
	}
	for name, bad := range map[string]map[string]string{
		"too many keys":    many,
		"upper-case key":   {"Slowms": "1"},
		"empty key":        {"": "1"},
		"key with a space": {"slow ms": "1"},
		"empty value":      {"slowms": ""},
		"long value":       {"pattern": strings.Repeat("x", importer.MaxSuppliedValue+1)},
		"control in value": {"pattern": "a\x00b"},
		"newline in value": {"pattern": "a\nb"},
		"bad utf-8":        {"pattern": "a\xffb"},
	} {
		m := meta()
		m.Supplied = bad
		_, err := c.Import(e.ctx, m, noRead{t}, emitAll(ev(c, "x", "k", "h", 0)))
		if !errors.Is(err, casestore.ErrBadSupplied) {
			t.Errorf("%s: Import returned %v, want ErrBadSupplied", name, err)
		}
	}
	if recs, _ := c.Imports(e.ctx); len(recs) != 0 {
		t.Errorf("%d imports recorded after refusals", len(recs))
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 0 {
		t.Errorf("%d events kept after refusals", len(evs))
	}
}
