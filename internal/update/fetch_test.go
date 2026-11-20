package update

import (
	"crypto/ed25519"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type feedServer struct {
	srv      *httptest.Server
	files    map[string][]byte // "/rc.json" -> body
	etag     string
	hits     atomic.Int32
	notMod   atomic.Int32
	override func(w http.ResponseWriter, r *http.Request) bool
}

func newFeed(t *testing.T) *feedServer {
	t.Helper()
	fs := &feedServer{files: map[string][]byte{}, etag: `"v1"`}
	fs.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.hits.Add(1)
		if fs.override != nil && fs.override(w, r) {
			return
		}
		body, ok := fs.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == fs.etag && strings.HasSuffix(r.URL.Path, ".json") {
			fs.notMod.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", fs.etag)
		w.Write(body)
	}))
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *feedServer) publish(t *testing.T, channel string, m Manifest, k Key, priv ed25519.PrivateKey) {
	t.Helper()
	b, _ := json.Marshal(m)
	fs.files["/"+channel+".json"] = b
	fs.files["/"+channel+".json.sig"] = []byte(FormatSignature(k.Pub, ed25519.Sign(priv, b)))
}

func (fs *feedServer) fetcher(t *testing.T, k Key, build, current string) *Fetcher {
	return &Fetcher{
		Base: fs.srv.URL, HTTP: fs.srv.Client(), Keys: []Key{k}, Build: build, Current: current,
		StatePath: filepath.Join(t.TempDir(), "state.json"), AllowHost: func(string) bool { return true },
		Now: func() time.Time { return now },
	}
}

func TestFetchAvailableLatestAndRollback(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")

	r := f.Check(t.Context())
	if r.Status != StatusAvailable || r.Manifest.Version != "0.1.0-rc.48" {
		t.Fatalf("%+v", r)
	}
	// Second check: the file is not downloaded again (304), and the answer is the same.
	r = f.Check(t.Context())
	if r.Status != StatusAvailable || fs.notMod.Load() != 1 {
		t.Fatalf("second check: %+v, 304s=%d", r, fs.notMod.Load())
	}
	// Up to date.
	f.Current = "0.1.0-rc.48"
	if r := f.Check(t.Context()); r.Status != StatusLatest {
		t.Fatalf("%+v", r)
	}
	// An older, validly signed manifest (rollback): refused, also after a restart (state file).
	old := goodManifest("0.1.0-rc.40", ChannelRC)
	old.Seq = 40
	fs.etag = `"v2"`
	fs.publish(t, "rc", old, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusRejected {
		t.Fatalf("rollback accepted: %+v", r)
	}
	f2 := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.30")
	f2.StatePath = f.StatePath
	if r := f2.Check(t.Context()); r.Status != StatusRejected {
		t.Fatalf("rollback accepted after restart: %+v", r)
	}
}

func TestFetchRefusals(t *testing.T) {
	k, priv := key(t, ChannelRC)
	other, otherPriv := key(t, ChannelRC)
	fs := newFeed(t)
	good := goodManifest("0.1.0-rc.48", ChannelRC)

	// forged signature
	fs.publish(t, "rc", good, other, otherPriv)
	if r := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47").Check(t.Context()); r.Status != StatusRejected {
		t.Errorf("forged: %+v", r)
	}
	// stable.json swapped in for an rc request: an rc manifest served as stable.json
	fs.publish(t, "rc", good, k, priv)
	fs.files["/stable.json"] = fs.files["/rc.json"]
	fs.files["/stable.json.sig"] = fs.files["/rc.json.sig"]
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	f.Want = ChannelStable
	if r := f.Check(t.Context()); r.Status != StatusRejected {
		t.Errorf("channel swap: %+v", r)
	}
	// a stable build never follows rc
	f = fs.fetcher(t, k, ChannelStable, "1.0.0")
	f.Want = ChannelRC
	if r := f.Check(t.Context()); r.Status != StatusRejected {
		t.Errorf("stable build followed rc: %+v", r)
	}
	// dev build never calls out
	before := fs.hits.Load()
	if r := fs.fetcher(t, k, ChannelRC, "dev").Check(t.Context()); r.Status != StatusDev || fs.hits.Load() != before {
		t.Errorf("dev build called out: %+v", r)
	}
	// no keys: nothing is ever accepted
	f = fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	f.Keys = nil
	if r := f.Check(t.Context()); r.Status != StatusRejected {
		t.Errorf("empty key list: %+v", r)
	}
}

func TestFetchNetworkProblems(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	cases := map[string]func(w http.ResponseWriter, r *http.Request) bool{
		"404": func(w http.ResponseWriter, r *http.Request) bool { http.NotFound(w, r); return true },
		"500": func(w http.ResponseWriter, r *http.Request) bool { w.WriteHeader(500); return true },
		"rate": func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			return true
		},
		"oversize": func(w http.ResponseWriter, r *http.Request) bool {
			w.Write([]byte(strings.Repeat("x", MaxManifestSize+10)))
			return true
		},
		"http redirect": func(w http.ResponseWriter, r *http.Request) bool {
			http.Redirect(w, r, "http://127.0.0.1/rc.json", http.StatusFound)
			return true
		},
	}
	for name, h := range cases {
		fs.override = h
		r := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47").Check(t.Context())
		if r.Status == StatusAvailable || r.Status == StatusLatest {
			t.Errorf("%s: accepted %+v", name, r)
		}
		if name == "rate" && (!r.Backoff || r.RetryIn != 120*time.Second) {
			t.Errorf("rate limit not honoured: %+v", r)
		}
	}
	fs.override = nil
	// redirect to a host that is not allowed
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	f.AllowHost = func(h string) bool { return h == "127.0.0.1" }
	fs.override = func(w http.ResponseWriter, r *http.Request) bool {
		http.Redirect(w, r, "https://evil.example/rc.json", http.StatusFound)
		return true
	}
	if r := f.Check(t.Context()); r.Status == StatusAvailable {
		t.Errorf("redirect to another host followed: %+v", r)
	}
	// too many redirects
	fs.override = func(w http.ResponseWriter, r *http.Request) bool {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
		return true
	}
	if r := f.Check(t.Context()); r.Status == StatusAvailable {
		t.Errorf("redirect loop followed: %+v", r)
	}
}

func TestFeedAddressMustBeAllowedHTTPS(t *testing.T) {
	k, _ := key(t, ChannelRC)
	for _, base := range []string{"http://github.com/x", "https://evil.example/x", "ftp://github.com/x"} {
		f := &Fetcher{Base: base, Keys: []Key{k}, Build: ChannelRC, Current: "0.1.0-rc.1"}
		if r := f.Check(t.Context()); r.Status != StatusRejected {
			t.Errorf("%s: %+v", base, r)
		}
	}
	if !DefaultAllowHost("github.com") || !DefaultAllowHost("release-assets.githubusercontent.com") || DefaultAllowHost("github.com.evil.example") || DefaultAllowHost("notgithub.com") {
		t.Error("default host allowlist is wrong")
	}
}

func TestNextDelay(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	if d := NextDelay(30*time.Minute, 0, 0, nil); d != 30*time.Minute {
		t.Error(d)
	}
	if d := NextDelay(time.Minute, 0, 0, nil); d != 5*time.Minute {
		t.Errorf("minimum interval: %v", d)
	}
	if d := NextDelay(30*time.Minute, 2, 0, nil); d != 2*time.Hour {
		t.Errorf("backoff: %v", d)
	}
	if d := NextDelay(30*time.Minute, 20, 0, nil); d != 6*time.Hour {
		t.Errorf("cap: %v", d)
	}
	if d := NextDelay(30*time.Minute, 0, time.Hour, nil); d != time.Hour {
		t.Errorf("server wait: %v", d)
	}
	for i := 0; i < 100; i++ {
		d := NextDelay(30*time.Minute, 0, 0, rnd)
		if d < 30*time.Minute || d > 33*time.Minute {
			t.Fatalf("jitter out of range: %v", d)
		}
	}
}
