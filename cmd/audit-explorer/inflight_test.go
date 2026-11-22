package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInflightWaitsForRunningHandler(t *testing.T) {
	var f inflight
	started, release := make(chan struct{}), make(chan struct{})
	h := f.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	}))
	done := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)); close(done) }()
	<-started
	if f.wait(50 * time.Millisecond) {
		t.Fatal("wait reported idle while a handler was running")
	}
	go func() { time.Sleep(50 * time.Millisecond); close(release) }()
	if !f.wait(5 * time.Second) {
		t.Fatal("wait did not see the handler return")
	}
	<-done
	if !f.wait(0) {
		t.Fatal("not idle after the handler returned")
	}
}

// A drain that times out: the servers are closed hard (errDrainTimeout), run() waits for the running handler, and the exit is still
// clean. Any other error is passed on, and a handler that never returns does not hold the exit longer than the wait.
func TestSettlePipeShutdown(t *testing.T) {
	var idle inflight
	if err := settlePipeShutdown(nil, &idle, time.Second); err != nil {
		t.Fatalf("clean stop: %v", err)
	}
	if err := settlePipeShutdown(errDrainTimeout, &idle, time.Second); err != nil {
		t.Fatalf("drain timeout with nothing running: %v", err)
	}
	other := errors.New("listener failed")
	if err := settlePipeShutdown(other, &idle, time.Second); !errors.Is(err, other) {
		t.Fatalf("other errors must pass through, got %v", err)
	}

	var busy inflight
	release := make(chan struct{})
	started := make(chan struct{})
	go busy.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(started); <-release })).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	<-started
	t0 := time.Now()
	if err := settlePipeShutdown(errDrainTimeout, &busy, 100*time.Millisecond); err != nil {
		t.Fatalf("drain timeout with a stuck handler: %v", err)
	}
	if d := time.Since(t0); d < 90*time.Millisecond || d > 2*time.Second {
		t.Fatalf("waited %v, want about 100 ms", d)
	}
	close(release)
}

// shutdownOnSignalWithin reports a hard close as errDrainTimeout, and still closes the server.
func TestShutdownOnSignalDrainTimeoutIsSentinel(t *testing.T) {
	srv := &fakeShutdown{err: errors.New("deadline")}
	err := shutdownOnSignalWithin(10*time.Millisecond, func() {}, srv)
	if !errors.Is(err, errDrainTimeout) || !srv.closed {
		t.Fatalf("err %v closed %v", err, srv.closed)
	}
}
