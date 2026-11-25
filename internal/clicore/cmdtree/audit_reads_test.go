package cmdtree

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/mcp"
)

// compareKey runs a comparison on the demo owner and returns its result id and the key of its first group.
func compareKey(t *testing.T, o *owner) (id, key string) {
	t.Helper()
	d := data(t, o.run(append([]string{"audit", "compare", "--json"}, o.window()...)...))
	id, _ = d["resultId"].(string)
	g := d["groups"].([]any)[0].(map[string]any)
	key, _ = g["key"].(string)
	if len(id) != 32 || key == "" {
		t.Fatalf("no handle or key: %v", d)
	}
	return id, key
}

func lastArgs(o *owner) map[string]any {
	s := o.opsSeen()
	return s[len(s)-1]["args"].(map[string]any)
}

func TestReadCommandsOnTheStoredComparison(t *testing.T) {
	o := newOwner(t, nil)
	id, key := compareKey(t, o)

	d := data(t, o.run("audit", "timeline", "--result-id", id, "--api-key", key, "--json"))
	if len(d["points"].([]any)) == 0 || d["group"].(map[string]any)["key"] != key {
		t.Errorf("timeline: %v", d)
	}
	if a := lastArgs(o); len(a) != 2 || a["resultId"] != id || a["apiKey"] != key {
		t.Errorf("timeline args: %v", a)
	}

	// two keys are sent as a list; the owner refuses the key it does not know with NOT_FOUND
	if r := o.run("audit", "timelines", "compare", "--result-id", id, "--api-key", key, "--api-key", key+"x", "--json"); r.code != 5 || errCode(t, r) != "NOT_FOUND" {
		t.Errorf("unknown key: %+v", r)
	}
	if a := lastArgs(o); len(a) != 2 || a["resultId"] != id || len(a["apiKeys"].([]any)) != 2 {
		t.Errorf("timelines compare args: %v", a)
	}

	d = data(t, o.run("audit", "timelines", "compare", "--result-id", id, "--api-key", key, "--json"))
	if len(d["slots"].([]any)) == 0 {
		t.Errorf("timelines compare: %v", d)
	}

	d = data(t, o.run("audit", "requests", "--result-id", id, "--api-key", key, "--limit", "3", "--json"))
	rows := d["rows"].([]any)
	if len(rows) != 3 || d["nextCursor"] == "" {
		t.Fatalf("requests: %v", d)
	}
	if a := lastArgs(o); len(a) != 3 || a["limit"] != float64(3) {
		t.Errorf("requests args: %v", a)
	}
	// the next page starts after the first one
	first := rows[0].(map[string]any)["id"]
	d2 := data(t, o.run("audit", "requests", "--result-id", id, "--api-key", key, "--limit", "3", "--cursor", d["nextCursor"].(string), "--json"))
	if a := lastArgs(o); a["cursor"] != d["nextCursor"] {
		t.Errorf("cursor not sent: %v", a)
	}
	if d2["rows"].([]any)[0].(map[string]any)["id"] == first {
		t.Errorf("the second page repeats the first")
	}

	d = data(t, o.run("audit", "slowest", "--result-id", id, "--limit", "2", "--min-ms", "100", "--json"))
	if n := len(d["rows"].([]any)); n == 0 || n > 2 {
		t.Errorf("slowest: %v", d)
	}
	if a := lastArgs(o); a["minMs"] != float64(100) || a["limit"] != float64(2) || len(a) != 3 {
		t.Errorf("slowest args: %v", a)
	}

	d = data(t, o.run("audit", "breakdown", "--result-id", id, "--dim", "statusCode", "--top", "5", "--include-ip", "--json"))
	if len(d["rows"].([]any)) == 0 {
		t.Errorf("breakdown: %v", d)
	}
	if a := lastArgs(o); a["dim"] != "statusCode" || a["top"] != float64(5) || a["includeIp"] != true || len(a) != 4 {
		t.Errorf("breakdown args: %v", a)
	}

	w := o.window() // --source a --source b --start .. --end .. --baseline-start .. --baseline-end ..
	d = data(t, o.run("audit", "activity", "--source", "demo-west", "--source", "demo-east", "--start", w[5], "--end", w[7], "--limit", "3", "--json"))
	if len(d["rows"].([]any)) == 0 {
		t.Errorf("activity: %v", d)
	}
	if a := lastArgs(o); len(a["sourceIds"].([]any)) != 2 || a["start"] != w[5] || a["end"] != w[7] || a["limit"] != float64(3) || len(a) != 4 {
		t.Errorf("activity args: %v", a)
	}
}

func TestReadCommandTables(t *testing.T) {
	o := newOwner(t, nil)
	id, key := compareKey(t, o)
	req := o.run("audit", "requests", "--result-id", id, "--api-key", key, "--limit", "2")
	if req.code != 0 || !strings.Contains(req.out, "demo-east-Employees-current-") || !strings.Contains(req.out, "200 OK") || !strings.Contains(req.out, "More: add --cursor ") {
		t.Errorf("requests table: %+v", req)
	}
	if strings.Contains(req.out, "SOURCE") && !strings.Contains(req.out, "demo-east ") {
		t.Errorf("source column without a source: %q", req.out)
	}
	sl := o.run("audit", "slowest", "--result-id", id, "--limit", "2")
	if sl.code != 0 || !strings.Contains(sl.out, "Payroll.Run") || !strings.Contains(sl.out, "260") {
		t.Errorf("slowest table: %+v", sl)
	}
	bd := o.run("audit", "breakdown", "--result-id", id, "--dim", "statusCode")
	if bd.code != 0 || !strings.Contains(bd.out, "200 OK") || !strings.Contains(bd.out, "1500") || !strings.Contains(bd.out, "requests in total") {
		t.Errorf("breakdown table: %+v", bd)
	}
	w := o.window()
	ac := o.run("audit", "activity", "--source", "demo-west", "--start", w[5], "--end", w[7], "--limit", "2")
	if ac.code != 0 || !strings.Contains(ac.out, "demo-west") || !strings.Contains(ac.out, "Generate") {
		t.Errorf("activity table: %+v", ac)
	}
	tl := o.run("audit", "timeline", "--result-id", id, "--api-key", key)
	if tl.code != 0 || !strings.Contains(tl.out, "points") {
		t.Errorf("timeline text: %+v", tl)
	}
}

func TestReadCommandUsageErrors(t *testing.T) {
	o := newOwner(t, nil)
	id, key := compareKey(t, o)
	before := len(o.opsSeen())
	w := o.window()
	for name, args := range map[string][]string{
		"timeline no key":     {"audit", "timeline", "--result-id", id},
		"timeline no result":  {"audit", "timeline", "--api-key", key},
		"empty key":           {"audit", "timeline", "--result-id", id, "--api-key", ""},
		"requests no key":     {"audit", "requests", "--result-id", id},
		"slowest no result":   {"audit", "slowest"},
		"breakdown no dim":    {"audit", "breakdown", "--result-id", id},
		"compare no keys":     {"audit", "timelines", "compare", "--result-id", id},
		"empty key in list":   {"audit", "timelines", "compare", "--result-id", id, "--api-key", ""},
		"activity no source":  {"audit", "activity", "--start", w[5], "--end", w[7]},
		"activity bad time":   {"audit", "activity", "--source", "a", "--start", "yesterday", "--end", w[7]},
		"activity no end":     {"audit", "activity", "--source", "a", "--start", w[5]},
		"limit is not number": {"audit", "slowest", "--result-id", id, "--limit", "many"},
		"request and flag":    {"audit", "slowest", "--result-id", id, "--request", "-"},
		"stray argument":      {"audit", "slowest", "--result-id", id, "extra"},
	} {
		r := o.run(append(args, "--json")...)
		if r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if n := len(o.opsSeen()); n != before {
		t.Errorf("a usage error reached the owner (%d requests)", n-before)
	}
	// the owner refuses a value out of range or a key it does not know, with the owner's code
	if r := o.run("audit", "slowest", "--result-id", id, "--limit", "9999", "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
		t.Errorf("limit: %+v", r)
	}
	if r := o.run("audit", "timeline", "--result-id", id, "--api-key", "nokey", "--json"); r.code != 5 && r.code != 2 {
		t.Errorf("unknown key: %+v", r)
	}
	if r := o.run("audit", "requests", "--result-id", "00000000000000000000000000000000", "--api-key", key, "--json"); r.code != 5 || errCode(t, r) != "RESULT_EXPIRED" {
		t.Errorf("expired: %+v", r)
	}
}

func TestReadCommandsAcceptARequestFile(t *testing.T) {
	o := newOwner(t, nil)
	id, _ := compareKey(t, o)
	r := runOn(t, o.attacher(), `{"resultId":"`+id+`","dim":"uiVersion"}`, nil, "audit", "breakdown", "--request", "-", "--json")
	if r.code != 0 || lastArgs(o)["dim"] != "uiVersion" {
		t.Errorf("%+v", r)
	}
}

// Every read command: its tool is the one the matrix names, every flag sets a field of that tool, every required field of the tool has a
// required flag, and every flag of the command is in the spec.
func TestReadFlagsMatchToolSchemas(t *testing.T) {
	schemas := map[string]map[string]any{}
	required := map[string][]any{}
	for _, c := range mcp.Catalog() {
		in := c["inputSchema"].(map[string]any)
		schemas[c["name"].(string)] = in["properties"].(map[string]any)
		req, _ := in["required"].([]any)
		if req == nil {
			if rs, ok := in["required"].([]string); ok {
				for _, s := range rs {
					req = append(req, s)
				}
			}
		}
		required[c["name"].(string)] = req
	}
	st := &state{}
	for _, sp := range allReadSpecs() {
		row, ok := clicap.Lookup(sp.op)
		if !ok || row.Tool == "" {
			t.Fatalf("%s has no tool in the matrix", sp.op)
		}
		props, ok := schemas[row.Tool]
		if !ok {
			t.Fatalf("%s: tool %s is not in the catalog", sp.op, row.Tool)
		}
		byField := map[string]fieldFlag{}
		for _, f := range sp.flags {
			if _, ok := props[f.field]; !ok {
				t.Errorf("%s --%s sets %s, which %s does not have", sp.op, f.name, f.field, row.Tool)
			}
			byField[f.field] = f
		}
		// the kind of each flag is the type of the field, and every field of the tool has a flag
		for field, raw := range props {
			f, ok := byField[field]
			if !ok {
				t.Errorf("%s: the tool field %s has no flag", sp.op, field)
				continue
			}
			prop := raw.(map[string]any)
			want := map[flagKind]string{kString: "string", kTime: "string", kStrings: "array", kInt: "integer", kFloat: "number", kBool: "boolean"}[f.kind]
			if prop["type"] != want {
				t.Errorf("%s --%s is a %s flag and the field %s is %v", sp.op, f.name, want, field, prop["type"])
			}
			if _, isEnum := prop["enum"]; isEnum && f.kind != kString {
				t.Errorf("%s --%s: an enum field needs a string flag", sp.op, f.name)
			}
		}
		for _, r := range required[row.Tool] {
			if f, ok := byField[r.(string)]; !ok || !f.required {
				t.Errorf("%s: the tool requires %v and the command has no required flag for it", sp.op, r)
			}
		}
		for _, f := range sp.flags {
			if f.required {
				found := false
				for _, r := range required[row.Tool] {
					found = found || r.(string) == f.field
				}
				if !found {
					t.Errorf("%s --%s is required here but not by the tool", sp.op, f.name)
				}
			}
		}
		cmd := sp.command(st)
		names := map[string]bool{}
		for _, f := range sp.flags {
			names[f.name] = true
		}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			// --max-rows and --max-bytes belong to the stream of the command (spec 3.3), not to a tool field
			stream := sp.page > 0 && (f.Name == "max-rows" || f.Name == "max-bytes")
			if !names[f.Name] && !stream {
				t.Errorf("%s --%s is in no spec", sp.op, f.Name)
			}
		})
	}
	// the ten operations of the spec table all have a command or are one of the earlier ones
	have := map[string]bool{"audit.compare": true, "audit.groups": true, "audit.guide": true, "sources.list": true}
	for _, sp := range allReadSpecs() {
		have[sp.op] = true
	}
	for _, row := range clicap.Rows() {
		if row.Tool != "" && !have[row.Op] {
			t.Errorf("%s (%s) has no command", row.Op, row.Tool)
		}
	}
}

func TestReadArgumentsEdges(t *testing.T) {
	o := newOwner(t, nil)
	id, key := compareKey(t, o)
	// zero values that were asked for are sent: the flag was set, so the tool sees it
	o.run("audit", "breakdown", "--result-id", id, "--dim", "statusCode", "--top", "0", "--include-ip=false", "--json")
	if a := lastArgs(o); a["top"] != float64(0) || a["includeIp"] != false || len(a) != 4 {
		t.Errorf("zero values: %v", a)
	}
	if r := o.run("audit", "requests", "--result-id", id, "--api-key", key, "--limit", "0", "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" || lastArgs(o)["limit"] != float64(0) {
		t.Errorf("limit 0 is the owner's to refuse: %+v", r)
	}
	o.run("audit", "slowest", "--result-id", id, "--min-ms", "0", "--json")
	if a := lastArgs(o); a["minMs"] != float64(0) || len(a) != 2 {
		t.Errorf("min-ms 0: %v", a)
	}
	// numbers that JSON cannot carry are a usage error, before anything is sent
	before := len(o.opsSeen())
	for _, v := range []string{"NaN", "inf", "+Inf", "-Infinity"} {
		if r := o.run("audit", "slowest", "--result-id", id, "--min-ms", v, "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
			t.Errorf("%s: %+v", v, r)
		}
	}
	if len(o.opsSeen()) != before {
		t.Error("a bad number reached the owner")
	}
	// a time with an offset and a fraction goes out as UTC with the fraction kept
	o.run("audit", "activity", "--source", "demo-west", "--start", "2026-11-23T10:00:00.250+05:30", "--end", "2026-11-23T11:00:00.999+05:30", "--json")
	if a := lastArgs(o); a["start"] != "2026-11-23T04:30:00.25Z" || a["end"] != "2026-11-23T05:30:00.999Z" {
		t.Errorf("times: %v", a)
	}
	// the cursor of slowest and of activity is sent
	o.run("audit", "slowest", "--result-id", id, "--cursor", "abc", "--json")
	if lastArgs(o)["cursor"] != "abc" {
		t.Errorf("slowest cursor: %v", lastArgs(o))
	}
	w := o.window()
	o.run("audit", "activity", "--source", "demo-west", "--start", w[5], "--end", w[7], "--cursor", "xyz", "--json")
	if lastArgs(o)["cursor"] != "xyz" {
		t.Errorf("activity cursor: %v", lastArgs(o))
	}
}

func TestReadTablesInDetail(t *testing.T) {
	o := newOwner(t, nil)
	id, key := compareKey(t, o)
	// the source and the API come from the top of an answer that has no such column in its rows
	req := o.run("audit", "requests", "--result-id", id, "--api-key", key, "--limit", "1")
	if !strings.Contains(req.out, "SOURCE") || !strings.Contains(req.out, "demo-east") || !strings.Contains(req.out, "Employees.List") {
		t.Errorf("requests: %q", req.out)
	}
	// activity rows have no id and no state, so those columns are not printed
	w := o.window()
	ac := o.run("audit", "activity", "--source", "demo-west", "--start", w[5], "--end", w[7], "--limit", "1")
	head := strings.SplitN(ac.out, "\n", 2)[0]
	if strings.Contains(head, "ID") || strings.Contains(head, "STATE") || !strings.Contains(head, "API") {
		t.Errorf("activity header: %q", head)
	}
	// a breakdown with a remainder prints it last, labelled by how many values it folds
	bd := o.run("audit", "breakdown", "--result-id", id, "--dim", "userAgent", "--top", "1")
	if bd.code != 0 || !strings.Contains(bd.out, "(other: ") || !strings.Contains(bd.out, " values)") {
		t.Errorf("remainder: %q", bd.out)
	}
	lines := strings.Split(strings.TrimSpace(bd.out), "\n")
	cols := strings.Fields(lines[1])
	if len(cols) < 3 || cols[1] == "" {
		t.Fatalf("row: %q", lines[1])
	}
	// COUNT is the count of requests, not of the complete ones (in the demo data they differ for some rows or are equal: check the header order)
	if !strings.HasPrefix(strings.Join(strings.Fields(lines[0]), " "), "VALUE COUNT COMPLETE INCOMPLETE PENDING 4XX 5XX") {
		t.Errorf("header: %q", lines[0])
	}
}

func TestDropEmpty(t *testing.T) {
	h, r := dropEmpty([]string{"A", "B", "C"}, [][]string{{"x"}, {"y", "", "z"}})
	if len(h) != 2 || h[0] != "A" || h[1] != "C" || r[0][0] != "x" || r[0][1] != "" || r[1][1] != "z" {
		t.Errorf("%v %v", h, r)
	}
	h, r = dropEmpty([]string{"A", "B"}, nil)
	if len(h) != 2 || len(r) != 0 {
		t.Errorf("no rows keep the header: %v %v", h, r)
	}
}

func TestBreakdownColumnsReadTheirOwnFields(t *testing.T) {
	o := newOwner(t, fakeBackend(func(_ context.Context, _ string, _ json.RawMessage) (any, *clicore.Error) {
		return map[string]any{"identifiers": "hidden", "total": 100, "rows": []map[string]any{
			{"value": "hidden-1", "count": 11, "complete": 22, "incomplete": 33, "pending": 44, "count4xx": 55, "count5xx": 66},
		}, "other": map[string]any{"value": "Other", "count": 7, "values": 3}}, nil
	}))
	r := o.run("audit", "breakdown", "--result-id", "x", "--dim", "userId")
	row := strings.Fields(strings.Split(r.out, "\n")[1])
	if r.code != 0 || strings.Join(row, " ") != "hidden-1 11 22 33 44 55 66" {
		t.Errorf("columns: %q", r.out)
	}
	if !strings.Contains(r.out, "(other: 3 values)") || !strings.Contains(r.out, "hidden-1, hidden-2") || !strings.Contains(r.out, "100 requests in total") {
		t.Errorf("%q", r.out)
	}
}
