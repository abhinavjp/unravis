package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/sessionproof"
)

// routeCase is one row of the route table: a concrete path for a route pattern
// and the exact set of methods that route allows (as the Allow header lists
// them, sorted).
type routeCase struct {
	pattern string
	path    string
	allow   string
}

var routeCases = []routeCase{
	{"/", "/", "GET, HEAD"},
	{launchPath, launchPath, "GET"},
	{sessionproof.Path, sessionproof.Path, "GET"},
	{"/api/connections/recent", "/api/connections/recent", "GET"},
	{"/api/connections/recent/{id}", "/api/connections/recent/c1", "DELETE"},
	{"/api/sql-connections", "/api/sql-connections", "GET, POST"},
	{"/api/sql-connections/test", "/api/sql-connections/test", "POST"},
	{"/api/sql-connections/{id}", "/api/sql-connections/c1", "DELETE"},
	{"/api/cases/{id}/health", "/api/cases/c1/health", "GET"},
	{"/api/cases/{id}/health/run", "/api/cases/c1/health/run", "POST"},
	{"/api/cases/{id}/ledger", "/api/cases/c1/ledger", "GET, POST"},
	{"/api/cases/{id}/ledger/copy-shared", "/api/cases/c1/ledger/copy-shared", "POST"},
	{"/api/cases/{id}/ledger/{hid}", "/api/cases/c1/ledger/h1", "DELETE, PUT"},
	{"/api/cases/{id}/ledger/{hid}/reuse", "/api/cases/c1/ledger/h1/reuse", "POST"},
	{"/api/cases/{id}/workspace", "/api/cases/c1/workspace", "GET, PUT"},
	{"/api/connections/remember","/api/connections/remember", "POST"},
	{"/api/connections/test", "/api/connections/test", "POST"},
	{"/api/probe/reachable", "/api/probe/reachable", "POST"},
	{"/api/master-key/status", "/api/master-key/status", "GET"},
	{"/api/master-key/setup", "/api/master-key/setup", "POST"},
	{"/api/master-key/unlock", "/api/master-key/unlock", "POST"},
	{"/api/master-key/lock", "/api/master-key/lock", "POST"},
	{"/api/master-key/rewrap", "/api/master-key/rewrap", "POST"},
	{"/api/master-key/reset-damaged", "/api/master-key/reset-damaged", "POST"},
	{"/api/info", "/api/info", "GET"},
	{"/api/version", "/api/version", "GET"},
	{"/api/sources", "/api/sources", "GET, POST"},
	{"/api/sources/{id}/schema", "/api/sources/s/schema", "GET"},
	{"/api/sources/{id}/coverage", "/api/sources/s/coverage", "GET"},
	{"/api/sources/{id}", "/api/sources/s", "DELETE"},
	{"/api/analyze", "/api/analyze", "POST"},
	{"/api/requests", "/api/requests", "POST"},
	{"/api/activity", "/api/activity", "POST"},
	{"/api/slowest", "/api/slowest", "POST"},
	{"/api/breakdown", "/api/breakdown", "POST"},
	{"/api/daily", "/api/daily", "GET"},
	{"/api/hourgrid", "/api/hourgrid", "GET"},
	{"/api/patterns", "/api/patterns", "POST"},
	{"/api/endpoint-daily", "/api/endpoint-daily", "GET"},
	{"/api/export/preview", "/api/export/preview", "POST"},
	{"/api/export/confirm", "/api/export/confirm", "POST"},
	{"/api/ledger", "/api/ledger", "GET, POST"},
	{"/api/ledger/{id}", "/api/ledger/h1", "DELETE, PUT"},
	{"/api/ledger/{id}/reuse", "/api/ledger/h1/reuse", "POST"},
	{"/api/payload", "/api/payload", "POST"},
	{"/api/record", "/api/record", "POST"},
	{"/api/export", "/api/export", "POST"},
	{"/api/cases", "/api/cases", "GET, POST"},
	{"/api/cases/{id}/close", "/api/cases/c1/close", "POST"},
	{"/api/cases/{id}/reopen", "/api/cases/c1/reopen", "POST"},
	{"/api/cases/{id}/retries", "/api/cases/c1/retries", "PUT"},
	{"/api/cases/{id}/retries/{retryId}", "/api/cases/c1/retries/1", "DELETE"},
	{"/api/cases/{id}/retries/{retryId}/retry", "/api/cases/c1/retries/1/retry", "POST"},
	{"/api/cases/{id}/imports", "/api/cases/c1/imports", "GET, POST"},
	{"/api/cases/{id}/imports/jobs/{job}", "/api/cases/c1/imports/jobs/j1", "DELETE, GET"},
	{"/api/cases/{id}/events", "/api/cases/c1/events", "GET"},
	{"/api/cases/{id}/series", "/api/cases/c1/series", "GET"},
	{"/api/cases/{id}/resource", "/api/cases/c1/resource", "GET"},
	{"/api/cases/{id}/collect", "/api/cases/c1/collect", "GET"},
	{"/api/cases/{id}/timeline", "/api/cases/c1/timeline", "POST"},
	{"/api/cases/{id}/call-trace", "/api/cases/c1/call-trace", "POST"},
	{"/api/cases/{id}/investigation", "/api/cases/c1/investigation", "POST"},
	{"/api/call-trace", "/api/call-trace", "POST"},
	{"/api/discover/capabilities", "/api/discover/capabilities", "GET"},
	{"/api/discover/scans", "/api/discover/scans", "POST"},
	{"/api/discover/scans/{scan}", "/api/discover/scans/s1", "DELETE, GET"},
	{"/api/discover/scans/{scan}/findings/{finding}/locate", "/api/discover/scans/s1/findings/f1/locate", "POST"},
	{"/api/cases/{id}/discover/imports", "/api/cases/c1/discover/imports", "POST"},
	{"/api/cases/{id}/sampler", "/api/cases/c1/sampler", "DELETE, GET, POST"},
	{"/api/cases/{id}/environments", "/api/cases/c1/environments", "GET, PUT"},
	{"/api/cases/{id}/sources", "/api/cases/c1/sources", "GET, POST"},
	{"/api/cases/{id}/sources/{sourceId}", "/api/cases/c1/sources/1", "DELETE"},
	{"/api/cases/{id}/mongo-sources", "/api/cases/c1/mongo-sources", "GET"},
	{"/api/cases/restore", "/api/cases/restore", "POST"},
	{"/api/cases/{id}/export", "/api/cases/c1/export", "POST"},
	{"/api/cases/orphan-files", "/api/cases/orphan-files", "GET"},
	{"/api/cases/{id}/archive", "/api/cases/c1/archive", "DELETE, GET, POST"},
	{"/api/cases/{id}/verify", "/api/cases/c1/verify", "DELETE, GET, POST"},
	{"/api/cases/{id}/free", "/api/cases/c1/free", "POST"},
	{"/api/cases/{id}/restore-in-place", "/api/cases/c1/restore-in-place", "POST"},
	{"/api/cases/{id}/restore-as-new", "/api/cases/c1/restore-as-new", "POST"},
	{"/api/cases/{id}", "/api/cases/c1", "DELETE, GET"},
	{"/api/hosts", "/api/hosts", "GET, PUT"},
	{"/api/importers", "/api/importers", "GET"},
}

var routeTestMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete}

func hasMethod(allow, method string) bool {
	for _, m := range strings.Split(allow, ", ") {
		if m == method {
			return true
		}
	}
	return false
}

func TestRouteTableCoversEveryRoute(t *testing.T) {
	want := map[string]bool{}
	for _, c := range routeCases {
		want[c.pattern] = true
		rt, _, ok := lookup(c.path)
		if !ok || rt.pattern != c.pattern {
			t.Errorf("lookup(%q) = %v, want pattern %q", c.path, rt, c.pattern)
			continue
		}
		if got := rt.allowHeader(); got != c.allow {
			t.Errorf("%s allows %q, want %q", c.pattern, got, c.allow)
		}
	}
	for _, rt := range routes {
		if !want[rt.pattern] {
			t.Errorf("route %q has no row in routeCases", rt.pattern)
		}
		delete(want, rt.pattern)
	}
	for pattern := range want {
		t.Errorf("routeCases row %q matches no route", pattern)
	}
}

func TestRoutesAnswerExpectedStatusPerMethod(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	for _, c := range routeCases {
		public := c.path == "/" || c.path == sessionproof.Path || c.path == launchPath
		for _, method := range routeTestMethods {
			res := request(h, method, c.path, "", !public)
			if hasMethod(c.allow, method) {
				if res.Code == http.StatusMethodNotAllowed || strings.Contains(res.Body.String(), "API route was not found.") {
					t.Errorf("%s %s = %d %s, want the route's own handler", method, c.path, res.Code, res.Body.String())
				}
				continue
			}
			if res.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405", method, c.path, res.Code)
			}
			if got := res.Header().Get("Allow"); got != c.allow {
				t.Errorf("%s %s Allow = %q, want %q", method, c.path, got, c.allow)
			}
			if !strings.Contains(res.Body.String(), "Method not allowed.") {
				t.Errorf("%s %s body = %q", method, c.path, res.Body.String())
			}
		}
	}
}

func TestRoutesRequireTokenBeforeMethodCheck(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	for _, c := range routeCases {
		if c.path == "/" || c.path == sessionproof.Path || c.path == launchPath {
			continue
		}
		for _, method := range routeTestMethods {
			if res := request(h, method, c.path, "", false); res.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without token = %d, want 401", method, c.path, res.Code)
			}
		}
	}
}

func TestUnknownRoutesAre404NotMethodErrors(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	for _, path := range []string{
		"/api/",
		"/api/nope",
		"/api/info/extra",
		"/api/analyze/",
		"/api/export/other",
		"/api/connections",
		"/api/connections/other",
		"/api/ledger/h1/other",
		"/api/ledger/h1/reuse/more",
		"/api/session-proof/x",
		"/api/cases/",
		"/api/cases/c1/other",
		"/api/cases/c1/imports/more",
		"/api/cases/c1/close/more",
		"/api/cases/c1/ledger/h1/other",
		"/api/cases/c1/ledger/h1/reuse/more",
		"/api/cases/c1/ledger/copy-shared/more",
		"/api/hosts/x",
	} {
		for _, method := range routeTestMethods {
			res := request(h, method, path, "", true)
			if res.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404", method, path, res.Code)
			}
			if got := res.Header().Get("Allow"); got != "" {
				t.Errorf("%s %s sent Allow %q on a 404", method, path, got)
			}
		}
		// Without a token the API still reveals nothing about which paths exist.
		if res := request(h, http.MethodGet, path, "", false); res.Code == http.StatusNotFound {
			t.Errorf("GET %s without token = 404, want 401", path)
		}
	}
}

func TestAssetPathsAllowOnlyGetAndHead(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		res := request(h, method, "/assets/app.js", "", false)
		if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s asset = %d Allow %q", method, res.Code, res.Header().Get("Allow"))
		}
	}
	if res := request(h, http.MethodGet, "/missing.js", "", false); res.Code != http.StatusNotFound {
		t.Errorf("GET unknown asset = %d, want 404", res.Code)
	}
}

func TestSourceItemRouteAllowsDeleteOnly(t *testing.T) {
	// Before the route table GET/POST/PUT on /api/sources/{id} answered 404.
	h := New(audit.NewDemo(time.Now()), nil, testToken, true)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
		res := request(h, method, "/api/sources/s", "", true)
		if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != "DELETE" {
			t.Errorf("%s = %d Allow %q", method, res.Code, res.Header().Get("Allow"))
		}
	}
}
