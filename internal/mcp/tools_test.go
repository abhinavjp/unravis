package mcp

import (
	"api-audit-explorer/internal/audit"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// legacySchema is the shape every tool had before the MVP-gap work: its input
// properties and required list. The MCP schema may only grow.
var legacySchema = map[string]struct {
	properties []string
	required   []string
}{
	"audit_get_activity":      {[]string{"sourceIds", "start", "end", "cursor", "limit"}, []string{"sourceIds", "start", "end"}},
	"audit_get_guide":         {nil, nil},
	"audit_list_sources":      {nil, nil},
	"audit_compare":           {[]string{"query", "limit", "sort"}, []string{"query"}},
	"audit_get_groups":        {[]string{"resultId", "offset", "limit", "search", "state", "sort"}, []string{"resultId"}},
	"audit_get_timeline":      {[]string{"resultId", "apiKey"}, []string{"resultId", "apiKey"}},
	"audit_compare_timelines": {[]string{"resultId", "apiKeys"}, []string{"resultId", "apiKeys"}},
	"audit_get_requests":      {[]string{"resultId", "apiKey", "cursor", "limit"}, []string{"resultId", "apiKey"}},
}

var legacyQuery = struct {
	properties []string
	required   []string
}{
	[]string{"sourceIds", "start", "end", "baselineStart", "baselineEnd", "kind", "outcome", "metric", "bucketSeconds", "controller", "action"},
	[]string{"sourceIds", "start", "end", "baselineStart", "baselineEnd", "kind", "outcome", "metric", "bucketSeconds"},
}

func toolByName(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, tool := range tools() {
		if tool["name"] == name {
			return tool
		}
	}
	t.Fatalf("tool %s is not advertised", name)
	return nil
}

func properties(schema map[string]any) map[string]any {
	p, _ := schema["properties"].(map[string]any)
	return p
}

func required(schema map[string]any) []string {
	r, _ := schema["required"].([]string)
	return r
}

func sameSet(a, b []string) bool {
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, ",") == strings.Join(b, ",")
}

func TestMCPSchemaAdditiveOnly(t *testing.T) {
	for name, want := range legacySchema {
		schema := toolByName(t, name)["inputSchema"].(map[string]any)
		props := properties(schema)
		for _, p := range want.properties {
			if _, ok := props[p]; !ok {
				t.Errorf("%s lost property %s", name, p)
			}
		}
		if !sameSet(required(schema), want.required) {
			t.Errorf("%s required changed: %v, want %v", name, required(schema), want.required)
		}
	}
	compare := toolByName(t, "audit_compare")["inputSchema"].(map[string]any)
	query := properties(compare)["query"].(map[string]any)
	props := properties(query)
	for _, p := range legacyQuery.properties {
		if _, ok := props[p]; !ok {
			t.Errorf("query lost property %s", p)
		}
	}
	if !sameSet(required(query), legacyQuery.required) {
		t.Errorf("query required changed: %v", required(query))
	}
	// The new record and range filters use the Query struct's JSON names.
	for _, p := range []string{"userIds", "uiVersions", "userAgentContains", "userIps", "urlContains", "auditId", "statusCodes", "completion", "operation", "observedMs", "apiTimeMs", "auditCreateTimeMs", "requestKb", "responseKb", "incompleteAgeMs"} {
		if _, ok := props[p]; !ok {
			t.Errorf("query is missing the %s filter", p)
		}
	}
	// Kind and outcome enums must be exactly what ValidateQuery accepts.
	kinds := props["kind"].(map[string]any)["enum"].([]string)
	if !sameSet(kinds, []string{"all", "synchronous", "asynchronous", "hangfire", "http", "http_unknown"}) {
		t.Errorf("kind enum %v", kinds)
	}
	outcomes := props["outcome"].(map[string]any)["enum"].([]string)
	if !sameSet(outcomes, []string{"all", "success", "failure", "client_error", "server_error"}) {
		t.Errorf("outcome enum %v", outcomes)
	}
	completions := props["completion"].(map[string]any)["enum"].([]string)
	if !sameSet(completions, []string{"all", "complete", "incomplete", "pending", "invalid"}) {
		t.Errorf("completion enum %v", completions)
	}
	// Every advertised filter must actually decode into a Query.
	filters := `{"userIds":[1],"uiVersions":["9"],"userAgentContains":"x","userIps":["1.2.3.4"],"urlContains":"y","auditId":"` + strings.Repeat("a", 24) + `","statusCodes":["200"],"completion":"complete","operation":"A.B","observedMs":{"min":1,"max":2},"apiTimeMs":{"min":1},"auditCreateTimeMs":{"max":3},"requestKb":{"min":0},"responseKb":{"max":9}}`
	var decoded audit.Query
	if err := strictArgs(json.RawMessage(filters), &decoded); err != nil {
		t.Fatalf("schema filters do not decode into Query: %v", err)
	}
	for _, name := range []string{"audit_get_breakdown", "audit_get_slowest"} {
		toolByName(t, name)
	}
}

func fptr(v float64) *float64 { return &v }

// auditServer is a fake dashboard that answers one route and records what it
// was sent.
func auditServer(t *testing.T, route string, response any, got *json.RawMessage) *httptest.Server {
	t.Helper()
	return auditServerDemo(t, route, response, got, false)
}

// auditServerDemo is auditServer with a chosen /api/info demo value.
func auditServerDemo(t *testing.T, route string, response any, got *json.RawMessage, demo bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<meta name="audit-session" content="test-token">`))
			return
		}
		if r.URL.Path == "/api/info" {
			json.NewEncoder(w).Encode(map[string]any{"demo": demo})
			return
		}
		if r.URL.Path != route {
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if got != nil {
			var body json.RawMessage
			json.NewDecoder(r.Body).Decode(&body)
			*got = body
		}
		json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	return server
}

func cachedBridge(server *httptest.Server, opts ...Option) *Bridge {
	b := newBridge(server.URL, server.Client(), opts...)
	b.resultID = "current"
	b.created = time.Now()
	b.result = &audit.Analysis{Query: audit.Query{SourceIDs: []string{"s1"}}, APIs: []audit.Comparison{{Key: "k", SourceID: "s1", Controller: "Payroll", Action: "Run", Kind: "synchronous"}}}
	return b
}

const privateIP = "203.0.113.77"

func TestMCPRequestsExposeIDNotPayloadOrIP(t *testing.T) {
	record := audit.RequestRecord{ID: "65a1b2c3d4e5f60718293a4b", State: "complete", Status: "OK", Outcome: "success", URL: "https://private.example/?password=secret", UserIP: privateIP, UserAgent: "private-agent/1.0", DurationMs: fptr(42), ApiTimeMs: fptr(30), AuditCreateTimeMs: fptr(12), PayloadRecordTimeMs: fptr(5)}
	server := auditServer(t, "/api/requests", audit.Page{Rows: []audit.RequestRecord{record}}, nil)
	b := cachedBridge(server)
	result, err := b.call(context.Background(), "audit_get_requests", json.RawMessage(`{"resultId":"current","apiKey":"k"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	text := string(data)
	for _, want := range []string{`"id":"65a1b2c3d4e5f60718293a4b"`, `"state":"complete"`, `"outcome":"success"`, `"apiTimeMs":30`, `"auditCreateTimeMs":12`, `"payloadRecordTimeMs":5`, `"durationMs":42`} {
		if !strings.Contains(text, want) {
			t.Errorf("projection lost %s: %s", want, text)
		}
	}
	for _, private := range []string{"private.example", "password", privateIP, "private-agent", "test-token"} {
		if strings.Contains(text, private) {
			t.Errorf("projection exposed %s", private)
		}
	}
	description := toolByName(t, "audit_get_requests")["description"].(string)
	if strings.Contains(description, "No payloads, URLs, request IDs") {
		t.Error("description still denies record ids")
	}
	if !strings.Contains(description, "record id") {
		t.Error("description does not say record ids are returned")
	}
}

func TestMCPBreakdownHidesIPUnlessRequested(t *testing.T) {
	var sent json.RawMessage
	response := audit.Breakdown{Dim: "userIp", Total: 3, Rows: []audit.BreakdownRow{{Value: privateIP, Count: 2, Filter: &audit.BreakdownFilter{UserIPs: []string{privateIP}}}}, Other: &audit.BreakdownRow{Value: "Other", Count: 1}}
	server := auditServer(t, "/api/breakdown", response, &sent)
	b := cachedBridge(server, WithIdentifiers(true))

	_, err := b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"userIp"}`))
	if err == nil {
		t.Fatal("userIp breakdown allowed without includeIp")
	}
	if strings.Contains(err.Error(), privateIP) || !strings.Contains(err.Error(), "includeIp") {
		t.Fatalf("unhelpful or leaking error: %v", err)
	}
	if sent != nil {
		t.Fatal("hidden dimension still queried production")
	}
	if _, err = b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"userIp","includeIp":false}`)); err == nil {
		t.Fatal("includeIp:false allowed userIp")
	}

	result, err := b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"userIp","includeIp":true,"top":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(result); !strings.Contains(string(data), privateIP) {
		t.Fatal("includeIp:true did not return the address")
	}
	var input audit.BreakdownInput
	if err := json.Unmarshal(sent, &input); err != nil || input.Dim != "userIp" || input.Top != 5 || len(input.Query.SourceIDs) != 1 {
		t.Fatalf("upstream input %s", sent)
	}

	// Any other dimension never carries an address, even if the upstream does.
	other := audit.Breakdown{Dim: "statusCode", Total: 3, Rows: []audit.BreakdownRow{{Value: "OK", Count: 2, Filter: &audit.BreakdownFilter{StatusCodes: []string{"OK"}, UserIPs: []string{privateIP}}}}, Other: &audit.BreakdownRow{Value: "Other", Count: 1, Filter: &audit.BreakdownFilter{UserIPs: []string{privateIP}}}}
	server = auditServer(t, "/api/breakdown", other, nil)
	b = cachedBridge(server, WithIdentifiers(true))
	result, err = b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"statusCode"}`))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(result); strings.Contains(string(data), privateIP) {
		t.Fatalf("address leaked through another dimension: %s", data)
	}
	// A cached query that filters by address is not echoed back with it.
	b.result.Query.UserIPs = []string{privateIP}
	result, err = b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"statusCode"}`))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(result); strings.Contains(string(data), privateIP) {
		t.Fatalf("address echoed through the query: %s", data)
	}
	b.result.Query.UserIPs = nil
	if _, err = b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"nope"}`)); err == nil {
		t.Fatal("unknown dimension accepted")
	}
	if _, err = b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"userId","top":51}`)); err == nil {
		t.Fatal("top above 50 accepted")
	}
	if _, err = b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"stale","dim":"userId"}`)); err == nil {
		t.Fatal("stale result accepted")
	}
	description := toolByName(t, "audit_get_breakdown")["description"].(string)
	if !strings.Contains(description, "untrusted") {
		t.Error("breakdown description must say values are untrusted data")
	}
}

func TestMCPSlowestProjectionAndBounds(t *testing.T) {
	var sent json.RawMessage
	record := audit.RequestRecord{ID: "65a1b2c3d4e5f60718293a4b", SourceID: "s1", Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", State: "complete", Status: "OK", URL: "private-url", UserIP: privateIP, UserAgent: "private-agent", DurationMs: fptr(900), ApiTimeMs: fptr(800), AuditCreateTimeMs: fptr(100)}
	server := auditServer(t, "/api/slowest", audit.Page{Rows: []audit.RequestRecord{record}, NextCursor: "next"}, &sent)
	b := cachedBridge(server)
	result, err := b.call(context.Background(), "audit_get_slowest", json.RawMessage(`{"resultId":"current","limit":10,"minMs":500}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	text := string(data)
	for _, want := range []string{`"id":"65a1b2c3d4e5f60718293a4b"`, `"sourceId":"s1"`, `"durationMs":900`, `"nextCursor":"next"`} {
		if !strings.Contains(text, want) {
			t.Errorf("slowest lost %s: %s", want, text)
		}
	}
	for _, private := range []string{"private-url", privateIP, "private-agent", "test-token"} {
		if strings.Contains(text, private) {
			t.Errorf("slowest exposed %s", private)
		}
	}
	var input audit.PageInput
	if err := json.Unmarshal(sent, &input); err != nil || input.Limit != 10 || input.MinMs == nil || *input.MinMs != 500 || input.APIKey != "" {
		t.Fatalf("upstream input %s", sent)
	}
	for _, bad := range []string{`{"resultId":"current","limit":51}`, `{"resultId":"current","minMs":-1}`, `{"resultId":"current","cursor":"` + strings.Repeat("x", 4097) + `"}`, `{"resultId":"stale"}`, `{"resultId":"current","extra":1}`} {
		if _, err := b.call(context.Background(), "audit_get_slowest", json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestMCPGuideCoversNewRules(t *testing.T) {
	guide := strings.ToLower(investigationGuide)
	for _, want := range []string{"completion", "pending", "4xx", "5xx", "record id"} {
		if !strings.Contains(guide, want) {
			t.Errorf("guide lacks %q", want)
		}
	}
	if !strings.Contains(investigationGuide, "No payloads or raw URLs are exposed") {
		t.Error("guide must keep the payload and URL rule")
	}
}

func breakdownResponse(dim string, rows ...audit.BreakdownRow) audit.Breakdown {
	return audit.Breakdown{Dim: dim, Total: 10, Rows: rows, Other: &audit.BreakdownRow{Value: "Other", Count: 3, Values: 4}}
}

func TestBreakdownHidesUserIDValuesByDefault(t *testing.T) {
	response := breakdownResponse("userId",
		audit.BreakdownRow{Value: "1001", Count: 4, Filter: &audit.BreakdownFilter{UserIDs: []int{1001}}},
		audit.BreakdownRow{Value: "1002", Count: 3, Filter: &audit.BreakdownFilter{UserIDs: []int{1002}}})
	response.Other.Filter = &audit.BreakdownFilter{UserIDs: []int{1003}}
	b := cachedBridge(auditServer(t, "/api/breakdown", response, nil))
	result, err := b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"userId"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	text := string(data)
	for _, leak := range []string{"1001", "1002", "1003", `"filter"`, "userIds"} {
		if strings.Contains(text, leak) {
			t.Errorf("hidden output leaked %s: %s", leak, text)
		}
	}
	for _, want := range []string{`"identifiers":"hidden"`, `"value":"hidden-1"`, `"value":"hidden-2"`, `"count":4`, `"count":3`, `"other"`} {
		if !strings.Contains(text, want) {
			t.Errorf("hidden output lost %s: %s", want, text)
		}
	}
	if result.(map[string]any)["identifiersNote"] != hiddenNote {
		t.Errorf("identifiersNote = %v", result.(map[string]any)["identifiersNote"])
	}
	if hiddenNote != "User ids, user agents and IP addresses are hidden: the operator has not turned on --mcp-allow-identifiers. includeIp was ignored." {
		t.Error("mcp.hidden wording changed")
	}
}

func TestBreakdownHidesUserAgentByDefault(t *testing.T) {
	response := breakdownResponse("userAgent",
		audit.BreakdownRow{Value: "private-agent/1.0", Count: 6, Filter: &audit.BreakdownFilter{UserAgentContains: "private-agent/1.0"}},
		audit.BreakdownRow{Value: audit.BreakdownMissingLabel, Missing: true, Count: 1})
	b := cachedBridge(auditServer(t, "/api/breakdown", response, nil))
	result, err := b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"userAgent"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	text := string(data)
	for _, leak := range []string{"private-agent", `"filter"`, "userAgentContains"} {
		if strings.Contains(text, leak) {
			t.Errorf("hidden output leaked %s: %s", leak, text)
		}
	}
	if !strings.Contains(text, `"value":"hidden-1"`) || !strings.Contains(text, `"value":"hidden-2"`) || !strings.Contains(text, `"identifiers":"hidden"`) {
		t.Errorf("unexpected output %s", text)
	}
}

func TestBreakdownIncludeIpIgnoredWhenSwitchOff(t *testing.T) {
	var sent json.RawMessage
	response := audit.Breakdown{Dim: "userIp", Total: 3, Rows: []audit.BreakdownRow{{Value: privateIP, Count: 2, Filter: &audit.BreakdownFilter{UserIPs: []string{privateIP}}}}}
	b := cachedBridge(auditServer(t, "/api/breakdown", response, &sent))
	_, err := b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"userIp","includeIp":true}`))
	if err == nil {
		t.Fatal("userIp allowed with the switch off")
	}
	if !strings.Contains(err.Error(), "--mcp-allow-identifiers") || strings.Contains(err.Error(), privateIP) {
		t.Fatalf("error must name the flag and carry no address: %v", err)
	}
	if sent != nil {
		t.Fatal("refused dimension still queried production")
	}
}

func TestBreakdownShowsIdentifiersWithSwitch(t *testing.T) {
	response := breakdownResponse("userId", audit.BreakdownRow{Value: "1001", Count: 4, Filter: &audit.BreakdownFilter{UserIDs: []int{1001}}})
	b := cachedBridge(auditServer(t, "/api/breakdown", response, nil), WithIdentifiers(true))
	result, err := b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"userId"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	text := string(data)
	for _, want := range []string{`"value":"1001"`, `"userIds":[1001]`, `"identifiers":"shown"`} {
		if !strings.Contains(text, want) {
			t.Errorf("lost %s: %s", want, text)
		}
	}
	if strings.Contains(text, "identifiersNote") {
		t.Errorf("shown output carries the hidden note: %s", text)
	}
	// The address filter is still dropped on every other dimension.
	other := breakdownResponse("statusCode", audit.BreakdownRow{Value: "OK", Count: 2, Filter: &audit.BreakdownFilter{StatusCodes: []string{"OK"}, UserIPs: []string{privateIP}}})
	b = cachedBridge(auditServer(t, "/api/breakdown", other, nil), WithIdentifiers(true))
	result, err = b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"statusCode"}`))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(result); strings.Contains(string(data), privateIP) {
		t.Fatalf("dropIPs not applied: %s", data)
	}
}

func TestBreakdownDemoUnaffected(t *testing.T) {
	response := breakdownResponse("userId", audit.BreakdownRow{Value: "1001", Count: 4, Filter: &audit.BreakdownFilter{UserIDs: []int{1001}}})
	b := cachedBridge(auditServerDemo(t, "/api/breakdown", response, nil, true))
	result, err := b.call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"userId"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if !strings.Contains(string(data), `"value":"1001"`) || !strings.Contains(string(data), `"identifiers":"shown"`) || strings.Contains(string(data), "hidden-") {
		t.Fatalf("demo values were hidden: %s", data)
	}
}

func TestIdentifiersAllowedIsReadPerCallAndFailsClosed(t *testing.T) {
	demo := false
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<meta name="audit-session" content="test-token">`))
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"demo": demo})
	}))
	defer server.Close()
	b := newBridge(server.URL, server.Client())
	if ok, err := b.identifiersAllowed(context.Background()); ok || err != nil {
		t.Fatalf("demo false: %v %v", ok, err)
	}
	demo = true
	if ok, err := b.identifiersAllowed(context.Background()); !ok || err != nil {
		t.Fatalf("dashboard restarted into demo, not seen: %v %v", ok, err)
	}
	status = http.StatusInternalServerError
	if ok, err := b.identifiersAllowed(context.Background()); ok || err == nil {
		t.Fatalf("info failure must fail closed: %v %v", ok, err)
	}
	if ok, err := newBridge("http://127.0.0.1:1", &http.Client{}, WithIdentifiers(true)).identifiersAllowed(context.Background()); !ok || err != nil {
		t.Fatalf("operator switch needs no dashboard call: %v %v", ok, err)
	}
}

func TestToolCountStaysTen(t *testing.T) {
	if n := len(tools()); n != 10 {
		t.Fatalf("tool count = %d, want 10", n)
	}
}

func TestToolDescriptionsMentionOperatorSwitch(t *testing.T) {
	if d := toolByName(t, "audit_get_breakdown")["description"].(string); !strings.Contains(d, "--mcp-allow-identifiers") {
		t.Error("breakdown description lacks the operator switch")
	}
	if !strings.Contains(investigationGuide, "--mcp-allow-identifiers") {
		t.Error("guide lacks the operator switch")
	}
}

func TestProvenanceForwarded(t *testing.T) {
	prov := []audit.Provenance{{SourceID: "s1", Window: "current", QueryMode: audit.QueryModeExact, ScanClass: audit.ScanNarrow, Completeness: audit.RanToEnd, DocsReturned: 7}}
	has := func(t *testing.T, label string, result any) {
		t.Helper()
		data, _ := json.Marshal(result)
		if !strings.Contains(string(data), `"provenance":[{"sourceId":"s1","window":"current"`) || !strings.Contains(string(data), `"completeness":"ran_to_end"`) {
			t.Errorf("%s dropped provenance: %s", label, data)
		}
	}
	q := audit.Query{SourceIDs: []string{"s1"}, Start: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 22, 13, 0, 0, 0, time.UTC), BaselineStart: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), BaselineEnd: time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC), Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 60}
	compare := auditServer(t, "/api/analyze", audit.Analysis{Query: q, Provenance: prov, APIs: []audit.Comparison{{Key: "k", State: "compared"}}}, nil)
	raw, _ := json.Marshal(map[string]any{"query": q})
	result, err := newBridge(compare.URL, compare.Client()).call(context.Background(), "audit_compare", raw)
	if err != nil {
		t.Fatal(err)
	}
	has(t, "compare", result)

	page := audit.Page{Rows: []audit.RequestRecord{{ID: "65a1b2c3d4e5f60718293a4b"}}, Provenance: prov}
	for _, tool := range []struct{ name, route, args string }{
		{"audit_get_requests", "/api/requests", `{"resultId":"current","apiKey":"k"}`},
		{"audit_get_slowest", "/api/slowest", `{"resultId":"current"}`},
	} {
		result, err := cachedBridge(auditServer(t, tool.route, page, nil)).call(context.Background(), tool.name, json.RawMessage(tool.args))
		if err != nil {
			t.Fatal(err)
		}
		has(t, tool.name, result)
	}

	bd := breakdownResponse("statusCode", audit.BreakdownRow{Value: "OK", Count: 2})
	bd.Provenance = prov
	result, err = cachedBridge(auditServer(t, "/api/breakdown", bd, nil)).call(context.Background(), "audit_get_breakdown", json.RawMessage(`{"resultId":"current","dim":"statusCode"}`))
	if err != nil {
		t.Fatal(err)
	}
	has(t, "breakdown", result)

	// Absent upstream provenance (an older dashboard) adds nothing.
	result, err = cachedBridge(auditServer(t, "/api/slowest", audit.Page{}, nil)).call(context.Background(), "audit_get_slowest", json.RawMessage(`{"resultId":"current"}`))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(result); strings.Contains(string(data), "provenance") {
		t.Errorf("empty provenance emitted: %s", data)
	}
}
