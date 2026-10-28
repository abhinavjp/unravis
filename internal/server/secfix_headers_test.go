package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// L9: response headers. L2: the one-time launch link.

func TestSecurityHeadersOnEveryKindOfReply(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Header case")
	replies := map[string]*httptest.ResponseRecorder{
		"index":            request(e.h, http.MethodGet, "/", "", false),
		"asset":            request(e.h, http.MethodGet, "/assets/app.js", "", false),
		"401":              request(e.h, http.MethodGet, "/api/info", "", false),
		"404 api":          request(e.h, http.MethodGet, "/api/nope", "", true),
		"404 case":         e.get("/api/cases/" + strings.Repeat("a", 26) + "/imports"),
		"405":              request(e.h, http.MethodPost, "/api/info", "", true),
		"200 case":         e.get("/api/cases/" + id + "/imports"),
		"204 erase":        e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`),
		"404 erase absent": e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`),
	}
	for name, res := range replies {
		csp := res.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "script-src 'self'", "form-action 'none'", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", name, csp, want)
			}
		}
		if got := res.Header().Get("Cross-Origin-Opener-Policy"); got != "same-origin" {
			t.Errorf("%s: COOP = %q", name, got)
		}
		pp := res.Header().Get("Permissions-Policy")
		for _, feature := range []string{"camera=()", "microphone=()", "geolocation=()"} {
			if !strings.Contains(pp, feature) {
				t.Errorf("%s: Permissions-Policy %q lacks %q", name, pp, feature)
			}
		}
	}
	if replies["204 erase"].Code != http.StatusNoContent || replies["404 erase absent"].Code != http.StatusNotFound {
		t.Fatalf("erase = %d, then %d", replies["204 erase"].Code, replies["404 erase absent"].Code)
	}
	// No API reply is cached, whatever its status.
	for _, name := range []string{"401", "404 api", "404 case", "405", "200 case", "204 erase", "404 erase absent"} {
		if got := replies[name].Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", name, got)
		}
	}
}

func launchRequest(h http.Handler, query string) *httptest.ResponseRecorder {
	return request(h, http.MethodGet, launchPath+query, "", false)
}

func wantGonePage(t *testing.T, name string, res *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	if res.Code != http.StatusNotFound {
		t.Fatalf("%s = %d, want 404", name, res.Code)
	}
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("%s: Content-Type = %q", name, ct)
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("%s: Cache-Control = %q, want no-store", name, res.Header().Get("Cache-Control"))
	}
	if res.Header().Get("Location") != "" {
		t.Errorf("%s: a Location header was sent", name)
	}
	body := res.Body.String()
	if !strings.Contains(body, "already been used") {
		t.Errorf("%s: body has no explanation: %q", name, body)
	}
	for _, s := range append(secrets, testToken, "<script") {
		if strings.Contains(body, s) {
			t.Errorf("%s: body contains %q", name, s)
		}
	}
}

func TestLaunchCodeRedirectsOnceWithTheTokenInTheFragment(t *testing.T) {
	code := strings.Repeat("c", 32)
	h := New(&fakeRepository{}, testAssets(), testToken, false, WithLaunchCode(code, time.Now().Add(time.Minute)))
	wantGonePage(t, "wrong code", launchRequest(h, "?c=wrong"+code[5:]), code)
	wantGonePage(t, "no code", launchRequest(h, ""), code)
	res := launchRequest(h, "?c="+code)
	if res.Code != http.StatusFound {
		t.Fatalf("right code = %d, want 302", res.Code)
	}
	if loc := res.Header().Get("Location"); loc != "/#t="+testToken {
		t.Fatalf("Location = %q", loc)
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Error("launch reply is cacheable")
	}
	if strings.Contains(res.Body.String(), testToken) {
		t.Error("the token is in the launch body")
	}
	wantGonePage(t, "second use", launchRequest(h, "?c="+code), code)
}

func TestLaunchCodeExpires(t *testing.T) {
	code := strings.Repeat("d", 32)
	h := New(&fakeRepository{}, testAssets(), testToken, false, WithLaunchCode(code, time.Now().Add(-time.Second)))
	wantGonePage(t, "expired", launchRequest(h, "?c="+code), code)
}

func TestLaunchIsClosedWithoutACode(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	res := launchRequest(h, "?c=")
	if res.Code != http.StatusNotFound {
		t.Fatalf("no armed code = %d, want 404", res.Code)
	}
	wantGonePage(t, "no armed code", res)
	if res := launchRequest(h, "?c="+strings.Repeat("e", 300)); res.Code != http.StatusNotFound {
		t.Fatalf("long code = %d, want 404", res.Code)
	}
}

func TestWrongLaunchCodeDoesNotSpendTheRealOne(t *testing.T) {
	code := strings.Repeat("f", 32)
	h := New(&fakeRepository{}, testAssets(), testToken, false, WithLaunchCode(code, time.Now().Add(time.Minute)))
	launchRequest(h, "?c="+strings.Repeat("0", 32))
	if res := launchRequest(h, "?c="+code); res.Code != http.StatusFound {
		t.Fatalf("right code after a wrong guess = %d, want 302", res.Code)
	}
}
