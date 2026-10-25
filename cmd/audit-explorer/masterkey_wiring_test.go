package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/server"
)

// TestOpenCasesSharesOneMasterKeySession proves the wiring: the key file sits
// beside hosts.dat, the case store opens master-key cases with the session the
// HTTP routes unlock, and locking closes them again.
func TestOpenCasesSharesOneMasterKeySession(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "AUDIT_CONFIG_DIR" {
			return dir
		}
		return ""
	}
	wiring, err := openCases(getenv, nil)
	if err != nil {
		t.Skipf("encrypted storage is unavailable here: %v", err)
	}
	defer wiring.close()
	const token = "wiring-test-token-0123456789abcdef"
	h := server.New(audit.NewDemo(time.Now()), nil, token, true, wiring.options...)
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
	newCase := func(title string) casestore.CaseID {
		t.Helper()
		res := do(http.MethodPost, "/api/cases", `{"title":"`+title+`"}`)
		if res.Code != http.StatusCreated {
			t.Fatalf("create case = %d %s", res.Code, res.Body)
		}
		list, err := wiring.store.List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range list {
			if c.Title == title {
				return c.ID
			}
		}
		t.Fatalf("case %q not listed", title)
		return ""
	}

	before := newCase("before the key")
	if k, _ := wiring.store.CaseKind(before); k != casestore.KindDPAPI {
		t.Fatalf("case before the key is %q", k)
	}
	const pass = "wiring test passphrase"
	body := `{"passphrase":"` + pass + `","confirm":"` + pass + `","acknowledged":true}`
	if res := do(http.MethodPost, "/api/master-key/setup", body); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, masterkey.FileName)); err != nil {
		t.Fatalf("masterkey.dat is not beside the other config files: %v", err)
	}
	after := newCase("after the key")
	if k, _ := wiring.store.CaseKind(after); k != casestore.KindMasterKey {
		t.Fatalf("case after the key is %q", k)
	}

	if res := do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	if res := do(http.MethodGet, "/api/cases", ""); res.Code != http.StatusLocked {
		t.Fatalf("list while locked = %d %s", res.Code, res.Body)
	}
	if res := do(http.MethodPost, "/api/master-key/unlock", `{"passphrase":"`+pass+`"}`); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	res := do(http.MethodGet, "/api/cases", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "after the key") {
		t.Fatalf("list after unlock = %d %s", res.Code, res.Body)
	}
}
