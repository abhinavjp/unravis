package mcp

import (
	"api-audit-explorer/internal/audit"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestScn1LimitZeroIsRefusedEverywhere(t *testing.T) {
	b := cachedBridge(auditServer(t, "/api/never", struct{}{}, nil))
	q := filterQuery()
	compare, _ := json.Marshal(map[string]any{"query": q, "limit": 0})
	for _, tc := range []struct{ name, args string }{
		{"audit_get_groups", `{"resultId":"current","limit":0}`},
		{"audit_get_requests", `{"resultId":"current","apiKey":"k","limit":0}`},
		{"audit_get_slowest", `{"resultId":"current","limit":0}`},
		{"audit_get_activity", `{"sourceIds":["s1"],"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z","limit":0}`},
		{"audit_compare", string(compare)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := b.call(context.Background(), tc.name, json.RawMessage(tc.args))
			if err == nil || !strings.Contains(err.Error(), "limit") {
				t.Fatalf("limit 0 error = %v", err)
			}
		})
	}
	noLimit, _ := json.Marshal(map[string]any{"query": q})
	b2 := cachedBridge(auditServer(t, "/api/analyze", audit.Analysis{Query: q}, nil))
	if _, err := b2.call(context.Background(), "audit_compare", json.RawMessage(noLimit)); err != nil && strings.Contains(err.Error(), "limit") {
		t.Fatalf("absent limit refused: %v", err)
	}
}

func TestScn1LimitAbsentOrNullStillDefaultsTo20(t *testing.T) {
	for _, args := range []string{`{"resultId":"current","apiKey":"k"}`, `{"resultId":"current","apiKey":"k","limit":null}`} {
		var sent json.RawMessage
		b := cachedBridge(auditServer(t, "/api/requests", audit.Page{}, &sent))
		if _, err := b.call(context.Background(), "audit_get_requests", json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
		var in audit.PageInput
		if err := json.Unmarshal(sent, &in); err != nil || in.Limit != 20 {
			t.Fatalf("requests %s: limit %d err %v", args, in.Limit, err)
		}
	}
	for _, args := range []string{`{"resultId":"current"}`, `{"resultId":"current","limit":null}`} {
		var sent json.RawMessage
		b := cachedBridge(auditServer(t, "/api/slowest", audit.Page{}, &sent))
		if _, err := b.call(context.Background(), "audit_get_slowest", json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
		var in audit.PageInput
		if err := json.Unmarshal(sent, &in); err != nil || in.Limit != 20 {
			t.Fatalf("slowest %s: limit %d err %v", args, in.Limit, err)
		}
		b = cachedBridge(auditServer(t, "/api/never", struct{}{}, nil))
		out, err := b.call(context.Background(), "audit_get_groups", json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		if page := out.(map[string]any); page["matchedGroups"] != 1 {
			t.Fatalf("groups %s: %v", args, page)
		}
	}
}

func TestScn1LimitAboveMaximumIsStillRefused(t *testing.T) {
	b := cachedBridge(auditServer(t, "/api/never", struct{}{}, nil))
	for _, tc := range []struct{ name, args string }{
		{"audit_get_groups", `{"resultId":"current","limit":101}`},
		{"audit_get_requests", `{"resultId":"current","apiKey":"k","limit":101}`},
		{"audit_get_slowest", `{"resultId":"current","limit":51}`},
		{"audit_get_activity", `{"sourceIds":["s1"],"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z","limit":101}`},
	} {
		if _, err := b.call(context.Background(), tc.name, json.RawMessage(tc.args)); err == nil {
			t.Fatalf("%s accepted a limit above the maximum", tc.name)
		}
	}
}

func TestScn1SlowestAndRequestRowsAgreeOnState(t *testing.T) {
	rec := func(state string) audit.Page {
		return audit.Page{Rows: []audit.RequestRecord{{ID: "65a1b2c3d4e5f60718293a4b", SourceID: "s1", Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", State: state, Status: "OK", DurationMs: fptr(900)}}}
	}
	for _, tc := range []struct{ route, tool, args string }{
		{"/api/slowest", "audit_get_slowest", `{"resultId":"current"}`},
		{"/api/requests", "audit_get_requests", `{"resultId":"current","apiKey":"k"}`},
	} {
		for state, want := range map[string]string{"": "valid", "complete": "complete"} {
			b := cachedBridge(auditServer(t, tc.route, rec(state), nil))
			out, err := b.call(context.Background(), tc.tool, json.RawMessage(tc.args))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(out)
			if !strings.Contains(string(raw), `"state":"`+want+`"`) || strings.Contains(string(raw), `"state":""`) {
				t.Fatalf("%s state %q: %s", tc.tool, state, raw)
			}
		}
	}
}

func TestScn1ActivityErrorTextReachesTheCaller(t *testing.T) {
	b := cachedBridge(auditServer(t, "/api/never", struct{}{}, nil))
	_, err := b.call(context.Background(), "audit_get_activity", json.RawMessage(`{"sourceIds":["s1"],"start":"2026-10-20T00:00:00Z","end":"2026-10-21T01:00:00Z","limit":10}`))
	if err == nil || !strings.Contains(err.Error(), "at most 24 hours") {
		t.Fatalf("window error = %v", err)
	}
	_, err = b.call(context.Background(), "audit_get_activity", json.RawMessage(`{"sourceIds":["s1"],"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z","limit":101}`))
	if err == nil || !strings.Contains(err.Error(), "limit must be 1 to 100") {
		t.Fatalf("limit error = %v", err)
	}
}
