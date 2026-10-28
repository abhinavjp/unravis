package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

// ---- version compare (F6) ----

func TestCompareVersionsTable(t *testing.T) {
	rows := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"1.2.0", "1.2.0", 0, true},
		{"v1.2.0", "1.2.0", 0, true},
		{"1.2.0", "1.10.0", -1, true},
		{"2.0.0", "1.99.99", 1, true},
		// git describe: release plus N commits
		{"v1.2.0-3-gabc1234", "v1.2.0", 1, true},
		{"v1.2.0", "v1.2.0-3-gabc1234", -1, true},
		{"v1.2.0-3-gabc1234", "v1.2.0-3-gdef5678", 0, true},
		{"v1.2.0-9-gbbbbbbb", "v1.2.0-12-gaaaaaaa", -1, true},
		{"v1.2.0-12-gaaaaaaa", "v1.2.0-9-gbbbbbbb", 1, true}, // the review's example: 12 is newer than 9, so 12 -> 9 is a downgrade
		{"v1.2.0-12-gaaaaaaa", "v1.2.1", -1, true},
		{"v1.2.1", "v1.2.0-40-gaaaaaaa", 1, true},
		// -dirty alone is the same numbers, never older
		{"v1.2.0-dirty", "v1.2.0", 0, true},
		{"v1.2.0", "v1.2.0-dirty", 0, true},
		{"v1.2.0-3-gabc1234-dirty", "v1.2.0-3-gabc1234", 0, true},
		{"v1.2.0-3-gabc1234-dirty", "v1.2.0", 1, true},
		{"v1.2.0-dirty", "v1.2.0-3-gabc1234", -1, true},
		// pre-releases
		{"1.2.0-rc1", "1.2.0", -1, true},
		{"1.2.0", "1.2.0-rc1", 1, true},
		{"1.2.0-rc2", "1.2.0-rc10", -1, true},
		{"1.2.0-rc10", "1.2.0-rc2", 1, true},
		{"1.2.0-rc.2", "1.2.0-rc.10", -1, true},
		{"1.2.0-beta", "1.2.0-rc1", -1, true},
		{"1.2.0-rc1", "1.2.0-rc1.1", -1, true},
		{"1.2.0-rc1-2-gabc1234", "1.2.0-rc1", 1, true},
		{"1.2.0-rc1-2-gabc1234", "1.2.0", -1, true},
		{"1.2.0-3-gabc1234", "1.2.0-rc1", 1, true},
		{"1.2.0+build5", "1.2.0", 0, true},
		// not release numbers: nothing is refused on the grounds of version
		{"dev", "1.2.0", 0, false},
		{"1.2.0", "abc", 0, false},
		{"1.2.0-", "1.2.0", 0, false},
		{"", "1.2.0", 0, false},
	}
	for _, r := range rows {
		got, ok := CompareVersions(r.a, r.b)
		if got != r.want || ok != r.ok {
			t.Errorf("CompareVersions(%q, %q) = %d, %v; want %d, %v", r.a, r.b, got, ok, r.want, r.ok)
		}
	}
}

// The review's example end to end: installed 12 commits after the tag, a setup that is 9 commits after it is a downgrade.
func TestGitDescribeDowngradeIsRefused(t *testing.T) {
	e := newEnv(t, "v1.2.0-12-gaaaaaaa")
	e.mustRun(OpInstall, e.userOpts(), UninstallOptions{})
	e.r.Payload = testPayload(t, "v1.2.0-9-gbbbbbbb", true)
	e.f.version = "v1.2.0-9-gbbbbbbb"
	old, err := Detect(e.sys.Reg)
	if err != nil || old == nil {
		t.Fatalf("detect: %v %v", old, err)
	}
	checks := e.r.Preflight(PreflightInput{Op: OpUpgrade, Opts: e.userOpts(), Old: old})
	refused := false
	for _, c := range checks {
		if c.Status == CheckFailed && strings.Contains(strings.ToLower(c.Title+c.Detail+c.Fix), "older") {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("a downgrade from 12 to 9 commits was not refused: %+v", checks)
	}
}

// ---- commit order and a journal that cannot be deleted (F1) ----

func backupDirs(e *testEnv) []string {
	m, _ := filepath.Glob(filepath.Join(NewLayout(e.userOpts(), e.dirs).InstallDir, ".setup-backup-*"))
	m2, _ := filepath.Glob(filepath.Join(NewLayout(e.machineOpts(), e.dirs).InstallDir, ".setup-backup-*"))
	return append(m, m2...)
}

func TestCommitRetiresJournalBeforeRemovingBackups(t *testing.T) {
	full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), true) }
	e := newEnv(t, "1.2.0")
	priorInstall(full)(e)
	var sawBackups, called bool
	e.r.BeforeCommit = func(j *Journal) {
		j.remove = func(p string) error {
			called = true
			sawBackups = len(backupDirs(e)) > 0
			return os.Remove(p)
		}
	}
	e.mustRun(OpUpgrade, full(e), UninstallOptions{})
	if !called {
		t.Fatal("the journal was not removed through Commit")
	}
	if !sawBackups {
		t.Error("the backups were gone before the journal was retired")
	}
	if len(backupDirs(e)) != 0 {
		t.Errorf("backups left after commit: %v", backupDirs(e))
	}
	if exists(filepath.Join(MachineJournalDir(e.dirs), JournalName)) {
		t.Error("journal left after commit")
	}
}

func lockJournal(j *Journal) {
	j.remove = func(string) error {
		return errors.New("the process cannot access the file because it is being used by another process")
	}
	j.rename = func(string, string) error { return errors.New("sharing violation") }
}

func TestJournalThatCannotBeDeletedIsNotReplayedAfterSuccess(t *testing.T) {
	full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), true) }
	e := newEnv(t, "1.2.0")
	e.r.BeforeCommit = lockJournal
	e.mustRun(OpInstall, full(e), UninstallOptions{})
	e.r.BeforeCommit = nil
	jp := filepath.Join(MachineJournalDir(e.dirs), JournalName)
	if !exists(jp) {
		t.Fatal("the test did not leave a journal behind")
	}
	if len(backupDirs(e)) != 0 {
		t.Errorf("backups kept after a committed run: %v", backupDirs(e))
	}
	l := NewLayout(full(e), e.dirs)
	rec := filepath.Join(e.root, "D", "UnravisBlackbox")
	writeFile(t, filepath.Join(rec, "recording-1.dat"), "weeks of evidence")
	_, cfg, key := l.BlackboxPaths()
	after := e.snapshot()

	// The next run starts by looking at the journal: it must delete it and change nothing else.
	found, left, err := e.r.RecoverJournal(true)
	if found || len(left) != 0 || err != nil {
		t.Fatalf("recover replayed or complained about a finished run: %v %v %v", found, left, err)
	}
	if exists(jp) {
		t.Error("the finished journal was not deleted")
	}
	for _, p := range []string{cfg, key, filepath.Join(rec, "recording-1.dat"), l.AppExe} {
		if !exists(p) {
			t.Errorf("%s was undone", p)
		}
	}
	if st, _ := e.f.Svc.State(blackbox.ServiceName); st == blackbox.SvcNotInstalled {
		t.Error("the Blackbox service was removed")
	}
	if d := diffSnap(after, e.snapshot()); strings.Contains(d, "ProgramData") && !strings.Contains(d, "journal") {
		t.Errorf("the install changed:\n%s", d)
	}
	// A repair afterwards works as normal.
	e.mustRun(OpRepair, full(e), UninstallOptions{})
	if !exists(filepath.Join(rec, "recording-1.dat")) {
		t.Error("recordings lost")
	}
}

func TestStaleFinishedJournalDoesNotBlockTheNextRun(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.r.BeforeCommit = lockJournal
	e.mustRun(OpInstall, e.machineOpts(), UninstallOptions{})
	e.r.BeforeCommit = nil
	// Even when RecoverJournal cannot delete it either, the journal is replaced.
	p, err := NewJournal(filepath.Join(MachineJournalDir(e.dirs), JournalName), OpRepair, "1.2.0", e.sys.now())
	if err != nil {
		t.Fatalf("a finished journal blocked a new one: %v", err)
	}
	_ = p.Close()
}

func TestCommitThatCannotRetireTheJournalIsRolledBack(t *testing.T) {
	full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), true) }
	e := newEnv(t, "1.2.0")
	before := e.snapshot()
	var tmp string
	e.r.BeforeCommit = func(j *Journal) {
		lockJournal(j)
		tmp = j.path + ".tmp"
		if err := os.Mkdir(tmp, 0o755); err != nil { // the "finished" marker cannot be written either
			t.Fatal(err)
		}
	}
	res := e.run(OpInstall, full(e), UninstallOptions{})
	if res.Outcome != OutcomeRolledBack {
		t.Fatalf("outcome %s: %s %v\n%s", res.Outcome, res.Message, res.Left, strings.Join(e.log, "\n"))
	}
	if !strings.Contains(res.Message, "journal") {
		t.Errorf("message does not say why: %q", res.Message)
	}
	_ = os.Remove(tmp)
	// Everything but the journal itself is back; the journal is only a record of undone work.
	d := diffSnap(before, e.snapshot())
	for _, l := range strings.Split(d, "\n") {
		if l != "" && !strings.Contains(l, JournalName) {
			t.Errorf("not put back: %s", l)
		}
	}
}

// ---- Blackbox files that were already there (F12) ----

func TestBlackboxFilesThatWereThereAreKeptOnRollback(t *testing.T) {
	cases := map[string][]string{
		"all four": {blackbox.ConfigFileName, blackbox.ConfigFileName + ".bak", blackbox.KeyFileName, blackbox.InstalledExeName},
		"settings": {blackbox.ConfigFileName},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			full := func(e *testEnv) Options { return e.withBlackbox(e.machineOpts(), true) }
			e := newEnv(t, "1.2.0")
			l := NewLayout(full(e), e.dirs)
			for _, f := range files {
				writeFile(t, filepath.Join(l.BlackboxDir, f), "old "+f)
			}
			before := e.snapshot()
			e.r.FailAfter = func(s string) error {
				if s == "firewall" {
					return errors.New("injected failure")
				}
				return nil
			}
			res := e.run(OpInstall, full(e), UninstallOptions{})
			if res.Outcome != OutcomeRolledBack {
				t.Fatalf("outcome %s: %s %v\n%s", res.Outcome, res.Message, res.Left, strings.Join(e.log, "\n"))
			}
			for _, f := range files {
				b, err := os.ReadFile(filepath.Join(l.BlackboxDir, f))
				if err != nil || string(b) != "old "+f {
					t.Errorf("%s was not kept: %q %v", f, b, err)
				}
			}
			if d := diffSnap(before, e.snapshot()); d != "" {
				t.Fatalf("not put back:\n%s\nlog:\n%s", d, strings.Join(e.log, "\n"))
			}
		})
	}
}

// ---- a fault on every fake-system call (F13) ----

// TestFaultOnEveryCallIsUndone fails each fake-system call of a run in turn (registry, services, firewall, permissions, processes, the
// version check). A run that stops must leave the fake PC as it was; a run whose error the code rightly tolerates may finish.
func TestFaultOnEveryCallIsUndone(t *testing.T) {
	injected := errors.New("injected")
	for _, c := range rollbackCases() {
		t.Run(c.name, func(t *testing.T) {
			setup := func() (*testEnv, *Plan) {
				e := newEnv(t, "1.2.0")
				if c.prior != nil {
					c.prior(e)
				}
				old, _ := Detect(e.sys.Reg)
				p, err := e.r.NewPlan(c.op, c.opts(e), UninstallOptions{}, old)
				if err != nil {
					t.Fatal(err)
				}
				return e, p
			}
			e, p := setup()
			e.f.Faults.StartCounting(0, nil)
			if res := e.r.Execute(t0ctx(), p); res.Outcome != OutcomeDone {
				t.Fatalf("clean run: %s: %s", res.Outcome, res.Message)
			}
			calls := e.f.Faults.StopCounting()
			if len(calls) == 0 {
				t.Fatal("no fake-system call was seen")
			}
			rolledBack := 0
			for n := 1; n <= len(calls); n++ {
				e, p := setup()
				before := e.snapshot()
				e.f.Faults.StartCounting(n, injected)
				res := e.r.Execute(t0ctx(), p)
				e.f.Faults.StopCounting()
				label := fmt.Sprintf("call %d (%s)", n, calls[n-1])
				switch res.Outcome {
				case OutcomeDone:
					continue // the code tolerates that call failing
				case OutcomeRolledBack:
					rolledBack++
					if d := diffSnap(before, e.snapshot()); d != "" {
						t.Errorf("%s: not everything was put back:\n%s\nlog:\n%s", label, d, strings.Join(e.log, "\n"))
					}
				default:
					t.Errorf("%s: outcome %s: %s %v\nlog:\n%s", label, res.Outcome, res.Message, res.Left, strings.Join(e.log, "\n"))
				}
			}
			if rolledBack == 0 {
				t.Error("no injected fault stopped a run, so the sweep proved nothing")
			}
			t.Logf("%d calls, %d stopped the run and were put back", len(calls), rolledBack)
		})
	}
}
