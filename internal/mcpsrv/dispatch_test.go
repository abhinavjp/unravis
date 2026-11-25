package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/server"
)

// BL-344 tests. The real path: the HTTP routes of Handler, the grants on a real SQLite file, the tool code of internal/mcp and the
// dashboard handler over the demo repository, all in one process with no network. Only the sources are demo data.

const dashToken = "tok-0123456789abcdef0123456789abcdef"

// dash is the real dashboard handler with a tap: it records every path it is asked for, and can hold a route until released.
type dash struct {
	inner    http.Handler
	mu       sync.Mutex
	paths    []string
	hold     map[string]chan struct{} // path -> closed to release
	seen     chan string              // path of a held request that has arrived
	ctxEnded atomic.Int32             // held requests whose context ended
}

func newDash(now time.Time) *dash {
	return &dash{inner: server.New(audit.NewDemo(now), nil, dashToken, true), hold: map[string]chan struct{}{}, seen: make(chan string, 64)}
}

func (d *dash) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.paths = append(d.paths, r.URL.Path)
	ch := d.hold[r.URL.Path]
	d.mu.Unlock()
	if ch != nil {
		d.seen <- r.URL.Path
		select {
		case <-ch:
		case <-r.Context().Done():
			d.ctxEnded.Add(1)
			return
		}
	}
	d.inner.ServeHTTP(w, r)
}

func (d *dash) holdPath(p string) chan struct{} {
	ch := make(chan struct{})
	d.mu.Lock()
	d.hold[p] = ch
	d.mu.Unlock()
	return ch
}

func (d *dash) hits() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.paths...)
}

// ledger wraps the real store and can be told to fail.
type ledger struct {
	*mcpaudit.DB
	failAdmit atomic.Bool
}

func (l *ledger) Record(ctx context.Context, e mcpaudit.Event) error {
	if l.failAdmit.Load() && e.Kind == mcpaudit.KindAdmit {
		return errors.New("disk full")
	}
	return l.DB.Record(ctx, e)
}

type rig struct {
	*env
	t       *testing.T
	dash    *dash
	led     *ledger
	srv     *httptest.Server
	backend *LocalBackend
	disp    *Dispatcher
	now     time.Time
}

// newRig has budgets no test run can reach (the sweeps of project_test.go make hundreds of comparisons on one fake clock); the budget
// tests ask for DefaultBudgets.
func newRig(t *testing.T) *rig {
	t.Helper()
	open := DefaultBudgets
	open.CompareRate, open.LiveRate, open.CumulativeBytes = 1_000_000, 1_000_000, 1<<40
	return newRigBudgets(t, open)
}

func newRigBudgets(t *testing.T, b Budgets) *rig {
	t.Helper()
	e := newEnv(t)
	now := time.Now().UTC().Truncate(time.Hour)
	r := &rig{env: e, t: t, dash: newDash(now), now: now}
	r.led = &ledger{DB: e.db}
	r.backend = NewLocalBackend(r.dash, dashToken)
	deps := Deps{Grants: e.g, Backend: r.backend, Recorder: r.led, Now: e.clk.Now, Budgets: b}
	disp, err := NewDispatcher(deps)
	if err != nil {
		t.Fatal(err)
	}
	r.disp = disp
	r.srv = httptest.NewServer(disp.Handler())
	t.Cleanup(r.srv.Close)
	return r
}

// host is a profile that enrolled over HTTP, was approved, and claimed its credential over HTTP.
type host struct {
	id, header string
}

func (r *rig) post(path, header string, body any) (int, []byte) {
	r.t.Helper()
	var b []byte
	switch v := body.(type) {
	case string:
		b = []byte(v)
	default:
		b, _ = json.Marshal(v)
	}
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+path, bytes.NewReader(b))
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	res, err := r.srv.Client().Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (r *rig) enroll(id string, a Approval) host {
	r.t.Helper()
	n := nonce(r.t)
	if st, b := r.post("/mcp/v1/enroll", "", EnrollReq{Profile: id, HostName: "claude-code", HostVersion: "2", Exe: `C:\Unravis\unravis.exe`, PPID: 1, ClaimNonce: n}); st != 200 {
		r.t.Fatalf("enroll %d %s", st, b)
	}
	if _, err := r.g.Approve(ctx, id, a); err != nil {
		r.t.Fatal(err)
	}
	st, b := r.post("/mcp/v1/enroll/claim", "", claimReq{Profile: id, ClaimNonce: n})
	var c struct{ Secret string }
	if st != 200 || json.Unmarshal(b, &c) != nil || c.Secret == "" {
		r.t.Fatalf("claim %d %s", st, b)
	}
	return host{id: id, header: credScheme + id + "." + c.Secret}
}

func (r *rig) approval(tier string, tools ...string) Approval {
	a := DefaultApproval([]string{"demo-west", "demo-east"})
	a.Tier, a.Tools = tier, tools
	return a
}

type reply struct {
	Status int
	Answer
	Raw []byte
	env map[string]any
}

func (r *rig) call(h host, tool string, args any) reply {
	r.t.Helper()
	return r.callRaw(h.header, map[string]any{"tool": tool, "args": args})
}

func (r *rig) callRaw(header string, body any) reply {
	r.t.Helper()
	st, b := r.post("/mcp/v1/call", header, body)
	out := reply{Status: st, Raw: b}
	_ = json.Unmarshal(b, &out.Answer)
	if len(out.Result) > 0 {
		_ = json.Unmarshal(out.Result, &out.env)
	}
	return out
}

func (r *rig) compareArgs() map[string]any {
	return map[string]any{"query": audit.Query{
		SourceIDs: []string{"demo-west", "demo-east"}, Start: r.now.Add(-time.Hour), End: r.now, BaselineStart: r.now.Add(-25 * time.Hour), BaselineEnd: r.now.Add(-24 * time.Hour),
		Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 60}}
}

func errCode(e map[string]any) string {
	m, _ := e["error"].(map[string]any)
	c, _ := m["code"].(string)
	return c
}

// ---- the real path ----

// A host enrols, is approved, claims its credential and runs tools against the real dashboard handler: the sources, a comparison and a
// page of it. The answer is the envelope of the stdio server (structured equals text), and no dashboard token is on the wire.
func TestRealPathEnrollApproveClaimCall(t *testing.T) {
	r := newRig(t)
	h := r.enroll("claude-code-1", r.approval(TierAggregate))
	list := r.call(h, "audit_list_sources", map[string]any{})
	if list.Status != 200 || !list.OK || list.IsError || list.ProfileRev != 2 || list.env["tool"] != "audit_list_sources" || len(list.env["sources"].([]any)) != 2 {
		t.Fatalf("sources: %d %.400s", list.Status, list.Raw)
	}
	cmp := r.call(h, "audit_compare", r.compareArgs())
	id, _ := cmp.env["resultId"].(string)
	if !cmp.OK || cmp.IsError || len(id) != 32 || cmp.env["schemaVersion"] != float64(1) {
		t.Fatalf("compare: %d %.400s", cmp.Status, cmp.Raw)
	}
	page := r.call(h, "audit_get_groups", map[string]any{"resultId": id, "limit": 2})
	if !page.OK || page.IsError || len(page.env["groups"].([]any)) == 0 || len(page.env["groups"].([]any)) > 2 {
		t.Fatalf("groups: %d %.400s", page.Status, page.Raw)
	}
	if bytes.Contains(append(append(list.Raw, cmp.Raw...), page.Raw...), []byte(dashToken)) {
		t.Error("the dashboard session token is in an answer")
	}
	guide := r.call(h, "audit_get_guide", map[string]any{})
	if !guide.OK || guide.env["guide"] == "" {
		t.Errorf("guide: %.200s", guide.Raw)
	}
	// An error of the tool itself is an answer of the tool (ok, isError), in the same envelope as on stdio.
	bad := r.call(h, "audit_get_groups", map[string]any{"resultId": "00000000000000000000000000000000"})
	if !bad.OK || !bad.IsError || errCode(bad.env) != "result_expired" {
		t.Errorf("expired handle: %d %.300s", bad.Status, bad.Raw)
	}
}

// Every tool reaches the dashboard only through the seven read routes of the allow-list, and a tool outside the grant reaches none.
func TestOnlyTheSevenReadRoutesAreReached(t *testing.T) {
	r := newRig(t)
	h := r.enroll("evidence-1", r.approval(TierEvidence))
	cmp := r.call(h, "audit_compare", r.compareArgs())
	id := cmp.env["resultId"].(string)
	for tool, args := range map[string]map[string]any{
		"audit_get_guide": {}, "audit_list_sources": {}, "audit_get_groups": {"resultId": id},
		"audit_get_timeline": {"resultId": id, "apiKey": "x"}, "audit_compare_timelines": {"resultId": id, "apiKeys": []string{"x"}},
		"audit_get_requests": {"resultId": id, "apiKey": "x"}, "audit_get_slowest": {"resultId": id},
		"audit_get_breakdown": {"resultId": id, "dim": "statusCode"},
		"audit_get_activity":  {"sourceIds": []string{"demo-west"}, "start": r.now.Add(-time.Hour), "end": r.now},
	} {
		if a := r.call(h, tool, args); a.Status != 200 || !a.OK {
			t.Errorf("%s: %d %.200s", tool, a.Status, a.Raw)
		}
	}
	allowed := map[string]bool{"/api/info": true, "/api/sources": true, "/api/analyze": true, "/api/requests": true, "/api/activity": true, "/api/slowest": true, "/api/breakdown": true}
	for _, p := range r.dash.hits() {
		if !allowed[p] {
			t.Errorf("the dashboard was asked for %q", p)
		}
	}
	// The handler offers no way to name another path.
	n := len(r.dash.hits())
	for _, p := range []string{"/api/cases", "/api/connections", "/api/internal/shutdown", "/mcp/v1/other", "/api/info", "/"} {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			req, _ := http.NewRequest(m, r.srv.URL+p, strings.NewReader("{}"))
			req.Header.Set("Authorization", h.header)
			res, err := r.srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s answered %d", m, p, res.StatusCode)
			}
		}
	}
	if len(r.dash.hits()) != n {
		t.Error("a path outside /mcp/v1 reached the dashboard")
	}
}

// ---- who may call ----

func TestNoGrantNoRun(t *testing.T) {
	r := newRig(t)
	h := r.enroll("h1", r.approval(TierAggregate))
	n := len(r.dash.hits())
	for name, header := range map[string]string{
		"none": "", "garbage": "Bearer x", "wrong secret": credScheme + "h1." + strings.Repeat("A", secretLen),
		"unknown id": credScheme + "nobody." + strings.Repeat("A", secretLen), "other scheme": strings.Replace(h.header, "Unravis-Grant", "Basic", 1),
	} {
		a := r.callRaw(header, map[string]any{"tool": "audit_get_guide", "args": map[string]any{}})
		if a.Status != 401 || a.OK || a.Error == nil || a.Error.Code != "permission_required" || len(a.Result) != 0 {
			t.Errorf("%s: %d %s", name, a.Status, a.Raw)
		}
	}
	// A pending profile (enrolled, not approved) is told to open the app.
	nn := nonce(t)
	_, _ = r.g.Enroll(ctx, req("pending-1", nn))
	a := r.callRaw(credScheme+"pending-1."+strings.Repeat("A", secretLen), map[string]any{"tool": "audit_get_guide"})
	if a.Status != 401 || a.Error == nil || !strings.Contains(a.Error.Message, "waiting for approval") || a.Error.Action == "" {
		t.Errorf("pending: %d %s", a.Status, a.Raw)
	}
	if len(r.dash.hits()) != n {
		t.Error("the dashboard was reached without a grant")
	}
}

func TestToolsOutsideTheGrantNeverReachTheBackend(t *testing.T) {
	r := newRig(t)
	h := r.enroll("agg", r.approval(TierAggregate, "audit_get_guide", "audit_compare"))
	n := len(r.dash.hits())
	for _, tool := range []string{"audit_get_requests", "audit_get_slowest", "audit_get_activity", "audit_list_sources", "audit_get_groups", "no_such_tool"} {
		a := r.call(h, tool, map[string]any{})
		if a.Status != 403 || a.OK || a.Error == nil || a.Error.Code != "permission_required" || a.Error.Action == "" {
			t.Errorf("%s: %d %s", tool, a.Status, a.Raw)
		}
		if strings.Contains(string(a.Raw), tool) {
			t.Errorf("the tool name %q is echoed: %s", tool, a.Raw)
		}
	}
	if !r.call(h, "audit_get_guide", map[string]any{}).OK {
		t.Error("an allowed tool was refused")
	}
	if len(r.dash.hits()) != n {
		t.Error("the dashboard was reached by a refused tool")
	}
}

// The identifiers switch of the bridge is the profile's identifiers grant and nothing else.
func TestIdentifierFiltersFollowTheGrant(t *testing.T) {
	r := newRig(t)
	q := r.compareArgs()
	qq := q["query"].(audit.Query)
	qq.UserIDs = []int{424242}
	withFilter := map[string]any{"query": qq}

	without := r.enroll("noid", r.approval(TierAggregate))
	a := r.call(without, "audit_compare", withFilter)
	if !a.OK || !a.IsError || errCode(a.env) != "permission_required" {
		t.Fatalf("identifier filter without the grant: %d %.400s", a.Status, a.Raw)
	}
	if strings.Contains(string(a.Raw), "424242") {
		t.Error("the filter value is echoed")
	}
	ap := r.approval(TierAggregate)
	ap.Identifiers = true
	with := r.enroll("withid", ap)
	if a := r.call(with, "audit_compare", withFilter); !a.OK || a.IsError {
		t.Fatalf("identifier filter with the grant: %d %.400s", a.Status, a.Raw)
	}
	// The grant is timed: after it ends the same call is refused again, with no other change.
	r.clk.Add(2 * time.Hour)
	_, _ = r.g.Authenticate(ctx, with.header) // idle expiry is 8 h: still active
	if a := r.call(with, "audit_compare", withFilter); !a.OK || !a.IsError || errCode(a.env) != "permission_required" {
		t.Fatalf("identifier filter after the grant ended: %d %.400s", a.Status, a.Raw)
	}
}

// A result handle belongs to the profile that made it, and to its revision.
func TestHandlesAreNotSharedBetweenHosts(t *testing.T) {
	r := newRig(t)
	a := r.enroll("host-a", r.approval(TierAggregate))
	b := r.enroll("host-b", r.approval(TierAggregate))
	id := r.call(a, "audit_compare", r.compareArgs()).env["resultId"].(string)
	if p := r.call(a, "audit_get_groups", map[string]any{"resultId": id}); !p.OK || p.IsError {
		t.Fatalf("own handle: %.300s", p.Raw)
	}
	if p := r.call(b, "audit_get_groups", map[string]any{"resultId": id}); !p.OK || !p.IsError || errCode(p.env) != "result_expired" {
		t.Fatalf("another host's handle: %.300s", p.Raw)
	}
}

// ---- the revoke gate and the stop ----

// holdCompare starts a comparison that the dashboard holds, and returns when it is running there.
func (r *rig) holdCompare(h host, callID string) (release chan struct{}, out chan reply) {
	r.t.Helper()
	release = r.dash.holdPath("/api/analyze")
	out = make(chan reply, 1)
	go func() {
		out <- r.callRaw(h.header, map[string]any{"tool": "audit_compare", "args": r.compareArgs(), "callId": callID})
	}()
	select {
	case <-r.dash.seen:
	case <-time.After(5 * time.Second):
		r.t.Fatal("the call never reached the dashboard")
	}
	return release, out
}

func waitReply(t *testing.T, out chan reply) reply {
	t.Helper()
	select {
	case a := <-out:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
		return reply{}
	}
}

func TestRevokeCancelsARunningCallAndNoResultIsSent(t *testing.T) {
	r := newRig(t)
	h := r.enroll("rv", r.approval(TierAggregate))
	_, out := r.holdCompare(h, "11111111-aaaa")
	if _, err := r.g.Revoke(ctx, "rv"); err != nil {
		t.Fatal(err)
	}
	a := waitReply(t, out)
	if a.Status != 401 || a.OK || len(a.Result) != 0 || a.Error == nil || a.Error.Code != "permission_required" || !strings.Contains(a.Error.Message, "revoked") {
		t.Fatalf("revoked in flight: %d %s", a.Status, a.Raw)
	}
	eventually(t, "the dashboard request to end", func() bool { return r.dash.ctxEnded.Load() == 1 })
	// and it stays revoked
	if a := r.call(h, "audit_get_guide", map[string]any{}); a.Status != 401 {
		t.Errorf("after revoke: %d", a.Status)
	}
}

// A backend that ignores its context and finishes after the revoke still cannot deliver: the gate rechecks before the answer.
type deaf struct {
	enter   chan struct{}
	release chan struct{}
}

func (d *deaf) Call(_ context.Context, _ Profile, tool string, _ json.RawMessage, _ time.Time) ([]byte, bool) {
	d.enter <- struct{}{}
	<-d.release
	return mcp.ErrorEnvelope(tool, "internal", "x", ""), false // a result that must never be seen
}
func (d *deaf) Forget(string) {}

func TestRecheckWithholdsTheResultOfADeafBackend(t *testing.T) {
	e := newEnv(t)
	d := &deaf{enter: make(chan struct{}, 1), release: make(chan struct{})}
	disp, err := NewDispatcher(Deps{Grants: e.g, Backend: d, Recorder: e.db, Now: e.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	header, pr := e.enrolled("deaf-1")
	_ = pr
	done := make(chan Answer, 1)
	go func() {
		p, _ := e.g.Authenticate(ctx, header)
		_, a := disp.CallAs(ctx, p, []byte(`{"tool":"audit_get_guide"}`))
		done <- a
	}()
	<-d.enter
	if _, err := e.g.Revoke(ctx, "deaf-1"); err != nil {
		t.Fatal(err)
	}
	close(d.release)
	select {
	case a := <-done:
		if a.OK || len(a.Result) != 0 || a.Error == nil || a.Error.Code != "permission_required" {
			t.Fatalf("a result was delivered after the revoke: %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}
}

// The gate also holds for a change that cancels nothing: the idle expiry passes while a deaf backend works.
func TestRecheckWithholdsTheResultAfterExpiry(t *testing.T) {
	e := newEnv(t)
	d := &deaf{enter: make(chan struct{}, 1), release: make(chan struct{})}
	disp, err := NewDispatcher(Deps{Grants: e.g, Backend: d, Recorder: e.db, Now: e.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	header, _ := e.enrolled("deaf-2")
	done := make(chan Answer, 1)
	go func() {
		p, _ := e.g.Authenticate(ctx, header)
		_, a := disp.CallAs(ctx, p, []byte(`{"tool":"audit_get_guide"}`))
		done <- a
	}()
	<-d.enter
	e.clk.Add(DefaultIdleTTL + time.Hour)
	close(d.release)
	select {
	case a := <-done:
		if a.OK || len(a.Result) != 0 || a.Error == nil || !strings.Contains(a.Error.Message, "expired") {
			t.Fatalf("a result was delivered after the grant expired: %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}
}

func TestReapprovalMidCallWithholdsTheResult(t *testing.T) {
	r := newRig(t)
	h := r.enroll("rev", r.approval(TierAggregate))
	release, out := r.holdCompare(h, "22222222-aaaa")
	// reissue + approve again changes the revision; the old call must not answer
	if _, err := r.g.Reissue(ctx, "rev"); err != nil {
		t.Fatal(err)
	}
	close(release)
	a := waitReply(t, out)
	if a.OK || len(a.Result) != 0 || a.Error == nil || a.Error.Code != "permission_required" {
		t.Fatalf("%d %s", a.Status, a.Raw)
	}
}

func TestGlobalStopCancelsEveryRunningCall(t *testing.T) {
	r := newRig(t)
	a := r.enroll("sa", r.approval(TierAggregate))
	b := r.enroll("sb", r.approval(TierAggregate))
	r.dash.holdPath("/api/analyze")
	outs := make(chan reply, 2)
	for i, h := range []host{a, b} {
		go func() {
			outs <- r.callRaw(h.header, map[string]any{"tool": "audit_compare", "args": r.compareArgs(), "callId": fmt.Sprintf("3333333%d-aaaa", i)})
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-r.dash.seen:
		case <-time.After(5 * time.Second):
			t.Fatal("calls did not arrive")
		}
	}
	if err := r.g.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		x := waitReply(t, outs)
		if x.OK || len(x.Result) != 0 || x.Error == nil || !strings.Contains(x.Error.Message, "stopped") {
			t.Fatalf("in flight at the stop: %d %s", x.Status, x.Raw)
		}
	}
	eventually(t, "both dashboard requests to end", func() bool { return r.dash.ctxEnded.Load() == 2 })
	if x := r.call(a, "audit_get_guide", map[string]any{}); x.Status != 401 || !strings.Contains(x.Error.Message, "stopped") {
		t.Errorf("after stop: %d %s", x.Status, x.Raw)
	}
	if err := r.g.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if x := r.call(a, "audit_get_guide", map[string]any{}); !x.OK {
		t.Errorf("after resume: %d %s", x.Status, x.Raw)
	}
}

// CancelAll (the case lock, the owner's shutdown) cancels a call without changing the profile: the answer is a refusal, not a result.
func TestCancelAllEndsACallWithoutAResult(t *testing.T) {
	r := newRig(t)
	h := r.enroll("ca", r.approval(TierAggregate))
	_, out := r.holdCompare(h, "44444444-aaaa")
	r.g.CancelAll()
	a := waitReply(t, out)
	if a.OK || len(a.Result) != 0 || a.Error == nil || a.Error.Code != "permission_required" {
		t.Fatalf("%d %s", a.Status, a.Raw)
	}
	if x := r.call(h, "audit_get_guide", map[string]any{}); !x.OK {
		t.Errorf("the profile itself must still work: %d %s", x.Status, x.Raw)
	}
}

// A revoke drops the backend's state for the profile (its result handle), and the next call of the same id starts empty.
func TestRevokeDropsTheBackendState(t *testing.T) {
	r := newRig(t)
	h := r.enroll("hd", r.approval(TierAggregate))
	r.call(h, "audit_compare", r.compareArgs())
	lb := r.backend
	has := func() bool { lb.mu.Lock(); defer lb.mu.Unlock(); return lb.bridges["hd"] != nil }
	if !has() {
		t.Fatal("no state to drop")
	}
	if _, err := r.g.Revoke(ctx, "hd"); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Error("the profile's bridge outlived its revoke")
	}
}

// ---- bounds ----

func TestInFlightBoundPerProfile(t *testing.T) {
	r := newRig(t)
	h := r.enroll("bnd", r.approval(TierAggregate))
	other := r.enroll("other", r.approval(TierAggregate))
	release := r.dash.holdPath("/api/sources")
	outs := make(chan reply, MaxInFlight)
	for i := 0; i < MaxInFlight; i++ {
		go func() { outs <- r.call(h, "audit_list_sources", map[string]any{}) }()
	}
	for i := 0; i < MaxInFlight; i++ {
		<-r.dash.seen
	}
	x := r.call(h, "audit_get_guide", map[string]any{})
	if x.Status != 429 || x.OK || x.Error == nil || x.Error.Code != "query_budget_exceeded" {
		t.Fatalf("fifth call: %d %s", x.Status, x.Raw)
	}
	// another host is not held back by this one
	if y := r.call(other, "audit_get_guide", map[string]any{}); !y.OK {
		t.Errorf("another profile: %d %s", y.Status, y.Raw)
	}
	close(release)
	for i := 0; i < MaxInFlight; i++ {
		if a := waitReply(t, outs); !a.OK {
			t.Errorf("held call: %d %s", a.Status, a.Raw)
		}
	}
	if y := r.call(h, "audit_get_guide", map[string]any{}); !y.OK {
		t.Errorf("slot not freed: %d %s", y.Status, y.Raw)
	}
}

func TestCancelByCallIDIsPerProfile(t *testing.T) {
	r := newRig(t)
	h := r.enroll("c1", r.approval(TierAggregate))
	o := r.enroll("c2", r.approval(TierAggregate))
	_, out := r.holdCompare(h, "55555555-aaaa")
	// another host cannot cancel it, a duplicate id is refused
	if st, b := r.post("/mcp/v1/cancel", o.header, map[string]string{"callId": "55555555-aaaa"}); st != 200 || !strings.Contains(string(b), "none") {
		t.Errorf("other profile: %d %s", st, b)
	}
	if d := r.callRaw(h.header, map[string]any{"tool": "audit_get_guide", "callId": "55555555-aaaa"}); d.Status != 400 {
		t.Errorf("duplicate call id: %d %s", d.Status, d.Raw)
	}
	if st, b := r.post("/mcp/v1/cancel", "", map[string]string{"callId": "55555555-aaaa"}); st != 401 {
		t.Errorf("cancel with no credential: %d %s", st, b)
	}
	if st, b := r.post("/mcp/v1/cancel", h.header, map[string]string{"callId": "55555555-aaaa"}); st != 200 || !strings.Contains(string(b), "cancelling") {
		t.Fatalf("cancel: %d %s", st, b)
	}
	a := waitReply(t, out)
	if !a.OK || !a.IsError || errCode(a.env) != "cancelled" {
		t.Errorf("cancelled call: %d %.300s", a.Status, a.Raw)
	}
}

func TestRequestShapeIsStrictAndNeverEchoed(t *testing.T) {
	r := newRig(t)
	h := r.enroll("shape", r.approval(TierAggregate))
	const secretText = "CLIENT-SUPPLIED-TEXT-7"
	for name, body := range map[string]string{
		"unknown field":     `{"tool":"audit_get_guide","args":{},"` + secretText + `":1}`,
		"duplicate key":     `{"tool":"audit_get_guide","tool":"audit_compare"}`,
		"case duplicate":    `{"tool":"audit_get_guide","TOOL":"audit_compare"}`,
		"duplicate in args": `{"tool":"audit_get_groups","args":{"resultId":"a","RESULTID":"b"}}`,
		"trailing data":     `{"tool":"audit_get_guide"} {}`,
		"not json":          secretText,
		"args not object":   `{"tool":"audit_get_guide","args":["` + secretText + `"]}`,
		"bad tool name":     `{"tool":"` + secretText + `"}`,
		"bad call id":       `{"tool":"audit_get_guide","callId":"` + secretText + `"}`,
		"empty":             ``,
	} {
		a := r.callRaw(h.header, body)
		if a.Status != 400 || a.OK || a.Error == nil || a.Error.Code != "invalid_arguments" {
			t.Errorf("%s: %d %s", name, a.Status, a.Raw)
		}
		if strings.Contains(string(a.Raw), secretText) {
			t.Errorf("%s: the request text is echoed: %s", name, a.Raw)
		}
	}
	// over 1 MiB
	big := `{"tool":"audit_get_guide","args":{"x":"` + strings.Repeat("a", MaxRequestBytes) + `"}}`
	if a := r.callRaw(h.header, big); a.Status != 413 {
		t.Errorf("big body: %d", a.Status)
	}
	// a request that says it comes from a page
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+"/mcp/v1/call", strings.NewReader(`{"tool":"audit_get_guide"}`))
	req.Header.Set("Authorization", h.header)
	req.Header.Set("Origin", "https://evil.example")
	res, err := r.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("Origin: %d", res.StatusCode)
	}
}

// ---- the ledger ----

func (r *rig) events() []mcpaudit.Event {
	r.t.Helper()
	rows, err := r.db.RecentEvents(ctx, 100)
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

func TestLedgerRowsAreDigestOnly(t *testing.T) {
	r := newRig(t)
	h := r.enroll("led", r.approval(TierAggregate))
	a := r.call(h, "audit_compare", r.compareArgs())
	if !a.OK {
		t.Fatalf("%s", a.Raw)
	}
	r.call(h, "audit_get_groups", map[string]any{"resultId": "00000000000000000000000000000000"})
	r.call(h, "audit_get_requests", map[string]any{}) // outside the tier: denied
	rows := r.events()
	kinds := map[string]int{}
	var cmpEnd mcpaudit.Event
	for _, row := range rows {
		kinds[row.Kind]++
		if row.Tool == "audit_compare" && row.Kind == "end" {
			cmpEnd = row
		}
	}
	if kinds["admit"] != 2 || kinds["end"] != 2 || kinds["denied"] != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	if cmpEnd.OpClass != "live_compare" || cmpEnd.Outcome != "ok" || cmpEnd.ProfileID != "led" || cmpEnd.Bytes == 0 || len(cmpEnd.Fingerprint) != 32 {
		t.Errorf("end row: %+v", cmpEnd)
	}
	// no argument value, no result text, no secret, in any row, or anywhere in the file
	secret := strings.TrimPrefix(h.header, credScheme+"led.")
	r.restart() // fold the log into the file
	dump := allBytes(t, r.dir)
	// (the source ids of the grant are in the profile row, by design; the call's own arguments are in no row)
	for _, needle := range []string{secret, h.header, dashToken, a.env["resultId"].(string), `"query"`, "bucketSeconds"} {
		if bytes.Contains(dump, []byte(needle)) {
			t.Errorf("%q is in the store", needle)
		}
	}
	for _, row := range r.events() {
		if strings.Contains(fmt.Sprintf("%s|%s|%s|%s|%s|%s", row.ProfileID, row.Kind, row.Tool, row.OpClass, row.CorrID, row.Code), "demo-") {
			t.Errorf("a source id is in a ledger row: %+v", row)
		}
	}
}

// The same arguments give the same fingerprint, other arguments another one.
func TestFingerprintIsAKeyedDigestOfTheArguments(t *testing.T) {
	r := newRig(t)
	h := r.enroll("fp", r.approval(TierAggregate))
	r.call(h, "audit_get_guide", map[string]any{})
	r.call(h, "audit_get_guide", map[string]any{})
	r.call(h, "audit_get_groups", map[string]any{"resultId": "00000000000000000000000000000000"})
	var guide, groups [][]byte
	for _, row := range r.events() {
		if row.Kind != "admit" {
			continue
		}
		if row.Tool == "audit_get_guide" {
			guide = append(guide, row.Fingerprint)
		} else {
			groups = append(groups, row.Fingerprint)
		}
	}
	if len(guide) != 2 || len(groups) != 1 || !bytes.Equal(guide[0], guide[1]) || bytes.Equal(guide[0], groups[0]) {
		t.Errorf("fingerprints: %x %x", guide, groups)
	}
	if bytes.Contains(guide[0], []byte("{}")) {
		t.Error("the fingerprint holds the arguments")
	}
}

func TestLiveWorkFailsClosedWhenTheLedgerCannotWrite(t *testing.T) {
	r := newRig(t)
	h := r.enroll("fc", r.approval(TierAggregate))
	r.led.failAdmit.Store(true)
	n := len(r.dash.hits())
	a := r.call(h, "audit_compare", r.compareArgs())
	if a.Status != 500 || a.OK || len(a.Result) != 0 || a.Error == nil {
		t.Fatalf("live call with no ledger: %d %s", a.Status, a.Raw)
	}
	if len(r.dash.hits()) != n {
		t.Error("the dashboard was reached with no ledger row")
	}
	if g := r.call(h, "audit_get_guide", map[string]any{}); !g.OK {
		t.Errorf("a cheap call goes on without a row: %d %s", g.Status, g.Raw)
	}
}

func TestHelloAndOnlyFiveRoutes(t *testing.T) {
	r := newRig(t)
	res, err := r.srv.Client().Get(r.srv.URL + "/mcp/v1/hello")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), `"v":1`) {
		t.Errorf("hello: %d %s", res.StatusCode, b)
	}
	for _, x := range []struct{ m, p string }{{"POST", "/mcp/v1/hello"}, {"GET", "/mcp/v1/call"}, {"GET", "/mcp/v1/cancel"}, {"GET", "/mcp/v1/enroll"}, {"PUT", "/mcp/v1/call"}} {
		req, _ := http.NewRequest(x.m, r.srv.URL+x.p, nil)
		res, err := r.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 405 && res.StatusCode != 404 {
			t.Errorf("%s %s: %d", x.m, x.p, res.StatusCode)
		}
	}
}

func TestDispatcherNeedsItsParts(t *testing.T) {
	e := newEnv(t)
	for name, d := range map[string]Deps{
		"no grants":   {Backend: NewLocalBackend(newDash(time.Now()), dashToken), Recorder: e.db},
		"no backend":  {Grants: e.g, Recorder: e.db},
		"no recorder": {Grants: e.g, Backend: NewLocalBackend(newDash(time.Now()), dashToken)},
	} {
		if _, err := Handler(d); err == nil {
			t.Errorf("%s: built", name)
		}
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}
