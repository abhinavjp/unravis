package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func mcpRig(t *testing.T) (*Updater, *handler) {
	t.Helper()
	u := &Updater{}
	h := &handler{token: "session-token-for-test"}
	WithUpdater(u)(h)
	return u, h
}

func askAs(h *handler, token string) {
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/not-a-route", nil)
	r.Header.Set("User-Agent", mcpUserAgent)
	if token != "" {
		r.Header.Set("X-Audit-Token", token)
	}
	h.ServeHTTP(httptest.NewRecorder(), r)
}

// Only a request that carries the session token counts as an assistant at work: another local process must not hold updates back.
func TestOnlyATokenedMCPRequestCountsAsUse(t *testing.T) {
	u, h := mcpRig(t)
	askAs(h, "")
	askAs(h, "wrong-token-of-the-same-length")
	if u.mcp.Active(time.Now(), mcpWindow) || u.InUse() {
		t.Fatal("a request without the token must not count")
	}
	askAs(h, h.token)
	if !u.mcp.Active(time.Now(), mcpWindow) || !u.InUse() {
		t.Fatal("an MCP request with the token counts")
	}
	if u.inflight.Load() != 0 {
		t.Fatal("nothing is in flight after the request")
	}
}

// The archive, verify and export work that goes on after its request returned keeps the app in use.
func TestCaseFileWorkKeepsTheAppInUse(t *testing.T) {
	u, h := mcpRig(t)
	if u.InUse() {
		t.Fatal("idle")
	}
	h.caseFiles().busy.Store(true)
	if !u.InUse() {
		t.Fatal("an export or restore that streams a database is work")
	}
	h.caseFiles().busy.Store(false)
	if u.InUse() {
		t.Fatal("idle again")
	}
}
