package bbtunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// pipeTunnel serves an in-process recorder as if it were behind the SSH connection.
type pipeTunnel struct {
	h     http.Handler
	mu    sync.Mutex
	dials int
}

func (p *pipeTunnel) DialTunnel(ctx context.Context) (net.Conn, error) {
	p.mu.Lock()
	p.dials++
	p.mu.Unlock()
	a, b := net.Pipe()
	srv := &http.Server{Handler: p.h, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(&oneConn{c: b}) //nolint:errcheck // test helper, error not needed
	return a, nil
}

type oneConn struct {
	c    net.Conn
	once sync.Once
	done chan struct{}
}

func (o *oneConn) Accept() (net.Conn, error) {
	var c net.Conn
	o.once.Do(func() { c = o.c; o.done = make(chan struct{}) })
	if c != nil {
		return c, nil
	}
	select {}
}
func (o *oneConn) Close() error   { return nil }
func (o *oneConn) Addr() net.Addr { return &net.TCPAddr{} }

func TestSourceReadsThroughTheTunnelWithTheKeyInTheHeaderOnly(t *testing.T) {
	var gotKey, gotURI string
	pt := &pipeTunnel{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotURI = r.Header.Get(blackbox.KeyHeader), r.RequestURI
		if gotKey != "the-read-key" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/files/bb-20261126-09.ndjson":
			io.WriteString(w, "line one\n") //nolint:errcheck,gosec // test helper, error not needed
		case "/redirect":
			http.Redirect(w, r, "http://elsewhere.invalid/", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})}
	src, err := NewSource(pt, DefaultAddr, "the-read-key")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := src.OpenFile(context.Background(), "bb-20261126-09.ndjson")
	if err != nil {
		t.Fatalf("%v (uri %q)", err, gotURI)
	}
	b, _ := io.ReadAll(rc)
	rc.Close() //nolint:errcheck,gosec // test helper, error not needed
	if string(b) != "line one\n" || strings.Contains(gotURI, "the-read-key") {
		t.Fatalf("%q uri=%q", b, gotURI)
	}
	if pt.dials == 0 {
		t.Fatal("no tunnel dial happened")
	}

	bad, _ := NewSource(pt, DefaultAddr, "wrong")
	_, err = bad.OpenFile(context.Background(), "bb-20261126-09.ndjson")
	if blackbox.KindOf(err) != blackbox.ReadKeyRefused || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("%v", err)
	}
}

func TestSourceRefusesBadAddressesAndSurfacesTunnelFailure(t *testing.T) {
	for _, a := range []string{"", "nohost", "127.0.0.1:0", "127.0.0.1:99999", "a b:80"} {
		if _, err := NewSource(&pipeTunnel{}, a, "k"); err == nil {
			t.Errorf("accepted %q", a)
		}
	}
	src, _ := NewSource(failTunnel{}, DefaultAddr, "k")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := src.Status(ctx); err == nil || blackbox.KindOf(err) != blackbox.ReadUnreachable {
		t.Fatalf("%v", err)
	}
	_ = httptest.NewRecorder
}

type failTunnel struct{}

func (failTunnel) DialTunnel(context.Context) (net.Conn, error) { return nil, errors.New("refused") }
