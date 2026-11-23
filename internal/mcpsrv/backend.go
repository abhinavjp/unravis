package mcpsrv

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"api-audit-explorer/internal/mcp"
)

// Backend runs one tool for an authenticated profile and returns the envelope the tool call answers with (internal/mcp: the same bytes
// as structured and as text content) and whether it is an error answer. The dispatcher has already checked who may call what; the
// backend only runs it. Forget drops everything kept for a profile (the result handle of its last comparison).
type Backend interface {
	Call(ctx context.Context, p Profile, tool string, args json.RawMessage, now time.Time) (body []byte, isError bool)
	Forget(profileID string)
}

// LocalBackend runs the tools of internal/mcp (the code the stdio server and the command line run) against the dashboard handler in
// this process. It uses no network and no dashboard token on the wire: the token is the dashboard's own, held by this process, and the
// bridge it builds can reach only the seven read routes of its allow-list (Bridge.request). Each profile has its own bridge, so the
// handle of one host's comparison is not valid for another host, and a bridge is dropped when its profile's grant changes.
type LocalBackend struct {
	dashboard http.Handler
	token     string

	mu      sync.Mutex
	bridges map[string]*profileBridge
}

type profileBridge struct {
	rev         int
	identifiers bool
	b           *mcp.Bridge
}

// NewLocalBackend builds the backend over the dashboard handler and the session token that process holds.
func NewLocalBackend(dashboard http.Handler, token string) *LocalBackend {
	return &LocalBackend{dashboard: dashboard, token: token, bridges: map[string]*profileBridge{}}
}

// bridgeFor returns the bridge of the profile at its revision and identifiers state. A change of either makes a new bridge, which drops
// the old one's cached comparison: a handle made under an older grant never works under a newer one, and rows that carried identifiers
// are not paged after the identifiers grant ended. At most MaxProfiles bridges exist, because only an authenticated profile gets one.
func (l *LocalBackend) bridgeFor(p Profile, identifiers bool) *mcp.Bridge {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.bridges[p.ID]; e != nil && e.rev == p.Rev && e.identifiers == identifiers {
		return e.b
	}
	b := mcp.NewLocal(l.dashboard, l.token, mcp.WithIdentifiers(identifiers))
	l.bridges[p.ID] = &profileBridge{rev: p.Rev, identifiers: identifiers, b: b}
	return b
}

// Call implements Backend. The identifiers switch of the bridge is the profile's identifiers grant at this moment and nothing else.
func (l *LocalBackend) Call(ctx context.Context, p Profile, tool string, args json.RawMessage, now time.Time) ([]byte, bool) {
	return l.bridgeFor(p, IdentifiersAllowed(p, now)).CallEnvelope(ctx, tool, args)
}

// Forget implements Backend.
func (l *LocalBackend) Forget(profileID string) {
	l.mu.Lock()
	delete(l.bridges, profileID)
	l.mu.Unlock()
}
