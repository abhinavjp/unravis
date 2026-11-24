package mcpsrv

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"slices"
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

	aliases *Aliases
	sign    func(domain string, data []byte) []byte
	now     func() time.Time
	maxOne  int // MaxHandleBytes; a field so a test can lower it
	maxAll  int // MaxHandleBytesAll

	mu      sync.Mutex
	bridges map[string]*profileBridge
}

// Bounds of the result handles (spec 03 section 3.5, BL-349). A host's bridge keeps one comparison (the bridge refuses a second at the
// same time, stricter than the 4 of the spec), kept ten minutes. The numbers below bound the memory of the whole table: the result one
// host keeps, and all hosts together. The least recently used results go first and their handles answer "expired or replaced".
const (
	MaxHandleBytes    = 64 << 20  // the largest comparison result one host may keep
	MaxHandleBytesAll = 128 << 20 // the results of all hosts together
)

// BackendOption configures a LocalBackend.
type BackendOption func(*LocalBackend)

// WithAliases gives the host bridges the minter of evidence references: the rows of audit_get_requests and audit_get_slowest then carry
// "evidenceRef". Without it they carry no reference (and, for a host without identifiers, no id at all).
func WithAliases(a *Aliases) BackendOption { return func(l *LocalBackend) { l.aliases = a } }

// WithCursorKey sets the function that signs cursors (mcpaudit.DB.MAC, the server key). Without it the backend signs with a key it makes
// at start and keeps in memory only: cursors are signed either way, and none survives a restart, which is also the rule.
func WithCursorKey(sign func(domain string, data []byte) []byte) BackendOption {
	return func(l *LocalBackend) { l.sign = sign }
}

// WithBackendClock sets the clock of cursors and handles (tests).
func WithBackendClock(now func() time.Time) BackendOption {
	return func(l *LocalBackend) { l.now = now }
}

type profileBridge struct {
	rev         int
	identifiers bool
	sources     []string
	tier        string
	b           *mcp.Bridge
}

// NewLocalBackend builds the backend over the dashboard handler and the session token that process holds.
func NewLocalBackend(dashboard http.Handler, token string, opts ...BackendOption) *LocalBackend {
	l := &LocalBackend{dashboard: dashboard, token: token, bridges: map[string]*profileBridge{}}
	for _, o := range opts {
		if o != nil {
			o(l)
		}
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.maxOne == 0 {
		l.maxOne = MaxHandleBytes
	}
	if l.maxAll == 0 {
		l.maxAll = MaxHandleBytesAll
	}
	if l.sign == nil {
		key := make([]byte, 32)
		_, _ = rand.Read(key) // a failure leaves a zero key: the cursors are then signed with a key nobody chose, and still die at restart
		l.sign = func(domain string, data []byte) []byte {
			m := hmac.New(sha256.New, key)
			m.Write([]byte(domain))
			m.Write([]byte{0})
			m.Write(data)
			return m.Sum(nil)
		}
	}
	return l
}

// bridgeFor returns the bridge of the profile at its revision, identifiers state and source scope. A change of either makes a new bridge, which drops
// the old one's cached comparison: a handle made under an older grant never works under a newer one, and rows that carried identifiers
// are not paged after the identifiers grant ended. At most MaxProfiles bridges exist, because only an authenticated profile gets one.
func (l *LocalBackend) bridgeFor(p Profile, identifiers bool) *mcp.Bridge {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.bridges[p.ID]; e != nil && e.rev == p.Rev && e.identifiers == identifiers && e.tier == p.Tier && slices.Equal(e.sources, p.Sources) {
		return e.b
	}
	// The source scope of the grant is part of the bridge: it can reach no other source, whatever a tool is asked.
	// The tier decides what a count looks like: an aggregate host never sees a count of fewer than MinCohort events, and no host without
	// the identifiers grant sees the audit record id of a row.
	// The cursors it hands out are signed for this profile at this revision, this scope, tier and identifiers state, and for this bridge
	// only; the references it hands out are aliases of this profile at this revision.
	codec := &cursorCodec{sign: l.sign, now: l.now, profile: p.ID, rev: p.Rev, binding: newBinding(p.Sources, p.Tier, identifiers, newGen())}
	opts := []mcp.Option{mcp.WithIdentifiers(identifiers), mcp.WithSources(p.Sources), mcp.WithHiddenRecordIDs(!identifiers),
		mcp.WithCursors(codec), mcp.WithResultCap(l.maxOne), mcp.WithClock(l.now)}
	if l.aliases != nil {
		opts = append(opts, mcp.WithEvidenceRefs(l.aliases.Minter(p)))
	}
	if p.Tier != TierEvidence {
		opts = append(opts, mcp.WithMinCohort(MinCohort)) // any tier but evidence, an unknown one included: fail closed
	}
	b := mcp.NewLocal(l.dashboard, l.token, opts...)
	l.bridges[p.ID] = &profileBridge{rev: p.Rev, identifiers: identifiers, sources: slices.Clone(p.Sources), tier: p.Tier, b: b}
	return b
}

// Call implements Backend. The identifiers switch of the bridge is the profile's identifiers grant at this moment and nothing else.
func (l *LocalBackend) Call(ctx context.Context, p Profile, tool string, args json.RawMessage, now time.Time) ([]byte, bool) {
	body, isErr := l.bridgeFor(p, IdentifiersAllowed(p, now)).CallEnvelope(ctx, tool, args)
	l.Sweep()
	return body, isErr
}

// Sweep drops the comparison results whose ten minutes are over, then, while the hosts together hold more than MaxHandleBytesAll, the
// result least recently used. It runs after every call and may be run by a timer as well; it costs one pass over at most MaxProfiles
// bridges.
func (l *LocalBackend) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	type held struct {
		b    *mcp.Bridge
		used time.Time
		n    int
	}
	var all []held
	total := 0
	for _, e := range l.bridges {
		e.b.ExpireResult()
		if n := e.b.ResultBytes(); n > 0 {
			all = append(all, held{e.b, e.b.ResultUsedAt(), n})
			total += n
		}
	}
	slices.SortFunc(all, func(a, b held) int { return a.used.Compare(b.used) })
	for _, h := range all {
		if total <= l.maxAll {
			break
		}
		if h.b.DropResultIf(h.used) { // not a result that was stored since it was counted
			total -= h.n
		}
	}
}

// HeldBytes is the size of the comparison results all hosts hold now (a test and status read).
func (l *LocalBackend) HeldBytes() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.bridges {
		n += e.b.ResultBytes()
	}
	return n
}

// Forget implements Backend.
func (l *LocalBackend) Forget(profileID string) {
	l.mu.Lock()
	delete(l.bridges, profileID)
	l.mu.Unlock()
}
