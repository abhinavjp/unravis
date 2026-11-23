package cmdtree

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/mcp"
)

// Parity of the adapters (spec 05 BL-393): the command line, the MCP bridge and the dashboard handler behind both answer the same
// question with the same data. The data set is the demo owner; the MCP side is a second bridge on the same dashboard handler, with the
// identifiers the command line of a person shows.

func (o *owner) bridge() *mcp.Bridge {
	return mcp.NewLocal(o.dash, ownerToken, mcp.WithIdentifiers(true))
}

func (o *owner) windowArgs() map[string]any {
	w := o.window()
	q := map[string]any{"kind": "all", "outcome": "all", "metric": "total", "bucketSeconds": 60}
	var src []string
	for i := 0; i < len(w); i += 2 {
		switch w[i] {
		case "--source":
			src = append(src, w[i+1])
		case "--start":
			q["start"] = w[i+1]
		case "--end":
			q["end"] = w[i+1]
		case "--baseline-start":
			q["baselineStart"] = w[i+1]
		case "--baseline-end":
			q["baselineEnd"] = w[i+1]
		}
	}
	q["sourceIds"] = src
	// the demo data has 125 rows in each quarter hour before its clock: the current period and the one before it
	q["start"], q["end"] = o.now.Add(-15*time.Minute).Format(time.RFC3339), o.now.Format(time.RFC3339)
	q["baselineStart"], q["baselineEnd"] = o.now.Add(-30*time.Minute).Format(time.RFC3339), o.now.Add(-15*time.Minute).Format(time.RFC3339)
	return map[string]any{"query": q}
}

// strip removes what differs between two runs of the same question: the handle, the cursors and the time a query took.
func strip(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if k == "resultId" || k == "elapsedMs" {
				continue
			}
			if k == "nextCursor" { // the cursor names a moment, but a side that loses paging must still differ
				out[k] = e != nil && e != ""
				continue
			}
			out[k] = strip(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = strip(e)
		}
		return out
	}
	return v
}

func mcpData(t *testing.T, b *mcp.Bridge, tool string, args any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(args)
	got, err := b.Call(context.Background(), tool, raw)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	enc, _ := json.Marshal(got)
	var m map[string]any
	if json.Unmarshal(enc, &m) != nil {
		t.Fatalf("%s: not an object: %s", tool, enc)
	}
	delete(m, "schemaVersion") // the envelope of each adapter carries these
	delete(m, "tool")
	return m
}

func cliRequest(t *testing.T, o *owner, op []string, args any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(args)
	r := runOn(t, o.attacher(), string(raw), nil, append(op, "--request", "-", "--json")...)
	return data(t, r)
}

func TestParityCompareAndFollowUps(t *testing.T) {
	o := newOwner(t, nil)
	b := o.bridge()
	args := o.windowArgs()

	cli := cliRequest(t, o, []string{"audit", "compare"}, args)
	mc := mcpData(t, b, "audit_compare", args)
	if !reflect.DeepEqual(strip(cli), strip(mc)) {
		t.Fatalf("compare differs:\ncli %v\nmcp %v", strip(cli), strip(mc))
	}
	if len(cli["groups"].([]any)) == 0 {
		t.Fatal("the comparison has no groups")
	}
	// order is the owner's: the group keys come in the same order
	keys := func(m map[string]any) []string {
		var k []string
		for _, g := range m["groups"].([]any) {
			k = append(k, g.(map[string]any)["key"].(string))
		}
		return k
	}
	if !reflect.DeepEqual(keys(cli), keys(mc)) {
		t.Errorf("order: %v vs %v", keys(cli), keys(mc))
	}
	checkAgainstDashboard(t, o, args["query"], cli)
	cid, mid := cli["resultId"].(string), mc["resultId"].(string)
	key := keys(cli)[0]
	for _, c := range []struct {
		op   []string
		tool string
		cli  map[string]any
		mcp  map[string]any
	}{
		{[]string{"audit", "groups"}, "audit_get_groups", map[string]any{"resultId": cid, "limit": 5}, map[string]any{"resultId": mid, "limit": 5}},
		{[]string{"audit", "timeline"}, "audit_get_timeline", map[string]any{"resultId": cid, "apiKey": key}, map[string]any{"resultId": mid, "apiKey": key}},
		{[]string{"audit", "requests"}, "audit_get_requests", map[string]any{"resultId": cid, "apiKey": key, "limit": 4}, map[string]any{"resultId": mid, "apiKey": key, "limit": 4}},
		{[]string{"audit", "slowest"}, "audit_get_slowest", map[string]any{"resultId": cid, "limit": 3}, map[string]any{"resultId": mid, "limit": 3}},
		{[]string{"audit", "breakdown"}, "audit_get_breakdown", map[string]any{"resultId": cid, "dim": "statusCode"}, map[string]any{"resultId": mid, "dim": "statusCode"}},
	} {
		got, want := cliRequest(t, o, c.op, c.cli), mcpData(t, b, c.tool, c.mcp)
		if len(got) == 0 || len(want) == 0 {
			t.Errorf("%s: an empty answer (cli %d fields, mcp %d)", c.tool, len(got), len(want))
		}
		if !reflect.DeepEqual(strip(got), strip(want)) {
			t.Errorf("%s differs:\ncli %v\nmcp %v", c.tool, strip(got), strip(want))
		}
	}
	// sources and the guide
	if got, want := cliRequest(t, o, []string{"sources", "list"}, map[string]any{}), mcpData(t, b, "audit_list_sources", map[string]any{}); !reflect.DeepEqual(strip(got), strip(want)) {
		t.Errorf("sources differ:\ncli %v\nmcp %v", got, want)
	}
}

// checkAgainstDashboard is the leg that does not go through the MCP bridge: the dashboard's own /api/analyze, the call of the web
// page, is asked the same question, and every group that the command line shows must have the numbers of that raw row.
func checkAgainstDashboard(t *testing.T, o *owner, query any, cli map[string]any) {
	t.Helper()
	body, _ := json.Marshal(query)
	req := httptest.NewRequest(http.MethodPost, "/api/analyze", bytes.NewReader(body))
	req.Header.Set("X-Audit-Token", ownerToken)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr, req.Host = "127.0.0.1:0", "127.0.0.1"
	rec := httptest.NewRecorder()
	o.dash.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard /api/analyze: %d %s", rec.Code, rec.Body.String())
	}
	var gui struct {
		APIs []map[string]any `json:"apis"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &gui); err != nil || len(gui.APIs) == 0 {
		t.Fatalf("dashboard answer: %v, %d rows", err, len(gui.APIs))
	}
	id := func(g map[string]any) string {
		return fmt.Sprint(g["sourceId"], "|", g["controller"], "|", g["action"], "|", g["kind"], "|", g["outcome"])
	}
	rows := map[string]map[string]any{}
	var cur, ref float64
	for _, g := range gui.APIs {
		rows[id(g)] = g
		cur += g["currentEvents"].(float64)
		ref += g["referenceEvents"].(float64)
	}
	for _, g := range cli["groups"].([]any) {
		c := g.(map[string]any)
		r, ok := rows[id(c)]
		if !ok {
			t.Errorf("the command line shows a group the dashboard does not have: %s", id(c))
			continue
		}
		for _, f := range []string{"currentEvents", "referenceEvents", "p95Ratio", "p95DeltaMs", "state"} {
			if !reflect.DeepEqual(c[f], r[f]) {
				t.Errorf("%s: %s is %v on the command line and %v on the dashboard", id(c), f, c[f], r[f])
			}
		}
	}
	if ref == 0 || cur == 0 {
		t.Fatalf("the data set has no events in a period (current %v, reference %v): the check would prove nothing", cur, ref)
	}
	re := cli["recordedEvents"].(map[string]any)
	if re["current"] != cur || re["reference"] != ref {
		t.Errorf("recorded events %v, the dashboard rows add up to %v and %v", re, cur, ref)
	}
}

// The same bad question is refused with the same category on both sides: boundaries of the time windows and the owner's own checks.
func TestParityBoundaries(t *testing.T) {
	o := newOwner(t, nil)
	b := o.bridge()
	base := o.windowArgs()
	for _, c := range []struct {
		name string
		want clicore.Code
		mut  func(q, top map[string]any)
	}{
		{"end before start", clicore.CodeInvalidArgument, func(q, _ map[string]any) { q["start"], q["end"] = q["end"], q["start"] }},
		{"baseline overlaps", clicore.CodeInvalidArgument, func(q, _ map[string]any) { q["baselineStart"], q["baselineEnd"] = q["start"], q["end"] }},
		{"no sources", clicore.CodeInvalidArgument, func(q, _ map[string]any) { q["sourceIds"] = []string{} }},
		{"unknown source", clicore.CodeNotFound, func(q, _ map[string]any) { q["sourceIds"] = []string{"no-such-source"} }},
		{"time with no zone (refused by the tool schema)", clicore.CodeInvalidArgument, func(q, _ map[string]any) { q["start"] = "2026-01-01T00:00:00" }},
		{"limit too big", clicore.CodeInvalidArgument, func(_, top map[string]any) { top["limit"] = 1000 }},
	} {
		q := map[string]any{}
		for k, v := range base["query"].(map[string]any) {
			q[k] = v
		}
		args := map[string]any{"query": q}
		c.mut(q, args)
		raw, _ := json.Marshal(args)
		r := runOn(t, o.attacher(), string(raw), nil, "audit", "compare", "--request", "-", "--json")
		_, err := b.Call(context.Background(), "audit_compare", raw)
		if r.code == 0 || err == nil {
			t.Errorf("%s: cli exit %d, mcp err %v", c.name, r.code, err)
			continue
		}
		if got, want := errCode(t, r), string(clicore.FromMCP(mcp.Code(err), false, "")); got != want || got != string(c.want) {
			t.Errorf("%s: cli %s, mcp %s, expected %s", c.name, got, want, c.want)
		}
	}
}

// The matrix and the MCP catalog agree: every operation that runs a tool names a tool the bridge advertises, and the operations that are
// not tools (cases, app, doctor) are not in the catalog.
func TestParityMatrixAndCatalog(t *testing.T) {
	catalog := map[string]bool{}
	for _, tl := range mcp.Catalog() {
		catalog[tl["name"].(string)] = true
	}
	for _, r := range clicap.Rows() {
		switch {
		case r.Tool != "":
			if !catalog[r.Tool] {
				t.Errorf("%s runs %s, which the catalog does not have", r.Op, r.Tool)
			}
			if r.MCP == clicap.Deny || r.Human == clicap.Deny {
				t.Errorf("%s: a tool the catalog serves is denied to the MCP or the person: %+v", r.Op, r)
			}
			delete(catalog, r.Tool)
		}
	}
	if len(catalog) != 0 {
		t.Errorf("tools with no operation: %v", catalog)
	}
}

// schemaProblems checks data against the part of a JSON schema that the published output schemas use: required fields, the type of
// every field that the schema describes, and (for an object that the schema does not say is open) fields that it does not describe.
func schemaProblems(schema map[string]any, data any, path string) []string {
	var out []string
	if ty, _ := schema["type"].(string); ty != "" {
		ok := true
		switch ty {
		case "object":
			_, ok = data.(map[string]any)
		case "array":
			_, ok = data.([]any)
		case "string":
			_, ok = data.(string)
		case "boolean":
			_, ok = data.(bool)
		case "integer":
			f, isNum := data.(float64)
			ok = isNum && f == float64(int64(f))
		case "number":
			_, ok = data.(float64)
		}
		if !ok && data != nil {
			return []string{fmt.Sprintf("%s is %T, the schema says %s", path, data, ty)}
		}
	}
	switch d := data.(type) {
	case map[string]any:
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, has := d[r.(string)]; !has {
					out = append(out, path+"."+r.(string)+" is missing")
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for k, v := range d {
			p, known := props[k].(map[string]any)
			if !known {
				if props != nil && schema["additionalProperties"] != true {
					out = append(out, path+"."+k+" is not in the schema")
				}
				continue
			}
			out = append(out, schemaProblems(p, v, path+"."+k)...)
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for _, e := range d {
				out = append(out, schemaProblems(items, e, path+"[]")...)
			}
		}
	}
	return out
}

// Contract snapshots: what the real owner answers has every field that the published schema of the operation requires, and the check
// fails when a field is taken away.
func TestSnapshotSchemasHoldOnRealAnswers(t *testing.T) {
	o := newOwner(t, nil)
	if _, err := o.store.Create(context.Background(), "A case"); err != nil {
		t.Fatal(err)
	}
	list := data(t, o.run("cases", "list", "--json"))
	cases := list["cases"].([]any)
	if len(cases) == 0 {
		t.Fatal("no case")
	}
	id := cases[0].(map[string]any)["id"].(string)
	answers := map[string]map[string]any{
		"app.status": data(t, o.run("app", "status", "--json")),
		"cases.list": list,
		"cases.show": data(t, o.run("cases", "show", id, "--json")),
		"doctor":     data(t, o.run("doctor", "--json")),
		"version":    data(t, o.run("version", "--json")),
		"app.start":  data(t, runStart(t, o, &starter{pid: 1}, "app", "start", "--headless", "--json")),
	}
	// cases.show has no required list in its schema: these fields are the ones a reader of the answer depends on.
	always := map[string][]string{"cases.show": {"id", "title"}}
	for op, got := range answers {
		schema, ok := clicore.NativeSchema(op)
		if !ok {
			t.Errorf("%s has no schema", op)
			continue
		}
		out, _ := schema["output"].(map[string]any)
		if out == nil {
			t.Errorf("%s: no output schema", op)
			continue
		}
		if len(got) == 0 {
			t.Errorf("%s: an empty answer", op)
		}
		if bad := schemaProblems(out, any(got), "data"); len(bad) != 0 {
			t.Errorf("%s: %v", op, bad)
		}
		var must []string
		required, _ := out["required"].([]any)
		for _, r := range required {
			must = append(must, r.(string))
		}
		props, _ := out["properties"].(map[string]any)
		must = append(must, always[op]...)
		if len(must) == 0 {
			t.Errorf("%s: no field is required, so the check proves nothing", op)
		}
		for _, f := range must {
			if _, has := got[f]; !has {
				t.Errorf("%s: the answer has no %s", op, f)
				continue
			}
			// the check sees a field that is taken away and a field of the wrong type
			cp := map[string]any{}
			for k, v := range got {
				cp[k] = v
			}
			delete(cp, f)
			if contains(required, f) && len(schemaProblems(out, any(cp), "data")) == 0 {
				t.Errorf("%s: removing %s was not noticed", op, f)
			}
			if p, _ := props[f].(map[string]any); p != nil && p["type"] != nil {
				cp[f] = []any{"wrong"}
				if p["type"] == "array" {
					cp[f] = "wrong"
				}
				if len(schemaProblems(out, any(cp), "data")) == 0 {
					t.Errorf("%s: a %s of the wrong type was not noticed", op, f)
				}
			}
		}
	}
}

func contains(l []any, s string) bool {
	for _, e := range l {
		if e == s {
			return true
		}
	}
	return false
}

// A snapshot of the command tree: every command with its flags, so a removed or renamed command or option shows up in review.
func commandSnapshot() string {
	st := &state{io: IO{Out: io.Discard, Err: io.Discard, Getenv: func(string) string { return "" }}}
	var lines []string
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		var flags []string
		c.LocalFlags().VisitAll(func(f *pflag.Flag) { flags = append(flags, "--"+f.Name+" "+f.Value.Type()) })
		sort.Strings(flags)
		lines = append(lines, strings.TrimSpace(path)+" | "+strings.Join(flags, ", "))
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			walk(sub, path+" "+sub.Name())
		}
	}
	root := st.root()
	walk(root, "unravis")
	sort.Strings(lines[1:])
	return strings.Join(lines, "\n") + "\n"
}

func TestSnapshotCommandTree(t *testing.T) {
	const golden = "testdata/commands.golden"
	got := commandSnapshot()
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("no snapshot: run with UPDATE_GOLDEN=1 (%v)", err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got { // a Windows checkout may give the file CRLF line ends
		t.Errorf("the command tree changed. If it is meant, run: UPDATE_GOLDEN=1 go test ./internal/clicore/cmdtree -run SnapshotCommandTree\n--- snapshot\n%s--- now\n%s", strings.ReplaceAll(string(want), "\r\n", "\n"), got)
	}
}
