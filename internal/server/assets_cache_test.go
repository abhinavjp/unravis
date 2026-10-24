package server

import (
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	explorer "api-audit-explorer"
	"api-audit-explorer/internal/audit"
)

const immutableAssetCache = "private, max-age=31536000, immutable"

// Every existing file under /assets/ is served with a one-year immutable cache
// header, for GET and HEAD alike.
func TestExistingAssetsAreCachedImmutably(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), testAssets(), testToken, true)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		res := request(h, method, "/assets/app.js", "", false)
		if res.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%q", method, res.Code, res.Body.String())
		}
		if got := res.Header().Get("Cache-Control"); got != immutableAssetCache {
			t.Fatalf("%s Cache-Control=%q, want %q", method, got, immutableAssetCache)
		}
	}
}

// The embedded build is what users actually receive, so every file it ships
// under assets/ must carry the header.
func TestEmbeddedBuildAssetsAreCachedImmutably(t *testing.T) {
	assets := explorer.Assets()
	entries, err := fs.ReadDir(assets, "assets")
	if err != nil {
		t.Fatalf("read embedded assets: %v", err)
	}
	h := New(audit.NewDemo(time.Now()), assets, testToken, true)
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		res := request(h, http.MethodGet, "/assets/"+entry.Name(), "", false)
		if res.Code != http.StatusOK {
			t.Fatalf("/assets/%s status=%d", entry.Name(), res.Code)
		}
		if got := res.Header().Get("Cache-Control"); got != immutableAssetCache {
			t.Fatalf("/assets/%s Cache-Control=%q", entry.Name(), got)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("embedded build has no files under assets/")
	}
}

// Index, root files, API responses and every 404 keep their current headers:
// none of them may gain the immutable asset policy.
func TestNonAssetResponsesKeepTheirHeaders(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":    {Data: []byte(`<!doctype html><html><head></head><body>ok</body></html>`)},
		"assets/app.js": {Data: []byte("ok")},
		"favicon.svg":   {Data: []byte("<svg></svg>")},
	}
	h := New(audit.NewDemo(time.Now()), assets, testToken, true)

	root := request(h, http.MethodGet, "/", "", false)
	if root.Code != http.StatusOK {
		t.Fatalf("root status=%d", root.Code)
	}
	if got := root.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("root Cache-Control=%q, want no-store", got)
	}

	notImmutable := map[string]string{
		"root file":        "/favicon.svg",
		"missing asset":    "/assets/missing.js",
		"asset directory":  "/assets/",
		"traversal":        "/assets/../index.html",
		"api without auth": "/api/info",
	}
	for name, path := range notImmutable {
		res := request(h, http.MethodGet, path, "", false)
		if got := res.Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
			t.Errorf("%s (%s) status=%d Cache-Control=%q must not be immutable", name, path, res.Code, got)
		}
	}

	api := request(h, http.MethodGet, "/api/info", "", true)
	if api.Code != http.StatusOK {
		t.Fatalf("api status=%d body=%s", api.Code, api.Body.String())
	}
	if got := api.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("api Cache-Control=%q, want no-store", got)
	}
}
