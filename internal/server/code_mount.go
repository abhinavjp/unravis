package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/casestore"
)

// The code-mode routes (BL-574, spec 04). The server owns the part every route has to get right: the session token, the host and
// Origin check, the 405 rules, the refusal of the MCP bridge, and the case lease (which case the run is recorded in, held against an
// erase). What a run means lives in internal/codeapi, which imports internal/codemode; the server cannot import codemode because
// codemode's own tests import the server. codeapi hangs its handler on a CodeMount, so the table below is static and a build with no
// broker answers 503 code_mode_unavailable.
//
//	GET  /api/code/types              the .d.ts a script is written against and the limits
//	GET  /api/code/settings           the three switches
//	PUT  /api/code/settings           change them (omitted fields stay as they are)
//	POST /api/code/run                run a script on the open case
//	POST /api/code/runs/{id}/cancel   end a live run
const (
	codeTypesPath    = "/api/code/types"
	codeSettingsPath = "/api/code/settings"
	codeRunPath      = "/api/code/run"
)

var codeCancelRe = regexp.MustCompile(`^/api/code/runs/[A-Za-z0-9_-]{1,64}/cancel$`)

func codeCancelPath(path string) (string, bool) { return "", codeCancelRe.MatchString(path) }

// Errors of CodeMount.OpenCase. Neither carries a case title, path or id.
var (
	// ErrCodeNoCase: no case is open (or the named one is not open).
	ErrCodeNoCase = errors.New("no open case")
	// ErrCodeManyCases: more than one case is open and the caller did not say which.
	ErrCodeManyCases = errors.New("more than one open case")
)

// codeOpenBudget bounds the best-effort sweep of orphan runs when a case is opened.
const codeOpenBudget = 5 * time.Second

// maxCodeSeen bounds the set of cases already swept in this process.
const maxCodeSeen = 1024

type codeMountState struct {
	h http.Handler
	// onCaseOpen is called when the app opens a case (once per case per process); codeapi sets it to Broker.OnCaseOpen.
	onCaseOpen func(context.Context, casestore.CodeRunStore)
}

// CodeMount is where codeapi hangs the code-mode handler once the Broker is built (the Broker needs the dashboard handler, which is
// this server, so the server is built first).
type CodeMount struct {
	state atomic.Pointer[codeMountState]
	h     atomic.Pointer[handler]

	mu   sync.Mutex
	seen map[casestore.CaseID]struct{}
}

// NewCodeMount returns a mount with no handler: the routes answer 503 code_mode_unavailable.
func NewCodeMount() *CodeMount { return &CodeMount{seen: map[casestore.CaseID]struct{}{}} }

// Set turns the routes on with h, which serves every /api/code/ path of the table above (the server has already checked the token, the
// origin, the method and the caller). onCaseOpen may be nil.
func (m *CodeMount) Set(h http.Handler, onCaseOpen func(context.Context, casestore.CodeRunStore)) {
	m.state.Store(&codeMountState{h: h, onCaseOpen: onCaseOpen})
}

// WithCodeMount wires the code-mode routes to m.
func WithCodeMount(m *CodeMount) Option {
	return func(h *handler) {
		h.code = m
		if m != nil {
			m.h.Store(h)
		}
	}
}

func (h *handler) serveCode(w http.ResponseWriter, r *http.Request) {
	// The MCP bridge and the MCP agent hold the session token too, but a model must not run owner-level code, nor flip the setting that
	// allows it. The User-Agent is the client's own word, so this is the same convenience guard as the grant screen's, not a boundary.
	if ua := r.Header.Get("User-Agent"); ua == mcpUserAgent || ua == mcpAgentUserAgent {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "denied", "message": "Not for the MCP bridge."}, 1024)
		return
	}
	var st *codeMountState
	if h.code != nil {
		st = h.code.state.Load()
	}
	if st == nil || st.h == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "code_mode_unavailable", "message": "Code mode is not available in this run."}, 1024)
		return
	}
	st.h.ServeHTTP(w, r)
}

// mcpAgentUserAgent is what the MCP agent sends (internal/mcpagent).
const mcpAgentUserAgent = "unravis-mcp-agent"

// OpenCase finds the case a run is recorded in and holds it open: caseID empty means the one open case, otherwise the named one. The
// returned store is the case's own CodeRunStore. release closes the handle and lets go of the read share that keeps an erase waiting;
// the caller must call it exactly once, after the run has ended.
func (m *CodeMount) OpenCase(ctx context.Context, caseID string) (casestore.CodeRunStore, string, func(), error) {
	if m == nil {
		return nil, "", nil, ErrCodeNoCase
	}
	h := m.h.Load()
	if h == nil || h.cases == nil {
		return nil, "", nil, ErrCodeNoCase
	}
	var id casestore.CaseID
	if caseID == "" {
		infos, err := h.cases.List(ctx)
		if err != nil {
			return nil, "", nil, ErrCodeNoCase
		}
		var open []casestore.CaseID
		for _, info := range infos {
			if info.Untrusted || info.KeyLocked || info.Missing || info.Mark != "" || casestore.StateOf(info) != casestore.StateOpen {
				continue
			}
			open = append(open, info.ID)
		}
		switch len(open) {
		case 0:
			return nil, "", nil, ErrCodeNoCase
		case 1:
			id = open[0]
		default:
			return nil, "", nil, ErrCodeManyCases
		}
	} else {
		parsed, err := casestore.ParseID(caseID)
		if err != nil {
			return nil, "", nil, ErrCodeNoCase
		}
		id = parsed
	}
	if h.caseFiles().freeing.has(id) {
		return nil, "", nil, ErrCodeNoCase
	}
	releaseRead, ok := h.gates.acquireRead(id)
	if !ok {
		return nil, "", nil, ErrCodeNoCase // an erase is waiting for the readers
	}
	c, err := h.cases.Open(ctx, id)
	if err != nil {
		releaseRead()
		return nil, "", nil, ErrCodeNoCase
	}
	fail := func() { _ = c.Close(); releaseRead() }
	info := c.Info()
	if info.Mark != "" {
		if err := h.markAnswer(ctx, info); err != nil {
			fail()
			return nil, "", nil, ErrCodeNoCase
		}
		info = c.Info()
	}
	if casestore.StateOf(info) != casestore.StateOpen {
		fail()
		return nil, "", nil, ErrCodeNoCase
	}
	store, ok := c.(casestore.CodeRunStore)
	if !ok {
		fail()
		return nil, "", nil, ErrCodeNoCase
	}
	var once sync.Once
	return store, string(id), func() { once.Do(fail) }, nil
}

// caseOpened is called by the case routes after they opened a case for the app. It lets the broker close the runs a crash left open
// (spec 3.5 step 9), once per case per process, and never fails or delays the page by more than codeOpenBudget.
func (h *handler) caseOpened(ctx context.Context, id casestore.CaseID, c casestore.Case) {
	if h.code == nil {
		return
	}
	st := h.code.state.Load()
	if st == nil || st.onCaseOpen == nil {
		return
	}
	store, ok := innerCase(c).(casestore.CodeRunStore)
	if !ok {
		return
	}
	h.code.mu.Lock()
	_, done := h.code.seen[id]
	h.code.mu.Unlock()
	if done {
		return
	}
	sweepCtx, cancel := context.WithTimeout(ctx, codeOpenBudget)
	defer cancel()
	st.onCaseOpen(sweepCtx, store)
	// Marked done even when the sweep reported nothing: Run sweeps once per case itself, so a failure here is retried there.
	h.code.mu.Lock()
	if len(h.code.seen) >= maxCodeSeen {
		h.code.seen = map[casestore.CaseID]struct{}{}
	}
	h.code.seen[id] = struct{}{}
	h.code.mu.Unlock()
}
