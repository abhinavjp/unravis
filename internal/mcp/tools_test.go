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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<meta name="audit-session" content="test-token">`))
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

func cachedBridge(server *httptest.Server) *Bridge {
	b := newBridge(server.URL, server.Client())
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
	b := cachedBridge(server)

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
	b = cachedBridge(server)
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
