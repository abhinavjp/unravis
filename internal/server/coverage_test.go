package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

type coverageTestRepo struct {
	fakeRepository
	calls int
	last  audit.CoverageInput
	err   error
}

func (r *coverageTestRepo) Coverage(_ context.Context, in audit.CoverageInput) (audit.CoverageReport, error) {
	r.calls++
	r.last = in
	if r.err != nil {
		return audit.CoverageReport{}, r.err
	}
	return audit.CoverageReport{SourceID: in.SourceID, TenantAttribution: "available"}, nil
}

func coveragePath(source string, start, end time.Time) string {
	q := url.Values{"start": {start.Format(time.RFC3339)}, "end": {end.Format(time.RFC3339)}}
	return "/api/sources/" + source + "/coverage?" + q.Encode()
}

func TestCoverageRouteRefusal409(t *testing.T) {
	repo := &coverageTestRepo{err: audit.ErrBroadScanRefused}
	h := New(repo, nil, testToken, false)
	end := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	r := request(h, http.MethodGet, coveragePath("src-1", end.Add(-24*time.Hour), end), "", true)
	if r.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", r.Code, r.Body.String())
	}
	var body struct{ Error, Code string }
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := "This check reads every record in the window, so it does not run on a live source by default. Run it on a restored copy, or start Unravis with --allow-broad-scans."
	if body.Error != want || body.Code != "broad_scan_refused" {
		t.Fatalf("body = %+v", body)
	}
	if repo.last.SourceID != "src-1" || !repo.last.End.Equal(end) {
		t.Fatalf("input = %+v", repo.last)
	}
}

func TestCoverageRouteDemo200(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	h := New(audit.NewDemo(now), nil, testToken, true)
	r := request(h, http.MethodGet, coveragePath("demo-west", now.Add(-time.Hour), now), "", true)
	if r.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", r.Code, r.Body.String())
	}
	var report audit.CoverageReport
	if err := json.Unmarshal(r.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SourceID != "demo-west" || report.Total.Docs == 0 || !strings.Contains(report.TenantAttribution, "0.0 % populated") || len(report.Provenance) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(r.Body.String(), `"docsExamined":`) {
		t.Fatal("docsExamined key missing")
	}
}

func TestCoverageRouteBadTimes400(t *testing.T) {
	repo := &coverageTestRepo{}
	h := New(repo, nil, testToken, false)
	end := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for name, path := range map[string]string{
		"missing":      "/api/sources/s/coverage",
		"not rfc3339":  "/api/sources/s/coverage?start=yesterday&end=today",
		"missing end":  "/api/sources/s/coverage?start=2026-10-23T00:00:00Z",
		"reversed":     coveragePath("s", end, end.Add(-time.Hour)),
		"over 31 days": coveragePath("s", end.Add(-32*24*time.Hour), end),
		"empty source": coveragePath("", end.Add(-time.Hour), end),
	} {
		if r := request(h, http.MethodGet, path, "", true); r.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d body = %s", name, r.Code, r.Body.String())
		}
	}
	if repo.calls != 0 {
		t.Fatalf("repository called %d times for invalid input", repo.calls)
	}
	repo.err = audit.ErrInvalidInput
	if r := request(h, http.MethodGet, coveragePath("s", end.Add(-time.Hour), end), "", true); r.Code != http.StatusBadRequest {
		t.Fatalf("repository invalid input status = %d", r.Code)
	}
}

func TestCoverageRouteNotProber503(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	end := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	if r := request(h, http.MethodGet, coveragePath("s", end.Add(-time.Hour), end), "", true); r.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", r.Code, r.Body.String())
	}
}

func TestCoverageRouteIsGetOnlyAndNeedsToken(t *testing.T) {
	repo := &coverageTestRepo{}
	h := New(repo, nil, testToken, false)
	end := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	path := coveragePath("s", end.Add(-time.Hour), end)
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
		if r := request(h, method, path, "", true); r.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d", method, r.Code)
		}
	}
	if r := request(h, http.MethodGet, path, "", false); r.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d", r.Code)
	}
	if repo.calls != 0 {
		t.Fatalf("repository called %d times", repo.calls)
	}
}

func TestCoverageRouteOtherErrorsUseRepoMapping(t *testing.T) {
	end := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for err, want := range map[error]int{audit.ErrNotFound: 404, audit.ErrLimit: 413, audit.ErrBusy: 429} {
		h := New(&coverageTestRepo{err: err}, nil, testToken, false)
		if r := request(h, http.MethodGet, coveragePath("s", end.Add(-time.Hour), end), "", true); r.Code != want {
			t.Fatalf("%v status = %d, want %d", err, r.Code, want)
		}
	}
}

func TestCoverageRouteSpecificErrorsUseFixedMessages(t *testing.T) {
	end := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		err    error
		status int
		want   string
	}{
		"budget": {fmt.Errorf("%w: %w: 11 documents", audit.ErrLimit, audit.ErrDocsExaminedBudget), 413, "The collection holds more documents than the docs-examined budget allows, so the coverage check did not run. Raise --docs-examined-budget or run it on a restored copy."},
		"size":   {fmt.Errorf("%w: mongodb://reader:secret@host: boom", audit.ErrCollectionSizeCheck), 502, "The coverage check could not read the collection size, so it did not run. Check that the source is reachable and that this account can read collection statistics."},
	}
	for name, tc := range cases {
		h := New(&coverageTestRepo{err: tc.err}, nil, testToken, false)
		r := request(h, http.MethodGet, coveragePath("s", end.Add(-time.Hour), end), "", true)
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if r.Code != tc.status || body.Error != tc.want || strings.Contains(r.Body.String(), "secret") {
			t.Fatalf("%s: status %d body %s", name, r.Code, r.Body.String())
		}
	}
}
