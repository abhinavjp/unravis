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
	{"/api/connections/remember", "/api/connections/remember", "POST"},
	{"/api/connections/test", "/api/connections/test", "POST"},
	{"/api/info", "/api/info", "GET"},
	{"/api/sources", "/api/sources", "GET, POST"},
	{"/api/sources/{id}/schema", "/api/sources/s/schema", "GET"},
	{"/api/sources/{id}/coverage", "/api/sources/s/coverage", "GET"},
	{"/api/sources/{id}", "/api/sources/s", "DELETE"},
	{"/api/analyze", "/api/analyze", "POST"},
	{"/api/requests", "/api/requests", "POST"},
	{"/api/activity", "/api/activity", "POST"},
	{"/api/slowest", "/api/slowest", "POST"},
	{"/api/breakdown", "/api/breakdown", "POST"},
	{"/api/patterns", "/api/patterns", "POST"},
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
	{"/api/cases/{id}/imports", "/api/cases/c1/imports", "GET, POST"},
	{"/api/cases/{id}/events", "/api/cases/c1/events", "GET"},
	{"/api/cases/{id}/timeline", "/api/cases/c1/timeline", "POST"},
	{"/api/cases/{id}", "/api/cases/c1", "DELETE"},
	{"/api/hosts", "/api/hosts", "GET, PUT"},
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
