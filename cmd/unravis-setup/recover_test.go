package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/setup"
	"api-audit-explorer/internal/update"
)

func journalBody(t *testing.T, path string) string {
	t.Helper()
	q, _ := json.Marshal(path)
	return `{"op":"upgrade","version":"1.1.0","started":"2026-11-21T10:00:00Z","undos":[{"step":"copy-files","kind":"remove-file","note":"file created","path":` + string(q) + `}]}`
}

func putJournal(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, setup.JournalName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const adminOnlySDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// trustMachine makes the machine journal's folder and the folder setup runs from administrator-only, as on a real machine install.
func trustMachine(t *testing.T, rg *testRig) {
	t.Helper()
	exeDir, _ := rg.env.setupExe()
	for _, dir := range []string{setup.MachineJournalDir(rg.dirs), filepath.Dir(exeDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = rg.f.Sec.SetSDDL(dir, adminOnlySDDL)
	}
}

func TestRecoverWithNothingPendingChangesNothing(t *testing.T) {
	rg := newRig(t)
	if code := run([]string{"--recover"}, rg.env); code != 0 || !strings.Contains(rg.out.String(), "Nothing to put back") {
		t.Fatalf("%d %s", code, rg.out.String())
	}
	if _, ok := update.ReadApplyResult(filepath.Join(rg.dirs.LocalAppData, "Unravis", "update-result")); ok {
		t.Fatal("no note when there was nothing to do")
	}
}

func TestRecoverPutsBackThePersonsJournalAndLeavesANote(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	made := filepath.Join(rg.root, "half.txt")
	if err := os.WriteFile(made, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	putJournal(t, setup.UserJournalDir(rg.dirs), journalBody(t, made))
	if code := run([]string{"--recover"}, rg.env); code != 0 {
		t.Fatalf("%d %s", code, rg.out.String())
	}
	if _, err := os.Stat(made); !os.IsNotExist(err) {
		t.Fatal("the cut-off run's file must be removed")
	}
	got, ok := update.ReadApplyResult(filepath.Join(rg.dirs.LocalAppData, "Unravis", "update-result"))
	if !ok || got.Outcome != "rolled-back" || !strings.Contains(got.Message, "cut off") {
		t.Fatalf("%+v %v", got, ok)
	}
}

// A machine journal needs administrator rights: a normal run asks Windows once, through the same program, and never replays it itself.
func TestRecoverAsksWindowsOnceForTheMachineJournal(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	putJournal(t, setup.MachineJournalDir(rg.dirs), journalBody(t, filepath.Join(rg.root, "never.txt")))
	trustMachine(t, rg)
	var gotArgs []string
	calls := 0
	rg.env.elevateWait = func(exe string, args []string) (int, error) { calls++; gotArgs = args; return 0, nil }
	if code := run([]string{"--recover"}, rg.env); code != 0 || calls != 1 || len(gotArgs) != 2 || gotArgs[0] != "--recover" || gotArgs[1] != "--no-restart" {
		t.Fatalf("%d calls %d %v", code, calls, gotArgs)
	}
	if !setupJournalStillThere(rg) {
		t.Fatal("the normal run must leave the machine journal to the administrator part")
	}
}

func setupJournalStillThere(rg *testRig) bool {
	_, err := os.Stat(filepath.Join(setup.MachineJournalDir(rg.dirs), setup.JournalName))
	return err == nil
}

func TestRecoverRefusedAtTheWindowsPromptSaysSoAndExitsNonZero(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	putJournal(t, setup.MachineJournalDir(rg.dirs), journalBody(t, filepath.Join(rg.root, "never.txt")))
	trustMachine(t, rg)
	rg.env.elevateWait = func(string, []string) (int, error) { return 0, errors.New("cancelled") }
	code := run([]string{"--recover"}, rg.env)
	if code == 0 {
		t.Fatal("a refused prompt is not success")
	}
	got, _ := update.ReadApplyResult(filepath.Join(rg.dirs.LocalAppData, "Unravis", "update-result"))
	if got.Outcome != "failed" || !strings.Contains(got.Message, "Repair") {
		t.Fatalf("%+v", got)
	}
}

// A machine journal in a folder others can change is never used: no Windows prompt, nothing closed, no note.
func TestRecoverIgnoresAMachineJournalInAFolderOthersCanChange(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	putJournal(t, setup.MachineJournalDir(rg.dirs), journalBody(t, filepath.Join(rg.root, "never.txt")))
	rg.env.elevateWait = func(string, []string) (int, error) { t.Fatal("no prompt for an untrusted journal"); return 0, nil }
	if code := run([]string{"--recover"}, rg.env); code != 0 {
		t.Fatalf("%d %s", code, rg.out.String())
	}
	if _, ok := update.ReadApplyResult(filepath.Join(rg.dirs.LocalAppData, "Unravis", "update-result")); ok {
		t.Fatal("no note: the journal was not used")
	}
}

// The elevated part never starts the app and leaves the note to the normal run that asked for it.
func TestRecoverNoRestartLeavesTheAppAndTheNoteAlone(t *testing.T) {
	rg := newRig(t)
	made := filepath.Join(rg.root, "half.txt")
	if err := os.WriteFile(made, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	putJournal(t, setup.MachineJournalDir(rg.dirs), journalBody(t, made))
	trustMachine(t, rg)
	rg.env.startBackground = func(string) error { t.Fatal("the elevated part must not start the app"); return nil }
	if code := run([]string{"--recover", "--no-restart"}, rg.env); code != 0 {
		t.Fatalf("%d %s", code, rg.out.String())
	}
	if _, err := os.Stat(made); !os.IsNotExist(err) {
		t.Fatal("the machine journal must be replayed by the administrator run")
	}
	if _, ok := update.ReadApplyResult(filepath.Join(rg.dirs.LocalAppData, "Unravis", "update-result")); ok {
		t.Fatal("the normal run writes the note")
	}
}

// A journal marked committed (its run finished) needs nothing: no prompt, no close, no note.
func TestRecoverDoesNothingForACommittedJournal(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	body := strings.Replace(journalBody(t, filepath.Join(rg.root, "good.txt")), `"undos"`, `"committed":true,"undos"`, 1)
	putJournal(t, setup.MachineJournalDir(rg.dirs), body)
	trustMachine(t, rg)
	rg.env.elevateWait = func(string, []string) (int, error) { t.Fatal("no prompt for a finished run"); return 0, nil }
	if code := run([]string{"--recover"}, rg.env); code != 0 || !strings.Contains(rg.out.String(), "Nothing to put back") {
		t.Fatalf("%d %s", code, rg.out.String())
	}
}
