package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/update"
	"api-audit-explorer/internal/version"
)

type fakeFeed struct {
	srv  *httptest.Server
	hits atomic.Int32
	body map[string][]byte
	code int
}

func newFakeFeed(t *testing.T) *fakeFeed {
	t.Helper()
	f := &fakeFeed{body: map[string][]byte{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.code != 0 {
			w.WriteHeader(f.code)
			return
		}
		b, ok := f.body[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFeed) publish(t *testing.T, channel, ver string, seq uint64, key update.Key, priv ed25519.PrivateKey) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	h := func(c string) string { return strings.Repeat(c, 64) }
	m := update.Manifest{
		Schema: 1, Product: "unravis", Version: ver, Channel: channel, Published: now.Add(-time.Hour), Expires: now.Add(30 * 24 * time.Hour),
		Seq: seq, Commit: strings.Repeat("e", 40), BlackboxExeSHA256: h("d"),
		Assets: map[string]update.Asset{
			"setup":    {Name: "unravis-setup-v" + ver + "-windows-x64.exe", SHA256: h("a"), Size: 10},
			"app":      {Name: "unravis-v" + ver + "-windows-x64.zip", SHA256: h("b"), Size: 10},
			"blackbox": {Name: "unravis-blackbox-v" + ver + "-windows-x64.zip", SHA256: h("c"), Size: 10},
		},
	}
	b, _ := json.Marshal(m)
	f.body["/"+channel+".json"] = b
	f.body["/"+channel+".json.sig"] = []byte(update.FormatSignature(key.Pub, ed25519.Sign(priv, b)))
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	version.Version = v
}

func testUpdater(t *testing.T, f *fakeFeed, key update.Key) *Updater {
	t.Helper()
	u := NewUpdater(t.TempDir(), []update.Key{key})
	u.fetcher.Base, u.fetcher.HTTP = f.srv.URL, f.srv.Client()
	u.fetcher.AllowHost = func(string) bool { return true }
	u.fetcher.RetryWait = time.Millisecond
	return u
}

func rcKey(t *testing.T) (update.Key, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return update.Key{Role: update.ChannelRC, Pub: pub}, priv
}

func updHandler(u *Updater, opts ...Option) http.Handler {
	return New(audit.NewDemo(time.Now()), nil, testToken, true, append([]Option{WithUpdater(u)}, opts...)...)
}

func decodeStatus(t *testing.T, code int, body string) UpdateStatus {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	var s UpdateStatus
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdaterEndpointsAvailable(t *testing.T) {
	setVersion(t, "0.1.0-rc.47")
	k, priv := rcKey(t)
	feed := newFakeFeed(t)
	feed.publish(t, "rc", "0.1.0-rc.48", 4801, k, priv)
	h := updHandler(testUpdater(t, feed, k))

	// Before any check there is nothing to show, and no call is made.
	res := request(h, http.MethodGet, "/api/update/status", "", true)
	if s := decodeStatus(t, res.Code, res.Body.String()); s.Status != "none" || !s.Auto || s.IntervalMinutes != 30 || s.Channel != "rc" || feed.hits.Load() != 0 {
		t.Fatalf("%+v hits=%d", s, feed.hits.Load())
	}
	res = request(h, http.MethodPost, "/api/update-check", "", true)
	s := decodeStatus(t, res.Code, res.Body.String())
	if s.Status != "available" || s.Latest != "0.1.0-rc.48" || s.URL != "https://github.com/abhinavjp/unravis/releases/tag/v0.1.0-rc.48" || s.CheckedAt == "" {
		t.Fatalf("%+v", s)
	}
	// The status endpoint now remembers it without another call.
	hits := feed.hits.Load()
	res = request(h, http.MethodGet, "/api/update/status", "", true)
	if s2 := decodeStatus(t, res.Code, res.Body.String()); s2.Status != "available" || feed.hits.Load() != hits {
		t.Fatalf("%+v", s2)
	}
}

func TestUpdaterStatesAndRefusals(t *testing.T) {
	setVersion(t, "0.1.0-rc.47")
	k, priv := rcKey(t)
	other, _ := rcKey(t)
	feed := newFakeFeed(t)

	// 404 feed: unreachable (the feed does not exist yet)
	h := updHandler(testUpdater(t, feed, k))
	res := request(h, http.MethodPost, "/api/update-check", "", true)
	if s := decodeStatus(t, res.Code, res.Body.String()); s.Status != "unreachable" {
		t.Errorf("404: %+v", s)
	}
	// signed by a key we do not trust: unverified
	feed.publish(t, "rc", "0.1.0-rc.48", 4801, k, priv)
	h = updHandler(testUpdater(t, feed, other))
	res = request(h, http.MethodPost, "/api/update-check", "", true)
	if s := decodeStatus(t, res.Code, res.Body.String()); s.Status != "unverified" || s.Latest != "" || s.URL != "" {
		t.Errorf("wrong key: %+v", s)
	}
	// up to date
	setVersion(t, "0.1.0-rc.48")
	h = updHandler(testUpdater(t, feed, k))
	res = request(h, http.MethodPost, "/api/update-check", "", true)
	if s := decodeStatus(t, res.Code, res.Body.String()); s.Status != "latest" {
		t.Errorf("latest: %+v", s)
	}
	// development build: no call at all
	setVersion(t, "dev")
	before := feed.hits.Load()
	h = updHandler(testUpdater(t, feed, k))
	res = request(h, http.MethodPost, "/api/update-check", "", true)
	if s := decodeStatus(t, res.Code, res.Body.String()); s.Status != "dev" || feed.hits.Load() != before {
		t.Errorf("dev: %+v", s)
	}
	// method and token rules
	if res := request(h, http.MethodGet, "/api/update-check", "", true); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET update-check = %d", res.Code)
	}
	if res := request(h, http.MethodPost, "/api/update-check", "", false); res.Code != http.StatusUnauthorized && res.Code != http.StatusForbidden {
		t.Errorf("no token = %d", res.Code)
	}
	if res := request(h, http.MethodPost, "/api/update/status", "", true); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d", res.Code)
	}
}

func TestUpdaterSettings(t *testing.T) {
	setVersion(t, "0.1.0-rc.47")
	k, _ := rcKey(t)
	feed := newFakeFeed(t)
	dir := t.TempDir()
	mk := func() *Updater {
		u := NewUpdater(dir, []update.Key{k})
		u.fetcher.Base, u.fetcher.HTTP = feed.srv.URL, feed.srv.Client()
		return u
	}
	h := updHandler(mk())
	put := func(body string) (int, string) {
		rec := request(h, http.MethodPut, "/api/update/settings", body, true)
		return rec.Code, rec.Body.String()
	}
	if code, body := put(`{"auto":false,"intervalMinutes":360}`); code != 200 {
		t.Fatalf("%d %s", code, body)
	} else if s := decodeStatus(t, code, body); s.Auto || s.IntervalMinutes != 360 {
		t.Fatalf("%+v", s)
	}
	// a new process reads it back
	if s := mk().Status(); s.Auto || s.IntervalMinutes != 360 {
		t.Fatalf("not kept: %+v", s)
	}
	for _, bad := range []string{`{"auto":true,"intervalMinutes":1}`, `{"auto":true,"intervalMinutes":99999999}`, `{"auto":true,"intervalMinutes":-5}`, `{"auto":true,"x":1}`, `nope`} {
		if code, _ := put(bad); code == http.StatusOK {
			t.Errorf("accepted %s", bad)
		}
	}
	if s := mk().Status(); s.IntervalMinutes != 360 {
		t.Errorf("a refused change was kept: %+v", s)
	}
	// no token, wrong method
	rec := request(h, http.MethodPut, "/api/update/settings", `{"auto":true,"intervalMinutes":30}`, false)
	if rec.Code == http.StatusOK {
		t.Error("PUT without the token worked")
	}
	if res := request(h, http.MethodPost, "/api/update/settings", "", true); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST settings = %d", res.Code)
	}
	// a damaged settings file falls back to the defaults
	if err := writeFileForTest(filepath.Join(dir, "settings.json"), `{"auto":true,"intervalMinutes":1`); err != nil {
		t.Fatal(err)
	}
	if s := mk().Status(); !s.Auto || s.IntervalMinutes != 30 {
		t.Errorf("damaged file: %+v", s)
	}
}

func TestUpdaterBackgroundLoop(t *testing.T) {
	setVersion(t, "0.1.0-rc.47")
	old := updateFirstDelay
	updateFirstDelay = 20 * time.Millisecond
	t.Cleanup(func() { updateFirstDelay = old })
	k, priv := rcKey(t)
	feed := newFakeFeed(t)
	feed.publish(t, "rc", "0.1.0-rc.48", 4801, k, priv)

	// on: the first check happens by itself
	u := testUpdater(t, feed, k)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { u.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for u.Status().Status != "available" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := u.Status(); s.Status != "available" {
		t.Fatalf("the background check never ran: %+v", s)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop did not stop with its context")
	}

	// off: nothing is ever checked
	feed2 := newFakeFeed(t)
	feed2.publish(t, "rc", "0.1.0-rc.48", 4801, k, priv)
	u2 := testUpdater(t, feed2, k)
	if _, err := u2.SetSettings(UpdaterSettings{Auto: false, IntervalMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(t.Context())
	done2 := make(chan struct{})
	go func() { u2.Run(ctx2); close(done2) }()
	time.Sleep(300 * time.Millisecond)
	if feed2.hits.Load() != 0 {
		t.Errorf("a check ran while updates were switched off (%d calls)", feed2.hits.Load())
	}
	// switching it on wakes the loop and the check follows
	if _, err := u2.SetSettings(UpdaterSettings{Auto: true, IntervalMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for u2.Status().Status != "available" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := u2.Status(); s.Status != "available" {
		t.Errorf("switching on did not start the checks: %+v", s)
	}
	cancel2()
	<-done2

	// a development build never starts a loop
	setVersion(t, "dev")
	devFeed := newFakeFeed(t)
	u3 := testUpdater(t, devFeed, k)
	ctx3, cancel3 := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel3()
	u3.Run(ctx3) // returns at once
	if devFeed.hits.Load() != 0 {
		t.Error("a development build called out")
	}
}

func TestUpdateDemoPreviewsAndNoUpdater(t *testing.T) {
	demo := New(audit.NewDemo(time.Now()), nil, testToken, true, WithUpdateCheckDemo())
	for preview, want := range map[string]string{"latest": "latest", "available": "available", "offline": "unreachable", "unverified": "unverified"} {
		res := request(demo, http.MethodPost, "/api/update-check?preview="+preview, "", true)
		if s := decodeStatus(t, res.Code, res.Body.String()); s.Status != want {
			t.Errorf("%s: %+v", preview, s)
		}
	}
	plain := New(audit.NewDemo(time.Now()), nil, testToken, true)
	res := request(plain, http.MethodPost, "/api/update-check?preview=available", "", true)
	if s := decodeStatus(t, res.Code, res.Body.String()); s.Status != "dev" {
		t.Errorf("preview worked without the demo switch: %+v", s)
	}
	rec := request(plain, http.MethodPut, "/api/update/settings", `{"auto":true,"intervalMinutes":30}`, true)
	if rec.Code != http.StatusConflict {
		t.Errorf("settings without an updater = %d", rec.Code)
	}
}

func writeFileForTest(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }
