package blackbox

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// A check builds a new reader each time. Repeated checks must reuse connections: the recorder
// allows 8 open connections per address, and a pool per reader left one open per check, so the
// fourth or fifth "Check again" was refused and the Blackbox showed as unreachable.
func TestRepeatedReadersDoNotFillThePerAddressLimit(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(TimeHeader, time.Now().UTC().Format(time.RFC3339Nano))
		_, _ = w.Write([]byte("{}"))
	}), IdleTimeout: 30 * time.Second}
	go func() { _ = srv.Serve(newLimitListener(inner, maxConnections, maxConnectionsPerAddr)) }()
	defer srv.Close()

	loc, err := ParseLocation("http://" + inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		src := NewHTTPSource(loc, "key", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		for _, p := range []string{"/manifest", "/status"} {
			if _, _, rerr := src.get(ctx, p, 1024); rerr != nil {
				cancel()
				t.Fatalf("check %d, %s: %v", i+1, p, rerr)
			}
		}
		cancel()
	}
}

// Many checks at the same moment queue on the shared pool instead of each opening a connection.
func TestConcurrentReadersStayUnderThePerAddressLimit(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("{}"))
	})}
	go func() { _ = srv.Serve(newLimitListener(inner, maxConnections, maxConnectionsPerAddr)) }()
	defer srv.Close()
	loc, _ := ParseLocation("http://" + inner.Addr().String())
	errs := make(chan error, 30)
	for i := 0; i < 30; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _, rerr := NewHTTPSource(loc, "key", nil).get(ctx, "/status", 1024)
			if rerr != nil {
				errs <- rerr
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < 30; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("a concurrent check failed: %v", err)
		}
	}
}
