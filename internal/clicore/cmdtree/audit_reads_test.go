package cmdtree

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"api-audit-explorer/internal/clicap"
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
			if !names[f.Name] {
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
