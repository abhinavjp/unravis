package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"api-audit-explorer/internal/setup"
	"api-audit-explorer/internal/update"
)

// BL-183 (b): a visible notice before the app is closed and before the Windows prompt. These tests use the fakes, so they run on Linux.

// pendingMachineRun prepares a normal (not elevated) run with a trusted machine journal that lists a file, and a running app beside the setup
// program. It returns the listed file and the app's process id.
func pendingMachineRun(t *testing.T, rg *testRig) (made string, appPID uint32) {
	t.Helper()
	rg.f.Mach.Admin = false
	made = filepath.Join(rg.root, "half.txt")
	if err := os.WriteFile(made, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	putJournal(t, setup.MachineJournalDir(rg.dirs), journalBody(t, made))
	trustMachine(t, rg)
	exe, _ := rg.env.setupExe()
	app := filepath.Join(filepath.Dir(exe), "unravis.exe")
	if err := os.WriteFile(app, []byte("app"), 0o600); err != nil {
		t.Fatal(err)
	}
	return made, rg.f.Proc.Run(app)
}

func askAppRunning(rg *testRig, pid uint32) bool {
	list, _ := rg.f.Proc.List()
	for _, p := range list {
		if p.PID == pid {
			return true
		}
	}
	return false
}

func TestRecoverAsksBeforeClosingAndBeforeThePrompt(t *testing.T) {
	rg := newRig(t)
	_, pid := pendingMachineRun(t, rg)
	var order []string
	rg.env.ask = func(title, text string) bool {
		order = append(order, "ask")
		if !askAppRunning(rg, pid) {
			t.Error("the app was closed before the notice")
		}
		if title != "Unravis" || !strings.Contains(text, "cut off") || !strings.Contains(text, "Click OK, then answer Yes") || !strings.Contains(text, "Click Cancel") {
			t.Errorf("notice text: %q %q", title, text)
		}
		return true
	}
	rg.env.elevateWait = func(string, []string) (int, error) {
		order = append(order, "elevate")
		// setup compares paths the Windows way (backslashes), so the fake can close the app only on Windows (CI); the order of the two
		// calls is what the other platforms check.
		if runtime.GOOS == "windows" && askAppRunning(rg, pid) {
			t.Error("the app was still running at the prompt")
		}
		return 0, nil
	}
	if code := run([]string{"--recover"}, rg.env); code != 0 {
		t.Fatalf("%d %s", code, rg.out.String())
	}
	if strings.Join(order, ",") != "ask,elevate" {
		t.Fatalf("order %v", order)
	}
}

func TestRecoverCancelAtTheNoticeStopsTheMachinePart(t *testing.T) {
	rg := newRig(t)
	made, pid := pendingMachineRun(t, rg)
	rg.env.ask = func(string, string) bool { return false }
	rg.env.elevateWait = func(string, []string) (int, error) { t.Fatal("no prompt after Cancel"); return 0, nil }
	code := run([]string{"--recover"}, rg.env)
	if code != setup.OutcomeCancelledUAC.ExitCode() || code != 3 {
		t.Fatalf("exit code %d", code)
	}
	if !askAppRunning(rg, pid) {
		t.Error("the app was closed although the person said Cancel")
	}
	if !setupJournalStillThere(rg) {
		t.Error("the machine journal was not kept")
	}
	if _, err := os.Stat(made); err != nil {
		t.Error("something was put back without approval")
	}
	got, ok := update.ReadApplyResult(filepath.Join(rg.dirs.LocalAppData, "Unravis", "update-result"))
	if !ok || got.Outcome != "failed" || !strings.Contains(got.Message, "not approved") || !strings.Contains(got.Message, "Repair") {
		t.Fatalf("%+v %v", got, ok)
	}
}

func TestRecoverCancelStillReplaysThePersonsOwnJournal(t *testing.T) {
	rg := newRig(t)
	_, _ = pendingMachineRun(t, rg)
	mine := filepath.Join(rg.root, "mine.txt")
	if err := os.WriteFile(mine, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	putJournal(t, setup.UserJournalDir(rg.dirs), journalBody(t, mine))
	rg.env.ask = func(string, string) bool { return false }
	rg.env.elevateWait = func(string, []string) (int, error) { t.Fatal("no prompt after Cancel"); return 0, nil }
	if code := run([]string{"--recover"}, rg.env); code != 3 {
		t.Fatalf("exit code %d", code)
	}
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Error("the person's own journal was not replayed")
	}
	if !setupJournalStillThere(rg) {
		t.Error("the machine journal was not kept")
	}
}

func TestRecoverElevatedCopyShowsNoNotice(t *testing.T) {
	rg := newRig(t)
	made := filepath.Join(rg.root, "half.txt")
	if err := os.WriteFile(made, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	putJournal(t, setup.MachineJournalDir(rg.dirs), journalBody(t, made))
	trustMachine(t, rg)
	rg.env.ask = func(string, string) bool { t.Fatal("the elevated copy must never ask"); return false }
	if code := run([]string{"--recover", "--no-restart"}, rg.env); code != 0 {
		t.Fatalf("%d %s", code, rg.out.String())
	}
	if _, err := os.Stat(made); !os.IsNotExist(err) {
		t.Fatal("the administrator run did not replay the machine journal")
	}
}

func TestRecoverNoNoticeWhenNothingPendingOrWhenTheJournalIsNotTrusted(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	rg.env.ask = func(string, string) bool { t.Fatal("nothing pending: no notice"); return false }
	if code := run([]string{"--recover"}, rg.env); code != 0 {
		t.Fatalf("%d", code)
	}
	// A machine journal in a folder others can change is never used, so no notice either.
	putJournal(t, setup.MachineJournalDir(rg.dirs), journalBody(t, filepath.Join(rg.root, "never.txt")))
	rg.env.elevateWait = func(string, []string) (int, error) { t.Fatal("no prompt"); return 0, nil }
	if code := run([]string{"--recover"}, rg.env); code != 0 {
		t.Fatalf("%d", code)
	}
}

// The default (no environment variable): production has no box, so --recover goes straight to the prompt, as before the box existed.
func TestRecoverNilAskProceeds(t *testing.T) {
	rg := newRig(t)
	pendingMachineRun(t, rg)
	rg.env.ask = nil
	calls := 0
	rg.env.elevateWait = func(string, []string) (int, error) { calls++; return 0, nil }
	if code := run([]string{"--recover"}, rg.env); code != 0 || calls != 1 {
		t.Fatalf("%d, prompt calls %d", code, calls)
	}
}

func TestRecoverOKThenNoAtThePromptIsReported(t *testing.T) {
	rg := newRig(t)
	pendingMachineRun(t, rg)
	rg.env.ask = func(string, string) bool { return true }
	rg.env.elevateWait = func(string, []string) (int, error) { return 0, setup.ErrUACCancelled }
	if code := run([]string{"--recover"}, rg.env); code == 0 {
		t.Fatal("a refused prompt is not success")
	}
	got, _ := update.ReadApplyResult(filepath.Join(rg.dirs.LocalAppData, "Unravis", "update-result"))
	if got.Outcome != "failed" {
		t.Fatalf("%+v", got)
	}
}

// The administrator part cannot say why it failed, so the normal run names the journal file and what to do, and says nothing is deleted.
// The result note is cut at 400 bytes (internal/update), so the advice must be in the note itself and the whole text is in the log.
func TestRecoverFailedAdministratorPartNamesTheJournalAndAWayOut(t *testing.T) {
	rg := newRig(t)
	pendingMachineRun(t, rg)
	rg.env.ask = func(string, string) bool { return true }
	rg.env.elevateWait = func(string, []string) (int, error) { return 1, nil }
	if code := run([]string{"--recover"}, rg.env); code == 0 {
		t.Fatal("a failed administrator part is not success")
	}
	got, _ := update.ReadApplyResult(filepath.Join(rg.dirs.LocalAppData, "Unravis", "update-result"))
	for _, want := range []string{"move that file", "do not delete it"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("the note lacks %q: %s", want, got.Message)
		}
	}
	path := filepath.Join(setup.MachineJournalDir(rg.dirs), setup.JournalName)
	logs, _ := filepath.Glob(filepath.Join(rg.dirs.LocalAppData, "Unravis", "setup-logs", "*.log"))
	if len(logs) != 1 {
		t.Fatalf("logs %v", logs)
	}
	full, _ := os.ReadFile(logs[0])
	for _, want := range []string{path, "move that file", "do not delete it", "Repair"} {
		if !strings.Contains(string(full), want) {
			t.Errorf("the log lacks %q", want)
		}
	}
	if !setupJournalStillThere(rg) {
		t.Error("the journal was touched")
	}
}

// --silent means no window: the box is never shown and never waited for, and the run goes on as it did before the box existed.
func TestRecoverSilentNeverAsks(t *testing.T) {
	rg := newRig(t)
	pendingMachineRun(t, rg)
	rg.env.ask = func(string, string) bool { t.Fatal("--recover --silent must not show the notice"); return false }
	calls := 0
	rg.env.elevateWait = func(string, []string) (int, error) { calls++; return 0, nil }
	if code := run([]string{"--recover", "--silent", "--repair"}, rg.env); code != 0 || calls != 1 {
		t.Fatalf("%d, prompt calls %d", code, calls)
	}
}

// The box is off by default and on only with UNRAVIS_RECOVER_NOTICE=1; "0" and anything else keep the default.
func TestRecoverNoticeIsOffByDefaultAndOnWithTheVariable(t *testing.T) {
	with := func(v string) func(string) string {
		return func(k string) string {
			if k != recoverNoticeEnv {
				t.Errorf("read %q", k)
			}
			return v
		}
	}
	if (recoverAsk(with("")) != nil) != recoverNoticeDefault {
		t.Error("unset must follow recoverNoticeDefault")
	}
	if recoverNoticeDefault {
		t.Error("the box must stay off by default until P3 passes (flip recoverNoticeDefault and this test together)")
	}
	for _, v := range []string{"yes", "true", " 1"} {
		if (recoverAsk(with(v)) != nil) != recoverNoticeDefault {
			t.Errorf("%q must leave the default", v)
		}
	}
	if recoverAsk(with("0")) != nil {
		t.Error("0 must turn the box off")
	}
	if recoverAsk(with("1")) == nil {
		t.Error("UNRAVIS_RECOVER_NOTICE=1 must turn the box on")
	}
}
