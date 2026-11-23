package cmdtree

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

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
	return map[string]any{"query": q}
}

// strip removes what differs between two runs of the same question: the handle, the cursors and the time a query took.
func strip(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if k == "resultId" || k == "nextCursor" || k == "elapsedMs" {
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
		if !reflect.DeepEqual(strip(got), strip(want)) {
			t.Errorf("%s differs:\ncli %v\nmcp %v", c.tool, strip(got), strip(want))
		}
	}
	// sources and the guide
	if got, want := cliRequest(t, o, []string{"sources", "list"}, map[string]any{}), mcpData(t, b, "audit_list_sources", map[string]any{}); !reflect.DeepEqual(strip(got), strip(want)) {
		t.Errorf("sources differ:\ncli %v\nmcp %v", got, want)
	}
}

// The same bad question is refused with the same category on both sides: boundaries of the time windows and the owner's own checks.
func TestParityBoundaries(t *testing.T) {
	o := newOwner(t, nil)
	b := o.bridge()
	base := o.windowArgs()
	mut := map[string]func(q, top map[string]any){
		"end before start":    func(q, _ map[string]any) { q["start"], q["end"] = q["end"], q["start"] },
		"baseline overlaps":   func(q, _ map[string]any) { q["baselineStart"], q["baselineEnd"] = q["start"], q["end"] },
		"no sources":          func(q, _ map[string]any) { q["sourceIds"] = []string{} },
		"unknown source":      func(q, _ map[string]any) { q["sourceIds"] = []string{"no-such-source"} },
		"window without zone": func(q, _ map[string]any) { q["start"] = "2026-01-01T00:00:00" },
		"limit too big":       func(_, top map[string]any) { top["limit"] = 1000 },
	}
	names := make([]string, 0, len(mut))
	for n := range mut {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		q := map[string]any{}
		for k, v := range base["query"].(map[string]any) {
			q[k] = v
		}
		args := map[string]any{"query": q}
		mut[name](q, args)
		raw, _ := json.Marshal(args)
		r := runOn(t, o.attacher(), string(raw), nil, "audit", "compare", "--request", "-", "--json")
		_, err := b.Call(context.Background(), "audit_compare", raw)
		if r.code == 0 || err == nil {
			t.Errorf("%s: cli exit %d, mcp err %v", name, r.code, err)
			continue
		}
		want := clicore.FromMCP(mcp.Code(err), false, "")
		got := errCode(t, r)
		if got != string(want) {
			t.Errorf("%s: cli %s, mcp %s", name, got, want)
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
		case catalog[r.Op]:
			t.Errorf("%s is a catalog name with no tool row", r.Op)
		}
	}
	if len(catalog) != 0 {
		t.Errorf("tools with no operation: %v", catalog)
	}
}

// requiredMissing lists the fields that a schema requires and the data does not have, down into objects and the items of lists.
func requiredMissing(schema map[string]any, data any, path string) []string {
	var out []string
	switch d := data.(type) {
	case map[string]any:
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, has := d[r.(string)]; !has {
					out = append(out, path+"."+r.(string))
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for k, p := range props {
			if v, has := d[k]; has {
				if ps, ok := p.(map[string]any); ok {
					out = append(out, requiredMissing(ps, v, path+"."+k)...)
				}
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, e := range d {
				out = append(out, requiredMissing(items, e, path+"[]")...)
				_ = i
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
		"app.status":       data(t, o.run("app", "status", "--json")),
		"cases.list":       list,
		"cases.show":       data(t, o.run("cases", "show", id, "--json")),
		"doctor":           data(t, o.run("doctor", "--json")),
		"version":          data(t, o.run("version", "--json")),
		"app.start":        data(t, runStart(t, o, &starter{pid: 1}, "app", "start", "--headless", "--json")),
		"app.stop":         nil,
		"blackbox.inspect": nil,
	}
	delete(answers, "app.stop")
	delete(answers, "blackbox.inspect")
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
		if miss := requiredMissing(out, any(got), "data"); len(miss) != 0 {
			t.Errorf("%s: the answer lacks %v", op, miss)
		}
		// take one required field away from a copy: the check must see it
		req, _ := out["required"].([]any)
		if len(req) == 0 {
			continue
		}
		cp := map[string]any{}
		for k, v := range got {
			cp[k] = v
		}
		delete(cp, req[0].(string))
		if miss := requiredMissing(out, any(cp), "data"); len(miss) == 0 {
			t.Errorf("%s: a removed field %v was not noticed", op, req[0])
		}
	}
}

// A snapshot of the command tree: every command with its flags, so a removed or renamed command or option shows up in review.
func commandSnapshot() string {
	st := &state{io: IO{Out: nopWriter{}, Err: nopWriter{}, Getenv: func(string) string { return "" }}}
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

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

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
	want, err := os.ReadFile(golden) //nolint:gosec // the golden file of this test
	if err != nil {
		t.Fatalf("no snapshot: run with UPDATE_GOLDEN=1 (%v)", err)
	}
	if string(want) != got {
		t.Errorf("the command tree changed. If it is meant, run: UPDATE_GOLDEN=1 go test ./internal/clicore/cmdtree -run SnapshotCommandTree\n--- snapshot\n%s--- now\n%s", want, got)
	}
}
