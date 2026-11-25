package cmdtree

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/locator"
	dashboard "api-audit-explorer/internal/server"
)

func TestCasesListAndShow(t *testing.T) {
	o := newOwner(t, nil)
	ctx := context.Background()
	a, err := o.store.Create(ctx, "Payroll slowdown")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.store.Create(ctx, "Second"); err != nil {
		t.Fatal(err)
	}
	l := data(t, o.run("cases", "list", "-o", "json"))
	if len(l["cases"].([]any)) != 2 || l["archivedTotal"] != float64(0) {
		t.Fatalf("list: %v", l)
	}
	if a := lastArgs(o); len(a) != 0 {
		t.Errorf("nothing was asked for, so nothing is sent: %v", a)
	}
	tb := o.run("cases", "list")
	if tb.code != 0 || !strings.Contains(tb.out, string(a.ID)) || !strings.Contains(tb.out, "Payroll slowdown") || !strings.Contains(tb.out, "0 archived") {
		t.Errorf("table: %+v", tb)
	}
	s := data(t, o.run("cases", "show", string(a.ID), "--json"))
	if s["id"] != string(a.ID) || s["title"] != "Payroll slowdown" || s["closed"] != nil {
		t.Errorf("show: %v", s)
	}
	if args := lastArgs(o); len(args) != 1 || args["id"] != string(a.ID) {
		t.Errorf("show args: %v", args)
	}
	if tb := o.run("cases", "show", string(a.ID)); tb.code != 0 || !strings.Contains(tb.out, "Payroll slowdown") || !strings.Contains(tb.out, "HISTORY") || !strings.Contains(tb.out, "created") {
		t.Errorf("show table: %+v", tb)
	}
	// the archived list takes its offset
	o.run("cases", "list", "--archived", "--offset", "3", "--json")
	if a := lastArgs(o); a["archived"] != true || a["offset"] != float64(3) || len(a) != 2 {
		t.Errorf("archived args: %v", a)
	}
	// with --request instead of the id
	r := runOn(t, o.attacher(), `{"id":"`+string(a.ID)+`"}`, nil, "cases", "show", "--request", "-", "--json")
	if r.code != 0 {
		t.Errorf("request: %+v", r)
	}
	// errors: the id of a case that is not there, no id, two ids, a bad id, an id next to --request
	if r := o.run("cases", "show", "aaaaaaaaaaaaaaaaaaaaaaaaaa", "--json"); r.code != 5 || errCode(t, r) != "NOT_FOUND" {
		t.Errorf("missing: %+v", r)
	}
	before := len(o.opsSeen())
	for name, args := range map[string][]string{
		"no id":        {"cases", "show"},
		"empty id":     {"cases", "show", ""},
		"two ids":      {"cases", "show", "a", "b"},
		"id and file":  {"cases", "show", "x", "--request", "-"},
		"offset alone": {"cases", "list", "--offset", "2"},
	} {
		r := runOn(t, o.attacher(), `{"id":"x"}`, nil, append(args, "--json")...)
		if r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// every one of them is refused on this side
	if n := len(o.opsSeen()) - before; n != 0 {
		t.Errorf("%d requests reached the owner, want 0", n)
	}
	if r := o.run("cases", "show", "not-an-id", "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
		t.Errorf("bad id: %+v", r)
	}
}

func TestCasesAreLockedAnswerNoData(t *testing.T) {
	const marker = "SECRET-TITLE"
	locked := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusLocked)
		_, _ = io.WriteString(w, `{"error":"`+marker+`","reason":"`+marker+`"}`)
	})
	o := newOwner(t, fakeBackend(func(context.Context, string, json.RawMessage) (any, *clicore.Error) { return map[string]any{}, nil }), withCases(locked))
	for _, args := range [][]string{{"cases", "list"}, {"cases", "show", "aaaaaaaaaaaaaaaaaaaaaaaaaa"}} {
		r := o.run(append(args, "--json")...)
		if r.code != 4 || errCode(t, r) != "CASE_LOCKED" || r.out != "" || strings.Contains(r.err, marker) {
			t.Errorf("%v: %+v", args, r)
		}
		if e := decode(t, r.err)["error"].(map[string]any); e["retryable"] != false || e["action"] == "" {
			t.Errorf("%v: %v", args, e)
		}
		tb := o.run(args...)
		if tb.code != 4 || tb.out != "" || strings.Contains(tb.err, marker) || !strings.Contains(tb.err, "Code: CASE_LOCKED") {
			t.Errorf("%v table: %+v", args, tb)
		}
	}
}

func TestCasesOperationsAreInTheSchemaList(t *testing.T) {
	for _, op := range []string{"cases.list", "cases.show"} {
		m, ok := describe(op)
		if !ok || m["operation"] != op {
			t.Errorf("%s has no description", op)
		}
		row, _ := clicap.Lookup(op)
		if row.Human != clicap.Allow || row.Auto != clicap.Deny || row.MCP != clicap.Deny {
			t.Errorf("%s: matrix row %+v", op, row)
		}
	}
	found := 0
	for _, o := range operations() {
		if o.Operation == "cases.list" || o.Operation == "cases.show" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("schema lists %d of the two case operations", found)
	}
}

func TestCasesTableCellsAndFooters(t *testing.T) {
	const id = "abcdefghijklmnopqrstuvwxyz"
	dash := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the owner sends the erase notices and the old-layout count with archived pages too
		const repeated = `"erased":[{"id":"cccccccccccccccccccccccccc","at":"2026-11-22T12:00:00+02:00"}],"oldCases":3`
		if strings.Contains(r.URL.RawQuery, "offset=0") {
			_, _ = io.WriteString(w, `{"cases":[],"archivedTotal":0,"offset":0,"pageSize":50,`+repeated+`}`)
			return
		}
		if strings.Contains(r.URL.RawQuery, "archived=1") {
			_, _ = io.WriteString(w, `{"cases":[{"id":"`+id+`","title":"Old","created":"2026-11-20T10:00:00.123456789Z","bytes":9,"imports":1,"archive":{"active":true}}],"archivedTotal":120,"offset":50,"pageSize":50,`+repeated+`}`)
			return
		}
		if r.URL.Path == "/api/cases" {
			_, _ = io.WriteString(w, `{"cases":[{"id":"`+id+`","title":"Bell\u0007 case","created":"2026-11-20T10:00:00.123456789Z","closed":"2026-11-21T10:00:00Z","bytes":1234,"imports":3,`+
				`"ownKey":true,"keyLocked":true,"missing":true,"untrusted":true},{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbb","title":"(locked)","created":"0001-01-01T00:00:00Z","keyLocked":true,"ownKey":true}],"archivedTotal":7,"offset":0,"pageSize":50,`+
				`"erased":[{"id":"cccccccccccccccccccccccccc","at":"2026-11-22T12:00:00+02:00","line":"SECRET-ERASED-LINE"}],"oldCases":3}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"`+id+`","title":"T","created":"2026-11-20T10:00:00.5Z","closed":"2026-11-21T10:00:00Z","bytes":1,"imports":2,"envs":4,"sources":5,"history":[{"at":"2026-11-20T10:00:00.9Z","kind":"created"}]}`)
	})
	o := newOwner(t, fakeBackend(func(context.Context, string, json.RawMessage) (any, *clicore.Error) { return map[string]any{}, nil }), withCases(dash))
	l := o.run("cases", "list")
	if l.code != 0 || strings.Contains(l.out, "\a") || strings.Contains(l.out, ".123") || strings.Contains(l.out, "SECRET-ERASED-LINE") {
		t.Errorf("control text or nanoseconds in the table: %q", l.out)
	}
	for _, want := range []string{"2026-11-20T10:00:00Z", "1234", "closed, own key, key locked, working copy missing, untrusted", "7 archived (use --archived)", "own key, key locked",
		"3 cases from before the shared layout", "ERASED CASE", "cccccccccccccccccccccccccc", "2026-11-22T10:00:00Z"} {
		if !strings.Contains(l.out, want) {
			t.Errorf("list table has no %q:\n%s", want, l.out)
		}
	}
	if strings.Contains(l.out, "0001") {
		t.Errorf("a case with no date shows a date:\n%s", l.out)
	}
	a := o.run("cases", "list", "--archived", "--offset", "50")
	if a.code != 0 || !strings.Contains(a.out, "1 archived cases shown (of 120)") || !strings.Contains(a.out, "More: add --archived --offset 51.") || strings.Contains(a.out, "use --archived") {
		t.Errorf("archived page:\n%s", a.out)
	}
	// the erase notices and the old-layout count belong to the first list: an archived page does not repeat them, empty or not
	for _, page := range []result{a, o.run("cases", "list", "--archived")} {
		if strings.Contains(page.out, "ERASED CASE") || strings.Contains(page.out, "cccccccccccccccccccccccccc") || strings.Contains(page.out, "before the shared layout") {
			t.Errorf("an archived page repeats the erase notices or the old-layout line:\n%s", page.out)
		}
	}
	// the JSON of an archived page is the owner's answer, whole
	if aj := data(t, o.run("cases", "list", "--archived", "--offset", "50", "-o", "json")); len(aj["erased"].([]any)) != 1 || aj["oldCases"] != float64(3) {
		t.Errorf("archived json: %v", aj)
	}
	s := o.run("cases", "show", id)
	for _, want := range []string{"2026-11-20T10:00:00Z", "2026-11-21T10:00:00Z", "environments", "4", "HISTORY", "created"} {
		if !strings.Contains(s.out, want) {
			t.Errorf("show table has no %q:\n%s", want, s.out)
		}
	}
	// the JSON keeps the full time and the null date
	j := data(t, o.run("cases", "list", "-o", "json"))
	if j["cases"].([]any)[1].(map[string]any)["created"] != nil {
		t.Errorf("%v", j)
	}
}

func TestCasesLocalChecksBeforeAnyCall(t *testing.T) {
	o := newOwner(t, nil)
	// no app running at all: a bad argument is still a usage error (exit 2), not a missing app (exit 3)
	_ = os.Remove(locator.FilePath(o.runDir, false))
	for name, args := range map[string][]string{
		"offset alone": {"cases", "list", "--offset", "2"},
		"bad id":       {"cases", "show", "not-an-id"},
		"upper id":     {"cases", "show", "ABCDEFGHIJKLMNOPQRSTUVWXYZ"},
		"id and file":  {"cases", "show", "abcdefghijklmnopqrstuvwxyz", "--request", filepath.Join(t.TempDir(), "missing.json")},
	} {
		r := o.run(append(args, "--json")...)
		if r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
			t.Errorf("%s: %+v", name, r)
		}
		if name == "id and file" && !strings.Contains(r.err, "cannot be combined") {
			t.Errorf("%s: the conflict is the answer, not the missing file: %s", name, r.err)
		}
	}
}

// A real erase through the real dashboard handler, with the real erased.json file behind it, reaches `cases list`: the line is the case
// id and the time of the erase, the table shows it, and the answer holds the published schema (a non-empty erased list).
func TestCasesListShowsARealErasedCase(t *testing.T) {
	ctx := context.Background()
	store := casestore.NewMemory()
	gone, err := store.Create(ctx, "Secret payroll title")
	if err != nil {
		t.Fatal(err)
	}
	kept, err := store.Create(ctx, "Kept case")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.November, 20, 8, 30, 0, 0, time.UTC)
	logPath := filepath.Join(t.TempDir(), "erased.json")
	log := dashboard.NewClockedErasedLog(dashboard.NewErasedFile(logPath), func() time.Time { return at })
	dash := dashboard.New(audit.NewDemo(at), nil, ownerToken, false, dashboard.WithCases(store, nil, nil), dashboard.WithErasedLog(log))
	req := httptest.NewRequest(http.MethodDelete, "/api/cases/"+string(gone.ID), strings.NewReader(`{"confirm":"`+string(gone.ID)+`"}`))
	req.Header.Set("X-Audit-Token", ownerToken)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr, req.Host = "127.0.0.1:0", "127.0.0.1"
	rec := httptest.NewRecorder()
	dash.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("erase: %d %s", rec.Code, rec.Body.String())
	}
	o := newOwner(t, fakeBackend(func(context.Context, string, json.RawMessage) (any, *clicore.Error) { return map[string]any{}, nil }), withCases(dash))
	l := data(t, o.run("cases", "list", "--json"))
	erased, _ := l["erased"].([]any)
	if len(erased) != 1 {
		t.Fatalf("erased: %v", l["erased"])
	}
	e := erased[0].(map[string]any)
	if len(e) != 2 || e["id"] != string(gone.ID) || e["at"] != "2026-11-20T08:30:00Z" {
		t.Errorf("erased line: %v", e)
	}
	if cs := l["cases"].([]any); len(cs) != 1 || cs[0].(map[string]any)["id"] != string(kept.ID) {
		t.Errorf("the erased case is still listed: %v", cs)
	}
	schema, _ := clicore.NativeSchema("cases.list")
	if bad := schemaProblems(schema["output"].(map[string]any), any(l), "data"); len(bad) != 0 {
		t.Errorf("a real erased answer does not hold the schema: %v", bad)
	}
	tb := o.run("cases", "list")
	for _, want := range []string{"ERASED CASE", string(gone.ID), "2026-11-20T08:30:00Z"} {
		if !strings.Contains(tb.out, want) {
			t.Errorf("table has no %q:\n%s", want, tb.out)
		}
	}
	if strings.Contains(tb.out, "Secret payroll title") {
		t.Errorf("the title of the erased case is in the table:\n%s", tb.out)
	}
	if raw, err := os.ReadFile(logPath); err != nil || strings.Contains(string(raw), "Secret payroll title") { //nolint:gosec // a test reading its own temp log
		t.Errorf("erased.json: %v %s", err, raw)
	}
}

// The published schema is closed at every level the command line fills, its times are date-times, and a non-empty erased list with an
// undated line (null) holds it. Without additionalProperties:false an outside validator would accept a leaked key.
func TestCasesSchemasAreClosedAndDated(t *testing.T) {
	const id = "abcdefghijklmnopqrstuvwxyz"
	dash := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"cases":[{"id":"`+id+`","title":"T","created":"2026-11-20T10:00:00Z","bytes":1}],"archivedTotal":0,"offset":0,"pageSize":50,"oldCases":1,`+
			`"erased":[{"id":"bcdefghijklmnopqrstuvwxyza","at":"2026-11-22T12:00:00Z","line":"X"},{"id":"cdefghijklmnopqrstuvwxyzab"},{"id":"not an id","at":"2026-11-22T12:00:00Z"}]}`)
	})
	o := newOwner(t, fakeBackend(func(context.Context, string, json.RawMessage) (any, *clicore.Error) { return map[string]any{}, nil }), withCases(dash))
	l := data(t, o.run("cases", "list", "--json"))
	erased := l["erased"].([]any)
	if len(erased) != 2 || erased[1].(map[string]any)["at"] != nil {
		t.Fatalf("erased: %v", erased)
	}
	schema, _ := clicore.NativeSchema("cases.list")
	out := schema["output"].(map[string]any)
	if bad := schemaProblems(out, any(l), "data"); len(bad) != 0 {
		t.Errorf("a non-empty erased answer does not hold the schema: %v", bad)
	}
	closed := func(name string, s map[string]any) {
		if s["additionalProperties"] != false {
			t.Errorf("%s is not closed (additionalProperties:false)", name)
		}
	}
	dated := func(name string, s any) {
		p, _ := s.(map[string]any)
		if p["format"] != "date-time" {
			t.Errorf("%s has no format date-time: %v", name, p)
		}
	}
	listRow := out["properties"].(map[string]any)["cases"].(map[string]any)["items"].(map[string]any)
	listErased := out["properties"].(map[string]any)["erased"].(map[string]any)["items"].(map[string]any)
	closed("cases.list output", out)
	closed("cases.list case row", listRow)
	closed("cases.list erased item", listErased)
	dated("cases.list created", listRow["properties"].(map[string]any)["created"])
	dated("cases.list closed", listRow["properties"].(map[string]any)["closed"])
	dated("cases.list erased at", listErased["properties"].(map[string]any)["at"])
	show, _ := clicore.NativeSchema("cases.show")
	so := show["output"].(map[string]any)
	hist := so["properties"].(map[string]any)["history"].(map[string]any)["items"].(map[string]any)
	closed("cases.show output", so)
	closed("cases.show history item", hist)
	dated("cases.show created", so["properties"].(map[string]any)["created"])
	dated("cases.show history at", hist["properties"].(map[string]any)["at"])
	// the times of the real answer are date-times
	for _, e := range erased {
		if at, ok := e.(map[string]any)["at"].(string); ok {
			if _, err := time.Parse(time.RFC3339, at); err != nil {
				t.Errorf("erased at %q is not a date-time", at)
			}
		}
	}
}
