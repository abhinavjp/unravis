package cmdtree

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/locator"
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
		if strings.Contains(r.URL.RawQuery, "archived=1") {
			_, _ = io.WriteString(w, `{"cases":[{"id":"`+id+`","title":"Old","created":"2026-11-20T10:00:00.123456789Z","bytes":9,"imports":1,"archive":{"active":true}}],"archivedTotal":120,"offset":50,"pageSize":50}`)
			return
		}
		if r.URL.Path == "/api/cases" {
			_, _ = io.WriteString(w, `{"cases":[{"id":"`+id+`","title":"Bell\u0007 case","created":"2026-11-20T10:00:00.123456789Z","closed":"2026-11-21T10:00:00Z","bytes":1234,"imports":3,`+
				`"ownKey":true,"keyLocked":true,"missing":true,"untrusted":true},{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbb","title":"(locked)","created":"0001-01-01T00:00:00Z","keyLocked":true,"ownKey":true}],"archivedTotal":7,"offset":0,"pageSize":50}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"`+id+`","title":"T","created":"2026-11-20T10:00:00.5Z","closed":"2026-11-21T10:00:00Z","bytes":1,"imports":2,"envs":4,"sources":5,"history":[{"at":"2026-11-20T10:00:00.9Z","kind":"created"}]}`)
	})
	o := newOwner(t, fakeBackend(func(context.Context, string, json.RawMessage) (any, *clicore.Error) { return map[string]any{}, nil }), withCases(dash))
	l := o.run("cases", "list")
	if l.code != 0 || strings.Contains(l.out, "\a") || strings.Contains(l.out, ".123") {
		t.Errorf("control text or nanoseconds in the table: %q", l.out)
	}
	for _, want := range []string{"2026-11-20T10:00:00Z", "1234", "closed, own key, key locked, working copy missing, untrusted", "7 archived (use --archived)", "own key, key locked"} {
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
