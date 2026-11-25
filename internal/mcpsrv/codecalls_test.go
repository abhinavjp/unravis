package mcpsrv

import (
	"context"
	"errors"
	"testing"
	"time"

	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
)

// The grant is checked on every call of a code run, as CallAs does it (review of #267, M1), and the slots are given back whatever
// happens (M2).

func (r *rig) principal(id string) Principal {
	r.t.Helper()
	p, err := r.g.db.Get(ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return Principal{ProfileID: id, Rev: p.Rev}
}

func (r *rig) deniedRows(id, tool string) int {
	n := 0
	for _, e := range r.events() {
		if e.ProfileID == id && e.Tool == tool && e.Kind == mcpaudit.KindDenied && e.Outcome == mcpaudit.OutcomeDenied && e.Code == mcp.WirePermissionRequired {
			n++
		}
	}
	return n
}

func wantPermission(t *testing.T, what string, end func([]byte, bool) *CodeCallRefusal, refused *CodeCallRefusal) {
	t.Helper()
	if refused == nil || end != nil || refused.Code != mcp.WirePermissionRequired || refused.Message == "" {
		t.Errorf("%s: want a permission refusal, got %v", what, refused)
	}
}

func TestCodeCallRefusedAfterRevokeAndItsContextEnds(t *testing.T) {
	r := newRig(t)
	r.enroll("cc-rev", r.approval(TierAggregate))
	pr := r.principal("cc-rev")
	callCtx, end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), false)
	if refused != nil || callCtx == nil {
		t.Fatalf("first call: %v", refused)
	}
	if callCtx.Err() != nil {
		t.Fatal("the call's context ended before the revoke")
	}
	if _, err := r.g.Revoke(ctx, "cc-rev"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(context.Cause(callCtx), ErrAccessEnded) {
		t.Errorf("cause of the call's context after the revoke: %v", context.Cause(callCtx))
	}
	if ref := end([]byte(`{}`), false); ref == nil || ref.Code != mcp.WirePermissionRequired || ref.Message != "This host's access changed while the call ran." {
		t.Errorf("end after the revoke: %v", ref)
	}
	c, e2, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), false)
	if refused == nil || c != nil || e2 != nil {
		t.Errorf("a call after the revoke was admitted")
	}
	if r.disp.total != 0 {
		t.Errorf("in-flight total %d", r.disp.total)
	}
}

func TestCodeCallRefusedForUnknownProfileAndChangedRev(t *testing.T) {
	r := newRig(t)
	r.enroll("cc-rev2", r.approval(TierAggregate))
	_, end, refused := r.disp.AdmitCodeCall(ctx, Principal{ProfileID: "never-enrolled", Rev: 42}, "audit_get_guide", []byte(`{}`), false)
	wantPermission(t, "never enrolled", end, refused)
	pr := r.principal("cc-rev2")
	if _, err := r.g.Reissue(ctx, "cc-rev2"); err != nil {
		t.Fatal(err)
	}
	_, end, refused = r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), false)
	wantPermission(t, "old revision after a reissue", end, refused)
	if r.disp.total != 0 || len(r.disp.perProf) != 0 {
		t.Errorf("slots left: %d %v", r.disp.total, r.disp.perProf)
	}
}

func TestCodeCallToolOutsideTheGrantIsDeniedAndRecorded(t *testing.T) {
	r := newRig(t)
	r.enroll("cc-tools", r.approval(TierEvidence, "audit_get_guide"))
	pr := r.principal("cc-tools")
	_, end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_requests", []byte(`{}`), false)
	wantPermission(t, "a tool outside the list", end, refused)
	if n := r.deniedRows("cc-tools", "audit_get_requests"); n != 1 {
		t.Errorf("%d denied rows for the tool outside the grant", n)
	}
	_, end, refused = r.disp.AdmitCodeCall(ctx, pr, "not_a_tool\x00secret", []byte(`{}`), false)
	wantPermission(t, "an unknown tool", end, refused)
	if n := r.deniedRows("cc-tools", "unknown"); n != 1 {
		t.Errorf("%d denied rows for the unknown tool", n)
	}
	// a tool of the list is admitted
	r.enroll("cc-tier", r.approval(TierAggregate, "audit_get_guide"))
	if _, end, refused := r.disp.AdmitCodeCall(ctx, r.principal("cc-tier"), "audit_get_guide", []byte(`{}`), false); refused != nil {
		t.Errorf("a tool of the list: %v", refused)
	} else {
		end(nil, false)
	}
	if r.disp.total != 0 {
		t.Errorf("in-flight total %d", r.disp.total)
	}
}

func TestCodeCallIdentifiersWindow(t *testing.T) {
	r := newRig(t)
	a := r.approval(TierAggregate)
	a.Identifiers = true
	r.enroll("cc-id", a)
	pr := r.principal("cc-id")
	// the window ends while the call runs: the script gets no answer
	_, end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), true)
	if refused != nil {
		t.Fatal(refused)
	}
	r.clk.Add(DefaultIdentifiers + time.Minute)
	if ref := end([]byte(`{}`), false); ref == nil || ref.Code != mcp.WirePermissionRequired {
		t.Errorf("end after the window: %v", ref)
	}
	// the window is over at admit: a run that asked for identifiers is refused, a run that did not is not
	_, end, refused = r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), true)
	wantPermission(t, "identifiers after the window", end, refused)
	if _, end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), false); refused != nil {
		t.Errorf("a run without identifiers: %v", refused)
	} else if ref := end(nil, false); ref != nil {
		t.Errorf("end of a run without identifiers: %v", ref)
	}
	if r.disp.total != 0 {
		t.Errorf("in-flight total %d", r.disp.total)
	}
}

// panicRec is the ledger of the rig that panics where it is told to.
type panicRec struct {
	*ledger
	onFingerprint, onAdmit, onEnd bool
}

func (p *panicRec) Fingerprint(a []byte) []byte {
	if p.onFingerprint {
		panic("fingerprint")
	}
	return p.ledger.Fingerprint(a)
}

func (p *panicRec) Record(c context.Context, e mcpaudit.Event) error {
	if (p.onAdmit && e.Kind == mcpaudit.KindAdmit) || (p.onEnd && e.Kind == mcpaudit.KindEnd) {
		panic("record")
	}
	return p.ledger.Record(c, e)
}

func (r *rig) panicDisp(pr *panicRec) *Dispatcher {
	r.t.Helper()
	d, err := NewDispatcher(Deps{Grants: r.g, Backend: r.backend, Recorder: pr, Now: r.clk.Now, Budgets: DefaultBudgets})
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

func panics(f func()) (did bool) {
	defer func() { did = recover() != nil }()
	f()
	return false
}

func TestCodeCallPanicBeforeHandOffLeaksNoSlot(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*panicRec, bool)
	}{
		{"fingerprint", func(p *panicRec, v bool) { p.onFingerprint = v }},
		{"admit row", func(p *panicRec, v bool) { p.onAdmit = v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.enroll("cc-panic", r.approval(TierAggregate))
			pr := r.principal("cc-panic")
			rec := &panicRec{ledger: r.led}
			d := r.panicDisp(rec)
			tc.set(rec, true)
			for i := 0; i < MaxInFlight+1; i++ { // more than the host may have in flight
				if !panics(func() { d.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), false) }) {
					t.Fatalf("attempt %d did not panic", i)
				}
			}
			tc.set(rec, false)
			if d.total != 0 || len(d.perProf) != 0 {
				t.Errorf("slots leaked: %d %v", d.total, d.perProf)
			}
			if len(r.g.running) != 0 {
				t.Errorf("running calls of the grant leaked: %d", len(r.g.running))
			}
			if st, a := d.CallAs(ctx, pr, []byte(`{"tool":"audit_get_guide","args":{}}`)); st != 200 || !a.OK {
				t.Errorf("a direct call after the panics: %d %v", st, a.Error)
			}
		})
	}
}

func TestCodeCallPanicInEndLeavesNoSlotAndEndRunsOnce(t *testing.T) {
	r := newRig(t)
	r.enroll("cc-end", r.approval(TierAggregate))
	pr := r.principal("cc-end")
	rec := &panicRec{ledger: r.led}
	d := r.panicDisp(rec)
	_, end, refused := d.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), false)
	if refused != nil {
		t.Fatal(refused)
	}
	rec.onEnd = true
	if !panics(func() { end([]byte(`{}`), false) }) {
		t.Fatal("end did not panic")
	}
	rec.onEnd = false
	if d.total != 0 || len(d.perProf) != 0 || len(r.g.running) != 0 {
		t.Errorf("after a panic in end: total %d, %v, running %d", d.total, d.perProf, len(r.g.running))
	}
	if ref := end([]byte(`{}`), false); ref != nil || d.total != 0 {
		t.Errorf("second end: %v, total %d", ref, d.total)
	}
}

func TestCodeCallEndTwiceDoesNotGoNegative(t *testing.T) {
	r := newRig(t)
	r.enroll("cc-twice", r.approval(TierAggregate))
	pr := r.principal("cc-twice")
	_, end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), false)
	if refused != nil {
		t.Fatal(refused)
	}
	_, other, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), false)
	if refused != nil {
		t.Fatal(refused)
	}
	if end(nil, false) != nil || end(nil, false) != nil {
		t.Error("end refused an unchanged grant")
	}
	if r.disp.total != 1 || r.disp.perProf["cc-twice"] != 1 {
		t.Errorf("after two ends of one call: total %d, per profile %v (the other call is still running)", r.disp.total, r.disp.perProf)
	}
	other(nil, false)
	if r.disp.total != 0 || len(r.disp.perProf) != 0 {
		t.Errorf("at the end: total %d, %v", r.disp.total, r.disp.perProf)
	}
}

func TestCodeCallEndGivesTheSlotBack(t *testing.T) {
	r := newRig(t)
	h := r.enroll("cc-slot", r.approval(TierAggregate))
	pr := r.principal("cc-slot")
	for i := 0; i < MaxInFlight+2; i++ {
		_, end, refused := r.disp.AdmitCodeCall(ctx, pr, "audit_get_guide", []byte(`{}`), false)
		if refused != nil {
			t.Fatalf("call %d: %v", i+1, refused)
		}
		end([]byte(`{}`), false)
	}
	if r.disp.total != 0 {
		t.Errorf("in-flight total %d", r.disp.total)
	}
	if c := r.call(h, "audit_get_guide", map[string]any{}); !c.OK {
		t.Errorf("a direct call: %.200s", c.Raw)
	}
}
