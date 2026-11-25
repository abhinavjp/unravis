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

func TestBusyDoesNotExtendRequestActivityWindows(t *testing.T) {
	u, _ := mcpRig(t)
	u.presence.Touch()
	u.NoteMCP()
	if !u.InUse() {
		t.Fatal("recent UI and MCP requests count as use")
	}
	if u.Busy() {
		t.Fatal("recent requests are not work that must block the idle power policy")
	}
}

func TestUpdateDownloadAndInstallCountAsBusy(t *testing.T) {
	u := &Updater{}
	u.inst.mu.Lock()
	u.inst.running = true
	u.inst.mu.Unlock()
	if !u.Busy() {
		t.Fatal("a running download must block idle throttling")
	}
	u.inst.mu.Lock()
	u.inst.running = false
	u.inst.busy = true
	u.inst.mu.Unlock()
	if !u.Busy() {
		t.Fatal("an install in progress must block idle throttling")
	}
	u.inst.mu.Lock()
	u.inst.busy = false
	u.inst.mu.Unlock()
	if u.Busy() {
		t.Fatal("finished update work no longer blocks throttling")
	}
}

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
