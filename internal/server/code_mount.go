package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
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
	onCaseOpen func(context.Context, casestore.CodeRunStore) error
}

// CodeMount is where codeapi hangs the code-mode handler once the Broker is built (the Broker needs the dashboard handler, which is
// this server, so the server is built first).
type CodeMount struct {
	state atomic.Pointer[codeMountState]
	h     atomic.Pointer[handler]

	mu   sync.Mutex
	seen map[casestore.CaseID]struct{}

	// live counts the runs that hold a case through OpenCase (counted under the case gate's own lock), so a route that moves the case
	// (close, key change, archive, erase, the master-key lock) can end them and wait until they have let go. stopper ends the live runs
	// of one case ("" for every case); codeapi sets it to the Broker's CancelCase.
	live    map[casestore.CaseID]int
	changed chan struct{} // closed and replaced whenever a run lets go of a case
	stopper func(caseID string)
}

// codeStopGrace is how long a route that moves a case waits for its code runs to let go after they were told to stop. A run ends within a
// moment of its cancel (the worker is killed); the wait only bounds a worker that does not.
const codeStopGrace = 5 * time.Second

// SetCaseStopper tells the mount how to end the live runs of one case (caseID empty: every case). codeapi sets it to Broker.CancelCase.
func (m *CodeMount) SetCaseStopper(stop func(caseID string)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.stopper = stop
	m.mu.Unlock()
}

func (m *CodeMount) hold(id casestore.CaseID) {
	m.mu.Lock()
	if m.live == nil {
		m.live = map[casestore.CaseID]int{}
	}
	m.live[id]++
	m.mu.Unlock()
}

func (m *CodeMount) let(id casestore.CaseID) {
	m.mu.Lock()
	if m.live[id]--; m.live[id] <= 0 {
		delete(m.live, id)
	}
	if m.changed != nil {
		close(m.changed)
		m.changed = nil
	}
	m.mu.Unlock()
}

// stop ends the live runs of one case (id "": all) and waits, up to codeStopGrace, until none holds it. It reports whether none does.
func (m *CodeMount) stop(id casestore.CaseID) bool {
	m.mu.Lock()
	stopper := m.stopper
	m.mu.Unlock()
	if stopper == nil {
		return true
	}
	stopper(string(id))
	timer := time.NewTimer(codeStopGrace)
	defer timer.Stop()
	// A run that was accepted a moment before the first cancel is not yet known to the broker; the cancel is sent again until none holds the case.
	// idle-ok: lives only while a case close, key change, archive, erase or lock waits for its code runs to end (bounded by codeStopGrace).
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		m.mu.Lock()
		busy := len(m.live) > 0
		if id != "" {
			busy = m.live[id] > 0
		}
		if !busy {
			m.mu.Unlock()
			return true
		}
		if m.changed == nil {
			m.changed = make(chan struct{})
		}
		ch := m.changed
		m.mu.Unlock()
		select {
		case <-ch:
		case <-tick.C:
			stopper(string(id))
		case <-timer.C:
			return false
		}
	}
}

// stopCodeRunsFor ends the code runs recorded in case id before a route moves the case (close, key set or remove, archive, erase), and waits
// for them to let go of it. It returns errCaseInUse when a run is still live after the grace period, so a route that needs the case free can
// refuse (M4) instead of moving it under a run. A route that holds the case's exclusive slot keeps a new run out (OpenCase refuses while it is
// held); the erase route stops the runs only after it has set `erasing` (see acquireEraseWithin), and the case-key lock route holds no slot, so
// a run can start between its stop and the lock (the lock still closes the handle and drops the key, and the run's end record fails).
func (h *handler) stopCodeRunsFor(id casestore.CaseID) error {
	if h.code != nil && !h.code.stop(id) {
		return errCaseInUse
	}
	return nil
}

// stopCodeRunsAll ends every code run, before the master key locks.
func (h *handler) stopCodeRunsAll() {
	if h.code != nil {
		_ = h.code.stop("") // the lock goes on either way: a run that opens late gets a handle the closing step shuts
	}
}

// NewCodeMount returns a mount with no handler: the routes answer 503 code_mode_unavailable.
func NewCodeMount() *CodeMount { return &CodeMount{seen: map[casestore.CaseID]struct{}{}} }

// Set turns the routes on with h, which serves every /api/code/ path of the table above (the server has already checked the token, the
// origin, the method and the caller). onCaseOpen may be nil.
func (m *CodeMount) Set(h http.Handler, onCaseOpen func(context.Context, casestore.CodeRunStore) error) {
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
	if ua := r.Header.Get("User-Agent"); strings.HasPrefix(ua, mcpUserAgent) || strings.HasPrefix(ua, mcpAgentUserAgent) {
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
	// A run does not start while the case is being moved (an import, close, key change, archive or erase holds the slot), and it is
	// counted in the same step, so the route that moves the case can end it and wait for it (stopCodeRunsFor).
	releaseRead, ok := h.gates.acquireReadIdle(id, func() { m.hold(id) })
	if !ok {
		return nil, "", nil, ErrCodeNoCase // the case is being moved, or an erase is waiting for the readers
	}
	letGo := func() { releaseRead(); m.let(id) }
	c, err := h.cases.Open(ctx, id)
	if err != nil {
		letGo()
		return nil, "", nil, ErrCodeNoCase
	}
	fail := func() { _ = c.Close(); letGo() }
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
	// The page leaving does not cancel the sweep (it is not tied to the request), and a sweep that failed (an import holds the case, the
	// budget ran out) is not marked done, so the next open tries again.
	sweepCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), codeOpenBudget)
	defer cancel()
	if err := st.onCaseOpen(sweepCtx, store); err != nil {
		return
	}
	h.code.mu.Lock()
	if len(h.code.seen) >= maxCodeSeen {
		h.code.seen = map[casestore.CaseID]struct{}{}
	}
	h.code.seen[id] = struct{}{}
	h.code.mu.Unlock()
}
