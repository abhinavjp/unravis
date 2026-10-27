package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/version"
)

func TestVersionRouteNeedsTheSessionTokenAndReportsTheStamp(t *testing.T) {
	oldVersion, oldCommit, oldDate := version.Version, version.Commit, version.Date
	t.Cleanup(func() { version.Version, version.Commit, version.Date = oldVersion, oldCommit, oldDate })
	version.Version, version.Commit, version.Date = "1.2.3", "abc1234", "2026-10-27"

	h := New(audit.NewDemo(time.Now()), nil, testToken, true)
	if res := request(h, http.MethodGet, "/api/version", "", false); res.Code != http.StatusUnauthorized {
		t.Fatalf("without a token status = %d", res.Code)
	}
	res := request(h, http.MethodGet, "/api/version", "", true)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"version": "1.2.3", "commit": "abc1234", "date": "2026-10-27"}
	if len(got) != len(want) {
		t.Fatalf("body = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if res := request(h, http.MethodPost, "/api/version", "", true); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d", res.Code)
	}
}
