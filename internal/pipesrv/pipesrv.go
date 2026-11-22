// Package pipesrv serves the existing dashboard handler over the two secured named pipes of the desktop sidecar (BL-301).
//
// The listeners come from internal/pipesec (the only pipe listener of Unravis; no other pipe library is used). The role of a pipe
// ("ui" for the shell, "ag" for the CLI and MCP) is put on every connection's context and decides what the handler lets through.
// Everything here is portable: it works on any net.Listener, so the rules are tested on every OS; only pipesec.Listen is Windows-only.
package pipesrv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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

// agPrefixes are the only paths the ag pipe passes on (spec 03 /mcp/v1/*, spec 05 /cli/v1/*). The GUI routes are never reachable there.
var agPrefixes = []string{"/mcp/v1/", "/cli/v1/"}

const tokenHeader = "X-Audit-Token" //nolint:gosec // a header name, not a credential

// Handler wraps the dashboard handler served for the pipes.
//
// A request with no pipe role is refused (fail closed). On the ui pipe the peer was verified by pipesec (same logon, same session,
// Unravis image), so that check stands in for the session token: the owner's token is set on the request and any token the client sent
// is dropped, which leaves every existing handler unchanged. The ag pipe never gets the token and reaches only agPrefixes.
func Handler(served http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := RoleFrom(r.Context())
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		r.Header.Del(tokenHeader)
		switch role {
		case RoleUI:
			r.Header.Set(tokenHeader, token)
		case RoleAG:
			if !hasAnyPrefix(r.URL.Path, agPrefixes) {
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
	var err error
	if p.UIName, p.UI, err = open(listen, pipesec.KindUI, policy); err != nil {
		return nil, err
	}
	if p.AGName, p.AG, err = open(listen, pipesec.KindAG, policy); err != nil {
		_ = p.UI.Close()
		return nil, err
	}
	return p, nil
}

func open(listen ListenFunc, kind pipesec.Kind, policy pipesec.PeerPolicy) (string, net.Listener, error) {
	name, err := pipesec.NewName(kind)
	if err != nil {
		return "", nil, err
	}
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
		s.ConnContext = func(ctx context.Context, _ net.Conn) context.Context { return WithRole(ctx, role) }
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

// Shutdown drains both servers.
func (g *Group) Shutdown(ctx context.Context) error {
	return errors.Join(g.ui.Shutdown(ctx), g.ag.Shutdown(ctx))
}

// Close stops both servers at once.
func (g *Group) Close() error {
	return errors.Join(g.ui.Close(), g.ag.Close())
}
