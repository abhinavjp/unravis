package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/remote/pull"
	"api-audit-explorer/internal/remote/registry"
	"api-audit-explorer/internal/remote/saltmaster"
	"api-audit-explorer/internal/remote/service"
	"api-audit-explorer/internal/remote/sshdirect"
	"api-audit-explorer/internal/remote/transport"
)

// Tests of the Servers pages over a real sealed list and a scripted service, so no server, key or network is touched.

const srvSecretSentinel = "SENTINEL-server-password-9f3a"

// fakeSrvSvc scripts the service. Each field is the answer of one method; hold blocks a pull until released.
type fakeSrvSvc struct {
	mu          sync.Mutex
	keys        []sshdirect.HostKey
	keysErr     error
	pinErr      error
	testErr     error
	files       []transport.FileInfo
	planErr     error
	pullErr     error
	pulled      service.Pulled
	hold        chan struct{}
	started     chan struct{}
	pulls       []string
	pinned      []string
	lastPlan    pull.Request
	lastMachine string
	pullMachine string
	machines    []service.MachineView
	machinesErr error
}

func (f *fakeSrvSvc) HostKeys(context.Context, string) ([]sshdirect.HostKey, error) {
	return f.keys, f.keysErr
}

func (f *fakeSrvSvc) Pin(_ context.Context, _ string, fp string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinned = append(f.pinned, fp)
	return f.pinErr
}

func (f *fakeSrvSvc) ReplaceKey(context.Context, string, string, string) error { return f.pinErr }

func (f *fakeSrvSvc) Test(_ context.Context, _, machine string) (service.TestResult, error) {
	f.mu.Lock()
	f.lastMachine = machine
	f.mu.Unlock()
	return service.TestResult{Folders: []service.FolderCheck{{Kind: "iis", Path: `C:\inetpub\logs`, OK: true, Files: 3}}}, f.testErr
}

func (f *fakeSrvSvc) Browse(context.Context, string, string, string) ([]transport.FileInfo, error) {
	return f.files, nil
}

func (f *fakeSrvSvc) Plan(_ context.Context, _, _ string, req pull.Request) (pull.Plan, error) {
	f.mu.Lock()
	f.lastPlan = req
	f.mu.Unlock()
	if f.planErr != nil {
		return pull.Plan{Kind: req.Kind, Dir: req.Dir}, f.planErr
	}
	return pull.Plan{Kind: req.Kind, Dir: req.Dir, Bytes: 10, Files: []pull.PlanFile{{Name: "a.log", Size: 10}}}, nil
}

func (f *fakeSrvSvc) Pull(ctx context.Context, id, machine string, req pull.Request, jobID string, progress func(pull.Progress)) (service.Pulled, error) {
	f.mu.Lock()
	f.pulls = append(f.pulls, jobID)
	f.pullMachine = machine
	f.mu.Unlock()
	progress(pull.Progress{JobID: jobID, File: "a.log", FilesTotal: 2, FilesDone: 1, JobBytes: 5})
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return service.Pulled{Result: pull.Result{JobID: jobID, Server: id, Interrupted: true}}, ctx.Err()
		}
	}
	res := f.pulled
	res.JobID, res.Server, res.Kind = jobID, id, req.Kind
	return res, f.pullErr
}

type srvEnv struct {
	t       *testing.T
	e       *caseEnv
	reg     *registry.Store
	bbs     *bbsaved.Store
	svc     *fakeSrvSvc
	staging string
}

func newSrvEnv(t *testing.T, withBB bool) *srvEnv {
	t.Helper()
	dir := t.TempDir()
	reg, err := registry.New(filepath.Join(dir, "servers.dat"), storetest.RandomProtector())
	if err != nil {
		t.Fatal(err)
	}
	s := &srvEnv{t: t, reg: reg, svc: &fakeSrvSvc{}, staging: filepath.Join(dir, "pulled")}
	opts := []Option{WithServers(ServersDeps{Reg: reg, Svc: s.svc, StagingRoot: s.staging})}
	if withBB {
		bbs, err := bbsaved.New(filepath.Join(dir, "blackboxes.dat"), storetest.RandomProtector())
		if err != nil {
			t.Fatal(err)
		}
		s.bbs = bbs
		opts = append(opts, WithBlackbox(BlackboxDeps{Store: bbs, Open: func(blackbox.Location, string) blackbox.Source { return nil }}))
	}
	s.e = tagEnv(t, casestore.NewMemory(), &fakeRepository{requestPages: []audit.Page{}}, opts...)
	return s
}

func (s *srvEnv) addBody() string {
	return jb(map[string]any{"name": "web-1", "host": "web-1.example.test", "port": 22, "user": "audit", "os": "windows", "auth": "password", "secret": srvSecretSentinel,
		"transports": []string{"ssh", "blackbox-http"}, "folders": map[string][]string{"iis": {`C:\inetpub\logs`}, "blackbox": {`C:\bb`}}})
}

func (s *srvEnv) mustAdd() registry.Entry {
	s.t.Helper()
	res := s.e.json(http.MethodPost, "/api/servers", s.addBody())
	if res.Code != http.StatusCreated {
		s.t.Fatalf("add = %d %s", res.Code, res.Body)
	}
	var e registry.Entry
	decodeBody(s.t, res, &e)
	return e
}

func srvFailed(t *testing.T, res *httptest.ResponseRecorder, code int, reason string) srvFailure {
	t.Helper()
	var f srvFailure
	decodeBody(t, res, &f)
	if res.Code != code || f.Reason != reason {
		t.Fatalf("got %d reason %q (%s), want %d %q", res.Code, f.Reason, res.Body, code, reason)
	}
	return f
}

func TestServersAddListNeverReturnsSecret(t *testing.T) {
	s := newSrvEnv(t, false)
	e := s.mustAdd()
	if !srvIDRe.MatchString(e.ID) || !e.HasSecret {
		t.Fatalf("added = %+v", e)
	}
	for _, res := range []*httptest.ResponseRecorder{s.e.get("/api/servers"), s.e.json(http.MethodPatch, "/api/servers/"+e.ID, `{"name":"web-one"}`)} {
		if res.Code != http.StatusOK || strings.Contains(res.Body.String(), srvSecretSentinel) {
			t.Fatalf("answer %d leaks or fails: %s", res.Code, res.Body)
		}
	}
	var l srvListAnswer
	decodeBody(t, s.e.get("/api/servers"), &l)
	if len(l.Items) != 1 || l.Items[0].Name != "web-one" {
		t.Fatalf("list = %+v", l)
	}
	if res := s.e.json(http.MethodPost, "/api/servers", s.addBody()); res.Code != http.StatusConflict {
		t.Fatalf("duplicate add = %d %s", res.Code, res.Body)
	}
}

func TestServersAddRefusesBadInput(t *testing.T) {
	s := newSrvEnv(t, false)
	srvFailed(t, s.e.json(http.MethodPost, "/api/servers", `{"name":"x","host":"bad host!","user":"u","os":"windows","auth":"password","secret":"s"}`), http.StatusUnprocessableEntity, "invalid")
	if res := s.e.json(http.MethodPost, "/api/servers", `{"name":"x","extra":1}`); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", res.Code, res.Body)
	}
}

func TestServersHostKeyFlowAndErrors(t *testing.T) {
	s := newSrvEnv(t, false)
	e := s.mustAdd()
	s.svc.keys = []sshdirect.HostKey{{Algorithm: "ssh-ed25519", Fingerprint: "SHA256:abc"}}
	res := s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/host-keys", "{}")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "SHA256:abc") {
		t.Fatalf("host-keys = %d %s", res.Code, res.Body)
	}
	if res := s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/pin", `{"fingerprint":"SHA256:abc"}`); res.Code != http.StatusOK || len(s.svc.pinned) != 1 {
		t.Fatalf("pin = %d %s", res.Code, res.Body)
	}
	cases := []struct {
		err    error
		code   int
		reason string
	}{
		{service.ErrNeedHostKey, 409, "need-host-key"},
		{&sshdirect.HostKeyChanged{Pinned: "SHA256:old", Presented: "SHA256:new"}, 409, "host-key-changed"},
		{sshdirect.ErrSignIn, 502, "sign-in"},
		{sshdirect.ErrUnreachable, 502, "unreachable"},
		{context.DeadlineExceeded, 504, "timeout"},
		{errors.New("secret detail " + srvSecretSentinel), 500, "failed"},
	}
	for _, c := range cases {
		s.svc.testErr = c.err
		res := s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/test", "")
		f := srvFailed(t, res, c.code, c.reason)
		if strings.Contains(res.Body.String(), srvSecretSentinel) {
			t.Fatalf("%s leaks an unknown error: %s", c.reason, res.Body)
		}
		if c.reason == "host-key-changed" && (f.Pinned != "SHA256:old" || f.Presented != "SHA256:new") {
			t.Fatalf("changed key = %+v", f)
		}
	}
}

func TestServersBrowseAndPlan(t *testing.T) {
	s := newSrvEnv(t, false)
	e := s.mustAdd()
	s.svc.files = []transport.FileInfo{{Name: "a.log", Size: 4}}
	if res := s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/browse", `{"dir":"C:\\inetpub\\logs"}`); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "a.log") {
		t.Fatalf("browse = %d %s", res.Code, res.Body)
	}
	res := s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/plan", `{"kind":"iis","dir":"C:\\inetpub\\logs","sinceHours":24}`)
	if res.Code != http.StatusOK || s.svc.lastPlan.Since.IsZero() || s.svc.lastPlan.Kind != "iis" {
		t.Fatalf("plan = %d %s req=%+v", res.Code, res.Body, s.svc.lastPlan)
	}
	s.svc.planErr = pull.ErrPlanTooLarge{Problem: "That is 9 GB. Narrow it."}
	res = s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/plan", `{"kind":"iis","dir":"C:\\inetpub\\logs"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Narrow it") {
		t.Fatalf("too large plan = %d %s", res.Code, res.Body)
	}
	srvFailed(t, s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/plan", `{"kind":"iis","dir":"x","sinceHours":-1}`), http.StatusUnprocessableEntity, "invalid")
	s.svc.planErr = transport.ErrNotAllowed
	srvFailed(t, s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/plan", `{"kind":"iis","dir":"C:\\Windows"}`), http.StatusUnprocessableEntity, "not-allowed")
}

func (s *srvEnv) waitJob(id, job, want string) srvJobView {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		res := s.e.get("/api/servers/" + id + "/pulls/" + job)
		var v srvJobView
		decodeBody(s.t, res, &v)
		if v.State == want {
			return v
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("job stayed %q, want %q (%s)", v.State, want, res.Body)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestServersPullProgressBusyAndCancel(t *testing.T) {
	s := newSrvEnv(t, false)
	e := s.mustAdd()
	s.svc.hold = make(chan struct{})
	s.svc.started = make(chan struct{}, 1)
	res := s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/pulls", `{"kind":"iis","dir":"C:\\inetpub\\logs"}`)
	if res.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", res.Code, res.Body)
	}
	var v srvJobView
	decodeBody(t, res, &v)
	<-s.svc.started
	if !srvJobRe.MatchString(v.ID) {
		t.Fatalf("job id = %q", v.ID)
	}
	if got := s.e.get("/api/servers/" + e.ID + "/pulls/" + v.ID); !strings.Contains(got.Body.String(), `"filesTotal":2`) {
		t.Fatalf("progress = %s", got.Body)
	}
	srvFailed(t, s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/pulls", `{"kind":"iis","dir":"x"}`), http.StatusConflict, "busy")
	srvFailed(t, s.e.json(http.MethodDelete, "/api/servers/"+e.ID, ""), http.StatusConflict, "busy")
	if res := s.e.json(http.MethodDelete, "/api/servers/"+e.ID+"/pulls/"+v.ID, ""); res.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", res.Code, res.Body)
	}
	if done := s.waitJob(e.ID, v.ID, "stopped"); done.Failure != nil {
		t.Fatalf("a stop is not a failure: %+v", done.Failure)
	}
	if res := s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/pulls", `{"kind":"iis","dir":"x","resume":"../etc"}`); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad resume id = %d %s", res.Code, res.Body)
	}
	// Resume carries on under the same id.
	s.svc.hold = nil
	s.svc.pulled = service.Pulled{Result: pull.Result{Files: []pull.FileResult{{Name: "a.log", Status: "done"}}}}
	res = s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/pulls", `{"kind":"iis","dir":"x","resume":"`+v.ID+`"}`)
	if res.Code != http.StatusAccepted {
		t.Fatalf("resume = %d %s", res.Code, res.Body)
	}
	if d := s.waitJob(e.ID, v.ID, "done"); d.Result == nil {
		t.Fatalf("resumed job = %+v", d)
	}
}

func TestServersPullFailureAndDone(t *testing.T) {
	s := newSrvEnv(t, false)
	e := s.mustAdd()
	s.svc.pullErr = sshdirect.ErrSignIn
	var v srvJobView
	decodeBody(t, s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/pulls", `{"kind":"iis","dir":"x"}`), &v)
	if f := s.waitJob(e.ID, v.ID, "failed"); f.Failure == nil || f.Failure.Reason != "sign-in" {
		t.Fatalf("failed = %+v", f)
	}
	s.svc.pullErr = nil
	s.svc.pulled = service.Pulled{Result: pull.Result{Files: []pull.FileResult{{Name: "a.log", Status: "done"}}}}
	decodeBody(t, s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/pulls", `{"kind":"iis","dir":"x"}`), &v)
	if d := s.waitJob(e.ID, v.ID, "done"); d.Result == nil || len(d.Result.Files) != 1 {
		t.Fatalf("done = %+v", d)
	}
	var l struct {
		Items []srvJobView `json:"items"`
	}
	decodeBody(t, s.e.get("/api/servers/"+e.ID+"/pulls"), &l)
	if len(l.Items) != 2 {
		t.Fatalf("jobs = %d", len(l.Items))
	}
}

func TestServersBlackboxPullIsListed(t *testing.T) {
	s := newSrvEnv(t, true)
	e := s.mustAdd()
	folder, err := blackbox.ParseLocation(filepath.Join(t.TempDir(), "bb"))
	if err != nil {
		t.Fatal(err)
	}
	s.svc.pulled = service.Pulled{Result: pull.Result{Files: []pull.FileResult{{Name: "manifest.json", Status: "done"}}}, BlackboxFolder: &folder}
	var v srvJobView
	decodeBody(t, s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/pulls", `{"kind":"blackbox","dir":"C:\\bb"}`), &v)
	deadline := time.Now().Add(5 * time.Second)
	for v.BlackboxID == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		decodeBody(t, s.e.get("/api/servers/"+e.ID+"/pulls/"+v.ID), &v)
	}
	if !strings.HasPrefix(v.BlackboxID, "bb-") || !strings.Contains(v.BlackboxName, "web-1") {
		t.Fatalf("not listed: %+v", v)
	}
	if list, _ := s.bbs.List(); len(list) != 1 {
		t.Fatalf("blackbox list = %d", len(list))
	}
}

func TestServersJobFileContainment(t *testing.T) {
	s := newSrvEnv(t, false)
	e := s.mustAdd()
	job := "job-0123456789abcdef"
	dir := filepath.Join(s.staging, e.ID, job)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.log", "hello")
	write("b.log.part", "half")
	write("b.log.state", "{}")
	write("pull-record.json", "{}")
	outside := filepath.Join(s.staging, "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := "/api/servers/" + e.ID + "/pulls/" + job + "/files/"
	if res := s.e.get(base + "a.log"); res.Code != http.StatusOK || res.Body.String() != "hello" ||
		!strings.Contains(res.Header().Get("Content-Disposition"), "attachment") || res.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("file = %d %q %v", res.Code, res.Body, res.Header())
	}
	for _, name := range []string{"b.log.part", "b.log.state", "missing.log", "..%2Fsecret.txt", "..", ".hidden"} {
		if res := s.e.get(base + name); res.Code != http.StatusNotFound {
			t.Fatalf("%s = %d %s", name, res.Code, res.Body)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.log")); err == nil {
		if res := s.e.get(base + "link.log"); res.Code != http.StatusNotFound {
			t.Fatalf("symlink served = %d %s", res.Code, res.Body)
		}
	}
	if res := s.e.get("/api/servers/" + e.ID + "/pulls/job-zz/files/a.log"); res.Code != http.StatusNotFound {
		t.Fatalf("bad job id = %d", res.Code)
	}
}

func TestServersRemoveAndSecret(t *testing.T) {
	s := newSrvEnv(t, false)
	e := s.mustAdd()
	if res := s.e.json(http.MethodPut, "/api/servers/"+e.ID+"/secret", jb(map[string]string{"auth": "password", "secret": "new-" + srvSecretSentinel})); res.Code != http.StatusNoContent {
		t.Fatalf("secret = %d %s", res.Code, res.Body)
	}
	if res := s.e.json(http.MethodDelete, "/api/servers/"+e.ID, ""); res.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", res.Code, res.Body)
	}
	srvFailed(t, s.e.json(http.MethodDelete, "/api/servers/"+e.ID, ""), http.StatusNotFound, "not-listed")
	if res := s.e.get("/api/servers/not-an-id/pulls"); res.Code != http.StatusNotFound {
		t.Fatalf("bad id = %d", res.Code)
	}
}

func TestServersOffWithoutWiring(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	if res := e.get("/api/servers"); res.Code == http.StatusOK {
		t.Fatalf("unwired list answered %d", res.Code)
	}
}

func (f *fakeSrvSvc) Machines(context.Context, string) ([]service.MachineView, error) {
	return f.machines, f.machinesErr
}

func TestServersSaltMachinesAndErrors(t *testing.T) {
	s := newSrvEnv(t, false)
	res := s.e.json(http.MethodPost, "/api/servers", jb(map[string]any{"name": "jump-1", "host": "jump.example.test", "port": 22, "user": "audit", "os": "linux", "auth": "password", "secret": srvSecretSentinel,
		"transports": []string{"salt"}, "minionIds": []string{"web-01"}, "saltSudo": true, "folders": map[string][]string{"blackbox": {"/var/log/blackbox"}}}))
	if res.Code != http.StatusCreated {
		t.Fatalf("add salt server = %d %s", res.Code, res.Body)
	}
	var e registry.Entry
	decodeBody(t, res, &e)
	if !e.SaltSudo || len(e.MinionIDs) != 1 {
		t.Fatalf("salt fields lost: %+v", e)
	}
	s.svc.machines = []service.MachineView{{Chosen: true}}
	res = s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/machines", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"machines"`) {
		t.Fatalf("machines = %d %s", res.Code, res.Body)
	}
	s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/test", `{"machine":"web-01"}`)
	if s.svc.lastMachine != "web-01" {
		t.Fatalf("machine not passed to test: %q", s.svc.lastMachine)
	}
	cases := []struct {
		err    error
		code   int
		reason string
	}{
		{service.ErrNeedMachine, 422, "need-machine"},
		{service.ErrMachineNotListed, 422, "machine-not-listed"},
		{service.ErrNoSalt, 422, "no-salt"},
		{saltmaster.ErrOffline, 502, "machine-offline"},
		{saltmaster.ErrMasterCmd, 502, "salt-master"},
	}
	for _, c := range cases {
		s.svc.machinesErr = c.err
		srvFailed(t, s.e.json(http.MethodPost, "/api/servers/"+e.ID+"/machines", ""), c.code, c.reason)
	}
}
