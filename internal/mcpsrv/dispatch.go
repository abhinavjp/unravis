package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
)

// BL-344: the dispatcher. It connects one call of a host to the grants (spec 03 section 3.5, order inside Dispatcher.Call):
//
//	authenticate (digest, constant time; state, global stop, expiry)
//	-> admit (in-flight bounds) -> BeginCall (registered before it is checked: a revoke or the stop cancels it from here on)
//	-> Scope (the grant as it is now: the tools the person allowed, the identifiers grant) -> ledger "admit" (fail closed for live work)
//	-> run (Backend) -> Recheck (the revoke gate) -> ledger "end" -> answer.
//
// A result is written to the answer only after Recheck passed on the line before. Whatever fails, the answer carries fixed text: never the
// arguments, the tool name the client sent, a path, an upstream or driver message.

// Limits of the wire (spec 03 section 3.3 and the bounds of the stdio server).
const (
	MaxRequestBytes = 1 << 20 // one request body
	MaxInFlight     = 4       // calls of one profile running at once
	MaxInFlightAll  = 16      // calls of all profiles together
)

// Operation classes of the ledger (spec 03 section 3.4). A live class reads the repositories; a cheap one does not.
const (
	ClassGuide        = "guide"
	ClassList         = "list"
	ClassLiveCompare  = "live_compare"
	ClassCached       = "cached"
	ClassLiveEvidence = "live_evidence"
	ClassLiveAgg      = "live_aggregate"
)

var toolClass = map[string]string{
	"audit_get_guide": ClassGuide, "audit_list_sources": ClassList, "audit_compare": ClassLiveCompare,
	"audit_get_groups": ClassCached, "audit_get_timeline": ClassCached, "audit_compare_timelines": ClassCached,
	"audit_get_requests": ClassLiveEvidence, "audit_get_slowest": ClassLiveEvidence, "audit_get_activity": ClassLiveEvidence,
	"audit_get_breakdown": ClassLiveAgg,
}

func isLive(class string) bool {
	switch class {
	case ClassLiveCompare, ClassLiveEvidence, ClassLiveAgg:
		return true
	}
	return false
}

// Recorder is the call ledger (mcpaudit.DB). Fingerprint is the keyed digest of the arguments: the ledger never gets the arguments.
type Recorder interface {
	Record(ctx context.Context, e mcpaudit.Event) error
	Fingerprint(args []byte) []byte
}

// Deps are the owner's parts; all three are needed.
type Deps struct {
	Grants   *Grants
	Backend  Backend
	Recorder Recorder
	Info     func() Info
	Budgets  Budgets // zero value: DefaultBudgets
	Now      func() time.Time
	Logf     func(format string, args ...any) // fixed text only
}

// Info is what the owner says about itself in hello.
type Info struct {
	PID        int
	Gen        string // the owner's start time, so a bridge can match it with the locator
	AppVersion string
}

// WireError is the refusal part of an answer. Code is one of the wire codes of internal/mcp (Wire*).
type WireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Action  string `json:"action,omitempty"`
}

// Answer is the body of POST /mcp/v1/call. OK says the tool ran and Result is its envelope (IsError: an error answer of the tool
// itself, such as an expired handle). Not OK says the dispatcher refused, or withheld the result because the grant ended; Error says why
// and no tool result is in the body.
type Answer struct {
	OK         bool            `json:"ok"`
	IsError    bool            `json:"isError,omitempty"`
	ProfileRev int             `json:"profileRev,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Ms         int64           `json:"ms,omitempty"`
	Error      *WireError      `json:"error,omitempty"`
}

type callReq struct {
	Tool       string          `json:"tool"`
	Args       json.RawMessage `json:"args"`
	CallID     string          `json:"callId"`
	DeadlineMs int64           `json:"deadlineMs"`
}

var (
	toolNameRE = regexp.MustCompile(`^[a-z][a-z_]{0,63}$`)
	callIDRE   = regexp.MustCompile(`^[0-9a-f-]{8,64}$`)
)

// Dispatcher runs the calls of the hosts. One value serves the backend; it is safe for concurrent use.
type Dispatcher struct {
	d Deps

	lim     *limiter
	mu      sync.Mutex
	perProf map[string]int
	total   int
	byCall  map[string]context.CancelFunc // profile id + "\x00" + call id
}

// NewDispatcher checks the parts and builds the dispatcher. It also tells the backend to forget a profile whose access ended
// (revoke, reissue, stop), so no handle outlives its grant.
func NewDispatcher(d Deps) (*Dispatcher, error) {
	if d.Grants == nil || d.Backend == nil || d.Recorder == nil {
		return nil, errors.New("mcpsrv: the dispatcher needs grants, a backend and a ledger")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logf == nil {
		d.Logf = func(string, ...any) {}
	}
	if d.Info == nil {
		d.Info = func() Info { return Info{} }
	}
	d.Grants.OnRevoke(d.Backend.Forget)
	return &Dispatcher{d: d, lim: newLimiter(d.Budgets), perProf: map[string]int{}, byCall: map[string]context.CancelFunc{}}, nil
}

// refuse is an answer that carries no result.
func refuse(status int, code, message, action string) (int, Answer) {
	return status, Answer{Error: &WireError{Code: code, Message: message, Action: action}}
}

// fromAuthError maps a failure of Authenticate, Scope or Recheck. A denial is the person's decision in the app (401, fixed text); any
// other error is the store failing, which fails closed with no detail.
func fromAuthError(err error) (int, Answer) {
	var d *Denied
	switch {
	case errors.As(err, &d):
		return refuse(http.StatusUnauthorized, d.Code, d.Message, d.Action)
	case errors.Is(err, ErrUnauthenticated):
		return refuse(http.StatusUnauthorized, mcp.WirePermissionRequired, "This host's credential was not accepted.", "Open Unravis > AI access and check this host.")
	}
	return refuse(http.StatusInternalServerError, mcp.WireInternal, "The request could not be processed.", "Retry once. If it fails again, tell the person to check Unravis.")
}

func (s *Dispatcher) admit(profile, callID string, cancel context.CancelFunc) (ok bool, dup bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if callID != "" {
		if _, found := s.byCall[profile+"\x00"+callID]; found {
			return false, true
		}
	}
	if s.perProf[profile] >= MaxInFlight || s.total >= MaxInFlightAll {
		return false, false
	}
	s.perProf[profile]++
	s.total++
	if callID != "" {
		s.byCall[profile+"\x00"+callID] = cancel
	}
	return true, false
}

func (s *Dispatcher) release(profile, callID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perProf[profile]--; s.perProf[profile] <= 0 {
		delete(s.perProf, profile)
	}
	s.total--
	if callID != "" {
		delete(s.byCall, profile+"\x00"+callID)
	}
}

// Call runs one call. header is the Authorization header; body is the request body (already bounded by the caller to MaxRequestBytes).
// The returned status is the HTTP status of the answer.
func (s *Dispatcher) Call(ctx context.Context, header string, body []byte) (int, Answer) {
	// Who is calling comes first: a caller with no accepted credential learns nothing about the request's shape or the tool names.
	pr, err := s.d.Grants.Authenticate(ctx, header)
	if err != nil {
		return fromAuthError(err)
	}
	return s.CallAs(ctx, pr, body)
}

// CallAs is Call for a caller Authenticate has already accepted (the route authenticates before it reads the body).
func (s *Dispatcher) CallAs(ctx context.Context, pr Principal, body []byte) (int, Answer) {
	var req callReq
	if status, a, ok := parseCall(body, &req); !ok {
		return status, a
	}
	callCtx, cancelCall := context.WithCancel(ctx)
	defer cancelCall()
	if ok, dup := s.admit(pr.ProfileID, req.CallID, cancelCall); !ok {
		if dup {
			return refuse(http.StatusBadRequest, mcp.WireInvalidArguments, "That call id is already running.", "")
		}
		return refuse(http.StatusTooManyRequests, mcp.WireBudgetExceeded, "Too many calls are running for this host.", "Wait for a running call to finish, then retry. Do not loop.")
	}
	defer s.release(pr.ProfileID, req.CallID)

	// From here on a revoke, a reissue, the global stop or CancelAll ends callCtx.
	runCtx, done, err := s.d.Grants.BeginCall(callCtx, pr)
	if err != nil {
		return fromAuthError(err)
	}
	defer done()
	p, err := s.d.Grants.Scope(runCtx, pr)
	if err != nil {
		return fromAuthError(err)
	}
	class := toolClass[req.Tool]
	ev := mcpaudit.Event{ProfileID: pr.ProfileID, ProfileRev: pr.Rev, Tool: req.Tool, OpClass: class, CorrID: req.CallID}
	// The tool must be in the grant's list and in the grant's tier: the tier is the ceiling even if a stored list names more.
	if class == "" || !slices.Contains(p.Tools, req.Tool) || !slices.Contains(ToolsForTier(p.Tier), req.Tool) {
		// A tool outside the grant (or outside the catalog) never reaches the backend. The ledger row is best effort: nothing ran.
		ev.Kind, ev.Outcome, ev.Code = mcpaudit.KindDenied, mcpaudit.OutcomeDenied, mcp.WirePermissionRequired
		if class == "" {
			ev.Tool = "unknown" // the client's own text is not stored
		}
		_ = s.d.Recorder.Record(context.WithoutCancel(runCtx), ev)
		return refuse(http.StatusForbidden, mcp.WirePermissionRequired, "This host may not use that tool.", "Open Unravis > AI access to change what this host may do.")
	}
	ev.Fingerprint = s.d.Recorder.Fingerprint(compact(req.Args))

	// Budgets (BL-350): a host that may call is still limited in how much it asks of the repositories. Charged before the ledger row and
	// before the run; a refusal is a denied row and says "do not loop".
	budgetDone, over := s.lim.admit(pr.ProfileID, class, s.d.Now())
	if over != nil {
		ev.Kind, ev.Outcome, ev.Code = mcpaudit.KindDenied, mcpaudit.OutcomeDenied, mcp.WireBudgetExceeded
		_ = s.d.Recorder.Record(context.WithoutCancel(runCtx), ev)
		return refuse(http.StatusTooManyRequests, mcp.WireBudgetExceeded, "This host reached its limit for "+over.what+".", "Wait and retry later, or ask the person to review the limits in Unravis > AI access. Do not loop.")
	}
	budgetBytes := 0
	defer func() { budgetDone(budgetBytes, s.d.Now()) }()

	start := s.d.Now()
	admit := ev
	admit.Kind, admit.Time = mcpaudit.KindAdmit, start
	if err := s.d.Recorder.Record(runCtx, admit); err != nil && isLive(class) {
		// Live work with no ledger row does not run (spec 03 section 3.5). Work that reads nothing live (the guide, the list, a cached page)
		// goes on without a row.
		s.d.Logf("mcp: the call ledger did not take a row; a live call was refused")
		return refuse(http.StatusInternalServerError, mcp.WireInternal, "Unravis could not record this call, so it was not run.", "Tell the person to check Unravis.")
	}

	timeout := audit.OperationTimeout
	if req.DeadlineMs > 0 && req.DeadlineMs < timeout.Milliseconds() {
		timeout = time.Duration(req.DeadlineMs) * time.Millisecond
	}
	tctx, cancelTimeout := context.WithTimeout(runCtx, timeout)
	defer cancelTimeout()
	ranWithIdentifiers := IdentifiersAllowed(p, start) // what the backend is told: the identifiers switch of the bridge is this value
	result, isError := s.d.Backend.Call(tctx, p, req.Tool, req.Args, start)
	elapsed := s.d.Now().Sub(start)

	end := ev
	end.Kind, end.Time, end.DurMs = mcpaudit.KindEnd, start.Add(elapsed), elapsed.Milliseconds()

	// The revoke gate: nothing is written to the answer unless the profile is still the one that was admitted. This holds also for a
	// backend that ignored its context and finished after the revoke.
	if err := s.d.Grants.RecheckIdentifiers(ctx, pr, ranWithIdentifiers); err != nil {
		end.Outcome, end.Code = mcpaudit.OutcomeDenied, mcp.WirePermissionRequired
		s.recordEnd(ctx, end)
		return fromAuthError(err)
	}
	if errors.Is(context.Cause(runCtx), ErrAccessEnded) {
		// The call was cancelled by CancelAll (the case lock) and the profile itself is unchanged.
		d := denied(ReasonRevision)
		end.Outcome, end.Code = mcpaudit.OutcomeDenied, mcp.WirePermissionRequired
		s.recordEnd(ctx, end)
		return refuse(http.StatusUnauthorized, d.Code, d.Message, d.Action)
	}
	end.Bytes = len(result)
	budgetBytes = len(result)
	end.Outcome, end.Code = outcomeOf(result, isError, tctx)
	s.recordEnd(ctx, end)
	return http.StatusOK, Answer{OK: true, IsError: isError, ProfileRev: pr.Rev, Result: json.RawMessage(result), Ms: end.DurMs}
}

// recordEnd writes the closing row. A failure is not the call's failure: the row is lost, the answer stands.
func (s *Dispatcher) recordEnd(ctx context.Context, e mcpaudit.Event) {
	if err := s.d.Recorder.Record(context.WithoutCancel(ctx), e); err != nil {
		s.d.Logf("mcp: the call ledger did not take a closing row")
	}
}

// outcomeOf names how a call ended for the ledger from the envelope's own error code.
func outcomeOf(result []byte, isError bool, ctx context.Context) (outcome, code string) {
	if !isError {
		return mcpaudit.OutcomeOK, ""
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(result, &e)
	code = e.Error.Code
	switch code {
	case mcp.WireCancelled:
		return mcpaudit.OutcomeCancelled, code
	case mcp.WireDeadlineExceeded:
		return mcpaudit.OutcomeTimeout, code
	case mcp.WirePermissionRequired:
		return mcpaudit.OutcomeDenied, code // the tool refused (an identifier filter, a source outside the grant): nothing was read
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return mcpaudit.OutcomeTimeout, code
	}
	return mcpaudit.OutcomeError, code
}

// parseCall reads and checks the request body. ok is false with the answer to send.
func parseCall(body []byte, req *callReq) (status int, a Answer, ok bool) {
	bad := func(text string) (int, Answer, bool) {
		st, ans := refuse(http.StatusBadRequest, mcp.WireInvalidArguments, text, "Correct the request.")
		return st, ans, false
	}
	if !mcp.StrictJSON(body) {
		return bad("The request is not valid JSON, or gives a name twice.")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(req) != nil {
		return bad("The request is not of the expected shape.")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return bad("The request is not of the expected shape.")
	}
	if !toolNameRE.MatchString(req.Tool) {
		return bad("The tool name is not valid.")
	}
	if req.CallID != "" && !callIDRE.MatchString(req.CallID) {
		return bad("The call id is not valid.")
	}
	if len(req.Args) > 0 {
		if t := bytes.TrimSpace(req.Args); len(t) == 0 || t[0] != '{' {
			return bad("The arguments must be an object.")
		}
	} else {
		req.Args = json.RawMessage(`{}`)
	}
	return 0, Answer{}, true
}

// compact is the argument text the ledger digest is made of.
func compact(args json.RawMessage) []byte {
	var b bytes.Buffer
	if json.Compact(&b, args) != nil {
		return nil
	}
	return b.Bytes()
}

// Cancel stops a running call of the authenticated profile by its call id. A call of another profile is never found: the key holds the
// profile id.
func (s *Dispatcher) Cancel(pr Principal, body []byte) (int, any) {
	var req struct {
		CallID string `json:"callId"`
	}
	if !mcp.StrictJSON(body) {
		return http.StatusBadRequest, map[string]string{"state": "invalid"}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || !callIDRE.MatchString(req.CallID) {
		return http.StatusBadRequest, map[string]string{"state": "invalid"}
	}
	s.mu.Lock()
	c := s.byCall[pr.ProfileID+"\x00"+req.CallID]
	s.mu.Unlock()
	if c == nil {
		return http.StatusOK, map[string]string{"state": "none"}
	}
	c()
	return http.StatusOK, map[string]string{"state": "cancelling"}
}
