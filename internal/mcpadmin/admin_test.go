package mcpadmin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcpadmin"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/mcpsrv"
	"api-audit-explorer/internal/server"
)

// BL-347 tests, on the real path: the grant store is a real SQLite file, the screen's routes are the real routes of the dashboard
// handler (token, Origin and method rules included) over the demo repository, and the host side is the real /mcp/v1 handler over
// real HTTP. Only the sources are demo data.

const (
	token  = "tok-0123456789abcdef0123456789abcdef"
	nonceA = "AAAAAAAAAAAAAAAAAAAAAA"
	nonceB = "BBBBBBBBBBBBBBBBBBBBBB"
)

type seal struct{}

func (seal) Protect(b []byte) ([]byte, error) { return append([]byte("T:"), b...), nil }
func (seal) Unprotect(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte("T:")) {
		return nil, errors.New("no")
	}
	return b[2:], nil
}

type rig struct {
	t      *testing.T
	ui     *httptest.Server // the dashboard: /api/mcp/* with the session token
	ag     *httptest.Server // the host's endpoint: /mcp/v1/*
	grants *mcpsrv.Grants
	db     *mcpaudit.DB
	dir    string
	logs   []string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	r := &rig{t: t, dir: filepath.Join(t.TempDir(), "mcp")}
	var err error
	if r.db, _, err = mcpaudit.Open(ctx, r.dir, seal{}, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Close() })
	if r.grants, err = mcpsrv.NewGrants(ctx, r.db, nil); err != nil {
		t.Fatal(err)
	}
	mount := server.NewMCPMount()
	dash := server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithMCPMount(mount))
	admin, err := mcpadmin.Handler(mcpadmin.Deps{Grants: r.grants, Logf: func(f string, a ...any) { r.logs = append(r.logs, f) }})
	if err != nil {
		t.Fatal(err)
	}
	mount.Set(admin)
	r.ui = httptest.NewServer(dash)
	t.Cleanup(r.ui.Close)
	h, err := mcpsrv.Handler(mcpsrv.Deps{Grants: r.grants, Backend: mcpsrv.NewLocalBackend(dash, token), Recorder: r.db})
	if err != nil {
		t.Fatal(err)
	}
	r.ag = httptest.NewServer(h)
	t.Cleanup(r.ag.Close)
	return r
}

type reply struct {
	code int
	body []byte
}

func (p reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(p.body, &m); err != nil {
		t.Fatalf("not json: %s", p.body)
	}
	return m
}

func (r *rig) do(base, method, path string, headers map[string]string, body any) reply {
	r.t.Helper()
	var rd io.Reader
	if s, ok := body.(string); ok {
		rd = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return reply{res.StatusCode, b}
}

// screen is what the person's browser does: the session token on every call.
func (r *rig) screen(method, path string, body any) reply {
	return r.do(r.ui.URL, method, path, map[string]string{"X-Audit-Token": token}, body)
}

func (r *rig) host(path string, body any, auth string) reply {
	h := map[string]string{}
	if auth != "" {
		h["Authorization"] = auth
	}
	return r.do(r.ag.URL, http.MethodPost, path, h, body)
}

func (r *rig) enroll(id, name, exe, nonce string) reply {
	return r.host("/mcp/v1/enroll", map[string]any{"profile": id, "hostName": name, "hostVersion": "1.0", "exe": exe, "ppid": 77, "claimNonce": nonce}, "")
}

func (r *rig) profile(id string) map[string]any {
	r.t.Helper()
	res := r.screen("GET", "/api/mcp/profiles", nil)
	if res.code != 200 {
		r.t.Fatalf("list: %d %s", res.code, res.body)
	}
	for _, p := range res.json(r.t)["profiles"].([]any) {
		if m := p.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

func (r *rig) demoSources() []string {
	r.t.Helper()
	res := r.screen("GET", "/api/sources", nil)
	var list []struct{ ID string }
	if res.code != 200 || json.Unmarshal(res.body, &list) != nil || len(list) == 0 {
		r.t.Fatalf("sources: %d %s", res.code, res.body)
	}
	var ids []string
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	return ids
}

func approveBody(rev int, src []string) map[string]any {
	return map[string]any{"expectRev": rev, "tier": "aggregate", "sources": src}
}

func TestRoutesNeedTheSessionToken(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/mcp/profiles"}, {"POST", "/api/mcp/profiles/x/approve"}, {"POST", "/api/mcp/stop"}, {"POST", "/api/mcp/resume"},
	} {
		if res := r.do(r.ui.URL, c.method, c.path, nil, "{}"); res.code != 401 {
			t.Errorf("%s %s without a token: %d", c.method, c.path, res.code)
		}
		if res := r.do(r.ui.URL, c.method, c.path, map[string]string{"X-Audit-Token": "wrong"}, "{}"); res.code != 401 {
			t.Errorf("%s %s with a wrong token: %d", c.method, c.path, res.code)
		}
	}
	// the page of another origin, even with the token, is refused before anything runs
	res := r.do(r.ui.URL, "POST", "/api/mcp/stop", map[string]string{"X-Audit-Token": token, "Origin": "https://evil.example"}, "{}")
	if res.code != 403 || r.grants.Stopped() {
		t.Errorf("foreign origin: %d stopped=%v", res.code, r.grants.Stopped())
	}
	// the stdio bridge holds the token too; it can never decide for itself
	res = r.do(r.ui.URL, "POST", "/api/mcp/profiles/x/approve", map[string]string{"X-Audit-Token": token, "User-Agent": "unravis-mcp"}, approveBody(2, []string{"a"}))
	if res.code != 403 {
		t.Errorf("bridge user agent: %d", res.code)
	}
	// the host's endpoint has none of these routes
	for _, p := range []string{"/api/mcp/profiles", "/api/mcp/stop"} {
		if res := r.do(r.ag.URL, "GET", p, map[string]string{"X-Audit-Token": token}, nil); res.code != 404 {
			t.Errorf("host endpoint serves %s: %d", p, res.code)
		}
	}
	if res := r.screen("POST", "/api/mcp/profiles", "{}"); res.code != 405 {
		t.Errorf("POST on the list: %d", res.code)
	}
}

// The whole person-and-host story over real routes: a host enrols, the screen lists it as waiting, the person approves it with a
// narrower scope, the host collects its credential and calls, a tool outside the tier is refused, then revoke, reissue and stop.
func TestApproveNarrowRevokeReissueStopOnTheRealRoutes(t *testing.T) {
	r := newRig(t)
	src := r.demoSources()
	if got := r.screen("GET", "/api/mcp/profiles", nil).json(t); got["mode"] != "on" || got["stopped"] != false {
		t.Fatalf("list: %v", got)
	}
	res := r.enroll("claude", "Claude Desktop", `C:\Apps\claude.exe`, nonceA)
	if res.code != 200 {
		t.Fatalf("enroll: %d %s", res.code, res.body)
	}
	p := r.profile("claude")
	if p["state"] != "pending" || p["enrolled"] != true || p["hostName"] != "Claude Desktop" || p["exe"] != `C:\Apps\claude.exe` || p["contested"].(float64) != 0 {
		t.Fatalf("pending card: %v", p)
	}
	rev := int(p["rev"].(float64))

	// the host cannot use a pending profile, and a call with no credential is refused
	if res := r.host("/mcp/v1/enroll/claim", map[string]any{"profile": "claude", "claimNonce": nonceA}, ""); res.code != 409 {
		t.Errorf("claim before approval: %d", res.code)
	}

	// an approval without the revision the card showed is refused, so is a stale one, and nothing changes
	if res := r.screen("POST", "/api/mcp/profiles/claude/approve", map[string]any{"tier": "aggregate", "sources": src}); res.code != 400 {
		t.Errorf("approve with no expectRev: %d %s", res.code, res.body)
	}
	if res := r.screen("POST", "/api/mcp/profiles/claude/approve", approveBody(rev+7, src)); res.code != 409 || res.json(t)["code"] != "stale" {
		t.Errorf("approve with a stale rev: %d %s", res.code, res.body)
	}
	if r.profile("claude")["state"] != "pending" {
		t.Fatal("a refused approval changed the profile")
	}
	// narrow: one source, two tools
	body := approveBody(rev, src[:1])
	body["tools"] = []string{"audit_get_guide", "audit_list_sources"}
	res = r.screen("POST", "/api/mcp/profiles/claude/approve", body)
	if res.code != 200 {
		t.Fatalf("approve: %d %s", res.code, res.body)
	}
	p = r.profile("claude")
	if p["state"] != "active" || p["tier"] != "aggregate" || len(p["tools"].([]any)) != 2 || len(p["sources"].([]any)) != 1 || p["identifiers"] != false {
		t.Fatalf("approved card: %v", p)
	}
	if p["claimed"] != false {
		t.Error("claimed before the host collected it")
	}

	// the host collects the credential and calls: only the approved tools and sources
	res = r.host("/mcp/v1/enroll/claim", map[string]any{"profile": "claude", "claimNonce": nonceA}, "")
	if res.code != 200 {
		t.Fatalf("claim: %d %s", res.code, res.body)
	}
	auth := "Unravis-Grant claude." + res.json(t)["secret"].(string)
	if r.profile("claude")["claimed"] != true {
		t.Error("not claimed after the claim")
	}
	res = r.host("/mcp/v1/call", map[string]any{"tool": "audit_list_sources", "args": map[string]any{}}, auth)
	if res.code != 200 || res.json(t)["ok"] != true {
		t.Fatalf("approved call: %d %s", res.code, res.body)
	}
	if strings.Contains(string(res.body), src[len(src)-1]) && len(src) > 1 {
		t.Errorf("a source outside the grant is listed: %s", res.body)
	}
	res = r.host("/mcp/v1/call", map[string]any{"tool": "audit_get_breakdown", "args": map[string]any{}}, auth)
	if res.code != 403 {
		t.Errorf("tool outside the grant: %d %s", res.code, res.body)
	}

	// revoke: the very next call is refused; the person sees it revoked
	if res := r.screen("POST", "/api/mcp/profiles/claude/revoke", map[string]any{"expectRev": 0}); res.code != 200 {
		t.Fatalf("revoke: %d %s", res.code, res.body)
	}
	if res := r.host("/mcp/v1/call", map[string]any{"tool": "audit_list_sources", "args": map[string]any{}}, auth); res.code != 401 {
		t.Errorf("call after revoke: %d", res.code)
	}
	if r.profile("claude")["state"] != "revoked" {
		t.Error("not shown revoked")
	}
	// reissue: pending again, host must enrol again; the old credential stays dead
	if res := r.screen("POST", "/api/mcp/profiles/claude/reissue", map[string]any{}); res.code != 200 {
		t.Fatalf("reissue: %d %s", res.code, res.body)
	}
	if p := r.profile("claude"); p["state"] != "pending" || p["enrolled"] != false {
		t.Errorf("after reissue: %v", p)
	}
	if res := r.host("/mcp/v1/call", map[string]any{"tool": "audit_list_sources", "args": map[string]any{}}, auth); res.code != 401 {
		t.Errorf("old credential after reissue: %d", res.code)
	}
	// approving a host that has not enrolled again is refused
	rev = int(r.profile("claude")["rev"].(float64))
	if res := r.screen("POST", "/api/mcp/profiles/claude/approve", approveBody(rev, src)); res.code != 409 || res.json(t)["code"] != "no_host" {
		t.Errorf("approve before the host enrolled: %d %s", res.code, res.body)
	}
	// remove clears a row that is not active
	if res := r.screen("POST", "/api/mcp/profiles/claude/remove", map[string]any{}); res.code != 200 || r.profile("claude") != nil {
		t.Errorf("remove: %d", res.code)
	}
}

func TestGlobalStopAndResume(t *testing.T) {
	r := newRig(t)
	src := r.demoSources()
	r.enroll("h1", "H", `C:\h.exe`, nonceA)
	rev := int(r.profile("h1")["rev"].(float64))
	r.screen("POST", "/api/mcp/profiles/h1/approve", approveBody(rev, src))
	auth := "Unravis-Grant h1." + r.host("/mcp/v1/enroll/claim", map[string]any{"profile": "h1", "claimNonce": nonceA}, "").json(t)["secret"].(string)
	call := func() int {
		return r.host("/mcp/v1/call", map[string]any{"tool": "audit_list_sources", "args": map[string]any{}}, auth).code
	}
	if call() != 200 {
		t.Fatal("approved host cannot call")
	}
	res := r.screen("POST", "/api/mcp/stop", map[string]any{})
	if res.code != 200 || res.json(t)["stopped"] != true || res.json(t)["saved"] != true {
		t.Fatalf("stop: %d %s", res.code, res.body)
	}
	if call() != 401 {
		t.Error("a call went through while stopped")
	}
	if got := r.screen("GET", "/api/mcp/profiles", nil).json(t); got["stopped"] != true {
		t.Error("the list does not say stopped")
	}
	// the profile keeps its grant: stop is not revoke
	if r.profile("h1")["state"] != "active" {
		t.Error("stop changed the profile")
	}
	if res := r.screen("POST", "/api/mcp/resume", map[string]any{}); res.code != 200 || res.json(t)["stopped"] != false {
		t.Fatalf("resume: %d %s", res.code, res.body)
	}
	if call() != 200 {
		t.Error("a call is refused after resume")
	}
	// the stop is stored: a restart of the owner (a new Grants over the same file) is still stopped
	r.screen("POST", "/api/mcp/stop", map[string]any{})
	g2, err := mcpsrv.NewGrants(context.Background(), r.db, nil)
	if err != nil || !g2.Stopped() {
		t.Errorf("stop did not survive a restart: %v", err)
	}
}

// Review S4 of #260: a second program that registers as the same host leaves a mark, and Approve needs the person's explicit word.
func TestContestedHostNeedsAnExplicitAccept(t *testing.T) {
	r := newRig(t)
	src := r.demoSources()
	r.enroll("cursor", "Cursor", `C:\cursor.exe`, nonceA)
	r.enroll("cursor", "Cursor", `C:\Temp\cursor-helper.exe`, nonceB)
	p := r.profile("cursor")
	if p["contested"].(float64) != 1 || p["exe"] != `C:\cursor.exe` {
		t.Fatalf("contested card: %v", p)
	}
	rev := int(p["rev"].(float64))
	res := r.screen("POST", "/api/mcp/profiles/cursor/approve", approveBody(rev, src))
	if res.code != 409 || res.json(t)["code"] != "contested" || r.profile("cursor")["state"] != "pending" {
		t.Fatalf("approve of a contested host: %d %s", res.code, res.body)
	}
	body := approveBody(rev, src)
	body["acceptContested"] = true
	if res := r.screen("POST", "/api/mcp/profiles/cursor/approve", body); res.code != 200 {
		t.Fatalf("approve with accept: %d %s", res.code, res.body)
	}
	// the other way out: reissue clears the mark
	r.enroll("cursor2", "Cursor", `C:\cursor.exe`, nonceA)
	r.enroll("cursor2", "Cursor", `C:\Temp\x.exe`, nonceB)
	if res := r.screen("POST", "/api/mcp/profiles/cursor2/reissue", map[string]any{}); res.code != 200 || r.profile("cursor2")["contested"].(float64) != 0 {
		t.Errorf("reissue did not clear the mark: %d", res.code)
	}
}

// Two windows: one reissues, the other approves from an old card. Approve, renew (and any action that sends a revision) are refused.
func TestStaleCardIsRefusedForEveryAction(t *testing.T) {
	r := newRig(t)
	src := r.demoSources()
	r.enroll("h1", "H", `C:\h.exe`, nonceA)
	old := int(r.profile("h1")["rev"].(float64))
	r.screen("POST", "/api/mcp/profiles/h1/reissue", map[string]any{})
	if res := r.screen("POST", "/api/mcp/profiles/h1/deny", map[string]any{"expectRev": old}); res.code != 409 {
		t.Errorf("deny from a stale card: %d", res.code)
	}
	r.enroll("h1", "Another", `C:\other.exe`, nonceB)
	if res := r.screen("POST", "/api/mcp/profiles/h1/approve", approveBody(old, src)); res.code != 409 || res.json(t)["code"] != "stale" {
		t.Errorf("approve from a stale card: %d %s", res.code, res.body)
	}
	cur := int(r.profile("h1")["rev"].(float64))
	r.screen("POST", "/api/mcp/profiles/h1/approve", approveBody(cur, src))
	// renew needs the revision
	if res := r.screen("POST", "/api/mcp/profiles/h1/renew", map[string]any{}); res.code != 400 {
		t.Errorf("renew with no rev: %d", res.code)
	}
	active := int(r.profile("h1")["rev"].(float64))
	if res := r.screen("POST", "/api/mcp/profiles/h1/renew", map[string]any{"expectRev": active + 1}); res.code != 409 {
		t.Errorf("renew with a stale rev: %d", res.code)
	}
	if res := r.screen("POST", "/api/mcp/profiles/h1/renew", map[string]any{"expectRev": active}); res.code != 200 {
		t.Errorf("renew: %d", res.code)
	}
}

// Host text arrives as data. Even if it carries markup, the answer is JSON (escaped), never HTML; the store already refuses
// invisible and bidi characters, so the card cannot show a spoofed name.
func TestHostTextIsDataNotMarkup(t *testing.T) {
	r := newRig(t)
	evil := `<img src=x onerror=alert(1)>`
	if res := r.enroll("evil", evil, `C:\a<b>.exe`, nonceA); res.code != 200 {
		t.Fatalf("enroll: %d %s", res.code, res.body)
	}
	res := r.screen("GET", "/api/mcp/profiles", nil)
	if res.code != 200 {
		t.Fatal(res.code)
	}
	if bytes.Contains(res.body, []byte("<img")) {
		t.Errorf("markup sent raw: %s", res.body)
	}
	if r.profile("evil")["hostName"] != evil {
		t.Error("the text was altered instead of carried as data")
	}
	// zero-width and bidi characters never reach the card
	if res := r.enroll("spoof", "Claude\u202eexe.dll", `C:\x.exe`, nonceB); res.code == 200 {
		t.Errorf("a bidi override was accepted: %s", res.body)
	}
	if r.profile("spoof") != nil {
		t.Error("a refused enroll left a row")
	}
}

func TestBadRequestsAreRefusedWithFixedText(t *testing.T) {
	r := newRig(t)
	src := r.demoSources()
	r.enroll("h1", "H", `C:\h.exe`, nonceA)
	rev := int(r.profile("h1")["rev"].(float64))
	cases := map[string]any{
		"unknown field":   map[string]any{"expectRev": rev, "tier": "aggregate", "sources": src, "extra": 1},
		"unknown tier":    map[string]any{"expectRev": rev, "tier": "admin", "sources": src},
		"no sources":      map[string]any{"expectRev": rev, "tier": "aggregate", "sources": []string{}},
		"evidence tool":   map[string]any{"expectRev": rev, "tier": "aggregate", "sources": src, "tools": []string{"audit_get_requests"}},
		"idle too short":  map[string]any{"expectRev": rev, "tier": "aggregate", "sources": src, "idleMinutes": 1},
		"idents too long": map[string]any{"expectRev": rev, "tier": "aggregate", "sources": src, "identifiers": true, "identifiersMinutes": 99999},
		"trailing data":   `{"expectRev":` + "1" + `} {}`,
		"not json":        "nope",
	}
	for name, b := range cases {
		res := r.screen("POST", "/api/mcp/profiles/h1/approve", b)
		if res.code != 400 || strings.Contains(string(res.body), "audit_get_requests") || strings.Contains(string(res.body), "admin") {
			t.Errorf("%s: %d %s", name, res.code, res.body)
		}
	}
	if r.profile("h1")["state"] != "pending" {
		t.Error("a refused approval changed the profile")
	}
	// wrong content type, a bad id, an unknown action, a host that does not exist
	req, _ := http.NewRequest("POST", r.ui.URL+"/api/mcp/profiles/h1/approve", strings.NewReader("{}"))
	req.Header.Set("X-Audit-Token", token)
	req.Header.Set("Content-Type", "text/plain")
	res415, err := http.DefaultClient.Do(req)
	if err != nil || res415.StatusCode != 415 {
		t.Errorf("content type: %v %v", res415, err)
	}
	if res415 != nil {
		_ = res415.Body.Close()
	}
	if res := r.screen("POST", "/api/mcp/profiles/H1!/approve", "{}"); res.code != 404 {
		t.Errorf("bad id: %d", res.code)
	}
	if res := r.screen("POST", "/api/mcp/profiles/h1/promote", "{}"); res.code != 404 {
		t.Errorf("unknown action: %d", res.code)
	}
	if res := r.screen("POST", "/api/mcp/profiles/nobody/revoke", "{}"); res.code != 404 {
		t.Errorf("unknown host: %d", res.code)
	}
	// an active host cannot be removed, and the refusal does not name anything
	r.screen("POST", "/api/mcp/profiles/h1/approve", approveBody(rev, src))
	if res := r.screen("POST", "/api/mcp/profiles/h1/remove", "{}"); res.code != 409 {
		t.Errorf("remove of an active host: %d", res.code)
	}
}

// The list never carries a digest, a secret or a nonce, and the log lines never carry host text.
func TestNoSecretNoDigestNoHostTextInLogs(t *testing.T) {
	r := newRig(t)
	src := r.demoSources()
	r.enroll("h1", "Name With Words", `C:\private-path\h.exe`, nonceA)
	rev := int(r.profile("h1")["rev"].(float64))
	r.screen("POST", "/api/mcp/profiles/h1/approve", approveBody(rev, src))
	sec := r.host("/mcp/v1/enroll/claim", map[string]any{"profile": "h1", "claimNonce": nonceA}, "").json(t)["secret"].(string)
	list := string(r.screen("GET", "/api/mcp/profiles", nil).body)
	for _, bad := range []string{sec, nonceA, "igest", "ecret\""} {
		if strings.Contains(list, bad) {
			t.Errorf("the list contains %q: %s", bad, list)
		}
	}
	for _, l := range r.logs {
		if strings.Contains(l, "Name With Words") || strings.Contains(l, "private-path") {
			t.Errorf("a log line carries host text: %q", l)
		}
	}
	if len(r.logs) == 0 {
		t.Error("an approval left no log line")
	}
}

// Without a grant store the routes exist and say why: legacy for a run that has no grants, off for a store that could not open.
func TestModesWithoutAStore(t *testing.T) {
	for _, c := range []struct {
		name   string
		mount  *server.MCPMount
		mode   string
		reason string
	}{
		{"no mount", nil, "legacy", ""},
		{"unset mount", server.NewMCPMount(), "legacy", ""},
	} {
		var opts []server.Option
		if c.mount != nil {
			opts = append(opts, server.WithMCPMount(c.mount))
		}
		ts := httptest.NewServer(server.New(audit.NewDemo(time.Now()), nil, token, true, opts...))
		req, _ := http.NewRequest("GET", ts.URL+"/api/mcp/profiles", nil)
		req.Header.Set("X-Audit-Token", token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		_ = json.NewDecoder(res.Body).Decode(&got)
		_ = res.Body.Close()
		if res.StatusCode != 200 || got["mode"] != c.mode || got["reason"] != c.reason {
			t.Errorf("%s: %d %v", c.name, res.StatusCode, got)
		}
		req, _ = http.NewRequest("POST", ts.URL+"/api/mcp/stop", strings.NewReader("{}"))
		req.Header.Set("X-Audit-Token", token)
		req.Header.Set("Content-Type", "application/json")
		stop, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if stop.StatusCode != 404 {
			t.Errorf("%s: stop answered %d with no store", c.name, stop.StatusCode)
		}
		_ = stop.Body.Close()
		ts.Close()
	}
	m := server.NewMCPMount()
	m.SetOff(server.MCPOffSealing)
	ts := httptest.NewServer(server.New(audit.NewDemo(time.Now()), nil, token, true, server.WithMCPMount(m)))
	defer ts.Close()
	req, _ := http.NewRequest("GET", ts.URL+"/api/mcp/profiles", nil)
	req.Header.Set("X-Audit-Token", token)
	res, _ := http.DefaultClient.Do(req)
	var got map[string]any
	_ = json.NewDecoder(res.Body).Decode(&got)
	_ = res.Body.Close()
	if got["mode"] != "off" || got["reason"] != server.MCPOffSealing {
		t.Errorf("off: %v", got)
	}
}

// BL-574 M1: the approve body carries `code`; the card shows it; a non-boolean is refused; a second approval without it clears it.
func TestApproveCarriesCodeModeAndNeverInheritsIt(t *testing.T) {
	r := newRig(t)
	src := r.demoSources()
	r.enroll("c1", "C", `C:\c.exe`, nonceA)
	p := r.profile("c1")
	if p["code"] != false {
		t.Fatalf("a pending card must say code=false: %v", p)
	}
	rev := int(p["rev"].(float64))
	if res := r.screen("POST", "/api/mcp/profiles/c1/approve", func() map[string]any { b := approveBody(rev, src); b["code"] = "yes"; return b }()); res.code != 400 {
		t.Errorf("a code that is not a boolean: %d %s", res.code, res.body)
	}
	body := approveBody(rev, src)
	body["code"] = true
	if res := r.screen("POST", "/api/mcp/profiles/c1/approve", body); res.code != 200 {
		t.Fatalf("approve with code: %d %s", res.code, res.body)
	}
	if p := r.profile("c1"); p["state"] != "active" || p["code"] != true {
		t.Fatalf("card after approval with code: %v", p)
	}
	// reissue, enrol again, approve with the defaults: code is off again
	if res := r.screen("POST", "/api/mcp/profiles/c1/reissue", map[string]any{}); res.code != 200 {
		t.Fatalf("reissue: %d", res.code)
	}
	if p := r.profile("c1"); p["code"] != false {
		t.Fatalf("a reissued card kept code: %v", p)
	}
	r.enroll("c1", "C", `C:\c.exe`, nonceB)
	rev = int(r.profile("c1")["rev"].(float64))
	if res := r.screen("POST", "/api/mcp/profiles/c1/approve", approveBody(rev, src)); res.code != 200 {
		t.Fatalf("second approve: %d %s", res.code, res.body)
	}
	if p := r.profile("c1"); p["code"] != false {
		t.Fatalf("the second approval inherited code: %v", p)
	}
}
