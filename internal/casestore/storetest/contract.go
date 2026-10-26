// Package storetest is the contract test every casestore.Store must pass: the
// in-memory fake (F0) and the SQLite store (CS card) run the same list.
//
//	func TestContract(t *testing.T) {
//		storetest.Run(t, func(t testing.TB, caps casestore.Caps, now func() time.Time) casestore.Store { ... })
//	}
//
// Events in the contract carry real evidence IDs (evidence.EventID for the
// case under test and the importer name "contract"), because a store refuses
// any other shape.
package storetest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
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

func cid(c casestore.Case) string { return string(c.Info().ID) }

// idOf is the evidence ID the contract gives the event named label in case c.
func idOf(c casestore.Case, label string) string {
	return evidence.EventID(cid(c), "contract", label, 2, 0)
}

// ev builds a valid event for case c. The same label in the same case gives the
// same ID, which is how the tests provoke duplicates.
func ev(c casestore.Case, label, kind, host string, at time.Duration) evidence.Event {
	d := 5.0
	return evidence.Event{
		EvidenceID: idOf(c, label), Source: "contract", Kind: kind, Host: host,
		Start: t0.Add(at), End: t0.Add(at + time.Millisecond), DurationMs: &d, Unit: "ms",
		Line: 2, TZRule: "utc-field", Attrs: map[string]string{"a": "b"},
	}
}

func meta() casestore.ImportMeta {
	return casestore.ImportMeta{
		Importer: "contract", FileName: "f.txt", HostLabel: "web-a", TZRule: "utc-field",
		AckGating: true, AckText: Ack, AllowedAttrs: map[string]bool{"a": true},
	}
}

// emitAll is a RunFunc that reads the whole body and emits events, stopping at
// the first emit error and returning it, as a correct importer does.
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

// swallowAll is the broken counterpart of emitAll: it ignores every error from
// emit and reports success. A store must still refuse the import.
func swallowAll(events ...evidence.Event) casestore.RunFunc {
	return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "contract"}
		_, _ = io.Copy(io.Discard, r)
		for _, e := range events {
			rep.Rows++
			if emit(e) == nil {
				rep.Emitted++
			}
		}
		return rep, nil
	}
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type noRead struct{ t *testing.T }

func (n noRead) Read([]byte) (int, error) {
	n.t.Error("body read although the import was refused")
	return 0, io.EOF
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// Run executes the contract as subtests of t.
func Run(t *testing.T, f Factory) {
	caps := casestore.DefaultCaps
	t.Run("ids-and-titles", func(t *testing.T) { ids(t, f, caps) })
	t.Run("create-list-open", func(t *testing.T) { createListOpen(t, f, caps) })
	t.Run("ack-required", func(t *testing.T) { ack(t, f, caps) })
	t.Run("import-ok", func(t *testing.T) { importOK(t, f, caps) })
	t.Run("import-is-atomic", func(t *testing.T) { atomic1(t, f, caps) })
	t.Run("quarantine", func(t *testing.T) { quarantine(t, f, caps) })
	t.Run("duplicate-sha", func(t *testing.T) { duplicate(t, f, caps) })
	t.Run("duplicate-evidence-id", func(t *testing.T) { dupEvent(t, f, caps) })
	t.Run("ignored-emit-error", func(t *testing.T) { ignoredEmitError(t, f, caps) })
	t.Run("event-provenance", func(t *testing.T) { provenance(t, f, caps) })
	t.Run("events-query", func(t *testing.T) { events(t, f, caps) })
	t.Run("pseudonymize", func(t *testing.T) { pseudo(t, f, caps) })
	t.Run("close-sweep-erase", func(t *testing.T) { lifecycle(t, f, caps) })
	t.Run("caps", func(t *testing.T) { capsTest(t, f) })
	t.Run("imports-serialised", func(t *testing.T) { serial(t, f, caps) })
	t.Run("same-bytes-race", func(t *testing.T) { sameBytesRace(t, f, caps) })
	t.Run("erase-during-import", func(t *testing.T) { eraseDuringImport(t, f, caps) })
	t.Run("reads-during-import", func(t *testing.T) { readsDuringImport(t, f, caps) })
	t.Run("handles", func(t *testing.T) { handles(t, f, caps) })
	t.Run("cancelled-context", func(t *testing.T) { cancelled(t, f, caps) })
	t.Run("failed-import-keeps-report", func(t *testing.T) { failedKeepsReport(t, f, caps) })
	t.Run("closed-case-refuses-import", func(t *testing.T) { closedRefusesImport(t, f, caps) })
	t.Run("ack-text-limit", func(t *testing.T) { ackLimit(t, f, caps) })
	t.Run("quarantine-column-is-canonical", func(t *testing.T) { quarantineColumn(t, f, caps) })
	t.Run("each-streams-matching-events", func(t *testing.T) { eachStreams(t, f, caps) })
	t.Run("each-attribute-filter", func(t *testing.T) { eachAttrs(t, f, caps) })
	t.Run("each-scan-budget", func(t *testing.T) { eachBudget(t, f, caps) })
	t.Run("each-stops-and-fails-cleanly", func(t *testing.T) { eachStops(t, f, caps) })
	t.Run("each-erase-race", func(t *testing.T) { eachEraseRace(t, f, caps) })
	t.Run("supplied-round-trip", func(t *testing.T) { suppliedRoundTrip(t, f, caps) })
	t.Run("supplied-refused", func(t *testing.T) { suppliedRefused(t, f, caps) })
	t.Run("discover-tables", func(t *testing.T) { discoverTables(t, f, caps) })
	t.Run("tags-and-environments", func(t *testing.T) { tagsContract(t, f, caps) })
	t.Run("ledger-blob", func(t *testing.T) { ledgerBlobContract(t, f, caps) })
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
	// Titles are shown back to the user: no control or invisible formatting
	// characters (newline, NUL, C1, bidi override, zero-width, line separator).
	for _, title := range []string{"", "   ", strings.Repeat("x", casestore.MaxTitleBytes+1), "a\nb", "bad\xffutf8", "a\x00b", "a\u0085b", "a‮b", "a​b", "a b"} {
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
		_, err := c.Import(e.ctx, m, noRead{t}, emitAll(ev(c, "x", "k", "h", 0)))
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
	events := []evidence.Event{ev(c, "one", "request", "web-a", 2*time.Second), ev(c, "two", "request", "web-a", time.Second)}
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
	if err != nil || len(got) != 2 || got[0].EvidenceID != idOf(c, "two") || got[1].EvidenceID != idOf(c, "one") {
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
	// The same bytes in another case are not a duplicate (the events carry the
	// other case's IDs).
	otherEvents := []evidence.Event{ev(other, "one", "request", "web-a", 2*time.Second), ev(other, "two", "request", "web-a", time.Second)}
	if _, err := other.Import(e.ctx, meta(), strings.NewReader(body), emitAll(otherEvents...)); err != nil {
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
			if err := emit(ev(c, "partial", "k", "h", 0)); err != nil {
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
	_, err = c.Import(e.ctx, meta(), io.MultiReader(strings.NewReader("x"), errReader{boom}), emitAll(ev(c, "a", "k", "h", 0)))
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
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("one"), emitAll(ev(c, "partial", "k", "h", 0))); err != nil {
		t.Errorf("retry after failure: %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// quarantine pins the plan's quarantine design (§1.5): a refused file leaves a
// QuarantineRecord (reason, file name, hash, rows seen), not an import, no
// events and no bytes against the case; the same bytes can be imported later.
func quarantine(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("quarantine")
	q, ok := c.(casestore.Quarantiner)
	if !ok {
		t.Fatal("the Case does not implement casestore.Quarantiner")
	}
	body := "refused bytes, read only in part"
	runFor := func(c casestore.Case) casestore.RunFunc {
		return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			_, _ = io.ReadFull(r, make([]byte, 3)) // stops early: the store hashes the rest
			if err := emit(ev(c, "q1", "k", "h", 0)); err != nil {
				return importer.Report{}, err
			}
			return importer.Report{Importer: "contract", Rows: 4},
				&importer.QuarantineError{Reason: importer.ReasonUnknownHost, Column: "host"}
		}
	}
	e.clock.add(time.Minute)
	res, err := c.Import(e.ctx, meta(), strings.NewReader(body), runFor(c))
	var qe *importer.QuarantineError
	if !errors.Is(err, importer.ErrQuarantine) || !errors.As(err, &qe) || qe.Reason != importer.ReasonUnknownHost {
		t.Fatalf("Import: %v", err)
	}
	r := res.Record
	if r.Status != casestore.StatusQuarantined || r.Reason != importer.ReasonUnknownHost || r.SHA256 != sum(body) || r.Bytes != int64(len(body)) || r.ID != 0 {
		t.Errorf("record %+v", r)
	}
	qs, err := q.Quarantines(e.ctx)
	if err != nil || len(qs) != 1 {
		t.Fatalf("Quarantines: %+v %v", qs, err)
	}
	g := qs[0]
	if g.ID < 1 || g.Importer != "contract" || g.FileName != "f.txt" || g.SHA256 != sum(body) || g.Reason != importer.ReasonUnknownHost ||
		g.Column != "host" || g.Rows != 4 || !g.At.Equal(t0.Add(time.Minute)) {
		t.Errorf("quarantine record %+v", g)
	}
	if imps, _ := c.Imports(e.ctx); len(imps) != 0 {
		t.Errorf("a quarantined file is listed as an import: %+v", imps)
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 0 {
		t.Errorf("%d events kept from a quarantined file", len(evs))
	}
	if info := c.Info(); info.Bytes != 0 || info.Imports != 0 {
		t.Errorf("a quarantined file changed the case: %+v", info)
	}
	// The same bytes import once the user fixes the cause.
	if _, err := c.Import(e.ctx, meta(), strings.NewReader(body), emitAll(ev(c, "q1", "k", "h", 0))); err != nil {
		t.Errorf("import of the same bytes after a quarantine: %v", err)
	}
	if qs, _ := q.Quarantines(e.ctx); len(qs) != 1 {
		t.Errorf("quarantine record lost or duplicated: %+v", qs)
	}
	// A file over the byte cap is refused as too big; it leaves no record.
	small := newEnv(t, f, casestore.Caps{MaxFileBytes: 10, MaxFileRows: 3, MaxCaseBytes: 100})
	sc := small.newCase("quarantine caps")
	if _, err := sc.Import(small.ctx, meta(), strings.NewReader(strings.Repeat("z", 11)), runFor(sc)); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("quarantine of an oversized file: %v, want ErrCapExceeded", err)
	}
	if qs, _ := sc.(casestore.Quarantiner).Quarantines(small.ctx); len(qs) != 0 {
		t.Errorf("record kept for a file over the cap: %+v", qs)
	}
	// Erase removes the records with the case.
	if err := e.store.Erase(e.ctx, c.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Quarantines(e.ctx); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Quarantines after erase: %v", err)
	}
}

func duplicate(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("dup")
	first, err := c.Import(e.ctx, meta(), strings.NewReader("same bytes"), emitAll(ev(c, "1", "k", "h", 0)))
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
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("other bytes"), emitAll(ev(c, "2", "k", "h", 0))); err != nil {
		t.Errorf("different bytes: %v", err)
	}
}

func dupEvent(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("dupev")
	_, err := c.Import(e.ctx, meta(), strings.NewReader("a"), emitAll(ev(c, "1", "k", "h", 0), ev(c, "1", "k", "h", 1)))
	if !errors.Is(err, casestore.ErrDuplicateEvent) {
		t.Errorf("same id twice in a file: %v", err)
	}
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("b"), emitAll(ev(c, "1", "k", "h", 0))); err != nil {
		t.Fatal(err)
	}
	_, err = c.Import(e.ctx, meta(), strings.NewReader("c"), emitAll(ev(c, "9", "k", "h", 0), ev(c, "1", "k", "h", 1)))
	if !errors.Is(err, casestore.ErrDuplicateEvent) {
		t.Errorf("id already in the case: %v", err)
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 1 {
		t.Errorf("%d events after refused imports, want 1", len(evs))
	}
}

// ignoredEmitError: a run that drops the error from emit and returns success
// must not produce an "ok" import. The store fails the import with the emit
// error and keeps nothing.
func ignoredEmitError(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("ignored")
	empty := func(what string) {
		t.Helper()
		if imps, _ := c.Imports(e.ctx); len(imps) != 0 {
			t.Errorf("%s: import recorded: %+v", what, imps)
		}
		if info := c.Info(); info.Bytes != 0 || info.Imports != 0 {
			t.Errorf("%s: case changed: %+v", what, info)
		}
	}
	_, err := c.Import(e.ctx, meta(), strings.NewReader("a"), swallowAll(ev(c, "1", "k", "h", 0), ev(c, "1", "k", "h", 1)))
	if !errors.Is(err, casestore.ErrDuplicateEvent) {
		t.Errorf("duplicate in the file, error ignored: %v", err)
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 0 {
		t.Errorf("%d events kept from an import that ignored a refusal", len(evs))
	}
	empty("duplicate in the file")
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("b"), emitAll(ev(c, "1", "k", "h", 0))); err != nil {
		t.Fatal(err)
	}
	_, err = c.Import(e.ctx, meta(), strings.NewReader("c"), swallowAll(ev(c, "9", "k", "h", 0), ev(c, "1", "k", "h", 1)))
	if !errors.Is(err, casestore.ErrDuplicateEvent) {
		t.Errorf("duplicate of the case, error ignored: %v", err)
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 1 {
		t.Errorf("%d events after the ignored refusal, want 1", len(evs))
	}
	if info := c.Info(); info.Imports != 1 {
		t.Errorf("case changed: %+v", info)
	}
	// A refused event first, a good one after: the file still fails.
	bad := ev(c, "bad", "k", "h", 0)
	bad.Line = 0
	_, err = c.Import(e.ctx, meta(), strings.NewReader("d"), swallowAll(bad, ev(c, "fine", "k", "h", 1)))
	if err == nil {
		t.Error("an invalid event followed by a good one, error ignored: import succeeded")
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 1 {
		t.Errorf("%d events after the ignored refusal, want 1", len(evs))
	}
	// Row cap.
	small := newEnv(t, f, casestore.Caps{MaxFileBytes: 100, MaxFileRows: 3, MaxCaseBytes: 1000})
	sc := small.newCase("ignored caps")
	many := []evidence.Event{ev(sc, "1", "k", "h", 0), ev(sc, "2", "k", "h", 1), ev(sc, "3", "k", "h", 2), ev(sc, "4", "k", "h", 3)}
	if _, err := sc.Import(small.ctx, meta(), strings.NewReader("rows"), swallowAll(many...)); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("row cap, error ignored: %v", err)
	}
	if imps, _ := sc.Imports(small.ctx); len(imps) != 0 {
		t.Errorf("import recorded after an ignored cap refusal: %+v", imps)
	}
	if evs, _ := sc.Events(small.ctx, casestore.EventQuery{}); len(evs) != 0 {
		t.Errorf("%d events kept after an ignored cap refusal", len(evs))
	}
}

// provenance: the store accepts only events whose ID is ev:<case8>:<importer>:<16hex>
// for this case and the importing importer, with Source equal to the importer,
// Line >= 1, a time-zone rule, and attributes on the allowlist. Each refusal
// holds whether the importer returns emit's error or drops it.
func provenance(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("prov")
	other := e.newCase("prov other")
	prefix := evidence.CasePrefixes(cid(c))[0] + "contract:"
	bad := map[string]func(x *evidence.Event){
		"another case's id":           func(x *evidence.Event) { x.EvidenceID = evidence.EventID(cid(other), "contract", "n", 2, 0) },
		"another importer's id":       func(x *evidence.Event) { x.EvidenceID = evidence.EventID(cid(c), "iis", "n", 2, 0) },
		"free-form id":                func(x *evidence.Event) { x.EvidenceID = "ev:x:1" },
		"short hash":                  func(x *evidence.Event) { x.EvidenceID = prefix + "abc" },
		"long hash":                   func(x *evidence.Event) { x.EvidenceID = prefix + "0123456789abcdef0" },
		"upper-case hash":             func(x *evidence.Event) { x.EvidenceID = prefix + "ABCDEF0123456789" },
		"join id in an event":         func(x *evidence.Event) { x.EvidenceID = strings.Replace(x.EvidenceID, "ev:", "jn:", 1) },
		"source is another importer":  func(x *evidence.Event) { x.Source = "iis" },
		"line 0":                      func(x *evidence.Event) { x.Line = 0 },
		"no time-zone rule":           func(x *evidence.Event) { x.TZRule = "" },
		"unknown time-zone rule":      func(x *evidence.Event) { x.TZRule = "whatever" },
		"attribute off the allowlist": func(x *evidence.Event) { x.Attrs = map[string]string{"zzz": "v"} },
	}
	names := make([]string, 0, len(bad))
	for n := range bad {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, swallow := range []bool{false, true} {
			x := ev(c, "p", "k", "h", 0)
			bad[name](&x)
			run := emitAll(x)
			if swallow {
				run = swallowAll(x)
			}
			if _, err := c.Import(e.ctx, meta(), strings.NewReader("body "+name), run); err == nil {
				t.Errorf("%s (error ignored: %v): import succeeded", name, swallow)
			}
		}
	}
	if imps, _ := c.Imports(e.ctx); len(imps) != 0 {
		t.Errorf("imports after refused events: %+v", imps)
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 0 {
		t.Errorf("%d refused events were stored", len(evs))
	}
	// With no allowlist given the store applies only the generic bounds.
	m := meta()
	m.AllowedAttrs = nil
	x := ev(c, "free", "k", "h", 0)
	x.Attrs = map[string]string{"zzz": "v"}
	if _, err := c.Import(e.ctx, m, strings.NewReader("no allowlist"), emitAll(x)); err != nil {
		t.Errorf("nil AllowedAttrs: %v", err)
	}
}

func events(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("events")
	mk := func(label, kind, source, host string, at time.Duration) evidence.Event {
		x := ev(c, label, kind, host, at)
		x.Source = source
		return x
	}
	// Other importers' names are in the Source here, so the import uses no
	// importer name and the store checks each event against its own Source.
	all := []evidence.Event{
		mk("5", "request", "iis", "web-a", 5*time.Second),
		mk("1", "request", "iis", "web-b", time.Second),
		mk("3", "slow-op", "mongo", "db-a", 3*time.Second),
		mk("2b", "request", "iis", "web-a", 2*time.Second),
		mk("2a", "counter", "pdh", "web-a", 2*time.Second),
	}
	// Re-ID each event for its own source so every ID is valid for the store.
	label := map[string]string{}
	for i, name := range []string{"5", "1", "3", "2b", "2a"} {
		all[i].EvidenceID = evidence.EventID(cid(c), all[i].Source, name, 2, 0)
		label[all[i].EvidenceID] = name
	}
	m := meta()
	m.Importer = ""
	if _, err := c.Import(e.ctx, m, strings.NewReader("x"), emitAll(all...)); err != nil {
		t.Fatal(err)
	}
	names := func(q casestore.EventQuery) string {
		got, err := c.Events(e.ctx, q)
		if err != nil {
			t.Fatalf("Events(%+v): %v", q, err)
		}
		var s []string
		for _, x := range got {
			s = append(s, label[x.EvidenceID])
		}
		return strings.Join(s, ",")
	}
	// 2a and 2b share a Start; they sort by EvidenceID.
	var a2, b2 string
	for _, x := range all {
		if label[x.EvidenceID] == "2a" {
			a2 = x.EvidenceID
		}
		if label[x.EvidenceID] == "2b" {
			b2 = x.EvidenceID
		}
	}
	tie := "2a,2b"
	if b2 < a2 {
		tie = "2b,2a"
	}
	if got := names(casestore.EventQuery{}); got != "1,"+tie+",3,5" {
		t.Errorf("order: %s", got)
	}
	w := importer.Window{From: t0.Add(2 * time.Second), To: t0.Add(3 * time.Second)}
	if got := names(casestore.EventQuery{Window: w}); got != tie+",3" {
		t.Errorf("window inclusive: %s", got)
	}
	if got := names(casestore.EventQuery{Window: importer.Window{From: t0.Add(4 * time.Second)}}); got != "5" {
		t.Errorf("open-ended window: %s", got)
	}
	if got := names(casestore.EventQuery{Kinds: []string{"counter", "slow-op"}}); got != "2a,3" {
		t.Errorf("kinds: %s", got)
	}
	if got := names(casestore.EventQuery{Sources: []string{"iis"}, Hosts: []string{"web-a"}}); got != "2b,5" {
		t.Errorf("sources and hosts: %s", got)
	}
	if got := names(casestore.EventQuery{Hosts: []string{"nobody"}}); got != "" {
		t.Errorf("no match: %s", got)
	}
	if got := names(casestore.EventQuery{Limit: 2}); got != "1,"+tie[:2] {
		t.Errorf("limit: %s", got)
	}
	if got := names(casestore.EventQuery{Limit: casestore.MaxEventQuery + 10}); got != "1,"+tie+",3,5" {
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
	src := ev(c, "copy", "k", "h", 0)
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
	later := ev(closed, "2", "k", "h", 0) // built before the case is erased
	if _, err := closed.Import(e.ctx, meta(), strings.NewReader("data"), emitAll(ev(closed, "1", "k", "h", 0))); err != nil {
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
	if _, err := closed.Import(e.ctx, meta(), strings.NewReader("more"), emitAll(later)); !errors.Is(err, casestore.ErrNotFound) {
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
	one := ev(c, "1", "k", "h", 0)
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
	many := []evidence.Event{ev(c, "1", "k", "h", 0), ev(c, "2", "k", "h", 1), ev(c, "3", "k", "h", 2), ev(c, "4", "k", "h", 3)}
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
	if _, err := c.Import(e.ctx, meta(), strings.NewReader(strings.Repeat("d", 6)), emitAll(ev(c, "9", "k", "h", 0))); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("case bytes: %v", err)
	}
	unchanged("case bytes", 10, 1)
	if _, err := c.Import(e.ctx, meta(), strings.NewReader(strings.Repeat("e", 5)), emitAll(ev(c, "9", "k", "h", 0))); err != nil {
		t.Errorf("exactly at the case cap: %v", err)
	}
	unchanged("at case cap", 15, 2)
}

// serial: imports into one case run one at a time, also when every caller has
// its own handle (a fresh Open per goroutine, as the web layer does).
func serial(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("serial")
	id := c.Info().ID
	var running, peak int32
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := e.store.Open(e.ctx, id)
			if err != nil {
				errs[i] = err
				return
			}
			defer h.Close()
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
				err := emit(ev(h, fmt.Sprintf("g%d", i), "k", "h", time.Duration(i)*time.Second))
				atomic.AddInt32(&running, -1)
				return importer.Report{Rows: 1}, err
			}
			_, errs[i] = h.Import(e.ctx, meta(), bytes.NewReader([]byte(fmt.Sprintf("body %d", i))), run)
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

// sameBytesRace: the same file imported by several handles at once is stored
// exactly once; the others get ErrDuplicate and leave no events.
func sameBytesRace(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("race")
	id := c.Info().ID
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := e.store.Open(e.ctx, id)
			if err != nil {
				errs[i] = err
				return
			}
			defer h.Close()
			_, errs[i] = h.Import(e.ctx, meta(), strings.NewReader("the very same bytes"), emitAll(ev(h, fmt.Sprintf("g%d", i), "k", "h", 0)))
		}(i)
	}
	wg.Wait()
	ok, dup := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, casestore.ErrDuplicate):
			dup++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || dup != n-1 {
		t.Errorf("%d imports stored and %d duplicates, want 1 and %d", ok, dup, n-1)
	}
	if imps, _ := c.Imports(e.ctx); len(imps) != 1 {
		t.Errorf("%d imports, want 1", len(imps))
	}
	if evs, _ := c.Events(e.ctx, casestore.EventQuery{}); len(evs) != 1 {
		t.Errorf("%d events, want only the winner's 1", len(evs))
	}
	if info := c.Info(); info.Imports != 1 || info.Bytes != int64(len("the very same bytes")) {
		t.Errorf("info %+v", info)
	}
}

// blocked returns a run that emits one event, signals started, and waits for
// release before it finishes.
func blocked(c casestore.Case, label string, started chan<- struct{}, release <-chan struct{}) casestore.RunFunc {
	return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		_, _ = io.Copy(io.Discard, r)
		if err := emit(ev(c, label, "k", "h", 0)); err != nil {
			return importer.Report{}, err
		}
		close(started)
		<-release
		return importer.Report{Rows: 1}, nil
	}
}

// eraseDuringImport: Erase while an import is running must leave nothing
// behind. A store may make Erase wait for the import, or erase at once and fail
// the import with ErrNotFound; it may not keep the case's rows.
func eraseDuringImport(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("erase during")
	id := c.Info().ID
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	defer rel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Import(e.ctx, meta(), strings.NewReader("body"), blocked(c, "a", started, release))
		done <- err
	}()
	waitFor(t, started, "the import to start")
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
	var impErr error
	select {
	case impErr = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Import did not return after the erase")
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
	if eraseFirst && !errors.Is(impErr, casestore.ErrNotFound) {
		t.Errorf("import after the case was erased: %v, want ErrNotFound", impErr)
	}
	if impErr != nil && !errors.Is(impErr, casestore.ErrNotFound) {
		t.Errorf("import: %v", impErr)
	}
	if _, err := e.store.Open(e.ctx, id); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Open after erase: %v", err)
	}
	if _, err := c.Events(e.ctx, casestore.EventQuery{}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Events after erase: %v", err)
	}
	if _, err := c.Imports(e.ctx); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("Imports after erase: %v", err)
	}
	if l, _ := e.store.List(e.ctx); len(l) != 0 {
		t.Errorf("List after erase: %+v", l)
	}
}

// readsDuringImport: while an import runs, readers (also through another
// handle) see none of its rows, and its bytes are not yet counted.
func readsDuringImport(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("reads during")
	c2, err := e.store.Open(e.ctx, c.Info().ID)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	defer rel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Import(e.ctx, meta(), strings.NewReader("body"), blocked(c, "a", started, release))
		done <- err
	}()
	waitFor(t, started, "the import to start")
	for name, h := range map[string]casestore.Case{"same handle": c, "other handle": c2} {
		if evs, err := h.Events(e.ctx, casestore.EventQuery{}); err != nil || len(evs) != 0 {
			t.Errorf("%s: Events during an import: %d, %v", name, len(evs), err)
		}
		if imps, err := h.Imports(e.ctx); err != nil || len(imps) != 0 {
			t.Errorf("%s: Imports during an import: %d, %v", name, len(imps), err)
		}
		if info := h.Info(); info.Imports != 0 || info.Bytes != 0 {
			t.Errorf("%s: Info during an import: %+v", name, info)
		}
	}
	rel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Import did not finish")
	}
	if evs, _ := c2.Events(e.ctx, casestore.EventQuery{}); len(evs) != 1 {
		t.Errorf("%d events after the import, want 1", len(evs))
	}
	if info := c2.Info(); info.Imports != 1 || info.Bytes != int64(len("body")) {
		t.Errorf("Info after the import: %+v", info)
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
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("x"), emitAll(ev(c, "1", "k", "h", 0))); err != nil {
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
	if _, err := c.Import(ctx, meta(), strings.NewReader("x"), emitAll(ev(c, "1", "k", "h", 0))); !errors.Is(err, context.Canceled) {
		t.Errorf("Import: %v", err)
	}
	if _, err := c.Events(ctx, casestore.EventQuery{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Events: %v", err)
	}
	// Cancelled while the importer runs: nothing is kept.
	ctx2, cancel2 := context.WithCancel(context.Background())
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		_, _ = io.Copy(io.Discard, r)
		if err := emit(ev(c, "1", "k", "h", 0)); err != nil {
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

// failedKeepsReport: whatever way an import fails after the importer ran, the
// ImportResult still carries the importer's Report (row and reject counts), so
// the caller can say what was read. Batch-2 M1.
func failedKeepsReport(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, casestore.Caps{MaxFileBytes: caps.MaxFileBytes, MaxFileRows: 3, MaxCaseBytes: caps.MaxCaseBytes})
	c := e.newCase("failed report")
	boom := errors.New("importer failed")
	cases := []struct {
		name string
		run  casestore.RunFunc
		want error
	}{
		{"importer error", func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			_, _ = io.Copy(io.Discard, r)
			return importer.Report{Importer: "contract", Rows: 7, Rejected: 2}, boom
		}, boom},
		{"rows over the file cap", func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			_, _ = io.Copy(io.Discard, r)
			return importer.Report{Importer: "contract", Rows: 7, Rejected: 2}, nil
		}, casestore.ErrCapExceeded},
		{"duplicate event", func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			_, _ = io.Copy(io.Discard, r)
			ev1 := ev(c, "same", "k", "h", 0)
			_ = emit(ev1)
			err := emit(ev1)
			return importer.Report{Importer: "contract", Rows: 7, Rejected: 2}, err
		}, casestore.ErrDuplicateEvent},
	}
	for i, tc := range cases {
		res, err := c.Import(e.ctx, meta(), strings.NewReader(fmt.Sprintf("body %d", i)), tc.run)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
			continue
		}
		if res.Report.Rows != 7 || res.Report.Rejected != 2 {
			t.Errorf("%s: report %+v lost on a failed import", tc.name, res.Report)
		}
	}
	if info := c.Info(); info.Imports != 0 || info.Bytes != 0 {
		t.Errorf("failed imports changed the case: %+v", info)
	}
}

// closedRefusesImport: a closed case takes no more files, through a handle
// opened before the close and one opened after it. Batch-2 L1.
func closedRefusesImport(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("closed")
	if _, err := c.Import(e.ctx, meta(), strings.NewReader("before"), emitAll(ev(c, "b", "k", "h", 0))); err != nil {
		t.Fatalf("import before close: %v", err)
	}
	if err := e.store.CloseCase(e.ctx, c.Info().ID); err != nil {
		t.Fatal(err)
	}
	c2, err := e.store.Open(e.ctx, c.Info().ID)
	if err != nil {
		t.Fatalf("Open of a closed case: %v", err)
	}
	defer c2.Close()
	for name, h := range map[string]casestore.Case{"handle opened before": c, "handle opened after": c2} {
		_, err := h.Import(e.ctx, meta(), strings.NewReader("after "+name), emitAll(ev(h, "a", "k", "h", time.Second)))
		if !errors.Is(err, casestore.ErrCaseClosed) {
			t.Errorf("%s: Import on a closed case = %v, want ErrCaseClosed", name, err)
		}
	}
	if imps, _ := c2.Imports(e.ctx); len(imps) != 1 {
		t.Errorf("%d imports after refused ones, want 1", len(imps))
	}
	if evs, _ := c2.Events(e.ctx, casestore.EventQuery{}); len(evs) != 1 {
		t.Errorf("%d events after refused imports, want 1", len(evs))
	}
}

// ackLimit: AckText is bounded by casestore.MaxAckText in every store, and the
// refusal is the one error casestore.ErrAckTooLong. Batch-2 L2.
func ackLimit(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("ack limit")
	m := meta()
	m.AckText = strings.Repeat("x", casestore.MaxAckText)
	if _, err := c.Import(e.ctx, m, strings.NewReader("at the limit"), emitAll(ev(c, "ok", "k", "h", 0))); err != nil {
		t.Errorf("AckText of exactly MaxAckText bytes: %v", err)
	}
	m.AckText = strings.Repeat("x", casestore.MaxAckText+1)
	if _, err := c.Import(e.ctx, m, noRead{t}, emitAll(ev(c, "over", "k", "h", 0))); !errors.Is(err, casestore.ErrAckTooLong) {
		t.Errorf("AckText over MaxAckText: %v, want ErrAckTooLong", err)
	}
}

// quarantineColumn: a stored quarantine Column is a canonical column name or
// empty, never raw header text, whatever the importer put in the error. The
// store is the last backstop (section 4 rule 9). Batch-2 M3.
func quarantineColumn(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	c := e.newCase("column")
	q := c.(casestore.Quarantiner)
	for i, tc := range []struct{ in, want string }{
		{"Wait_Type", "Wait_Type"},
		{"timestamp_utc", "timestamp_utc"},
		{"sql-a.example.test hostname", ""},
		{"duration ms\u2063", ""},
		{"caf\u00e9", ""},
		{"a,b", ""},
		{strings.Repeat("a", 65), ""},
	} {
		column := tc.in
		body := fmt.Sprintf("refused %d", i)
		run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			_, _ = io.Copy(io.Discard, r)
			return importer.Report{Importer: "contract", Rows: 1}, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: column}
		}
		if _, err := c.Import(e.ctx, meta(), strings.NewReader(body), run); !errors.Is(err, importer.ErrQuarantine) {
			t.Fatalf("%q: %v", tc.in, err)
		}
		qs, err := q.Quarantines(e.ctx)
		if err != nil || len(qs) != i+1 {
			t.Fatalf("%q: Quarantines %+v %v", tc.in, qs, err)
		}
		if got := qs[i].Column; got != tc.want {
			t.Errorf("column %q stored as %q, want %q", tc.in, got, tc.want)
		}
	}
}
