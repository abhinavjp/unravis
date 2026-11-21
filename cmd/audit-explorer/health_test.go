package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/update"
)

func healthRig(t *testing.T) (*update.Tracker, update.Marker) {
	t.Helper()
	prev := filepath.Join(t.TempDir(), update.PreviousDir)
	if err := os.MkdirAll(prev, 0o755); err != nil {
		t.Fatal(err)
	}
	m := update.Marker{From: "1.0.0", To: "1.1.0", SetupSHA256: strings.Repeat("b", 64), At: time.Now().UTC()}
	if err := update.WriteMarker(prev, m); err != nil {
		t.Fatal(err)
	}
	return &update.Tracker{StateDir: t.TempDir(), PrevDir: prev, Version: "1.1.0"}, m
}

func TestGoBackSkipsTheVersionAndStartsTheMarkedSetup(t *testing.T) {
	tr, m := healthRig(t)
	var gotPath, gotSha, gotVer string
	ok := goBack(tr, m, func(p, s, v string) error { gotPath, gotSha, gotVer = p, s, v; return nil }, func(string, ...any) {})
	if !ok || gotPath != tr.SetupPath() || gotSha != m.SetupSHA256 || gotVer != "1.0.0" {
		t.Fatalf("%v %q %q %q", ok, gotPath, gotSha, gotVer)
	}
	if !tr.Skipped("1.1.0") || tr.Skipped("1.1.1") {
		t.Fatal("the failed version must be skipped, and only it")
	}
}

func TestGoBackThatCannotStartLetsTheAppStart(t *testing.T) {
	tr, m := healthRig(t)
	if goBack(tr, m, func(string, string, string) error { return errors.New("no") }, func(string, ...any) {}) {
		t.Fatal("a rollback that did not start must not stop the app")
	}
}

func TestAFailedRunIsNotCountedAsAVisit(t *testing.T) {
	tr, _ := healthRig(t)
	tr.Begin()
	// an early error return never calls end, and a returned error is not a clean end
	watchHealth(tr, time.Hour)(errors.New("port in use"))
	tr.Begin()
	tr.Begin()
	if a, _ := tr.Begin(); a != update.ActRecover {
		t.Fatalf("a failed run did not count: %v", a)
	}
}

func TestACleanShortVisitDoesNotCount(t *testing.T) {
	tr, _ := healthRig(t)
	for i := 0; i < 10; i++ {
		tr.Begin()
		watchHealth(tr, time.Hour)(nil)
	}
	if a, _ := tr.Begin(); a != update.ActWatch {
		t.Fatalf("%v", a)
	}
}

func TestARollbackIsHandedOverOnlyOnce(t *testing.T) {
	tr, m := healthRig(t)
	for i := 0; i <= update.MaxFailedStarts; i++ {
		tr.Begin()
	}
	goBack(tr, m, func(string, string, string) error { return nil }, func(string, ...any) {})
	if a, _ := tr.Begin(); a != update.ActNone {
		t.Fatalf("the app must start once the rollback was tried: %v", a)
	}
}

func TestStayingUpConfirms(t *testing.T) {
	tr, _ := healthRig(t)
	tr.Begin()
	end := watchHealth(tr, 10*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	end(nil)
	for i := 0; i < 6; i++ {
		if a, _ := tr.Begin(); a != update.ActNone {
			t.Fatalf("not confirmed: %v", a)
		}
	}
}

// writeJournal puts a journal of a cut-off run in a folder, aged so it counts as still.
func writeJournal(t *testing.T, version string, committed bool, age time.Duration) (folders []string, started time.Time) {
	t.Helper()
	dir := t.TempDir()
	started = time.Now().UTC().Add(-age).Truncate(time.Second)
	body := `{"op":"upgrade","version":"` + version + `","started":"` + started.Format(time.RFC3339) + `","undos":[]`
	if committed {
		body += `,"committed":true`
	}
	body += `}`
	p := filepath.Join(dir, update.JournalFile)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	_ = os.Chtimes(p, old, old)
	return []string{dir}, started
}

func noLaunch(t *testing.T) func(string) error {
	return func(string) error { t.Fatal("must not launch"); return nil }
}

func TestCutOffUpdateStartsTheSetupRecoverFlagWhenItKnowsIt(t *testing.T) {
	tr, _ := healthRig(t)
	folders, _ := writeJournal(t, "1.1.0", false, time.Hour)
	var started []string
	launch := func(p string) error { started = append(started, p); return nil }
	note := func(update.ApplyResult) { t.Fatal("the setup leaves the note, not the app") }
	canRecover := func(string) bool { return true }
	if !recoverCutOff(tr, folders, time.Now(), "C:/x/unravis-setup.exe", canRecover, launch, note, func(string, ...any) {}) || len(started) != 1 || started[0] != "C:/x/unravis-setup.exe" {
		t.Fatalf("%v", started)
	}
	// a recovery can fail for reasons that pass (another setup holds the lock): it is tried at the next starts, three times in all
	for i := 0; i < 5; i++ {
		recoverCutOff(tr, folders, time.Now(), "C:/x/unravis-setup.exe", canRecover, launch, note, func(string, ...any) {})
	}
	if len(started) != update.MaxJournalTries {
		t.Fatalf("started %d times", len(started))
	}
}

func TestCutOffUpdateWithAnOlderSetupLeavesOneNoteAndStartsNothing(t *testing.T) {
	tr, _ := healthRig(t)
	folders, _ := writeJournal(t, "1.1.0", false, time.Hour)
	var got []update.ApplyResult
	note := func(r update.ApplyResult) { got = append(got, r) }
	for i := 0; i < 4; i++ {
		recoverCutOff(tr, folders, time.Now(), "x", func(string) bool { return false }, noLaunch(t), note, func(string, ...any) {})
	}
	if len(got) != 1 || got[0].Outcome != "failed" || !strings.Contains(got[0].Message, "Repair") {
		t.Fatalf("one note, once: %+v", got)
	}
}

func TestCutOffUpdateWhoseSetupCannotStartLeavesANote(t *testing.T) {
	tr, _ := healthRig(t)
	folders, _ := writeJournal(t, "1.1.0", false, time.Hour)
	var got []update.ApplyResult
	recoverCutOff(tr, folders, time.Now(), "x", func(string) bool { return true }, func(string) error { return errors.New("no") },
		func(r update.ApplyResult) { got = append(got, r) }, func(string, ...any) {})
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestJournalsThatAreNotCutOffAreLeftAlone(t *testing.T) {
	tr, _ := healthRig(t)
	for name, c := range map[string]struct {
		committed bool
		age       time.Duration
	}{"committed": {true, time.Hour}, "a setup still running": {false, time.Second}} {
		folders, _ := writeJournal(t, "1.1.0", c.committed, c.age)
		if recoverCutOff(tr, folders, time.Now(), "x", func(string) bool { return true }, noLaunch(t), func(update.ApplyResult) { t.Fatal(name) }, func(string, ...any) {}) {
			t.Fatal(name)
		}
	}
}
