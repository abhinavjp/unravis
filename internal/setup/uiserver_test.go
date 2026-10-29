package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type uiClient struct {
	t   *testing.T
	s   *UIServer
	srv *httptest.Server
}

func newUIClient(t *testing.T, be UIBackend) *uiClient {
	t.Helper()
	s := &UIServer{Key: "k-test", Backend: be, Static: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Unravis Setup</title>")}}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	s.Addr = strings.TrimPrefix(srv.URL, "http://")
	return &uiClient{t: t, s: s, srv: srv}
}

func (c *uiClient) do(method, path, body string, hdr map[string]string) (int, map[string]any) {
	c.t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	req.Header.Set("X-Setup-Key", c.s.Key)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// waitResult polls progress until the run ends and returns the last response.
func (c *uiClient) waitResult() map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, out := c.do("GET", "/api/progress?since=0", "", nil)
		if out["running"] == false && out["result"] != nil {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.t.Fatal("the run did not finish")
	return nil
}

func TestSetupPageRefusesStrangers(t *testing.T) {
	c := newUIClient(t, CaptureScenarios()["fresh"])
	if code, _ := c.do("GET", "/api/state", "", map[string]string{"X-Setup-Key": ""}); code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", code)
	}
	if code, _ := c.do("GET", "/api/state", "", map[string]string{"X-Setup-Key": "k-tesT"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", code)
	}
	// Another site in the browser, or a DNS-rebinding name for 127.0.0.1.
	if code, _ := c.do("GET", "/api/state", "", map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("other origin: %d", code)
	}
	if code, _ := c.do("GET", "/api/state", "", map[string]string{"Host": "rebind.example:80"}); code != http.StatusMisdirectedRequest {
		t.Fatalf("other host: %d", code)
	}
	if code, _ := c.do("GET", "/", "", map[string]string{"Host": "rebind.example:80"}); code != http.StatusMisdirectedRequest {
		t.Fatalf("the page itself on another host: %d", code)
	}
	if code, _ := c.do("POST", "/index.html", "", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("post to the page: %d", code)
	}
	if code, st := c.do("GET", "/api/state", "", map[string]string{"Origin": "http://" + c.s.Addr}); code != 200 || st["version"] != "1.0.0" {
		t.Fatalf("own origin: %d %v", code, st)
	}
}

func TestSetupPageServesThePageWithoutTheKey(t *testing.T) {
	c := newUIClient(t, CaptureScenarios()["fresh"])
	resp, err := http.Get(c.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	if !strings.HasPrefix(c.s.URL(), "http://"+c.s.Addr+"/#k=") {
		t.Fatal(c.s.URL())
	}
}

func TestSetupPageReadsOnlyTypedRequests(t *testing.T) {
	c := newUIClient(t, CaptureScenarios()["fresh"])
	for _, body := range []string{
		`{"op":"install","options":{"scope":"user"},"command":"del C:\\"}`,
		`{"op":"format-disk"}`,
		`{"op":`,
	} {
		if code, _ := c.do("POST", "/api/preflight", body, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, code)
		}
	}
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/run", strings.NewReader(`{"op":"install"}`))
	req.Header.Set("X-Setup-Key", c.s.Key)
	req.Header.Set("Content-Type", "text/plain") // what a cross-site form could send
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d", resp.StatusCode)
	}
}

func TestSetupPageRunShowsProgressThenTheResult(t *testing.T) {
	c := newUIClient(t, CaptureScenarios()["done"])
	code, out := c.do("POST", "/api/preflight", `{"op":"install","options":{"scope":"machine","blackbox":{"name":"web-01","folder":"D:\\UnravisBlackbox","keepDays":7,"maxGB":2,"minFreePercent":10,"port":7788}}}`, nil)
	if code != 200 || out["checks"] == nil || out["changes"].(map[string]any)["needsAdmin"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if code, _ := c.do("POST", "/api/open-app", "", nil); code != http.StatusConflict {
		t.Fatalf("open before a run: %d", code)
	}
	if code, _ := c.do("POST", "/api/run", `{"op":"install","options":{"scope":"machine"}}`, nil); code != http.StatusAccepted {
		t.Fatalf("run: %d", code)
	}
	out = c.waitResult()
	res := out["result"].(map[string]any)
	if res["outcome"] != string(OutcomeDone) || res["readKey"] == "" || res["readKey"] == nil {
		t.Fatalf("%v", res)
	}
	if n := len(out["events"].([]any)); n < 10 {
		t.Fatalf("only %d events", n)
	}
	// The read key is handed over once.
	_, again := c.do("GET", "/api/progress?since=0", "", nil)
	if k, _ := again["result"].(map[string]any)["readKey"].(string); k != "" {
		t.Fatalf("the read key was sent twice: %q", k)
	}
	opened := ""
	c.s.OpenApp = func(exe string) error { opened = exe; return nil }
	if code, _ := c.do("POST", "/api/open-app", "", nil); code != 200 || !strings.HasSuffix(opened, `\unravis.exe`) {
		t.Fatalf("open after done: %d %q", code, opened)
	}
}

func TestSetupPageOneRunAtATimeAndCancel(t *testing.T) {
	c := newUIClient(t, CaptureScenarios()["progress"])
	quit := make(chan struct{}, 1)
	c.s.Quit = func() { quit <- struct{}{} }
	if code, _ := c.do("POST", "/api/run", `{"op":"install","options":{"scope":"user"}}`, nil); code != http.StatusAccepted {
		t.Fatal(code)
	}
	// A double click on Install, or a second tab.
	if code, _ := c.do("POST", "/api/run", `{"op":"install","options":{"scope":"user"}}`, nil); code != http.StatusConflict {
		t.Fatalf("second run: %d", code)
	}
	if code, _ := c.do("POST", "/api/close", "", nil); code != http.StatusConflict {
		t.Fatalf("close while running: %d", code)
	}
	if code, _ := c.do("POST", "/api/cancel", "", nil); code != 200 {
		t.Fatal(code)
	}
	res := c.waitResult()["result"].(map[string]any)
	if res["outcome"] != string(OutcomeCancelled) {
		t.Fatalf("%v", res)
	}
	if code, _ := c.do("POST", "/api/open-app", "", nil); code != http.StatusConflict {
		t.Fatalf("open after a cancel: %d", code)
	}
	if code, _ := c.do("POST", "/api/close", "", nil); code != 200 {
		t.Fatalf("close after the run: %d", code)
	}
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not quit")
	}
}

func TestSetupPageScenarioHeaderPicksTheFake(t *testing.T) {
	c := newUIClient(t, CaptureScenarios()["fresh"])
	c.s.Scenarios = CaptureScenarios()
	_, st := c.do("GET", "/api/state", "", map[string]string{"X-Setup-Scenario": "uninstall"})
	if st["mode"] != string(OpUninstall) || len(st["data"].([]any)) != 2 {
		t.Fatalf("%v", st)
	}
	_, st = c.do("GET", "/api/state", "", map[string]string{"X-Setup-Scenario": "no-such"})
	if st["mode"] != string(OpInstall) {
		t.Fatalf("unknown scenario falls back: %v", st)
	}
}

// Every screenshot scenario is a sane run: a version, a mode, checks where the screen shows them, and a result that ends.
func TestCaptureScenariosAreSane(t *testing.T) {
	want := []string{"fresh", "preflight-fail", "progress", "done", "failed", "upgrade", "repair", "uninstall", "uninstall-left"}
	sc := CaptureScenarios()
	if len(sc) != len(want) {
		t.Fatalf("%d scenarios", len(sc))
	}
	for _, name := range want {
		be, ok := sc[name]
		if !ok {
			t.Fatalf("no %s", name)
		}
		st := be.State()
		if st.Version != "1.0.0" || !st.Capture || st.LogPath == "" {
			t.Fatalf("%s: %+v", name, st)
		}
		req := WorkRequest{Op: st.Mode, Options: Options{Scope: ScopeUser, Location: st.Defaults.UserLocation}}
		checks := be.Preflight(req)
		if st.Mode != OpUninstall && len(checks) == 0 {
			t.Fatalf("%s has no checks", name)
		}
		if (name == "preflight-fail") != Blocked(checks) {
			t.Fatalf("%s: blocked=%v", name, Blocked(checks))
		}
		if ch := be.Changes(req); len(ch.WillChange) == 0 || len(ch.WillNotChange) == 0 {
			t.Fatalf("%s: %+v", name, ch)
		}
		if name == "progress" || name == "preflight-fail" {
			continue
		}
		res := be.Run(context.Background(), req, func(StepEvent) {}, func(string) {})
		if res.Outcome == "" || res.Message == "" {
			t.Fatalf("%s: %+v", name, res)
		}
		if (name == "failed") != (res.Outcome == OutcomeRolledBack) {
			t.Fatalf("%s: %s", name, res.Outcome)
		}
	}
}

// The real backend runs a just-for-me install in this process and a machine install through the elevated worker.
func TestLocalBackendRunsInPlaceOrThroughTheWorker(t *testing.T) {
	e := newEnv(t, "1.0.0")
	be := &LocalBackend{R: e.r, Mode: OpInstall}
	st := be.State()
	if st.Version != "1.0.0" || st.Installed != nil || st.Defaults.UserLocation == "" || st.Defaults.Blackbox.Port == 0 {
		t.Fatalf("%+v", st)
	}
	ch := be.Changes(WorkRequest{Op: OpInstall, Options: e.machineOpts()})
	if !ch.NeedsAdmin {
		t.Fatal("a machine install needs approval")
	}
	// No elevator: a machine install is refused without changing anything.
	before := e.snapshot()
	res := be.Run(context.Background(), WorkRequest{Op: OpInstall, Options: e.machineOpts()}, func(StepEvent) {}, func(string) {})
	if res.Outcome == OutcomeDone || e.snapshot() != before {
		t.Fatalf("%+v", res)
	}
	el := &pipeElevator{r: e.r, prepare: WorkerPrepare("S-1-5-21-1000", e.dirs.UserProfile)}
	be.Elev = el
	var l eventLog
	res = be.Run(context.Background(), WorkRequest{Op: OpInstall, Options: e.machineOpts()}, l.ev, l.ln)
	if res.Outcome != OutcomeDone || len(l.evs) == 0 || len(l.lines) == 0 {
		t.Fatalf("%+v", res)
	}
	if w := <-el.served; w.Outcome != OutcomeDone {
		t.Fatalf("worker: %+v", w)
	}
	st = be.State()
	if st.Mode != OpRepair || st.Installed == nil || st.Installed.Scope != ScopeMachine {
		t.Fatalf("after install: %+v", st)
	}
	// Uninstall offers the shared data folder with its size.
	var cases *UIDataPlace
	for i := range st.Data {
		if st.Data[i].Kind == "cases" {
			cases = &st.Data[i]
		}
	}
	if cases == nil || !strings.HasSuffix(cases.Folder, SharedFolderName) {
		t.Fatalf("%+v", st.Data)
	}
}

func TestLocalBackendUserInstallStaysUnelevated(t *testing.T) {
	e := newEnv(t, "1.0.0")
	be := &LocalBackend{R: e.r, Mode: OpInstall, Elev: &pipeElevator{err: ErrUACCancelled}}
	if be.Changes(WorkRequest{Op: OpInstall, Options: e.userOpts()}).NeedsAdmin {
		t.Fatal("a just-for-me install needs no approval")
	}
	res := be.Run(context.Background(), WorkRequest{Op: OpInstall, Options: e.userOpts()}, func(StepEvent) {}, func(string) {})
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	// Cancel at the Windows approval for a machine choice changes nothing.
	e2 := newEnv(t, "1.0.0")
	be2 := &LocalBackend{R: e2.r, Mode: OpInstall, Elev: &pipeElevator{err: ErrUACCancelled}}
	before := e2.snapshot()
	res = be2.Run(context.Background(), WorkRequest{Op: OpInstall, Options: e2.machineOpts()}, func(StepEvent) {}, func(string) {})
	if res.Outcome != OutcomeCancelledUAC || res.Outcome.ExitCode() != 3 || e2.snapshot() != before {
		t.Fatalf("%+v", res)
	}
}

func TestSetupPageIdleEndsOnlyWhenThePageIsQuietAndNothingRuns(t *testing.T) {
	c := newUIClient(t, CaptureScenarios()["fresh"])
	t0 := time.Now()
	if c.s.Idle(t0, time.Minute) {
		t.Fatal("idle before the clock started")
	}
	if !c.s.Idle(t0.Add(2*time.Minute), time.Minute) {
		t.Fatal("a page that never asked anything is not idle")
	}
	c.do("GET", "/api/state", "", nil) // the page's ping
	if c.s.Idle(time.Now().Add(30*time.Second), time.Minute) {
		t.Fatal("idle right after a ping")
	}
	// A stranger's request (no key) does not keep setup alive.
	c.s.mu.Lock()
	c.s.lastSeen = time.Now().Add(-2 * time.Minute)
	c.s.mu.Unlock()
	c.do("GET", "/api/state", "", map[string]string{"X-Setup-Key": "wrong"})
	if !c.s.Idle(time.Now(), time.Minute) {
		t.Fatal("a request without the key kept setup alive")
	}
	// While a run is going, setup never ends on its own.
	c.s.mu.Lock()
	c.s.running = true
	c.s.mu.Unlock()
	if c.s.Idle(time.Now().Add(time.Hour), time.Minute) {
		t.Fatal("idle during a run")
	}
}

func TestLocalBackendPageCannotAskForADowngrade(t *testing.T) {
	e := newEnv(t, "2.0.0")
	e.r.Payload = testPayload(t, "2.0.0", true)
	e.mustRun(OpInstall, e.userOpts(), UninstallOptions{})
	// The older setup: the page sends allowDowngrade, which is ignored.
	e.r.Payload = testPayload(t, "1.0.0", true)
	e.f.Versions[strings.ToLower(filepath.Join(e.userOpts().Location, AppExeName))] = "unravis 1.0.0"
	be := &LocalBackend{R: e.r, Mode: OpInstall}
	c := newUIClient(t, be)
	before := e.snapshot()
	if code, _ := c.do("POST", "/api/run", `{"op":"upgrade","allowDowngrade":true,"options":{"scope":"user"}}`, nil); code != http.StatusOK && code != http.StatusAccepted {
		t.Fatalf("run: %d", code)
	}
	out := c.waitResult()
	res := out["result"].(map[string]any)
	if res["outcome"] != string(OutcomePreflightFailed) || e.snapshot() != before {
		t.Fatalf("the page downgraded: %v", res)
	}
	// --allow-downgrade on the command line is the way.
	be.AllowDowngrade = true
	r := be.Run(context.Background(), WorkRequest{Op: OpUpgrade, Options: e.userOpts()}, func(StepEvent) {}, func(string) {})
	if r.Outcome != OutcomeDone {
		t.Fatalf("with --allow-downgrade: %+v", r)
	}
}

// The "Left behind" screen has a scenario of its own (D-C-13): a done-with-leftovers uninstall with a list that is shown, and the exit
// code the command line gives it.
func TestCaptureUninstallLeftReachesTheLeftBehindScreen(t *testing.T) {
	be, ok := CaptureScenarios()["uninstall-left"]
	if !ok {
		t.Fatal("no uninstall-left scenario")
	}
	if st := be.State(); st.Mode != OpUninstall || st.Installed == nil {
		t.Fatalf("state = %+v", st)
	}
	var events []StepEvent
	res := be.Run(context.Background(), WorkRequest{Op: OpUninstall}, func(e StepEvent) { events = append(events, e) }, func(string) {})
	if res.Outcome != OutcomeLeftovers || len(res.Left) == 0 {
		t.Fatalf("result = %+v", res)
	}
	if res.Outcome.ExitCode() != 5 {
		t.Errorf("exit code = %d, want 5", res.Outcome.ExitCode())
	}
	sawLeft := false
	for _, e := range events {
		if e.State == StepLeft && e.ID == "program-files" {
			sawLeft = true
		}
	}
	if !sawLeft {
		t.Errorf("no step ended as left: %+v", events)
	}
	// Through the page's own endpoints, as the screenshot build serves it.
	c := newUIClient(t, be)
	if code, _ := c.do("POST", "/api/run", `{"op":"uninstall"}`, nil); code != http.StatusAccepted {
		t.Fatalf("run: %d", code)
	}
	out := c.waitResult()
	r := out["result"].(map[string]any)
	left, _ := r["left"].([]any)
	if r["outcome"] != string(OutcomeLeftovers) || len(left) != 2 {
		t.Fatalf("result over HTTP = %v", r)
	}
}

// The list shown before approval covers everything the plan creates (D-C-10).
func TestInstallChangesNameTheShortcutAndTheInstalledAppsEntry(t *testing.T) {
	d := captureDirs()
	cases := []struct {
		name string
		op   Op
		opts Options
	}{
		{"just for me", OpInstall, Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, d)}},
		{"everyone", OpInstall, Options{Scope: ScopeMachine, Location: DefaultLocation(ScopeMachine, d)}},
		{"repair", OpRepair, Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, d)}},
		{"upgrade", OpUpgrade, Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, d)}},
	}
	for _, c := range cases {
		ch := DescribeChanges(WorkRequest{Op: c.op, Options: c.opts}, nil, d, &FakeServices{}, false)
		var shortcut, arp int
		for _, line := range ch.WillChange {
			switch line {
			case "Start menu shortcut":
				shortcut++
			case "Installed apps entry":
				arp++
			}
		}
		if shortcut != 1 || arp != 1 {
			t.Errorf("%s: shortcut %d, installed apps entry %d in %q", c.name, shortcut, arp, ch.WillChange)
		}
		if len(ch.WillNotChange) != 4 {
			t.Errorf("%s: will-not-change = %q", c.name, ch.WillNotChange)
		}
	}
}

// The uninstall list keeps its own wording.
func TestUninstallChangesStillNameTheShortcutAndTheEntry(t *testing.T) {
	d := captureDirs()
	o := Options{Scope: ScopeUser, Location: DefaultLocation(ScopeUser, d)}
	ch := DescribeChanges(WorkRequest{Op: OpUninstall, Options: o}, nil, d, &FakeServices{}, false)
	found := false
	for _, line := range ch.WillChange {
		if line == "Remove the Start menu shortcut and the Installed apps entry" {
			found = true
		}
		if line == "Start menu shortcut" || line == "Installed apps entry" {
			t.Errorf("an install line leaked into the uninstall list: %q", line)
		}
	}
	if !found {
		t.Errorf("uninstall list = %q", ch.WillChange)
	}
}
