package blackbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type fakeElev struct {
	calls   []fakeCall
	answer  string
	err     error
	cfgSeen string
}

type fakeCall struct {
	exe      string
	args     []string
	elevated bool
}

func (f *fakeElev) Run(_ context.Context, exe string, args []string, elevated bool) ([]byte, error) {
	f.calls = append(f.calls, fakeCall{exe, args, elevated})
	for i, a := range args {
		if a == "--config" && i+1 < len(args) {
			b, err := os.ReadFile(args[i+1])
			if err == nil {
				f.cfgSeen = string(b)
			}
		}
	}
	return []byte(f.answer), f.err
}

func newThisPC(f *fakeElev, t *testing.T) *ThisPC {
	return &ThisPC{Elev: f, Program: `C:\Unravis\unravis-blackbox.exe`, TempRoot: t.TempDir()}
}

func TestInstallRunsTheHelperElevatedWithTheSettingsInAFile(t *testing.T) {
	f := &fakeElev{answer: `{"ok":true,"readKey":"ubk-TESTKEY","service":"UnravisBlackbox","folder":"D:\\UnravisBlackbox","port":7788,"name":"web-b","exePath":"C:\\PF\\u.exe","configPath":"C:\\PF\\blackbox.json"}` + "\n"}
	tp := newThisPC(f, t)
	out, err := tp.Install(t.Context(), packCfg())
	if err != nil {
		t.Fatal(err)
	}
	if out.ReadKey != "ubk-TESTKEY" || out.Name != "web-b" || out.Port != 7788 {
		t.Fatalf("%+v", out)
	}
	c := f.calls[0]
	if !c.elevated || c.args[0] != "install" || c.args[1] != "--json" || c.args[2] != "--config" {
		t.Fatalf("%+v", c)
	}
	var cfg Config
	if err := json.Unmarshal([]byte(f.cfgSeen), &cfg); err != nil || cfg.Name != "web-b" {
		t.Fatalf("settings file: %q %v", f.cfgSeen, err)
	}
	if _, err := os.Stat(c.args[3]); err == nil {
		t.Fatal("the settings file was left behind")
	}
	for _, a := range c.args {
		if strings.Contains(a, "web-b") {
			t.Fatalf("settings in the arguments: %v", c.args)
		}
	}
}

func TestInstallFailureReasonsComeThroughInPlainWords(t *testing.T) {
	for _, reason := range []string{"no-admin", "service-exists", "port-busy", "folder", "antivirus", "other"} {
		f := &fakeElev{answer: `{"ok":false,"reason":"` + reason + `","message":"plain words here"}`}
		_, err := newThisPC(f, t).Install(t.Context(), packCfg())
		var te *ThisPCError
		if !errors.As(err, &te) || te.Reason != reason || te.Message == "" {
			t.Errorf("%s: %v", reason, err)
		}
		if what, next := FailureWords(reason); what == "" || next == "" {
			t.Errorf("%s has no words", reason)
		}
	}
}

func TestCancelledPermissionPromptChangesNothing(t *testing.T) {
	f := &fakeElev{err: ErrDeclined}
	_, err := newThisPC(f, t).Install(t.Context(), packCfg())
	var te *ThisPCError
	if !errors.As(err, &te) || te.Reason != ReasonDeclined || !errors.Is(err, ErrDeclined) {
		t.Fatalf("%v", err)
	}
}

func TestAnAnswerThatIsNotJSONIsNeverSuccess(t *testing.T) {
	for _, ans := range []string{"", "Installed. The service is running.", "{broken", "\n\n"} {
		f := &fakeElev{answer: ans}
		if _, err := newThisPC(f, t).Install(t.Context(), packCfg()); err == nil {
			t.Errorf("%q passed as success", ans)
		}
	}
}

func TestInstallWithoutAReadKeyIsAFailure(t *testing.T) {
	f := &fakeElev{answer: `{"ok":true,"name":"web-b"}`}
	if _, err := newThisPC(f, t).Install(t.Context(), packCfg()); err == nil {
		t.Fatal("no key was accepted")
	}
}

func TestInvalidSettingsNeverReachTheHelper(t *testing.T) {
	f := &fakeElev{}
	bad := packCfg()
	bad.Folder = `C:\Windows\System32`
	_, err := newThisPC(f, t).Install(t.Context(), bad)
	var te *ThisPCError
	if !errors.As(err, &te) || te.Reason != ReasonInvalid || len(f.calls) != 0 {
		t.Fatalf("%v calls=%d", err, len(f.calls))
	}
}

func TestStartStopApplyUseTheRightCommands(t *testing.T) {
	f := &fakeElev{answer: `{"ok":true,"restarted":true}`}
	tp := newThisPC(f, t)
	if err := tp.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := tp.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	re, err := tp.Apply(t.Context(), packCfg())
	if err != nil || !re {
		t.Fatalf("%v %v", re, err)
	}
	if f.calls[0].args[0] != "start" || f.calls[1].args[0] != "stop" || f.calls[2].args[0] != "apply" {
		t.Fatalf("%+v", f.calls)
	}
	for _, c := range f.calls {
		if !c.elevated {
			t.Errorf("%v ran without a permission prompt", c.args)
		}
	}
}

// ---- apply through the helper, with real temporary files (W3-01) ----

// helperElev stands in for the elevated helper program: it runs the real
// Manager with the args ThisPC builds, against the fake service manager and
// environment of service_test.go, and answers in the helper's JSON protocol.
type helperElev struct {
	m     *Manager
	calls []fakeCall
}

func (h *helperElev) Run(_ context.Context, exe string, args []string, elevated bool) ([]byte, error) {
	h.calls = append(h.calls, fakeCall{exe, args, elevated})
	cfgPath := ""
	for i, a := range args {
		if a == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
		}
	}
	line := func(v map[string]any) []byte {
		b, _ := json.Marshal(v)
		return append(b, '\n')
	}
	fail := func(err error) []byte {
		reason := string(ReasonOther)
		var ie *InstallError
		var ae *ApplyError
		switch {
		case errors.As(err, &ie):
			reason = string(ie.Reason)
		case errors.As(err, &ae):
			reason = string(ae.Reason)
		case errors.Is(err, ErrNotInstalled):
			reason = ReasonNotInstall
		case errors.Is(err, ErrInvalidConfig):
			reason = ReasonInvalid
		}
		return line(map[string]any{"ok": false, "reason": reason, "message": err.Error()})
	}
	switch Op(args[0]) {
	case OpInstall:
		cfg, err := LoadConfig(cfgPath)
		if err != nil {
			return fail(err), nil
		}
		res, err := h.m.Install(cfg)
		if err != nil {
			return fail(err), nil
		}
		return line(map[string]any{"ok": true, "readKey": res.ReadKey, "name": cfg.Name, "folder": res.Folder,
			"port": res.Port, "exePath": res.ExePath, "configPath": res.ConfigPath}), nil
	case OpApply:
		res, err := h.m.Apply(cfgPath)
		if err != nil {
			return fail(err), nil
		}
		return line(map[string]any{"ok": true, "restarted": res.Restarted}), nil
	case OpStart:
		if err := h.m.Start(); err != nil {
			return fail(err), nil
		}
		return line(map[string]any{"ok": true}), nil
	case OpStop:
		if err := h.m.Stop(); err != nil {
			return fail(err), nil
		}
		return line(map[string]any{"ok": true}), nil
	}
	return fail(errors.New("unsupported operation")), nil
}

// W3-01 end to end: install target A through ThisPC, apply target B through
// ThisPC's short-lived settings file, and the INSTALLED config the service
// command line names must hold B and not A — for a running and a stopped
// recorder. The temporary file is gone afterwards and is never what the
// service reads.
func TestApplyThroughTheHelperSavesTheInstalledConfigNotTheTemporaryFile(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		name := "running"
		if stopped {
			name = "stopped"
		}
		t.Run(name, func(t *testing.T) {
			g := newSvcRig(t)
			h := &helperElev{m: g.m}
			tp := &ThisPC{Elev: h, Program: g.env.exe, TempRoot: t.TempDir()}
			cfgA := g.cfg
			cfgA.Targets = []Target{{Kind: "appPool", Name: "PoolA"}}
			out, err := tp.Install(t.Context(), cfgA)
			if err != nil || out.ConfigPath != g.p.ConfigPath {
				t.Fatalf("%+v %v", out, err)
			}
			if stopped {
				if err := tp.Stop(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			cfgB := cfgA
			cfgB.Targets = []Target{{Kind: "service", Name: "SvcB"}}
			restarted, err := tp.Apply(t.Context(), cfgB)
			if err != nil || restarted != !stopped {
				t.Fatalf("restarted=%v %v", restarted, err)
			}
			// The installed config selects B, not A.
			svcPath := serviceConfigPath(g.svc.spec)
			if svcPath != g.p.ConfigPath {
				t.Fatalf("service reads %q, want the installed %q", svcPath, g.p.ConfigPath)
			}
			got, err := LoadConfig(svcPath)
			if err != nil || strings.Join(targetNames(got), ",") != "service:SvcB" {
				t.Fatalf("installed config: %+v %v", got.Targets, err)
			}
			raw, _ := os.ReadFile(svcPath)
			if !strings.Contains(string(raw), "SvcB") || strings.Contains(string(raw), "PoolA") {
				t.Fatalf("installed config text: %s", raw)
			}
			// The temporary settings file is gone and was never the service's.
			applyCall := h.calls[len(h.calls)-1]
			tmp := ""
			for i, a := range applyCall.args {
				if a == "--config" && i+1 < len(applyCall.args) {
					tmp = applyCall.args[i+1]
				}
			}
			if tmp == "" || samePath(tmp, svcPath) {
				t.Fatalf("apply used %q", tmp)
			}
			if _, err := os.Stat(tmp); err == nil {
				t.Fatal("the temporary settings file was left behind")
			}
			if strings.Contains(strings.Join(g.svc.spec.Args, " "), tmp) {
				t.Fatal("the service reads the temporary file")
			}
			if stopped && g.svc.state != SvcStopped {
				t.Fatalf("a stopped recorder was started: %s", g.svc.state)
			}
		})
	}
}

// W3-01: a save failure travels back as the fixed save-failed reason, the
// recorder keeps running and the old settings stay.
func TestApplySaveFailureThroughTheHelperKeepsTheRecorderRunning(t *testing.T) {
	g := newSvcRig(t)
	h := &helperElev{m: g.m}
	tp := &ThisPC{Elev: h, Program: g.env.exe, TempRoot: t.TempDir()}
	cfgA := g.cfg
	cfgA.Targets = []Target{{Kind: "appPool", Name: "PoolA"}}
	if _, err := tp.Install(t.Context(), cfgA); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(g.p.ConfigPath+".bak", 0o755); err != nil {
		t.Fatal(err)
	}
	cfgB := cfgA
	cfgB.Targets = []Target{{Kind: "service", Name: "SvcB"}}
	_, err := tp.Apply(t.Context(), cfgB)
	var te *ThisPCError
	if !errors.As(err, &te) || te.Reason != string(ReasonSaveFailed) {
		t.Fatalf("%v", err)
	}
	if g.svc.state != SvcRunning {
		t.Fatalf("the recorder was stopped although the save failed: %s", g.svc.state)
	}
	got, err := LoadConfig(g.p.ConfigPath)
	if err != nil || strings.Join(targetNames(got), ",") != "appPool:PoolA" {
		t.Fatalf("old settings not kept: %+v %v", got.Targets, err)
	}
	if what, next := FailureWords(te.Reason); what == "" || next == "" {
		t.Fatalf("%s has no words", te.Reason)
	}
}

func TestApplyFailureReasonsComeThroughInPlainWords(t *testing.T) {
	for _, reason := range []string{"save-failed", "restart-failed"} {
		f := &fakeElev{answer: `{"ok":false,"reason":"` + reason + `","message":"plain words here"}`}
		_, err := newThisPC(f, t).Apply(t.Context(), packCfg())
		var te *ThisPCError
		if !errors.As(err, &te) || te.Reason != reason || te.Message == "" {
			t.Errorf("%s: %v", reason, err)
		}
		if what, next := FailureWords(reason); what == "" || next == "" {
			t.Errorf("%s has no words", reason)
		}
	}
}

func TestRemoveKeepsTheRecordingsUnlessTheNameIsTyped(t *testing.T) {
	f := &fakeElev{answer: `{"ok":true,"serviceRemoved":true,"recordingsDeleted":false}`}
	tp := newThisPC(f, t)
	if _, err := tp.Remove(t.Context(), "web-b", false, ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.calls[0].args, " "); strings.Contains(got, "delete") {
		t.Fatalf("keep deleted: %s", got)
	}
	// A typo in the name never gets as far as a permission prompt.
	n := len(f.calls)
	for _, typed := range []string{"", "web-bb", "WEB-B", "web-b "} {
		_, err := tp.Remove(t.Context(), "web-b", true, typed)
		var te *ThisPCError
		if !errors.As(err, &te) || te.Reason != ReasonConfirm {
			t.Errorf("%q: %v", typed, err)
		}
	}
	if len(f.calls) != n {
		t.Fatal("the helper was started with a wrong name")
	}
	f.answer = `{"ok":true,"serviceRemoved":true,"recordingsDeleted":true,"filesDeleted":42}`
	out, err := tp.Remove(t.Context(), "web-b", true, "web-b")
	if err != nil || !out.RecordingsDeleted || out.FilesDeleted != 42 {
		t.Fatalf("%+v %v", out, err)
	}
	got := strings.Join(f.calls[len(f.calls)-1].args, " ")
	if !strings.Contains(got, "--delete-recordings --confirm-name web-b") {
		t.Fatalf("%s", got)
	}
}

func TestSnapshotNeedsNoPermissionPrompt(t *testing.T) {
	f := &fakeElev{answer: `{"ok":true,"dir":"D:\\UnravisBlackbox\\snapshots\\web-b-2026","bytes":1048576,"recordings":3,"logs":0}`}
	out, err := newThisPC(f, t).Snapshot(t.Context())
	if err != nil || out.Recordings != 3 || out.Bytes != 1048576 {
		t.Fatalf("%+v %v", out, err)
	}
	if f.calls[0].elevated || f.calls[0].args[0] != "snapshot" {
		t.Fatalf("%+v", f.calls[0])
	}
	f.answer = `{"ok":false,"reason":"drive-full","message":"x"}`
	_, err = newThisPC(f, t).Snapshot(t.Context())
	var te *ThisPCError
	if !errors.As(err, &te) || te.Reason != "drive-full" {
		t.Fatalf("%v", err)
	}
}

func TestWithoutAnElevatorOrProgramNothingRuns(t *testing.T) {
	var nilPC *ThisPC
	if err := nilPC.Start(t.Context()); err == nil {
		t.Fatal("nil ThisPC ran")
	}
	f := &fakeElev{}
	if err := (&ThisPC{Elev: f}).Stop(t.Context()); err == nil || len(f.calls) != 0 {
		t.Fatalf("no program: %v", err)
	}
}

func TestHelperErrorsNeverCarryTheKeyOrSettings(t *testing.T) {
	f := &fakeElev{answer: `{"ok":false,"reason":"other","message":"oops"}`, err: nil}
	_, err := newThisPC(f, t).Install(t.Context(), packCfg())
	if err == nil || strings.Contains(err.Error(), "ubk-") {
		t.Fatal(err)
	}
}

// ---- scan this PC ----

type scanSvc struct {
	st  SvcState
	err error
}

func (s scanSvc) State(string) (SvcState, error) { return s.st, s.err }
func (scanSvc) Create(ServiceSpec) error         { panic("scan must not create") }
func (scanSvc) Start(string) error               { panic("scan must not start") }
func (scanSvc) Stop(string) error                { panic("scan must not stop") }
func (scanSvc) Delete(string) error              { panic("scan must not delete") }

type scanEnv struct {
	Env
	dir string
	err error
}

func (e scanEnv) ProgramDir() (string, error) { return e.dir, e.err }

func TestScanThisPCFindsAnInstalledBlackbox(t *testing.T) {
	cfg := packCfg()
	cfg.Port = 7801
	load := func(path string) (Config, error) {
		if !strings.HasSuffix(path, ConfigFileName) {
			t.Errorf("read %s", path)
		}
		return cfg, nil
	}
	f, err := ScanThisPC(scanSvc{st: SvcRunning}, scanEnv{dir: `C:\PF\Unravis Blackbox`}, load)
	if err != nil || !f.Installed || !f.Running || f.Name != "web-b" || f.Port != 7801 || f.LocalURL() != "http://127.0.0.1:7801" {
		t.Fatalf("%+v %v", f, err)
	}
	f, _ = ScanThisPC(scanSvc{st: SvcStopped}, scanEnv{dir: "x"}, load)
	if !f.Installed || f.Running {
		t.Fatalf("%+v", f)
	}
}

func TestScanThisPCNoneFoundAndUnreadable(t *testing.T) {
	f, err := ScanThisPC(scanSvc{st: SvcNotInstalled}, scanEnv{}, nil)
	if err != nil || f.Installed || f.LocalURL() != "" {
		t.Fatalf("%+v %v", f, err)
	}
	f, _ = ScanThisPC(scanSvc{st: SvcRunning}, scanEnv{dir: "x"}, func(string) (Config, error) { return Config{}, errors.New("denied") })
	if !f.Installed || !f.ConfigUnreadable || f.Name != "" {
		t.Fatalf("%+v", f)
	}
	f, _ = ScanThisPC(scanSvc{st: SvcRunning}, scanEnv{err: errors.New("no folder")}, nil)
	if !f.Installed || !f.ConfigUnreadable {
		t.Fatalf("%+v", f)
	}
	// Not being able to ask is not "none found".
	if _, err := ScanThisPC(scanSvc{err: errors.New("scm")}, scanEnv{}, nil); err == nil {
		t.Fatal("a service manager error was hidden")
	}
}

func TestUpdateRunsTheHelperElevatedAndReportsShortHashes(t *testing.T) {
	old, nu := strings.Repeat("a", 64), strings.Repeat("b", 64)
	f := &fakeElev{answer: `{"ok":true,"updated":true,"wasRunning":true,"restarted":true,"oldHash":"` + old + `","newHash":"` + nu + `","version":"1.4.0"}`}
	out, err := newThisPC(f, t).Update(t.Context())
	if err != nil || !out.Updated || !out.WasRunning || !out.Restarted || out.NewVersion != "1.4.0" || out.OldHash != old[:12] || out.NewHash != nu[:12] {
		t.Fatalf("%+v %v", out, err)
	}
	if len(f.calls) != 1 || !f.calls[0].elevated || f.calls[0].args[0] != "update" || strings.Contains(strings.Join(f.calls[0].args, " "), "--config") {
		t.Fatalf("%+v", f.calls)
	}
}

func TestUpdateFailureKeepsThePlainReason(t *testing.T) {
	f := &fakeElev{answer: `{"ok":false,"reason":"update-failed","message":"The updated recorder did not start."}`}
	_, err := newThisPC(f, t).Update(t.Context())
	var te *ThisPCError
	if !errors.As(err, &te) || te.Reason != string(ReasonUpdateFailed) || te.Message != "The updated recorder did not start." {
		t.Fatalf("%v", err)
	}
	what, next := FailureWords(te.Reason)
	if what == GenericFailure || next == "" {
		t.Fatal("no plain words for update-failed")
	}
}

func TestUpdateOpNeedsAdminAndTheGuard(t *testing.T) {
	if !OpUpdate.NeedsAdmin() || !OpUpdate.NeedsGuard() {
		t.Fatal("update must be elevated and guarded")
	}
}
