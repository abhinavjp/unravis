package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type breakdownTestRepo struct {
	fakeRepository
	calls int
	err   error
}

func (r *breakdownTestRepo) Breakdown(_ context.Context, in audit.BreakdownInput) (audit.Breakdown, error) {
	r.calls++
	if r.err != nil {
		return audit.Breakdown{}, r.err
	}
	return audit.Breakdown{Dim: in.Dim, Rows: []audit.BreakdownRow{{Value: "UA", Count: 2, Filter: audit.NewBreakdownFilter(in.Dim, "UA", 0)}}, Total: 2, Distinct: 1}, nil
}

func TestBreakdownEndpointValidatesBeforeReadingAndMapsErrors(t *testing.T) {
	repo := &breakdownTestRepo{}
	h := New(repo, nil, testToken, false)
	body := func(dim string, top int) string {
		raw, _ := json.Marshal(audit.BreakdownInput{Query: testQuery(), Dim: dim, Top: top})
		return string(raw)
	}
	res := request(h, http.MethodPost, "/api/breakdown", body(audit.BreakdownUserAgent, 5), true)
	var got audit.Breakdown
	if res.Code != http.StatusOK || repo.calls != 1 || json.Unmarshal(res.Body.Bytes(), &got) != nil || len(got.Rows) != 1 || got.Rows[0].Filter == nil || got.Rows[0].Filter.UserAgentContains != "UA" {
		t.Fatalf("valid breakdown: %d %s", res.Code, res.Body.String())
	}
	for name, raw := range map[string]string{"unknown dim": body("controller", 5), "top over 50": body(audit.BreakdownUserAgent, 51)} {
		if res := request(h, http.MethodPost, "/api/breakdown", raw, true); res.Code != http.StatusBadRequest || repo.calls != 1 {
			t.Errorf("%s: code %d, calls %d; the endpoint must reject it before reading", name, res.Code, repo.calls)
		}
	}
	if res := request(h, http.MethodPost, "/api/breakdown", body(audit.BreakdownUserAgent, 5), false); res.Code == http.StatusOK || repo.calls != 1 {
		t.Errorf("breakdown must require the session token, got %d", res.Code)
	}
	repo.err = fmt.Errorf("%w: %w: more than 50000", audit.ErrLimit, audit.ErrBreakdownGroupLimit)
	res = request(h, http.MethodPost, "/api/breakdown", body(audit.BreakdownUserIP, 5), true)
	if res.Code != http.StatusRequestEntityTooLarge || !json.Valid(res.Body.Bytes()) || !strings.Contains(res.Body.String(), "distinct values") {
		t.Errorf("group limit: %d %s, want 413 naming the distinct values", res.Code, res.Body.String())
	}
}

func TestBreakdownEndpointIsUnavailableWithoutAReader(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	raw, _ := json.Marshal(audit.BreakdownInput{Query: testQuery(), Dim: audit.BreakdownUserID})
	if res := request(h, http.MethodPost, "/api/breakdown", string(raw), true); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d, want 503", res.Code)
	}
}
