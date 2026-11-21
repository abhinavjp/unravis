package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// An MCP request leaves a mark, and nothing stays in flight after a request ends.
func TestServeHTTPMarksMCPForUpdates(t *testing.T) {
	u := &Updater{}
	h := &handler{}
	WithUpdater(u)(h)
	r := httptest.NewRequest(http.MethodGet, "/api/cases", nil)
	r.Header.Set("User-Agent", mcpUserAgent)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !u.mcp.Active(time.Now(), mcpWindow) {
		t.Fatal("an MCP request must leave a mark")
	}
	if u.inflight.Load() != 0 {
		t.Fatal("nothing is in flight after the request")
	}
	u2 := &Updater{}
	h2 := &handler{}
	WithUpdater(u2)(h2)
	h2.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/cases", nil))
	if u2.mcp.Active(time.Now(), mcpWindow) || u2.inflight.Load() != 0 {
		t.Fatal("a page request is not an MCP request")
	}
}
