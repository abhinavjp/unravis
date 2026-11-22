package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BL-183 review of #105, SHOULD 1: a refused machine journal blocks every elevated setup until somebody deals with it, so every refusal says
// which file or folder it is, that nothing was deleted, and what the person can do. Nothing it suggests deletes anything: it moves.

func mustAdvise(t *testing.T, err error, thing string) {
	t.Helper()
	if err == nil {
		t.Fatal("no refusal")
	}
	msg := err.Error()
	for _, want := range []string{thing, "deleted", "do not delete it", "run setup again"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, msg)
		}
	}
	for _, bad := range []string{"delete the file", "delete the folder", "Delete "} {
		if strings.Contains(msg, bad) {
			t.Errorf("the refusal tells the person to delete something (%q):\n%s", bad, msg)
		}
	}
}

func TestEveryJournalRefusalNamesTheFileAndAWayOutWithoutDeletingAnything(t *testing.T) {
	// A file that is refused: its owner is a person.
	e := newEnv(t, "1.2.0")
	journal, victim := machineJournal(t, e)
	if err := e.f.Sec.SetSDDL(journal, "O:S-1-5-21-1000D:P(A;;FA;;;SY)(A;;FA;;;BA)"); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.r.RecoverJournal(true)
	mustAdvise(t, err, journal)
	if !strings.Contains(err.Error(), "move the file") {
		t.Errorf("no way out in:\n%s", err)
	}

	// Any other reader error (a sharing violation) too.
	e = newEnv(t, "1.2.0")
	journal, _ = machineJournal(t, e)
	e.f.Faults.Set("sec.ReadFileProven:"+journal, os.ErrPermission)
	_, _, err = e.r.RecoverJournal(true)
	mustAdvise(t, err, journal)

	// A file that is not JSON.
	e = newEnv(t, "1.2.0")
	journal = filepath.Join(MachineJournalDir(e.dirs), JournalName)
	writeFile(t, journal, "not json")
	_, _, err = e.r.RecoverJournal(true)
	mustAdvise(t, err, journal)

	// A folder others can change: the folder is named, and the way out is about the folder.
	e = newEnv(t, "1.2.0")
	journal, victim = machineJournal(t, e)
	dir := filepath.Dir(journal)
	_ = e.f.Sec.SetSDDL(dir, AdminOnlySDDL+"(A;OICI;FA;;;BU)")
	_, _, err = e.r.RecoverJournal(true)
	mustAdvise(t, err, dir)
	if !strings.Contains(err.Error(), "renames the folder") {
		t.Errorf("no way out for the folder in:\n%s", err)
	}

	// Nothing was deleted or replayed by any of them.
	if !exists(journal) || !exists(victim) {
		t.Fatal("a refusal deleted or replayed something")
	}
}

func TestExecuteWithARefusedJournalSaysWhichFileAndWhatToDo(t *testing.T) {
	e := newEnv(t, "1.2.0")
	journal, victim := machineJournal(t, e)
	e.f.Faults.Set("sec.ReadFileProven:"+journal, ErrNotProven)
	p := &Plan{Op: OpUpgrade, Version: "1.2.0", Elevated: true, r: e.r, Steps: []Step{{ID: "one", Title: "One"}}}
	res := e.r.Execute(t0ctx(), p)
	if res.Outcome != OutcomeRollbackLeft {
		t.Fatalf("%+v", res)
	}
	all := strings.Join(res.Left, "\n")
	if !strings.Contains(all, journal) || !strings.Contains(all, "move the file") {
		t.Fatalf("the result does not name the file and a way out:\n%s", all)
	}
	if !exists(journal) || !exists(victim) {
		t.Fatal("the journal was replayed or deleted")
	}
	// Moving the file out of the way is the way out: the next run goes on.
	if err := os.Rename(journal, filepath.Join(e.root, "journal-moved.json")); err != nil {
		t.Fatal(err)
	}
	ran := false
	p2 := &Plan{Op: OpUpgrade, Version: "1.2.0", Elevated: true, r: e.r, Steps: []Step{{ID: "one", Title: "One", Do: func(_ context.Context, _ *Txn) error { ran = true; return nil }}}}
	if res := e.r.Execute(t0ctx(), p2); res.Outcome != OutcomeDone || !ran {
		t.Fatalf("after moving the journal away: %+v", res)
	}
}

func TestPreflightSaysSoWhenTheMachineJournalWillBeRefused(t *testing.T) {
	opts := func(e *testEnv) PreflightInput { return PreflightInput{Op: OpInstall, Opts: e.machineOpts()} }

	e := newEnv(t, "1.2.0")
	e.f.Mach.Admin = true
	journal, _ := machineJournal(t, e)
	if c, ok := checkByID(e.r.Preflight(opts(e)), "journal"); !ok || c.Status != CheckPartly {
		t.Fatalf("a proven pending journal: %+v %v", c, ok)
	}

	e.f.Faults.Set("sec.ReadFileProven:"+journal, ErrNotProven)
	c, ok := checkByID(e.r.Preflight(opts(e)), "journal")
	if !ok || c.Status != CheckFailed || !strings.Contains(c.Detail, journal) || !strings.Contains(c.Detail, "move the file") {
		t.Fatalf("a refused journal: %+v %v", c, ok)
	}

	// A normal run cannot look inside that folder, so it keeps the plain check: present is "an earlier setup did not finish".
	e.f.Mach.Admin = false
	if c, ok := checkByID(e.r.Preflight(opts(e)), "journal"); ok && c.Status == CheckFailed {
		t.Fatalf("a normal run judged a journal it cannot read: %+v", c)
	}
}

// The way out is short enough for the result note, which is cut at 400 bytes: with the usual folder it fits with the rest of the note.
func TestMachineJournalHelpFitsTheResultNote(t *testing.T) {
	r := &Runner{Dirs: Dirs{ProgramData: `C:\ProgramData`}}
	help := r.MachineJournalHelp()
	note := "An update was cut off before it finished and could not be fully put back. The administrator-level part could not put everything back." +
		help + " Run Unravis Setup from the Unravis program folder and choose Repair."
	if len(note) > 400 {
		t.Fatalf("the note is %d bytes: %s", len(note), note)
	}
	if !strings.Contains(help, "do not delete it") || !strings.Contains(help, JournalName) {
		t.Fatal(help)
	}
}

// A link at ProgramData is refused with advice about ProgramData, not about renaming the journal folder.
func TestAProgramDataLinkIsAdvisedAboutProgramData(t *testing.T) {
	e := newEnv(t, "1.2.0")
	machineJournal(t, e)
	real := e.dirs.ProgramData + ".real"
	if err := os.Rename(e.dirs.ProgramData, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, e.dirs.ProgramData); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	_, _, err := e.r.RecoverJournal(true)
	if err == nil || !strings.Contains(err.Error(), e.dirs.ProgramData) || !strings.Contains(err.Error(), "an administrator has to") ||
		strings.Contains(err.Error(), "renames the folder") || strings.Contains(err.Error(), "Unravis Setup old") {
		t.Fatalf("wrong advice for a ProgramData link:\n%v", err)
	}
	if !strings.Contains(err.Error(), "do not delete") {
		t.Fatalf("no warning against deleting:\n%v", err)
	}
}
