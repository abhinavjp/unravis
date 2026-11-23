package clisrv

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/masterkey"
	dashboard "api-audit-explorer/internal/server"
)

const caseToken = "tok-0123456789abcdef0123456789abcdef"

func casesHandler(dash http.Handler) http.Handler {
	return Handler(Deps{Backend: echo(), Internal: NewInternalGET(dash, caseToken), Info: func() Info { return Info{PID: 42, Gen: "133", AppVersion: "1.2.3", Owner: "app"} }, Logf: func(string, ...any) {}})
}

// The real dashboard handler with a case store: what the window lists is what the command line lists.
func TestCasesOverTheRealDashboardHandler(t *testing.T) {
	store := casestore.NewMemory()
	ctx := context.Background()
	a, err := store.Create(ctx, "Payroll slowdown")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, "Second case"); err != nil {
		t.Fatal(err)
	}
	dash := dashboard.New(audit.NewDemo(time.Now().UTC()), nil, caseToken, false, dashboard.WithCases(store, nil, nil))
	h := casesHandler(dash)

	l := op(t, h, "cases.list", "")
	if !l.OK {
		t.Fatalf("list: %s", l.Raw)
	}
	cs := l.Data["cases"].([]any)
	if len(cs) != 2 {
		t.Fatalf("cases: %s", l.Raw)
	}
	found := false
	for _, c := range cs {
		m := c.(map[string]any)
		if m["id"] == string(a.ID) {
			found = m["title"] == "Payroll slowdown" && m["closed"] == nil && m["archived"] == false && m["keyLocked"] == false
		}
	}
	if !found {
		t.Errorf("the case is not listed as the store has it: %s", l.Raw)
	}
	if l.Meta["identifiers"] == "" {
		t.Errorf("meta: %v", l.Meta)
	}

	s := op(t, h, "cases.show", `{"id":"`+string(a.ID)+`"}`)
	if !s.OK || s.Data["id"] != string(a.ID) || s.Data["title"] != "Payroll slowdown" {
		t.Fatalf("show: %s", s.Raw)
	}
	if _, has := s.Data["history"].([]any); !has {
		t.Errorf("history: %s", s.Raw)
	}
	for _, k := range []string{"envs", "sources", "imports", "bytes", "created", "ownKey"} {
		if _, has := s.Data[k]; !has {
			t.Errorf("show has no %s: %s", k, s.Raw)
		}
	}
	// an id of the right shape that does not exist, and ids that are not ids
	if r := op(t, h, "cases.show", `{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaa"}`); r.OK || r.Error.Code != "NOT_FOUND" {
		t.Errorf("missing: %s", r.Raw)
	}
	for _, bad := range []string{`{}`, `{"id":""}`, `{"id":"../cases"}`, `{"id":"` + string(a.ID) + `/key"}`, `{"id":"` + strings.ToUpper(string(a.ID)) + `"}`, `{"id":1}`, `{"id":"` + string(a.ID) + `","x":1}`} {
		if r := op(t, h, "cases.show", bad); r.OK || r.Error.Code != "INVALID_ARGUMENT" {
			t.Errorf("%s: %s", bad, r.Raw)
		}
	}
	// the archived list and its offset
	if r := op(t, h, "cases.list", `{"archived":true}`); !r.OK || len(r.Data["cases"].([]any)) != 0 {
		t.Errorf("archived: %s", r.Raw)
	}
	for _, bad := range []string{`{"offset":1}`, `{"archived":true,"offset":-1}`, `{"archived":true,"offset":1000000000}`, `{"archived":"yes"}`, `{"x":1}`} {
		if r := op(t, h, "cases.list", bad); r.OK || r.Error.Code != "INVALID_ARGUMENT" {
			t.Errorf("%s: %s", bad, r.Raw)
		}
	}
}

// What the dashboard says beyond the allowed fields never reaches the command line, and no answer text is copied into an error.
func TestCasesProjectionAndErrorsCarryNoExtraText(t *testing.T) {
	const marker = "SECRET-MARKER"
	id := "abcdefghijklmnopqrstuvwxyz"[:26]
	full := `{"id":"` + id + `","title":"T","created":"2026-11-20T10:00:00+02:00","closed":null,"bytes":5,"imports":2,"state":"open","ownKey":true,"keyLocked":false,` +
		`"keyEnding":"` + marker + `","missing":false,"filesMade":3,"requestId":"` + marker + `","path":"C:\\` + marker + `","mark":"` + marker + `",` +
		`"archive":{"active":true,"current":{"name":"` + marker + `","folder":"` + marker + `","keyEnding":"` + marker + `"}}}`
	view := strings.TrimSuffix(full, "}") + `,"envs":1,"sources":2,"retries":[{"label":"` + marker + `"}],"history":[{"at":"2026-11-20T10:00:00Z","kind":"created","detail":"` + marker + `"}]}`
	var status = http.StatusOK
	dash := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Audit-Token") != caseToken {
			w.WriteHeader(401)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"`+marker+`","reason":"`+marker+`"}`)
			return
		}
		if r.URL.Path == "/api/cases" {
			_, _ = io.WriteString(w, `{"cases":[`+full+`],"archivedTotal":4,"offset":0,"pageSize":50,"erased":[{"line":"`+marker+`"}],"oldCases":2}`)
			return
		}
		_, _ = io.WriteString(w, view)
	})
	h := casesHandler(dash)
	l := op(t, h, "cases.list", "")
	s := op(t, h, "cases.show", `{"id":"`+id+`"}`)
	for _, r := range []reply{l, s} {
		if !r.OK || strings.Contains(r.Raw, marker) {
			t.Errorf("a field outside the allow-list got through: %s", r.Raw)
		}
	}
	c := l.Data["cases"].([]any)[0].(map[string]any)
	if c["archived"] != true || c["ownKey"] != true || c["created"] != "2026-11-20T08:00:00Z" || l.Data["archivedTotal"] != float64(4) {
		t.Errorf("list row: %v", c)
	}
	if _, has := c["state"]; has {
		t.Errorf("state is not in the spec: %v", c)
	}
	h0 := s.Data["history"].([]any)[0].(map[string]any)
	if len(h0) != 2 || h0["kind"] != "created" || s.Data["envs"] != float64(1) || s.Data["sources"] != float64(2) {
		t.Errorf("show: %v", s.Data)
	}
	// statuses of the dashboard
	for code, want := range map[int]string{423: "CASE_LOCKED", 404: "NOT_FOUND", 503: "ENDPOINT_UNAVAILABLE", 429: "OWNER_BUSY", 500: "INTERNAL", 418: "INTERNAL",
		504: "TIMEOUT", 400: "INVALID_ARGUMENT", 401: "PERMISSION_DENIED", 403: "PERMISSION_DENIED", 409: "ENDPOINT_UNAVAILABLE"} {
		status = code
		for _, r := range []reply{op(t, h, "cases.list", ""), op(t, h, "cases.show", `{"id":"`+id+`"}`)} {
			if r.OK || r.Error.Code != want || strings.Contains(r.Raw, marker) {
				t.Errorf("status %d: %s", code, r.Raw)
			}
			retry := strings.Contains(r.Raw, `"retryable":true`)
			if retry != (code == 429) {
				t.Errorf("status %d: retryable is %v", code, retry)
			}
		}
	}
}

func TestInternalGETAllowlist(t *testing.T) {
	var seen []string
	dash := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		_, _ = io.WriteString(w, `{}`)
	})
	get := NewInternalGET(dash, caseToken)
	ok := []string{"/api/cases", "/api/cases?archived=1&offset=0", "/api/cases?archived=1&offset=123", "/api/cases/abcdefghijklmnopqrstuvwxyz"}
	for _, p := range ok {
		if st, _, err := get(context.Background(), p); err != nil || st != 200 {
			t.Errorf("%s: %d %v", p, st, err)
		}
	}
	for _, p := range []string{"/api/cases/", "/api/cases/abc", "/api/cases/abcdefghijklmnopqrstuvwxyz/key", "/api/cases/abcdefghijklmnopqrstuvwxyz/key/unlock",
		"/api/cases/ABCDEFGHIJKLMNOPQRSTUVWXYZ", "/api/cases?x=1", "/api/cases?archived=1", "/api/cases?archived=1&offset=-1", "/api/cases?archived=1&offset=1234567890",
		"/api/cases/orphan-files", "/api/hosts", "/api/version", "/api/sources", "/api/cases/../hosts", "//api/cases", "http://x/api/cases", "/api/cases#x", "/api/cases\n", ""} {
		if _, _, err := get(context.Background(), p); err == nil {
			t.Errorf("%q was allowed", p)
		}
	}
	if len(seen) != len(ok) {
		t.Errorf("the handler saw %v", seen)
	}
	for _, s := range seen {
		if !strings.HasPrefix(s, "GET ") {
			t.Errorf("not a GET: %s", s)
		}
	}
	// a handler that panics, a cancelled context and an answer over the cap
	boom := NewInternalGET(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("x") }), caseToken)
	if _, _, err := boom(context.Background(), "/api/cases"); err == nil {
		t.Error("a panic was not turned into an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := get(ctx, "/api/cases"); err == nil {
		t.Error("a cancelled call ran")
	}
	big := NewInternalGET(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, MaxResponseBytes/2+1))
		_, _ = w.Write(make([]byte, MaxResponseBytes/2+1))
	}), caseToken)
	if _, _, err := big(context.Background(), "/api/cases"); err != errTooLarge {
		t.Errorf("over the cap: %v", err)
	}
	h := casesHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, MaxResponseBytes/2+1))
		_, _ = w.Write(make([]byte, MaxResponseBytes/2+1))
	}))
	if r := op(t, h, "cases.list", ""); r.OK || r.Error.Code != "RESULT_TOO_LARGE" {
		t.Errorf("%s", r.Raw)
	}
}

func TestCasesDeadlineAndCancel(t *testing.T) {
	started := make(chan struct{})
	dash := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	h := casesHandler(dash)
	r := httptest.NewRequest("POST", "/cli/v1/op", strings.NewReader(`{"op":"cases.list","args":{},"requestId":"`+rid+`","deadlineMs":50}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	<-started
	var out reply
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.OK || out.Error.Code != "TIMEOUT" {
		t.Errorf("%s", w.Body.String())
	}
	_ = clicore.CodeTimeout
}

// A 409 says why in its reason: a busy case passes by itself (retryable); anything else needs the app. The reason is read against a list and
// never copied.
func TestCasesConflictReasons(t *testing.T) {
	reason := ""
	h := casesHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		_, _ = io.WriteString(w, `{"error":"TEXT-FROM-DASHBOARD","reason":"`+reason+`"}`)
	}))
	for r, wantBusy := range map[string]bool{"case-erasing": true, "case-freeing": true, "case-in-use": true, "archive-running": true, "case-import-running": true,
		"untrusted-case": false, "needs-rewrap": false, "case-archived": false, "": false, "SOMETHING-NEW": false} {
		reason = r
		got := op(t, h, "cases.show", `{"id":"abcdefghijklmnopqrstuvwxyz"}`)
		busy := got.Error.Code == "OWNER_BUSY"
		if got.OK || busy != wantBusy || strings.Contains(got.Raw, "TEXT-FROM-DASHBOARD") || strings.Contains(got.Raw, "SOMETHING-NEW") || (busy != strings.Contains(got.Raw, `"retryable":true`)) {
			t.Errorf("reason %q: %s", r, got.Raw)
		}
		if !busy && got.Error.Code != "ENDPOINT_UNAVAILABLE" {
			t.Errorf("reason %q: %s", r, got.Raw)
		}
	}
}

// The exact keys of what leaves: a field the dashboard adds later, or one this projection adds, must be added here on purpose.
func TestCasesProjectionKeys(t *testing.T) {
	id := "abcdefghijklmnopqrstuvwxyz"
	var ua, token string
	row := `{"id":"` + id + `","title":"T","created":"2026-11-20T10:00:00+02:00","closed":"2026-11-21T10:00:00+02:00","bytes":5,"imports":2,"ownKey":false,"keyLocked":false,"missing":true,` +
		`"untrusted":false,"archive":{"active":false}}`
	h := casesHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua, token = r.Header.Get("User-Agent"), r.Header.Get("X-Audit-Token")
		if r.URL.Path == "/api/cases" {
			_, _ = io.WriteString(w, `{"cases":[`+row+`],"archivedTotal":3,"offset":50,"pageSize":25}`)
			return
		}
		_, _ = io.WriteString(w, strings.TrimSuffix(row, "}")+`,"envs":1,"sources":2,"history":[{"at":"2026-11-20T10:00:00+02:00","kind":"closed"}]}`)
	}))
	keys := func(m map[string]any) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}
	l := op(t, h, "cases.list", "")
	if got := keys(l.Data); got != "archivedTotal,cases,offset,pageSize" {
		t.Errorf("list keys: %s", got)
	}
	if got := keys(l.Data["cases"].([]any)[0].(map[string]any)); got != "archived,bytes,closed,created,id,imports,keyLocked,missing,ownKey,title,untrusted" {
		t.Errorf("row keys: %s", got)
	}
	c := l.Data["cases"].([]any)[0].(map[string]any)
	if c["closed"] != "2026-11-21T08:00:00Z" || c["created"] != "2026-11-20T08:00:00Z" || c["archived"] != false || c["missing"] != true || c["untrusted"] != false || c["bytes"] != float64(5) || c["imports"] != float64(2) {
		t.Errorf("row: %v", c)
	}
	if l.Data["offset"] != float64(50) || l.Data["pageSize"] != float64(25) || l.Data["archivedTotal"] != float64(3) {
		t.Errorf("paging: %v", l.Data)
	}
	if ua != "unravis-mcp" || token != caseToken {
		t.Errorf("the dashboard saw agent %q and token %q", ua, token)
	}
	s := op(t, h, "cases.show", `{"id":"`+id+`"}`)
	if got := keys(s.Data); got != "archived,bytes,closed,created,envs,history,id,imports,keyLocked,missing,ownKey,sources,title,untrusted" {
		t.Errorf("show keys: %s", got)
	}
	if hh := s.Data["history"].([]any)[0].(map[string]any); keys(hh) != "at,kind" || hh["at"] != "2026-11-20T08:00:00Z" {
		t.Errorf("history: %v", hh)
	}
}

// A row the store shows without a date (a locked or untrusted case) has a null date, not year 1.
func TestCasesNoDateIsNull(t *testing.T) {
	h := casesHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"cases":[{"id":"abcdefghijklmnopqrstuvwxyz","title":"(locked)","created":"0001-01-01T00:00:00Z","keyLocked":true,"ownKey":true}],"archivedTotal":0,"offset":0,"pageSize":50}`)
	}))
	c := op(t, h, "cases.list", "").Data["cases"].([]any)[0].(map[string]any)
	if c["created"] != nil || c["title"] != "(locked)" || c["keyLocked"] != true {
		t.Errorf("%v", c)
	}
}

// The real handler with the real sqlite store and a real master key: what a locked case key and a locked master key look like.
func TestCasesRealLocks(t *testing.T) {
	sess := dashboard.NewMasterKeySession(t.TempDir())
	store, err := sqlite.New(sqlite.Options{
		Root: filepath.Join(t.TempDir(), "cases"), Protector: storetest.RandomProtector(), MinFreeBytes: -1,
		Protectors: sess.Lookup, NewCaseKind: sess.NewCaseKind, LegacyCheck: sess.LegacyCheck, LegacyDone: sess.LegacyDone,
		KeyParams: masterkey.Params{Time: 1, Memory: 16 << 10, Threads: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	dash := dashboard.New(audit.NewDemo(time.Now().UTC()), nil, caseToken, false, dashboard.WithCases(store, nil, nil), dashboard.WithMasterKey(sess))
	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
		r.Header.Set("X-Audit-Token", caseToken)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		dash.ServeHTTP(w, r)
		return w
	}
	const pass, ck = "a long master key phrase for tests", "a case key phrase for tests ok"
	if w := do("POST", "/api/master-key/setup", `{"passphrase":`+strconv.Quote(pass)+`,"confirm":`+strconv.Quote(pass)+`,"acknowledged":true}`); w.Code/100 != 2 {
		t.Fatalf("setup %d %s", w.Code, w.Body)
	}
	w := do("POST", "/api/cases", `{"title":"Keyed SECRETTITLE","caseKey":`+strconv.Quote(ck)+`,"caseKeyConfirm":`+strconv.Quote(ck)+`}`)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	id := w.Body.String()[strings.Index(w.Body.String(), `"id":"`)+6:][:26]
	h := casesHandler(dash)
	if s := op(t, h, "cases.show", `{"id":"`+id+`"}`); !s.OK || s.Data["title"] != "Keyed SECRETTITLE" || s.Data["ownKey"] != true {
		t.Errorf("an open case with its own key: %s", s.Raw)
	}
	if w := do("POST", "/api/cases/"+id+"/key/lock", ""); w.Code/100 != 2 {
		t.Fatalf("key lock %d %s", w.Code, w.Body)
	}
	l := op(t, h, "cases.list", "")
	row := l.Data["cases"].([]any)[0].(map[string]any)
	if !l.OK || strings.Contains(l.Raw, "SECRETTITLE") || row["title"] != "(locked)" || row["keyLocked"] != true || row["created"] != nil && row["created"] == "0001-01-01T00:00:00Z" {
		t.Errorf("a locked case key: %s", l.Raw)
	}
	if s := op(t, h, "cases.show", `{"id":"`+id+`"}`); s.OK || s.Error.Code != "CASE_LOCKED" || strings.Contains(s.Raw, "SECRETTITLE") {
		t.Errorf("show of a case with a locked key: %s", s.Raw)
	}
	if w := do("POST", "/api/master-key/lock", ""); w.Code/100 != 2 {
		t.Fatalf("master key lock %d %s", w.Code, w.Body)
	}
	for _, r := range []reply{op(t, h, "cases.list", ""), op(t, h, "cases.show", `{"id":"`+id+`"}`), op(t, h, "cases.list", `{"archived":true}`)} {
		if r.OK || r.Error.Code != "CASE_LOCKED" || strings.Contains(r.Raw, "SECRETTITLE") {
			t.Errorf("a locked master key: %s", r.Raw)
		}
	}
}
