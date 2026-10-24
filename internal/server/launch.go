package server

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// launchPath is the one-time link the program opens in the browser and prints.
// It carries a single-use launch code, never the session token, so the token
// stays out of process command lines and log files.
const launchPath = "/launch"

// launchState holds the pending launch code. The code is random, valid once and
// only until its expiry; a used or expired code answers 404 like any unknown
// path. The reply hands the browser the token in a URL fragment, which the
// browser never sends to a server (the page reads it and removes it).
type launchState struct {
	mu    sync.Mutex
	code  string
	until time.Time
	now   func() time.Time
}

// WithLaunchCode arms the one-time /launch link: GET /launch?c=<code> redirects
// to the dashboard with the session token in the fragment, once, before until.
func WithLaunchCode(code string, until time.Time) Option {
	return func(h *handler) {
		h.launch.mu.Lock()
		h.launch.code, h.launch.until = code, until
		h.launch.mu.Unlock()
	}
}

// take consumes the code when got matches it and it has not expired.
func (l *launchState) take(got string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	if l.code == "" || len(got) != len(l.code) || len(got) > 256 ||
		subtle.ConstantTimeCompare([]byte(got), []byte(l.code)) != 1 {
		return false
	}
	live := now.Before(l.until)
	l.code = "" // spent whether or not it was still live
	return live
}

// serveLaunch answers GET /launch?c=<code>. It is public (the browser has no
// token yet) and rate limited with the other unauthenticated route.
func (h *handler) serveLaunch(w http.ResponseWriter, r *http.Request) {
	if h.token == "" || !h.proofs.allow() {
		http.NotFound(w, r)
		return
	}
	if !h.launch.take(r.URL.Query().Get("c")) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Location", "/#t="+url.QueryEscape(h.token))
	w.WriteHeader(http.StatusFound)
}
