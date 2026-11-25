package mcpsrv

import (
	"context"
	"strconv"
	"sync/atomic"

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
// than the host's own tool calls get. The call is charged to the same limiter as a direct call of the same profile (calls running at
// once, comparisons at once and per hour, live reads per hour, bytes per 10 minutes) and written to the same call ledger (an admit row
// and an end row, or a denied row). The tool must already be one the grant may call (GrantTools): the broker checks that before this.
//
// On success the returned end function must be called once, when the call has finished, with the envelope it returned; it charges the
// bytes and releases the slots. A refusal is a *CodeCallRefusal and nothing ran.
func (s *Dispatcher) AdmitCodeCall(ctx context.Context, pr Principal, tool string, args []byte) (end func(result []byte, isError bool), refused *CodeCallRefusal) {
	class := toolClass[tool]
	if class == "" {
		return nil, &CodeCallRefusal{mcp.WirePermissionRequired, "This host may not use that tool."}
	}
	if ok, _ := s.admit(pr.ProfileID, "", func() {}); !ok {
		return nil, &CodeCallRefusal{mcp.WireBudgetExceeded, "Too many calls are running for this host."}
	}
	release := func() { s.release(pr.ProfileID, "") }
	ev := mcpaudit.Event{ProfileID: pr.ProfileID, ProfileRev: pr.Rev, Tool: tool, OpClass: class,
		CorrID: "code-" + strconv.FormatUint(codeCorr.Add(1), 16), Fingerprint: s.d.Recorder.Fingerprint(compact(args))}

	budgetDone, over := s.lim.admit(pr.ProfileID, class, s.d.Now())
	if over != nil {
		ev.Kind, ev.Outcome, ev.Code = mcpaudit.KindDenied, mcpaudit.OutcomeDenied, mcp.WireBudgetExceeded
		_ = s.d.Recorder.Record(context.WithoutCancel(ctx), ev)
		release()
		return nil, &CodeCallRefusal{mcp.WireBudgetExceeded, "This host reached its limit for " + over.what + "."}
	}
	start := s.d.Now()
	admit := ev
	admit.Kind, admit.Time = mcpaudit.KindAdmit, start
	if err := s.d.Recorder.Record(ctx, admit); err != nil && isLive(class) {
		// Live work with no ledger row does not run (spec 03 section 3.5), for a script as for a direct call.
		s.d.Logf("mcp: the call ledger did not take a row; a live call of a code run was refused")
		budgetDone(0, s.d.Now())
		release()
		return nil, &CodeCallRefusal{mcp.WireInternal, "Unravis could not record this call, so it was not run."}
	}
	return func(result []byte, isError bool) {
		elapsed := s.d.Now().Sub(start)
		closing := ev
		closing.Kind, closing.Time, closing.DurMs, closing.Bytes = mcpaudit.KindEnd, start.Add(elapsed), elapsed.Milliseconds(), len(result)
		closing.Outcome, closing.Code = outcomeOf(result, isError, ctx)
		s.recordEnd(ctx, closing)
		budgetDone(len(result), s.d.Now())
		release()
	}, nil
}
