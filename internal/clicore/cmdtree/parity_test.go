package cmdtree

import (
	"bytes"
	"context"
	"crypto/sha256"
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

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/clisrv"
	"api-audit-explorer/internal/mcp"
)

// Parity of the adapters (spec 05 BL-393). What this proves, and what it does not:
//   - the command line and the MCP bridge give the same data. Both run the same bridge code on the same dashboard handler, so this
//     checks the command line's envelope, flags and error mapping, not the tool code itself;
//   - the tool code is held by an independent side: the dashboard's own routes (compare, timelines, activity) and the pinned
//     followups.golden (groups, timeline, requests, slowest, breakdown, guide, sources);
//   - the data set is the demo owner only: no real source, no real Windows pipe.

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

func dashboardJSON(t *testing.T, o *owner, path string, body any) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("X-Audit-Token", ownerToken)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr, req.Host = "127.0.0.1:0", "127.0.0.1"
	rec := httptest.NewRecorder()
	o.dash.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard %s: %d %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

func jsonValue(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func checkTimelinesAgainstDashboard(t *testing.T, o *owner, keys []string, got map[string]any) {
	t.Helper()
	q := o.windowArgs()["query"]
	var raw audit.Analysis
	if err := json.Unmarshal(dashboardJSON(t, o, "/api/analyze", q), &raw); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["query"], jsonValue(t, raw.Query)) {
		t.Errorf("timeline query differs from the dashboard: %v vs %v", got["query"], jsonValue(t, raw.Query))
	}
	groups := map[string]audit.Comparison{}
	for _, g := range raw.APIs {
		groups[g.Key] = g
	}
	shownGroups, ok := got["groups"].([]any)
	if !ok || len(shownGroups) != len(keys) {
		t.Fatalf("timeline groups: %v", got["groups"])
	}
	points := map[string]map[int64]audit.Point{}
	for _, series := range raw.Timeline {
		points[series.Key] = map[int64]audit.Point{}
		for _, p := range series.Points {
			points[series.Key][p.At.UnixNano()] = p
		}
	}
	for i, key := range keys {
		g, ok := groups[key]
		if !ok {
			t.Fatalf("dashboard has no group %q", key)
		}
		if !reflect.DeepEqual(shownGroups[i], jsonValue(t, g)) {
			t.Errorf("timeline group %d differs from the dashboard", i)
		}
	}
	slots, ok := got["slots"].([]any)
	if !ok {
		t.Fatalf("timeline slots: %v", got["slots"])
	}
	width := time.Duration(raw.Query.BucketSeconds) * time.Second
	seconds := int64(raw.Query.BucketSeconds)
	epoch := raw.Query.Start.Unix() / seconds
	if raw.Query.Start.Unix()%seconds < 0 {
		epoch--
	}
	first := time.Unix(epoch*seconds, 0).UTC()
	var wantSlots int
	for at := first; at.Before(raw.Query.End); at = at.Add(width) {
		wantSlots++
	}
	if len(slots) != wantSlots {
		t.Fatalf("timeline has %d slots, dashboard query implies %d", len(slots), wantSlots)
	}
	for i, value := range slots {
		slot := value.(map[string]any)
		at, err := time.Parse(time.RFC3339Nano, slot["at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		start, end := at, at.Add(width)
		if start.Before(raw.Query.Start) {
			start = raw.Query.Start
		}
		if end.After(raw.Query.End) {
			end = raw.Query.End
		}
		if slot["start"] != start.UTC().Format(time.RFC3339Nano) || slot["end"] != end.UTC().Format(time.RFC3339Nano) {
			t.Errorf("slot %d boundaries differ: %v", i, slot)
		}
		cells := slot["groups"].([]any)
		if len(cells) != len(keys) {
			t.Fatalf("slot %d has %d groups", i, len(cells))
		}
		for j, key := range keys {
			g := groups[key]
			p, observed := points[key][at.UnixNano()]
			var current, baseline, delta, percent any
			if g.Reference.P95 != nil && g.Reference.P95.ValueMs != nil {
				baseline = *g.Reference.P95.ValueMs
			}
			if observed && p.P95 != nil && p.P95.ValueMs != nil {
				current = *p.P95.ValueMs
				if base, ok := baseline.(float64); ok {
					delta = *p.P95.ValueMs - base
					if base > 0 && g.Reference.P95.LowerMs > 0 {
						percent = (*p.P95.ValueMs/base - 1) * 100
					}
				}
			}
			status := "missing"
			if observed && p.Count > 0 {
				status = "few_samples"
				if p.Count >= audit.MinSampleCount && g.State == "compared" {
					status = "enough_samples"
				}
			}
			want := map[string]any{"apiKey": key, "count": float64(p.Count), "sampleStatus": status, "currentP95Ms": current,
				"baselineP95Ms": baseline, "increaseMs": delta, "increasePercent": percent}
			if !reflect.DeepEqual(cells[j], want) {
				t.Errorf("slot %d group %d differs from dashboard data:\ngot  %v\nwant %v", i, j, cells[j], want)
			}
		}
	}
}

func checkActivityAgainstDashboard(t *testing.T, o *owner, input map[string]any, got map[string]any) {
	t.Helper()
	var raw audit.Page
	if err := json.Unmarshal(dashboardJSON(t, o, "/api/activity", input), &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"start", "end", "sourceIds"} {
		if !reflect.DeepEqual(got[field], jsonValue(t, input[field])) {
			t.Errorf("activity %s differs: %v vs %v", field, got[field], input[field])
		}
	}
	if !reflect.DeepEqual(got["sources"], jsonValue(t, raw.Sources)) || got["truncated"] != raw.Truncated {
		t.Errorf("activity coverage differs: %v vs %v", got, raw)
	}
	if (got["nextCursor"] != nil && got["nextCursor"] != "") != (raw.NextCursor != "") {
		t.Errorf("activity paging differs: %v vs %q", got["nextCursor"], raw.NextCursor)
	}
	rows, ok := got["rows"].([]any)
	if !ok || len(rows) != len(raw.Rows) {
		t.Fatalf("activity rows: %v vs %d", got["rows"], len(raw.Rows))
	}
	for i, r := range raw.Rows {
		want := jsonValue(t, map[string]any{"sourceId": r.SourceID, "controller": r.Controller, "action": r.Action, "kind": r.Kind, "outcome": r.Outcome,
			"initiatedAt": r.InitiatedAt, "completedAt": r.CompletedAt, "durationMs": r.DurationMs, "apiTimeMs": r.ApiTimeMs,
			"auditCreateTimeMs": r.AuditCreateTimeMs, "payloadRecordTimeMs": r.PayloadRecordTimeMs})
		if !reflect.DeepEqual(rows[i], want) {
			t.Errorf("activity row %d differs from dashboard data:\ngot  %v\nwant %v", i, rows[i], want)
		}
	}
}

func TestParityCompareAndFollowUps(t *testing.T) {
	o := newOwner(t, nil, fixedClock)
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
	// Each follow-up is run once with its own flags (the way a person types it), not with a raw request, and is compared with the MCP
	// bridge AND with a pinned golden (followups.golden). The bridge and the command line run the same tool code, so only the golden
	// (and the raw dashboard legs above and below) can tell that the tool code itself changed.
	golden := map[string]any{}
	for _, c := range []struct {
		op    []string
		tool  string
		flags []string
		mcp   map[string]any
	}{
		{[]string{"audit", "groups"}, "audit_get_groups", []string{"--result-id", cid, "--limit", "5"}, map[string]any{"resultId": mid, "limit": 5}},
		{[]string{"audit", "timeline"}, "audit_get_timeline", []string{"--result-id", cid, "--api-key", key}, map[string]any{"resultId": mid, "apiKey": key}},
		{[]string{"audit", "requests"}, "audit_get_requests", []string{"--result-id", cid, "--api-key", key, "--limit", "4"}, map[string]any{"resultId": mid, "apiKey": key, "limit": 4}},
		{[]string{"audit", "slowest"}, "audit_get_slowest", []string{"--result-id", cid, "--limit", "3"}, map[string]any{"resultId": mid, "limit": 3}},
		{[]string{"audit", "breakdown"}, "audit_get_breakdown", []string{"--result-id", cid, "--dim", "statusCode"}, map[string]any{"resultId": mid, "dim": "statusCode"}},
		{[]string{"audit", "guide"}, "audit_get_guide", nil, map[string]any{}},
		{[]string{"sources", "list"}, "audit_list_sources", nil, map[string]any{}},
	} {
		got := data(t, o.run(append(append(append([]string(nil), c.op...), c.flags...), "--json")...))
		want := mcpData(t, b, c.tool, c.mcp)
		if len(got) == 0 || len(want) == 0 {
			t.Errorf("%s: an empty answer (cli %d fields, mcp %d)", c.tool, len(got), len(want))
		}
		if !reflect.DeepEqual(strip(got), strip(want)) {
			t.Errorf("%s differs:\ncli %v\nmcp %v", c.tool, strip(got), strip(want))
		}
		golden[c.tool] = relativeTimes(strip(got), o.now)
	}
	checkFollowUpGolden(t, golden)
	selected := keys(cli)
	if len(selected) < 2 {
		t.Fatal("the comparison has fewer than two groups")
	}
	tlCLI := cliRequest(t, o, []string{"audit", "timelines", "compare"}, map[string]any{"resultId": cid, "apiKeys": selected[:2]})
	tlMCP := mcpData(t, b, "audit_compare_timelines", map[string]any{"resultId": mid, "apiKeys": selected[:2]})
	if !reflect.DeepEqual(strip(tlCLI), strip(tlMCP)) {
		t.Errorf("timeline compare differs:\ncli %v\nmcp %v", strip(tlCLI), strip(tlMCP))
	}
	checkTimelinesAgainstDashboard(t, o, selected[:2], tlCLI)
	q := args["query"].(map[string]any)
	activity := map[string]any{"sourceIds": q["sourceIds"], "start": q["start"], "end": q["end"], "limit": 3}
	actCLI := cliRequest(t, o, []string{"audit", "activity"}, activity)
	actMCP := mcpData(t, b, "audit_get_activity", activity)
	if !reflect.DeepEqual(strip(actCLI), strip(actMCP)) {
		t.Errorf("activity differs:\ncli %v\nmcp %v", strip(actCLI), strip(actMCP))
	}
	checkActivityAgainstDashboard(t, o, activity, actCLI)
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

func TestParityReadBoundaries(t *testing.T) {
	o := newOwner(t, nil)
	b := o.bridge()
	compare := o.windowArgs()
	cid := cliRequest(t, o, []string{"audit", "compare"}, compare)["resultId"].(string)
	mid := mcpData(t, b, "audit_compare", compare)["resultId"].(string)
	var key string
	for _, g := range mcpData(t, b, "audit_get_groups", map[string]any{"resultId": mid})["groups"].([]any) {
		key = g.(map[string]any)["key"].(string)
		break
	}
	q := compare["query"].(map[string]any)
	for _, c := range []struct {
		name string
		op   []string
		tool string
		args map[string]any
		want clicore.Code
	}{
		{"guide takes no arguments", []string{"audit", "guide"}, "audit_get_guide", map[string]any{"extra": true}, clicore.CodeInvalidArgument},
		{"timeline compare needs keys", []string{"audit", "timelines", "compare"}, "audit_compare_timelines", map[string]any{"resultId": cid}, clicore.CodeInvalidArgument},
		{"timeline compare refuses duplicates", []string{"audit", "timelines", "compare"}, "audit_compare_timelines", map[string]any{"resultId": cid, "apiKeys": []string{key, key}}, clicore.CodeInvalidArgument},
		{"timeline compare refuses unknown key", []string{"audit", "timelines", "compare"}, "audit_compare_timelines", map[string]any{"resultId": cid, "apiKeys": []string{"no-such-key"}}, clicore.CodeNotFound},
		{"activity end before start", []string{"audit", "activity"}, "audit_get_activity", map[string]any{"sourceIds": q["sourceIds"], "start": q["end"], "end": q["start"], "limit": 3}, clicore.CodeInvalidArgument},
		{"activity requires a source", []string{"audit", "activity"}, "audit_get_activity", map[string]any{"sourceIds": []string{}, "start": q["start"], "end": q["end"], "limit": 3}, clicore.CodeInvalidArgument},
		{"activity window too long", []string{"audit", "activity"}, "audit_get_activity", map[string]any{"sourceIds": q["sourceIds"], "start": q["start"], "end": o.now.Add(25 * time.Hour).Format(time.RFC3339), "limit": 3}, clicore.CodeInvalidArgument},
		{"activity limit too large", []string{"audit", "activity"}, "audit_get_activity", map[string]any{"sourceIds": q["sourceIds"], "start": q["start"], "end": q["end"], "limit": 101}, clicore.CodeInvalidArgument},
		{"activity unknown source", []string{"audit", "activity"}, "audit_get_activity", map[string]any{"sourceIds": []string{"no-such-source"}, "start": q["start"], "end": q["end"], "limit": 3}, clicore.CodeNotFound},
	} {
		cliArgs := c.args
		mcpArgs := c.args
		if c.tool == "audit_compare_timelines" {
			mcpArgs = map[string]any{"resultId": mid}
			if keys, ok := c.args["apiKeys"]; ok {
				mcpArgs["apiKeys"] = keys
			}
		}
		rawCLI, _ := json.Marshal(cliArgs)
		rawMCP, _ := json.Marshal(mcpArgs)
		r := runOn(t, o.attacher(), string(rawCLI), nil, append(c.op, "--request", "-", "--json")...)
		_, err := b.Call(context.Background(), c.tool, rawMCP)
		if r.code == 0 || err == nil {
			t.Errorf("%s: cli exit %d, mcp err %v", c.name, r.code, err)
			continue
		}
		gotCLI, gotMCP := errCode(t, r), string(clicore.FromMCP(mcp.Code(err), false, ""))
		if gotCLI != string(c.want) || gotMCP != string(c.want) {
			t.Errorf("%s: cli %s, mcp %s, expected %s", c.name, gotCLI, gotMCP, c.want)
		}
	}
}

func TestParityCancellation(t *testing.T) {
	started := make(chan struct{}, 3)
	observed := make(chan string, 3)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		observed <- r.URL.Path
	})
	bridge := mcp.NewLocal(handler, ownerToken, mcp.WithIdentifiers(true))
	o := newOwner(t, clisrv.MCPBackend(bridge))
	ctx, cancel := context.WithCancel(context.Background())
	cliDone := make(chan result, 1)
	go func() {
		cliDone <- runCtx(t, ctx, o.attacher(), "", nil, "sources", "list", "--timeout", "30s", "--json")
	}()
	<-started
	cancel()
	select {
	case r := <-cliDone:
		if r.code != clicore.ExitStopped || errCode(t, r) != "CANCELED" {
			t.Errorf("CLI cancellation: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not stop at cancellation")
	}

	mcpCtx, stopMCP := context.WithCancel(context.Background())
	mcpDone := make(chan error, 1)
	go func() {
		_, err := bridge.Call(mcpCtx, "audit_list_sources", json.RawMessage(`{}`))
		mcpDone <- err
	}()
	<-started
	stopMCP()
	select {
	case err := <-mcpDone:
		if mcp.Code(err) != mcp.CodeCancelled {
			t.Errorf("MCP cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MCP did not stop at cancellation")
	}

	guiCtx, stopGUI := context.WithCancel(context.Background())
	guiDone := make(chan struct{})
	go func() {
		defer close(guiDone)
		req := httptest.NewRequest(http.MethodGet, "/api/sources", nil).WithContext(guiCtx)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-started
	stopGUI()
	select {
	case <-guiDone:
	case <-time.After(5 * time.Second):
		t.Fatal("dashboard did not stop at cancellation")
	}
	for range 3 {
		select {
		case <-observed:
		case <-time.After(5 * time.Second):
			t.Fatal("a cancelled adapter did not reach the dashboard")
		}
	}
}

func TestParityCappedPages(t *testing.T) {
	o := newOwner(t, nil)
	b := o.bridge()
	cliID := cliRequest(t, o, []string{"audit", "compare"}, o.windowArgs())["resultId"].(string)
	mcpID := mcpData(t, b, "audit_compare", o.windowArgs())["resultId"].(string)
	tooLarge := o.run("audit", "slowest", "--result-id", cliID, "--limit", "100", "--json")
	_, tooLargeMCP := b.Call(context.Background(), "audit_get_slowest", json.RawMessage(`{"resultId":"`+mcpID+`","limit":100}`))
	if errCode(t, tooLarge) != "INVALID_ARGUMENT" || string(clicore.FromMCP(mcp.Code(tooLargeMCP), false, "")) != "INVALID_ARGUMENT" {
		t.Errorf("an over-limit page: cli %+v, mcp %v", tooLarge, tooLargeMCP)
	}
	cliPage := data(t, o.run("audit", "slowest", "--result-id", cliID, "--limit", "50", "--json"))
	mcpPage := mcpData(t, b, "audit_get_slowest", map[string]any{"resultId": mcpID, "limit": 50})
	if !reflect.DeepEqual(strip(cliPage), strip(mcpPage)) {
		t.Errorf("maximum page differs:\ncli %v\nmcp %v", strip(cliPage), strip(mcpPage))
	}
	if len(cliPage["rows"].([]any)) != 50 || cliPage["nextCursor"] == nil || cliPage["nextCursor"] == "" {
		t.Errorf("the maximum page is not full: rows %d, cursor %v", len(cliPage["rows"].([]any)), cliPage["nextCursor"])
	}
	stream := o.run("audit", "slowest", "--result-id", cliID, "-o", "jsonl", "--limit", "7", "--max-rows", "21")
	ls := lines(t, stream.out)
	streamIDs := ids(ls)
	end := ls[len(ls)-1]
	if stream.code != 0 || len(streamIDs) != 21 || end["coverage"] != "partial" || end["nextCursor"] == nil || end["nextCursor"] == "" {
		t.Fatalf("capped stream: %+v", stream)
	}
	var pagedIDs []string
	cursor := ""
	for range 3 {
		args := map[string]any{"resultId": mcpID, "limit": 7}
		if cursor != "" {
			args["cursor"] = cursor
		}
		page := mcpData(t, b, "audit_get_slowest", args)
		for _, row := range page["rows"].([]any) {
			pagedIDs = append(pagedIDs, row.(map[string]any)["id"].(string))
		}
		cursor, _ = page["nextCursor"].(string)
	}
	if !reflect.DeepEqual(streamIDs, pagedIDs) {
		t.Errorf("stream %v, MCP pages %v", streamIDs, pagedIDs)
	}
	if cursor == "" || end["nextCursor"] != cursor {
		t.Errorf("stream cursor %v, MCP cursor %q", end["nextCursor"], cursor)
	}
}

func TestParityResultHandlesExpireOnOwnerRestart(t *testing.T) {
	a := newOwner(t, nil)
	bridge := a.bridge()
	id := mcpData(t, bridge, "audit_compare", a.windowArgs())["resultId"].(string)
	b := newOwner(t, nil)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"audit_get_groups", map[string]any{"resultId": id}},
		{"audit_get_timeline", map[string]any{"resultId": id, "apiKey": "old"}},
		{"audit_compare_timelines", map[string]any{"resultId": id, "apiKeys": []string{"old"}}},
		{"audit_get_requests", map[string]any{"resultId": id, "apiKey": "old"}},
		{"audit_get_slowest", map[string]any{"resultId": id}},
		{"audit_get_breakdown", map[string]any{"resultId": id, "dim": "statusCode"}},
	} {
		raw, _ := json.Marshal(c.args)
		_, err := b.bridge().Call(context.Background(), c.tool, raw)
		if mcp.Code(err) != mcp.CodeResultExpired {
			t.Errorf("%s after restart: %v", c.tool, err)
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

func commandOperations() map[string]string {
	st := &state{io: IO{Out: io.Discard, Err: io.Discard, Getenv: func(string) string { return "" }}}
	out := map[string]string{}
	var walk func(*cobra.Command, string)
	walk = func(c *cobra.Command, path string) {
		if op := c.Annotations[opAnnotation]; op != "" && c.Annotations[emitsAnnotation] == "1" {
			if old, dup := out[op]; dup {
				panic("operation has two commands: " + op + " (" + old + ", " + path + ")")
			}
			out[op] = path
		}
		for _, sub := range c.Commands() {
			if !sub.Hidden && sub.Name() != "help" {
				walk(sub, path+" "+sub.Name())
			}
		}
	}
	walk(st.root(), "unravis")
	return out
}

func TestParityMatrixCoversTheCommandTree(t *testing.T) {
	commands := commandOperations()
	unbuilt := map[string]bool{"grant.list": true, "grant.approve": true, "grant.revoke": true, "code.run": true, "code.types": true, "code.replay": true}
	for op := range commands {
		row, ok := clicap.Lookup(op)
		if !ok || !row.Complete() {
			t.Errorf("command %s has no complete matrix row", op)
			continue
		}
		if row.Human == clicap.Deny {
			t.Errorf("denied operation %s has a command: %s", op, commands[op])
		}
		if row.Tool != "" {
			if _, ok := describe(op); !ok {
				t.Errorf("tool operation %s has no published schema", op)
			}
			if row.MCP != clicap.Allow && row.MCP != clicap.Scoped {
				t.Errorf("tool operation %s is denied to MCP", op)
			}
		}
	}
	for _, op := range clicore.NativeOps() {
		if commands[op] == "" {
			t.Errorf("native operation %s has no command", op)
		}
	}
	for _, row := range clicap.Rows() {
		if row.Human == clicap.Allow && commands[row.Op] == "" && !unbuilt[row.Op] {
			t.Errorf("human operation %s has no command", row.Op)
		}
		if row.Tool != "" && commands[row.Op] == "" {
			t.Errorf("tool operation %s has no command", row.Op)
		}
	}
	published := map[string]bool{}
	for _, op := range operations() {
		published[op.Operation] = true
		if commands[op.Operation] == "" {
			t.Errorf("published operation %s has no command", op.Operation)
		}
	}
	for op := range commands {
		if !published[op] {
			t.Errorf("command %s has no published operation", op)
		}
	}
}

func contractDigest(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func contractSnapshot() string {
	var lines []string
	for _, op := range operations() {
		lines = append(lines, "operation "+op.Operation+" | "+op.Description)
	}
	for _, op := range clicore.NativeOps() {
		schema, _ := clicore.NativeSchema(op)
		lines = append(lines, "native "+op+" | "+contractDigest(schema))
	}
	for _, op := range operations() {
		schema, _ := describe(op.Operation)
		lines = append(lines, "schema "+op.Operation+" | "+contractDigest(schema))
	}
	for _, tool := range mcp.Catalog() {
		lines = append(lines, "mcp "+tool["name"].(string)+" | "+contractDigest(tool))
	}
	for _, row := range clicap.Rows() {
		lines = append(lines, fmt.Sprintf("matrix %s | gui=%s human=%s auto=%s mcp=%s sandbox=%s tool=%s", row.Op, row.Gui, row.Human, row.Auto, row.MCP, row.Sandbox, row.Tool))
	}
	commands := commandOperations()
	var ops []string
	for op := range commands {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	for _, op := range ops {
		lines = append(lines, "command "+op+" | "+commands[op])
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestSnapshotAdapterContracts(t *testing.T) {
	const golden = "testdata/adapter-contracts.golden"
	got := contractSnapshot()
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
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("the adapter contracts changed. If it is meant, run: UPDATE_GOLDEN=1 go test ./internal/clicore/cmdtree -run SnapshotAdapterContracts\n--- snapshot\n%s--- now\n%s", strings.ReplaceAll(string(want), "\r\n", "\n"), got)
	}
}

func matchesSchemaType(t any, data any) bool {
	match := func(ty string) bool {
		switch ty {
		case "object":
			_, ok := data.(map[string]any)
			return ok
		case "array":
			_, ok := data.([]any)
			return ok
		case "string":
			_, ok := data.(string)
			return ok
		case "boolean":
			_, ok := data.(bool)
			return ok
		case "integer":
			f, ok := data.(float64)
			return ok && f == float64(int64(f))
		case "number":
			_, ok := data.(float64)
			return ok
		case "null":
			return data == nil
		}
		return true
	}
	switch ty := t.(type) {
	case string:
		return match(ty)
	case []any:
		for _, option := range ty {
			if match(option.(string)) {
				return true
			}
		}
		return false
	}
	return true
}

// schemaProblems checks data against the part of a JSON schema that the published output schemas use: required fields, the type of
// every field that the schema describes, and (for an object that the schema does not say is open) fields that it does not describe.
func schemaProblems(schema map[string]any, data any, path string) []string {
	var out []string
	if ty, ok := schema["type"]; ok && !matchesSchemaType(ty, data) && data != nil {
		return []string{fmt.Sprintf("%s is %T, the schema says %v", path, data, ty)}
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
	stopOwner := newOwnerHeadless(t)
	stopped := data(t, stopOwner.run("app", "stop", "--json"))
	inspected := data(t, runOn(t, o.attacher(), "", nil, "blackbox", "inspect", "--offline", "--input", record(t), "--json"))
	answers := map[string]map[string]any{
		"app.status":       data(t, o.run("app", "status", "--json")),
		"app.stop":         stopped,
		"blackbox.inspect": inspected,
		"cases.list":       list,
		"cases.show":       data(t, o.run("cases", "show", id, "--json")),
		"doctor":           data(t, o.run("doctor", "--json")),
		"version":          data(t, o.run("version", "--json")),
		"app.start":        data(t, runStart(t, o, &starter{pid: 1}, "app", "start", "--headless", "--json")),
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

// The follow-up answers are pinned in testdata/followups.golden, because the command line and the MCP bridge run the same tool code:
// a change in that code (a renamed field, a wrong dimension, a changed number) reaches both sides and only a pinned answer shows it.
// Times are written as an offset from the demo clock, so the file does not depend on the hour the test runs.
const followUpGolden = "testdata/followups.golden"

func fixedClock(o *owner) { o.now = time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC) }

func relativeTimes(v any, now time.Time) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = relativeTimes(e, now)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = relativeTimes(e, now)
		}
		return out
	case string:
		if at, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return "now" + fmt.Sprintf("%+d", int64(at.Sub(now)/time.Second)) + "s"
		}
	}
	return v
}

func checkFollowUpGolden(t *testing.T, got map[string]any) {
	t.Helper()
	raw, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	have := string(raw) + "\n"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(followUpGolden, []byte(have), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(followUpGolden)
	if err != nil {
		t.Fatalf("no snapshot: run with UPDATE_GOLDEN=1 (%v)", err)
	}
	if w := strings.ReplaceAll(string(want), "\r\n", "\n"); w != have {
		t.Errorf("a follow-up answer changed. If it is meant, run: UPDATE_GOLDEN=1 go test ./internal/clicore/cmdtree -run ParityCompareAndFollowUps\nfirst difference near: %s", firstDifference(w, have))
	}
}

// firstDifference shows the lines around the first line where two texts differ, so a failure names the field.
func firstDifference(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(la) || i < len(lb); i++ {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			return fmt.Sprintf("line %d\n  snapshot: %s\n  now:      %s", i+1, strings.TrimSpace(x), strings.TrimSpace(y))
		}
	}
	return "(none)"
}
