package mcpsrv

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
)

// BL-350, dedupe of identical in-flight comparisons (docs/desktop/spikes/BL-350-dedupe.md). One flight is one running audit_compare; an
// identical call of the same profile and revision waits on it instead of running again.

// flightTries is how often a follower looks for a leader again after the one it waited on gave no answer to share.
const flightTries = 3

type flight struct {
	done chan struct{}
	once sync.Once

	// Set by the leader before done is closed; read by followers after it.
	ok     bool
	result []byte
	isErr  bool
	idents bool // whether the leader ran with the identifiers grant
}

type flights struct {
	mu sync.Mutex
	m  map[string]*flight
}

func newFlights() *flights { return &flights{m: map[string]*flight{}} }

// flightKey is the key of one comparison: the profile, its revision and the keyed digest of its arguments.
func flightKey(pr Principal, fingerprint []byte) string {
	return pr.ProfileID + "\x00" + strconv.Itoa(pr.Rev) + "\x00" + hex.EncodeToString(fingerprint)
}

// join returns the flight of key. leader is true when the caller made it and must run the work and call finish.
func (f *flights) join(key string) (fl *flight, leader bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur := f.m[key]; cur != nil {
		return cur, false
	}
	fl = &flight{done: make(chan struct{})}
	f.m[key] = fl
	return fl, true
}

// share is called by the leader with an answer that followers may use.
func (fl *flight) share(result []byte, isErr, idents bool) {
	fl.result, fl.isErr, fl.idents, fl.ok = result, isErr, idents, true
}

// finish ends the flight: it leaves the table and wakes the followers. It is safe to call twice.
func (f *flights) finish(key string, fl *flight) {
	fl.once.Do(func() {
		f.mu.Lock()
		if f.m[key] == fl {
			delete(f.m, key)
		}
		f.mu.Unlock()
		close(fl.done)
	})
}

// callTimeout is how long a call may run: the operation deadline, or the shorter one the host asked for.
func callTimeout(req *callReq) time.Duration {
	timeout := audit.OperationTimeout
	if req.DeadlineMs > 0 && req.DeadlineMs < timeout.Milliseconds() {
		timeout = time.Duration(req.DeadlineMs) * time.Millisecond
	}
	return timeout
}

// shareCompare looks for a running comparison identical to this one. If there is none, the call leads one (lead is its flight; the caller
// runs the work and ends the flight). If there is, the call waits on it, takes the answer and is handled. A leader that gave nothing to
// share sends the follower round again, flightTries times at most; after that the call goes the normal way (lead nil) and meets the
// limiter. admitted and start say the call's admit row is already written, so the caller does not write a second one.
func (s *Dispatcher) shareCompare(ctx, runCtx context.Context, pr Principal, req *callReq, ev mcpaudit.Event, key string) (st int, a Answer, handled bool, lead *flight, admitted bool, start time.Time) {
	tctx, cancel := context.WithTimeout(runCtx, callTimeout(req))
	defer cancel()
	for try := 0; ; try++ {
		f, leader := s.flights.join(key)
		if leader {
			return 0, Answer{}, false, f, admitted, start
		}
		if try >= flightTries {
			return 0, Answer{}, false, nil, admitted, start
		}
		if !admitted {
			start = s.d.Now()
			row := ev
			row.Kind, row.Time = mcpaudit.KindAdmit, start
			if err := s.d.Recorder.Record(runCtx, row); err != nil { // a comparison is live work: no row, no run
				s.d.Logf("mcp: the call ledger did not take a row; a live call was refused")
				st, a = refuse(http.StatusInternalServerError, mcp.WireInternal, "Unravis could not record this call, so it was not run.", "Tell the person to check Unravis.")
				return st, a, true, nil, false, start
			}
			admitted = true
		}
		select {
		case <-f.done:
		case <-tctx.Done():
			st, a = s.endWaiting(ctx, runCtx, tctx, pr, ev, start)
			return st, a, true, nil, admitted, start
		}
		if f.ok {
			st, a = s.serveShared(ctx, runCtx, pr, ev, start, f)
			return st, a, true, nil, admitted, start
		}
	}
}

// serveShared answers a follower with the leader's result, through the same revoke gate as any answer.
func (s *Dispatcher) serveShared(ctx, runCtx context.Context, pr Principal, ev mcpaudit.Event, start time.Time, f *flight) (int, Answer) {
	end := ev
	end.Kind, end.Time = mcpaudit.KindEnd, s.d.Now()
	end.DurMs = end.Time.Sub(start).Milliseconds()
	if err := s.d.Grants.RecheckIdentifiers(ctx, pr, f.idents); err != nil {
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
	end.Bytes, end.Outcome = len(f.result), mcpaudit.OutcomeOK
	s.recordEnd(ctx, end)
	return http.StatusOK, Answer{OK: true, IsError: f.isErr, ProfileRev: pr.Rev, Result: json.RawMessage(f.result), Ms: end.DurMs}
}

// endWaiting ends a follower that was cancelled or ran out of time while it waited. The leader goes on.
func (s *Dispatcher) endWaiting(ctx, runCtx, tctx context.Context, pr Principal, ev mcpaudit.Event, start time.Time) (int, Answer) {
	end := ev
	end.Kind, end.Time = mcpaudit.KindEnd, s.d.Now()
	end.DurMs = end.Time.Sub(start).Milliseconds()
	if errors.Is(context.Cause(runCtx), ErrAccessEnded) {
		d := denied(ReasonRevision)
		end.Outcome, end.Code = mcpaudit.OutcomeDenied, mcp.WirePermissionRequired
		s.recordEnd(ctx, end)
		return refuse(http.StatusUnauthorized, d.Code, d.Message, d.Action)
	}
	code, outcome, msg, action := mcp.WireCancelled, mcpaudit.OutcomeCancelled, "The call was cancelled.", "The call was cancelled. Repeat it only if the answer is still needed."
	if errors.Is(tctx.Err(), context.DeadlineExceeded) && runCtx.Err() == nil {
		code, outcome, msg, action = mcp.WireDeadlineExceeded, mcpaudit.OutcomeTimeout, "The comparison did not finish in time.", "Do not repeat this call as it is: narrow the sources or the window."
	}
	end.Outcome, end.Code = outcome, code
	s.recordEnd(ctx, end)
	return http.StatusOK, Answer{OK: true, IsError: true, ProfileRev: pr.Rev, Result: json.RawMessage(mcp.ErrorEnvelope("audit_compare", code, msg, action)), Ms: end.DurMs}
}
