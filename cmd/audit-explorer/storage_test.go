package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/server"
)

func TestStorageFollowsTheLayout(t *testing.T) {
	user := t.TempDir()
	st := storageFor(envOf(map[string]string{"AUDIT_CONFIG_DIR": user}))
	if st.layout.Shared() {
		t.Fatal("AUDIT_CONFIG_DIR alone keeps the per-user layout")
	}
	if b, _ := st.base(envOf(map[string]string{"AUDIT_CONFIG_DIR": user})); b != user {
		t.Fatalf("per-user base = %q", b)
	}
	data := t.TempDir()
	env := envOf(map[string]string{"AUDIT_CONFIG_DIR": user, "UNRAVIS_DATA_DIR": data})
	st = storageFor(env)
	if !st.layout.Shared() {
		t.Fatal("UNRAVIS_DATA_DIR picks the shared layout")
	}
	b, _ := st.base(env)
	c, _ := st.casesRoot(env)
	if b != data || c != filepath.Join(data, "cases") {
		t.Fatalf("shared base %q cases %q", b, c)
	}
	if userStorage().layout.Shared() {
		t.Fatal("the demo's capture store is always per user")
	}
}

// TestSharedLayoutKeepsCasesInTheDataFolder opens the real case store in the shared layout: the case folders, the host map and the
// erased lines go to the data folder, and the person's own folder gets none of them.
func TestSharedLayoutKeepsCasesInTheDataFolder(t *testing.T) {
	user, data := t.TempDir(), t.TempDir()
	env := envOf(map[string]string{"AUDIT_CONFIG_DIR": user, "UNRAVIS_DATA_DIR": data})
	wiring, err := openCases(env, nil)
	if err != nil {
		t.Skipf("encrypted storage is unavailable here: %v", err)
	}
	defer wiring.close()
	const token = "storage-test-token-0123456789abcdef"
	h := server.New(audit.NewDemo(time.Now()), nil, token, false, wiring.options...)
	req := httptest.NewRequest(http.MethodPost, "/api/cases", strings.NewReader(`{"title":"shared"}`))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create case = %d %s", res.Code, res.Body)
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "cases")); len(entries) == 0 {
		t.Fatal("the case is not in the data folder")
	}
	if entries, _ := os.ReadDir(user); len(entries) != 0 {
		t.Fatalf("the per-user folder got files in the shared layout: %v", entries)
	}
}

// TestSharedLayoutAsksTheKeeper wires the saved stores in the shared layout with no keeper running: the routes say the keeper is
// stopped, and nothing is read from or written to the person's old saved files.
func TestSharedLayoutAsksTheKeeper(t *testing.T) {
	user, data := t.TempDir(), t.TempDir()
	env := envOf(map[string]string{
		"AUDIT_CONFIG_DIR":    user,
		"UNRAVIS_DATA_DIR":    data,
		"UNRAVIS_KEEPER_PIPE": fmt.Sprintf(`\\.\pipe\UnravisKeeperTest-%d`, time.Now().UnixNano()),
	})
	saved := wireSaved(env, storageFor(env))
	if saved.bb == nil || len(saved.options) != 3 {
		t.Fatalf("shared wiring: %d options, blackbox %v", len(saved.options), saved.bb)
	}
	const token = "storage-test-token-0123456789abcdef"
	opts := append(saved.options, wireBlackbox(false, demoSettings{}, env, saved.bb)...)
	h := server.New(audit.NewDemo(time.Now()), nil, token, false, opts...)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "127.0.0.1:8765"
		req.Header.Set("X-Audit-Token", token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res
	}
	res := do(http.MethodGet, "/api/keeper/status", "")
	var st struct {
		Mode    string `json:"mode"`
		Running bool   `json:"running"`
		Problem string `json:"problem"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &st) != nil || st.Mode != "shared" || st.Running || st.Problem != "stopped" {
		t.Fatalf("status = %d %s", res.Code, res.Body)
	}
	if res := do(http.MethodGet, "/api/sql-connections", ""); res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), "keeper") {
		t.Fatalf("saved SQL list = %d %s", res.Code, res.Body)
	}
	if entries, _ := os.ReadDir(user); len(entries) != 0 {
		t.Fatalf("the old per-user folder was written: %v", entries)
	}
}

// The person's own cases from before the shared layout are counted by folder name only, never opened.
func TestOldCaseCounterCountsCaseFoldersOnly(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return dir
		}
		return ""
	}
	root := filepath.Join(dir, "cases")
	for _, name := range []string{"abcdefghijklmnopqrstuvwxyz", "a2345672345672345672345672", "not-a-case", "ABCDEFGHIJKLMNOPQRSTUVWXYZ"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bcdefghijklmnopqrstuvwxyza"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	count := oldCaseCounter(getenv)
	if count == nil {
		t.Fatal("no counter")
	}
	if n := count(); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("count without the folder = %d", n)
	}
}
