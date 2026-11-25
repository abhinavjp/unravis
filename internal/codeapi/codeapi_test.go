package codeapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/codemode/privacy"
	"api-audit-explorer/internal/codemode/settingsfile"
	"api-audit-explorer/internal/hosts"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/server"
)

// These tests drive the real server handler (token, host and Origin checks, the static route table) with the real Broker and the real
// codeapi handlers. The store is a real sealed SQLite case store unless a test says it uses the in-memory one. There is no JavaScript
// worker in this slice, so the Spawner is either nil, or a fake that cannot start a worker (the run is then recorded as failed).

const testToken = "0123456789abcdef0123456789abcdef"

const hostsDoc = `{"version":1,"hosts":[{"label":"web-a","aliases":["web-a.example.test"],"role":"iis","tz":"UTC"}]}`

var demoNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// spawn is a Spawner with no worker. Start counts itself, can wait on gate, and then fails.
type spawn struct {
	avail   error
	gate    chan struct{} // when set, Start waits until it is closed or the run's context ends
	entered chan struct{} // when set, Start sends on it (non-blocking) as it begins
	starts  atomic.Int32
}

func (s *spawn) Available() error { return s.avail }

func (s *spawn) Start(ctx context.Context, _ codemode.SpawnOptions) (codemode.Worker, error) {
	s.starts.Add(1)
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
		}
	}
	return nil, errors.New("no worker in this build")
}

type rigOpts struct {
	store      casestore.Store
	spawner    codemode.Spawner
	noInstall  bool // build the server with a mount that is never Set
	noMount    bool // build the server without any mount
	allowIdent bool
	settingsIn string // folder of code-mode.json; default is a fresh temp folder
}

type rig struct {
	t      *testing.T
	h      http.Handler
	store  casestore.Store
	mount  *server.CodeMount
	files  *settingsfile.Store
	broker *codemode.Broker
	ctx    context.Context
}

func sealedStore(t *testing.T) casestore.Store {
	t.Helper()
	s, err := sqlite.New(sqlite.Options{Root: filepath.Join(t.TempDir(), "cases"), Protector: storetest.RandomProtector(), MinFreeBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newRig(t *testing.T, o rigOpts) *rig {
	t.Helper()
	if o.store == nil {
		o.store = sealedStore(t)
	}
	hostMap, err := hosts.Load(strings.NewReader(hostsDoc))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, store: o.store, mount: server.NewCodeMount(), ctx: context.Background()}
	opts := []server.Option{server.WithCases(o.store, hostMap, importer.NewRegistry())}
	if !o.noMount {
		opts = append(opts, server.WithCodeMount(r.mount))
	}
	r.h = server.New(audit.NewDemo(demoNow), nil, testToken, true, opts...)
	dir := o.settingsIn
	if dir == "" {
		dir = t.TempDir()
	}
	r.files = settingsfile.New(dir)
	b, err := codemode.New(codemode.Config{
		Spawner:          o.spawner,
		Hosts:            codemode.DashboardHosts(r.h, testToken),
		Cases:            Resolver(r.mount),
		Settings:         r.files.Settings,
		AllowIdentifiers: o.allowIdent,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.broker = b
	if !o.noInstall && !o.noMount {
		Install(Options{Mount: r.mount, Broker: b, Settings: r.files, AllowIdentifiers: o.allowIdent})
	}
	return r
}

// call sends one request as the owner's page would: the session token, a loopback host and a JSON body.
func (r *rig) call(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	r.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	res := httptest.NewRecorder()
	r.h.ServeHTTP(res, req)
	return res
}

func (r *rig) enable() {
	r.t.Helper()
	if res := r.call(http.MethodPut, "/api/code/settings", `{"enabled":true}`); res.Code != http.StatusOK {
		r.t.Fatalf("enable = %d %s", res.Code, res.Body)
	}
}

func (r *rig) newCase(title string) string {
	r.t.Helper()
	info, err := r.store.Create(r.ctx, title)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(info.ID)
}

func (r *rig) closeCase(id string) {
	r.t.Helper()
	if res := r.call(http.MethodPost, "/api/cases/"+id+"/close", "", "X-Expect-State", "open"); res.Code != http.StatusOK {
		r.t.Fatalf("close case = %d %s", res.Code, res.Body)
	}
}

// runsOf lists the runs recorded in a case, through a handle of the test's own.
func (r *rig) runsOf(id string) []casestore.CodeRun {
	r.t.Helper()
	c, err := r.store.Open(r.ctx, casestore.CaseID(id))
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	runs, err := c.(casestore.CodeRunStore).ListRuns(r.ctx, casestore.RunQuery{})
	if err != nil {
		r.t.Fatal(err)
	}
	return runs
}

func wantProblem(t *testing.T, res *httptest.ResponseRecorder, status int, code string) problem {
	t.Helper()
	var p problem
	if err := json.Unmarshal(res.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem body (%d): %q", res.Code, res.Body.String())
	}
	if res.Code != status || p.Code != code {
		t.Fatalf("got %d %q (%s), want %d %q", res.Code, p.Code, p.Message, status, code)
	}
	if p.Message == "" {
		t.Fatalf("%q has no message", code)
	}
	return p
}

func runBodyOf(code string) string {
	b, _ := json.Marshal(runBody{Code: code, Reason: "unit test"})
	return string(b)
}

var allRoutes = []struct{ method, path string }{
	{http.MethodGet, "/api/code/types"},
	{http.MethodGet, "/api/code/settings"},
	{http.MethodPut, "/api/code/settings"},
	{http.MethodPost, "/api/code/run"},
	{http.MethodPost, "/api/code/runs/abc123/cancel"},
}

/* ------------------------------------------------------------- the gate */

func TestRoutesNeedTheSessionTokenAndALocalOrigin(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}})
	r.enable()
	for _, rt := range allRoutes {
		body := ""
		if rt.method != http.MethodGet && !strings.HasSuffix(rt.path, "/cancel") {
			body = `{"enabled":true}`
			if rt.path == "/api/code/run" {
				body = runBodyOf("1")
			}
		}
		// No token and a wrong token: 401, before the handler sees anything.
		for _, tok := range []string{"", "wrong-token"} {
			res := r.call(rt.method, rt.path, body, "X-Audit-Token", tok)
			if res.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with token %q = %d, want 401", rt.method, rt.path, tok, res.Code)
			}
		}
		// A page of another origin (a browser sends Origin) is refused even with the right token.
		if res := r.call(rt.method, rt.path, body, "Origin", "http://evil.example"); res.Code != http.StatusForbidden {
			t.Errorf("%s %s from a foreign origin = %d, want 403", rt.method, rt.path, res.Code)
		}
		// A request for another host name (DNS rebinding) is refused.
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(body))
		req.Host = "evil.example"
		req.Header.Set("X-Audit-Token", testToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		r.h.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Errorf("%s %s for a foreign host = %d, want 403", rt.method, rt.path, res.Code)
		}
	}
	// None of that started a run or touched the settings.
	set, err := r.files.Load()
	if err != nil || !set.Enabled {
		t.Fatalf("settings changed by refused requests: %+v %v", set, err)
	}
}

func TestMCPBridgeIsRefusedOnEveryCodeRoute(t *testing.T) {
	sp := &spawn{}
	r := newRig(t, rigOpts{spawner: sp})
	r.enable()
	r.newCase("Bridge case")
	for _, ua := range []string{"unravis-mcp", "unravis-mcp-agent", "unravis-mcp/2.0 (future)"} {
		for _, rt := range allRoutes {
			body := ""
			if rt.method == http.MethodPut {
				body = `{"enabled":false}`
			} else if rt.path == "/api/code/run" {
				body = runBodyOf("1")
			}
			wantProblem(t, r.call(rt.method, rt.path, body, "User-Agent", ua), http.StatusForbidden, "denied")
		}
	}
	if sp.starts.Load() != 0 {
		t.Fatal("a bridge request started a worker")
	}
	if set, _ := r.files.Load(); !set.Enabled {
		t.Fatal("a bridge request changed the settings")
	}
}

func TestRoutesAnswer503WhenNoBrokerIsMounted(t *testing.T) {
	for name, o := range map[string]rigOpts{"mount never set": {noInstall: true}, "no mount at all": {noMount: true}} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, o)
			for _, rt := range allRoutes {
				body := ""
				if rt.method == http.MethodPut {
					body = `{"enabled":true}`
				} else if rt.path == "/api/code/run" {
					body = runBodyOf("1")
				}
				wantProblem(t, r.call(rt.method, rt.path, body), http.StatusServiceUnavailable, "code_mode_unavailable")
			}
			if _, err := os.Stat(r.files.Path()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("a refused PUT wrote the settings file")
			}
		})
	}
}

func TestWrongMethodIs405WithAllowHeader(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}})
	for _, c := range []struct{ method, path, allow string }{
		{http.MethodPost, "/api/code/types", "GET"},
		{http.MethodGet, "/api/code/run", "POST"},
		{http.MethodDelete, "/api/code/settings", "GET, PUT"},
		{http.MethodGet, "/api/code/runs/abc/cancel", "POST"},
	} {
		res := r.call(c.method, c.path, "")
		if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != c.allow {
			t.Errorf("%s %s = %d Allow %q, want 405 %q", c.method, c.path, res.Code, res.Header().Get("Allow"), c.allow)
		}
	}
}

/* --------------------------------------------------------------- types */

func TestTypesRoute(t *testing.T) {
	r := newRig(t, rigOpts{})
	res := r.call(http.MethodGet, "/api/code/types", "")
	if res.Code != http.StatusOK {
		t.Fatalf("types = %d %s", res.Code, res.Body)
	}
	if res.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(res.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("headers: %v", res.Header())
	}
	var out typesDTO
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Namespace == "" || len(out.DTS) < 100 || len(out.SchemaSHA256) != 64 {
		t.Fatalf("types answer is thin: ns %q, dts %d bytes, sha %q", out.Namespace, len(out.DTS), out.SchemaSHA256)
	}
	if out.SchemaSHA256 != r.broker.ToolSchemaHash() {
		t.Fatal("the schema hash on the screen is not the one runs are recorded with")
	}
	l := out.Limits
	if l.MaxSourceBytes != codemode.MaxSource || l.MaxConcurrentRuns != codemode.MaxConcurrentRuns || l.MaxCalls != codemode.MaxCalls ||
		l.DefaultTimeoutMS != codemode.DefaultTimeout.Milliseconds() || l.RunCapSeconds != int(codemode.RunCap.Seconds()) {
		t.Fatalf("limits: %+v", l)
	}
	// A second read serves the same text.
	if again := r.call(http.MethodGet, "/api/code/types", ""); again.Body.String() != res.Body.String() {
		t.Fatal("types differ between two reads")
	}
}

/* ------------------------------------------------------------ settings */

func TestSettingsDefaultOffThenSaveAndRead(t *testing.T) {
	r := newRig(t, rigOpts{allowIdent: true})
	var got settingsDTO
	res := r.call(http.MethodGet, "/api/code/settings", "")
	if res.Code != http.StatusOK {
		t.Fatalf("get = %d", res.Code)
	}
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	if got.Enabled || got.AllowDegraded || got.KeepResults || got.Damaged || !got.AllowIdentifiers {
		t.Fatalf("defaults: %+v (allowIdentifiers shows the operator's permission)", got)
	}
	if _, err := os.Stat(r.files.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reading the settings created a file")
	}

	if res := r.call(http.MethodPut, "/api/code/settings", `{"enabled":true}`); res.Code != http.StatusOK {
		t.Fatalf("put = %d %s", res.Code, res.Body)
	}
	if set, err := r.files.Load(); err != nil || !set.Enabled || set.KeepResults {
		t.Fatalf("file after first save: %+v %v", set, err)
	}
	// An omitted switch stays as it was: the second save does not switch code mode off.
	if res := r.call(http.MethodPut, "/api/code/settings", `{"keepResults":true}`); res.Code != http.StatusOK {
		t.Fatalf("put = %d %s", res.Code, res.Body)
	}
	set, _ := r.files.Load()
	if !set.Enabled || !set.KeepResults || set.AllowDegraded {
		t.Fatalf("file after merge: %+v", set)
	}
	res = r.call(http.MethodGet, "/api/code/settings", "")
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	if !got.Enabled || !got.KeepResults || got.AllowDegraded {
		t.Fatalf("get after save: %+v", got)
	}
	// An empty object changes nothing and still answers.
	if res := r.call(http.MethodPut, "/api/code/settings", `{}`); res.Code != http.StatusOK {
		t.Fatalf("empty put = %d", res.Code)
	}
	if set2, _ := r.files.Load(); set2 != set {
		t.Fatalf("an empty save changed the settings: %+v", set2)
	}
	// The operator permission is not a switch the page can flip.
	wantProblem(t, r.call(http.MethodPut, "/api/code/settings", `{"allowIdentifiers":true}`), http.StatusBadRequest, "bad_request")
}

func TestSettingsPutRefusesBadBodies(t *testing.T) {
	r := newRig(t, rigOpts{})
	for _, c := range []struct {
		name, body string
		status     int
		code       string
		hdr        []string
	}{
		{"unknown field", `{"enabled":true,"root":true}`, 400, "bad_request", nil},
		{"wrong type", `{"enabled":"yes"}`, 400, "bad_request", nil},
		{"not json", `{"enabled":`, 400, "bad_request", nil},
		{"two values", `{"enabled":true}{"enabled":false}`, 400, "bad_request", nil},
		{"array", `[true]`, 400, "bad_request", nil},
		{"too big", `{"enabled":true,"pad":"` + strings.Repeat("a", maxSettingsBody) + `"}`, 413, "source_too_large", nil},
		{"form content type", `{"enabled":true}`, 415, "unsupported_media_type", []string{"Content-Type", "text/plain"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantProblem(t, r.call(http.MethodPut, "/api/code/settings", c.body, c.hdr...), c.status, c.code)
		})
	}
	if _, err := os.Stat(r.files.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused PUT wrote the settings file")
	}
}

func TestDamagedSettingsFileIsOffAndRepairedBySaving(t *testing.T) {
	sp := &spawn{}
	r := newRig(t, rigOpts{spawner: sp})
	r.newCase("Damaged settings")
	r.enable()
	if err := os.WriteFile(r.files.Path(), []byte(`{"enabled":true,"surprise":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var got settingsDTO
	res := r.call(http.MethodGet, "/api/code/settings", "")
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	if got.Enabled || !got.Damaged {
		t.Fatalf("a damaged file must read as off and damaged: %+v", got)
	}
	// A run with a damaged file is refused as off, never started.
	wantProblem(t, r.call(http.MethodPost, "/api/code/run", runBodyOf("1")), http.StatusForbidden, "code_mode_off")
	if sp.starts.Load() != 0 {
		t.Fatal("a worker was started with a damaged settings file")
	}
	// Saving again repairs it.
	if res := r.call(http.MethodPut, "/api/code/settings", `{"keepResults":true}`); res.Code != http.StatusOK {
		t.Fatalf("repair = %d %s", res.Code, res.Body)
	}
	res = r.call(http.MethodGet, "/api/code/settings", "")
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	if got.Damaged || got.Enabled || !got.KeepResults {
		t.Fatalf("after repair: %+v (code mode stays off until the owner switches it on)", got)
	}
}

func TestSettingsSaveFailureIsAFixedErrorAndChangesNothing(t *testing.T) {
	// The folder of the file is under a plain file, so it cannot be made.
	blocker := filepath.Join(t.TempDir(), "plainfile")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, rigOpts{settingsIn: filepath.Join(blocker, "inside")})
	res := r.call(http.MethodPut, "/api/code/settings", `{"enabled":true}`)
	p := wantProblem(t, res, http.StatusInternalServerError, "internal_error")
	if strings.Contains(res.Body.String(), "plainfile") || strings.Contains(res.Body.String(), "inside") {
		t.Fatalf("the error shows a path: %q", p.Message)
	}
	if set, _ := r.files.Load(); set.Enabled {
		t.Fatal("a failed save turned code mode on")
	}
}

/* ----------------------------------------------------------------- run */

func TestRunIsOffByDefault(t *testing.T) {
	sp := &spawn{}
	r := newRig(t, rigOpts{spawner: sp})
	id := r.newCase("Off by default")
	wantProblem(t, r.call(http.MethodPost, "/api/code/run", runBodyOf("1+1")), http.StatusForbidden, "code_mode_off")
	if len(r.runsOf(id)) != 0 || sp.starts.Load() != 0 {
		t.Fatal("a refused run left a trace")
	}
}

func TestRunWithNoWorkerIsRefusedAndNotRecorded(t *testing.T) {
	for name, sp := range map[string]codemode.Spawner{
		"nil spawner":         nil,
		"spawner unavailable": &spawn{avail: errors.New("worker file missing")},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, rigOpts{spawner: sp})
			id := r.newCase("No worker")
			r.enable()
			res := r.call(http.MethodPost, "/api/code/run", runBodyOf("1+1"))
			wantProblem(t, res, http.StatusServiceUnavailable, "sandbox_unavailable")
			if strings.Contains(res.Body.String(), "worker file missing") {
				t.Fatal("the spawner's own words reached the reply")
			}
			if runs := r.runsOf(id); len(runs) != 0 {
				t.Fatalf("a run that never started was recorded: %d", len(runs))
			}
		})
	}
}

// runRecorded is the real path as far as it goes without a worker: the request reaches the broker, the case is found and leased, the begin
// row is written, the spawner fails, and the run is sealed as failed. It runs on the sealed SQLite store and on the in-memory store.
func TestRunIsRecordedInTheCase(t *testing.T) {
	stores := map[string]func(*testing.T) casestore.Store{
		"sealed sqlite": sealedStore,
		"in memory":     func(*testing.T) casestore.Store { return casestore.NewMemory() },
	}
	for name, mk := range stores {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, rigOpts{store: mk(t), spawner: &spawn{}})
			id := r.newCase("Recorded run")
			r.enable()
			res := r.call(http.MethodPost, "/api/code/run", `{"code":"1+1","reason":"my reason"}`)
			if res.Code != http.StatusOK {
				t.Fatalf("run = %d %s", res.Code, res.Body)
			}
			var out codemode.RunResult
			if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.RunID == "" || out.Status == "ok" || out.Error == nil || out.Error.Code != "sandbox_unavailable" {
				t.Fatalf("result: %+v", out)
			}
			runs := r.runsOf(id)
			if len(runs) != 1 {
				t.Fatalf("runs in the case: %d, want 1", len(runs))
			}
			got := runs[0]
			if got.RunID != out.RunID || got.Surface != "ui" || got.CallerID != "ui" || got.AllowIdentifiers || got.EndedMS == 0 || got.Status == "running" ||
				!strings.Contains(got.Reason, "my reason") {
				t.Fatalf("recorded run: %+v", got)
			}
			full, err := r.getRun(id, got.RunID)
			if err != nil || full.Source != "1+1" {
				t.Fatalf("source not recorded: %q %v", full.Source, err)
			}
			// The command line's own User-Agent labels the run "cli".
			r.call(http.MethodPost, "/api/code/run", runBodyOf("2+2"), "User-Agent", "unravis-cli/1.0 (windows)")
			surfaces := map[string]int{}
			for _, run := range r.runsOf(id) {
				surfaces[run.Surface]++
			}
			if surfaces["ui"] != 1 || surfaces["cli"] != 1 {
				t.Fatalf("surfaces: %v", surfaces)
			}
		})
	}
}

func (r *rig) getRun(caseID, runID string) (casestore.CodeRun, error) {
	c, err := r.store.Open(r.ctx, casestore.CaseID(caseID))
	if err != nil {
		return casestore.CodeRun{}, err
	}
	defer func() { _ = c.Close() }()
	return c.(casestore.CodeRunStore).GetRun(r.ctx, runID)
}

func TestRunIdentifiersNeedTheOperatorPermission(t *testing.T) {
	// Refused when the operator did not allow it: nothing recorded.
	r := newRig(t, rigOpts{spawner: &spawn{}})
	id := r.newCase("Identifiers")
	r.enable()
	wantProblem(t, r.call(http.MethodPost, "/api/code/run", `{"code":"1","allowIdentifiers":true}`), http.StatusForbidden, "denied")
	if len(r.runsOf(id)) != 0 {
		t.Fatal("a denied run was recorded")
	}
	// Allowed when the operator did, and the row says so.
	r2 := newRig(t, rigOpts{spawner: &spawn{}, allowIdent: true})
	id2 := r2.newCase("Identifiers allowed")
	r2.enable()
	if res := r2.call(http.MethodPost, "/api/code/run", `{"code":"1","allowIdentifiers":true}`); res.Code != http.StatusOK {
		t.Fatalf("run = %d %s", res.Code, res.Body)
	}
	if runs := r2.runsOf(id2); len(runs) != 1 || !runs[0].AllowIdentifiers {
		t.Fatalf("recorded: %+v", runs)
	}
}

func TestRunCaseSelection(t *testing.T) {
	sp := &spawn{}
	r := newRig(t, rigOpts{spawner: sp})
	r.enable()

	// No case at all.
	wantProblem(t, r.call(http.MethodPost, "/api/code/run", runBodyOf("1")), http.StatusConflict, "no_case")

	// One case: it is the default.
	a := r.newCase("Case A")
	if res := r.call(http.MethodPost, "/api/code/run", runBodyOf("1")); res.Code != http.StatusOK {
		t.Fatalf("one case: %d %s", res.Code, res.Body)
	}
	if len(r.runsOf(a)) != 1 {
		t.Fatal("run not in the only case")
	}

	// Two cases and no name: refused, and the message says what to do.
	b := r.newCase("Case B")
	p := wantProblem(t, r.call(http.MethodPost, "/api/code/run", runBodyOf("1")), http.StatusConflict, "no_case")
	if !strings.Contains(p.Message, "caseId") {
		t.Fatalf("message does not say how to name the case: %q", p.Message)
	}
	if len(r.runsOf(a)) != 1 || len(r.runsOf(b)) != 0 {
		t.Fatal("a refused run was recorded")
	}

	// Naming the case works with two open.
	body, _ := json.Marshal(runBody{Code: "1", CaseID: b})
	if res := r.call(http.MethodPost, "/api/code/run", string(body)); res.Code != http.StatusOK {
		t.Fatalf("named case: %d %s", res.Code, res.Body)
	}
	if len(r.runsOf(a)) != 1 || len(r.runsOf(b)) != 1 {
		t.Fatalf("named run in the wrong case: a=%d b=%d", len(r.runsOf(a)), len(r.runsOf(b)))
	}

	// A case id that is not a case id, one that is well formed but unknown, and one that is far too long.
	unknown, _ := casestore.NewID()
	for _, bad := range []string{"../../etc", "not a case", "<script>marker-7</script>", string(unknown)} {
		body, _ := json.Marshal(runBody{Code: "1", CaseID: bad})
		res := r.call(http.MethodPost, "/api/code/run", string(body))
		wantProblem(t, res, http.StatusConflict, "no_case")
		if strings.Contains(res.Body.String(), "marker-7") || strings.Contains(res.Body.String(), string(unknown)) {
			t.Fatalf("the reply repeats what the caller sent: %s", res.Body)
		}
	}
	long, _ := json.Marshal(runBody{Code: "1", CaseID: strings.Repeat("a", maxCaseIDBytes+1)})
	wantProblem(t, r.call(http.MethodPost, "/api/code/run", string(long)), http.StatusBadRequest, "bad_request")

	// A closed case is not a place to record a run, named or not.
	r.closeCase(b)
	body, _ = json.Marshal(runBody{Code: "1", CaseID: b})
	wantProblem(t, r.call(http.MethodPost, "/api/code/run", string(body)), http.StatusConflict, "no_case")
	// With B closed, A is the one open case again and is the default.
	if res := r.call(http.MethodPost, "/api/code/run", runBodyOf("1")); res.Code != http.StatusOK {
		t.Fatalf("default after closing B: %d %s", res.Code, res.Body)
	}
	if len(r.runsOf(a)) != 2 || len(r.runsOf(b)) != 1 {
		t.Fatalf("runs: a=%d b=%d", len(r.runsOf(a)), len(r.runsOf(b)))
	}
}

func TestRunBodyRules(t *testing.T) {
	sp := &spawn{}
	r := newRig(t, rigOpts{spawner: sp})
	id := r.newCase("Body rules")
	r.enable()
	const marker = "SECRET-MARKER-91"
	cases := []struct {
		name, body string
		status     int
		code       string
		hdr        []string
	}{
		{"body over the cap", `{"code":"` + strings.Repeat("a", maxRunBody) + `"}`, 413, "source_too_large", nil},
		{"script over the limit", `{"code":"` + strings.Repeat("a", codemode.MaxSource+1) + `"}`, 413, "source_too_large", nil},
		{"bad json", `{"code":` + marker, 400, "bad_request", nil},
		{"unknown field", `{"code":"1","` + marker + `":1}`, 400, "bad_request", nil},
		{"two values", `{"code":"1"}{"code":"2"}`, 400, "bad_request", nil},
		{"array", `[]`, 400, "bad_request", nil},
		{"wrong type", `{"code":5}`, 400, "bad_request", nil},
		{"negative timeout", `{"code":"1","timeoutMs":-1}`, 400, "bad_request", nil},
		{"content type", `{"code":"1"}`, 415, "unsupported_media_type", []string{"Content-Type", "text/plain"}},
		{"no content type", `{"code":"1"}`, 415, "unsupported_media_type", []string{"Content-Type", ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := r.call(http.MethodPost, "/api/code/run", c.body, c.hdr...)
			wantProblem(t, res, c.status, c.code)
			if strings.Contains(res.Body.String(), marker) {
				t.Fatalf("the reply repeats the request: %s", res.Body)
			}
		})
	}
	if sp.starts.Load() != 0 || len(r.runsOf(id)) != 0 {
		t.Fatal("a refused body started or recorded a run")
	}
	// An empty body is not a run either.
	wantProblem(t, r.call(http.MethodPost, "/api/code/run", ""), http.StatusUnsupportedMediaType, "unsupported_media_type")
}

func TestRunTextOfARefusalIsFixed(t *testing.T) {
	// Whatever the script holds, a refusal never repeats it.
	r := newRig(t, rigOpts{spawner: &spawn{}})
	r.newCase("Fixed text")
	res := r.call(http.MethodPost, "/api/code/run", `{"code":"10.1.2.3 secret-host.internal \u0000","reason":"203.0.113.9"}`)
	if res.Code == http.StatusOK {
		t.Fatalf("a NUL in a script was accepted: %s", res.Body)
	}
	if strings.Contains(res.Body.String(), "10.1.2.3") || strings.Contains(res.Body.String(), "secret-host") || strings.Contains(res.Body.String(), "203.0.113.9") {
		t.Fatalf("the reply repeats the script or reason: %s", res.Body)
	}
}

func TestOnlyTwoRunsAtOnceAndTheThirdIsToldToWait(t *testing.T) {
	sp := &spawn{gate: make(chan struct{}), entered: make(chan struct{}, 8)}
	r := newRig(t, rigOpts{spawner: sp})
	id := r.newCase("Busy")
	r.enable()
	var wg sync.WaitGroup
	var oks atomic.Int32
	for i := 0; i < codemode.MaxConcurrentRuns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res := r.call(http.MethodPost, "/api/code/run", runBodyOf("1")); res.Code == http.StatusOK {
				oks.Add(1)
			}
		}()
	}
	for i := 0; i < codemode.MaxConcurrentRuns; i++ {
		select {
		case <-sp.entered:
		case <-time.After(10 * time.Second):
			close(sp.gate)
			t.Fatal("runs did not start")
		}
	}
	wantProblem(t, r.call(http.MethodPost, "/api/code/run", runBodyOf("1")), http.StatusTooManyRequests, "busy")
	close(sp.gate)
	wg.Wait()
	if int(oks.Load()) != codemode.MaxConcurrentRuns || len(r.runsOf(id)) != codemode.MaxConcurrentRuns {
		t.Fatalf("ok runs %d, recorded %d, want %d each (the refused third is not recorded)", oks.Load(), len(r.runsOf(id)), codemode.MaxConcurrentRuns)
	}
}

/* -------------------------------------------------------------- cancel */

func TestCancelUnknownRun(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}})
	wantProblem(t, r.call(http.MethodPost, "/api/code/runs/0190aaaa-bbbb-7ccc-8ddd-eeeeffff0000/cancel", ""), http.StatusNotFound, "not_found")
	wantProblem(t, r.call(http.MethodPost, "/api/code/runs/nothere/cancel", ""), http.StatusNotFound, "not_found")
	// A path that is not shaped like a run id does not reach the handler at all.
	if res := r.call(http.MethodPost, "/api/code/runs/..%2f..%2fapi/cancel", ""); res.Code != http.StatusNotFound {
		t.Fatalf("odd id = %d", res.Code)
	}
}

func TestCancelEndsALiveRunOnce(t *testing.T) {
	sp := &spawn{gate: make(chan struct{}), entered: make(chan struct{}, 2)}
	r := newRig(t, rigOpts{spawner: sp})
	id := r.newCase("Cancel")
	r.enable()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- r.call(http.MethodPost, "/api/code/run", runBodyOf("while(true){}")) }()
	select {
	case <-sp.entered:
	case <-time.After(10 * time.Second):
		close(sp.gate)
		t.Fatal("run did not start")
	}
	// The id of a live run is in the case's run list (the reply that names it comes only when the run ends).
	var runID string
	for deadline := time.Now().Add(10 * time.Second); runID == "" && time.Now().Before(deadline); {
		for _, run := range r.runsOf(id) {
			if run.Status == "running" {
				runID = run.RunID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if runID == "" {
		close(sp.gate)
		t.Fatal("the live run is not in the case")
	}
	res := r.call(http.MethodPost, "/api/code/runs/"+runID+"/cancel", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"cancelled":true`) {
		close(sp.gate)
		t.Fatalf("cancel = %d %s", res.Code, res.Body)
	}
	close(sp.gate)
	select {
	case res := <-done:
		if res.Code != http.StatusOK {
			t.Fatalf("the cancelled run answered %d %s", res.Code, res.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not end after the cancel")
	}
	if runs := r.runsOf(id); len(runs) != 1 || runs[0].Status == "running" {
		t.Fatalf("the run is not sealed: %+v", runs)
	}
	// A second cancel finds nothing live.
	wantProblem(t, r.call(http.MethodPost, "/api/code/runs/"+runID+"/cancel", ""), http.StatusNotFound, "not_found")
}

/* ------------------------------------------------------------ resolver */

func TestResolver(t *testing.T) {
	stores := map[string]func(*testing.T) casestore.Store{
		"sealed sqlite": sealedStore,
		"in memory":     func(*testing.T) casestore.Store { return casestore.NewMemory() },
	}
	for name, mk := range stores {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, rigOpts{store: mk(t)})
			resolve := Resolver(r.mount)
			open := func(caseID string) (casestore.CodeRunStore, string, func(), error) {
				ctx, release := WithLeases(context.Background())
				store, id, err := resolve(ctx, caseID)
				return store, id, release, err
			}

			// None.
			if _, _, rel, err := open(""); !errors.Is(err, codemode.ErrNoCase) {
				t.Fatalf("none: %v", err)
			} else {
				rel()
			}

			// One: the default; the store is usable while the lease is held.
			a := r.newCase("Resolver A")
			store, id, rel, err := open("")
			if err != nil || id != a || store == nil {
				t.Fatalf("one: id %q err %v", id, err)
			}
			if _, err := store.ListRuns(context.Background(), casestore.RunQuery{}); err != nil {
				t.Fatalf("the leased case is not usable: %v", err)
			}
			rel()
			rel() // giving back twice is harmless

			// Many: no default, an error that says so; the named one works.
			b := r.newCase("Resolver B")
			ctx, relMany := WithLeases(context.Background())
			if _, _, err := resolve(ctx, ""); !errors.Is(err, codemode.ErrNoCase) || !leasesOf(ctx).many() {
				t.Fatalf("many: %v many=%v", err, leasesOf(ctx).many())
			}
			relMany()
			for _, want := range []string{a, b} {
				_, got, rel, err := open(want)
				if err != nil || got != want {
					t.Fatalf("named %q: got %q %v", want, got, err)
				}
				rel()
			}

			// Named but not a case, and named but closed.
			for _, bad := range []string{"nope", "../x", strings.Repeat("z", 200)} {
				if _, _, rel, err := open(bad); !errors.Is(err, codemode.ErrNoCase) {
					t.Fatalf("named %q: %v", bad, err)
				} else {
					rel()
				}
			}
			r.closeCase(b)
			if _, _, rel, err := open(b); !errors.Is(err, codemode.ErrNoCase) {
				t.Fatalf("closed: %v", err)
			} else {
				rel()
			}
			if _, got, rel, err := open(""); err != nil || got != a {
				t.Fatalf("after closing B the default is A: %q %v", got, err)
			} else {
				rel()
			}

			// No lease scope: refused, because the handle could never be given back.
			if _, _, err := resolve(context.Background(), a); !errors.Is(err, codemode.ErrNoCase) {
				t.Fatalf("no scope: %v", err)
			}
			// A scope that was already released does not take new handles.
			ctx, rel2 := WithLeases(context.Background())
			rel2()
			if _, _, err := resolve(ctx, a); err != nil {
				// either outcome is safe as long as nothing is left held; the erase test below proves the release path
				if !errors.Is(err, codemode.ErrNoCase) {
					t.Fatalf("after release: %v", err)
				}
			}
		})
	}
}

// The case is held against an erase for as long as a run uses it and not a moment longer: after the run (a real sealed case), the owner
// can erase it at once. A lease that leaked would make the erase wait for the readers and answer that the case is busy.
func TestCaseCanBeErasedAfterARun(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}})
	id := r.newCase("Erase after run")
	r.enable()
	if res := r.call(http.MethodPost, "/api/code/run", runBodyOf("1")); res.Code != http.StatusOK {
		t.Fatalf("run = %d %s", res.Code, res.Body)
	}
	// And after a refused run (named a case that is not there).
	r.call(http.MethodPost, "/api/code/run", `{"code":"1","caseId":"nope"}`)
	start := time.Now()
	res := r.call(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`, "X-Expect-State", "open")
	if res.Code != http.StatusOK && res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s (a lease was not given back)", res.Code, res.Body)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("the erase waited on a reader that never left")
	}
	infos, err := r.store.List(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range infos {
		if string(info.ID) == id {
			t.Fatal("the case is still there after the erase")
		}
	}
}

func TestSurfaceRule(t *testing.T) {
	for ua, want := range map[string]string{
		"unravis-cli/1.0":      "cli",
		"unravis-cli/0.9 (x)":  "cli",
		"":                     "ui",
		"Mozilla/5.0 Chrome":   "ui",
		"Unravis-CLI/1.0":      "ui", // an exact prefix, no guessing
		"x unravis-cli/1.0":    "ui",
		"unravis-clinic/1.0":   "ui",
		"unravis-mcp-whatever": "ui", // the bridge never gets this far; if it did it would still not be "cli"
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/code/run", nil)
		req.Header.Set("User-Agent", ua)
		if got := surfaceOf(req); got != want {
			t.Errorf("surfaceOf(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestStatusMap(t *testing.T) {
	for code, want := range map[string]int{
		codemode.CodeModeOff:            http.StatusForbidden,
		codemode.CodeDenied:             http.StatusForbidden,
		codemode.CodeNoCase:             http.StatusConflict,
		codemode.CodeBusy:               http.StatusTooManyRequests,
		codemode.CodeSourceTooLarge:     http.StatusRequestEntityTooLarge,
		codemode.CodeSyntaxError:        http.StatusBadRequest,
		codemode.CodeBadRequest:         http.StatusBadRequest,
		codemode.CodeSandboxUnavailable: http.StatusServiceUnavailable,
		codemode.CodeAuditFailed:        http.StatusInternalServerError,
		codemode.CodeInternal:           http.StatusInternalServerError,
		"something_new":                 http.StatusInternalServerError,
	} {
		if got := statusOf(code); got != want {
			t.Errorf("statusOf(%q) = %d, want %d", code, got, want)
		}
	}
}

// A run that a crash left open is closed when the owner next opens its case (spec 3.5 step 9), through the server's case routes, once.
// This uses the in-memory store because its clock can be moved past the grace period; the sealed store's sweep is the store's own and is
// covered in internal/casestore/sqlite.
func TestOpeningACaseClosesRunsACrashLeftOpen(t *testing.T) {
	var offset atomic.Int64
	now := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	r := newRig(t, rigOpts{store: casestore.NewMemoryWith(casestore.DefaultCaps, now), spawner: &spawn{}})
	id := r.newCase("Crash sweep")

	seed, err := privacy.NewSeed(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.store.Open(r.ctx, casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	runID, err := casestore.NewRunID(now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.(casestore.CodeRunStore).BeginRun(r.ctx, casestore.RunStart{RunID: runID, Surface: "cli", CallerID: "test", Seed: seed, Source: "1", ToolSchemaHash: r.broker.ToolSchemaHash()}); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	// Younger than the grace period: another window may still be running it, so opening the case leaves it.
	if res := r.call(http.MethodGet, "/api/cases/"+id, ""); res.Code != http.StatusOK {
		t.Fatalf("open case = %d %s", res.Code, res.Body)
	}
	if runs := r.runsOf(id); len(runs) != 1 || runs[0].Status != casestore.RunRunning {
		t.Fatalf("a young run was touched: %+v", runs)
	}
	// The sweep is once per case per process, so a later open does not look again (Run sweeps too, which is covered by the broker's tests).
	// A fresh server over the same store sweeps again, after the grace period.
	offset.Store(int64(casestore.OrphanGrace + time.Minute))
	r2 := newRig(t, rigOpts{store: r.store, spawner: &spawn{}})
	if res := r2.call(http.MethodGet, "/api/cases/"+id, ""); res.Code != http.StatusOK {
		t.Fatalf("open case = %d %s", res.Code, res.Body)
	}
	runs := r2.runsOf(id)
	if len(runs) != 1 || runs[0].Status == casestore.RunRunning || runs[0].EndedMS == 0 {
		t.Fatalf("the orphan run was not closed by opening the case: %+v", runs)
	}
}

// A sweep that failed (an import held the case, the budget ran out) is not marked done: the next open of the case tries again, and a
// sweep that worked is not repeated.
func TestFailedCrashSweepIsRetriedOnTheNextOpen(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}})
	id := r.newCase("Sweep retry")
	var calls int
	r.mount.Set(http.NotFoundHandler(), func(ctx context.Context, _ casestore.CodeRunStore) error {
		calls++
		if ctx.Err() != nil {
			t.Error("the sweep must not be tied to the page's request")
		}
		if calls == 1 {
			return errors.New("the case is held by an import")
		}
		return nil
	})
	for i, want := range []int{1, 2, 2} {
		if res := r.call(http.MethodGet, "/api/cases/"+id, ""); res.Code != http.StatusOK {
			t.Fatalf("open %d = %d %s", i, res.Code, res.Body)
		}
		if calls != want {
			t.Fatalf("after open %d the sweep ran %d times, want %d", i, calls, want)
		}
	}
}

// A settings file that cannot be read (not one that is corrupt) must not be replaced by defaults plus the patch: the save is refused.
func TestSettingsSaveIsRefusedWhenTheFileCannotBeRead(t *testing.T) {
	notAFolder := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notAFolder, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, rigOpts{spawner: &spawn{}, settingsIn: notAFolder})
	if _, err := r.files.Load(); err == nil || errors.Is(err, settingsfile.ErrCorrupt) {
		t.Skipf("this OS reports the path as missing or damaged, not as unreadable: %v", err)
	}
	wantProblem(t, r.call(http.MethodPut, "/api/code/settings", `{"keepResults":true}`), http.StatusInternalServerError, codemode.CodeInternal)
}
