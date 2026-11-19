package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func openBody(m map[string]string) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func stubOpener(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := openFolderFn
	openFolderFn = func(dir string) error { got = append(got, dir); return nil }
	t.Cleanup(func() { openFolderFn = old })
	return &got
}

func TestOpenFolderRefusesUnsafePaths(t *testing.T) {
	got := stubOpener(t)
	f := newDiscFix(t, nil)
	for _, p := range []string{
		"", `relative\dir`, `\server\share`, `\?\C:\Windows`, `//server/share`, `/select,C:\x`, `-x`,
		`C:\Windows\..\Windows`, `C:\dir:stream`, "C:\a\x00b", `C:\Logs ` + strings.Repeat("a", 2000),
	} {
		rr := f.req(http.MethodPost, "/api/local/open-folder", openBody(map[string]string{"path": p}))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("path %q: status %d, want 400 (%s)", p, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "explorer") {
			t.Errorf("answer leaks detail: %s", rr.Body.String())
		}
	}
	if len(*got) != 0 {
		t.Fatalf("opener ran for refused paths: %v", *got)
	}
}

func TestOpenFolderOpensAnExistingFolderAndAFilesParent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive paths exist only on Windows")
	}
	got := stubOpener(t)
	f := newDiscFix(t, nil)
	dir := t.TempDir()
	file := filepath.Join(dir, "a.log")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, file} {
		rr := f.req(http.MethodPost, "/api/local/open-folder", openBody(map[string]string{"path": p}))
		if rr.Code != http.StatusOK {
			t.Fatalf("path %q: status %d (%s)", p, rr.Code, rr.Body.String())
		}
	}
	if len(*got) != 2 || !strings.EqualFold((*got)[0], dir) || !strings.EqualFold((*got)[1], dir) {
		t.Fatalf("opened %v, want the folder twice", *got)
	}
	rr := f.req(http.MethodPost, "/api/local/open-folder", openBody(map[string]string{"path": filepath.Join(dir, "missing")}))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "not-found") {
		t.Fatalf("missing folder: %d %s", rr.Code, rr.Body.String())
	}
}

func TestOpenFolderRateLimit(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive paths exist only on Windows")
	}
	stubOpener(t)
	f := newDiscFix(t, nil)
	dir := t.TempDir()
	last := 0
	for i := 0; i <= openFoldersPerWindow; i++ {
		last = f.req(http.MethodPost, "/api/local/open-folder", openBody(map[string]string{"path": dir})).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("last status %d, want 429", last)
	}
}

func TestOpenFolderUnknownScanFinding(t *testing.T) {
	got := stubOpener(t)
	f := newDiscFix(t, nil)
	rr := f.req(http.MethodPost, "/api/local/open-folder", openBody(map[string]string{"scan": strings.Repeat("a", 32), "finding": "nope1"}))
	if rr.Code != http.StatusNotFound || len(*got) != 0 {
		t.Fatalf("status %d opened %v", rr.Code, *got)
	}
	rr = f.req(http.MethodPost, "/api/local/open-folder", openBody(map[string]string{"scan": "x", "finding": "y", "path": `C:\`}))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("both given: %d", rr.Code)
	}
}
