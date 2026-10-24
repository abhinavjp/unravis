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

func filterQuery() audit.Query {
	return audit.Query{SourceIDs: []string{"s1"}, Start: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 22, 13, 0, 0, 0, time.UTC), BaselineStart: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), BaselineEnd: time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC), Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 60}
}

// identifierFilters are the record filters that match on an identifier value. A
// model that may set them can bisect a hidden id, so they follow the same switch.
var identifierFilters = map[string]func(*audit.Query){
	"userIds":           func(q *audit.Query) { q.UserIDs = []int{1001} },
	"userAgentContains": func(q *audit.Query) { q.UserAgentContains = "secret-agent" },
	"userIps":           func(q *audit.Query) { q.UserIPs = []string{"203.0.113.77"} },
}

func compareCall(t *testing.T, b *Bridge, q audit.Query) error {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"query": q})
	_, err := b.call(context.Background(), "audit_compare", raw)
	return err
}

func TestCompareRejectsIdentifierFiltersWhenSwitchOff(t *testing.T) {
	for name, set := range identifierFilters {
		t.Run(name, func(t *testing.T) {
			var sent json.RawMessage
			server := auditServer(t, "/api/analyze", audit.Analysis{}, &sent)
			q := filterQuery()
			set(&q)
			err := compareCall(t, newBridge(server.URL, server.Client()), q)
			if err == nil || !strings.Contains(err.Error(), "--mcp-allow-identifiers") || !strings.Contains(err.Error(), name) {
				t.Fatalf("filter %s accepted or unclear error: %v", name, err)
			}
			for _, value := range []string{"1001", "secret-agent", "203.0.113.77"} {
				if strings.Contains(err.Error(), value) {
					t.Errorf("error echoes the filter value %q", value)
				}
			}
			if len(sent) != 0 {
				t.Errorf("a rejected query reached the dashboard: %s", sent)
			}
		})
	}
}

func TestCompareIdentifierFiltersFailClosedWhenInfoFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<meta name="audit-session" content="test-token">`))
			return
		}
		if r.URL.Path == "/api/analyze" {
			t.Error("analyze reached while info failed")
		}
		w.WriteHeader(500)
	}))
	defer server.Close()
	q := filterQuery()
	q.UserIDs = []int{1001}
	if err := compareCall(t, newBridge(server.URL, server.Client()), q); err == nil {
		t.Fatal("info failure must reject the filter")
	}
}

func TestCompareAcceptsIdentifierFiltersWithSwitchOrDemo(t *testing.T) {
	for name, set := range identifierFilters {
		for _, mode := range []string{"switch", "demo"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				var sent json.RawMessage
				q := filterQuery()
				set(&q)
				var b *Bridge
				if mode == "switch" {
					server := auditServer(t, "/api/analyze", audit.Analysis{Query: q}, &sent)
					b = newBridge(server.URL, server.Client(), WithIdentifiers(true))
				} else {
					server := auditServerDemo(t, "/api/analyze", audit.Analysis{Query: q}, &sent, true)
					b = newBridge(server.URL, server.Client())
				}
				if err := compareCall(t, b, q); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(sent), name) {
					t.Errorf("filter not forwarded: %s", sent)
				}
			})
		}
	}
}

// Only audit_compare takes a query; every other tool reads the cached result, so
// none of them can carry an identifier filter.
func TestIdentifierFiltersOnlyOnCompare(t *testing.T) {
	for _, tool := range tools() {
		name, _ := tool["name"].(string)
		data, _ := json.Marshal(tool["inputSchema"])
		for filter := range identifierFilters {
			has := strings.Contains(string(data), `"`+filter+`"`)
			if has != (name == "audit_compare") {
				t.Errorf("%s: schema has %s = %v", name, filter, has)
			}
		}
	}
}

func TestCompareSchemaSaysIdentifierFiltersNeedOperatorSwitch(t *testing.T) {
	data, _ := json.Marshal(toolByName(t, "audit_compare")["inputSchema"])
	var schema struct {
		Properties struct {
			Query struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"query"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	for name := range identifierFilters {
		if !strings.Contains(schema.Properties.Query.Properties[name].Description, "--mcp-allow-identifiers") {
			t.Errorf("%s description lacks the operator switch", name)
		}
	}
}
