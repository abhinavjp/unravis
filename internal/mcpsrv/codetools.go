package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
)

// BL-574 slice 2b: the two code-mode tools (unravis_types, unravis_execute) as the dispatcher serves them. They are not tools of the
// grant's list (a grant never names them, ToolAllowed would refuse them): the owner's code-mode switch (code-mode.json) and the grant's
// own state are the gates, and what a script may reach is the grant's tool list, tier and sources, taken from the stored profile and
// nothing else. The arguments of the call never carry a scope.
//
// Order inside callCode, and why it differs from CallAs:
//
//	BeginCall (a revoke ends the context from here on) -> Scope (the grant as it is now) -> slot -> ledger "admit"
//	-> Code.CallCode (the broker runs the script under the BeginCall context) -> RecheckIdentifiers (the revoke gate) -> ledger "end".
//
// The slot is not the per-profile call slot of CallAs: a run can last 280 seconds and its own tool calls are admitted through
// AdmitCodeCall, which uses that same counter, so a run holding one would leave its script three of the four calls it may have at once.
// unravis_execute takes a slot of its own (one run at a time for a profile); unravis_types is short and takes the ordinary one.

// ClassCode is the ledger class of the two tools. A run is live work (it reads the repositories through the tool calls it makes, which are
// charged and recorded one by one), so a run whose admit row cannot be written does not start.
const ClassCode = "code"

// CodeModeOffMessage is the plain text of the refusal when code mode is off (the broker's own text for code_mode_off is this).
const CodeModeOffMessage = "Code mode is switched off. Turn it on in Settings or run `unravis code enable`."

// CodeBackend runs the two tools for a grant. The code-mode broker's adapter (internal/codemode) is the only one.
type CodeBackend interface {
	// CallCode runs tool (mcp.ToolTypes or mcp.ToolExecute) for the profile as Scope just returned it. It returns the answer body (the
	// declarations, the run record, or a CodeRefusalBody) and whether it is an error answer, and says whether the call ran with
	// identifiers (the revoke gate reads it). It must end when ctx ends (a revoke ends it).
	CallCode(ctx context.Context, tool string, pr Principal, p Profile, args []byte, now time.Time) (body []byte, isError bool, ranWithIdentifiers bool)
}

// CodeRefusalBody is the body of a refused code-mode call: the stable {code, message} of the HTTP routes under an "error" key. The code is
// one of the codes of internal/codemode (code_mode_off, no_case, sandbox_unavailable, busy, denied, ...), not one of the wire codes of the
// audit tools, so a client can tell them apart. The message is fixed text.
func CodeRefusalBody(code, message string) []byte {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return body
}

// callCode serves one call of unravis_types or unravis_execute.
func (s *Dispatcher) callCode(ctx context.Context, pr Principal, req *callReq) (int, Answer) {
	callCtx, cancelCall := context.WithCancel(ctx)
	defer cancelCall()
	execute := req.Tool == mcp.ToolExecute
	if execute {
		if !s.takeCodeSlot(pr.ProfileID, req.CallID, cancelCall) {
			return refuse(http.StatusTooManyRequests, mcp.WireBudgetExceeded, "A code run of this host is already running.", "Wait for it to finish, then retry. Do not loop.")
		}
		defer s.releaseCodeSlot(pr.ProfileID, req.CallID)
	} else {
		if ok, dup := s.admit(pr.ProfileID, req.CallID, cancelCall); !ok {
			if dup {
				return refuse(http.StatusBadRequest, mcp.WireInvalidArguments, "That call id is already running.", "")
			}
			return refuse(http.StatusTooManyRequests, mcp.WireBudgetExceeded, "Too many calls are running for this host.", "Wait for a running call to finish, then retry. Do not loop.")
		}
		defer s.release(pr.ProfileID, req.CallID)
	}

	// From here on a revoke, a reissue, the global stop or CancelAll ends runCtx, and with it the run.
	runCtx, done, err := s.d.Grants.BeginCall(callCtx, pr)
	if err != nil {
		return fromAuthError(err)
	}
	defer done()
	p, err := s.d.Grants.Scope(runCtx, pr)
	if err != nil {
		return fromAuthError(err)
	}
	ev := mcpaudit.Event{ProfileID: pr.ProfileID, ProfileRev: pr.Rev, Tool: req.Tool, OpClass: ClassCode, CorrID: req.CallID}
	if s.d.Code == nil {
		// No code-mode broker in this build or session: the same answer as the switch being off. Nothing ran.
		ev.Kind, ev.Outcome, ev.Code = mcpaudit.KindDenied, mcpaudit.OutcomeDenied, "code_mode_off"
		_ = s.d.Recorder.Record(context.WithoutCancel(runCtx), ev)
		return http.StatusOK, Answer{OK: true, IsError: true, ProfileRev: pr.Rev, Result: CodeRefusalBody("code_mode_off", CodeModeOffMessage)}
	}
	ev.Fingerprint = s.d.Recorder.Fingerprint(compact(req.Args))

	start := s.d.Now()
	admit := ev
	admit.Kind, admit.Time = mcpaudit.KindAdmit, start
	if err := s.d.Recorder.Record(runCtx, admit); err != nil && execute {
		s.d.Logf("mcp: the call ledger did not take a row; a code run was refused")
		return refuse(http.StatusInternalServerError, mcp.WireInternal, "Unravis could not record this call, so it was not run.", "Tell the person to check Unravis.")
	}

	timeout := audit.OperationTimeout
	if req.DeadlineMs > 0 && req.DeadlineMs < timeout.Milliseconds() {
		timeout = time.Duration(req.DeadlineMs) * time.Millisecond
	}
	tctx, cancelTimeout := context.WithTimeout(runCtx, timeout)
	defer cancelTimeout()
	result, isError, ranWithIdentifiers := s.d.Code.CallCode(tctx, req.Tool, pr, p, req.Args, start)
	elapsed := s.d.Now().Sub(start)

	end := ev
	end.Kind, end.Time, end.DurMs = mcpaudit.KindEnd, start.Add(elapsed), elapsed.Milliseconds()

	// The revoke gate, as in CallAs: nothing is written to the answer unless the profile is still the one that was admitted (and, when the
	// run used identifiers, the identifiers grant is still in force). It holds also for a broker that ignored its context.
	if err := s.d.Grants.RecheckIdentifiers(ctx, pr, ranWithIdentifiers); err != nil {
		end.Outcome, end.Code = mcpaudit.OutcomeDenied, mcp.WirePermissionRequired
		s.recordEnd(ctx, end)
		return fromAuthError(err)
	}
	if errors.Is(context.Cause(runCtx), ErrAccessEnded) {
		d := denied(ReasonRevision)
		end.Outcome, end.Code = mcpaudit.OutcomeDenied, mcp.WirePermissionRequired
		s.recordEnd(ctx, end)
		return refuse(http.StatusUnauthorized, d.Code, d.Message, d.Action)
	}
	end.Bytes = len(result)
	end.Outcome, end.Code = outcomeOf(result, isError, tctx)
	s.recordEnd(ctx, end)
	return http.StatusOK, Answer{OK: true, IsError: isError, ProfileRev: pr.Rev, Result: json.RawMessage(result), Ms: end.DurMs}
}

// takeCodeSlot allows one running unravis_execute per profile, and registers its call id so the cancel route reaches it.
func (s *Dispatcher) takeCodeSlot(profile, callID string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.codeRuns == nil {
		s.codeRuns = map[string]bool{}
	}
	if s.codeRuns[profile] {
		return false
	}
	if callID != "" {
		if _, found := s.byCall[profile+"\x00"+callID]; found {
			return false
		}
		s.byCall[profile+"\x00"+callID] = cancel
	}
	s.codeRuns[profile] = true
	return true
}

func (s *Dispatcher) releaseCodeSlot(profile, callID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.codeRuns, profile)
	if callID != "" {
		delete(s.byCall, profile+"\x00"+callID)
	}
}
