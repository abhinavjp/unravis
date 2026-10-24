package mcp

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/server"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The demo dataset is seeded with known identifiers: user ids 1000-1006, client
// addresses in 203.0.113.0/24, URLs /payroll/run, /reports/generate and
// /employees/list, and four "Demo ..." user agents. None of them may reach an
// MCP caller while --mcp-allow-identifiers is off.
var (
	leakSubstrings = []string{"203.0.113.", "/payroll/", "/reports/", "/employees/", "Demo Browser", "Demo Mobile", "Demo Automation"}
	leakKeys       = map[string]bool{"url": true, "urls": true, "userip": true, "userips": true, "ip": true, "useragent": true, "userid": true, "userids": true}
)

// leaks lists every identifier found in a decoded JSON value.
func leaks(value any, path string) []string {
	var found []string
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if leakKeys[strings.ToLower(key)] && child != nil {
				found = append(found, path+"."+key+" (identifier field)")
			}
			found = append(found, leaks(child, path+"."+key)...)
		}
	case []any:
		for i, child := range v {
			found = append(found, leaks(child, path+"["+strconv.Itoa(i)+"]")...)
		}
	case string:
		found = append(found, leaksInText(v, path)...)
	}
	return found
}

func leaksInText(text, path string) []string {
	var found []string
	for _, needle := range leakSubstrings {
		if strings.Contains(text, needle) {
			found = append(found, fmt.Sprintf("%s contains %q", path, needle))
		}
	}
	for id := 1000; id <= 1006; id++ {
		if text == strconv.Itoa(id) {
			found = append(found, fmt.Sprintf("%s is user id %d", path, id))
		}
	}
	return found
}

// demoBridge runs the real dashboard handler on the demo dataset and connects a
// bridge to it through a session file, as the shipped binary does.
func demoBridge(t *testing.T, anchor time.Time, opts ...Option) *Bridge {
	t.Helper()
	token := "output-audit-token-0123456789abcdef"
	dashboard := httptest.NewServer(server.New(audit.NewDemo(anchor), nil, token, true))
	t.Cleanup(dashboard.Close)
	u, _ := url.Parse(dashboard.URL)
	port, _ := strconv.Atoi(u.Port())
	path := filepath.Join(t.TempDir(), SessionFileName)
	if err := WriteSessionFile(path, port, token); err != nil {
		t.Fatal(err)
	}
	b, err := New(0, append([]Option{WithSessionFile(path)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type auditRun struct {
	t       *testing.T
	b       *Bridge
	outputs map[string]any
	errs    map[string]string
}

func (r *auditRun) call(label, tool string, args any) any {
	r.t.Helper()
	raw, _ := json.Marshal(args)
	result, err := r.b.call(context.Background(), tool, raw)
	if err != nil {
		r.errs[label] = err.Error()
		return nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		r.t.Fatalf("%s: %v", label, err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		r.t.Fatalf("%s: %v", label, err)
	}
	r.outputs[label] = decoded
	return decoded
}

// runAllTools calls every one of the ten tools against the demo dashboard.
func runAllTools(t *testing.T, b *Bridge, anchor time.Time) *auditRun {
	t.Helper()
	r := &auditRun{t: t, b: b, outputs: map[string]any{}, errs: map[string]string{}}
	sources := []string{"demo-west", "demo-east"}
	query := audit.Query{SourceIDs: sources, Start: anchor.Add(-15 * time.Minute), End: anchor, BaselineStart: anchor.Add(-30 * time.Minute), BaselineEnd: anchor.Add(-15 * time.Minute), Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 60}

	r.call("guide", "audit_get_guide", map[string]any{})
	r.call("sources", "audit_list_sources", map[string]any{})
	compared, ok := r.call("compare", "audit_compare", map[string]any{"query": query, "limit": 100}).(map[string]any)
	if !ok {
		t.Fatalf("compare failed: %s", r.errs["compare"])
	}
	id, _ := compared["resultId"].(string)
	groups, _ := compared["groups"].([]any)
	if id == "" || len(groups) == 0 {
		t.Fatalf("compare returned no groups: %v", compared)
	}
	var keys []string
	for _, group := range groups {
		if key, _ := group.(map[string]any)["key"].(string); key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		t.Fatal("no API keys in the comparison")
	}
	r.call("groups", "audit_get_groups", map[string]any{"resultId": id, "limit": 100})
	timelineKeys := keys
	if len(timelineKeys) > 8 {
		timelineKeys = timelineKeys[:8]
	}
	r.call("compare_timelines", "audit_compare_timelines", map[string]any{"resultId": id, "apiKeys": timelineKeys})
	for _, key := range keys {
		r.call("timeline "+key, "audit_get_timeline", map[string]any{"resultId": id, "apiKey": key})
		r.call("requests "+key, "audit_get_requests", map[string]any{"resultId": id, "apiKey": key, "limit": 100})
	}
	r.call("slowest", "audit_get_slowest", map[string]any{"resultId": id, "limit": audit.MaxSlowestPageSize})
	for _, dim := range []string{audit.BreakdownStatusCode, audit.BreakdownUserID, audit.BreakdownUIVersion, audit.BreakdownUserAgent, audit.BreakdownPollingType} {
		r.call("breakdown "+dim, "audit_get_breakdown", map[string]any{"resultId": id, "dim": dim, "top": audit.MaxBreakdownTop})
	}
	r.call("breakdown userIp", "audit_get_breakdown", map[string]any{"resultId": id, "dim": audit.BreakdownUserIP, "includeIp": true, "top": audit.MaxBreakdownTop})
	r.call("activity", "audit_get_activity", map[string]any{"sourceIds": sources, "start": anchor.Add(-15 * time.Minute), "end": anchor, "limit": 100})
	// Identifier filters carrying the seeded values are refused, and the refusal
	// never echoes them.
	for name, set := range map[string]func(*audit.Query){
		"userIds":           func(q *audit.Query) { q.UserIDs = []int{1001} },
		"userAgentContains": func(q *audit.Query) { q.UserAgentContains = "Demo Browser" },
		"userIps":           func(q *audit.Query) { q.UserIPs = []string{"203.0.113.10"} },
		"urlContains":       func(q *audit.Query) { q.URLContains = "/payroll/" },
	} {
		filtered := query
		set(&filtered)
		r.call("filter "+name, "audit_compare", map[string]any{"query": filtered})
	}
	return r
}

// TestMCPOutputAuditNoIdentifiersWithoutSwitch is the roadmap 8.9 output audit:
// all ten tools, called against the demo dashboard with the switch off, return
// no user id, IP address, URL or user agent in any output or error.
func TestMCPOutputAuditNoIdentifiersWithoutSwitch(t *testing.T) {
	anchor := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	r := runAllTools(t, demoBridge(t, anchor), anchor)
	t.Logf("audited %d tool outputs and %d refusals", len(r.outputs), len(r.errs))

	called := map[string]bool{}
	for label := range r.outputs {
		called[strings.Fields(label)[0]] = true
	}
	for _, want := range []string{"guide", "sources", "compare", "groups", "compare_timelines", "timeline", "requests", "slowest", "breakdown", "activity"} {
		if !called[want] {
			t.Errorf("tool output %q missing; errors: %v", want, r.errs)
		}
	}
	for label, output := range r.outputs {
		for _, leak := range leaks(output, label) {
			t.Errorf("identifier leaked: %s", leak)
		}
	}
	for label, message := range r.errs {
		for _, leak := range leaksInText(message, label+" error") {
			t.Errorf("identifier leaked: %s", leak)
		}
	}
	if _, ok := r.errs["breakdown userIp"]; !ok {
		t.Error("userIp breakdown answered with the switch off")
	}
	for _, name := range []string{"userIds", "userAgentContains", "userIps", "urlContains"} {
		if !strings.Contains(r.errs["filter "+name], "--mcp-allow-identifiers") {
			t.Errorf("%s filter not refused: %v", name, r.errs["filter "+name])
		}
	}
	// The audit saw real data, not empty pages.
	for _, label := range []string{"slowest", "activity"} {
		rows, _ := r.outputs[label].(map[string]any)["rows"].([]any)
		if len(rows) == 0 {
			t.Errorf("%s returned no rows; the audit proves nothing", label)
		}
	}
}

// The detector must be able to see a leak: with the switch on, identifier
// breakdowns show the seeded values, and the same scan reports them.
func TestMCPOutputAuditDetectorSeesIdentifiersWithSwitch(t *testing.T) {
	anchor := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	r := runAllTools(t, demoBridge(t, anchor, WithIdentifiers(true)), anchor)
	for _, label := range []string{"breakdown userId", "breakdown userAgent", "breakdown userIp"} {
		if len(leaks(r.outputs[label], label)) == 0 {
			t.Errorf("%s with the switch on showed no identifiers; the detector or fixture is broken (errors: %v)", label, r.errs)
		}
	}
}
