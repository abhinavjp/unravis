package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode/settingsfile"
	"api-audit-explorer/internal/server"
)

// main's wiring of code mode (BL-574 slice 1): before mountCodeMode the routes say "unavailable", after it the settings route works on a
// real file in the app's config folder, and a run is refused because this build has no worker.
func TestCodeModeIsWiredIntoTheDashboard(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return dir
		}
		return ""
	}
	store := casestore.NewMemory()
	if _, err := store.Create(context.Background(), "Wired case"); err != nil {
		t.Fatal(err)
	}
	mount := server.NewCodeMount()
	dash := server.New(audit.NewDemo(time.Now()), nil, token, false, server.WithCases(store, nil, nil), server.WithCodeMount(mount))
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1:8765"
		r.Header.Set("X-Audit-Token", token)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		dash.ServeHTTP(w, r)
		return w
	}
	if w := call(http.MethodGet, "/api/code/settings", ""); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "code_mode_unavailable") {
		t.Fatalf("before mounting: %d %s", w.Code, w.Body.String())
	}
	mountCodeMode(getenv, dash, token, mount)
	if w := call(http.MethodGet, "/api/code/settings", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, "/api/code/settings", `{"enabled":true}`); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	// The file is where the config folder says, and the next run reads it (no restart).
	if set, err := settingsfile.New(dir).Load(); err != nil || !set.Enabled {
		t.Fatalf("file: %+v %v", set, err)
	}
	if _, err := os.Stat(filepath.Join(dir, settingsfile.FileName)); err != nil {
		t.Fatal(err)
	}
	w := call(http.MethodPost, "/api/code/run", `{"code":"1+1"}`)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "sandbox_unavailable") {
		t.Fatalf("run without a worker: %d %s", w.Code, w.Body.String())
	}
}

// A folder that cannot be known leaves the routes at "unavailable" instead of crashing the start.
func TestCodeModeStaysOffWhenTheFolderIsUnknown(t *testing.T) {
	t.Setenv("APPDATA", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := os.UserConfigDir(); err == nil {
		t.Skip("this machine always has a config folder")
	}
	const token = "tok-0123456789abcdef0123456789abcdef"
	mount := server.NewCodeMount()
	dash := server.New(audit.NewDemo(time.Now()), nil, token, false, server.WithCodeMount(mount))
	mountCodeMode(func(string) string { return "" }, dash, token, mount)
	r := httptest.NewRequest(http.MethodGet, "/api/code/settings", nil)
	r.Host = "127.0.0.1:8765"
	r.Header.Set("X-Audit-Token", token)
	w := httptest.NewRecorder()
	dash.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
