package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type slowTestRepo struct {
	fakeRepository
	calls int
	last  audit.PageInput
}

func (r *slowTestRepo) Slowest(_ context.Context, input audit.PageInput) (audit.Page, error) {
	r.calls++
	r.last = input
	return audit.Page{Rows: []audit.RequestRecord{{URL: "private"}}}, nil
}
func TestSlowestValidatesBeforeReadingAndStripsURL(t *testing.T) {
	repo := &slowTestRepo{}
	h := New(repo, nil, testToken, false)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{"s"}, Start: at, End: at.Add(time.Minute), Limit: 1})
	in := audit.PageInput{Query: q, Limit: 50}
	body, _ := json.Marshal(in)
	r := request(h, http.MethodPost, "/api/slowest", string(body), true)
	if r.Code != 200 || repo.calls != 1 || stringsContainURL(r.Body.Bytes()) {
		t.Fatal("valid endpoint failed")
	}
	in.APIKey = "not allowed"
	body, _ = json.Marshal(in)
	r = request(h, http.MethodPost, "/api/slowest", string(body), true)
	if r.Code != 400 || repo.calls != 1 {
		t.Fatal("invalid query executed")
	}
}
func TestSlowestForwardsCursorAndMinimumAndBoundsThem(t *testing.T) {
	repo := &slowTestRepo{}
	h := New(repo, nil, testToken, false)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	q, _ := audit.ActivityQuery(audit.ActivityInput{SourceIDs: []string{"s"}, Start: at, End: at.Add(time.Minute), Limit: 1})
	min := 1200.5
	body, _ := json.Marshal(audit.PageInput{Query: q, Limit: 50, Cursor: "next", MinMs: &min})
	if r := request(h, http.MethodPost, "/api/slowest", string(body), true); r.Code != 200 || repo.calls != 1 || repo.last.Cursor != "next" || repo.last.MinMs == nil || *repo.last.MinMs != min {
		t.Fatalf("cursor/minimum not forwarded: %d %+v", repo.calls, repo.last)
	}
	for name, body := range map[string]string{
		"page above 50":    mustJSON(audit.PageInput{Query: q, Limit: 51}),
		"negative minimum": mustJSON(map[string]any{"query": q, "limit": 50, "minMs": -5}),
	} {
		if r := request(h, http.MethodPost, "/api/slowest", body, true); r.Code != 400 || repo.calls != 1 {
			t.Fatalf("%s executed: %d", name, r.Code)
		}
	}
}
func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
func stringsContainURL(raw []byte) bool {
	var p audit.Page
	return json.Unmarshal(raw, &p) != nil || len(p.Rows) != 1 || p.Rows[0].URL != ""
}
