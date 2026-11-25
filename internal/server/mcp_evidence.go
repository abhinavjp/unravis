package server

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// BL-349: the resolver of an evidence reference (spec 03 D12). A host that may not see audit record ids is given an alias for each row;
// the person pastes it into the app and this route says which source and record it stands for, so the app can open the row. It is a
// first-party route: it needs the session token like every /api route, and it is not one of the seven paths the MCP bridge may reach,
// so no host can resolve an alias, its own or another's.

// EvidenceTarget is the record an alias stands for.
type EvidenceTarget struct {
	SourceID  string    `json:"sourceId"`
	RecordID  string    `json:"recordId"`
	Host      string    `json:"host"` // the id of the host profile the reference was made for
	ExpiresAt time.Time `json:"expiresAt"`
}

// EvidenceResolver finds the record of an alias. ok is false for an alias that does not exist, ended, expired, or belongs to a host that
// is no longer approved at the revision it was made under; the reasons are not told apart.
type EvidenceResolver func(ctx context.Context, alias string) (t EvidenceTarget, ok bool)

type evidenceHolder struct {
	fn atomic.Pointer[EvidenceResolver]
	// onLock runs after the master key was locked (the aliases of every host end with it).
	onLock atomic.Pointer[func()]
}

// SetMasterKeyLockHook sets what runs after the master-key lock has detached the key: the owner ends every MCP host's handles and
// aliases there. nil removes it.
func SetMasterKeyLockHook(served http.Handler, fn func()) {
	h, ok := served.(*handler)
	if !ok || h == nil {
		return
	}
	if fn == nil {
		h.evidence.onLock.Store(nil)
		return
	}
	h.evidence.onLock.Store(&fn)
}

func (h *handler) runLockHook() {
	if fn := h.evidence.onLock.Load(); fn != nil {
		(*fn)()
	}
}

// mcpEvidencePattern is the route of the resolver; it is gated like the case routes (423 while the master key is locked or damaged),
// because what it resolves is the person's own evidence of a case-sized investigation and the lock means "Unravis is closed".
const mcpEvidencePattern = "/api/mcp/evidence/{alias}"

const maxAliasText = 64

// SetEvidenceResolver wires the resolver into a handler made by New. The owner calls it once the MCP store is open; until then (and when
// MCP access is off) the route answers 404.
func SetEvidenceResolver(served http.Handler, r EvidenceResolver) {
	h, ok := served.(*handler)
	if !ok || h == nil {
		return
	}
	if r == nil {
		h.evidence.fn.Store(nil)
		return
	}
	h.evidence.fn.Store(&r)
}

// mcpEvidence answers GET /api/mcp/evidence/{alias}. The answer for anything but a live alias is one fixed 404; the alias text is never
// echoed.
func (h *handler) mcpEvidence(w http.ResponseWriter, r *http.Request, alias string) {
	fn := h.evidence.fn.Load()
	if fn == nil || !strings.HasPrefix(alias, "ev1_") || len(alias) > maxAliasText {
		writeError(w, http.StatusNotFound, "No such evidence reference.")
		return
	}
	t, ok := (*fn)(r.Context(), alias)
	if !ok {
		writeError(w, http.StatusNotFound, "No such evidence reference.")
		return
	}
	writeJSON(w, http.StatusOK, t, 4096)
}
