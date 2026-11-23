package setup

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newCLIEnv is a fake PC whose payload carries the thin command line program.
func newCLIEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t, "1.2.0")
	e.r.Payload = testPayload(t, "1.2.0", true, CLIExeName)
	return e
}

func planIDs(t *testing.T, e *testEnv, op Op, o Options) []string {
	t.Helper()
	old, _ := Detect(e.sys.Reg)
	p, err := e.r.NewPlan(op, o, UninstallOptions{}, old)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range p.Steps {
		ids = append(ids, s.ID)
	}
	return ids
}

func pathEntries(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ";")
}

func TestInstallPlacesCLIAtBin(t *testing.T) {
	e := newCLIEnv(t)
	const before = `C:\Tools;%SystemRoot%\system32`
	e.f.Env.Set(HKCU, before)
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if l.CLIExe != filepath.Join(l.InstallDir, "bin", "unravis.exe") {
		t.Fatalf("CLIExe %q", l.CLIExe)
	}
	want, err := fs.ReadFile(e.r.Payload.FS, CLIExeName)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(l.CLIExe)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("bin\\unravis.exe is not the payload file (%v)", err)
	}
	if exists(filepath.Join(l.InstallDir, CLIExeName)) {
		t.Error("the payload name was left in the program folder")
	}
	if p := e.f.Env.Get(HKCU); p != before+";"+l.CLIDir {
		t.Errorf("PATH %q", p)
	}
	if p := e.f.Env.Get(HKLM); p != "" {
		t.Errorf("a just-for-me install wrote the machine PATH: %q", p)
	}
	if e.f.Env.Broadcasts == 0 {
		t.Error("running programs were not told about the new PATH")
	}
}

func TestInstallWithoutCLIInPayloadIsUnchanged(t *testing.T) {
	ec := newCLIEnv(t)
	with := planIDs(t, ec, OpInstall, ec.userOpts())
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	without := planIDs(t, e, OpInstall, o)
	var wantIDs []string
	for _, id := range with {
		if id != "cli-path" {
			wantIDs = append(wantIDs, id)
		}
	}
	if strings.Join(without, ",") != strings.Join(wantIDs, ",") || len(with) != len(without)+1 {
		t.Fatalf("steps without the CLI %v, with it %v", without, with)
	}
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if exists(l.CLIDir) {
		t.Error("a bin folder was made")
	}
	if e.f.Env.Get(HKCU) != "" || e.f.Env.Get(HKLM) != "" || e.f.Env.Broadcasts != 0 {
		t.Errorf("the PATH was touched: %q %q %d", e.f.Env.Get(HKCU), e.f.Env.Get(HKLM), e.f.Env.Broadcasts)
	}
}

func TestCLIStepComesRightAfterCopyFiles(t *testing.T) {
	e := newCLIEnv(t)
	ids := planIDs(t, e, OpInstall, e.machineOpts())
	for i, id := range ids {
		if id == "copy-files" {
			if i+1 >= len(ids) || ids[i+1] != "cli-path" {
				t.Fatalf("steps %v", ids)
			}
			return
		}
	}
	t.Fatalf("no copy-files in %v", ids)
}

func TestMachineScopeUsesHKLM(t *testing.T) {
	e := newCLIEnv(t)
	e.f.Env.Set(HKLM, `C:\Windows\system32`)
	e.f.Env.Set(HKCU, `C:\Mine`)
	o := e.machineOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if p := e.f.Env.Get(HKLM); p != `C:\Windows\system32;`+l.CLIDir {
		t.Errorf("machine PATH %q", p)
	}
	if p := e.f.Env.Get(HKCU); p != `C:\Mine` {
		t.Errorf("the person's PATH was changed: %q", p)
	}
	if !exists(l.CLIExe) {
		t.Error("no bin\\unravis.exe")
	}
}

func TestRepairKeepsOneEntry(t *testing.T) {
	e := newCLIEnv(t)
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	e.mustRun(OpRepair, Options{}, UninstallOptions{})
	if got := e.f.Env.Get(HKCU); got != l.CLIDir {
		t.Fatalf("after a repair the PATH is %q", got)
	}
	// A copy someone added by hand is dropped, the first one stays where it is.
	e.f.Env.Set(HKCU, `C:\A;`+l.CLIDir+`;C:\B;`+strings.ToUpper(l.CLIDir)+`\`)
	e.mustRun(OpRepair, Options{}, UninstallOptions{})
	if got := e.f.Env.Get(HKCU); got != `C:\A;`+l.CLIDir+`;C:\B` {
		t.Fatalf("duplicates were not dropped: %q", got)
	}
	n := 0
	for _, s := range pathEntries(e.f.Env.Get(HKCU)) {
		if pathEntryKey(s) == pathEntryKey(l.CLIDir) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d entries", n)
	}
}

func TestRollbackRemovesOnlyOurEntry(t *testing.T) {
	e := newCLIEnv(t)
	const old = `C:\Tools;%SystemRoot%\system32;`
	e.f.Env.Set(HKCU, old)
	before := e.snapshot()
	o := e.userOpts()
	l := NewLayout(o, e.dirs)
	// Another program adds its folder after our step and before the run fails.
	e.r.BeforeStep = func(s string) {
		if s == "shortcuts" {
			if !strings.Contains(e.f.Env.Get(HKCU), l.CLIDir) {
				t.Error("our entry is not there before the failure")
			}
			e.f.Env.Set(HKCU, e.f.Env.Get(HKCU)+`;D:\Other`)
		}
	}
	e.r.FailAfter = func(s string) error {
		if s == "shortcuts" {
			return errors.New("injected failure")
		}
		return nil
	}
	res := e.run(OpInstall, o, UninstallOptions{})
	if res.Outcome != OutcomeRolledBack {
		t.Fatalf("outcome %s: %s %v", res.Outcome, res.Message, res.Left)
	}
	if got := e.f.Env.Get(HKCU); got != old+`D:\Other` {
		t.Fatalf("PATH after the rollback is %q", got)
	}
	e.f.Env.Set(HKCU, old)
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("not everything was put back:\n%s", d)
	}
}

func TestPreexistingEntryIsNotJournaled(t *testing.T) {
	e := newCLIEnv(t)
	o := e.userOpts()
	l := NewLayout(o, e.dirs)
	have := `C:\Tools;` + l.CLIDir + `;C:\More`
	e.f.Env.Set(HKCU, have)
	e.r.FailAfter = func(s string) error {
		if s == "shortcuts" {
			return errors.New("injected failure")
		}
		return nil
	}
	if res := e.run(OpInstall, o, UninstallOptions{}); res.Outcome != OutcomeRolledBack {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Message)
	}
	if got := e.f.Env.Get(HKCU); got != have {
		t.Fatalf("the entry that was there before is gone: %q", got)
	}
}

func TestPathTooLongFailsStep(t *testing.T) {
	e := newCLIEnv(t)
	full := strings.Repeat("a", MaxPathValue-10)
	e.f.Env.Set(HKCU, full)
	before := e.snapshot()
	o := e.userOpts()
	res := e.run(OpInstall, o, UninstallOptions{})
	if res.Outcome != OutcomeRolledBack {
		t.Fatalf("outcome %s: %s %v", res.Outcome, res.Message, res.Left)
	}
	if res.Failed != "Add the unravis command to the path" || !strings.Contains(res.Message, "too long") {
		t.Errorf("failed %q message %q", res.Failed, res.Message)
	}
	if !strings.HasPrefix(res.Fix, "Remove entries you no longer need from the PATH") {
		t.Errorf("fix %q", res.Fix)
	}
	if got := e.f.Env.Get(HKCU); got != full {
		t.Error("the PATH value changed")
	}
	if d := diffSnap(before, e.snapshot()); d != "" {
		t.Fatalf("not everything was put back:\n%s", d)
	}
}

func TestUninstallRemovesEntryAndFiles(t *testing.T) {
	e := newCLIEnv(t)
	o := e.userOpts()
	l := NewLayout(o, e.dirs)
	e.f.Env.Set(HKCU, `C:\Tools`)
	e.mustRun(OpInstall, o, UninstallOptions{})
	e.f.Env.Set(HKCU, e.f.Env.Get(HKCU)+`;D:\Other`)
	broadcasts := e.f.Env.Broadcasts
	res := e.run(OpUninstall, Options{}, UninstallOptions{})
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if got := e.f.Env.Get(HKCU); got != `C:\Tools;D:\Other` {
		t.Errorf("PATH %q", got)
	}
	if exists(l.CLIExe) || exists(l.CLIDir) || exists(l.InstallDir) {
		t.Error("the command line program, its bin folder or the program folder is still there")
	}
	if e.f.Env.Broadcasts <= broadcasts {
		t.Error("no broadcast after the entry was removed")
	}

	// A bin folder with a file the person added stays, and the answer says so.
	e2 := newCLIEnv(t)
	oo := e2.userOpts()
	e2.mustRun(OpInstall, oo, UninstallOptions{})
	l2 := NewLayout(oo, e2.dirs)
	note := filepath.Join(l2.CLIDir, "notes.txt")
	writeFile(t, note, "mine")
	res = e2.run(OpUninstall, Options{}, UninstallOptions{})
	if res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if !exists(note) || exists(l2.CLIExe) {
		t.Error("the person's file was removed, or unravis.exe stayed")
	}
	if e2.f.Env.Get(HKCU) != "" {
		t.Errorf("PATH %q", e2.f.Env.Get(HKCU))
	}
	found := false
	for _, k := range res.Kept {
		if strings.Contains(k, l2.CLIDir) {
			found = true
		}
	}
	if !found {
		t.Errorf("the kept list does not mention the bin folder: %v", res.Kept)
	}
}

func TestInstallOverExistingBinIsNotForeign(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Bin", "UNRAVIS.exe"), "cli")
	writeFile(t, filepath.Join(dir, AppExeName), "app")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other := foreignEntries(dir, ents); len(other) != 0 {
		t.Fatalf("our own bin folder is foreign: %v", other)
	}
	if c, failed := checkInstallFolder(dir); failed {
		t.Fatalf("preflight refuses it: %+v", c)
	}
	writeFile(t, filepath.Join(dir, "Bin", "other.txt"), "somebody's")
	ents, _ = os.ReadDir(dir)
	if other := foreignEntries(dir, ents); len(other) != 1 || !strings.EqualFold(other[0], "bin") {
		t.Fatalf("foreign entries %v", other)
	}
	if c, failed := checkInstallFolder(dir); !failed || !strings.Contains(c.Detail, "Bin") {
		t.Fatalf("a bin folder with another file was accepted: %+v", c)
	}
}

func TestUndoPathEntryReplay(t *testing.T) {
	e := newCLIEnv(t)
	o := e.userOpts()
	l := NewLayout(o, e.dirs)
	e.f.Env.Set(HKCU, `C:\Tools`)
	// A power cut after the PATH step: the journal holds the path-entry record and the run never ends.
	e.r.BeforeStep = func(s string) {
		if s == "shortcuts" {
			e.f.Env.Set(HKCU, e.f.Env.Get(HKCU)+`;D:\Other`) // another installer
			panic("power cut")
		}
	}
	func() {
		defer func() { _ = recover() }()
		e.run(OpInstall, o, UninstallOptions{})
	}()
	e.r.BeforeStep = nil
	if got := e.f.Env.Get(HKCU); got != `C:\Tools;`+l.CLIDir+`;D:\Other` {
		t.Fatalf("PATH before the recovery %q", got)
	}
	found, left, err := e.r.RecoverJournal(false)
	if !found || err != nil || len(left) != 0 {
		t.Fatalf("recover: %v %v %v", found, left, err)
	}
	if got := e.f.Env.Get(HKCU); got != `C:\Tools;D:\Other` {
		t.Fatalf("PATH after the recovery %q", got)
	}
	if exists(l.CLIDir) {
		t.Error("the bin folder was not removed")
	}
}

func TestUndoPathEntryReportsWhatIsLeft(t *testing.T) {
	e := newEnv(t, "1.2.0")
	entry := filepath.Join(e.root, "x", "bin")
	e.f.Env.Set(HKCU, `C:\A;`+entry)
	u := Undo{Kind: UndoPathEntry, Root: HKCU, Key: envKeyFor(HKCU), Name: EnvPathName, Path: entry}
	e.f.Faults.Once("env.Remove:HKCU", errors.New("access denied"))
	left := Rollback(e.sys, []Undo{u}, nil)
	if len(left) != 1 || !strings.HasPrefix(left[0], "PATH entry "+entry) {
		t.Fatalf("left %v", left)
	}
	if left = Rollback(e.sys, []Undo{u}, nil); len(left) != 0 || e.f.Env.Get(HKCU) != `C:\A` {
		t.Fatalf("left %v, PATH %q", left, e.f.Env.Get(HKCU))
	}
}
