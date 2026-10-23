package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type activityTestRepository struct {
	fakeRepository
	calls int
}

func (r *activityTestRepository) Activity(context.Context, audit.ActivityInput) (audit.Page, error) {
	r.calls++
	return audit.Page{Rows: []audit.RequestRecord{{URL: "private-query", Controller: "Payroll"}}, Sources: []audit.SourceResult{{SourceID: "s", OK: true}}}, nil
}
func TestActivityEndpointValidatesAndStripsURL(t *testing.T) {
	repo := &activityTestRepository{}
	h := New(repo, nil, testToken, false)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	input := audit.ActivityInput{SourceIDs: []string{"s"}, Start: at, End: at.Add(time.Minute), Limit: 100}
	body, _ := json.Marshal(input)
	res := request(h, http.MethodPost, "/api/activity", string(body), true)
	if res.Code != 200 || repo.calls != 1 {
		t.Fatalf("status%d calls%d", res.Code, repo.calls)
	}
	var page audit.Page
	if json.Unmarshal(res.Body.Bytes(), &page) != nil || page.Rows[0].URL != "" {
		t.Fatal("activity leaked URL")
	}
	input.End = at.Add(48 * time.Hour)
	body, _ = json.Marshal(input)
	res = request(h, http.MethodPost, "/api/activity", string(body), true)
	if res.Code != 400 || repo.calls != 1 {
		t.Fatal("unbounded activity executed")
	}
}
