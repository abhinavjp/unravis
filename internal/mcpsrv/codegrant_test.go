package mcpsrv

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
)

// BL-574 M1: code mode is its own permission of a grant. A grant approved for tools only cannot run unravis_execute or unravis_types.

// countingCode is a CodeBackend that counts what reached it.
type countingCode struct{ calls atomic.Int32 }

func (c *countingCode) CallCode(_ context.Context, tool string, _ Principal, p Profile, _ []byte, _ time.Time) ([]byte, bool, bool) {
	c.calls.Add(1)
	body, _ := json.Marshal(map[string]any{"tool": tool, "code": p.Code})
	return body, false, false
}

// codeDispatcher is a dispatcher with a code backend over the rig's grants and ledger.
func (r *rig) codeDispatcher(c CodeBackend) *Dispatcher {
	r.t.Helper()
	d, err := NewDispatcher(Deps{Grants: r.g, Backend: r.backend, Recorder: r.led, Now: r.clk.Now, Code: c})
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

func (r *rig) callCodeTool(d *Dispatcher, id, tool string, n int) Answer {
	r.t.Helper()
	body, _ := json.Marshal(map[string]any{"tool": tool, "args": map[string]any{"code": "1"}, "callId": []string{"00000001", "00000002", "00000003", "00000004"}[n]})
	st, ans := d.CallAs(ctx, r.principal(id), body)
	if st != 200 {
		r.t.Fatalf("%s: status %d %+v", tool, st, ans)
	}
	return ans
}

func TestGrantWithoutCodeModeCannotRunEitherCodeTool(t *testing.T) {
	r := newRig(t)
	fake := &countingCode{}
	d := r.codeDispatcher(fake)
	r.enroll("no-code", r.approval(TierAggregate)) // the defaults: code mode off
	withCode := r.approval(TierAggregate)
	withCode.Code = true
	r.enroll("with-code", withCode)

	for i, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
		ans := r.callCodeTool(d, "no-code", tool, i)
		var env struct {
			Error struct{ Code, Message string }
		}
		if !ans.OK || !ans.IsError || json.Unmarshal(ans.Result, &env) != nil || env.Error.Code != "denied" || env.Error.Message != CodeNotGrantedMessage {
			t.Fatalf("%s without code mode: %+v %s", tool, ans, ans.Result)
		}
		if string(ans.Result) != string(CodeRefusalBody("denied", "This access was not given code mode. Approve it with code mode on in Unravis.")) {
			t.Errorf("%s: the refusal body is not the stable one: %s", tool, ans.Result)
		}
	}
	if n := fake.calls.Load(); n != 0 {
		t.Fatalf("the code backend ran %d times for a grant without code mode", n)
	}
	rows := 0
	for _, e := range r.events() {
		if e.ProfileID == "no-code" && e.Kind == mcpaudit.KindDenied && e.Outcome == mcpaudit.OutcomeDenied && e.Code == "code_not_granted" && e.OpClass == ClassCode {
			rows++
		}
	}
	if rows != 2 {
		t.Errorf("want 2 denied ledger rows with code_not_granted, got %d", rows)
	}

	for i, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
		ans := r.callCodeTool(d, "with-code", tool, i)
		if !ans.OK || ans.IsError {
			t.Fatalf("%s with code mode: %+v %s", tool, ans, ans.Result)
		}
	}
	if n := fake.calls.Load(); n != 2 {
		t.Fatalf("the code backend ran %d times, want 2", n)
	}
}

// A build with no code backend still says "code mode is off" first, whatever the grant holds.
func TestNoCodeBackendStillAnswersCodeModeOff(t *testing.T) {
	r := newRig(t)
	d := r.codeDispatcher(nil)
	r.enroll("h", r.approval(TierAggregate))
	ans := r.callCodeTool(d, "h", mcp.ToolExecute, 0)
	if string(ans.Result) != string(CodeRefusalBody("code_mode_off", CodeModeOffMessage)) {
		t.Fatalf("%s", ans.Result)
	}
}
