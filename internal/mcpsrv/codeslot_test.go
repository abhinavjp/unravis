package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
)

// BL-574 L1: a code run is counted in the same limiter as the other calls of a host: runs at once per host and in all, runs per hour,
// and the bytes of its result in the ten-minute byte budget.

// gateCode is a CodeBackend whose runs wait until released (or until their context ends) and then return size bytes.
type gateCode struct {
	size    int
	started chan struct{}
	release chan struct{}
	runs    atomic.Int32
}

func newGateCode(size int) *gateCode {
	return &gateCode{size: size, started: make(chan struct{}, 16), release: make(chan struct{})}
}

func (g *gateCode) CallCode(ctx context.Context, _ string, _ Principal, _ Profile, _ []byte, _ time.Time) ([]byte, bool, bool) {
	g.runs.Add(1)
	select {
	case g.started <- struct{}{}:
	default:
	}
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	body, _ := json.Marshal(map[string]string{"pad": string(make([]byte, g.size))})
	return body, false, false
}

var slotCall atomic.Uint32

func (r *rig) codeDispatcherB(c CodeBackend, b Budgets) *Dispatcher {
	r.t.Helper()
	d, err := NewDispatcher(Deps{Grants: r.g, Backend: r.backend, Recorder: r.led, Now: r.clk.Now, Code: c, Budgets: b})
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

func (r *rig) execute(d *Dispatcher, id, tool string) (int, Answer) {
	body, _ := json.Marshal(map[string]any{"tool": tool, "args": map[string]any{"code": "1"}, "callId": fmt.Sprintf("%08x", slotCall.Add(1))})
	return d.CallAs(ctx, r.principal(id), body)
}

func (r *rig) enrollCode(id string) {
	a := r.approval(TierAggregate)
	a.Code = true
	r.enroll(id, a)
}

func overCodeBudget(st int, a Answer) bool {
	return st == 429 && !a.OK && a.Error != nil && a.Error.Code == mcp.WireBudgetExceeded
}

func TestCodeRunsAtOnceArePerHostAndInAll(t *testing.T) {
	r := newRig(t)
	g := newGateCode(10)
	d := r.codeDispatcherB(g, Budgets{})
	for _, id := range []string{"run-a", "run-b", "run-c"} {
		r.enrollCode(id)
	}
	var wg sync.WaitGroup
	start := func(id string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st, a := r.execute(d, id, mcp.ToolExecute); st != 200 || !a.OK || a.IsError {
				t.Errorf("run of %s: %d %+v %s", id, st, a, a.Result)
			}
		}()
		<-g.started
	}
	start("run-a")
	if st, a := r.execute(d, "run-a", mcp.ToolExecute); !overCodeBudget(st, a) {
		t.Errorf("a second run of one host at once: %d %+v", st, a)
	}
	start("run-b")
	// Two hosts are running: the limit for all hosts is reached, and the refusal does not say why.
	st, a := r.execute(d, "run-c", mcp.ToolExecute)
	if !overCodeBudget(st, a) {
		t.Errorf("a third host's run: %d %+v", st, a)
	}
	if a.Error != nil && (a.Error.Message == "" || a.Error.Action == "") {
		t.Errorf("the refusal has no message or action: %+v", a.Error)
	}
	if n := g.runs.Load(); n != 2 {
		t.Errorf("the backend ran %d times, want 2 (a refused run must not reach it)", n)
	}
	// A direct call of a running host is not a code run: it is still served.
	direct, _ := json.Marshal(map[string]any{"tool": "audit_get_guide", "args": map[string]any{}})
	if st, a := d.CallAs(ctx, r.principal("run-a"), direct); st != 200 || !a.OK {
		t.Errorf("a direct call while a run is running: %d %+v", st, a)
	}
	close(g.release)
	wg.Wait()
	// Slots are given back: the same hosts can run again.
	if st, a := r.execute(d, "run-a", mcp.ToolExecute); st != 200 || !a.OK {
		t.Errorf("run after the first ended: %d %+v", st, a)
	}
	if len(d.byCall) != 0 {
		t.Errorf("call ids leaked: %v", d.byCall)
	}
}

func TestCodeRunsPerHourAndRefusalIsLogged(t *testing.T) {
	r := newRig(t)
	g := newGateCode(10)
	close(g.release)
	d := r.codeDispatcherB(g, Budgets{})
	r.enrollCode("rate")
	for i := 0; i < DefaultBudgets.CodeRate; i++ {
		if st, a := r.execute(d, "rate", mcp.ToolExecute); st != 200 || !a.OK {
			t.Fatalf("run %d: %d %+v", i+1, st, a)
		}
		r.clk.Add(time.Minute)
	}
	before := g.runs.Load()
	if st, a := r.execute(d, "rate", mcp.ToolExecute); !overCodeBudget(st, a) {
		t.Fatalf("the run past the hourly limit: %d %+v", st, a)
	}
	if g.runs.Load() != before {
		t.Error("a refused run reached the backend")
	}
	rows := 0
	for _, e := range r.events() {
		if e.ProfileID == "rate" && e.Kind == mcpaudit.KindDenied && e.Code == mcp.WireBudgetExceeded && e.OpClass == ClassCode {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("%d denied rows for the refused run, want 1", rows)
	}
	r.clk.Add(time.Hour)
	if st, a := r.execute(d, "rate", mcp.ToolExecute); st != 200 || !a.OK {
		t.Errorf("after the hour: %d %+v", st, a)
	}
}

func TestCodeRunResultCountsInTheByteBudgetOfTheHost(t *testing.T) {
	r := newRig(t)
	g := newGateCode(2000)
	close(g.release)
	small := DefaultBudgets
	small.CumulativeBytes = 1000
	d := r.codeDispatcherB(g, small)
	r.enrollCode("bytes")
	if st, a := r.execute(d, "bytes", mcp.ToolExecute); st != 200 || !a.OK {
		t.Fatalf("first run: %d %+v", st, a)
	}
	// The run's result was bigger than the ten-minute budget, so neither door lets the next call through.
	if st, a := r.execute(d, "bytes", mcp.ToolExecute); !overCodeBudget(st, a) {
		t.Errorf("a second run after the byte budget: %d %+v", st, a)
	}
	body, _ := json.Marshal(map[string]any{"tool": "audit_get_guide", "args": map[string]any{}})
	if st, a := d.CallAs(ctx, r.principal("bytes"), body); !overCodeBudget(st, a) {
		t.Errorf("a direct call after a code run used the budget: %d %+v", st, a)
	}
	r.clk.Add(11 * time.Minute)
	if st, a := r.execute(d, "bytes", mcp.ToolExecute); st != 200 || !a.OK {
		t.Errorf("after the window: %d %+v", st, a)
	}
}

// A call that a gate refuses (no code mode on the grant, no broker) does not use up one of the host's runs.
func TestRefusedCodeCallsDoNotUseUpARun(t *testing.T) {
	r := newRig(t)
	g := newGateCode(10)
	close(g.release)
	d := r.codeDispatcherB(g, Budgets{})
	r.enroll("no-code", r.approval(TierAggregate))
	for i := 0; i < DefaultBudgets.CodeRate+5; i++ {
		if st, a := r.execute(d, "no-code", mcp.ToolExecute); st != 200 || !a.IsError {
			t.Fatalf("call %d: %d %+v", i, st, a)
		}
	}
	if pb := d.lim.per["no-code"]; pb != nil && (len(pb.codeRuns) != 0 || pb.code != 0) {
		t.Errorf("refused calls were counted: %d runs, %d running", len(pb.codeRuns), pb.code)
	}
}

// funcCode is a CodeBackend that answers with whatever its function says.
type funcCode func(ctx context.Context) ([]byte, bool, bool)

func (f funcCode) CallCode(ctx context.Context, _ string, _ Principal, _ Profile, _ []byte, _ time.Time) ([]byte, bool, bool) {
	return f(ctx)
}

// A run the broker refused before a script started (code mode off, bad arguments, no sandbox, no case) gives its place in the hourly
// count back; a run that started (its answer carries a run id) keeps it, also when it ended in an error.
func TestBrokerRefusalsDoNotUseUpTheHourlyRuns(t *testing.T) {
	r := newRig(t)
	refuse := funcCode(func(context.Context) ([]byte, bool, bool) {
		return CodeRefusalBody("sandbox_unavailable", "no sandbox"), true, false
	})
	d := r.codeDispatcherB(refuse, Budgets{})
	r.enrollCode("refused")
	for i := 0; i < DefaultBudgets.CodeRate+5; i++ {
		if st, a := r.execute(d, "refused", mcp.ToolExecute); st != 200 || !a.OK || !a.IsError {
			t.Fatalf("call %d: %d %+v", i, st, a)
		}
	}
	started := funcCode(func(context.Context) ([]byte, bool, bool) {
		return []byte(`{"runId":"r1","status":"ERROR","error":{"code":"script_error","message":"x"}}`), true, false
	})
	d = r.codeDispatcherB(started, Budgets{})
	r.enrollCode("started")
	for i := 0; i < DefaultBudgets.CodeRate; i++ {
		if st, a := r.execute(d, "started", mcp.ToolExecute); st != 200 || !a.OK {
			t.Fatalf("started run %d: %d %+v", i, st, a)
		}
	}
	if st, a := r.execute(d, "started", mcp.ToolExecute); !overCodeBudget(st, a) {
		t.Errorf("a started run that failed must keep its place in the hourly count: %d %+v", st, a)
	}
}

// A busy refusal writes no ledger row, and the slot is given back when the backend panics or the run is cancelled.
func TestCodeSlotIsGivenBackAndBusyWritesNoRow(t *testing.T) {
	r := newRig(t)
	g := newGateCode(10)
	d := r.codeDispatcherB(g, Budgets{})
	r.enrollCode("slot")
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.execute(d, "slot", mcp.ToolExecute)
	}()
	<-g.started
	before := len(r.events())
	if st, a := r.execute(d, "slot", mcp.ToolExecute); !overCodeBudget(st, a) {
		t.Fatalf("a second run at once: %d %+v", st, a)
	}
	if n := len(r.events()); n != before {
		t.Errorf("a busy refusal wrote %d ledger rows", n-before)
	}
	// The same call id twice at once is refused before it reaches the limiter.
	body, _ := json.Marshal(map[string]any{"tool": mcp.ToolExecute, "args": map[string]any{"code": "1"}, "callId": "0000dd01"})
	b2 := DefaultBudgets
	b2.CodeRate = 100
	d2 := r.codeDispatcherB(g, b2)
	r.enrollCode("dup")
	dupDone := make(chan struct{})
	go func() {
		defer close(dupDone)
		d2.CallAs(ctx, r.principal("dup"), body)
	}()
	<-g.started
	if st, _ := d2.CallAs(ctx, r.principal("dup"), body); st != 400 {
		t.Errorf("the same call id running twice: status %d, want 400", st)
	}
	close(g.release)
	<-done
	<-dupDone
	if len(d.byCall) != 0 || len(d2.byCall) != 0 {
		t.Errorf("call ids leaked: %v %v", d.byCall, d2.byCall)
	}

	boom := funcCode(func(context.Context) ([]byte, bool, bool) { panic("backend failed") })
	d3 := r.codeDispatcherB(boom, Budgets{})
	r.enrollCode("panic")
	func() {
		defer func() { _ = recover() }()
		r.execute(d3, "panic", mcp.ToolExecute)
	}()
	if pb := d3.lim.per["panic"]; pb == nil || pb.code != 0 || d3.lim.codeAl != 0 {
		t.Errorf("the slot was not given back after a panic: %+v, all=%d", pb, d3.lim.codeAl)
	}
}
