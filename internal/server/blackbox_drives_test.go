package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func init() {
	routeCases = append(routeCases, routeCase{"/api/blackbox/this-pc/drives", "/api/blackbox/this-pc/drives", "GET"})
}

// bbDrivesFake replaces the platform read for one test. Tests in this package
// do not run in parallel, and cleanup always restores the real implementation.
func bbDrivesFake(t *testing.T, drives []bbDriveInfo) {
	t.Helper()
	old := bbDrivesRead
	bbDrivesRead = func() []bbDriveInfo { return drives }
	t.Cleanup(func() { bbDrivesRead = old })
}

func TestBBDrivesShape(t *testing.T) {
	free, total := uint64(123), uint64(456)
	want := []bbDriveInfo{
		{Letter: "C", Label: "Windows", Type: "fixed", System: true, FreeBytes: &free, TotalBytes: &total},
		{Letter: "E", Label: "USB", Type: "removable", System: false, FreeBytes: nil, TotalBytes: nil},
	}
	bbDrivesFake(t, want)
	b := newBBEnv(t, nil)

	res := b.e.get("/api/blackbox/this-pc/drives")
	if res.Code != http.StatusOK {
		t.Fatalf("drives = %d %s", res.Code, res.Body)
	}
	var out bbDrivesAnswer
	decodeBody(t, res, &out)
	if !reflect.DeepEqual(out.Drives, want) {
		t.Fatalf("drives = %+v, want %+v", out.Drives, want)
	}
	if out.Drives[1].FreeBytes != nil || out.Drives[1].TotalBytes != nil {
		t.Fatalf("unreadable space became a number: %+v", out.Drives[1])
	}

	// The answer has exactly the C-3 keys. Null space stays explicit null.
	var doc map[string]any
	decodeBody(t, res, &doc)
	if len(doc) != 1 || doc["drives"] == nil {
		t.Fatalf("top-level answer = %v", doc)
	}
	items, ok := doc["drives"].([]any)
	if !ok || len(items) != len(want) {
		t.Fatalf("raw drives = %v", doc["drives"])
	}
	allowed := map[string]bool{
		"letter": true, "label": true, "type": true, "system": true,
		"freeBytes": true, "totalBytes": true,
	}
	for i, item := range items {
		entry, ok := item.(map[string]any)
		if !ok || len(entry) != len(allowed) {
			t.Fatalf("drive %d keys = %v", i, entry)
		}
		for key := range entry {
			if !allowed[key] {
				t.Fatalf("drive %d has unexpected key %q", i, key)
			}
		}
	}
	second := items[1].(map[string]any)
	if value, present := second["freeBytes"]; !present || value != nil {
		t.Fatalf("freeBytes = %v, want explicit null", value)
	}
	if value, present := second["totalBytes"]; !present || value != nil {
		t.Fatalf("totalBytes = %v, want explicit null", value)
	}
}

func TestBBDrivesEmptyListIsArray(t *testing.T) {
	bbDrivesFake(t, nil)
	b := newBBEnv(t, nil)
	res := b.e.get("/api/blackbox/this-pc/drives")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"drives":[]`) {
		t.Fatalf("empty drives = %d %s", res.Code, res.Body)
	}
}

func TestBBDrivesRefusedRequestsDoNotReadDrives(t *testing.T) {
	reads := 0
	old := bbDrivesRead
	bbDrivesRead = func() []bbDriveInfo {
		reads++
		return []bbDriveInfo{}
	}
	t.Cleanup(func() { bbDrivesRead = old })
	b := newBBEnv(t, nil)

	if res := b.e.json(http.MethodPost, "/api/blackbox/this-pc/drives", ""); res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST drives = %d, want 405", res.Code)
	}
	for _, token := range []string{"", "wrong-token"} {
		req := httptest.NewRequest(http.MethodGet, "/api/blackbox/this-pc/drives", nil)
		req.Host = "127.0.0.1:8765"
		if token != "" {
			req.Header.Set("X-Audit-Token", token)
		}
		res := httptest.NewRecorder()
		b.e.h.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("token %q = %d, want 401", token, res.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/blackbox/this-pc/drives", nil)
	req.Host = "example.test"
	req.Header.Set("X-Audit-Token", testToken)
	res := httptest.NewRecorder()
	b.e.h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("non-loopback drives = %d, want 403", res.Code)
	}
	if reads != 0 {
		t.Fatalf("refused requests read drives %d times", reads)
	}
}
