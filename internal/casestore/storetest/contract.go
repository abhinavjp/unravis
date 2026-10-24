// Package storetest is the contract test every casestore.Store must pass: the
// in-memory fake (F0) and the SQLite store (CS card) run the same list.
//
//	func TestContract(t *testing.T) {
//		storetest.Run(t, func(t testing.TB, caps casestore.Caps, now func() time.Time) casestore.Store { ... })
//	}
package storetest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Factory builds a fresh, empty store with the given caps. now is the store's
// clock; the test moves it to age cases.
type Factory func(t testing.TB, caps casestore.Caps, now func() time.Time) casestore.Store

// Ack is the gating acknowledgement text the tests send.
const Ack = "I have removed anything I am not allowed to share."

type clockT struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clockT) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clockT) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var t0 = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

type env struct {
	t     *testing.T
	ctx   context.Context
	clock *clockT
	store casestore.Store
}

func newEnv(t *testing.T, f Factory, caps casestore.Caps) *env {
	t.Helper()
	c := &clockT{t: t0}
	return &env{t: t, ctx: context.Background(), clock: c, store: f(t, caps, c.now)}
}

func (e *env) newCase(title string) casestore.Case {
	e.t.Helper()
	info, err := e.store.Create(e.ctx, title)
	if err != nil {
		e.t.Fatalf("Create: %v", err)
	}
	c, err := e.store.Open(e.ctx, info.ID)
	if err != nil {
		e.t.Fatalf("Open: %v", err)
	}
	e.t.Cleanup(func() { _ = c.Close() })
	return c
}

func ev(id, kind, host string, at time.Duration) evidence.Event {
	d := 5.0
	return evidence.Event{
		EvidenceID: id, Source: "contract", Kind: kind, Host: host,
		Start: t0.Add(at), End: t0.Add(at + time.Millisecond), DurationMs: &d, Unit: "ms",
		Line: 2, TZRule: "utc-field", Attrs: map[string]string{"a": "b"},
	}
}

func meta() casestore.ImportMeta {
	return casestore.ImportMeta{Importer: "contract", FileName: "f.txt", HostLabel: "web-a", TZRule: "utc-field", AckGating: true, AckText: Ack}
}

// emitAll is a RunFunc that reads the whole body and emits events.
func emitAll(events ...evidence.Event) casestore.RunFunc {
	return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "contract"}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for _, e := range events {
			rep.Rows++
			if err := emit(e); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		rep.KnownGaps = []string{"a fixed sentence"}
		rep.Rejected = 1
		return rep, nil
	}
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type noRead struct{ t *testing.T }

func (n noRead) Read([]byte) (int, error) {
	n.t.Error("body read although the import was refused")
	return 0, io.EOF
}

// Run executes the contract as subtests of t.
func Run(t *testing.T, f Factory) {
	caps := casestore.DefaultCaps
	t.Run("ids-and-titles", func(t *testing.T) { ids(t, f, caps) })
	t.Run("create-list-open", func(t *testing.T) { createListOpen(t, f, caps) })
	t.Run("ack-required", func(t *testing.T) { ack(t, f, caps) })
	t.Run("import-ok", func(t *testing.T) { importOK(t, f, caps) })
	t.Run("import-is-atomic", func(t *testing.T) { atomic1(t, f, caps) })
	t.Run("duplicate-sha", func(t *testing.T) { duplicate(t, f, caps) })
	t.Run("duplicate-evidence-id", func(t *testing.T) { dupEvent(t, f, caps) })
	t.Run("events-query", func(t *testing.T) { events(t, f, caps) })
	t.Run("pseudonymize", func(t *testing.T) { pseudo(t, f, caps) })
	t.Run("close-sweep-erase", func(t *testing.T) { lifecycle(t, f, caps) })
	t.Run("caps", func(t *testing.T) { capsTest(t, f) })
	t.Run("imports-serialised", func(t *testing.T) { serial(t, f, caps) })
	t.Run("handles", func(t *testing.T) { handles(t, f, caps) })
	t.Run("cancelled-context", func(t *testing.T) { cancelled(t, f, caps) })
}

func ids(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	info, err := e.store.Create(e.ctx, "Case one")
	if err != nil || !info.ID.Valid() || info.Title != "Case one" || !info.Created.Equal(t0) || info.Closed != nil || info.Bytes != 0 || info.Imports != 0 {
		t.Fatalf("Create: %+v %v", info, err)
	}
	other, _ := e.store.Create(e.ctx, "Case two")
	if other.ID == info.ID {
		t.Error("two cases share an id")
	}
	for _, bad := range []string{"", "../etc/passwd", strings.Repeat("a", 25), strings.Repeat("a", 27), strings.Repeat("A", 26), strings.Repeat("1", 26), strings.Repeat("a", 25) + "/"} {
		if _, err := e.store.Open(e.ctx, casestore.CaseID(bad)); !errors.Is(err, casestore.ErrBadID) {
			t.Errorf("Open(%q): %v, want ErrBadID", bad, err)
		}
		if err := e.store.Erase(e.ctx, casestore.CaseID(bad)); !errors.Is(err, casestore.ErrBadID) {
			t.Errorf("Erase(%q): %v, want ErrBadID", bad, err)
		}
		if err := e.store.CloseCase(e.ctx, casestore.CaseID(bad)); !errors.Is(err, casestore.ErrBadID) {
			t.Errorf("CloseCase(%q): %v, want ErrBadID", bad, err)
		}
	}
	for _, title := range []string{"", "   ", strings.Repeat("x", casestore.MaxTitleBytes+1), "a\nb", "bad\xffutf8"} {
		if _, err := e.store.Create(e.ctx, title); !errors.Is(err, casestore.ErrBadTitle) {
			t.Errorf("Create(%q): %v, want ErrBadTitle", title, err)
		}
	}
}

func createListOpen(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	if l, err := e.store.List(e.ctx); err != nil || len(l) != 0 {
		t.Fatalf("empty List: %v %v", l, err)
	}
	a, _ := e.store.Create(e.ctx, "A")
	e.clock.add(time.Hour)
	b, _ := e.store.Create(e.ctx, "B")
	l, err := e.store.List(e.ctx)
	if err != nil || len(l) != 2 || l[0].ID != a.ID || l[1].ID != b.ID {
		t.Fatalf("List: %+v %v", l, err)
	}
	c, err := e.store.Open(e.ctx, b.ID)
	if err != nil || c.Info().ID != b.ID || c.Info().Title != "B" {
		t.Fatalf("Open: %v", err)
	}
	if _, err := e.store.Open(e.ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Open unknown: %v", err)
	}
	if err := e.store.CloseCase(e.ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("CloseCase unknown: %v", err)
	}
}

func ack(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("ack")
	for name, m := range map[string]casestore.ImportMeta{
		"not ticked": func() casestore.ImportMeta { m := meta(); m.AckGating = false; return m }(),
		"no text":    func() casestore.ImportMeta { m := meta(); m.AckText = ""; return m }(),
	} {
		_, err := c.Import(e.ctx, m, noRead{t}, emitAll(ev("ev:x:1", "k", "h", 0)))
		if !errors.Is(err, casestore.ErrAckRequired) {
			t.Errorf("%s: %v, want ErrAckRequired", name, err)
		}
	}
	if imps, _ := c.Imports(e.ctx); len(imps) != 0 {
		t.Errorf("%d imports after refused acknowledgements", len(imps))
	}
}

func importOK(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("ok")
	e.clock.add(time.Minute)
	body := "hello world\n"
	events := []evidence.Event{ev("ev:one", "request", "web-a", 2*time.Second), ev("ev:two", "request", "web-a", time.Second)}
	res, err := c.Import(e.ctx, meta(), strings.NewReader(body), emitAll(events...))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	r := res.Record
	if r.ID < 1 || r.Status != casestore.StatusOK || r.SHA256 != sum(body) || r.Bytes != int64(len(body)) ||
		r.Rows != 2 || r.Rejected != 1 || r.Importer != "contract" || r.FileName != "f.txt" || r.HostLabel != "web-a" ||
		r.TZRule != "utc-field" || len(r.KnownGaps) != 1 || !r.Imported.Equal(t0.Add(time.Minute)) {
		t.Errorf("record %+v", r)
	}
	if res.Report.Rows != 2 || res.Report.Importer != "contract" {
		t.Errorf("report %+v", res.Report)
	}
	info := c.Info()
	if info.Imports != 1 || info.Bytes != int64(len(body)) {
		t.Errorf("info %+v", info)
	}
	imps, err := c.Imports(e.ctx)
	if err != nil || len(imps) != 1 || imps[0].SHA256 != r.SHA256 || imps[0].ID != r.ID {
		t.Errorf("Imports: %+v %v", imps, err)
	}
	got, err := c.Events(e.ctx, casestore.EventQuery{})
	if err != nil || len(got) != 2 || got[0].EvidenceID != "ev:two" || got[1].EvidenceID != "ev:one" {
		t.Fatalf("Events: %v %v", got, err)
	}
	if got[0].Attrs["a"] != "b" || got[0].DurationMs == nil || *got[0].DurationMs != 5 || !got[0].Start.Equal(t0.Add(time.Second)) || got[0].Start.Location() != time.UTC {
		t.Errorf("event not stored faithfully: %+v", got[0])
	}
	// A second case is separate.
	other := e.newCase("other")
	if evs, _ := other.Events(e.ctx, casestore.EventQuery{}); len(evs) != 0 {
		t.Errorf("events leaked across cases")
	}
	// The same bytes in another case are not a duplicate.
	if _, err := other.Import(e.ctx, meta(), strings.NewReader(body), emitAll(events...)); err != nil {
		t.Errorf("same file, other case: %v", err)
	}
}

func atomic1(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("atomic")
	boom := errors.New("importer failed")
	failing := func(err error) casestore.RunFunc {
		return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			_, _ = io.Copy(io.Discard, r)
			if err := emit(ev("ev:partial", "k", "h", 0)); err != nil {
				return importer.Report{}, err
			}
			return importer.Report{Rows: 1}, err
		}
	}
	res, err := c.Import(e.ctx, meta(), strings.NewReader("one"), failing(boom))
	if !errors.Is(err, boom) || res.Record.Status == casestore.StatusOK {
		t.Errorf("run error: %v %+v", err, res.Record)
	}
	res, err = c.Import(e.ctx, meta(), strings.NewReader("two"), failing(&importer.QuarantineError{Reason: importer.ReasonArchive}))
	if !errors.Is(err, importer.ErrQuarantine) || res.Record.Status != casestore.StatusQuarantined || res.Record.Reason != importer.ReasonArchive {
		t.Errorf("quarantine: %v %+v", err, res.Record)
	}
	_, err = c.Import(e.ctx, meta(), strings.NewReader("three"), failing(&importer.LimitError{Limit: "rows", Max: 1, At: 2}))
	if !errors.Is(err, importer.ErrLimit) {
		t.Errorf("limit: %v", err)
	}
	// A read error from the body aborts the import.
	_, err = c.Import(e.ctx, meta(), io.MultiReader(strings.NewReader("x"), errReader{boom}), emitAll(ev("ev:a", "k", "h", 0)))
	if !errors.Is(err, boom) {
		t.Errorf("body error: %v", err)
	}
	if imps, _ := c.Imports(e.ctx); len(imps) != 0 {
		t.Errorf("imports after failures: %+v", imps)
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 0 {
		t.Errorf("%d events survive a failed import", len(evs))
	}
	if info := c.Info(); info.Bytes != 0 || info.Imports != 0 {
		t.Errorf("info changed by failed imports: %+v", info)
	}
	// After the failures the same bytes import cleanly.
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("one"), emitAll(ev("ev:partial", "k", "h", 0))); err != nil {
		t.Errorf("retry after failure: %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func duplicate(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("dup")
	first, err := c.Import(e.ctx, meta(), strings.NewReader("same bytes"), emitAll(ev("ev:1", "k", "h", 0)))
	if err != nil {
		t.Fatal(err)
	}
	m := meta()
	m.FileName = "renamed.txt"
	// The second import reads only one byte; the hash still covers the file.
	partial := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		_, err := io.ReadFull(r, make([]byte, 1))
		return importer.Report{Rows: 1}, err
	}
	res, err := c.Import(e.ctx, m, strings.NewReader("same bytes"), partial)
	var de *casestore.DuplicateError
	if !errors.Is(err, casestore.ErrDuplicate) || !errors.As(err, &de) {
		t.Fatalf("second import: %v", err)
	}
	if de.Existing.ID != first.Record.ID || de.Existing.FileName != "f.txt" || res.Record.Status != casestore.StatusDuplicate || res.Record.ID != first.Record.ID {
		t.Errorf("duplicate details: %+v / %+v", de.Existing, res.Record)
	}
	if imps, _ := c.Imports(e.ctx); len(imps) != 1 || c.Info().Imports != 1 || c.Info().Bytes != int64(len("same bytes")) {
		t.Errorf("duplicate changed the case: %+v %+v", imps, c.Info())
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("other bytes"), emitAll(ev("ev:2", "k", "h", 0))); err != nil {
		t.Errorf("different bytes: %v", err)
	}
}

func dupEvent(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("dupev")
	_, err := c.Import(e.ctx, meta(), strings.NewReader("a"), emitAll(ev("ev:1", "k", "h", 0), ev("ev:1", "k", "h", 1)))
	if !errors.Is(err, casestore.ErrDuplicateEvent) {
		t.Errorf("same id twice in a file: %v", err)
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("b"), emitAll(ev("ev:1", "k", "h", 0))); err != nil {
		t.Fatal(err)
	}
	_, err = c.Import(e.ctx, meta(), strings.NewReader("c"), emitAll(ev("ev:9", "k", "h", 0), ev("ev:1", "k", "h", 1)))
	if !errors.Is(err, casestore.ErrDuplicateEvent) {
		t.Errorf("id already in the case: %v", err)
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 1 {
		t.Errorf("%d events after refused imports, want 1", len(evs))
	}
}

func events(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("events")
	mk := func(id, kind, source, host string, at time.Duration) evidence.Event {
		x := ev(id, kind, host, at)
		x.Source = source
		return x
	}
	all := []evidence.Event{
		mk("ev:5", "request", "iis", "web-a", 5*time.Second),
		mk("ev:1", "request", "iis", "web-b", time.Second),
		mk("ev:3", "slow-op", "mongo", "db-a", 3*time.Second),
		mk("ev:2b", "request", "iis", "web-a", 2*time.Second),
		mk("ev:2a", "counter", "pdh", "web-a", 2*time.Second),
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("x"), emitAll(all...)); err != nil {
		t.Fatal(err)
	}
	ids := func(q casestore.EventQuery) string {
		got, err := c.Events(e.ctx, q)
		if err != nil {
			t.Fatalf("Events(%+v): %v", q, err)
		}
		var s []string
		for _, x := range got {
			s = append(s, x.EvidenceID)
		}
		return strings.Join(s, ",")
	}
	if got := ids(casestore.EventQuery{}); got != "ev:1,ev:2a,ev:2b,ev:3,ev:5" {
		t.Errorf("order: %s", got)
	}
	w := importer.Window{From: t0.Add(2 * time.Second), To: t0.Add(3 * time.Second)}
	if got := ids(casestore.EventQuery{Window: w}); got != "ev:2a,ev:2b,ev:3" {
		t.Errorf("window inclusive: %s", got)
	}
	if got := ids(casestore.EventQuery{Window: importer.Window{From: t0.Add(4 * time.Second)}}); got != "ev:5" {
		t.Errorf("open-ended window: %s", got)
	}
	if got := ids(casestore.EventQuery{Kinds: []string{"counter", "slow-op"}}); got != "ev:2a,ev:3" {
		t.Errorf("kinds: %s", got)
	}
	if got := ids(casestore.EventQuery{Sources: []string{"iis"}, Hosts: []string{"web-a"}}); got != "ev:2b,ev:5" {
		t.Errorf("sources and hosts: %s", got)
	}
	if got := ids(casestore.EventQuery{Hosts: []string{"nobody"}}); got != "" {
		t.Errorf("no match: %s", got)
	}
	if got := ids(casestore.EventQuery{Limit: 2}); got != "ev:1,ev:2a" {
		t.Errorf("limit: %s", got)
	}
	if got := ids(casestore.EventQuery{Limit: casestore.MaxEventQuery + 10}); got != "ev:1,ev:2a,ev:2b,ev:3,ev:5" {
		t.Errorf("limit over the maximum is clamped: %s", got)
	}
	// Returned events are copies.
	got, _ := c.Events(e.ctx, casestore.EventQuery{Limit: 1})
	got[0].Attrs["a"] = "changed"
	*got[0].DurationMs = 99
	again, _ := c.Events(e.ctx, casestore.EventQuery{Limit: 1})
	if again[0].Attrs["a"] != "b" || *again[0].DurationMs != 5 {
		t.Error("Events returns the store's own memory")
	}
	// And so are the events the importer handed over.
	src := ev("ev:copy", "k", "h", 0)
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("y"), emitAll(src)); err != nil {
		t.Fatal(err)
	}
	src.Attrs["a"] = "mutated after import"
	for _, x := range func() []evidence.Event { g, _ := c.Events(e.ctx, casestore.EventQuery{Kinds: []string{"k"}}); return g }() {
		if x.Attrs["a"] != "b" {
			t.Error("store aliases the importer's attribute map")
		}
	}
}

func pseudo(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a, b := e.newCase("a"), e.newCase("b")
	u := a.Pseudonymize("user", "alice")
	if !strings.HasPrefix(u, "user-") || len(u) < len("user-")+4 || strings.Contains(u, "alice") {
		t.Errorf("label %q", u)
	}
	if a.Pseudonymize("user", "alice") != u {
		t.Error("not stable within a case")
	}
	if a.Pseudonymize("user", "bob") == u {
		t.Error("different values collide")
	}
	if c := a.Pseudonymize("client", "alice"); c == u || !strings.HasPrefix(c, "client-") {
		t.Errorf("kind not part of the label: %q", c)
	}
	if b.Pseudonymize("user", "alice") == u {
		t.Error("two cases share a pseudonym key")
	}
	if a.Pseudonymize("user", "") != "" {
		t.Error("empty value should stay empty")
	}
}

func lifecycle(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	keep := e.newCase("keep")
	closed := e.newCase("closed")
	old := closed.Info().ID
	if _, err := closed.Import(e.ctx, meta(), strings.NewReader("data"), emitAll(ev("ev:1", "k", "h", 0))); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CloseCase(e.ctx, old); err != nil {
		t.Fatal(err)
	}
	info := func(id casestore.CaseID) casestore.CaseInfo {
		l, _ := e.store.List(e.ctx)
		for _, i := range l {
			if i.ID == id {
				return i
			}
		}
		t.Fatalf("case %s not listed", id)
		return casestore.CaseInfo{}
	}
	closedAt := info(old).Closed
	if closedAt == nil || !closedAt.Equal(t0) {
		t.Fatalf("Closed = %v", closedAt)
	}
	e.clock.add(time.Hour)
	_ = e.store.CloseCase(e.ctx, old) // second close keeps the first time
	if c := info(old).Closed; c == nil || !c.Equal(t0) {
		t.Errorf("second close moved the clock: %v", c)
	}
	if info(keep.Info().ID).Closed != nil {
		t.Error("open case shows Closed")
	}
	// Exactly 30 days after closing is kept; one second more is erased.
	if gone, err := e.store.Sweep(e.ctx, t0.Add(casestore.Retention)); err != nil || len(gone) != 0 {
		t.Errorf("sweep at 30d: %v %v", gone, err)
	}
	gone, err := e.store.Sweep(e.ctx, t0.Add(casestore.Retention+time.Second))
	if err != nil || len(gone) != 1 || gone[0] != old {
		t.Fatalf("sweep after 30d: %v %v", gone, err)
	}
	if _, err := e.store.Open(e.ctx, old); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Open after sweep: %v", err)
	}
	if _, err := closed.Events(e.ctx, casestore.EventQuery{}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("open handle after erase: %v", err)
	}
	if _, err := closed.Import(e.ctx, meta(), strings.NewReader("more"), emitAll(ev("ev:2", "k", "h", 0))); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("import into erased case: %v", err)
	}
	if l, _ := e.store.List(e.ctx); len(l) != 1 || l[0].ID != keep.Info().ID {
		t.Errorf("List after sweep: %+v", l)
	}
	// Erase is idempotent and also removes open cases.
	for i := 0; i < 2; i++ {
		if err := e.store.Erase(e.ctx, old); err != nil {
			t.Errorf("Erase #%d: %v", i, err)
		}
	}
	if err := e.store.Erase(e.ctx, "bbbbbbbbbbbbbbbbbbbbbbbbbb"); err != nil {
		t.Errorf("Erase unknown: %v", err)
	}
	if err := e.store.Erase(e.ctx, keep.Info().ID); err != nil {
		t.Errorf("Erase open case: %v", err)
	}
	if l, _ := e.store.List(e.ctx); len(l) != 0 {
		t.Errorf("List after erase: %+v", l)
	}
}

func capsTest(t *testing.T, f Factory) {
	caps := casestore.Caps{MaxFileBytes: 10, MaxFileRows: 3, MaxCaseBytes: 15}
	e := newEnv(t, f, caps)
	c := e.newCase("caps")
	unchanged := func(what string, bytes int64, imports int) {
		t.Helper()
		if info := c.Info(); info.Bytes != bytes || info.Imports != imports {
			t.Errorf("%s: case changed: %+v", what, info)
		}
	}
	one := ev("ev:1", "k", "h", 0)
	// File bytes: exactly the cap is fine, one over is refused.
	_, err := c.Import(e.ctx, meta(), strings.NewReader(strings.Repeat("a", 11)), emitAll(one))
	if !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("file bytes: %v", err)
	}
	unchanged("file bytes", 0, 0)
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 0 {
		t.Error("events kept after a cap refusal")
	}
	// A parser that stops reading early cannot dodge the cap.
	early := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		_, err := io.ReadFull(r, make([]byte, 1))
		return importer.Report{Rows: 1}, err
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader(strings.Repeat("b", 11)), early); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("early stop over the file cap: %v", err)
	}
	unchanged("early stop", 0, 0)
	// Rows and events.
	many := []evidence.Event{ev("ev:1", "k", "h", 0), ev("ev:2", "k", "h", 1), ev("ev:3", "k", "h", 2), ev("ev:4", "k", "h", 3)}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("rows"), emitAll(many...)); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("rows: %v", err)
	}
	unchanged("rows", 0, 0)
	manyRows := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		_, _ = io.Copy(io.Discard, r)
		return importer.Report{Rows: 4}, nil
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("rows2"), manyRows); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("reported rows: %v", err)
	}
	unchanged("reported rows", 0, 0)
	// At the caps.
	if _, err := c.Import(e.ctx, meta(), strings.NewReader(strings.Repeat("c", 10)), emitAll(many[:3]...)); err != nil {
		t.Fatalf("exactly at the file caps: %v", err)
	}
	unchanged("at caps", 10, 1)
	// Case bytes: 10 used, 15 allowed.
	if _, err := c.Import(e.ctx, meta(), strings.NewReader(strings.Repeat("d", 6)), emitAll(ev("ev:9", "k", "h", 0))); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("case bytes: %v", err)
	}
	unchanged("case bytes", 10, 1)
	if _, err := c.Import(e.ctx, meta(), strings.NewReader(strings.Repeat("e", 5)), emitAll(ev("ev:9", "k", "h", 0))); err != nil {
		t.Errorf("exactly at the case cap: %v", err)
	}
	unchanged("at case cap", 15, 2)
}

func serial(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("serial")
	var running, peak int32
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
				cur := atomic.AddInt32(&running, 1)
				for {
					p := atomic.LoadInt32(&peak)
					if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				_, _ = io.Copy(io.Discard, r)
				err := emit(ev(fmt.Sprintf("ev:%d", i), "k", "h", time.Duration(i)*time.Second))
				atomic.AddInt32(&running, -1)
				return importer.Report{Rows: 1}, err
			}
			_, errs[i] = c.Import(e.ctx, meta(), bytes.NewReader([]byte(fmt.Sprintf("body %d", i))), run)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("import %d: %v", i, err)
		}
	}
	if peak != 1 {
		t.Errorf("%d imports ran at once, want 1", peak)
	}
	imps, _ := c.Imports(e.ctx)
	seen := map[int64]bool{}
	for _, r := range imps {
		seen[r.ID] = true
	}
	if len(imps) != n || len(seen) != n || c.Info().Imports != n {
		t.Errorf("%d imports, %d distinct ids", len(imps), len(seen))
	}
}

func handles(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	info, _ := e.store.Create(e.ctx, "handle")
	c, err := e.store.Open(e.ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := e.store.Open(e.ctx, info.ID)
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("x"), emitAll(ev("ev:1", "k", "h", 0))); err != nil {
		t.Fatal(err)
	}
	if got := c2.Info(); got.Imports != 1 {
		t.Errorf("second handle does not see the import: %+v", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Events(e.ctx, casestore.EventQuery{}); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("Events on a closed handle: %v", err)
	}
	if _, err := c.Imports(e.ctx); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("Imports on a closed handle: %v", err)
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("y"), emitAll()); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("Import on a closed handle: %v", err)
	}
	if _, err := c2.Events(e.ctx, casestore.EventQuery{}); err != nil {
		t.Errorf("other handle affected by Close: %v", err)
	}
	_ = c.Close() // closing twice is harmless
}

func cancelled(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("cancel")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.store.Create(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("Create: %v", err)
	}
	if _, err := e.store.List(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("List: %v", err)
	}
	if _, err := c.Import(ctx, meta(), strings.NewReader("x"), emitAll(ev("ev:1", "k", "h", 0))); !errors.Is(err, context.Canceled) {
		t.Errorf("Import: %v", err)
	}
	if _, err := c.Events(ctx, casestore.EventQuery{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Events: %v", err)
	}
	// Cancelled while the importer runs: nothing is kept.
	ctx2, cancel2 := context.WithCancel(context.Background())
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		_, _ = io.Copy(io.Discard, r)
		if err := emit(ev("ev:1", "k", "h", 0)); err != nil {
			return importer.Report{}, err
		}
		cancel2()
		return importer.Report{Rows: 1}, nil
	}
	if _, err := c.Import(ctx2, meta(), strings.NewReader("x"), run); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel during import: %v", err)
	}
	if imps, _ := c.Imports(e.ctx); len(imps) != 0 {
		t.Errorf("import kept after cancellation: %+v", imps)
	}
}
