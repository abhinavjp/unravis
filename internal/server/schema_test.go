package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type schemaTestRepo struct {
	fakeRepository
	gotID  string
	gotN   int
	calls  int
	census audit.SchemaCensus
	err    error
}

func (r *schemaTestRepo) SampleSchema(_ context.Context, id string, n int) (audit.SchemaCensus, error) {
	r.calls++
	r.gotID, r.gotN = id, n
	return r.census, r.err
}

func TestSchemaRouteReturnsCensusBehindSessionToken(t *testing.T) {
	repo := &schemaTestRepo{census: audit.SchemaCensus{Sampled: 3, Fields: []audit.FieldPresence{
		{Name: "ApiTime", Present: 3, Types: []string{"double", "int"}},
		{Name: "UserId", Present: 2, Types: []string{"string"}},
	}}}
	h := New(repo, nil, testToken, false)

	if r := request(h, http.MethodGet, "/api/sources/abc123/schema", "", false); r.Code != http.StatusUnauthorized || repo.calls != 0 {
		t.Fatalf("missing token status=%d calls=%d", r.Code, repo.calls)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/sources/abc123/schema?n=500", nil)
	req.Host = "attacker.example"
	req.Header.Set("X-Audit-Token", testToken)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || repo.calls != 0 {
		t.Fatalf("non-loopback host status=%d calls=%d", res.Code, repo.calls)
	}

	r := request(h, http.MethodGet, "/api/sources/abc123/schema?n=500", "", true)
	if r.Code != http.StatusOK || repo.calls != 1 || repo.gotID != "abc123" || repo.gotN != 500 {
		t.Fatalf("status=%d calls=%d id=%q n=%d body=%s", r.Code, repo.calls, repo.gotID, repo.gotN, r.Body.String())
	}
	want := `{"sampled":3,"fields":[{"name":"ApiTime","present":3,"types":["double","int"]},{"name":"UserId","present":2,"types":["string"]}]}`
	if got := strings.TrimSpace(r.Body.String()); got != want {
		t.Fatalf("body = %s\nwant   %s", got, want)
	}

	request(h, http.MethodGet, "/api/sources/abc123/schema", "", true)
	if repo.gotN != 0 {
		t.Fatalf("missing n forwarded as %d, want 0 so the store applies its default", repo.gotN)
	}
}

func TestSchemaRouteNormalizesEmptyCensusToArrays(t *testing.T) {
	h := New(&schemaTestRepo{census: audit.SchemaCensus{Fields: []audit.FieldPresence{{Name: "X", Present: 1}}}}, nil, testToken, false)
	r := request(h, http.MethodGet, "/api/sources/s/schema", "", true)
	if got := strings.TrimSpace(r.Body.String()); got != `{"sampled":0,"fields":[{"name":"X","present":1,"types":[]}]}` {
		t.Fatalf("body = %s", got)
	}
	h = New(&schemaTestRepo{}, nil, testToken, false)
	r = request(h, http.MethodGet, "/api/sources/s/schema", "", true)
	if got := strings.TrimSpace(r.Body.String()); got != `{"sampled":0,"fields":[]}` {
		t.Fatalf("body = %s", got)
	}
}

func TestSchemaRouteRejectsBadInputAndMapsErrors(t *testing.T) {
	repo := &schemaTestRepo{}
	h := New(repo, nil, testToken, false)
	for _, tc := range []struct {
		name, method, path string
		want               int
	}{
		{"blank id", http.MethodGet, "/api/sources//schema", http.StatusBadRequest},
		{"nested id", http.MethodGet, "/api/sources/a/b/schema", http.StatusBadRequest},
		{"non numeric n", http.MethodGet, "/api/sources/s/schema?n=lots", http.StatusBadRequest},
		{"post", http.MethodPost, "/api/sources/s/schema", http.StatusMethodNotAllowed},
		{"delete", http.MethodDelete, "/api/sources/s/schema", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := request(h, tc.method, tc.path, "", true); r.Code != tc.want || repo.calls != 0 {
				t.Fatalf("status=%d want=%d calls=%d body=%s", r.Code, tc.want, repo.calls, r.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		err  error
		want int
	}{
		{audit.ErrNotFound, http.StatusNotFound},
		{audit.ErrBusy, http.StatusTooManyRequests},
		{fmt.Errorf("mongodb://u:secret@host failed"), http.StatusInternalServerError},
	} {
		repo.err = tc.err
		r := request(h, http.MethodGet, "/api/sources/s/schema", "", true)
		if r.Code != tc.want || strings.Contains(r.Body.String(), "secret") {
			t.Fatalf("err %v: status=%d body=%s", tc.err, r.Code, r.Body.String())
		}
	}
}

func TestSchemaRouteUnavailableWithoutSamplerAndDeleteStillWorks(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	if r := request(h, http.MethodGet, "/api/sources/s/schema", "", true); r.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", r.Code)
	}
	if r := request(h, http.MethodDelete, "/api/sources/s", "", true); r.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", r.Code)
	}
}
