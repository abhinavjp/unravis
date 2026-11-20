package update

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		Now: func() time.Time { return now }, RetryWait: time.Millisecond,
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
	restart()
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
	cases := []struct {
		name string
		h    func(w http.ResponseWriter, r *http.Request) bool
		want Status
	}{
		{"404", func(w http.ResponseWriter, r *http.Request) bool { http.NotFound(w, r); return true }, StatusUnreachable},
		{"500", func(w http.ResponseWriter, r *http.Request) bool { w.WriteHeader(500); return true }, StatusUnreachable},
		{"503 with wait", func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(503)
			return true
		}, StatusUnreachable},
		{"rate", func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			return true
		}, StatusUnreachable},
		{"oversize", func(w http.ResponseWriter, r *http.Request) bool {
			w.Write([]byte(strings.Repeat("x", MaxManifestSize+10)))
			return true
		}, StatusUnreachable},
		{"captive portal page", func(w http.ResponseWriter, r *http.Request) bool {
			w.Write([]byte("<html>sign in to the wifi</html>"))
			return true
		}, StatusRejected},
	}
	for _, c := range cases {
		fs.override = c.h
		r := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47").Check(t.Context())
		if r.Status != c.want {
			t.Errorf("%s: got %+v", c.name, r)
		}
		if strings.Contains(r.Detail, "wifi") || strings.Contains(r.Detail, "xxxx") {
			t.Errorf("%s: response body leaked into Detail: %q", c.name, r.Detail)
		}
		if (c.name == "rate" || c.name == "503 with wait") && (!r.Backoff || r.RetryIn != 120*time.Second) {
			t.Errorf("%s: wait not honoured: %+v", c.name, r)
		}
	}
}

func TestRedirectRules(t *testing.T) {
	k, priv := key(t, ChannelRC)
	good := newFeed(t) // a second server that would serve a valid, signed pair
	good.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.40", ChannelRC), k, priv)

	// Redirect to a plain http:// server that would serve a valid pair: it must not be followed.
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
		w.Write(good.files[r.URL.Path])
	}))
	t.Cleanup(plain.Close)
	fs.override = func(w http.ResponseWriter, r *http.Request) bool {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
		return true
	}
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	if r := f.Check(t.Context()); r.Status != StatusUnreachable || plainHits.Load() != 0 {
		t.Errorf("http redirect: %+v, plain server hit %d times", r, plainHits.Load())
	}
	// Redirect to a host the allowlist refuses (example.com, which the test transport maps to the valid server).
	goodAddr := good.srv.Listener.Addr().String()
	_, goodPort, _ := net.SplitHostPort(goodAddr)
	fs.override = func(w http.ResponseWriter, r *http.Request) bool {
		http.Redirect(w, r, "https://example.com:"+goodPort+r.URL.Path, http.StatusFound)
		return true
	}
	fs2 := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	fs2.AllowHost = func(h string) bool { return h == "127.0.0.1" }
	tr := fs.srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.RootCAs.AddCert(good.srv.Certificate())
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasSuffix(addr, ":"+goodPort) {
			addr = goodAddr
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	fs2.HTTP = &http.Client{Transport: tr}
	if r := fs2.Check(t.Context()); r.Status != StatusUnreachable || good.hits.Load() != 0 {
		t.Errorf("redirect to a disallowed host followed: %+v, other server hit %d times", r, good.hits.Load())
	}
	// Control: when the allowlist does permit that host, the redirect IS followed (so the test above proves the check).
	fs2.AllowHost = func(h string) bool { return h == "127.0.0.1" || h == "example.com" }
	if _ = fs2.Check(t.Context()); good.hits.Load() == 0 {
		t.Errorf("control failed: the redirect target was never reached, so the test above proves nothing")
	}
	// Exactly 3 redirects are allowed, the 4th is refused. One counter per file, so the manifest and its signature are separate.
	for _, hops := range []int{3, 4} {
		var n, m atomic.Int32
		fs.override = func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/hop-rc.json" || r.URL.Path == "/hop-rc.json.sig" {
				return false
			}
			c := &n
			if strings.HasSuffix(r.URL.Path, ".sig") {
				c = &m
			}
			if int(c.Add(1)) <= hops {
				http.Redirect(w, r, r.URL.Path, http.StatusFound) // back to itself: counts hops
				return true
			}
			return false
		}
		r := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47").Check(t.Context())
		if hops == 3 && r.Status != StatusLatest { // this feed serves rc.40, so a followed fetch reads as up to date
			t.Errorf("3 redirects should be followed: %+v", r)
		}
		if hops == 4 && r.Status != StatusUnreachable {
			t.Errorf("a 4th redirect was followed: %+v", r)
		}
	}
}

func TestAntiRollbackWithoutStateFile(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	for _, statePath := range []string{"", "corrupt"} {
		f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.30")
		switch statePath {
		case "":
			f.StatePath = ""
		case "corrupt":
			f.StatePath = filepath.Join(t.TempDir(), "state.json")
		}
		fs.etag = `"a` + statePath + `"`
		fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
		if r := f.Check(t.Context()); r.Status != StatusAvailable {
			t.Fatalf("%s: %+v", statePath, r)
		}
		if statePath == "corrupt" { // power cut left a broken file
			if err := os.WriteFile(f.StatePath, []byte(`{"seenSeq":{"rc":48`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		old := goodManifest("0.1.0-rc.40", ChannelRC)
		old.Seq = 40
		fs.etag = `"b` + statePath + `"`
		fs.publish(t, "rc", old, k, priv)
		r := f.Check(t.Context())
		if r.Status != StatusRejected {
			t.Errorf("%s: rollback accepted: %+v", statePath, r)
		}
		// A new process after the refusal must still refuse it: the file was put right during the refused check.
		restart()
		f3 := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.30")
		f3.StatePath = f.StatePath
		fs.etag = `"c` + statePath + `"`
		if statePath != "" {
			if r3 := f3.Check(t.Context()); r3.Status != StatusRejected {
				t.Errorf("%s: rollback accepted after a restart: %+v", statePath, r3)
			}
		}
		if statePath == "corrupt" {
			if !strings.Contains(r.Detail, "unreadable") {
				t.Errorf("a broken state file was not reported: %q", r.Detail)
			}
			if m, _ := filepath.Glob(f.StatePath + ".corrupt-*"); len(m) != 1 {
				t.Errorf("broken file not set aside: %v", m)
			}
		}
	}
}

func TestSameSeqDifferentContentRefused(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatal(r)
	}
	other := goodManifest("0.1.0-rc.49", ChannelRC) // same seq 48, different body
	fs.etag = `"x"`
	fs.publish(t, "rc", other, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusRejected {
		t.Errorf("different body under the same seq accepted: %+v", r)
	}
}

func TestTwoChannelsOneStateFile(t *testing.T) {
	k, priv := key(t, ChannelRC)
	sk, spriv := key(t, ChannelStable)
	fs := newFeed(t)
	stM := goodManifest("1.0.0", ChannelStable)
	stM.Seq = 50
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	fs.publish(t, "stable", stM, sk, spriv)
	for i := 0; i < 300; i++ { // many rounds, each from an empty file: without the lock one channel's update is lost
		restart()
		path := filepath.Join(t.TempDir(), "state.json")
		var wg sync.WaitGroup
		for _, w := range []string{ChannelRC, ChannelStable} {
			f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.1")
			f.Keys = []Key{k, sk}
			f.Want, f.StatePath = w, path
			wg.Add(1)
			go func() { defer wg.Done(); f.Check(t.Context()) }()
		}
		wg.Wait()
		b, _ := os.ReadFile(path)
		var st State
		if err := json.Unmarshal(b, &st); err != nil || st.SeenSeq[ChannelRC] != 48 || st.SeenSeq[ChannelStable] != 50 {
			t.Fatalf("round %d lost an update: %s %v", i, b, err)
		}
	}
}

func TestUnreadableStateFailsClosed(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.30")
	f.StatePath = t.TempDir() // a directory where the file should be: reading it fails
	r := f.Check(t.Context())
	if r.Status != StatusUnreachable || !strings.Contains(r.Detail, "update memory") {
		t.Fatalf("a read problem did not fail closed: %+v", r)
	}
}

func TestTransientBadPairIsRetriedOnce(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	goodSig := fs.files["/rc.json.sig"]
	var calls atomic.Int32
	fs.override = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/rc.json.sig" && calls.Add(1) == 1 { // first read: the old signature, mid-upload
			w.Write([]byte(strings.Replace(string(goodSig), "unravis-sig-v1", "unravis-sig-v1", 1)[:20]))
			return true
		}
		return false
	}
	if r := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47").Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("a mid-upload read was not retried: %+v", r)
	}
}

func TestBaseMustBePlain(t *testing.T) {
	k, _ := key(t, ChannelRC)
	for _, base := range []string{"https://u:p@github.com/x", "https://github.com/x?a=b", "https://github.com/x#f", "https://github.com:8443/x"} {
		f := &Fetcher{Base: base, Keys: []Key{k}, Build: ChannelRC, Current: "0.1.0-rc.1"}
		if r := f.Check(t.Context()); r.Status != StatusRejected {
			t.Errorf("%s: %+v", base, r)
		}
	}
}

func TestCancelledCheckIsNotAFailure(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if r := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47").Check(ctx); r.Backoff {
		t.Errorf("shutdown counted as a failure: %+v", r)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	mk := func(v string) *http.Response { return &http.Response{Header: http.Header{"Retry-After": []string{v}}} }
	for v, want := range map[string]time.Duration{"120": 2 * time.Minute, "120abc": 0, "-5": 0, "0": 0, "": 0, "999999": maxBackoff, "Wed, 21 Oct 2026 07:28:00 GMT": 0} {
		if got := retryAfter(mk(v)); got != want {
			t.Errorf("%q: %v want %v", v, got, want)
		}
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
	if d := NextDelay(30*time.Minute, 0, time.Duration(1<<62), rnd); d < 0 || d > 7*time.Hour {
		t.Errorf("huge server wait: %v", d)
	}
	if d := NextDelay(24*time.Hour, 0, 0, nil); d != 24*time.Hour {
		t.Errorf("a long interval is kept: %v", d)
	}
	for i := 0; i < 100; i++ {
		d := NextDelay(30*time.Minute, 0, 0, rnd)
		if d < 30*time.Minute || d > 33*time.Minute {
			t.Fatalf("jitter out of range: %v", d)
		}
	}
}

// restart forgets the in-process memory, as a new process would; only the state file remains.
func restart() {
	stateMu.Lock()
	memByPath = map[string]*seenMem{}
	stateMu.Unlock()
}
