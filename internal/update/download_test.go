package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func dlManifest(payload []byte) (*Manifest, Asset) {
	sum := sha256.Sum256(payload)
	a := Asset{Name: "unravis-setup-v1.2.3-windows-x64.exe", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload))}
	return &Manifest{Version: "1.2.3", Assets: map[string]Asset{AssetSetup: a}}, a
}

func dlServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Downloader) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv, &Downloader{Dir: t.TempDir(), Base: srv.URL, HTTP: srv.Client(), AllowHost: func(string) bool { return true }, Current: "1.0.0"}
}

func TestDownloadGoodFileAndReuse(t *testing.T) {
	payload := bytes.Repeat([]byte("unravis"), 5000)
	m, a := dlManifest(payload)
	var hits atomic.Int32
	var gotPath string
	_, d := dlServer(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); gotPath = r.URL.Path; w.Write(payload) })
	var last int64
	got, err := d.Download(context.Background(), m, AssetSetup, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1.2.3/"+a.Name || got.Path != filepath.Join(d.Dir, a.Name) || got.SHA256 != a.SHA256 || last != a.Size {
		t.Fatalf("%q %+v %d", gotPath, got, last)
	}
	// second call reuses the verified file
	if _, err := d.Download(context.Background(), m, AssetSetup, nil); err != nil || hits.Load() != 1 {
		t.Fatalf("err=%v hits=%d", err, hits.Load())
	}
	// a tampered file on disk is not reused: it is fetched again
	if err := os.WriteFile(got.Path, []byte("evil"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Download(context.Background(), m, AssetSetup, nil); err != nil || hits.Load() != 2 {
		t.Fatalf("err=%v hits=%d", err, hits.Load())
	}
	b, _ := os.ReadFile(got.Path)
	if !bytes.Equal(b, payload) {
		t.Fatal("tampered file stayed")
	}
}

func TestDownloadRefusals(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 4096)
	m, a := dlManifest(payload)
	flip := append([]byte(nil), payload...)
	flip[100] ^= 1
	cases := map[string]http.HandlerFunc{
		"same size, wrong bytes": func(w http.ResponseWriter, r *http.Request) { w.Write(flip) },
		"longer":                 func(w http.ResponseWriter, r *http.Request) { w.Write(append(append([]byte(nil), payload...), 'x')) },
		"shorter":                func(w http.ResponseWriter, r *http.Request) { w.Write(payload[:100]) },
		"404":                    func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"cut off": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "4096")
			w.Write(payload[:100])
			panic(http.ErrAbortHandler)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			_, d := dlServer(t, h)
			if _, err := d.Download(context.Background(), m, AssetSetup, nil); err == nil {
				t.Fatal("accepted")
			}
			ents, _ := os.ReadDir(d.Dir)
			if len(ents) != 0 {
				t.Errorf("left files behind: %v", ents)
			}
		})
	}
	// unknown kind, nil manifest, unsafe name
	_, d := dlServer(t, func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	if _, err := d.Download(context.Background(), m, "lite", nil); err == nil {
		t.Error("unknown kind accepted")
	}
	if _, err := d.Download(context.Background(), nil, AssetSetup, nil); err == nil {
		t.Error("nil manifest accepted")
	}
	bad := *m
	bad.Assets = map[string]Asset{AssetSetup: {Name: "..\\evil.exe", SHA256: a.SHA256, Size: a.Size}}
	if _, err := d.Download(context.Background(), &bad, AssetSetup, nil); err == nil {
		t.Error("unsafe name accepted")
	}
}

func TestDownloadHostRules(t *testing.T) {
	payload := []byte("hello")
	m, _ := dlManifest(payload)
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	t.Cleanup(other.Close)
	// "example.com" is reached through a dialer that goes to the second test server.
	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/f", http.StatusFound)
	}))
	t.Cleanup(first.Close)
	tr := first.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "example.com") {
			addr = other.Listener.Addr().String()
		} else {
			addr = first.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	mk := func(allow func(string) bool) *Downloader {
		return &Downloader{Dir: t.TempDir(), Base: "https://127.0.0.1", HTTP: &http.Client{Transport: tr}, AllowHost: allow}
	}
	// control: with the target host allowed, the redirect is followed and the file arrives
	if _, err := mk(func(string) bool { return true }).Download(context.Background(), m, AssetSetup, nil); err != nil {
		t.Fatalf("control failed: %v", err)
	}
	// the same redirect to a host outside the allowlist is refused
	if _, err := mk(func(h string) bool { return h == "127.0.0.1" }).Download(context.Background(), m, AssetSetup, nil); err == nil {
		t.Error("redirect to a host that is not allowed was followed")
	}
	d2 := &Downloader{Dir: t.TempDir(), Base: "http://github.com/x", AllowHost: func(string) bool { return true }}
	if _, err := d2.Download(context.Background(), m, AssetSetup, nil); err == nil {
		t.Error("http base accepted")
	}
	d3 := &Downloader{Dir: t.TempDir(), Base: "https://evil.example/x"}
	if _, err := d3.Download(context.Background(), m, AssetSetup, nil); err == nil {
		t.Error("host outside the allowlist accepted")
	}
}

func TestDownloadPrune(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"unravis-setup-v1.0.0-windows-x64.exe", "unravis-setup-v1.2.3-windows-x64.exe", ".part-123", "notes.txt"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	(&Downloader{Dir: dir}).Prune("unravis-setup-v1.2.3-windows-x64.exe")
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "notes.txt,unravis-setup-v1.2.3-windows-x64.exe" {
		t.Errorf("%v", names)
	}
}

func TestFileURLRules(t *testing.T) {
	m, a := dlManifest([]byte("x"))
	ok := &Downloader{Base: "https://github.com/abhinavjp/unravis/releases/download"}
	u, err := ok.FileURL(m, a)
	if err != nil || u != "https://github.com/abhinavjp/unravis/releases/download/v1.2.3/"+a.Name {
		t.Fatalf("%q %v", u, err)
	}
	for _, base := range []string{"http://github.com/x", "https://user@github.com/x", "https://github.com/x?a=b", "https://github.com/x#f", "https://evil.example/x", "ftp://github.com/x"} {
		if _, err := (&Downloader{Base: base}).FileURL(m, a); err == nil {
			t.Errorf("accepted %s", base)
		}
	}
	bad := *m
	bad.Version = "../x"
	if _, err := ok.FileURL(&bad, a); err == nil {
		t.Error("bad version accepted")
	}
}

func TestOnlyASetupNameWeWouldMake(t *testing.T) {
	payload := []byte("hello")
	m, a := dlManifest(payload)
	for _, name := range []string{"evil.cmd", "unravis-setup-v1.2.3.exe.cmd", "setup.exe", "unravis-setup-v1.2.3.msi"} {
		bad := *m
		bad.Assets = map[string]Asset{AssetSetup: {Name: name, SHA256: a.SHA256, Size: a.Size}}
		_, d := dlServer(t, func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
		if _, err := d.Download(context.Background(), &bad, AssetSetup, nil); err == nil {
			t.Errorf("downloaded %q", name)
		}
		if _, ok := d.Recheck(&bad, AssetSetup); ok {
			t.Errorf("rechecked %q", name)
		}
	}
}

func TestEndlessBodyIsStoppedAtTheDeclaredSize(t *testing.T) {
	m, a := dlManifest(bytes.Repeat([]byte("x"), 100_000))
	var sent atomic.Int64
	_, d := dlServer(t, func(w http.ResponseWriter, r *http.Request) { // no Content-Length: chunked and endless
		chunk := bytes.Repeat([]byte("y"), 32*1024)
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			sent.Add(int64(len(chunk)))
			if sent.Load() > 64<<20 {
				return // a runaway test server must not fill the machine
			}
		}
	})
	if _, err := d.Download(context.Background(), m, AssetSetup, nil); err == nil {
		t.Fatal("accepted an endless body")
	}
	// The server counts what it wrote into socket buffers, which the system may take in the many megabytes before it sees the client
	// close (3 MB seen on a loaded Windows runner). The client stops at the declared size; a client that kept reading would run to
	// the server's 64 MB cap.
	if sent.Load() > a.Size+16<<20 {
		t.Errorf("the client kept reading: %d bytes sent for a %d byte file", sent.Load(), a.Size)
	}
	if ents, _ := os.ReadDir(d.Dir); len(ents) != 0 {
		t.Errorf("left files: %v", ents)
	}
}

func TestStalledDownloadEnds(t *testing.T) {
	old := downloadIdle
	downloadIdle = 200 * time.Millisecond
	t.Cleanup(func() { downloadIdle = old })
	m, _ := dlManifest(bytes.Repeat([]byte("x"), 100_000))
	_, d := dlServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.Write([]byte("ten bytes!"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // then nothing
	})
	start := time.Now()
	if _, err := d.Download(context.Background(), m, AssetSetup, nil); err == nil {
		t.Fatal("a stalled download was accepted")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v to give up", time.Since(start))
	}
}

func TestRedirectLimit(t *testing.T) {
	payload := []byte("hello")
	m, _ := dlManifest(payload)
	hops := func(n int) error {
		var srv *httptest.Server
		srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var i int
			if _, err := fmt.Sscanf(r.URL.Path, "/h%d", &i); err == nil && i > 0 {
				http.Redirect(w, r, fmt.Sprintf("/h%d", i-1), http.StatusFound)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1.2.3/") {
				if n == 0 {
					w.Write(payload)
				} else {
					http.Redirect(w, r, fmt.Sprintf("/h%d", n-1), http.StatusFound)
				}
				return
			}
			w.Write(payload)
		}))
		defer srv.Close()
		d := &Downloader{Dir: t.TempDir(), Base: srv.URL, HTTP: srv.Client(), AllowHost: func(string) bool { return true }}
		_, err := d.Download(context.Background(), m, AssetSetup, nil)
		return err
	}
	if err := hops(3); err != nil {
		t.Fatalf("three redirects (the limit) were refused: %v", err)
	}
	if err := hops(4); err == nil {
		t.Error("four redirects were followed")
	}
}

func TestASymlinkInPlaceOfTheFileIsNotReused(t *testing.T) {
	payload := bytes.Repeat([]byte("z"), 5000)
	m, a := dlManifest(payload)
	var hits atomic.Int32
	_, d := dlServer(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write(payload) })
	outside := filepath.Join(t.TempDir(), "elsewhere.exe")
	if err := os.WriteFile(outside, payload, 0o600); err != nil { // the right bytes, but behind a link
		t.Fatal(err)
	}
	final := filepath.Join(d.Dir, a.Name)
	if err := os.Symlink(outside, final); err != nil {
		t.Skip("symlinks not available")
	}
	got, err := d.Download(context.Background(), m, AssetSetup, nil)
	if err != nil || hits.Load() != 1 {
		t.Fatalf("err=%v hits=%d (the link was reused)", err, hits.Load())
	}
	if fi, _ := os.Lstat(got.Path); fi.Mode()&os.ModeSymlink != 0 {
		t.Error("the final path is still a link")
	}
	if _, ok := d.Recheck(m, AssetSetup); !ok {
		t.Error("recheck of the real file failed")
	}
	os.Remove(final)
	os.Symlink(outside, final)
	if _, ok := d.Recheck(m, AssetSetup); ok {
		t.Error("recheck accepted a link")
	}
}

// A slow download that keeps making progress is not cut off by the idle limit.
func TestSlowButSteadyDownloadFinishes(t *testing.T) {
	old := downloadIdle
	downloadIdle = 300 * time.Millisecond
	t.Cleanup(func() { downloadIdle = old })
	payload := bytes.Repeat([]byte("s"), 6000)
	m, _ := dlManifest(payload)
	_, d := dlServer(t, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 6; i++ {
			w.Write(payload[i*1000 : (i+1)*1000])
			w.(http.Flusher).Flush()
			time.Sleep(120 * time.Millisecond) // 720 ms in all: longer than the idle limit, but never silent for that long
		}
	})
	if _, err := d.Download(context.Background(), m, AssetSetup, nil); err != nil {
		t.Fatalf("a steady download was cut off: %v", err)
	}
}
