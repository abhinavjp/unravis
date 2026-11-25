package mcpsrv

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
)

// CodeCallRefusal is a call of a code run that the host's budgets (or the ledger) do not let through. The code-mode broker records it
// as a denied call and gives the script an error answer with Code and Message; the run goes on.
type CodeCallRefusal struct {
	Code    string
	Message string
}

func (r *CodeCallRefusal) Error() string { return r.Message }

var codeCorr atomic.Uint64

// AdmitCodeCall is CallAs for one call of a code run (BL-363): a script that a host started must not get more from the repositories
// than the host's own tool calls get. The grant is checked on every call, as CallAs does (the profile is active at the revision of the
// run, the tool is in the grant's list and tier, the identifiers window is open when the run asked for identifiers). The call is charged
// to the same limiter as a direct call of the same profile (calls running at once, comparisons at once and per hour, live reads per
// hour, bytes per 10 minutes) and written to the same call ledger (an admit row and an end row, or a denied row).
//
// On success the host call must run under callCtx (it ends when the grant is revoked, reissued or stopped, or on CancelAll), and end
// must be called when the call has finished, with the envelope it returned; it charges the bytes and releases the slots. end is safe to
// call twice (the second call does nothing). A non-nil answer of end means the grant changed while the call ran: the script must not get
// the envelope. A refusal is a *CodeCallRefusal and nothing ran.
func (s *Dispatcher) AdmitCodeCall(ctx context.Context, pr Principal, tool string, args []byte, withIdentifiers bool) (callCtx context.Context, end func(result []byte, isError bool) *CodeCallRefusal, refused *CodeCallRefusal) {
	class := toolClass[tool]
	ev := mcpaudit.Event{ProfileID: pr.ProfileID, ProfileRev: pr.Rev, Tool: tool, OpClass: class, CorrID: "code-" + strconv.FormatUint(codeCorr.Add(1), 16)}
	deny := func(c context.Context, code string) {
		ev.Kind, ev.Outcome, ev.Code = mcpaudit.KindDenied, mcpaudit.OutcomeDenied, code
		_ = s.d.Recorder.Record(context.WithoutCancel(c), ev) // best effort: nothing ran
	}
	if class == "" {
		ev.Tool = "unknown" // the client's own text is not stored
		deny(ctx, mcp.WirePermissionRequired)
		return nil, nil, &CodeCallRefusal{mcp.WirePermissionRequired, "This host may not use that tool."}
	}
	if ok, _ := s.admit(pr.ProfileID, "", func() {}); !ok {
		return nil, nil, &CodeCallRefusal{mcp.WireBudgetExceeded, "Too many calls are running for this host."}
	}
	// Everything taken below is given back by cleanup: on every early return and every panic until end has the call, and by end after.
	var done func()
	var budgetDone func(int, time.Time)
	cleanup := func() {
		if done != nil {
			done()
		}
		s.release(pr.ProfileID, "")
	}
	handedOff := false
	defer func() {
		if !handedOff {
			defer cleanup()
			if budgetDone != nil {
				budgetDone(0, s.d.Now())
			}
		}
	}()

	runCtx, done, err := s.d.Grants.BeginCall(ctx, pr)
	if err != nil {
		return nil, nil, refusalOf(err)
	}
	p, err := s.d.Grants.Scope(runCtx, pr)
	if err != nil {
		return nil, nil, refusalOf(err)
	}
	// The tool must be in the grant's list and in the grant's tier: the tier is the ceiling even if a stored list names more.
	if !ToolAllowed(p.Tier, p.Tools, tool) {
		deny(runCtx, mcp.WirePermissionRequired)
		return nil, nil, &CodeCallRefusal{mcp.WirePermissionRequired, "This host may not use that tool."}
	}
	if withIdentifiers && !IdentifiersAllowed(p, s.d.Now()) {
		deny(runCtx, mcp.WirePermissionRequired)
		return nil, nil, &CodeCallRefusal{mcp.WirePermissionRequired, "This host's identifiers access has ended."}
	}
	ev.Fingerprint = s.d.Recorder.Fingerprint(compact(args))

	bd, over := s.lim.admit(pr.ProfileID, class, s.d.Now())
	if over != nil {
		deny(runCtx, mcp.WireBudgetExceeded)
		return nil, nil, &CodeCallRefusal{mcp.WireBudgetExceeded, "This host reached its limit for " + over.what + ". Wait and retry later; do not loop."}
	}
	budgetDone = bd
	start := s.d.Now()
	admit := ev
	admit.Kind, admit.Time = mcpaudit.KindAdmit, start
	if err := s.d.Recorder.Record(runCtx, admit); err != nil && isLive(class) {
		// Live work with no ledger row does not run (spec 03 section 3.5), for a script as for a direct call.
		s.d.Logf("mcp: the call ledger did not take a row; a live call of a code run was refused")
		return nil, nil, &CodeCallRefusal{mcp.WireInternal, "Unravis could not record this call, so it was not run."}
	}

	var once sync.Once
	end = func(result []byte, isError bool) (answer *CodeCallRefusal) {
		once.Do(func() {
			// Deferred, so a panic in the ledger still gives the slot back; budgetDone runs first, with the bytes charged.
			defer cleanup()
			charged := 0
			defer func() { budgetDone(charged, s.d.Now()) }()
			elapsed := s.d.Now().Sub(start)
			closing := ev
			closing.Kind, closing.Time, closing.DurMs = mcpaudit.KindEnd, start.Add(elapsed), elapsed.Milliseconds()
			// The revoke gate, as in CallAs: the script gets the answer only if the grant is still the one that admitted the call. The read does
			// not use the call's own deadline: a call that timed out is not an access change (review 6 of #267).
			if s.d.Grants.RecheckIdentifiers(context.WithoutCancel(ctx), pr, withIdentifiers) != nil || errors.Is(context.Cause(runCtx), ErrAccessEnded) {
				closing.Outcome, closing.Code = mcpaudit.OutcomeDenied, mcp.WirePermissionRequired
				s.recordEnd(ctx, closing)
				answer = &CodeCallRefusal{mcp.WirePermissionRequired, "This host's access changed while the call ran."}
				return
			}
			closing.Bytes = len(result)
			charged = len(result)
			closing.Outcome, closing.Code = outcomeOf(result, isError, ctx)
			s.recordEnd(ctx, closing)
		})
		return answer
	}
	handedOff = true
	return runCtx, end, nil
}

// refusalOf is fromAuthError for a code call: the denial of the grant, or an internal error that says nothing more.
func refusalOf(err error) *CodeCallRefusal {
	var d *Denied
	if errors.As(err, &d) {
		return &CodeCallRefusal{d.Code, d.Message}
	}
	return &CodeCallRefusal{mcp.WireInternal, "The request could not be processed."}
}
