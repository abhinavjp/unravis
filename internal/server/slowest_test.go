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
}

func (r *slowTestRepo) Slowest(context.Context, audit.PageInput) (audit.Page, error) {
	r.calls++
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
	in.Cursor = "not allowed"
	body, _ = json.Marshal(in)
	r = request(h, http.MethodPost, "/api/slowest", string(body), true)
	if r.Code != 400 || repo.calls != 1 {
		t.Fatal("invalid query executed")
	}
}
func stringsContainURL(raw []byte) bool {
	var p audit.Page
	return json.Unmarshal(raw, &p) != nil || len(p.Rows) != 1 || p.Rows[0].URL != ""
}
