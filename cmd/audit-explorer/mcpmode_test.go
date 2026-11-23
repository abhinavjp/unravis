package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/pipesrv"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/server"
)

type testSeal struct{}

func (testSeal) Protect(b []byte) ([]byte, error) { return append([]byte("T:"), b...), nil }
func (testSeal) Unprotect(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte("T:")) {
		return nil, errors.New("no")
	}
	return b[2:], nil
}

func agGet(h http.Handler, image, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r = r.WithContext(pipesrv.WithPeerImage(pipesrv.WithRole(r.Context(), pipesrv.RoleAG), image))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// BL-344 wiring: with a sealing protector the owner serves /mcp/v1/* on the ag pipe for the host's program only; without one (not
// Windows) the routes stay a 404 and nothing is written.
func TestOwnerServesMCPOnTheAgPipeOnlyWhenTheStoreCanOpen(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	dash := server.New(audit.NewDemo(time.Now()), nil, token, true)
	dir := filepath.Join(t.TempDir(), "mcp")
	getenv := func(k string) string {
		if k == "UNRAVIS_MCP_DIR" {
			return dir
		}
		return ""
	}
	seal := func() (sealedfile.Protector, error) { return testSeal{}, nil }
	h, closeFn := openMCPEndpoint(context.Background(), getenv, dash, token, false, seal)
	if h == nil {
		t.Fatal("no handler with a protector")
	}
	defer closeFn()
	served := pipesrv.Handler(dash, pipesrv.WithMCP(h))
	if w := agGet(served, `C:\App\unravis.exe`, "/mcp/v1/hello"); w.Code != 200 || !strings.Contains(w.Body.String(), `"pid"`) {
		t.Errorf("hello: %d %s", w.Code, w.Body)
	}
	if w := agGet(served, `C:\App\other.exe`, "/mcp/v1/hello"); w.Code != 403 {
		t.Errorf("another image: %d", w.Code)
	}
	// a call with no credential is refused and nothing of the dashboard answers
	r := httptest.NewRequest("POST", "/mcp/v1/call", strings.NewReader(`{"tool":"audit_list_sources"}`))
	r = r.WithContext(pipesrv.WithPeerImage(pipesrv.WithRole(r.Context(), pipesrv.RoleAG), `C:\App\unravis.exe`))
	w := httptest.NewRecorder()
	served.ServeHTTP(w, r)
	if w.Code != 401 || strings.Contains(w.Body.String(), "demo-") {
		t.Errorf("no credential: %d %s", w.Code, w.Body)
	}

	// no sealing: off, no folder made
	dir2 := filepath.Join(t.TempDir(), "mcp")
	none, closeNone := openMCPEndpoint(context.Background(), func(string) string { return dir2 }, dash, token, false,
		func() (sealedfile.Protector, error) { return nil, sealedfile.ErrUnavailable })
	closeNone()
	if none != nil {
		t.Error("a handler without a protector")
	}
	plain := pipesrv.Handler(dash)
	if w := agGet(plain, `C:\App\unravis.exe`, "/mcp/v1/hello"); w.Code == 200 || strings.Contains(w.Body.String(), `"pid"`) { // the dashboard handler answers 403 or 404: no token, no such route
		t.Errorf("routes without the endpoint: %d", w.Code)
	}
	// a demo run with no folder of its own does not touch the real store
	demo, closeDemo := openMCPEndpoint(context.Background(), func(string) string { return "" }, dash, token, true, seal)
	closeDemo()
	if demo != nil {
		t.Error("a demo run opened the real store")
	}
}
