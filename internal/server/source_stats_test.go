package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

type statsTestRepo struct {
	fakeRepository
	last audit.StatsInput
}

func (r *statsTestRepo) Stats(_ context.Context, in audit.StatsInput) (audit.SourceStats, error) {
	r.last = in
	at := time.Date(2026, 10, 20, 8, 30, 0, 0, time.UTC)
	return audit.SourceStats{SourceID: in.SourceID, Docs: 218000, NewestAt: &at}, nil
}

func TestSourceStatsRoute200(t *testing.T) {
	repo := &statsTestRepo{}
	h := New(repo, nil, testToken, false)
	r := request(h, http.MethodGet, "/api/sources/src-1/stats", "", true)
	if r.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", r.Code, r.Body.String())
	}
	var got audit.SourceStats
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if repo.last.SourceID != "src-1" || got.Docs != 218000 || got.NewestAt == nil {
		t.Fatalf("got = %+v last = %+v", got, repo.last)
	}
}

func TestSourceStatsRouteDemoAndNotStatter503(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if r := request(New(audit.NewDemo(now), nil, testToken, true), http.MethodGet, "/api/sources/demo-west/stats", "", true); r.Code != http.StatusOK {
		t.Fatalf("demo status = %d body = %s", r.Code, r.Body.String())
	}
	if r := request(New(&fakeRepository{}, nil, testToken, false), http.MethodGet, "/api/sources/s/stats", "", true); r.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", r.Code)
	}
}

func TestSourceStatsRouteIsGetOnlyAndNeedsToken(t *testing.T) {
	h := New(&statsTestRepo{}, nil, testToken, false)
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
		if r := request(h, method, "/api/sources/s/stats", "", true); r.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d", method, r.Code)
		}
	}
	if r := request(h, http.MethodGet, "/api/sources/s/stats", "", false); r.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d", r.Code)
	}
}
