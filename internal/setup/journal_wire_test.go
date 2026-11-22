package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// BL-183 (a), PR 4: JournalPending, RecoverJournal and Execute read the machine journal through one function (readJournal), and no byte
// is read before the folder and the file are proven. These tests use the fakes, so they run on Linux; the real-NTFS ones are in
// journal_open_windows_test.go.

// machineJournal writes a pending journal that lists one file to remove, with the writer the elevated run uses, and returns its path and
// the listed file (which exists).
func machineJournal(t *testing.T, e *testEnv) (journal, victim string) {
	t.Helper()
	victim = filepath.Join(e.root, "victim.txt")
	writeFile(t, victim, "x")
	journal = filepath.Join(MachineJournalDir(e.dirs), JournalName)
	j, err := NewJournalWith(journal, OpUpgrade, "1.2.0", time.Now(), LoadJournal, &journalWriter{sec: e.f.Sec, sddl: MachineJournalFileSDDL})
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Record(Undo{Step: "x", Kind: UndoRemoveFile, Path: victim, Note: "test file"}); err != nil {
		t.Fatal(err)
	}
	return journal, victim
}

func readProvenCalls(calls []string) (n int) {
	for _, c := range calls {
		if strings.HasPrefix(c, "sec.ReadFileProven:") {
			n++
		}
	}
	return n
}

func TestJournalPendingDoesNotReadAnUntrustedFolder(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := MachineJournalDir(e.dirs)
	p := filepath.Join(dir, JournalName)
	writeFile(t, p, "not json")
	_ = e.f.Sec.SetSDDL(dir, AdminOnlySDDL+"(A;OICI;FA;;;BU)")
	e.f.Faults.StartCounting(0, nil)
	pending := e.r.JournalPending(true)
	calls := e.f.Faults.StopCounting()
	if !pending {
		t.Fatal("a refused journal must count as pending, so the replay reports it")
	}
	if n := readProvenCalls(calls); n != 0 {
		t.Fatalf("the file was opened %d time(s) although its folder is not trusted: %v", n, calls)
	}
	if got, _ := os.ReadFile(p); string(got) != "not json" {
		t.Fatal("the journal was changed")
	}
}

func TestJournalPendingAbsentIsFalse(t *testing.T) {
	e := newEnv(t, "1.2.0")
	if e.r.JournalPending(true) {
		t.Fatal("no journal, yet pending")
	}
	// A folder others can change that holds no journal is not a pending journal either (nothing to replay, nothing to prompt for).
	_ = e.f.Sec.SetSDDL(MachineJournalDir(e.dirs), AdminOnlySDDL+"(A;OICI;FA;;;BU)")
	if e.r.JournalPending(true) {
		t.Fatal("an empty untrusted folder counted as a pending journal")
	}
	// The journal folder is not there at all.
	e2 := newEnv(t, "1.2.0")
	if err := os.RemoveAll(MachineJournalDir(e2.dirs)); err != nil {
		t.Fatal(err)
	}
	if e2.r.JournalPending(true) {
		t.Fatal("no journal folder, yet pending")
	}
}

func TestJournalPendingCommittedIsFalse(t *testing.T) {
	e := newEnv(t, "1.2.0")
	journal, _ := machineJournal(t, e)
	j, found, err := e.r.readJournal(true)
	if !found || err != nil {
		t.Fatalf("%v %v", found, err)
	}
	j.f.Committed = true
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if !exists(journal) {
		t.Fatal("setup of the test failed")
	}
	if e.r.JournalPending(true) {
		t.Fatal("a journal marked committed is pending")
	}
}

func TestJournalPendingProvenPendingIsTrue(t *testing.T) {
	e := newEnv(t, "1.2.0")
	machineJournal(t, e)
	if !e.r.JournalPending(true) {
		t.Fatal("a proven pending journal is not pending")
	}
	if e.r.JournalPending(false) {
		t.Fatal("the person's scope saw the machine journal")
	}
}

func TestJournalPendingRefusedLeafIsTrue(t *testing.T) {
	e := newEnv(t, "1.2.0")
	journal, _ := machineJournal(t, e)
	if err := e.f.Sec.SetSDDL(journal, "O:S-1-5-21-1000D:P(A;;FA;;;SY)(A;;FA;;;BA)"); err != nil {
		t.Fatal(err)
	}
	if !e.r.JournalPending(true) {
		t.Fatal("a journal whose owner is a person is not reported as pending")
	}
	found, _, err := e.r.RecoverJournal(true)
	if !found || err == nil || !strings.Contains(err.Error(), "only administrators made") {
		t.Fatalf("%v %v", found, err)
	}
}

func TestRecoverJournalRefusesAnUnprovenLeaf(t *testing.T) {
	e := newEnv(t, "1.2.0")
	journal, victim := machineJournal(t, e)
	e.f.Faults.Set("sec.ReadFileProven:"+journal, ErrNotProven)
	found, left, err := e.r.RecoverJournal(true)
	if !found || err == nil || len(left) != 0 {
		t.Fatalf("%v %v %v", found, left, err)
	}
	if !exists(journal) || !exists(victim) {
		t.Fatal("a refused journal was replayed or deleted")
	}
}

// Review SHOULD: any error from the reader other than "not there" is a journal found but refused, not only ErrNotProven. A sharing
// violation or access denied must not look like "no journal".
func TestRecoverJournalRefusesAnyReaderErrorNotOnlyNotProven(t *testing.T) {
	for name, rerr := range map[string]error{
		"sharing violation": errors.New("The process cannot access the file because it is being used by another process."),
		"access denied":     os.ErrPermission,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			journal, victim := machineJournal(t, e)
			e.f.Faults.Set("sec.ReadFileProven:"+journal, rerr)
			if !e.r.JournalPending(true) {
				t.Fatal("a journal that could not be opened looks like no journal")
			}
			found, _, err := e.r.RecoverJournal(true)
			if !found || err == nil {
				t.Fatalf("%v %v", found, err)
			}
			if !exists(journal) || !exists(victim) {
				t.Fatal("a refused journal was replayed or deleted")
			}
		})
	}
	// Only "not there" is no journal.
	e := newEnv(t, "1.2.0")
	journal, _ := machineJournal(t, e)
	e.f.Faults.Set("sec.ReadFileProven:"+journal, os.ErrNotExist)
	if e.r.JournalPending(true) {
		t.Fatal("a journal that is gone counted as pending")
	}
}

func TestRecoverJournalRefusesALinkedLeaf(t *testing.T) {
	e := newEnv(t, "1.2.0")
	journal, victim := machineJournal(t, e)
	good := filepath.Join(e.root, "good.json")
	if err := os.Rename(journal, good); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(good, journal); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	found, _, err := e.r.RecoverJournal(true)
	if !found || err == nil {
		t.Fatalf("a journal reached through a link was used: %v %v", found, err)
	}
	if !exists(victim) || !exists(good) {
		t.Fatal("the linked journal was replayed or deleted")
	}
}

func TestRecoverJournalRefusesALinkAboveTheJournalFolder(t *testing.T) {
	e := newEnv(t, "1.2.0")
	_, victim := machineJournal(t, e)
	real := e.dirs.ProgramData + ".real"
	if err := os.Rename(e.dirs.ProgramData, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, e.dirs.ProgramData); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	found, _, err := e.r.RecoverJournal(true)
	if !found || err == nil || !strings.Contains(err.Error(), "a link") {
		t.Fatalf("%v %v", found, err)
	}
	if !e.r.JournalPending(true) {
		t.Fatal("a link above the journal looks like no journal")
	}
	if !exists(victim) {
		t.Fatal("replayed")
	}
}

// A link in the middle that points at a folder without the file must not look like an absent journal (review NIT).
func TestRecoverJournalRefusesAJournalFolderThatIsALinkToAnEmptyFolder(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := MachineJournalDir(e.dirs)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(e.root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(empty, dir); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	found, _, err := e.r.RecoverJournal(true)
	if !found || err == nil || !strings.Contains(err.Error(), "a link") {
		t.Fatalf("%v %v", found, err)
	}
}

func TestRecoverJournalRefusesAnOversizedJournal(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := MachineJournalDir(e.dirs)
	p := filepath.Join(dir, JournalName)
	writeFile(t, p, strings.Repeat("a", maxJournalBytes+1))
	found, _, err := e.r.RecoverJournal(true)
	if !found || err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("%v %v", found, err)
	}
	if !exists(p) {
		t.Fatal("the journal was deleted")
	}
}

func TestRecoverJournalUserScopeStillReadsByPath(t *testing.T) {
	e := newEnv(t, "1.2.0")
	victim := filepath.Join(e.root, "victim.txt")
	writeFile(t, victim, "x")
	p := filepath.Join(UserJournalDir(e.dirs), JournalName)
	j, err := NewJournal(p, OpUpgrade, "1.2.0", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Record(Undo{Step: "x", Kind: UndoRemoveFile, Path: victim}); err != nil {
		t.Fatal(err)
	}
	e.f.Faults.StartCounting(0, nil)
	found, left, err := e.r.RecoverJournal(false)
	calls := e.f.Faults.StopCounting()
	if !found || err != nil || len(left) != 0 {
		t.Fatalf("%v %v %v", found, left, err)
	}
	if n := readProvenCalls(calls); n != 0 {
		t.Fatalf("the person's journal went through the proven reader: %v", calls)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "sec.CreateFileSDDL:") {
			t.Fatalf("the person's journal was written with an owner list: %v", calls)
		}
	}
	if exists(victim) {
		t.Fatal("not replayed")
	}
}

func TestRecoverJournalReplaysAProvenJournal(t *testing.T) {
	e := newEnv(t, "1.2.0")
	journal, victim := machineJournal(t, e)
	found, left, err := e.r.RecoverJournal(true)
	if !found || err != nil || len(left) != 0 {
		t.Fatalf("%v %v %v", found, left, err)
	}
	if exists(victim) {
		t.Fatal("the listed file was not removed")
	}
	if exists(journal) {
		t.Fatal("the replayed journal was not retired")
	}
}

// A replay that marks the journal committed (it could not delete it) writes it again with the same owner, so it is still readable.
func TestARecoveredJournalIsWrittenAgainWithTheAdministratorsOwner(t *testing.T) {
	e := newEnv(t, "1.2.0")
	journal, _ := machineJournal(t, e)
	j, found, err := e.r.readJournal(true)
	if err != nil || !found {
		t.Fatalf("%v %v", found, err)
	}
	if j.w == nil || j.w.sddl != MachineJournalFileSDDL {
		t.Fatalf("a journal read for the machine scope has no owner writer: %+v", j.w)
	}
	e.f.Faults.StartCounting(0, nil)
	j.f.Committed = true
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	calls := e.f.Faults.StopCounting()
	if len(calls) == 0 || calls[0] != "sec.CreateFileSDDL:"+journal+".tmp" {
		t.Fatalf("calls %v", calls)
	}
}

// BL-183 review SHOULD (a): the machine journal is made with the owner the reader accepts, not whatever the token's default is.
func TestExecuteElevatedWritesTheJournalWithTheAdministratorsOwner(t *testing.T) {
	for _, elevated := range []bool{true, false} {
		e := newEnv(t, "1.2.0")
		victim := filepath.Join(e.root, "made.txt")
		dir := e.r.journalDir(elevated)
		p := &Plan{Op: OpUpgrade, Version: "1.2.0", Elevated: elevated, r: e.r, Steps: []Step{{ID: "one", Title: "One",
			Do: func(ctx context.Context, t *Txn) error { return t.WriteFile(victim, []byte("x"), "test file") }}}}
		e.f.Faults.StartCounting(0, nil)
		res := e.r.Execute(context.Background(), p)
		calls := e.f.Faults.StopCounting()
		if res.Outcome != OutcomeDone {
			t.Fatalf("elevated=%v: %+v", elevated, res)
		}
		want := "sec.CreateFileSDDL:" + filepath.Join(dir, JournalName) + ".tmp"
		got := false
		for _, c := range calls {
			got = got || c == want
		}
		if got != elevated {
			t.Errorf("elevated=%v: journal written through CreateFileSDDL = %v, calls %v", elevated, got, calls)
		}
		if !elevated && readProvenCalls(calls) != 0 {
			t.Errorf("the person's run used the proven reader: %v", calls)
		}
	}
}

func TestExecuteRefusesAnUnprovenLeftoverJournal(t *testing.T) {
	for name, rerr := range map[string]error{
		"not proven":        ErrNotProven,
		"sharing violation": errors.New("sharing violation"),
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, "1.2.0")
			journal, victim := machineJournal(t, e)
			e.f.Faults.Set("sec.ReadFileProven:"+journal, rerr)
			ran := false
			p := &Plan{Op: OpUpgrade, Version: "1.2.0", Elevated: true, r: e.r, Steps: []Step{{ID: "one", Title: "One",
				Do: func(ctx context.Context, t *Txn) error { ran = true; return nil }}}}
			res := e.r.Execute(context.Background(), p)
			if res.Outcome != OutcomeRollbackLeft {
				t.Fatalf("%+v", res)
			}
			if ran {
				t.Fatal("setup went on with a leftover journal it could not prove")
			}
			if !exists(journal) || !exists(victim) {
				t.Fatal("the journal was replayed or deleted")
			}
		})
	}
}

// The journal of a run that finished (it could not be deleted) is replaced by the next run, through the proven reader.
func TestNewJournalWithReplacesACommittedJournalAndRefusesAnUnreadableOne(t *testing.T) {
	e := newEnv(t, "1.2.0")
	journal, _ := machineJournal(t, e)
	w := &journalWriter{sec: e.f.Sec, sddl: MachineJournalFileSDDL}
	load := func(string) (*Journal, error) {
		j, found, err := e.r.readJournal(true)
		switch {
		case err != nil:
			return nil, err
		case !found:
			return nil, os.ErrNotExist
		}
		return j, nil
	}
	if _, err := NewJournalWith(journal, OpUpgrade, "1.2.1", time.Now(), load, w); err == nil {
		t.Fatal("a journal of a run that did not finish was replaced")
	}
	j, _, _ := e.r.readJournal(true)
	j.f.Committed = true
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	n, err := NewJournalWith(journal, OpUpgrade, "1.2.1", time.Now(), load, w)
	if err != nil || n.f.Version != "1.2.1" {
		t.Fatalf("%v %v", n, err)
	}
	e.f.Faults.Set("sec.ReadFileProven:"+journal, errors.New("sharing violation"))
	if _, err := NewJournalWith(journal, OpUpgrade, "1.2.2", time.Now(), load, w); err == nil {
		t.Fatal("a journal that could not be read was replaced")
	}
}
