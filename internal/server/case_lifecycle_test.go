package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
)

// The tests here run over both stores (memory and SQLite), through the HTTP
// routes, so the belief table, reopen, create request ids, the retry list and the
// marks framework are proved the way a page meets them.

const (
	m2Text  = "This case changed in another tab. Reload."
	reqID1  = "wizard-run-0001"
	reqID2  = "wizard-run-0002"
	toyFile = "a.csv"
)

// believing sends one request that carries the page's belief.
func (e *caseEnv) believing(method, path, body, belief string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Audit-Token", testToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if belief != "" {
		req.Header.Set("X-Expect-State", belief)
	}
	return e.serve(req)
}

func (e *caseEnv) view(t *testing.T, id string) caseViewDTO {
	t.Helper()
	res := e.get("/api/cases/" + id)
	if res.Code != http.StatusOK {
		t.Fatalf("get case = %d %s", res.Code, res.Body)
	}
	var v caseViewDTO
	decodeBody(t, res, &v)
	return v
}

func wantFailure(t *testing.T, res *httptest.ResponseRecorder, code int, reason, text string) {
	t.Helper()
	if res.Code != code {
		t.Fatalf("status = %d %s, want %d %s", res.Code, res.Body, code, reason)
	}
	f := failureOf(t, res)
	if f.Reason != reason || (text != "" && f.Error != text) {
		t.Fatalf("failure = %+v, want reason %q text %q", f, reason, text)
	}
}

func historyKinds(v caseViewDTO) string {
	kinds := make([]string, 0, len(v.History))
	for _, h := range v.History {
		kinds = append(kinds, h.Kind)
	}
	return strings.Join(kinds, ",")
}

/* ------------------------------------------------------- belief + reopen */

func TestCloseAndReopenCheckTheBelief(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id
		if v := e.view(t, id); v.State != "open" || historyKinds(v) != "created" {
			t.Fatalf("new case = %s %s", v.State, historyKinds(v))
		}
		// A page that thinks the case is closed cannot close it.
		wantFailure(t, e.believing(http.MethodPost, base+"/close", "", "closed"), 409, "case-changed", m2Text)
		// A header that is not a state is a plain bad request.
		wantFailure(t, e.believing(http.MethodPost, base+"/close", "", "sleeping"), 400, "bad-expect-state", "")
		// Reopen needs a Closed case; this one is Open.
		wantFailure(t, e.believing(http.MethodPost, base+"/reopen", "", "closed"), 409, "case-changed", m2Text)

		res := e.believing(http.MethodPost, base+"/close", "", "open")
		if res.Code != http.StatusOK {
			t.Fatalf("close = %d %s", res.Code, res.Body)
		}
		var closed caseViewDTO
		decodeBody(t, res, &closed)
		if closed.State != "closed" || closed.Closed == nil {
			t.Fatalf("closed view = %+v", closed)
		}
		// The second tab, still believing Open, is told the case changed.
		wantFailure(t, e.believing(http.MethodPost, base+"/close", "", "open"), 409, "case-changed", m2Text)
		// Reopen believing Open (stale) is refused; believing Closed it runs.
		wantFailure(t, e.believing(http.MethodPost, base+"/reopen", "", "open"), 409, "case-changed", m2Text)
		res = e.believing(http.MethodPost, base+"/reopen", "", "closed")
		if res.Code != http.StatusOK {
			t.Fatalf("reopen = %d %s", res.Code, res.Body)
		}
		var reopened caseViewDTO
		decodeBody(t, res, &reopened)
		if reopened.State != "open" || reopened.Closed != nil || historyKinds(reopened) != "created,closed,reopened" {
			t.Fatalf("reopened view = %s closed=%v history=%s", reopened.State, reopened.Closed, historyKinds(reopened))
		}
		if v := e.view(t, id); v.State != "open" || v.Closed != nil {
			t.Fatalf("view after reopen = %+v", v)
		}
	})
}

// A request with no X-Expect-State (an older client) asks for a state: Close of a Closed case succeeds and writes nothing.
func TestHeaderlessCloseIsIdempotent(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id
		for i := 0; i < 2; i++ {
			if res := e.believing(http.MethodPost, base+"/close", "", ""); res.Code != http.StatusOK {
				t.Fatalf("headerless close #%d = %d %s", i, res.Code, res.Body)
			}
		}
		if v := e.view(t, id); v.State != "closed" || historyKinds(v) != "created,closed" {
			t.Fatalf("after two headerless closes: %s %s", v.State, historyKinds(v))
		}
		// A page that does say what it believes is still told about a move it missed.
		wantFailure(t, e.believing(http.MethodPost, base+"/close", "", "open"), 409, "case-changed", m2Text)
	})
}

func TestReopenTwiceFastReopensOnce(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id
		if res := e.believing(http.MethodPost, base+"/close", "", "open"); res.Code != 200 {
			t.Fatal(res.Body)
		}
		const presses = 6
		codes := make([]int, presses)
		var wg sync.WaitGroup
		for i := range codes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i] = e.believing(http.MethodPost, base+"/reopen", "", "closed").Code
			}()
		}
		wg.Wait()
		ok, changed := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusOK:
				ok++
			case http.StatusConflict:
				changed++
			default:
				t.Fatalf("unexpected status %d in %v", c, codes)
			}
		}
		if ok != 1 || changed != presses-1 {
			t.Fatalf("codes = %v, want one 200 and the rest 409", codes)
		}
		if v := e.view(t, id); historyKinds(v) != "created,closed,reopened" {
			t.Fatalf("history = %s, want one reopen entry", historyKinds(v))
		}
	})
}

func TestReopenedCaseTakesImportsAgain(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id
		e.believing(http.MethodPost, base+"/close", "", "open")
		file := &upload{toyFile, toyCSV(toyRow(1, "web-a", "alpha", 5))}
		wantFailure(t, e.upload(commitPath(id), ackFields, file), 409, "case-closed", "")
		if res := e.believing(http.MethodPost, base+"/reopen", "", "closed"); res.Code != 200 {
			t.Fatal(res.Body)
		}
		if res := e.upload(commitPath(id), ackFields, file); res.Code != http.StatusCreated && res.Code != http.StatusOK {
			t.Fatalf("import after reopen = %d %s", res.Code, res.Body)
		}
		if n := len(e.importsOf(id)); n != 1 {
			t.Fatalf("imports = %d", n)
		}
	})
}

// Close in tab A and import in tab B: the import is refused and nothing is half-written.
func TestCloseInOneTabImportInAnother(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		if res := e.believing(http.MethodPost, "/api/cases/"+id+"/close", "", "open"); res.Code != 200 {
			t.Fatal(res.Body)
		}
		res := e.upload(commitPath(id), ackFields, &upload{toyFile, toyCSV(toyRow(1, "web-a", "alpha", 5))})
		wantFailure(t, res, 409, "case-closed", "")
		if n := len(e.importsOf(id)); n != 0 {
			t.Fatalf("closed case holds %d imports", n)
		}
		// Tags and environments are closed too, with their own reason.
		res = e.json(http.MethodPut, "/api/cases/"+id+"/environments", `{"environments":["prod"]}`)
		wantFailure(t, res, 409, "case-closed", "")
	})
}

/* ---------------------------------------------------------- create + id */

func createWith(e *caseEnv, title, rid string) *httptest.ResponseRecorder {
	body := `{"title":` + strconv.Quote(title)
	if rid != "" {
		body += `,"requestId":` + strconv.Quote(rid)
	}
	return e.json(http.MethodPost, "/api/cases", body+`}`)
}

func TestCreateWithRequestIDMakesOneCase(t *testing.T) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			e := tagEnv(t, store, &fakeRepository{})
			first := createWith(e, "Wizard case", reqID1)
			if first.Code != http.StatusCreated {
				t.Fatalf("first create = %d %s", first.Code, first.Body)
			}
			var a, b caseDTO
			decodeBody(t, first, &a)
			// A second press, or a reload that sends the saved id, gets the same case.
			second := createWith(e, "Wizard case", reqID1)
			if second.Code != http.StatusOK {
				t.Fatalf("repeat create = %d %s", second.Code, second.Body)
			}
			decodeBody(t, second, &b)
			if a.ID != b.ID {
				t.Fatalf("repeat made another case: %s then %s", a.ID, b.ID)
			}
			// A different id is a different wizard run.
			var c caseDTO
			third := createWith(e, "Wizard case", reqID2)
			if third.Code != http.StatusCreated {
				t.Fatalf("other id = %d %s", third.Code, third.Body)
			}
			decodeBody(t, third, &c)
			if c.ID == a.ID {
				t.Fatal("a new request id reused a case")
			}
			// An id the server cannot log safely is refused.
			wantFailure(t, createWith(e, "x", "bad id!"), 400, "bad-request-id", "")
			if items, _ := store.List(context.Background()); len(items) != 2 {
				t.Fatalf("store holds %d cases, want 2", len(items))
			}
		})
	}
}

func TestDoublePressCreateMakesOneCase(t *testing.T) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			e := tagEnv(t, store, &fakeRepository{})
			const presses = 8
			ids := make([]string, presses)
			codes := make([]int, presses)
			var wg sync.WaitGroup
			for i := 0; i < presses; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					res := createWith(e, "Pressed", reqID1)
					codes[i] = res.Code
					var d caseDTO
					decodeBody(t, res, &d)
					ids[i] = d.ID
				}()
			}
			wg.Wait()
			created := 0
			for i := range ids {
				if ids[i] != ids[0] || ids[i] == "" {
					t.Fatalf("presses answered different cases: %v (%v)", ids, codes)
				}
				if codes[i] == http.StatusCreated {
					created++
				}
			}
			if created != 1 {
				t.Fatalf("%d presses answered 201, want exactly one (%v)", created, codes)
			}
			if items, _ := store.List(context.Background()); len(items) != 1 {
				t.Fatalf("store holds %d cases, want 1", len(items))
			}
		})
	}
}

func TestCreateWithRequestIDOfACaseBeingArchivedIsM18(t *testing.T) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			e := tagEnv(t, store, &fakeRepository{})
			var a caseDTO
			decodeBody(t, createWith(e, "Archive me", reqID1), &a)
			if err := store.(casestore.Lifecycle).SetMark(context.Background(), casestore.CaseID(a.ID), casestore.MarkArchiving); err != nil {
				t.Fatal(err)
			}
			wantFailure(t, createWith(e, "Archive me", reqID1), 409, "case-already-made", "This case was already made. Find it on the start screen.")
		})
	}
}

/* ----------------------------------------------------------- retry list */

func putRetries(e *caseEnv, id, body string) *httptest.ResponseRecorder {
	return e.json(http.MethodPut, "/api/cases/"+id+"/retries", body)
}

const twoRetries = `{"items":[` +
	`{"kind":"environments","key":"environments","label":"Environments","reason":"failed","payload":{"environments":["prod","staging"]}},` +
	`{"kind":"audit-source","key":"audit:orders","label":"orders","reason":"locked"}]}`

func TestRetryListIsStoredInTheCase(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		if v := e.view(t, id); v.Retries == nil || len(v.Retries) != 0 {
			t.Fatalf("new case retries = %v, want []", v.Retries)
		}
		if res := putRetries(e, id, twoRetries); res.Code != 200 {
			t.Fatalf("put retries = %d %s", res.Code, res.Body)
		}
		v := e.view(t, id)
		if len(v.Retries) != 2 || v.Retries[0].Kind != "environments" || v.Retries[1].Kind != "audit-source" {
			t.Fatalf("retries = %+v", v.Retries)
		}
		// The same run again replaces the list: nothing doubles.
		putRetries(e, id, twoRetries)
		if v := e.view(t, id); len(v.Retries) != 2 {
			t.Fatalf("after a repeat the list holds %d items", len(v.Retries))
		}
		// "Not added to analysis" and "import failed" are separate kinds.
		res := putRetries(e, id, `{"items":[{"kind":"audit-source","key":"k","label":"a","reason":"failed"},{"kind":"import","key":"k","label":"b","reason":"failed"}]}`)
		if res.Code != 200 {
			t.Fatalf("two kinds with one key = %d %s", res.Code, res.Body)
		}
		if v := e.view(t, id); len(v.Retries) != 2 {
			t.Fatalf("kinds were merged: %+v", v.Retries)
		}
		// One item goes; the rest stay.
		first := e.view(t, id).Retries[0].ID
		if res := e.json(http.MethodDelete, "/api/cases/"+id+"/retries/"+strconv.FormatInt(first, 10), ""); res.Code != http.StatusNoContent {
			t.Fatalf("delete = %d %s", res.Code, res.Body)
		}
		if v := e.view(t, id); len(v.Retries) != 1 {
			t.Fatalf("after delete the list holds %d items", len(v.Retries))
		}
		// An empty list clears it.
		putRetries(e, id, `{"items":[]}`)
		if v := e.view(t, id); len(v.Retries) != 0 {
			t.Fatal("empty put did not clear")
		}
	})
}

func TestRetryItemsRefuseCredentialsAndBadShapes(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		for name, body := range map[string]string{
			"uri in label":    `{"items":[{"kind":"tag","key":"k","label":"mongodb://u:p@h/db","reason":"failed"}]}`,
			"password key":    `{"items":[{"kind":"tag","key":"password=hunter2","label":"x","reason":"failed"}]}`,
			"uri in payload":  `{"items":[{"kind":"tag","key":"k","label":"x","reason":"failed","payload":{"a":"https://h/x"}}]}`,
			"unknown kind":    `{"items":[{"kind":"gossip","key":"k","label":"x","reason":"failed"}]}`,
			"empty key":       `{"items":[{"kind":"tag","key":"","label":"x","reason":"failed"}]}`,
			"payload not obj": `{"items":[{"kind":"tag","key":"k","label":"x","reason":"failed","payload":[1]}]}`,
		} {
			res := putRetries(e, id, body)
			if res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != "bad-retry" {
				t.Errorf("%s = %d %s", name, res.Code, res.Body)
			}
		}
		if v := e.view(t, id); len(v.Retries) != 0 {
			t.Fatalf("a refused put stored %d items", len(v.Retries))
		}
	})
}

func TestRetryRunsEnvironmentsAndTagsOnTheServer(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		putRetries(e, id, `{"items":[`+
			`{"kind":"environments","key":"environments","label":"Environments","reason":"failed","payload":{"environments":["prod","staging"]}},`+
			`{"kind":"tag","key":"iis:web","label":"web","reason":"failed","payload":{"kind":"iis-logs","label":"web","env":"prod"}},`+
			`{"kind":"import","key":"i","label":"file","reason":"failed"}]}`)
		items := e.view(t, id).Retries
		run := func(it retryDTO) *httptest.ResponseRecorder {
			return e.json(http.MethodPost, "/api/cases/"+id+"/retries/"+strconv.FormatInt(it.ID, 10)+"/retry", "")
		}
		if res := run(items[0]); res.Code != http.StatusNoContent {
			t.Fatalf("retry environments = %d %s", res.Code, res.Body)
		}
		if got := envsOf(t, e, id); len(got) != 2 || got[0] != "prod" {
			t.Fatalf("environments = %v", got)
		}
		if res := run(items[1]); res.Code != http.StatusNoContent {
			t.Fatalf("retry tag = %d %s", res.Code, res.Body)
		}
		if got := sourcesOf(t, e, "/api/cases/"+id+"/sources"); len(got) != 1 || got[0].Kind != "iis-logs" {
			t.Fatalf("sources = %+v", got)
		}
		// An import needs the page's file, so the server leaves it for the page.
		wantFailure(t, run(items[2]), 422, "retry-in-page", "")
		left := e.view(t, id).Retries
		if len(left) != 1 || left[0].Kind != "import" {
			t.Fatalf("items left = %+v", left)
		}
		// A retry of an item that is gone is a 404.
		wantFailure(t, run(items[0]), 404, "retry-not-found", "")
	})
}

func TestRetryOnAClosedCaseWaitsForReopen(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		putRetries(e, id, `{"items":[{"kind":"environments","key":"environments","label":"Environments","reason":"failed","payload":{"environments":["prod"]}}]}`)
		e.believing(http.MethodPost, "/api/cases/"+id+"/close", "", "open")
		// The list is still readable, and still there.
		v := e.view(t, id)
		if len(v.Retries) != 1 {
			t.Fatalf("closed case lost its list: %+v", v.Retries)
		}
		res := e.json(http.MethodPost, "/api/cases/"+id+"/retries/"+strconv.FormatInt(v.Retries[0].ID, 10)+"/retry", "")
		wantFailure(t, res, 409, "case-closed", "The case is closed. Reopen it to try again.")
		if v := e.view(t, id); len(v.Retries) != 1 {
			t.Fatal("a refused retry dropped the item")
		}
		e.believing(http.MethodPost, "/api/cases/"+id+"/reopen", "", "closed")
		res = e.json(http.MethodPost, "/api/cases/"+id+"/retries/"+strconv.FormatInt(v.Retries[0].ID, 10)+"/retry", "")
		if res.Code != http.StatusNoContent {
			t.Fatalf("retry after reopen = %d %s", res.Code, res.Body)
		}
	})
}

/* --------------------------------------------------------------- marks */

func TestMarkedCaseIsRepairedPerCallOrAnswersWait(t *testing.T) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			lc := store.(casestore.Lifecycle)
			rec, err := casestore.NewRecoverer(store)
			if err != nil {
				t.Fatal(err)
			}
			e := tagEnv(t, store, &fakeRepository{}, WithRecoverer(rec))
			freeing, erasing, plain := e.newCase("freeing"), e.newCase("erasing"), e.newCase("plain")
			ctx := context.Background()
			_ = lc.SetMark(ctx, casestore.CaseID(freeing), casestore.MarkFreeing)
			_ = lc.SetMark(ctx, casestore.CaseID(erasing), casestore.MarkErasing)

			// No hook yet: the marks answer M19 and M5 on every case route.
			wantFailure(t, e.get("/api/cases/"+freeing), 409, "case-freeing", "This case is being freed. Try again in a moment.")
			wantFailure(t, e.get("/api/cases/"+erasing), 409, "case-erasing", "This case is being erased.")
			wantFailure(t, e.believing(http.MethodPost, "/api/cases/"+freeing+"/close", "", "open"), 409, "case-freeing", "")
			if res := e.get("/api/cases/" + plain); res.Code != 200 {
				t.Fatalf("plain case = %d", res.Code)
			}

			// A hook for Freeing finishes the move under the call that meets it, once.
			var ran atomic.Int64
			_ = rec.Register(casestore.MarkFreeing, func(ctx context.Context, id casestore.CaseID) error {
				ran.Add(1)
				return lc.SetMark(ctx, id, "")
			})
			var wg sync.WaitGroup
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if res := e.get("/api/cases/" + freeing); res.Code != 200 {
						t.Errorf("repaired case = %d %s", res.Code, res.Body)
					}
				}()
			}
			wg.Wait()
			if n := ran.Load(); n != 1 {
				t.Fatalf("hook ran %d times, want 1", n)
			}
			// The erasing case still waits (its hook belongs to P4).
			wantFailure(t, e.get("/api/cases/"+erasing), 409, "case-erasing", "")
		})
	}
}

func TestListRepairsOnlyTheMarkedRowsOfItsPage(t *testing.T) {
	store := casestore.NewMemory()
	lc := store.(casestore.Lifecycle)
	rec, _ := casestore.NewRecoverer(store)
	e := tagEnv(t, store, &fakeRepository{}, WithRecoverer(rec))
	a, b := e.newCase("a"), e.newCase("b")
	ctx := context.Background()
	_ = lc.SetMark(ctx, casestore.CaseID(a), casestore.MarkFreeing)
	var ran []casestore.CaseID
	_ = rec.Register(casestore.MarkFreeing, func(ctx context.Context, id casestore.CaseID) error {
		ran = append(ran, id)
		return lc.SetMark(ctx, id, "")
	})
	if res := e.get("/api/cases"); res.Code != 200 {
		t.Fatalf("list = %d", res.Code)
	}
	if len(ran) != 1 || string(ran[0]) != a {
		t.Fatalf("list ran hooks for %v, want only the marked case %s (unmarked %s)", ran, a, b)
	}
}

/* ------------------------------------------------- injection points */

// INJ-8: an import starts while a move holds the case's gate. The import waits its
// turn; when the move was a Close the import is refused (case-closed) and leaves
// nothing behind, and when the import won it is whole and the case closes after it.
func TestInj8ImportStartsWhileAMoveHoldsTheGate(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		hh := e.handler()
		release, err := hh.gates.acquire(context.Background(), casestore.CaseID(id))
		if err != nil {
			t.Fatal(err)
		}
		var importCode, closeCode int
		var importBody string
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			res := e.upload(commitPath(id), ackFields, &upload{toyFile, toyCSV(toyRow(1, "web-a", "alpha", 5))})
			importCode, importBody = res.Code, res.Body.String()
		}()
		go func() {
			defer wg.Done()
			closeCode = e.believing(http.MethodPost, "/api/cases/"+id+"/close", "", "open").Code
		}()
		release()
		wg.Wait()
		if closeCode != http.StatusOK {
			t.Fatalf("close = %d", closeCode)
		}
		imports := len(e.importsOf(id))
		switch importCode {
		case http.StatusCreated, http.StatusOK:
			if imports != 1 {
				t.Fatalf("import answered %d but the case holds %d imports", importCode, imports)
			}
		case http.StatusConflict:
			if imports != 0 {
				t.Fatalf("import was refused but the case holds %d imports", imports)
			}
		default:
			t.Fatalf("import = %d %s", importCode, importBody)
		}
		if v := e.view(t, id); v.State != "closed" {
			t.Fatalf("final state = %s", v.State)
		}
	})
}

// INJ-7: the master key is locked between the steps of a create. Every step
// answers 423 while it is locked, nothing is made twice, and the same request id
// finds the case again after unlock. A marked case is repaired before the unlock
// reply.
func TestInj7LockTheKeyBetweenCreateSteps(t *testing.T) {
	e := newMKEnv(t)
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != 200 {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	rec, err := casestore.NewRecoverer(e.store)
	if err != nil {
		t.Fatal(err)
	}
	var ran atomic.Int64
	lc := casestore.Lifecycle(e.store)
	_ = rec.Register(casestore.MarkFreeing, func(ctx context.Context, id casestore.CaseID) error {
		ran.Add(1)
		return lc.SetMark(ctx, id, "")
	})
	e.h = New(&fakeRepository{}, testAssets(), testToken, false,
		WithCases(e.store, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})), WithMasterKey(e.sess), WithRecoverer(rec))

	body := `{"title":"Wizard","requestId":"` + reqID1 + `"}`
	first := e.do(http.MethodPost, "/api/cases", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", first.Code, first.Body)
	}
	var a caseDTO
	decodeBody(t, first, &a)
	// A mark left by a move that stopped, then the key is locked.
	if err := lc.SetMark(context.Background(), casestore.CaseID(a.ID), casestore.MarkFreeing); err != nil {
		t.Fatal(err)
	}
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != 200 {
		t.Fatalf("lock = %d", res.Code)
	}
	for _, step := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/cases", body},
		{http.MethodPut, "/api/cases/" + a.ID + "/environments", `{"environments":["prod"]}`},
		{http.MethodPut, "/api/cases/" + a.ID + "/retries", `{"items":[]}`},
		{http.MethodPost, "/api/cases/" + a.ID + "/close", ""},
	} {
		if res := e.do(step.method, step.path, step.body); res.Code != http.StatusLocked {
			t.Fatalf("%s %s while locked = %d %s", step.method, step.path, res.Code, res.Body)
		}
	}
	if n := ran.Load(); n != 0 {
		t.Fatalf("hook ran %d times while locked", n)
	}
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)); res.Code != 200 {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	// Recovery finished before the unlock reply, so the first call already works.
	if n := ran.Load(); n != 1 {
		t.Fatalf("hook ran %d times by the end of unlock, want 1", n)
	}
	again := e.do(http.MethodPost, "/api/cases", body)
	if again.Code != http.StatusOK {
		t.Fatalf("repeat create after unlock = %d %s", again.Code, again.Body)
	}
	var b caseDTO
	decodeBody(t, again, &b)
	if a.ID != b.ID {
		t.Fatalf("the same request id made another case: %s then %s", a.ID, b.ID)
	}
	if items, _ := e.store.List(context.Background()); len(items) != 1 {
		t.Fatalf("store holds %d cases, want 1", len(items))
	}
}

// C17: an import that failed, or was killed, is listed as needing another try by the server itself, not only by the wizard. A kill
// in the middle is the same as a failure here: the line is written before the work starts and removed only when the file is in.
func TestFailedImportIsListedAsNeedingAnotherTry(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		bad := &upload{"orders.csv", toyCSV(toyRow(1, "web-a", "BOOM", 5))}
		if res := e.upload(commitPath(id), ackFields, bad); res.Code != http.StatusInternalServerError {
			t.Fatalf("failing import = %d %s", res.Code, res.Body)
		}
		v := e.view(t, id)
		if len(v.Retries) != 1 || v.Retries[0].Kind != "import" || v.Retries[0].Label != "orders.csv" || v.Retries[0].Reason != "import-failed" {
			t.Fatalf("after a failed import the list is %+v", v.Retries)
		}
		// A preview keeps nothing, so it leaves no line either.
		good := &upload{"fresh.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}
		if res := e.upload(commitPath(id)+"&preview=1", nil, good); res.Code != http.StatusOK {
			t.Fatalf("preview = %d %s", res.Code, res.Body)
		}
		if v := e.view(t, id); len(v.Retries) != 1 {
			t.Fatalf("a preview changed the list: %+v", v.Retries)
		}
		// The same file name, tried again and landed: the line goes, whichever side wrote it. The wizard's own line for the file
		// (its key is the source key) goes too; a line for another file stays.
		putRetries(e, id, `{"items":[{"kind":"import","key":"file|orders.csv","label":"orders.csv","reason":"import-failed"},{"kind":"import","key":"file|other.csv","label":"other.csv","reason":"import-failed"}]}`)
		noteAttempt(t, e, id, "orders.csv")
		if v := e.view(t, id); len(v.Retries) != 3 {
			t.Fatalf("setup: %+v", v.Retries)
		}
		again := &upload{"orders.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}
		if res := e.upload(commitPath(id), ackFields, again); res.Code != http.StatusCreated {
			t.Fatalf("second try = %d %s", res.Code, res.Body)
		}
		v = e.view(t, id)
		if len(v.Retries) != 1 || v.Retries[0].Label != "other.csv" {
			t.Fatalf("after the file landed the list is %+v", v.Retries)
		}
		// The same bytes again is a duplicate: the file is in the case, so its line goes as well.
		noteAttempt(t, e, id, "renamed.csv")
		if res := e.upload(commitPath(id), ackFields, &upload{"renamed.csv", again.content}); res.Code != http.StatusConflict {
			t.Fatalf("duplicate = %d %s", res.Code, res.Body)
		}
		if v := e.view(t, id); len(v.Retries) != 1 {
			t.Fatalf("a duplicate left its line: %+v", v.Retries)
		}
	})
}

// noteAttempt writes the server's line for a file the way an import that starts does, to stand in for a run that was killed.
func noteAttempt(t *testing.T, e *caseEnv, id, fileName string) {
	t.Helper()
	c, err := e.store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	noteImportAttempt(context.Background(), c, fileName)
}
