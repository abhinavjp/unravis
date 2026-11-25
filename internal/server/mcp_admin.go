package server

import (
	"net/http"
	"regexp"
	"sync/atomic"
)

// The AI access screen's routes (BL-347, spec 03 BL-347). The rules live in internal/mcpadmin over the grant store; this file only
// mounts them in the route table, so every one of them has the session token check, the host and Origin check and the 405 rules of the
// other routes. They are never reachable on the ag pipe (pipesrv passes only its own route list there) nor by the MCP bridge itself
// (its User-Agent is refused below): the person decides in the app, never the host.
//
//	GET  /api/mcp/profiles                      the hosts, their state, the global stop and the limits
//	POST /api/mcp/profiles/{id}/{action}        approve, deny, revoke, reissue, renew or remove one host
//	POST /api/mcp/stop, /api/mcp/resume         the global stop
const (
	mcpProfilesPath = "/api/mcp/profiles"
	mcpStopPath     = "/api/mcp/stop"
	mcpResumePath   = "/api/mcp/resume"
)

var mcpActionRe = regexp.MustCompile(`^/api/mcp/profiles/[a-z0-9-]{1,32}/[a-z]{1,16}$`)

// MCPMount is where the owner hangs the grant screen's handler once the grant store is open. The server is built before the store
// (the store's dispatcher calls the server), so the route table reads the handler from here at request time.
type MCPMount struct{ state atomic.Pointer[mcpMountState] }

type mcpMountState struct {
	h      http.Handler
	reason string // when h is nil: why the store is off ("sealing", "store")
}

// NewMCPMount returns a mount in legacy mode: the screen shows that grants do not apply to this run.
func NewMCPMount() *MCPMount { return &MCPMount{} }

// Set turns the screen on with h, which serves every /api/mcp/ path of the table above.
func (m *MCPMount) Set(h http.Handler) { m.state.Store(&mcpMountState{h: h}) }

// SetOff records that the grant store could not be opened; reason is a short code the screen explains, never a path or an error text.
func (m *MCPMount) SetOff(reason string) { m.state.Store(&mcpMountState{reason: reason}) }

// WithMCPMount wires the grant screen's routes to m.
func WithMCPMount(m *MCPMount) Option { return func(h *handler) { h.mcp = m } }

// MCPOffReasons are the codes SetOff takes; the screen has a sentence for each.
const (
	MCPOffSealing = "sealing" // this PC cannot protect the grant key for the current user
	MCPOffStore   = "store"   // the grant store could not be opened or read
)

func mcpActionPath(path string) (string, bool) { return "", mcpActionRe.MatchString(path) }

func (h *handler) serveMCP(w http.ResponseWriter, r *http.Request) {
	// The stdio bridge holds the session token too, but it is the host's side: it can never approve itself.
	if r.Header.Get("User-Agent") == mcpUserAgent {
		writeError(w, http.StatusForbidden, "Not for the MCP bridge.")
		return
	}
	var st *mcpMountState
	if h.mcp != nil {
		st = h.mcp.state.Load()
	}
	if st != nil && st.h != nil {
		st.h.ServeHTTP(w, r)
		return
	}
	// No grant store in this run. The screen reads the mode; nothing else exists.
	if r.Method == http.MethodGet && r.URL.Path == mcpProfilesPath {
		mode, reason := "legacy", ""
		if st != nil {
			mode, reason = "off", st.reason
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "reason": reason, "profiles": []any{}}, 4096)
		return
	}
	writeError(w, http.StatusNotFound, "AI access is not available in this run.")
}
