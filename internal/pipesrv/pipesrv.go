// Package pipesrv serves the existing dashboard handler over the two secured named pipes of the desktop sidecar (BL-301).
//
// The listeners come from internal/pipesec (the only pipe listener of Unravis; no other pipe library is used). The role of a pipe
// ("ui" for the shell, "ag" for the CLI and MCP) is put on every connection's context and decides what the handler lets through.
// Everything here is portable: it works on any net.Listener, so the rules are tested on every OS; only pipesec.Listen is Windows-only.
package pipesrv

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"

	"api-audit-explorer/internal/pipesec"
)

// Role says which pipe a request came in on.
type Role string

// The roles (docs/desktop/PLAN.md section 1).
const (
	RoleUI Role = "ui"
	RoleAG Role = "ag"
)

type roleKey struct{}

// WithRole returns ctx carrying role.
func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey{}, role)
}

// RoleFrom returns the role on ctx; ok is false when the request did not come in on a pipe.
func RoleFrom(ctx context.Context) (Role, bool) {
	r, ok := ctx.Value(roleKey{}).(Role)
	return r, ok
}

// agPrefixes are the paths the ag pipe passes on (spec 03 /mcp/v1/*, spec 05 /cli/v1/*). The GUI routes are never reachable there.
var agPrefixes = []string{"/mcp/v1/", "/cli/v1/"}

// agRoutes are the exact read routes today's MCP bridge uses (internal/mcp), which the ag pipe serves until area 03 supplies its own
// handler (spec 01 D5). Everything else, the GUI routes included, is a 404 there.
var agRoutes = map[string]bool{
	"/api/info": true, "/api/sources": true, "/api/analyze": true, "/api/requests": true,
	"/api/activity": true, "/api/slowest": true, "/api/breakdown": true, "/api/session-proof": true,
}

const tokenHeader = "X-Audit-Token" //nolint:gosec // a header name, not a credential

// ShutdownPath is the shutdown RPC (BL-303). It exists only on the ui pipe: loopback and the ag pipe never serve it.
const ShutdownPath = "/api/internal/shutdown"

// Option changes what Handler serves on the ui pipe.
type Option func(*config)

type config struct {
	token string
	stop  func()
}

// WithShutdown turns on POST ShutdownPath on the ui pipe. A request must carry token in the session token header (the same token as
// every other API call); stop starts the normal quit, the one --quit and Ctrl+C run, and is called after the 202 is written.
func WithShutdown(token string, stop func()) Option {
	return func(c *config) { c.token, c.stop = token, stop }
}

// shutdownRoute answers the shutdown RPC. The caller has already put the request on the ui role.
func (c *config) shutdownRoute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	got := r.Header.Get(tokenHeader)
	if c.token == "" || len(got) != len(c.token) || subtle.ConstantTimeCompare([]byte(got), []byte(c.token)) != 1 {
		http.Error(w, "invalid or missing session token", http.StatusUnauthorized)
		return
	}
	// A browser page can never reach a pipe, but a request that claims to come from one is not the shell.
	if r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"state": "stopping"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// The route answers before the served handler, so its request log never sees this call; leave the line here.
	log.Print("Shutdown requested over the ui pipe")
	c.stop()
}

// Handler wraps the dashboard handler served for the pipes.
//
// A request with no pipe role is refused (fail closed). The session token stays the client's to send, unchanged, on both pipes
// (spec 01 D6): the shell and the CLI read it from the locator, so browser mode and pipe mode share one auth path and the peer check of
// pipesec is not the only gate. The ag pipe reaches only agRoutes and agPrefixes, and only by their clean path.
func Handler(served http.Handler, opts ...Option) http.Handler {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := RoleFrom(r.Context())
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch role {
		case RoleUI:
			if r.URL.Path == ShutdownPath && cfg.stop != nil {
				cfg.shutdownRoute(w, r)
				return
			}
		case RoleAG:
			p := r.URL.Path
			if path.Clean(p) != p || (!agRoutes[p] && !hasAnyPrefix(p, agPrefixes)) {
				http.NotFound(w, r)
				return
			}
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		served.ServeHTTP(w, r)
	})
}

func hasAnyPrefix(p string, prefixes []string) bool {
	for _, x := range prefixes {
		if strings.HasPrefix(p, x) {
			return true
		}
	}
	return false
}

// logf is the log sink for peer lines; tests replace it.
var logf = log.Printf

// logPeer records who connected: PID, image path and the last 8 characters of the logon SID, nothing else (spec 02 section 6).
func logPeer(role Role, c net.Conn) {
	pc, ok := c.(interface{ Peer() pipesec.Peer })
	if !ok {
		return
	}
	p := pc.Peer()
	sid := p.LogonSID
	if len(sid) > 8 {
		sid = sid[len(sid)-8:]
	}
	logf("pipe %s peer pid=%d image=%s sid=...%s", role, p.PID, p.Image, sid)
}

// ListenFunc opens one pipe; pipesec.Listen in production.
type ListenFunc func(pipesec.ListenConfig) (net.Listener, error)

// Pipes are the two open listeners and their names.
type Pipes struct {
	UI, AG         net.Listener
	UIName, AGName string
}

// Open creates the ui and ag pipes with fresh random names. It fails (and closes what it opened) when either cannot be created, so
// there is never a half-served state.
func Open(listen ListenFunc, policy pipesec.PeerPolicy) (*Pipes, error) {
	p := &Pipes{}
	uiName, agName, err := pipesec.NewNamePair()
	if err != nil {
		return nil, err
	}
	if p.UIName, p.UI, err = open(listen, uiName, pipesec.KindUI, policy); err != nil {
		return nil, err
	}
	if p.AGName, p.AG, err = open(listen, agName, pipesec.KindAG, policy); err != nil {
		_ = p.UI.Close()
		return nil, err
	}
	return p, nil
}

func open(listen ListenFunc, name string, kind pipesec.Kind, policy pipesec.PeerPolicy) (string, net.Listener, error) {
	l, err := listen(pipesec.ListenConfig{Name: name, Policy: policy})
	if err != nil {
		return "", nil, fmt.Errorf("open %s pipe: %w", kind, err)
	}
	return name, l, nil
}

// Close closes both listeners.
func (p *Pipes) Close() error {
	return errors.Join(p.UI.Close(), p.AG.Close())
}

// Group is the two http servers over the pipes, started and stopped together.
type Group struct {
	ui, ag *http.Server
	done   chan error
	wg     sync.WaitGroup
}

// Serve starts one http.Server per pipe. newServer builds a server (limits) around a handler; both share handler h, and each
// connection's context carries its role. Done delivers the result of the first server to stop, then the second's.
func Serve(p *Pipes, h http.Handler, newServer func(http.Handler) *http.Server) *Group {
	g := &Group{done: make(chan error, 2)}
	start := func(l net.Listener, role Role) *http.Server {
		s := newServer(h)
		s.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
			logPeer(role, c)
			return WithRole(ctx, role)
		}
		g.wg.Add(1)
		go func() { defer g.wg.Done(); g.done <- s.Serve(l) }()
		return s
	}
	g.ui = start(p.UI, RoleUI)
	g.ag = start(p.AG, RoleAG)
	return g
}

// Done delivers one result per server (buffered, never blocks the servers).
func (g *Group) Done() <-chan error { return g.done }

// Shutdown drains both servers at the same time, so neither keeps accepting while the other drains.
func (g *Group) Shutdown(ctx context.Context) error {
	var uiErr, agErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); uiErr = g.ui.Shutdown(ctx) }()
	go func() { defer wg.Done(); agErr = g.ag.Shutdown(ctx) }()
	wg.Wait()
	return errors.Join(uiErr, agErr)
}

// Close stops both servers at once.
func (g *Group) Close() error {
	return errors.Join(g.ui.Close(), g.ag.Close())
}
