package releasedash

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
)

const cookieName = "rd_session"

// The page is served from embedded files, and with X-Content-Type-Options: nosniff the browser trusts only the type
// we send. On Windows Go reads types from the registry, which a tidy-up tool can break, so name the ones we need.
func init() {
	for ext, typ := range map[string]string{
		".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".svg": "image/svg+xml",
		".woff2": "font/woff2", ".ico": "image/x-icon", ".html": "text/html; charset=utf-8",
	} {
		_ = mime.AddExtensionType(ext, typ)
	}
}

// Handler serves the page and its JSON routes. Every route but POST /api/action/<id> (see actions.go) is read-only
// (GET and HEAD only). All answer only to a loopback host name (so a web page cannot reach them by DNS rebinding) and
// need the one-time code from the start link once per browser.
func Handler(d *Dash, token string, ui fs.FS) http.Handler {
	files := http.FileServer(http.FS(ui))
	actionToken := d.ActionToken
	mux := http.NewServeMux()
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		// The action list only. The action token is never served: anything holding the session cookie (any loopback
		// listener the browser sends it to) could read it here. The page has it from the start link's fragment.
		writeJSON(w, map[string]any{"actions": actionList(), "actionsOn": actionToken != ""})
	})
	mux.HandleFunc("/api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		s := d.Snapshot(ctx, r.URL.Query().Get("refresh") == "1")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})
	d.registerDetail(mux)
	d.registerCI(mux)
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/api/bye", func(w http.ResponseWriter, r *http.Request) {
		// The page is closing (or reloading). The program stops after a short wait unless the page comes back.
		if d.OnBye != nil {
			d.OnBye()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, actionPrefix) {
			setSecurityHeaders(w)
			d.serveAction(w, r, token, actionToken)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "read only", http.StatusMethodNotAllowed)
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
		if d.OnRequest != nil {
			d.OnRequest()
		}
		setSecurityHeaders(w)
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/" || strings.Count(r.URL.Path, "/") == 1 {
			mux.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; font-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
