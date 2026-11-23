package cmdtree

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
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
	// "offset alone" reaches the owner, which refuses it; the others never leave this side
	if n := len(o.opsSeen()) - before; n != 1 {
		t.Errorf("%d requests reached the owner, want 1", n)
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
