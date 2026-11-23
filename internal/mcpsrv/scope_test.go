package mcpsrv

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcp"
)

// BL-348 slice 1 tests: the source scope of the grant, the tier as a ceiling, and the revoke gate for the identifiers grant (review S1 of
// #264), plus the two ledger rules the review found untested (S2). The real path as in dispatch_test.go: HTTP routes, a SQLite file, the
// real dashboard handler over the demo repository (two sources: demo-west and demo-east).

func (r *rig) scoped(id string, tier string, sources ...string) host {
	a := r.approval(tier)
	a.Sources = sources
	return r.enroll(id, a)
}

func (r *rig) compareFor(sources ...string) map[string]any {
	a := r.compareArgs()
	q := a["query"].(audit.Query)
	q.SourceIDs = sources
	return map[string]any{"query": q}
}

func (r *rig) activityFor(sources ...string) map[string]any {
	return map[string]any{"sourceIds": sources, "start": r.now.Add(-time.Hour), "end": r.now}
}

// A host approved for one source can use every tool on it and none on another, and a refusal reaches no repository.
func TestSourceScopeKeepsAHostToItsSources(t *testing.T) {
	r := newRig(t)
	h := r.scoped("west-only", TierEvidence, "demo-west")

	list := r.call(h, "audit_list_sources", map[string]any{})
	srcs, _ := list.env["sources"].([]any)
	if !list.OK || len(srcs) != 1 || strings.Contains(string(list.Raw), "demo-east") || strings.Contains(string(list.Raw), "Demo East") {
		t.Fatalf("the source list shows more than the grant: %.400s", list.Raw)
	}

	cmp := r.call(h, "audit_compare", r.compareFor("demo-west"))
	id, _ := cmp.env["resultId"].(string)
	if !cmp.OK || cmp.IsError || id == "" {
		t.Fatalf("compare on the granted source: %.400s", cmp.Raw)
	}
	groups := cmp.env["groups"].([]any)
	if len(groups) == 0 {
		t.Fatal("no group to follow")
	}
	key, _ := groups[0].(map[string]any)["key"].(string)
	for tool, args := range map[string]map[string]any{
		"audit_get_groups": {"resultId": id}, "audit_get_slowest": {"resultId": id}, "audit_get_breakdown": {"resultId": id, "dim": "statusCode"},
		"audit_get_requests": {"resultId": id, "apiKey": key}, "audit_get_timeline": {"resultId": id, "apiKey": key},
		"audit_get_activity": r.activityFor("demo-west"),
	} {
		if a := r.call(h, tool, args); !a.OK || a.IsError {
			t.Errorf("%s on the granted source: %.300s", tool, a.Raw)
		}
	}

	// Every way to name a source outside the grant gives the same answer, with no tool run and no echo.
	n := len(r.dash.hits())
	outside := map[string][]string{
		"other source": {"demo-east"}, "both": {"demo-west", "demo-east"}, "unknown": {"no-such-source"},
		"case": {"DEMO-WEST"}, "padded": {" demo-west"}, "suffix": {"demo-west\x00"}, "empty": {""},
	}
	for name, ids := range outside {
		for tool, args := range map[string]map[string]any{"audit_compare": r.compareFor(ids...), "audit_get_activity": r.activityFor(ids...)} {
			a := r.call(h, tool, args)
			want := "permission_required"
			if name == "empty" {
				want = "invalid_arguments" // a blank id is refused by the query's own validation before the scope is looked at
			}
			if !a.OK || !a.IsError || errCode(a.env) != want {
				t.Errorf("%s %s: %.300s", name, tool, a.Raw)
			}
			for _, leak := range []string{"demo-east", "no-such-source", "DEMO-WEST"} {
				if strings.Contains(string(a.Raw), leak) {
					t.Errorf("%s %s echoes %q: %.300s", name, tool, leak, a.Raw)
				}
			}
		}
	}
	if len(r.dash.hits()) != n {
		t.Errorf("a refused call reached the dashboard: %v", r.dash.hits()[n:])
	}
	// The refusal is the same text for a real source and for one that does not exist: it is no way to learn which sources exist.
	e1 := r.call(h, "audit_compare", r.compareFor("demo-east"))
	e2 := r.call(h, "audit_compare", r.compareFor("no-such-source"))
	if string(e1.Result) != string(e2.Result) {
		t.Errorf("the refusal tells an existing source from a missing one:\n%s\n%s", e1.Result, e2.Result)
	}
	// The ledger calls it denied, not an error.
	denied := 0
	for _, ev := range r.events() {
		if ev.Kind == "end" && ev.Tool == "audit_compare" && ev.Outcome == "denied" {
			denied++
		}
	}
	if denied == 0 {
		t.Error("the ledger has no denied row for a source outside the grant")
	}
}

// Two hosts with different scopes: each reads its own source, neither the other's, and a handle is not valid across hosts.
func TestSourceScopeIsPerHost(t *testing.T) {
	r := newRig(t)
	w := r.scoped("only-w", TierAggregate, "demo-west")
	e := r.scoped("only-e", TierAggregate, "demo-east")
	if a := r.call(w, "audit_compare", r.compareFor("demo-east")); !a.IsError || errCode(a.env) != "permission_required" {
		t.Errorf("west host read east: %.300s", a.Raw)
	}
	if a := r.call(e, "audit_compare", r.compareFor("demo-east")); !a.OK || a.IsError {
		t.Errorf("east host on east: %.300s", a.Raw)
	}
	if a := r.call(e, "audit_compare", r.compareFor("demo-west")); !a.IsError || errCode(a.env) != "permission_required" {
		t.Errorf("east host read west: %.300s", a.Raw)
	}
	if a := r.call(e, "audit_list_sources", map[string]any{}); strings.Contains(string(a.Raw), "demo-west") {
		t.Errorf("east host sees west in the list: %.300s", a.Raw)
	}
}

// A scoped bridge with no source in scope reaches nothing at all, and a bridge cannot be built unscoped by a nil list.
func TestEmptyScopeReachesNothing(t *testing.T) {
	r := newRig(t)
	for _, src := range [][]string{{}, nil} {
		p := Profile{ID: "nobody", Rev: 1, Sources: src}
		for _, args := range []any{r.compareFor("demo-west"), r.activityFor("demo-west")} {
			tool := "audit_compare"
			if _, ok := args.(map[string]any)["sourceIds"]; ok {
				tool = "audit_get_activity"
			}
			raw, _ := json.Marshal(args)
			body, isErr := r.backend.Call(context.Background(), p, tool, raw, r.now)
			if !isErr || !strings.Contains(string(body), "permission_required") {
				t.Errorf("empty scope %v %s: %.300s", src, tool, body)
			}
		}
		body, isErr := r.backend.Call(context.Background(), p, "audit_list_sources", json.RawMessage(`{}`), r.now)
		if isErr || strings.Contains(string(body), "demo-") {
			t.Errorf("empty scope lists sources: %.300s", body)
		}
	}
}

// The tier is a ceiling: a stored tool list that names more than the tier allows does not widen it.
func TestTierIsACeilingOnTheToolList(t *testing.T) {
	r := newRig(t)
	h := r.scoped("ceil", TierAggregate, "demo-west")
	if _, err := r.db.Update(ctx, "ceil", func(p *Profile) error {
		p.Tools = append(p.Tools, "audit_get_requests", "audit_get_slowest", "audit_get_activity")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	n := len(r.dash.hits())
	for _, tool := range evidenceTools {
		a := r.call(h, tool, map[string]any{})
		if a.Status != 403 || a.OK || a.Error == nil || a.Error.Code != "permission_required" {
			t.Errorf("%s: %d %.200s", tool, a.Status, a.Raw)
		}
	}
	if len(r.dash.hits()) != n {
		t.Error("an evidence tool of an aggregate profile reached the dashboard")
	}
}

// The user filter of audit_get_activity is an identifier filter. The published schema does not list it, but the input type accepts it.
func TestActivityUserFilterNeedsTheIdentifiersGrant(t *testing.T) {
	r := newRig(t)
	args := r.activityFor("demo-west")
	args["userId"] = 424242
	without := r.scoped("act-no", TierEvidence, "demo-west")
	a := r.call(without, "audit_get_activity", args)
	if !a.OK || !a.IsError || errCode(a.env) != "permission_required" || strings.Contains(string(a.Raw), "424242") {
		t.Fatalf("without the grant: %.300s", a.Raw)
	}
	ap := r.approval(TierEvidence)
	ap.Sources, ap.Identifiers = []string{"demo-west"}, true
	with := r.enroll("act-yes", ap)
	if a := r.call(with, "audit_get_activity", args); !a.OK || a.IsError {
		t.Fatalf("with the grant: %.300s", a.Raw)
	}
}

// ---- review S1 and S2 of #264 ----

// held is a backend that records what it was given and waits to be released, ignoring its context.
type held struct {
	mu       sync.Mutex
	deadline time.Time
	hasDl    bool
	enter    chan struct{}
	release  chan struct{}
}

func (h *held) Call(ctx context.Context, _ Profile, tool string, _ json.RawMessage, _ time.Time) ([]byte, bool) {
	h.mu.Lock()
	h.deadline, h.hasDl = ctx.Deadline()
	h.mu.Unlock()
	if h.enter != nil {
		h.enter <- struct{}{}
		<-h.release
	}
	return mcp.ErrorEnvelope(tool, "internal", "x", ""), false
}
func (h *held) Forget(string) {}

func (e *env) enrolledWith(id string, a Approval) (header string, p Principal) {
	e.t.Helper()
	n := nonce(e.t)
	if _, err := e.g.Enroll(ctx, req(id, n)); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.g.Approve(ctx, id, a); err != nil {
		e.t.Fatal(err)
	}
	secret, _, err := e.g.Claim(ctx, id, n)
	if err != nil {
		e.t.Fatal(err)
	}
	header = credScheme + id + "." + secret
	if p, err = e.g.Authenticate(ctx, header); err != nil {
		e.t.Fatal(err)
	}
	return header, p
}

// S1: a call that started while identifiers were allowed must not return rows after the grant ended.
func TestIdentifiersGrantEndingMidCallWithholdsTheResult(t *testing.T) {
	for _, tc := range []struct {
		name        string
		identifiers bool
		wantResult  bool
	}{{"started with identifiers", true, false}, {"started without identifiers", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			b := &held{enter: make(chan struct{}, 1), release: make(chan struct{})}
			disp, err := NewDispatcher(Deps{Grants: e.g, Backend: b, Recorder: e.db, Now: e.clk.Now})
			if err != nil {
				t.Fatal(err)
			}
			a := appr()
			a.Identifiers, a.IdentifiersFor = tc.identifiers, time.Hour
			header, _ := e.enrolledWith(map[bool]string{true: "ident-yes", false: "ident-no"}[tc.identifiers], a)
			done := make(chan Answer, 1)
			go func() {
				p, _ := e.g.Authenticate(ctx, header)
				_, ans := disp.CallAs(ctx, p, []byte(`{"tool":"audit_get_guide"}`))
				done <- ans
			}()
			<-b.enter
			e.clk.Add(2 * time.Hour) // past IdentifiersUntil (1 h), well inside the idle expiry (8 h)
			close(b.release)
			select {
			case ans := <-done:
				if tc.wantResult && (!ans.OK || len(ans.Result) == 0) {
					t.Fatalf("a call that used no identifiers was withheld: %+v", ans)
				}
				if !tc.wantResult && (ans.OK || len(ans.Result) != 0 || ans.Error == nil || ans.Error.Code != "permission_required") {
					t.Fatalf("a result that may carry identifiers was delivered after the grant ended: %+v", ans)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no answer")
			}
		})
	}
}

// S2 (M14): the host's deadlineMs can lower the deadline of the operation, never raise it above audit.OperationTimeout.
func TestDeadlineIsCappedAtTheOperationTimeout(t *testing.T) {
	e := newEnv(t)
	b := &held{}
	disp, err := NewDispatcher(Deps{Grants: e.g, Backend: b, Recorder: e.db, Now: e.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	header, _ := e.enrolled("deadline-1")
	run := func(body string) time.Duration {
		p, _ := e.g.Authenticate(ctx, header)
		before := time.Now()
		if _, a := disp.CallAs(ctx, p, []byte(body)); !a.OK {
			t.Fatalf("%s: %+v", body, a)
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		if !b.hasDl {
			t.Fatal("the backend got no deadline")
		}
		return b.deadline.Sub(before)
	}
	slack := 30 * time.Second
	if d := run(`{"tool":"audit_get_guide","deadlineMs":86400000}`); d > audit.OperationTimeout+time.Second {
		t.Errorf("a day-long deadlineMs gave %v, above the operation timeout %v", d, audit.OperationTimeout)
	} else if d < audit.OperationTimeout-slack {
		t.Errorf("the cap is too low: %v", d)
	}
	if d := run(`{"tool":"audit_get_guide"}`); d > audit.OperationTimeout+time.Second || d < audit.OperationTimeout-slack {
		t.Errorf("no deadlineMs gave %v", d)
	}
	if d := run(`{"tool":"audit_get_guide","deadlineMs":2000}`); d > 3*time.Second {
		t.Errorf("a short deadlineMs was not honoured: %v", d)
	}
}

// S2 (M16): a tool name outside the catalog is stored as "unknown", never as the client's text.
func TestUnknownToolIsStoredAsUnknown(t *testing.T) {
	r := newRig(t)
	h := r.scoped("unk", TierAggregate, "demo-west")
	const clientText = "zz_client_chosen_name_that_must_not_be_stored"
	a := r.call(h, clientText, map[string]any{})
	if a.Status != 403 || strings.Contains(string(a.Raw), clientText) {
		t.Fatalf("%d %s", a.Status, a.Raw)
	}
	seen := false
	for _, ev := range r.events() {
		if strings.Contains(ev.Tool, "client_chosen") {
			t.Errorf("the client's tool text is in the ledger: %+v", ev)
		}
		if ev.Kind == "denied" && ev.Tool == "unknown" {
			seen = true
		}
	}
	if !seen {
		t.Error(`no denied row with tool "unknown"`)
	}
	r.restart()
	if strings.Contains(string(allBytes(t, r.dir)), clientText) {
		t.Error("the client's tool text is in the store file")
	}
}
