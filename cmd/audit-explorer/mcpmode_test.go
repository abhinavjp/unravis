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
	"api-audit-explorer/internal/mcpaudit"
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

// BL-351: opening the store prunes the ledger, so rows older than 90 days are gone after a restart whatever the call rate.
func TestOpeningTheStorePrunesTheLedger(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	dash := server.New(audit.NewDemo(time.Now()), nil, token, true)
	dir := filepath.Join(t.TempDir(), "mcp")
	getenv := func(k string) string {
		if k == "UNRAVIS_MCP_DIR" {
			return dir
		}
		return ""
	}
	ctx := context.Background()
	db, _, err := mcpaudit.Open(ctx, dir, testSeal{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, age := range []time.Duration{120 * 24 * time.Hour, 91 * 24 * time.Hour, time.Hour} {
		if err := db.Record(ctx, mcpaudit.Event{ProfileID: "p", Kind: mcpaudit.KindEnd, Time: time.Now().Add(-age)}); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	h, closeFn := openMCPEndpoint(ctx, getenv, dash, token, false, func() (sealedfile.Protector, error) { return testSeal{}, nil })
	if h == nil {
		t.Fatal("no handler")
	}
	closeFn()
	db, _, err = mcpaudit.Open(ctx, dir, testSeal{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if n, err := db.CountEvents(ctx); err != nil || n != 1 {
		t.Errorf("%d rows after the open (want 1): %v", n, err)
	}
}

// BL-349: opening the endpoint wires the app's evidence resolver into the dashboard handler. An alias made for an approved host (through a
// second connection to the same store file) opens to its record with the session token, and only then; after the endpoint is closed the
// route is a 404 again.
func TestOwnerWiresTheEvidenceResolver(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	dash := server.New(audit.NewDemo(time.Now()), nil, token, true)
	dir := filepath.Join(t.TempDir(), "mcp")
	getenv := func(k string) string {
		if k == "UNRAVIS_MCP_DIR" {
			return dir
		}
		return ""
	}
	h, closeFn := openMCPEndpoint(context.Background(), getenv, dash, token, false, func() (sealedfile.Protector, error) { return testSeal{}, nil })
	if h == nil {
		t.Fatal("no handler")
	}
	ctx := context.Background()
	db, _, err := mcpaudit.Open(ctx, dir, testSeal{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Insert(ctx, mcpaudit.Profile{ID: "h1", State: mcpaudit.StateActive, Rev: 2, Sources: []string{"demo-west"}, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	alias, err := db.MintAliases(ctx, "h1", 2, []mcpaudit.EvidenceKey{{SourceID: "demo-west", RecordID: "0123456789abcdef01234567"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
		r.Host = "127.0.0.1"
		if tok != "" {
			r.Header.Set("X-Audit-Token", tok)
		}
		w := httptest.NewRecorder()
		dash.ServeHTTP(w, r)
		return w
	}
	if w := get("/api/mcp/evidence/"+alias[0], token); w.Code != 200 || !strings.Contains(w.Body.String(), `"recordId":"0123456789abcdef01234567"`) {
		t.Errorf("resolve: %d %s", w.Code, w.Body)
	}
	if w := get("/api/mcp/evidence/"+alias[0], ""); w.Code != 401 {
		t.Errorf("no token: %d", w.Code)
	}
	closeFn()
	if w := get("/api/mcp/evidence/"+alias[0], token); w.Code != 404 {
		t.Errorf("after close: %d", w.Code)
	}
}
