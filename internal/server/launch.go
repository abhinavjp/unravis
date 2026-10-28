package server

import (
	"crypto/subtle"
	"io"
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
// only until its expiry; a used or expired code answers 404 with one plain page. The reply hands the browser the token in a URL fragment, which the
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

// launchGonePage is the answer for a start link that cannot be used. One text for
// a used, expired or wrong code: it says nothing about which. No script, no token.
const launchGonePage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Link already used</title>
<style>
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;margin:0;padding:48px 16px;background:#fafaf8;color:#1d1d1b}
main{max-width:34rem;margin:0 auto}
h1{font-size:1.4rem;margin:0 0 .75rem}
p{font-size:1rem;line-height:1.5;margin:0}
@media (prefers-color-scheme:dark){body{background:#161614;color:#ecece8}}
</style>
</head>
<body>
<main>
<h1>This link has already been used or has expired</h1>
<p>A start link works once and only for a few minutes. Close this tab, then start Unravis again to open a fresh link.</p>
</main>
</body>
</html>
`

// serveLaunch answers GET /launch?c=<code>. It is public (the browser has no
// token yet) and rate limited with the other unauthenticated route.
func (h *handler) serveLaunch(w http.ResponseWriter, r *http.Request) {
	if h.token == "" || !h.proofs.allow() {
		http.NotFound(w, r)
		return
	}
	if !h.launch.take(r.URL.Query().Get("c")) {
		hd := w.Header()
		hd.Set("Content-Type", "text/html; charset=utf-8")
		hd.Set("Cache-Control", "no-store")
		hd.Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, launchGonePage)
		return
	}
	w.Header().Set("Location", "/#t="+url.QueryEscape(h.token))
	w.WriteHeader(http.StatusFound)
}
