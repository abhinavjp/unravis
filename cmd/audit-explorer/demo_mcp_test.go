package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/server"
)

func screenGet(t *testing.T, h http.Handler, token string) map[string]any {
	t.Helper()
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/mcp/profiles", nil)
	r.Header.Set("X-Audit-Token", token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	return got
}

func stateOf(got map[string]any, id string) string {
	for _, p := range got["profiles"].([]any) {
		if m := p.(map[string]any); m["id"] == id {
			return m["state"].(string)
		}
	}
	return ""
}

// BL-347: the owner hands the screen's routes to the dashboard once the store is open, and says why when it is not.
func TestOwnerMountsTheAIAccessScreen(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	mount := server.NewMCPMount()
	dash := server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithMCPMount(mount))
	if got := screenGet(t, dash, token); got["mode"] != "legacy" {
		t.Errorf("before the store: %v", got)
	}
	dir := filepath.Join(t.TempDir(), "mcp")
	getenv := func(k string) string {
		if k == "UNRAVIS_MCP_DIR" {
			return dir
		}
		return ""
	}
	_, closeFn := openMCPEndpoint(context.Background(), getenv, dash, token, false,
		func() (sealedfile.Protector, error) { return testSeal{}, nil }, withMCPMount(mount))
	defer closeFn()
	if got := screenGet(t, dash, token); got["mode"] != "on" {
		t.Errorf("with the store: %v", got)
	}

	// no sealing on this PC: the screen says off and why, and nothing is written
	m2 := server.NewMCPMount()
	d2 := server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithMCPMount(m2))
	_, c2 := openMCPEndpoint(context.Background(), func(string) string { return filepath.Join(t.TempDir(), "x") }, d2, token, false,
		func() (sealedfile.Protector, error) { return nil, sealedfile.ErrUnavailable }, withMCPMount(m2))
	c2()
	if got := screenGet(t, d2, token); got["mode"] != "off" || got["reason"] != server.MCPOffSealing {
		t.Errorf("no sealing: %v", got)
	}
}

// The demo run (no pipes) gets a real store of its own, with four hosts made through the same calls the screen and a host use.
func TestDemoRunHasItsOwnGrantStoreOnlyWhenAsked(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	repo := audit.NewDemo(time.Now())
	env := func(dir string, on string) func(string) string {
		return func(k string) string {
			switch k {
			case "AUDIT_CONFIG_DIR":
				return dir
			case envDemoMCP:
				return on
			}
			return ""
		}
	}
	// not asked for: nothing opens
	m := server.NewMCPMount()
	dash := server.New(repo, nil, token, true, server.WithMCPMount(m))
	openDemoMCP(context.Background(), env(t.TempDir(), ""), repo, m)()
	if got := screenGet(t, dash, token); got["mode"] != "legacy" {
		t.Errorf("not asked for: %v", got)
	}
	// asked for, but with no folder of its own: it never touches the person's real store
	noDir := openDemoMCP(context.Background(), env("", "1"), repo, m)
	noDir()
	if got := screenGet(t, dash, token); got["mode"] != "legacy" {
		t.Errorf("no folder: %v", got)
	}
	// asked for with a folder: five hosts, in the four states the screen has to show
	dir := t.TempDir()
	closeFn := openDemoMCP(context.Background(), env(dir, "1"), repo, m)
	got := screenGet(t, dash, token)
	if got["mode"] != "on" {
		t.Fatalf("asked for: %v", got)
	}
	for id, want := range map[string]string{"claude-desktop": "pending", "cursor": "pending", "zed": "pending", "vscode": "active", "old-laptop": "revoked"} {
		if s := stateOf(got, id); s != want {
			t.Errorf("%s is %q, want %q", id, s, want)
		}
	}
	for _, p := range got["profiles"].([]any) {
		if m := p.(map[string]any); m["id"] == "cursor" && m["contested"].(float64) != 1 {
			t.Errorf("the second program's try is not marked: %v", m)
		}
	}
	closeFn()
	// a second start on the same folder keeps what is there and makes no copies
	closeAgain := openDemoMCP(context.Background(), env(dir, "1"), repo, m)
	defer closeAgain()
	if again := screenGet(t, dash, token); len(again["profiles"].([]any)) != 5 || !strings.Contains(stateOf(again, "vscode"), "active") {
		t.Errorf("second start: %v", again)
	}
}

// BL-353: a store written by a newer version (this build was rolled back) is left untouched, and the screen says why.
func TestARolledBackBuildLeavesTheNewerStoreAloneAndSaysSo(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	dir := filepath.Join(t.TempDir(), "mcp")
	getenv := func(k string) string {
		if k == "UNRAVIS_MCP_DIR" {
			return dir
		}
		return ""
	}
	seal := func() (sealedfile.Protector, error) { return testSeal{}, nil }
	m := server.NewMCPMount()
	dash := server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithMCPMount(m))
	_, closeFn := openMCPEndpoint(context.Background(), getenv, dash, token, false, seal, withMCPMount(m))
	closeFn()
	path := filepath.Join(dir, mcpaudit.FileName)
	raw, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	before, _ := os.ReadFile(path) //nolint:gosec // the test's own file

	m2 := server.NewMCPMount()
	d2 := server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithMCPMount(m2))
	h, c2 := openMCPEndpoint(context.Background(), getenv, d2, token, false, seal, withMCPMount(m2))
	c2()
	if h != nil {
		t.Fatal("an endpoint on a store of a newer version")
	}
	if got := screenGet(t, d2, token); got["mode"] != "off" || got["reason"] != server.MCPOffNewer {
		t.Errorf("screen: %v", got)
	}
	after, _ := os.ReadFile(path) //nolint:gosec // the test's own file
	if !bytes.Equal(before, after) {
		t.Error("the newer store was modified")
	}
	if m, _ := filepath.Glob(path + ".bad-*"); len(m) != 0 {
		t.Errorf("set aside: %v", m)
	}
}
