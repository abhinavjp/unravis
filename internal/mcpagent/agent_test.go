package mcpagent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcpadmin"
	"api-audit-explorer/internal/mcpagent"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/mcpsrv"
	"api-audit-explorer/internal/server"
)

// BL-345 (the part that runs without Windows), on the real path: the agent talks HTTP over a real connection to the real /mcp/v1
// handler, over a real SQLite grant store and the real dashboard handler on the demo repository; the person's decisions go through the
// real /api/mcp routes with the session token. Only the transport (TCP instead of the named pipe) and the sealing key differ.

const (
	dashToken = "tok-0123456789abcdef0123456789abcdef"
	ownerPID  = 4242
	ownerGen  = "2026-11-24T08:00:00.123456789Z"
)

type seal struct{ tag string }

func (s seal) Protect(b []byte) ([]byte, error) { return append([]byte("S:"+s.tag+":"), b...), nil }
func (s seal) Unprotect(b []byte) ([]byte, error) {
	p := []byte("S:" + s.tag + ":")
	if !bytes.HasPrefix(b, p) {
		return nil, errors.New("not sealed for this user")
	}
	return b[len(p):], nil
}

type rig struct {
	t      *testing.T
	ui     *httptest.Server
	ag     *httptest.Server
	grants *mcpsrv.Grants
	credDr string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	db, _, err := mcpaudit.Open(ctx, filepath.Join(t.TempDir(), "mcp"), seal{"srv"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	grants, err := mcpsrv.NewGrants(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	mount := server.NewMCPMount()
	dash := server.New(audit.NewDemo(time.Now()), nil, dashToken, true, server.WithMCPMount(mount))
	admin, err := mcpadmin.Handler(mcpadmin.Deps{Grants: grants})
	if err != nil {
		t.Fatal(err)
	}
	mount.Set(admin)
	r := &rig{t: t, grants: grants, credDr: filepath.Join(t.TempDir(), "agent")}
	r.ui = httptest.NewServer(dash)
	t.Cleanup(r.ui.Close)
	h, err := mcpsrv.Handler(mcpsrv.Deps{
		Grants: grants, Backend: mcpsrv.NewLocalBackend(dash, dashToken), Recorder: db,
		Info: func() mcpsrv.Info { return mcpsrv.Info{PID: ownerPID, Gen: ownerGen, AppVersion: "t"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.ag = httptest.NewServer(h)
	t.Cleanup(r.ag.Close)
	return r
}

func (r *rig) agent(profile string) *mcpagent.Agent {
	addr := strings.TrimPrefix(r.ag.URL, "http://")
	return &mcpagent.Agent{
		Dial:    func(ctx context.Context) (net.Conn, error) { var d net.Dialer; return d.DialContext(ctx, "tcp", addr) },
		Locate:  func() (mcpagent.Expect, error) { return mcpagent.Expect{PID: ownerPID, Gen: ownerGen}, nil },
		Store:   mcpagent.Store{Dir: r.credDr, Prot: seal{"user"}},
		Profile: profile, HostName: "Test Host", HostVersion: "1.0", Exe: "/usr/bin/test-host", PPID: 77,
	}
}

// screen is what the person's browser does.
func (r *rig) screen(method, path string, body any) (int, map[string]any) {
	r.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.ui.URL+path, rd)
	req.Header.Set("X-Audit-Token", dashToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func (r *rig) rev(id string) int {
	r.t.Helper()
	_, m := r.screen("GET", "/api/mcp/profiles", nil)
	for _, p := range m["profiles"].([]any) {
		if p := p.(map[string]any); p["id"] == id {
			return int(p["rev"].(float64))
		}
	}
	r.t.Fatalf("no profile %s", id)
	return 0
}

func (r *rig) approve(id string) {
	r.t.Helper()
	req, _ := http.NewRequest("GET", r.ui.URL+"/api/sources", nil)
	req.Header.Set("X-Audit-Token", dashToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	var list []struct{ ID string }
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	var ids []string
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	code, m := r.screen("POST", "/api/mcp/profiles/"+id+"/approve", map[string]any{"expectRev": r.rev(id), "tier": "aggregate", "sources": ids})
	if code != 200 {
		r.t.Fatalf("approve: %d %v", code, m)
	}
}

type env struct {
	body    []byte
	isError bool
	m       map[string]any
}

func call(t *testing.T, a *mcpagent.Agent, tool string) env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, isErr := a.CallEnvelope(ctx, tool, json.RawMessage(`{}`))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not an envelope: %.200s", b)
	}
	return env{b, isErr, m}
}

func code(e env) string {
	if x, ok := e.m["error"].(map[string]any); ok {
		c, _ := x["code"].(string)
		return c
	}
	return ""
}

func TestEnrollWaitApproveClaimCallOnTheRealBackend(t *testing.T) {
	r := newRig(t)
	a := r.agent("agent-1")

	e := call(t, a, "audit_list_sources")
	if !e.isError || code(e) != "permission_required" || !strings.Contains(string(e.body), "waiting for approval") {
		t.Fatalf("before approval: %.300s", e.body)
	}
	// the host is on the person's screen, enrolled, with what it reported about itself
	_, list := r.screen("GET", "/api/mcp/profiles", nil)
	ps := list["profiles"].([]any)
	if len(ps) != 1 || ps[0].(map[string]any)["state"] != "pending" || ps[0].(map[string]any)["hostName"] != "Test Host" || ps[0].(map[string]any)["enrolled"] != true {
		t.Fatalf("the screen's list: %v", list)
	}
	// still waiting: the same nonce is kept, not a new one each call (that would mark the host contested)
	_ = call(t, a, "audit_list_sources")
	if _, m := r.screen("GET", "/api/mcp/profiles", nil); m["profiles"].([]any)[0].(map[string]any)["contested"] != float64(0) {
		t.Errorf("a waiting host made itself contested: %v", m)
	}

	r.approve("agent-1")
	e = call(t, a, "audit_list_sources")
	if e.isError || e.m["tool"] != "audit_list_sources" || len(e.m["sources"].([]any)) == 0 {
		t.Fatalf("after approval: %.300s", e.body)
	}
	// a tool outside the grant comes back as the backend's refusal in the same envelope shape
	if e := call(t, a, "audit_get_requests"); !e.isError || code(e) != "permission_required" {
		t.Errorf("tool outside the tier: %.300s", e.body)
	}
	// a second run of the host (new process, same folder) uses the stored credential and does not enroll again
	b := r.agent("agent-1")
	if e := call(t, b, "audit_get_guide"); e.isError {
		t.Errorf("restart: %.300s", e.body)
	}
	if _, m := r.screen("GET", "/api/mcp/profiles", nil); m["profiles"].([]any)[0].(map[string]any)["claimed"] != true {
		t.Errorf("claimed flag: %v", m)
	}
}

func TestCredentialFileIsSealedAndPrivate(t *testing.T) {
	r := newRig(t)
	a := r.agent("agent-2")
	_ = call(t, a, "audit_get_guide")
	path := filepath.Join(r.credDr, "bridge-agent-2.cred")
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(raw, []byte("S:user:")) {
		t.Fatalf("pending file: %v %.30s", err, raw)
	}
	r.approve("agent-2")
	_ = call(t, a, "audit_get_guide")
	raw, _ = os.ReadFile(path)
	if !bytes.HasPrefix(raw, []byte("S:user:")) {
		t.Fatal("the credential is not sealed")
	}
	st := mcpagent.Store{Dir: r.credDr, Prot: seal{"user"}}
	c, err := st.Load("agent-2")
	if err != nil || len(c.Secret) != 43 || c.Nonce != "" {
		t.Fatalf("load: %v %+v", err, c)
	}
	// the plain secret is nowhere in the file or in the folder
	entries, _ := os.ReadDir(r.credDr)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(r.credDr, e.Name()))
		if strings.Contains(string(b), c.Secret) && !bytes.HasPrefix(b, []byte("S:user:")) {
			t.Errorf("%s holds the secret unsealed", e.Name())
		}
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode %v", fi.Mode().Perm())
		}
		if fi, _ := os.Stat(r.credDr); fi.Mode().Perm() != 0o700 {
			t.Errorf("folder mode %v", fi.Mode().Perm())
		}
	}
	// a copy opened by another user does not unseal: it is dropped and the host enrolls again
	other := mcpagent.Store{Dir: r.credDr, Prot: seal{"other"}}
	if _, err := other.Load("agent-2"); !errors.Is(err, mcpagent.ErrNoCred) {
		t.Errorf("other user: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("an unreadable credential was kept")
	}
	// no protector, no file
	if err := (mcpagent.Store{Dir: r.credDr}).Save(mcpagent.Cred{V: 1, Profile: "x", Secret: strings.Repeat("A", 43)}); err == nil {
		t.Error("saved without a protector")
	}
}

func TestReissueMakesTheHostEnrollAgain(t *testing.T) {
	r := newRig(t)
	a := r.agent("agent-3")
	_ = call(t, a, "audit_get_guide")
	r.approve("agent-3")
	if e := call(t, a, "audit_get_guide"); e.isError {
		t.Fatalf("first run: %.200s", e.body)
	}
	if c, m := r.screen("POST", "/api/mcp/profiles/agent-3/reissue", map[string]any{}); c != 200 {
		t.Fatalf("reissue: %d %v", c, m)
	}
	// the old secret is dead; the host is told it waits, and enrolls again
	e := call(t, a, "audit_get_guide")
	if !e.isError || code(e) != "permission_required" {
		t.Fatalf("after reissue: %.300s", e.body)
	}
	e = call(t, a, "audit_get_guide")
	if !strings.Contains(string(e.body), "waiting for approval") {
		t.Fatalf("second call after reissue: %.300s", e.body)
	}
	r.approve("agent-3")
	if e := call(t, a, "audit_get_guide"); e.isError {
		t.Fatalf("after the new approval: %.300s", e.body)
	}
}

func TestRevokeStopAndExpiryReachTheHostAsRefusals(t *testing.T) {
	r := newRig(t)
	a := r.agent("agent-4")
	_ = call(t, a, "audit_get_guide")
	r.approve("agent-4")
	_ = call(t, a, "audit_get_guide")
	if c, _ := r.screen("POST", "/api/mcp/stop", map[string]any{}); c != 200 {
		t.Fatal("stop")
	}
	if e := call(t, a, "audit_get_guide"); !e.isError || code(e) != "permission_required" || !strings.Contains(string(e.body), "stopped") {
		t.Errorf("stopped: %.300s", e.body)
	}
	r.screen("POST", "/api/mcp/resume", map[string]any{})
	if e := call(t, a, "audit_get_guide"); e.isError {
		t.Errorf("resumed: %.300s", e.body)
	}
	r.screen("POST", "/api/mcp/profiles/agent-4/revoke", map[string]any{})
	e := call(t, a, "audit_get_guide")
	if !e.isError || code(e) != "permission_required" || !strings.Contains(string(e.body), "revoked") {
		t.Errorf("revoked: %.300s", e.body)
	}
	// a revoked host keeps its (dead) credential: it does not enroll itself back in
	if _, err := (mcpagent.Store{Dir: r.credDr, Prot: seal{"user"}}).Load("agent-4"); err != nil {
		t.Errorf("credential dropped on revoke: %v", err)
	}
}

// ---- a peer that is not the owner never sees a credential ----

// recorder is a fake peer: it answers hello with the given pid and gen, and keeps every byte it was sent.
type recorder struct {
	ln    net.Listener
	got   atomic.Pointer[[]byte]
	calls atomic.Int32
	pid   int
	gen   string
	// dieOnCall closes the connection after reading the call.
	dieOnCall bool
}

func newRecorder(t *testing.T, pid int, gen string) *recorder {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rc := &recorder{ln: ln, pid: pid, gen: gen}
	empty := []byte{}
	rc.got.Store(&empty)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go rc.serve(c)
		}
	}()
	return rc
}

func (rc *recorder) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	tee := &teeConn{Conn: c, rc: rc}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/mcp/v1/hello":
			_ = json.NewEncoder(w).Encode(map[string]any{"v": 1, "pid": rc.pid, "gen": rc.gen})
		case "/mcp/v1/call":
			rc.calls.Add(1)
			if rc.dieOnCall {
				hj, _ := w.(http.Hijacker)
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"schemaVersion": 1, "tool": "audit_get_guide"}})
		default:
			http.NotFound(w, r)
		}
	})}
	ln := &oneConn{c: tee}
	_ = srv.Serve(ln)
}

type teeConn struct {
	net.Conn
	rc *recorder
}

func (t *teeConn) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)
	old := *t.rc.got.Load()
	nb := append(append([]byte{}, old...), p[:n]...)
	t.rc.got.Store(&nb)
	return n, err
}

type oneConn struct {
	c    net.Conn
	done bool
}

func (o *oneConn) Accept() (net.Conn, error) {
	if o.done {
		time.Sleep(time.Hour)
	}
	o.done = true
	return o.c, nil
}
func (o *oneConn) Close() error   { return nil }
func (o *oneConn) Addr() net.Addr { return o.c.LocalAddr() }

func agentFor(t *testing.T, rc *recorder, want mcpagent.Expect, dir string) *mcpagent.Agent {
	return &mcpagent.Agent{
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", rc.ln.Addr().String())
		},
		Locate:  func() (mcpagent.Expect, error) { return want, nil },
		Store:   mcpagent.Store{Dir: dir, Prot: seal{"user"}},
		Profile: "agent-5", HostName: "H", HostVersion: "1", Exe: "/x", PPID: 1,
	}
}

func TestNoSecretNorNonceNorArgumentsBeforeHelloPasses(t *testing.T) {
	want := mcpagent.Expect{PID: ownerPID, Gen: ownerGen}
	for name, c := range map[string]struct {
		pid int
		gen string
	}{
		"wrong pid":     {ownerPID + 1, ownerGen},
		"stale start":   {ownerPID, "2026-11-23T08:00:00Z"},
		"no generation": {ownerPID, ""},
		"both wrong":    {1, "x"},
	} {
		t.Run(name, func(t *testing.T) {
			rc := newRecorder(t, c.pid, c.gen)
			dir := t.TempDir()
			a := agentFor(t, rc, want, dir)
			e := call(t, a, "audit_get_guide")
			if !e.isError || code(e) != "backend_unavailable" {
				t.Fatalf("answer: %.300s", e.body)
			}
			got := string(*rc.got.Load())
			for _, bad := range []string{"claimNonce", "Unravis-Grant", "/mcp/v1/enroll", "/mcp/v1/call", "audit_get_guide"} {
				if strings.Contains(got, bad) {
					t.Errorf("the peer was sent %q", bad)
				}
			}
			if !strings.Contains(got, "GET /mcp/v1/hello") {
				t.Errorf("no hello was sent: %q", got)
			}
			// nothing was written either: a wrong peer does not make the host hold a nonce
			if es, _ := os.ReadDir(dir); len(es) != 0 {
				t.Errorf("files written for a wrong peer: %v", es)
			}
		})
	}
}

func TestNoOwnerAnswersWithoutDialling(t *testing.T) {
	var dialled atomic.Bool
	a := &mcpagent.Agent{
		Dial:    func(context.Context) (net.Conn, error) { dialled.Store(true); return nil, errors.New("no") },
		Locate:  func() (mcpagent.Expect, error) { return mcpagent.Expect{}, errors.New("no owner") },
		Store:   mcpagent.Store{Dir: t.TempDir(), Prot: seal{"user"}},
		Profile: "agent-6",
	}
	e := call(t, a, "audit_get_guide")
	if !e.isError || code(e) != "backend_unavailable" || !strings.Contains(string(e.body), "Start Unravis") || dialled.Load() {
		t.Errorf("%.300s dialled=%v", e.body, dialled.Load())
	}
	// an unknown tool and bad arguments are refused before any of that
	if b, isErr := a.CallEnvelope(context.Background(), "rm_rf", nil); !isErr || !strings.Contains(string(b), "invalid_arguments") {
		t.Errorf("unknown tool: %.200s", b)
	}
	if b, isErr := a.CallEnvelope(context.Background(), "audit_get_guide", json.RawMessage(`{"a":1,"A":2}`)); !isErr || !strings.Contains(string(b), "invalid_arguments") {
		t.Errorf("duplicate keys: %.200s", b)
	}
}

func TestACallThatWasSentIsNotRepeated(t *testing.T) {
	want := mcpagent.Expect{PID: ownerPID, Gen: ownerGen}
	rc := newRecorder(t, ownerPID, ownerGen)
	dir := t.TempDir()
	st := mcpagent.Store{Dir: dir, Prot: seal{"user"}}
	if err := st.Save(mcpagent.Cred{V: 1, Profile: "agent-5", Secret: strings.Repeat("A", 43)}); err != nil {
		t.Fatal(err)
	}
	rc.dieOnCall = true
	a := agentFor(t, rc, want, dir)
	e := call(t, a, "audit_get_guide")
	if !e.isError || code(e) != "backend_unavailable" || !strings.Contains(string(e.body), "may have run") {
		t.Fatalf("answer: %.300s", e.body)
	}
	if n := rc.calls.Load(); n != 1 {
		t.Errorf("the backend saw %d calls", n)
	}
}

func TestACancelledCallStopsWaiting(t *testing.T) {
	want := mcpagent.Expect{PID: ownerPID, Gen: ownerGen}
	// a peer that answers hello and then never answers the call
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/mcp/v1/hello" {
				_ = json.NewEncoder(w).Encode(map[string]any{"v": 1, "pid": ownerPID, "gen": ownerGen})
				return
			}
			<-r.Context().Done()
		})}
		_ = srv.Serve(&oneConn{c: c})
	}()
	dir := t.TempDir()
	st := mcpagent.Store{Dir: dir, Prot: seal{"user"}}
	_ = st.Save(mcpagent.Cred{V: 1, Profile: "agent-5", Secret: strings.Repeat("A", 43)})
	a := &mcpagent.Agent{
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", ln.Addr().String())
		},
		Locate: func() (mcpagent.Expect, error) { return want, nil }, Store: st, Profile: "agent-5",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []byte, 1)
	go func() { b, _ := a.CallEnvelope(ctx, "audit_get_guide", nil); done <- b }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case b := <-done:
		if !strings.Contains(string(b), "backend_unavailable") {
			t.Errorf("answer: %.200s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call kept waiting after cancel")
	}
}

func TestNothingOfTheSecretInAnyAnswer(t *testing.T) {
	r := newRig(t)
	a := r.agent("agent-7")
	var all [][]byte
	for i := 0; i < 2; i++ {
		b, _ := a.CallEnvelope(context.Background(), "audit_get_guide", nil)
		all = append(all, b)
	}
	r.approve("agent-7")
	b, _ := a.CallEnvelope(context.Background(), "audit_get_guide", nil)
	all = append(all, b)
	c, err := (mcpagent.Store{Dir: r.credDr, Prot: seal{"user"}}).Load("agent-7")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range all {
		if bytes.Contains(b, []byte(c.Secret)) || bytes.Contains(b, []byte(dashToken)) || bytes.Contains(b, []byte(r.credDr)) {
			t.Errorf("an answer holds a secret or a path: %.300s", b)
		}
	}
}
