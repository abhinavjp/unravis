package sqlite

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// diffValues are attribute values chosen to trip quoting, JSON typing and
// pattern handling in a filter pushed into SQL. All synthetic.
var diffValues = []string{
	"a", "b", "c", "1", "01", "1.0", "true", "null", "{}", "[1]", "0", "-0",
	"it's", `say "x"`, `back\slash`, "50%", "a_b", "%", "_", " lead", "trail ",
	"café", "中文", "\U0001F600", "SENTINEL-7f3a", "a.b", "a-b", "$.x",
	strings.Repeat("v", 200), strings.Repeat("é", 120),
}

var diffNames = []string{"counter", "metric", "instance", "object", "a", "a.b", "a-b", "x1", "Counter", "pid"}

var diffKinds = []string{"counter", "metric", "cpu", "wait"}

func diffAllowed() map[string]bool {
	m := map[string]bool{}
	for _, n := range diffNames {
		m[n] = true
	}
	return m
}

// diffEvents builds n events whose Value (off+i) is unique in the case: the key
// both stores are compared by, since evidence IDs differ between cases.
func diffEvents(c casestore.Case, rnd *rand.Rand, label string, off, n int, base time.Time) []evidence.Event {
	out := make([]evidence.Event, n)
	for i := range out {
		line := int64(i + 2)
		// Millisecond-aligned, with plenty of ties.
		start := base.Add(time.Duration(rnd.Intn(40)) * 250 * time.Millisecond)
		v := float64(off + i)
		e := evidence.Event{
			EvidenceID: evidence.EventID(string(c.Info().ID), testImporter, label, line, 0),
			Source:     testImporter, Kind: diffKinds[rnd.Intn(len(diffKinds))],
			Host:  []string{"web-a", "db-a"}[rnd.Intn(2)],
			Start: start, End: start.Add(time.Duration(rnd.Intn(3)) * time.Second), Value: &v, Unit: "count",
			Line: line, TZRule: "utc-field",
		}
		if k := rnd.Intn(8); k > 0 { // 0..7 attributes
			e.Attrs = map[string]string{}
			for j := 0; j < k; j++ {
				e.Attrs[diffNames[rnd.Intn(len(diffNames))]] = diffValues[rnd.Intn(len(diffValues))]
			}
		}
		out[i] = e
	}
	return out
}

func diffImport(t *testing.T, c casestore.Case, label string, evs []evidence.Event) {
	t.Helper()
	m := tMeta(label + ".csv")
	m.AllowedAttrs = diffAllowed()
	if _, err := c.Import(context.Background(), m, tBody(label), tRun(evs)); err != nil {
		t.Fatal(err)
	}
}

func diffQuery(rnd *rand.Rand, base time.Time, pool []evidence.Event) casestore.EventQuery {
	var q casestore.EventQuery
	switch rnd.Intn(4) {
	case 1:
		q.Kinds = []string{diffKinds[rnd.Intn(len(diffKinds))]}
	case 2:
		q.Kinds = []string{diffKinds[rnd.Intn(len(diffKinds))], "no-such-kind"}
	case 3:
		q.Kinds = []string{"no-such-kind"}
	}
	if rnd.Intn(3) == 0 {
		q.Hosts = []string{[]string{"web-a", "db-a", "web-z"}[rnd.Intn(3)]}
	}
	if rnd.Intn(3) == 0 {
		a := base.Add(time.Duration(rnd.Intn(12)) * time.Second)
		b := base.Add(time.Duration(rnd.Intn(12)) * time.Second) // may be before a: a reversed window
		q.Window = importer.Window{From: a, To: b}
		if rnd.Intn(4) == 0 {
			q.Window.To = time.Time{}
		}
	}
	if rnd.Intn(10) < 7 && len(pool) > 0 {
		q.Attrs = map[string]string{}
		for j := rnd.Intn(6); j > 0; j-- { // up to 5: five or more is a refusal
			name := diffNames[rnd.Intn(len(diffNames))]
			val := diffValues[rnd.Intn(len(diffValues))]
			if rnd.Intn(2) == 0 { // a pair some event really carries
				for k, v := range pool[rnd.Intn(len(pool))].Attrs {
					name, val = k, v
					break
				}
			}
			q.Attrs[name] = val
		}
	}
	if rnd.Intn(4) == 0 {
		q.Limit = rnd.Intn(30)
	}
	q.Unordered = rnd.Intn(2) == 0
	return q
}

// vals keys events by Value, which the fixture makes unique.
func vals(evs []evidence.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = fmt.Sprint(*e.Value)
	}
	return out
}

func diffCompare(t *testing.T, name string, q casestore.EventQuery, mem, sq casestore.Case) (matched int, refused bool) {
	t.Helper()
	ctx := context.Background()
	desc := fmt.Sprintf("%s query %+v", name, q)

	// Events: the same refusal, or the same events in time order.
	me, merr := mem.Events(ctx, q)
	se, serr := sq.Events(ctx, q)
	if (merr == nil) != (serr == nil) {
		t.Fatalf("%s: Events errors differ: memory %v, sqlite %v", desc, merr, serr)
	}
	if merr == nil {
		limit := q.Limit
		if limit <= 0 || limit > casestore.MaxEventQuery {
			limit = casestore.MaxEventQuery
		}
		if len(me) != len(se) {
			t.Fatalf("%s: Events memory %d events, sqlite %d", desc, len(me), len(se))
		}
		for i := 1; i < len(se); i++ {
			if se[i].Start.Before(se[i-1].Start) {
				t.Fatalf("%s: sqlite Events is out of time order at %d", desc, i)
			}
		}
		if len(me) < limit { // not cut by Limit: the same events, field for field
			if !sameSet(vals(me), vals(se)) {
				t.Fatalf("%s: Events sets differ", desc)
			}
			byVal := map[string]evidence.Event{}
			for _, e := range me {
				byVal[fmt.Sprint(*e.Value)] = e
			}
			for _, g := range se {
				w := byVal[fmt.Sprint(*g.Value)]
				if !w.Start.Equal(g.Start) || w.Kind != g.Kind || w.Host != g.Host || len(w.Attrs) != len(g.Attrs) {
					t.Fatalf("%s: event %v is not equal in both stores", desc, *g.Value)
				}
				for k, v := range w.Attrs {
					if g.Attrs[k] != v {
						t.Fatalf("%s: event %v attr %q is %q in sqlite, %q in memory", desc, *g.Value, k, g.Attrs[k], v)
					}
				}
			}
		} else { // cut by Limit: the same start times, tie-breaks aside
			for i := range me {
				if !me[i].Start.Equal(se[i].Start) {
					t.Fatalf("%s: limited Events differ in time at %d", desc, i)
				}
			}
		}
	}

	// Each: the same refusal, or the same set; ordered unless Unordered.
	each := func(c casestore.Case, q casestore.EventQuery, n *int) ([]evidence.Event, error) {
		var got []evidence.Event
		err := c.Each(ctx, q, func(e evidence.Event) error { got = append(got, e); *n++; return nil })
		return got, err
	}
	var mn, sn int
	meEach, merr := each(mem, q, &mn)
	seEach, serr := each(sq, q, &sn)
	if (merr == nil) != (serr == nil) {
		t.Fatalf("%s: Each errors differ: memory %v, sqlite %v", desc, merr, serr)
	}
	if merr != nil {
		return 0, true
	}
	defer func() { matched = len(meEach) }()
	if !sameSet(vals(meEach), vals(seEach)) {
		t.Fatalf("%s: Each sets differ: memory %d, sqlite %d", desc, len(meEach), len(seEach))
	}
	if !q.Unordered {
		for i := 1; i < len(seEach); i++ {
			p, c := seEach[i-1], seEach[i]
			if c.Kind < p.Kind || (c.Kind == p.Kind && c.Start.Before(p.Start)) {
				t.Fatalf("%s: sqlite Each is out of order at %d", desc, i)
			}
		}
	}
	// A tiny scan budget stops both after the same count: exactly max complete.
	if n := len(meEach); n > 1 {
		tq := q
		tq.MaxScan = int64(n - 1)
		mn, sn = 0, 0
		_, mErr := each(mem, tq, &mn)
		_, sErr := each(sq, tq, &sn)
		if !errors.Is(mErr, casestore.ErrScanBudget) || !errors.Is(sErr, casestore.ErrScanBudget) || mn != sn || sn != n-1 {
			t.Fatalf("%s: MaxScan %d: memory %d/%v, sqlite %d/%v", desc, tq.MaxScan, mn, mErr, sn, sErr)
		}
		tq.MaxScan = int64(n)
		if _, err := each(sq, tq, &sn); err != nil {
			t.Fatalf("%s: MaxScan equal to the matches must complete, got %v", desc, err)
		}
	}
	return
}

// The SQLite store answers every query the way the in-memory reference does:
// odd attribute names and values, five or more filters, unmatched selectors,
// reversed windows, an empty Kinds list with Unordered, Limit and tiny MaxScan.
func TestDifferentialAgainstMemoryStore(t *testing.T) {
	ctx := context.Background()
	refused, nonEmpty := 0, 0
	for seed := int64(1); seed <= 6; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		s, _ := newTestStore(t)
		sc := mustOpen(t, s, mustCreate(t, s, "diff").ID)
		mstore := casestore.NewMemory()
		minfo, err := mstore.Create(ctx, "diff")
		if err != nil {
			t.Fatal(err)
		}
		mc, err := mstore.Open(ctx, minfo.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = mc.Close() })

		// The same random stream builds each case's events, under its own IDs.
		for imp := 0; imp < 3; imp++ {
			label := fmt.Sprintf("d%d", imp)
			diffImport(t, sc, label, diffEvents(sc, rand.New(rand.NewSource(seed*100+int64(imp))), label, imp*1000, 80, testBase))
			diffImport(t, mc, label, diffEvents(mc, rand.New(rand.NewSource(seed*100+int64(imp))), label, imp*1000, 80, testBase))
		}
		pool, _ := sc.Events(ctx, casestore.EventQuery{})
		for i := 0; i < 150; i++ {
			m, r := diffCompare(t, fmt.Sprintf("seed %d #%d", seed, i), diffQuery(rnd, testBase, pool), mc, sc)
			if r {
				refused++
			} else if m > 0 {
				nonEmpty++
			}
		}
	}
	// The queries must exercise both outcomes, or the comparison proves nothing.
	if refused < 10 || nonEmpty < 200 {
		t.Fatalf("only %d refused and %d non-empty queries", refused, nonEmpty)
	}
}
