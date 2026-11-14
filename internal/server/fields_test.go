package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
)

type fieldsTestRepo struct {
	fakeRepository
	gotID string
	calls int
	out   audit.FieldPaths
	err   error
}

func (r *fieldsTestRepo) SampleFieldPaths(_ context.Context, id string) (audit.FieldPaths, error) {
	r.calls++
	r.gotID = id
	return r.out, r.err
}

func TestFieldsRouteReturnsPathsBehindSessionTokenAndLoopback(t *testing.T) {
	repo := &fieldsTestRepo{out: audit.FieldPaths{Sampled: 2, Paths: []audit.FieldPath{
		{Path: "PreActionFilterTimings", Types: []string{"array"}},
		{Path: "PreActionFilterTimings[].FilterName", Types: []string{"string"}},
	}}}
	h := New(repo, nil, testToken, false)

	if r := request(h, http.MethodGet, "/api/sources/abc123/fields", "", false); r.Code != http.StatusUnauthorized || repo.calls != 0 {
		t.Fatalf("missing token status=%d calls=%d", r.Code, repo.calls)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/sources/abc123/fields", nil)
	req.Host = "attacker.example"
	req.Header.Set("X-Audit-Token", testToken)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || repo.calls != 0 {
		t.Fatalf("non-loopback host status=%d calls=%d", res.Code, repo.calls)
	}
	r := request(h, http.MethodGet, "/api/sources/abc123/fields", "", true)
	if r.Code != http.StatusOK || repo.calls != 1 || repo.gotID != "abc123" {
		t.Fatalf("status=%d calls=%d id=%q body=%s", r.Code, repo.calls, repo.gotID, r.Body.String())
	}
	want := `{"sampled":2,"paths":[{"path":"PreActionFilterTimings","types":["array"]},{"path":"PreActionFilterTimings[].FilterName","types":["string"]}],"truncated":false}`
	if got := strings.TrimSpace(r.Body.String()); got != want {
		t.Fatalf("body = %s\nwant   %s", got, want)
	}
}

func TestFieldsRouteRejectsBadInputMapsErrorsAndNormalizesEmpty(t *testing.T) {
	repo := &fieldsTestRepo{}
	h := New(repo, nil, testToken, false)
	for _, tc := range []struct {
		name, method, path string
		want               int
	}{
		{"blank id", http.MethodGet, "/api/sources//fields", http.StatusBadRequest},
		{"nested id", http.MethodGet, "/api/sources/a/b/fields", http.StatusBadRequest},
		{"post", http.MethodPost, "/api/sources/s/fields", http.StatusMethodNotAllowed},
		{"delete", http.MethodDelete, "/api/sources/s/fields", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := request(h, tc.method, tc.path, "", true); r.Code != tc.want || repo.calls != 0 {
				t.Fatalf("status=%d want=%d calls=%d body=%s", r.Code, tc.want, repo.calls, r.Body.String())
			}
		})
	}
	if r := request(h, http.MethodGet, "/api/sources/s/fields", "", true); strings.TrimSpace(r.Body.String()) != `{"sampled":0,"paths":[],"truncated":false}` {
		t.Fatalf("empty body = %s", r.Body.String())
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
		r := request(h, http.MethodGet, "/api/sources/s/fields", "", true)
		if r.Code != tc.want || strings.Contains(r.Body.String(), "secret") {
			t.Fatalf("err %v: status=%d body=%s", tc.err, r.Code, r.Body.String())
		}
	}
	h = New(&fakeRepository{}, nil, testToken, false)
	if r := request(h, http.MethodGet, "/api/sources/s/fields", "", true); r.Code != http.StatusServiceUnavailable {
		t.Fatalf("no sampler status=%d", r.Code)
	}
}
