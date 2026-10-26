package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/ledger"
)

const absentCaseID = "aaaaaaaaaaaaaaaaaaaaaaaaaa" // well formed, never created

// ledgerEnvs runs fn once per case store with a shared (PC-wide) ledger wired.
func ledgerEnvs(t *testing.T, fn func(t *testing.T, e *caseEnv, shared ledger.Store)) {
	t.Helper()
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			shared, err := ledger.Open("")
			if err != nil {
				t.Fatal(err)
			}
			e := tagEnv(t, store, &fakeRepository{}, WithLedger(shared))
			e.ledger = shared
			fn(t, e, shared)
		})
	}
}

func caseLedgerPath(id string) string { return "/api/cases/" + id + "/ledger" }

func listCaseLedger(t *testing.T, e *caseEnv, id string) []ledger.Hypothesis {
	t.Helper()
	res := e.get(caseLedgerPath(id))
	if res.Code != http.StatusOK {
		t.Fatalf("list = %d %s", res.Code, res.Body)
	}
	if strings.TrimSpace(res.Body.String()) == "null" {
		t.Fatalf("list is null, want []")
	}
	var out []ledger.Hypothesis
	decodeBody(t, res, &out)
	return out
}

func createCaseTheory(t *testing.T, e *caseEnv, id, text string) ledger.Hypothesis {
	t.Helper()
	res := e.json(http.MethodPost, caseLedgerPath(id), `{"text":`+q(text)+`}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", res.Code, res.Body)
	}
	var h ledger.Hypothesis
	decodeBody(t, res, &h)
	return h
}

func TestCaseLedgerCRUDAndReuse(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, _ ledger.Store) {
		id := e.newCase("theories")
		if got := listCaseLedger(t, e, id); len(got) != 0 {
			t.Fatalf("fresh case has %d theories", len(got))
		}
		h := createCaseTheory(t, e, id, "Slow calls follow the nightly job")
		if h.ID == "" || h.State != ledger.StateOpen || h.Text != "Slow calls follow the nightly job" {
			t.Fatalf("created = %+v", h)
		}

		path := caseLedgerPath(id) + "/" + h.ID
		body := `{"text":"Slow calls follow the nightly job","state":"supported_by_correlation","evidenceIds":["ev:abcd1234:f1"],"notes":"seen twice","settlingObservation":"Run with the job paused","facts":{"presentK":2,"totalN":3}}`
		res := e.json(http.MethodPut, path, body)
		if res.Code != http.StatusOK {
			t.Fatalf("update = %d %s", res.Code, res.Body)
		}
		var upd ledger.Hypothesis
		decodeBody(t, res, &upd)
		if upd.State != ledger.StateSupported || upd.Notes != "seen twice" || len(upd.EvidenceIDs) != 1 || upd.Facts.TotalN != 3 {
			t.Fatalf("updated = %+v", upd)
		}
		if got := listCaseLedger(t, e, id); len(got) != 1 || got[0].State != ledger.StateSupported {
			t.Fatalf("list after update = %+v", got)
		}

		res = e.json(http.MethodPost, path+"/reuse", "")
		if res.Code != http.StatusCreated {
			t.Fatalf("reuse = %d %s", res.Code, res.Body)
		}
		var reused ledger.Hypothesis
		decodeBody(t, res, &reused)
		if reused.ID == h.ID || reused.State != ledger.StateOpen || len(reused.History) == 0 {
			t.Fatalf("reused = %+v", reused)
		}
		if got := listCaseLedger(t, e, id); len(got) != 2 {
			t.Fatalf("list after reuse has %d", len(got))
		}

		if res = e.json(http.MethodDelete, path, ""); res.Code != http.StatusNoContent {
			t.Fatalf("delete = %d %s", res.Code, res.Body)
		}
		if res = e.json(http.MethodDelete, path, ""); res.Code != http.StatusNotFound {
			t.Fatalf("delete again = %d", res.Code)
		}
		if res = e.json(http.MethodPut, path, body); res.Code != http.StatusNotFound {
			t.Fatalf("update deleted = %d", res.Code)
		}
		if res = e.json(http.MethodPost, path+"/reuse", ""); res.Code != http.StatusNotFound {
			t.Fatalf("reuse deleted = %d", res.Code)
		}
		if res = e.json(http.MethodPost, caseLedgerPath(id), `{"text":"   "}`); res.Code != http.StatusBadRequest {
			t.Fatalf("empty text = %d %s", res.Code, res.Body)
		}
		if got := listCaseLedger(t, e, id); len(got) != 1 || got[0].ID != reused.ID {
			t.Fatalf("final list = %+v", got)
		}
	})
}

func TestCaseLedgersAreIsolated(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, shared ledger.Store) {
		a, b := e.newCase("a"), e.newCase("b")
		onlyA := createCaseTheory(t, e, a, "only in A")
		createCaseTheory(t, e, b, "only in B")
		if got := listCaseLedger(t, e, a); len(got) != 1 || got[0].Text != "only in A" {
			t.Fatalf("A = %+v", got)
		}
		if got := listCaseLedger(t, e, b); len(got) != 1 || got[0].Text != "only in B" {
			t.Fatalf("B = %+v", got)
		}
		// A's entry id means nothing in B, and nothing in the shared ledger.
		if res := e.json(http.MethodDelete, caseLedgerPath(b)+"/"+onlyA.ID, ""); res.Code != http.StatusNotFound {
			t.Fatalf("delete A's id in B = %d", res.Code)
		}
		if items, _ := shared.List(); len(items) != 0 {
			t.Fatalf("shared ledger gained %d entries", len(items))
		}
		// And the shared routes still work on their own.
		if res := e.json(http.MethodPost, "/api/ledger", `{"text":"shared one"}`); res.Code != http.StatusCreated {
			t.Fatalf("shared create = %d %s", res.Code, res.Body)
		}
		if got := listCaseLedger(t, e, a); len(got) != 1 {
			t.Fatalf("A after a shared create = %d", len(got))
		}
	})
}

func TestCaseLedgerClosedCaseListsButRefusesWrites(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, _ ledger.Store) {
		id := e.newCase("closing")
		h := createCaseTheory(t, e, id, "kept after close")
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK && res.Code != http.StatusNoContent {
			t.Fatalf("close = %d %s", res.Code, res.Body)
		}
		if got := listCaseLedger(t, e, id); len(got) != 1 || got[0].Text != "kept after close" {
			t.Fatalf("closed list = %+v", got)
		}
		path := caseLedgerPath(id) + "/" + h.ID
		for _, c := range []struct{ method, path, body string }{
			{http.MethodPost, caseLedgerPath(id), `{"text":"new"}`},
			{http.MethodPut, path, `{"text":"edit","state":"open","evidenceIds":[],"notes":"","facts":{}}`},
			{http.MethodPost, path + "/reuse", ""},
			{http.MethodDelete, path, ""},
		} {
			res := e.json(c.method, c.path, c.body)
			if res.Code != http.StatusConflict || failureOf(t, res).Reason != "case-closed" {
				t.Errorf("%s %s on a closed case = %d %s, want 409 case-closed", c.method, c.path, res.Code, res.Body)
			}
		}
		if got := listCaseLedger(t, e, id); len(got) != 1 || got[0].Text != "kept after close" {
			t.Fatalf("closed case changed: %+v", got)
		}
	})
}

func TestCaseLedgerCopyShared(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, shared ledger.Store) {
		id := e.newCase("copy target")
		other := e.newCase("other")
		copyPath := caseLedgerPath(id) + "/copy-shared"

		// 404 while the shared ledger holds nothing.
		if res := e.json(http.MethodPost, copyPath, ""); res.Code != http.StatusNotFound || failureOf(t, res).Reason != "no-shared-ledger" {
			t.Fatalf("empty shared = %d %s", res.Code, res.Body)
		}

		rich, err := shared.Create("Rich theory")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := shared.Update(rich.ID, ledger.Input{
			Text: "Rich theory", State: ledger.StateSupported, EvidenceIDs: []string{"ev:abcd1234:f1", "ev:abcd1234:f2"},
			Notes: "a note", SettlingObservation: "Pause the job",
			Facts: ledger.Facts{PresentK: 2, TotalN: 3, Counterexamples: []string{"ep 3"}, Alternatives: []ledger.Alternative{{Text: "GC", Status: ledger.AltOpen}}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := shared.Reuse(rich.ID); err != nil { // gives a second entry with a history note
			t.Fatal(err)
		}
		before, _ := shared.List()

		res := e.json(http.MethodPost, copyPath, "")
		if res.Code != http.StatusCreated {
			t.Fatalf("copy = %d %s", res.Code, res.Body)
		}
		var copied []ledger.Hypothesis
		decodeBody(t, res, &copied)
		if len(copied) != len(before) {
			t.Fatalf("copied %d of %d", len(copied), len(before))
		}
		listed := listCaseLedger(t, e, id)
		if len(listed) != len(before) {
			t.Fatalf("case holds %d, want %d", len(listed), len(before))
		}
		for i, src := range before {
			got := listed[i]
			if got.ID == src.ID {
				t.Errorf("entry %d kept the shared id", i)
			}
			if got.Text != src.Text || got.State != src.State || got.Notes != src.Notes || got.SettlingObservation != src.SettlingObservation ||
				strings.Join(got.EvidenceIDs, ",") != strings.Join(src.EvidenceIDs, ",") ||
				got.Facts.PresentK != src.Facts.PresentK || got.Facts.TotalN != src.Facts.TotalN ||
				len(got.Facts.Counterexamples) != len(src.Facts.Counterexamples) || len(got.Facts.Alternatives) != len(src.Facts.Alternatives) ||
				len(got.History) != len(src.History) {
				t.Errorf("entry %d differs:\n got %+v\nwant %+v", i, got, src)
			}
		}

		// Once: the second copy is refused and changes nothing.
		if res = e.json(http.MethodPost, copyPath, ""); res.Code != http.StatusConflict || failureOf(t, res).Reason != "ledger-not-empty" {
			t.Fatalf("second copy = %d %s", res.Code, res.Body)
		}
		if got := listCaseLedger(t, e, id); len(got) != len(before) {
			t.Fatalf("second copy changed the case: %d", len(got))
		}
		// The shared ledger is untouched, and another case can copy it too.
		if after, _ := shared.List(); len(after) != len(before) || after[0].ID != before[0].ID {
			t.Fatalf("shared ledger changed")
		}
		if res = e.json(http.MethodPost, caseLedgerPath(other)+"/copy-shared", ""); res.Code != http.StatusCreated {
			t.Fatalf("copy into another case = %d %s", res.Code, res.Body)
		}
		// Editing the copy leaves the original alone.
		path := caseLedgerPath(id) + "/" + listed[0].ID
		if res = e.json(http.MethodDelete, path, ""); res.Code != http.StatusNoContent {
			t.Fatalf("delete copy = %d", res.Code)
		}
		if after, _ := shared.List(); len(after) != len(before) {
			t.Fatalf("deleting the copy changed the shared ledger")
		}
	})
}

func TestCaseLedgerCopySharedWithoutSharedLedgerIs404(t *testing.T) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			e := tagEnv(t, store, &fakeRepository{}) // no WithLedger
			id := e.newCase("none")
			if res := e.json(http.MethodPost, caseLedgerPath(id)+"/copy-shared", ""); res.Code != http.StatusNotFound {
				t.Fatalf("copy without a shared ledger = %d %s", res.Code, res.Body)
			}
			// The case ledger works without the shared one.
			createCaseTheory(t, e, id, "still works")
		})
	}
}

func TestCaseLedgerGuardBlockMatchesSharedRoute(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, shared ledger.Store) {
		id := e.newCase("guards")
		strong := `{"text":"h","state":"supported_by_strong_correlation","evidenceIds":[],"notes":"","facts":{}}`

		sh, _ := shared.Create("h")
		want := e.json(http.MethodPut, "/api/ledger/"+sh.ID, strong)
		h := createCaseTheory(t, e, id, "h")
		got := e.json(http.MethodPut, caseLedgerPath(id)+"/"+h.ID, strong)

		if want.Code != http.StatusUnprocessableEntity || got.Code != want.Code {
			t.Fatalf("shared = %d, case = %d %s", want.Code, got.Code, got.Body)
		}
		var a, b struct {
			Code    string   `json:"code"`
			Reasons []string `json:"reasons"`
		}
		decodeBody(t, want, &a)
		decodeBody(t, got, &b)
		if b.Code != "guard_blocked" || len(b.Reasons) == 0 || strings.Join(a.Reasons, "|") != strings.Join(b.Reasons, "|") {
			t.Fatalf("case body %s differs from shared %s", got.Body, want.Body)
		}
		if items := listCaseLedger(t, e, id); items[0].State != ledger.StateOpen {
			t.Fatalf("blocked update changed the state to %q", items[0].State)
		}
	})
}

func TestCaseLedgerUnknownAndInvalidCase(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, _ ledger.Store) {
		for _, id := range []string{"not-a-case", absentCaseID} {
			for _, c := range []struct{ method, suffix, body string }{
				{http.MethodGet, "", ""},
				{http.MethodPost, "", `{"text":"x"}`},
				{http.MethodPut, "/h1", `{"text":"x","state":"open","evidenceIds":[],"notes":"","facts":{}}`},
				{http.MethodDelete, "/h1", ""},
				{http.MethodPost, "/h1/reuse", ""},
				{http.MethodPost, "/copy-shared", ""},
			} {
				res := e.json(c.method, caseLedgerPath(id)+c.suffix, c.body)
				if res.Code != http.StatusNotFound {
					t.Errorf("%s %s%s = %d %s, want 404", c.method, id, c.suffix, res.Code, res.Body)
				}
			}
		}
	})
}

func TestCaseLedgerBodyLimits(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, _ ledger.Store) {
		id := e.newCase("limits")
		h := createCaseTheory(t, e, id, "h")
		big := `{"text":"` + strings.Repeat("a", 1<<20+10) + `"}`
		if res := e.json(http.MethodPost, caseLedgerPath(id), big); res.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("big create = %d", res.Code)
		}
		if res := e.json(http.MethodPut, caseLedgerPath(id)+"/"+h.ID, big); res.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("big update = %d", res.Code)
		}

		req := httptest.NewRequest(http.MethodPost, caseLedgerPath(id), strings.NewReader(`{"text":"x"}`))
		req.Header.Set("X-Audit-Token", testToken)
		req.Header.Set("Content-Type", "text/plain")
		if res := e.serve(req); res.Code != http.StatusUnsupportedMediaType {
			t.Errorf("non-JSON create = %d", res.Code)
		}
		if res := e.json(http.MethodPost, caseLedgerPath(id), `{"text":"x","extra":1}`); res.Code != http.StatusBadRequest {
			t.Errorf("unknown field = %d", res.Code)
		}
		// An entry over the per-field limit is a 400, as on the shared route.
		if res := e.json(http.MethodPost, caseLedgerPath(id), `{"text":"`+strings.Repeat("b", ledger.MaxTextLen+1)+`"}`); res.Code != http.StatusBadRequest {
			t.Errorf("long text = %d %s", res.Code, res.Body)
		}
		if got := listCaseLedger(t, e, id); len(got) != 1 {
			t.Fatalf("refused bodies left %d entries", len(got))
		}
	})
}

func TestCaseLedgerIsUnavailableInTheDemo(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, true)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, caseLedgerPath(absentCaseID)},
		{http.MethodPost, caseLedgerPath(absentCaseID)},
		{http.MethodPost, caseLedgerPath(absentCaseID) + "/copy-shared"},
	} {
		if res := request(h, c.method, c.path, `{"text":"x"}`, true); res.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s in the demo = %d", c.method, c.path, res.Code)
		}
	}
}

func TestCaseLedgerRefusesWithoutToken(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, _ ledger.Store) {
		id := e.newCase("auth")
		req := httptest.NewRequest(http.MethodGet, caseLedgerPath(id), nil)
		if res := e.serve(req); res.Code != http.StatusUnauthorized {
			t.Fatalf("no token = %d", res.Code)
		}
	})
}

// A stored ledger that cannot be read is reported and never replaced.
func TestCaseLedgerDamagedBlobIsKept(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, _ ledger.Store) {
		id := e.newCase("damaged")
		c, err := e.store.Open(context.Background(), casestore.CaseID(id))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		lb := c.(casestore.LedgerBlobStore)
		junk := []byte(`{"version":1,"hypotheses":[{"id":`)
		if err := lb.SetLedgerBlob(context.Background(), junk); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct{ method, body string }{{http.MethodGet, ""}, {http.MethodPost, `{"text":"x"}`}} {
			res := e.json(c.method, caseLedgerPath(id), c.body)
			if res.Code != http.StatusInternalServerError || failureOf(t, res).Reason != "ledger-damaged" {
				t.Errorf("%s damaged = %d %s", c.method, res.Code, res.Body)
			}
		}
		got, err := lb.LedgerBlob(context.Background())
		if err != nil || !bytes.Equal(got, junk) {
			t.Fatalf("damaged ledger was replaced: %q %v", got, err)
		}
	})
}

// Two tabs adding at once must both land.
func TestCaseLedgerConcurrentCreatesAreNotLost(t *testing.T) {
	ledgerEnvs(t, func(t *testing.T, e *caseEnv, _ ledger.Store) {
		id := e.newCase("race")
		const n = 8
		done := make(chan int, n)
		for i := 0; i < n; i++ {
			go func() {
				res := e.json(http.MethodPost, caseLedgerPath(id), `{"text":"parallel"}`)
				done <- res.Code
			}()
		}
		for i := 0; i < n; i++ {
			if code := <-done; code != http.StatusCreated {
				t.Errorf("parallel create = %d", code)
			}
		}
		if got := listCaseLedger(t, e, id); len(got) != n {
			t.Fatalf("kept %d of %d parallel creates", len(got), n)
		}
	})
}

func TestCaseLedgerPersistsAfterReopeningTheStore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cases")
	prot := storetest.RandomProtector()
	open := func() casestore.Store {
		s, err := sqlite.New(sqlite.Options{Root: root, Protector: prot, MinFreeBytes: -1})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first := tagEnv(t, open(), &fakeRepository{})
	id := first.newCase("persist")
	h := createCaseTheory(t, first, id, "survives a restart")
	if res := first.json(http.MethodPut, caseLedgerPath(id)+"/"+h.ID,
		`{"text":"survives a restart","state":"supported_by_correlation","evidenceIds":["ev:abcd1234:f1"],"notes":"n","facts":{}}`); res.Code != http.StatusOK {
		t.Fatalf("update = %d %s", res.Code, res.Body)
	}

	second := tagEnv(t, open(), &fakeRepository{})
	got := listCaseLedger(t, second, id)
	if len(got) != 1 || got[0].ID != h.ID || got[0].State != ledger.StateSupported || got[0].Notes != "n" {
		t.Fatalf("after reopening = %+v", got)
	}
	// The stored bytes are inside the encrypted database, not readable in the folder.
	scanFolder(t, root, []byte("survives a restart"))
}

func TestCaseLedgerIsLockedBehindTheMasterKey(t *testing.T) {
	e := newMKEnv(t)
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	id := e.newCase("locked")
	path := caseLedgerPath(id)
	if res := e.do(http.MethodPost, path, `{"text":"before the lock"}`); res.Code != http.StatusCreated {
		t.Fatalf("create unlocked = %d %s", res.Code, res.Body)
	}
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock = %d", res.Code)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, path, ""},
		{http.MethodPost, path, `{"text":"while locked"}`},
		{http.MethodPut, path + "/h1", `{}`},
		{http.MethodDelete, path + "/h1", ""},
		{http.MethodPost, path + "/h1/reuse", ""},
		{http.MethodPost, path + "/copy-shared", ""},
	} {
		if res := e.do(c.method, c.path, c.body); res.Code != http.StatusLocked {
			t.Errorf("%s %s locked = %d %s, want 423", c.method, c.path, res.Code, res.Body)
		}
	}
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	res := e.do(http.MethodGet, path, "")
	var got []ledger.Hypothesis
	decodeBody(t, res, &got)
	if res.Code != http.StatusOK || len(got) != 1 || got[0].Text != "before the lock" {
		t.Fatalf("after unlock = %d %s", res.Code, res.Body)
	}
}

func TestLedgerBlobCapHoldsTheLedgerBudget(t *testing.T) {
	if casestore.MaxLedgerBlob < ledger.MaxTotalBytes {
		t.Fatalf("MaxLedgerBlob %d is below ledger.MaxTotalBytes %d", casestore.MaxLedgerBlob, ledger.MaxTotalBytes)
	}
}
