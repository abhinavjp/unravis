package codemode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode/bindings"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/mcpsrv"
)

// BL-574 slice 2b: unravis_types and unravis_execute for a grant, end to end over the real grants store, the real dispatcher, the real
// broker and the real read-only tools on the demo repository. Only the JavaScript worker is scripted (there is none yet).

// xorSeal is a reversible stand-in for DPAPI (the same idea as the one in the mcpsrv tests).
type xorSeal struct{}

func (xorSeal) Protect(b []byte) ([]byte, error) {
	out := append([]byte("SEALED1:"), b...)
	for i := 8; i < len(out); i++ {
		out[i] ^= 0x5a
	}
	return out, nil
}

func (xorSeal) Unprotect(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte("SEALED1:")) {
		return nil, errors.New("not sealed")
	}
	out := append([]byte(nil), b[8:]...)
	for i := range out {
		out[i] ^= 0x5a
	}
	return out, nil
}

// mcpRig is the broker of env wired to a dispatcher the way the app wires them.
type mcpRig struct {
	*env
	db    *mcpaudit.DB
	g     *mcpsrv.Grants
	disp  *mcpsrv.Dispatcher
	code  *MCPCode
	mu    sync.Mutex
	calls int
}

func newMCPRig(t *testing.T, script fakeScript, opts ...envOpt) *mcpRig {
	t.Helper()
	e := newEnv(t, script, opts...)
	db, rec, err := mcpaudit.Open(context.Background(), t.TempDir(), xorSeal{}, nil)
	if err != nil || rec != "" {
		t.Fatalf("mcpaudit: %v %q", err, rec)
	}
	t.Cleanup(func() { _ = db.Close() })
	g, err := mcpsrv.NewGrants(context.Background(), db, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	r := &mcpRig{env: e, db: db, g: g, code: &MCPCode{Broker: e.b}}
	r.disp, err = mcpsrv.NewDispatcher(mcpsrv.Deps{Grants: g, Backend: mcpsrv.NewLocalBackend(e.handler, testToken), Recorder: db, Code: r.code})
	if err != nil {
		t.Fatal(err)
	}
	// The same late binding cmd/audit-explorer does.
	e.b.SetAdmit(r.disp.AdmitCodeCall)
	g.OnRevoke(func(id string) { e.b.CancelGrant(id) })
	return r
}

// grant makes an active, claimed grant and returns its principal.
func (r *mcpRig) grant(id string, a mcpsrv.Approval) mcpsrv.Principal {
	r.t.Helper()
	ctx := context.Background()
	n := make([]byte, 16)
	if _, err := rand.Read(n); err != nil {
		r.t.Fatal(err)
	}
	nonce := base64.RawURLEncoding.EncodeToString(n)
	if _, err := r.g.Enroll(ctx, mcpsrv.EnrollReq{Profile: id, HostName: "test-host", HostVersion: "1", Exe: `C:\host.exe`, PPID: 1, ClaimNonce: nonce}); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.g.Approve(ctx, id, a); err != nil {
		r.t.Fatal(err)
	}
	secret, _, err := r.g.Claim(ctx, id, nonce)
	if err != nil {
		r.t.Fatal(err)
	}
	pr, err := r.g.Authenticate(ctx, "Unravis-Grant "+id+"."+secret)
	if err != nil {
		r.t.Fatal(err)
	}
	return pr
}

func approval(tier string, tools ...string) mcpsrv.Approval {
	return mcpsrv.Approval{Tier: tier, Tools: tools, Sources: []string{"demo-west"}, IdleTTL: mcpsrv.DefaultIdleTTL}
}

// invoke is a tools/call of the host: the body of POST /mcp/v1/call.
func (r *mcpRig) invoke(pr mcpsrv.Principal, tool string, args any) (int, mcpsrv.Answer) {
	r.t.Helper()
	r.mu.Lock()
	r.calls++
	id := fmt.Sprintf("%08x", r.calls)
	r.mu.Unlock()
	body, err := json.Marshal(map[string]any{"tool": tool, "args": args, "callId": id})
	if err != nil {
		r.t.Fatal(err)
	}
	return r.disp.CallAs(context.Background(), pr, body)
}

// refusal reads the {code, message} of a refused code-mode call, or "" when the answer is not one.
func refusal(ans mcpsrv.Answer) (code, message string) {
	var env struct {
		Error *struct{ Code, Message string } `json:"error"`
	}
	if !ans.OK || !ans.IsError || json.Unmarshal(ans.Result, &env) != nil || env.Error == nil {
		return "", ""
	}
	return env.Error.Code, env.Error.Message
}

func (r *mcpRig) wantRefusal(what string, status int, ans mcpsrv.Answer, wantCode string) {
	r.t.Helper()
	code, msg := refusal(ans)
	if status != http.StatusOK || code != wantCode || msg == "" {
		r.t.Errorf("%s: status %d code %q message %q (answer %+v)", what, status, code, msg, ans)
	}
}

func (r *mcpRig) wantNothingRan(what string) {
	r.t.Helper()
	if n := r.sp.started(); n != 0 {
		r.t.Errorf("%s: %d workers were started", what, n)
	}
	if n := len(r.runs()); n != 0 {
		r.t.Errorf("%s: %d runs were recorded", what, n)
	}
}

// runBody decodes the record of a run an execute answer carries.
func runBody(t *testing.T, ans mcpsrv.Answer) RunResult {
	t.Helper()
	var res RunResult
	if !ans.OK || json.Unmarshal(ans.Result, &res) != nil || res.RunID == "" {
		t.Fatalf("not a run record: %+v", ans)
	}
	return res
}

type typesBody struct {
	Namespace string   `json:"namespace"`
	DTS       string   `json:"dts"`
	Tier      string   `json:"tier"`
	Tools     []string `json:"tools"`
	Sources   []string `json:"sources"`
	Limits    map[string]int64
}

func typesOf(t *testing.T, ans mcpsrv.Answer) typesBody {
	t.Helper()
	var tb typesBody
	if !ans.OK || ans.IsError || json.Unmarshal(ans.Result, &tb) != nil || tb.DTS == "" {
		t.Fatalf("not a types answer: %+v", ans)
	}
	return tb
}

// The declarations of unravis_types name the tools of the grant's tier and tool list and the grant's sources, and nothing wider.
func TestMCPTypesNarrowToTheGrant(t *testing.T) {
	r := newMCPRig(t, nil)
	narrow := r.grant("h-narrow", approval(mcpsrv.TierEvidence, "audit_get_guide", "audit_compare"))
	_, ans := r.invoke(narrow, mcp.ToolTypes, map[string]any{})
	tb := typesOf(t, ans)
	if want := mcpsrv.GrantTools(mcpsrv.TierEvidence, []string{"audit_get_guide", "audit_compare"}); !sameSet(tb.Tools, want) || len(want) != 2 {
		t.Errorf("tools %v, want %v", tb.Tools, want)
	}
	if !slices.Equal(tb.Sources, []string{"demo-west"}) || tb.Tier != mcpsrv.TierEvidence {
		t.Errorf("scope %v / %q", tb.Sources, tb.Tier)
	}
	// A function is declared for each tool of the grant and for no other (a tool's name may still appear in the doc text of a shared field).
	for _, name := range []string{"audit_get_guide", "audit_compare"} {
		if !declares(tb.DTS, name) {
			t.Errorf("%s is not declared", name)
		}
	}
	for _, name := range []string{"audit_get_requests", "audit_get_slowest", "audit_get_activity", "audit_get_breakdown", "audit_list_sources", "audit_get_groups", mcp.ToolExecute, mcp.ToolTypes} {
		if declares(tb.DTS, name) {
			t.Errorf("%s is declared to a grant that does not list it", name)
		}
	}
	if tb.Limits["maxCalls"] != int64(MaxCalls) || tb.Limits["maxSourceBytes"] != int64(MaxSource) {
		t.Errorf("limits %v", tb.Limits)
	}

	// The tier decides what an empty tool list means: all of the tier, so the aggregate grant has no raw-row tool and the evidence grant has.
	agg := r.grant("h-agg", approval(mcpsrv.TierAggregate))
	ev := r.grant("h-ev", approval(mcpsrv.TierEvidence))
	_, a := r.invoke(agg, mcp.ToolTypes, map[string]any{})
	aggT := typesOf(t, a)
	_, a = r.invoke(ev, mcp.ToolTypes, map[string]any{})
	evT := typesOf(t, a)
	if declares(aggT.DTS, "audit_get_requests") || slices.Contains(aggT.Tools, "audit_get_requests") {
		t.Error("an aggregate grant is shown the raw-row tool")
	}
	if !declares(evT.DTS, "audit_get_requests") || !sameSet(evT.Tools, mcpsrv.ToolsForTier(mcpsrv.TierEvidence)) {
		t.Errorf("an evidence grant is not shown its tools: %v", evT.Tools)
	}
	if !sameSet(aggT.Tools, mcpsrv.ToolsForTier(mcpsrv.TierAggregate)) || len(aggT.DTS) >= len(evT.DTS) {
		t.Errorf("aggregate tools %v", aggT.Tools)
	}
	r.wantNothingRan("unravis_types")
}

// The caller cannot widen what it is shown (or run) with arguments: scope, tier and tool list are never arguments.
func TestMCPArgumentsNeverCarryScope(t *testing.T) {
	r := newMCPRig(t, func(w *fakeWorker) { w.done(1) })
	pr := r.grant("h-args", approval(mcpsrv.TierAggregate, "audit_compare"))
	for name, args := range map[string]any{
		"types with a tier":  map[string]any{"tier": "evidence"},
		"types with tools":   map[string]any{"tools": []string{"audit_get_requests"}},
		"types with sources": map[string]any{"sources": []string{"demo-east"}},
	} {
		_, ans := r.invoke(pr, mcp.ToolTypes, args)
		r.wantRefusal(name, http.StatusOK, ans, CodeBadRequest)
	}
	for name, args := range map[string]any{
		"execute with a tier":         map[string]any{"code": "1", "tier": "evidence"},
		"execute with tools":          map[string]any{"code": "1", "tools": []string{"audit_get_requests"}},
		"execute with sources":        map[string]any{"code": "1", "sources": []string{"demo-east"}},
		"execute with identifiers":    map[string]any{"code": "1", "allowIdentifiers": true},
		"execute with a profile":      map[string]any{"code": "1", "profileId": "other"},
		"execute with scoped false":   map[string]any{"code": "1", "scoped": false},
		"execute, negative timeout":   map[string]any{"code": "1", "timeout_ms": -1},
		"execute, long case id":       map[string]any{"code": "1", "case_id": strings.Repeat("c", mcp.ExecuteMaxCaseID+1)},
		"execute, code is not a text": map[string]any{"code": 5},
	} {
		_, ans := r.invoke(pr, mcp.ToolExecute, args)
		r.wantRefusal(name, http.StatusOK, ans, CodeBadRequest)
	}
	r.wantNothingRan("a refused argument")

	// What the dispatcher lets through to the adapter is only an object, but the adapter holds the rule itself: null and an empty object
	// are no arguments, and anything else is refused, including two documents in a row.
	p, err := r.g.Scope(context.Background(), pr)
	if err != nil {
		t.Fatal(err)
	}
	for args, want := range map[string]string{"": "", "null": "", "{}": "", " {} ": "", "[1]": CodeBadRequest, `{"tier":"evidence"}`: CodeBadRequest, `{} {}`: CodeBadRequest} {
		body, isErr, _ := r.code.CallCode(context.Background(), mcp.ToolTypes, pr, p, []byte(args), time.Now())
		var env struct{ Error *struct{ Code string } }
		_ = json.Unmarshal(body, &env)
		if got := ""; env.Error != nil {
			got = env.Error.Code
			if got != want || !isErr {
				t.Errorf("types args %q: %q", args, got)
			}
		} else if want != "" || isErr {
			t.Errorf("types args %q: %v %.100s", args, isErr, body)
		}
	}
	for _, args := range []string{`{"code":"1"} {"code":"2"}`, `{"code":"1"}x`, `[`, `"code"`} {
		body, isErr, _ := r.code.CallCode(context.Background(), mcp.ToolExecute, pr, p, []byte(args), time.Now())
		if !isErr || !strings.Contains(string(body), CodeBadRequest) {
			t.Errorf("execute args %q: %v %.100s", args, isErr, body)
		}
	}
	r.wantNothingRan("a refused argument")
}

// sameSet compares two lists of names without their order (the declarations come in the order of the catalog).
func sameSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// declares says whether the declarations hold a function of that name.
func declares(dts, name string) bool { return strings.Contains(dts, "function "+name+"(") }

// The run carries exactly the grant's scope: the frame the worker gets lists the grant's tools, a call to anything else is a violation,
// and the record says what the run could reach.
func TestMCPExecuteCarriesTheGrantScope(t *testing.T) {
	narrow := []string{"audit_compare", "audit_get_guide"}
	for _, outside := range []string{"audit_get_requests", "audit_get_breakdown", mcp.ToolExecute, mcp.ToolTypes} {
		var worker *fakeWorker
		var answer json.RawMessage
		r := newMCPRig(t, func(w *fakeWorker) {
			worker = w
			answer, _ = w.Call(outside, map[string]any{})
			w.done(1)
		})
		pr := r.grant("h-scope", approval(mcpsrv.TierEvidence, narrow...))
		status, ans := r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "1", "reason": "scope test"})
		if status != http.StatusOK {
			t.Fatalf("%s: status %d", outside, status)
		}
		res := runBody(t, ans)
		if res.Status != casestore.RunError || res.Error == nil || res.Error.Code != CodeBadFrame || !ans.IsError {
			t.Errorf("%s: a call outside the tool list did not end the run: %+v", outside, res)
		}
		if len(answer) != 0 {
			t.Errorf("%s: it was answered: %.200s", outside, answer)
		}
		if want := mcpsrv.GrantTools(mcpsrv.TierEvidence, narrow); !slices.Equal(worker.runMsg.Tools, want) {
			t.Errorf("%s: the run frame names %v, want %v", outside, worker.runMsg.Tools, want)
		}
		row := r.getRun(res.RunID)
		if row.Surface != "mcp" || row.CallerID != "h-scope" || row.CallerDeclared || row.AllowIdentifiers ||
			!strings.HasPrefix(row.Reason, "[sources: demo-west; tier: evidence] ") || !strings.HasSuffix(row.Reason, "scope test") {
			t.Errorf("%s: the record: %+v", outside, row)
		}
		r.verify()
	}

	// Inside the scope the script works, and the other source is out of reach even for a tool of the list.
	var west, east json.RawMessage
	r := newMCPRig(t, func(w *fakeWorker) {
		q := demoQuery()
		q["sourceIds"] = []string{"demo-west"}
		west, _ = w.Call("audit_compare", map[string]any{"query": q, "limit": 3})
		q["sourceIds"] = []string{"demo-east"}
		east, _ = w.Call("audit_compare", map[string]any{"query": q, "limit": 3})
		w.done("fine")
	})
	pr := r.grant("h-src", approval(mcpsrv.TierEvidence, narrow...))
	_, ans := r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "1"})
	if res := runBody(t, ans); res.Status != casestore.RunOK || ans.IsError {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(string(west), `"groups"`) {
		t.Errorf("a query inside the scope: %.300s", west)
	}
	var env struct{ Error *struct{ Code string } }
	if json.Unmarshal(east, &env) != nil || env.Error == nil || strings.Contains(string(east), `"groups"`) {
		t.Errorf("a query on a source outside the grant was answered: %.300s", east)
	}
	r.verify()
}

// The grant's tier decides what a script sees: an aggregate grant gets "<5" for a small group and no figures; an evidence grant gets them.
func TestMCPTierHidesSmallCountsAndRecordIDs(t *testing.T) {
	var agg, ev json.RawMessage
	r := newMCPRig(t, func(w *fakeWorker) {
		agg, _ = w.Call("audit_compare", map[string]any{"query": narrowQuery(), "limit": 5})
		w.done(1)
	})
	_, ans := r.invoke(r.grant("h-agg", approval(mcpsrv.TierAggregate)), mcp.ToolExecute, map[string]any{"code": "1"})
	if res := runBody(t, ans); res.Status != casestore.RunOK {
		t.Fatalf("%+v", res)
	}
	r2 := newMCPRig(t, func(w *fakeWorker) {
		ev, _ = w.Call("audit_compare", map[string]any{"query": narrowQuery(), "limit": 5})
		w.done(1)
	})
	_, ans = r2.invoke(r2.grant("h-ev", approval(mcpsrv.TierEvidence)), mcp.ToolExecute, map[string]any{"code": "1"})
	if res := runBody(t, ans); res.Status != casestore.RunOK {
		t.Fatalf("%+v", res)
	}
	for _, g := range groupsOf(t, agg) {
		if g["suppressed"] != true || g["currentEvents"] != "<5" || g["referenceEvents"] != "<5" {
			t.Errorf("an aggregate grant saw a small count: %v", g)
		}
	}
	for _, g := range groupsOf(t, ev) {
		if g["suppressed"] != nil || g["currentEvents"] != float64(2) {
			t.Errorf("an evidence grant did not get the real figures: %v", g)
		}
	}

	// Record ids need the grant's identifiers permission AND the operator's switch; either one missing hides them.
	for _, tc := range []struct {
		name     string
		operator bool
		grant    bool
		want     bool
	}{{"neither", false, false, false}, {"operator only", true, false, false}, {"grant only", false, true, false}, {"both", true, true, true}} {
		var rows []any
		var opts []envOpt
		if tc.operator {
			opts = append(opts, withOperatorIdentifiers)
		}
		rig := newMCPRig(t, func(w *fakeWorker) {
			q := demoQuery()
			q["sourceIds"] = []string{"demo-west"}
			c, _ := w.Call("audit_compare", map[string]any{"query": q, "limit": 5})
			var env struct{ ResultID string }
			_ = json.Unmarshal(c, &env)
			raw, _ := w.Call("audit_get_requests", map[string]any{"resultId": env.ResultID, "apiKey": groupsOf(t, c)[0]["key"], "limit": 5})
			var out struct{ Rows []any }
			_ = json.Unmarshal(raw, &out)
			rows = out.Rows
			w.done(1)
		}, opts...)
		a := approval(mcpsrv.TierEvidence)
		a.Identifiers = tc.grant
		_, ans := rig.invoke(rig.grant("h-id", a), mcp.ToolExecute, map[string]any{"code": "1"})
		res := runBody(t, ans)
		if res.Status != casestore.RunOK || len(rows) == 0 {
			t.Fatalf("%s: %+v rows %d", tc.name, res, len(rows))
		}
		for _, row := range rows {
			if _, has := row.(map[string]any)["id"]; has != tc.want {
				t.Errorf("%s: record id present %v", tc.name, has)
			}
		}
		if got := rig.getRun(res.RunID).AllowIdentifiers; got != tc.want {
			t.Errorf("%s: the record says identifiers %v", tc.name, got)
		}
	}
}

// A tier the broker does not know, or a surface mcp run with no scope, is refused with nothing written, whatever the grant says.
func TestMCPUnknownTierAndMissingScopeAreRefused(t *testing.T) {
	r := newMCPRig(t, func(w *fakeWorker) { w.done(1) })
	pr := mcpsrv.Principal{ProfileID: "forged", Rev: 1}
	now := time.Now()
	for name, p := range map[string]mcpsrv.Profile{
		"unknown tier":    {ID: "forged", Tier: "root", Tools: []string{"audit_compare"}, Sources: []string{"demo-west"}},
		"empty tier":      {ID: "forged", Tier: "", Tools: []string{"audit_compare"}, Sources: []string{"demo-west"}},
		"tier in caps":    {ID: "forged", Tier: "Evidence", Tools: []string{"audit_compare"}, Sources: []string{"demo-west"}},
		"no tool of tier": {ID: "forged", Tier: mcpsrv.TierAggregate, Tools: []string{"audit_get_requests"}, Sources: []string{"demo-west"}},
		"no sources":      {ID: "forged", Tier: mcpsrv.TierEvidence, Tools: []string{"audit_compare"}},
	} {
		for _, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
			body, isErr, _ := r.code.CallCode(context.Background(), tool, pr, p, []byte(`{"code":"1"}`), now)
			var env struct{ Error *struct{ Code string } }
			if !isErr || json.Unmarshal(body, &env) != nil || env.Error == nil || env.Error.Code == "" {
				t.Errorf("%s / %s: %v %.200s", name, tool, isErr, body)
			}
			if tool == mcp.ToolExecute && name != "no sources" && name != "no tool of tier" && env.Error != nil && env.Error.Code != CodeDenied {
				t.Errorf("%s / %s: code %q, want denied", name, tool, env.Error.Code)
			}
		}
	}
	r.wantNothingRan("a grant with no usable scope")

	// The broker's own rule under the adapter: the surface mcp needs a scope.
	_, err := r.b.Run(context.Background(), RunRequest{Code: "1", Surface: "mcp"})
	if ref, ok := err.(*Refusal); !ok || ref.Code != CodeDenied && ref.Code != CodeBadRequest {
		t.Errorf("surface mcp without scope: %v", err)
	}
	// With no call admitter bound (the app did not wire the dispatcher), a grant's run is refused instead of running unmetered.
	r.b.SetAdmit(nil)
	pr = r.grant("h-noadmit", approval(mcpsrv.TierAggregate))
	_, ans := r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "1"})
	r.wantRefusal("no admitter", http.StatusOK, ans, CodeDenied)
	r.wantNothingRan("a run with no admitter")
}

// The owner's switch (code-mode.json) gates both tools, and so does a build with no broker.
func TestMCPCodeModeOffRefusesBothTools(t *testing.T) {
	r := newMCPRig(t, func(w *fakeWorker) { w.done(1) })
	pr := r.grant("h-off", approval(mcpsrv.TierAggregate))
	r.set.Enabled = false
	for _, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
		status, ans := r.invoke(pr, tool, map[string]any{"code": "1"})
		if tool == mcp.ToolTypes {
			status, ans = r.invoke(pr, tool, map[string]any{})
		}
		r.wantRefusal(tool, status, ans, CodeModeOff)
		if _, msg := refusal(ans); msg != mcpsrv.CodeModeOffMessage || msg != PlainMessage(CodeModeOff) {
			t.Errorf("%s: message %q", tool, msg)
		}
	}
	r.wantNothingRan("code mode off")

	// Switched on again, the same grant works: the refusal was the switch and not a lingering state.
	r.set.Enabled = true
	_, ans := r.invoke(pr, mcp.ToolTypes, map[string]any{})
	typesOf(t, ans)

	// No broker in this build: the dispatcher answers the same way, and a nil adapter does too.
	bare, err := mcpsrv.NewDispatcher(mcpsrv.Deps{Grants: r.g, Backend: mcpsrv.NewLocalBackend(r.handler, testToken), Recorder: r.db})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
		body, _ := json.Marshal(map[string]any{"tool": tool, "args": map[string]any{}, "callId": "0000beef"})
		status, ans := bare.CallAs(context.Background(), pr, body)
		r.wantRefusal("no broker "+tool, status, ans, CodeModeOff)
	}
	var none *MCPCode
	body, isErr, _ := none.CallCode(context.Background(), mcp.ToolExecute, pr, mcpsrv.Profile{}, nil, time.Now())
	if code := string(body); !isErr || !strings.Contains(code, CodeModeOff) {
		t.Errorf("nil adapter: %v %s", isErr, body)
	}
	r.wantNothingRan("no broker")
}

// A revoked grant admits nothing: both tools are refused with the same permission refusal the other tools give, and nothing starts.
func TestMCPRevokedGrantIsRefused(t *testing.T) {
	r := newMCPRig(t, func(w *fakeWorker) { w.done(1) })
	pr := r.grant("h-rev", approval(mcpsrv.TierEvidence))
	if _, err := r.g.Revoke(context.Background(), "h-rev"); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
		status, ans := r.invoke(pr, tool, map[string]any{"code": "1"})
		if tool == mcp.ToolTypes {
			status, ans = r.invoke(pr, tool, map[string]any{})
		}
		if status != http.StatusUnauthorized || ans.OK || ans.Error == nil || ans.Error.Code != mcp.WirePermissionRequired || ans.Error.Message == "" || len(ans.Result) != 0 {
			t.Errorf("%s: status %d answer %+v", tool, status, ans)
		}
	}
	r.wantNothingRan("a revoked grant")
}

// Revoking a grant ends the run it has going: the worker is killed, the record says cancelled, and the host is told its access ended and
// is given no part of the run.
func TestMCPRevokeCancelsTheRunInFlight(t *testing.T) {
	r := newMCPRig(t, func(w *fakeWorker) { <-w.killed })
	pr := r.grant("h-live", approval(mcpsrv.TierEvidence))
	type out struct {
		status int
		ans    mcpsrv.Answer
	}
	ch := make(chan out, 1)
	go func() {
		status, ans := r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "for(;;){}"})
		ch <- out{status, ans}
	}()
	eventually(t, func() bool { return r.sp.started() == 1 && len(r.runs()) == 1 })

	// One run at a time for a grant: a second execute is told to wait, and starts nothing.
	status, second := r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "1"})
	if status != http.StatusTooManyRequests || second.OK || second.Error == nil || second.Error.Code != mcp.WireBudgetExceeded {
		t.Errorf("second run: %d %+v", status, second)
	}
	if r.sp.started() != 1 {
		t.Error("the second run started a worker")
	}

	if _, err := r.g.Revoke(context.Background(), "h-live"); err != nil {
		t.Fatal(err)
	}
	var o out
	select {
	case o = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("the run outlived the revoke")
	}
	if o.status != http.StatusUnauthorized || o.ans.OK || o.ans.Error == nil || o.ans.Error.Code != mcp.WirePermissionRequired || len(o.ans.Result) != 0 {
		t.Errorf("the host's answer after the revoke: %d %+v", o.status, o.ans)
	}
	if !r.sp.worker(0).isKilled() {
		t.Error("the worker is still alive")
	}
	runs := r.runs()
	if len(runs) != 1 || runs[0].Status != casestore.RunCancelled || runs[0].ErrorCode != CodeCancelled {
		t.Errorf("the record: %+v", runs)
	}
	r.verify()
}

// Broker.CancelGrant ends the live runs of one grant only, and reaches a run that is not tied to the revoke's call context.
func TestCancelGrantEndsOnlyThatGrantsRuns(t *testing.T) {
	e := newEnv(t, func(w *fakeWorker) { <-w.killed })
	type out struct {
		res RunResult
		err error
	}
	start := func(profile string) chan out {
		ch := make(chan out, 1)
		go func() {
			res, err := e.b.Run(context.Background(), RunRequest{Code: "for(;;){}", Surface: "mcp", Scoped: true, Tier: mcpsrv.TierEvidence,
				Sources: []string{"demo-west"}, ProfileID: profile, Tools: mcpsrv.ToolsForTier(mcpsrv.TierEvidence)})
			ch <- out{res, err}
		}()
		return ch
	}
	a, b := start("host-a"), start("host-b")
	eventually(t, func() bool { return e.sp.started() == 2 })
	if n := e.b.CancelGrant(""); n != 0 {
		t.Errorf("an empty id cancelled %d runs", n)
	}
	if n := e.b.CancelGrant("host-other"); n != 0 {
		t.Errorf("an unknown grant cancelled %d runs", n)
	}
	if n := e.b.CancelGrant("host-a"); n != 1 {
		t.Fatalf("CancelGrant(host-a) = %d", n)
	}
	oa := <-a
	if oa.err != nil {
		t.Fatal(oa.err)
	}
	e.wantStatus(oa.res, casestore.RunCancelled, CodeCancelled)
	select {
	case ob := <-b:
		t.Fatalf("the other grant's run ended too: %+v %v", ob.res, ob.err)
	case <-time.After(100 * time.Millisecond):
	}
	if n := e.b.CancelGrant("host-a"); n != 0 {
		t.Errorf("a finished run was cancelled again: %d", n)
	}
	if n := e.b.CancelGrant("host-b"); n != 1 {
		t.Fatalf("CancelGrant(host-b) = %d", n)
	}
	ob := <-b
	if ob.err != nil {
		t.Fatal(ob.err)
	}
	e.wantStatus(ob.res, casestore.RunCancelled, CodeCancelled)
	e.verify()
}

// A run asks for a case like the HTTP routes: no case given and one open runs there; an unknown case is no_case, with nothing started.
func TestMCPExecuteNeedsACase(t *testing.T) {
	r := newMCPRig(t, func(w *fakeWorker) { w.done(1) })
	pr := r.grant("h-case", approval(mcpsrv.TierAggregate))
	_, ans := r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "1", "case_id": "no-such-case"})
	r.wantRefusal("unknown case", http.StatusOK, ans, CodeNoCase)
	if r.sp.started() != 0 && len(r.runs()) != 0 {
		t.Error("a refused run left traces")
	}
	_, ans = r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "1", "case_id": r.caseID})
	if res := runBody(t, ans); res.Status != casestore.RunOK || ans.IsError {
		t.Errorf("%+v", res)
	}

	// The adapter says why there was no case when more than one is open.
	r.code.ManyOpen = func(context.Context) bool { return true }
	_, ans = r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "1", "case_id": "no-such-case"})
	if _, msg := refusal(ans); !strings.Contains(msg, "More than one case") {
		t.Errorf("message %q", msg)
	}
	leased := 0
	r.code.Leases = func(ctx context.Context) (context.Context, func()) { leased++; return ctx, func() { leased-- } }
	_, ans = r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "1"})
	if res := runBody(t, ans); res.Status != casestore.RunOK || leased != 0 {
		t.Errorf("%+v leases %d", res, leased)
	}
}

// A key that is not spelled exactly (encoding/json would match it case-insensitively) is refused, and nothing runs.
func TestMCPExecuteRefusesKeysThatAreNotExact(t *testing.T) {
	r := newMCPRig(t, func(w *fakeWorker) { w.done(1) })
	pr := r.grant("h-keys", approval(mcpsrv.TierAggregate))
	for _, args := range []map[string]any{{"CODE": "1"}, {"code": "1", "Reason": "x"}, {"code": "1", "extra": 1}} {
		_, ans := r.invoke(pr, mcp.ToolExecute, args)
		r.wantRefusal("inexact key", http.StatusOK, ans, CodeBadRequest)
	}
	r.wantNothingRan("inexact keys")
}

// A second unravis_execute that reuses a running call id is told so (400), not that the host is busy (429).
func TestMCPExecuteDuplicateCallID(t *testing.T) {
	r := newMCPRig(t, func(w *fakeWorker) { <-w.killed })
	pr := r.grant("h-dup", approval(mcpsrv.TierEvidence))
	body, err := json.Marshal(map[string]any{"tool": mcp.ToolExecute, "args": map[string]any{"code": "for(;;){}"}, "callId": "aaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	go r.disp.CallAs(context.Background(), pr, body)
	eventually(t, func() bool { return r.sp.started() == 1 })
	status, ans := r.disp.CallAs(context.Background(), pr, body)
	if status != http.StatusBadRequest || ans.OK || ans.Error == nil || ans.Error.Code != mcp.WireInvalidArguments {
		t.Errorf("duplicate call id: %d %+v", status, ans)
	}
	if _, err := r.g.Revoke(context.Background(), "h-dup"); err != nil {
		t.Fatal(err)
	}
}

// A run's answer too big for one message is replaced by a short error that names the run, never cut.
func TestMCPExecuteAnswerTooBigIsReplaced(t *testing.T) {
	r := newMCPRig(t, func(w *fakeWorker) { w.done(strings.Repeat("x", maxExecuteAnswer+1000)) })
	pr := r.grant("h-big", approval(mcpsrv.TierAggregate))
	_, ans := r.invoke(pr, mcp.ToolExecute, map[string]any{"code": "1"})
	var body struct {
		RunID  string
		Status string
		Result json.RawMessage
		Error  *struct{ Code, Message string }
	}
	if !ans.OK || !ans.IsError || json.Unmarshal(ans.Result, &body) != nil || body.RunID == "" || len(body.Result) != 0 ||
		body.Error == nil || body.Error.Code != CodeOutputTooLarge || len(ans.Result) > 1000 {
		t.Fatalf("answer %d bytes: %.300s", len(ans.Result), ans.Result)
	}
	// The run itself is on record and finished well.
	if row := r.getRun(body.RunID); row.Status != casestore.RunOK {
		t.Errorf("record %+v", row)
	}
}

// The code tools are not tools a script can call, and the ten audit tools are all it is told about.
func TestCodeToolsAreNotInTheScriptCatalog(t *testing.T) {
	tools := bindings.Spec().Tools
	if len(tools) != 10 {
		t.Errorf("%d tools", len(tools))
	}
	for _, tl := range tools {
		if mcp.IsCodeTool(tl.Name) {
			t.Errorf("%s is a script tool", tl.Name)
		}
	}
}
