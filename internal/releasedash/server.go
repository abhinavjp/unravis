package releasedash

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"
)

const cookieName = "rd_session"

// Handler serves the page and one JSON route. It is read-only (GET and HEAD only), answers only to a loopback host name
// (so a web page cannot reach it by DNS rebinding), and needs the one-time code from the start link once per browser.
func Handler(d *Dash, token string, ui fs.FS) http.Handler {
	files := http.FileServer(http.FS(ui))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		s := d.Snapshot(ctx, r.URL.Query().Get("refresh") == "1")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})
	mux.Handle("/", files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "read only", http.StatusMethodNotAllowed)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if t := r.URL.Query().Get("t"); t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if c, err := r.Cookie(cookieName); err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) != 1 {
			http.Error(w, "Open the link printed by unravis-releases.", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/" || strings.Count(r.URL.Path, "/") == 1 {
			mux.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}
