package mcp

import (
	"api-audit-explorer/internal/audit"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyToolsAndCache(t *testing.T) {
	calls := 0
	q := audit.Query{SourceIDs: []string{"s1"}, Start: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 22, 13, 0, 0, 0, time.UTC), BaselineStart: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), BaselineEnd: time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC), Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 60}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<meta name="audit-session" content="private-test-token">`))
			return
		}
		if r.Header.Get("X-Audit-Token") != "private-test-token" {
			t.Error("missing private auth")
		}
		switch r.URL.Path {
		case "/api/sources":
			json.NewEncoder(w).Encode([]audit.Source{{ID: "s1", Database: "db", Collection: "Audit"}})
		case "/api/analyze":
			calls++
			json.NewEncoder(w).Encode(audit.Analysis{Query: q, APIs: []audit.Comparison{{Key: "k", SourceID: "s1", Controller: "Payroll", Action: "Run", State: "compared"}}, Timeline: []audit.Series{{Key: "k", Points: []audit.Point{{At: q.Start, Count: 30}}}}})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	b := newBridge(upstream.URL, upstream.Client())
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"query": q})
	result, err := b.call(context.Background(), "audit_compare", raw)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "private-test-token") {
		t.Fatal("credential leaked")
	}
	handle := result.(map[string]any)["resultId"].(string)
	args, _ := json.Marshal(map[string]any{"resultId": handle, "apiKey": "k"})
	if _, err = b.call(context.Background(), "audit_get_timeline", args); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("timeline re-queried production: calls=%d", calls)
	}
	if _, err = b.call(context.Background(), "audit_delete_source", json.RawMessage(`{}`)); err == nil {
		t.Fatal("mutation tool accepted")
	}
	if _, err = b.call(context.Background(), "audit_get_timeline", json.RawMessage(`{"resultId":"expired","apiKey":"k"}`)); err == nil {
		t.Fatal("stale result accepted")
	}
}

func TestInvalidQueryDoesNotContactProduction(t *testing.T) {
	b := newBridge("http://127.0.0.1:1", &http.Client{})
	if _, err := b.call(context.Background(), "audit_compare", json.RawMessage(`{"query":{}}`)); err == nil {
		t.Fatal("invalid query accepted")
	}
	if _, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{"url":"http://elsewhere"}`)); err == nil {
		t.Fatal("unknown argument accepted")
	}
}

var auditAnalysisForTest = audit.Analysis{APIs: []audit.Comparison{{Key: "a"}, {Key: "b"}}}

func TestRequestMetadataProjectionExcludesSensitiveFields(t *testing.T) {
	token := "test-token"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<meta name="audit-session" content="` + token + `">`))
			return
		}
		if r.URL.Path != "/api/requests" {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(audit.Page{Rows: []audit.RequestRecord{{ID: "record-id-1", State: "incomplete", URL: "https://private.example/?password=secret", Status: "OK", DurationMs: func() *float64 { v := 42.0; return &v }()}}, Sources: []audit.SourceResult{{SourceID: "s1", OK: true}}})
	}))
	defer upstream.Close()
	b := newBridge(upstream.URL, upstream.Client())
	b.resultID = "current"
	b.created = time.Now()
	b.result = &audit.Analysis{Query: audit.Query{}, APIs: []audit.Comparison{{Key: "k", SourceID: "s1"}}}
	result, err := b.call(context.Background(), "audit_get_requests", json.RawMessage(`{"resultId":"current","apiKey":"k"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	for _, private := range []string{"password", "private.example", token} {
		if strings.Contains(string(data), private) {
			t.Fatalf("projection exposed %s", private)
		}
	}
	// The record id and state identify the record; they are not sensitive.
	for _, want := range []string{`"id":"record-id-1"`, `"state":"incomplete"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("projection lost %s", want)
		}
	}
	if !strings.Contains(string(data), `"durationMs":42`) {
		t.Fatal("timing lost")
	}
}

func TestSessionEscapingAndRedirectPolicy(t *testing.T) {
	b, err := New(18765)
	if err != nil {
		t.Fatal(err)
	}
	if b.client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("local requests must not use environment proxy")
	}
	if b.client.CheckRedirect == nil {
		t.Fatal("redirects not disabled")
	}
	if _, err = New(0); err == nil {
		t.Fatal("ephemeral MCP target accepted")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<meta name="audit-session" content="private&amp;token">`))
	}))
	defer upstream.Close()
	b = newBridge(upstream.URL, upstream.Client())
	token, err := b.session(context.Background())
	if err != nil || token != "private&token" {
		t.Fatal("session escaping failed")
	}
	if err = b.request(context.Background(), "/api/payload", nil, nil); err == nil {
		t.Fatal("payload route accepted")
	}
}

func TestUpstreamErrorsDoNotExposeDetails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<meta name="audit-session" content="private-test-token">`))
			return
		}
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"mongodb://user:password@private"}`))
	}))
	defer upstream.Close()
	b := newBridge(upstream.URL, upstream.Client())
	_, err := b.call(context.Background(), "audit_list_sources", json.RawMessage(`{}`))
	if err == nil || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "private") {
		t.Fatal("upstream private failure leaked")
	}
}

func TestMultipleTimelinesKeepOwnBaselinesAndMissingData(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 5, 0, time.UTC)
	q := audit.Query{Start: start, End: start.Add(15 * time.Second), BucketSeconds: 10}
	quantile := func(v float64) *audit.Quantile { return &audit.Quantile{ValueMs: &v, LowerMs: v - 1} }
	a := audit.Analysis{Query: q, APIs: []audit.Comparison{{Key: "a", State: "compared", Reference: audit.Stats{P95: quantile(100)}}, {Key: "b", State: "compared", Reference: audit.Stats{P95: quantile(200)}}}, Timeline: []audit.Series{{Key: "a", Points: []audit.Point{{At: start.Truncate(10 * time.Second), Count: 30, P95: quantile(150)}}}, {Key: "b", Points: []audit.Point{{At: start.Truncate(10 * time.Second), Count: 3, P95: quantile(150)}}}}}
	raw, err := compareTimelines(&a, "test", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(map[string]any)
	slots := result["slots"].([]map[string]any)
	first := slots[0]["groups"].([]map[string]any)
	if first[0]["increaseMs"] != float64(50) || first[1]["increaseMs"] != float64(-50) {
		t.Fatal("APIs did not retain separate baselines")
	}
	if first[0]["increasePercent"] != float64(50) || first[1]["increasePercent"] != float64(-25) {
		t.Fatal("wrong percentage change")
	}
	if first[1]["sampleStatus"] != "few_samples" {
		t.Fatal("sparse evidence marked reliable")
	}
	if slots[0]["start"].(time.Time) != q.Start {
		t.Fatal("incident boundary not clipped")
	}
	missing := slots[1]["groups"].([]map[string]any)[0]
	if missing["currentP95Ms"] != nil || missing["sampleStatus"] != "missing" {
		t.Fatal("missing observations became zero")
	}
	if _, err := compareTimelines(&a, "test", []string{"a", "a"}); err == nil {
		t.Fatal("duplicate keys accepted")
	}
}

func TestMultiTimelineUsesUnixEpochForNonstandardIntervals(t *testing.T) {
	start := time.Date(2026, 10, 22, 7, 54, 51, 296000000, time.UTC)
	a := audit.Analysis{Query: audit.Query{Start: start, End: start.Add(time.Hour), BucketSeconds: 2443}, APIs: []audit.Comparison{{Key: "a"}}}
	raw, err := compareTimelines(&a, "test", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	slots := raw.(map[string]any)["slots"].([]map[string]any)
	if slots[0]["at"].(time.Time).Unix()%2443 != 0 || slots[0]["start"].(time.Time) != start {
		t.Fatal("time slots do not align to backend epoch buckets")
	}
}

func TestActivityToolPreservesSourceIdentityWithoutURLOrID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<meta name="audit-session" content="private-token">`))
			return
		}
		if r.URL.Path != "/api/activity" {
			t.Error("unexpected route")
		}
		json.NewEncoder(w).Encode(audit.Page{Rows: []audit.RequestRecord{{ID: "private-id", URL: "private-url", SourceID: "source2", Controller: "Payroll", Action: "Run", Kind: "hangfire"}}})
	}))
	defer server.Close()
	b := newBridge(server.URL, server.Client())
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	raw, _ := json.Marshal(audit.ActivityInput{SourceIDs: []string{"source2"}, Start: at, End: at.Add(time.Minute), Limit: 20})
	data, err := b.call(context.Background(), "audit_get_activity", raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(data)
	if strings.Contains(string(encoded), "private-") || !strings.Contains(string(encoded), "source2") || !strings.Contains(string(encoded), "hangfire") {
		t.Fatal("activity projection lost identity or exposed private fields")
	}
}
