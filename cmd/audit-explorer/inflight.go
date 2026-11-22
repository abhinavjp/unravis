package main

import (
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

// errDrainTimeout is what shutdownOnSignalWithin returns when the http servers still had a request running at the end of the drain
// time and were closed hard.
var errDrainTimeout = errors.New("server shutdown did not finish cleanly")

// pipeHandlerWait is how long run() waits, after a hard close in pipe mode, for the handlers that were still running to return. The
// shell waits 5 s in all: 2 s of drain (pipeShutdownTimeout), this, then the case close and exit.
const pipeHandlerWait = 1500 * time.Millisecond

// inflight counts the requests being served. http.Server.Close does not wait for a running handler, so without this the deferred
// case close and import stop could run while a handler is still writing to a case.
type inflight struct {
	mu   sync.Mutex
	n    int
	idle chan struct{} // closed while n == 0 and a waiter is parked; nil otherwise
}

func (f *inflight) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.n++
		f.mu.Unlock()
		defer func() {
			f.mu.Lock()
			f.n--
			if f.n == 0 && f.idle != nil {
				close(f.idle)
				f.idle = nil
			}
			f.mu.Unlock()
		}()
		next.ServeHTTP(w, r)
	})
}

// wait returns true when no request is running, waiting up to d for the running ones to return.
func (f *inflight) wait(d time.Duration) bool {
	f.mu.Lock()
	if f.n == 0 {
		f.mu.Unlock()
		return true
	}
	if f.idle == nil {
		f.idle = make(chan struct{})
	}
	idle := f.idle
	f.mu.Unlock()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-idle:
		return true
	case <-t.C:
		return false
	}
}

// settlePipeShutdown ends a pipe-mode run. The servers are closed hard when a request outlived the drain; the handlers that are still
// running get a short time to return so the case close that follows does not race them. A request cut off this way was never answered,
// so its caller knows it was not saved; the data on disk is whole, so a forced close of that kind is logged and the exit code stays 0.
// Any other error is returned as it is.
func settlePipeShutdown(err error, running *inflight, wait time.Duration) error {
	if err != nil && !errors.Is(err, errDrainTimeout) {
		return err
	}
	clean := running.wait(wait)
	if err != nil {
		log.Printf("A request was still running after the %v drain; the servers were closed and the request was cut off", pipeShutdownTimeout)
	}
	if !clean {
		log.Printf("A request was still running %v after the servers closed; the cases are closed anyway", wait)
	}
	return nil
}
