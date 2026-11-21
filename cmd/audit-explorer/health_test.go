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

func TestCutOffUpdateHandsOverToTheKeptSetupOnce(t *testing.T) {
	tr, m := healthRig(t)
	folders, _ := writeJournal(t, "1.1.0", false, time.Hour)
	var gotVer string
	launch := func(p, s, v string) error { gotVer = v; return nil }
	note := func(update.ApplyResult) { t.Fatal("no note when the hand-over worked") }
	if !recoverCutOff(tr, folders, time.Now(), launch, note, func(string, ...any) {}) || gotVer != m.From {
		t.Fatalf("it must hand over to the setup of %s, got %q", m.From, gotVer)
	}
	if tr.Skipped(m.To) {
		t.Fatal("a cut-off run is not the version's fault: it must not be skipped")
	}
	if recoverCutOff(tr, folders, time.Now(), launch, note, func(string, ...any) {}) {
		t.Fatal("a run is tried once")
	}
}

func TestCutOffUpdateWithoutItsKeptSetupLeavesANote(t *testing.T) {
	tr, _ := healthRig(t)
	folders, _ := writeJournal(t, "9.9.9", false, time.Hour) // another update than the kept setup's
	var got update.ApplyResult
	stopped := recoverCutOff(tr, folders, time.Now(), func(string, string, string) error { t.Fatal("must not launch"); return nil },
		func(r update.ApplyResult) { got = r }, func(string, ...any) {})
	if stopped || got.Outcome != "failed" || !strings.Contains(got.Message, "Repair") {
		t.Fatalf("%v %+v", stopped, got)
	}
}

func TestJournalsThatAreNotCutOffAreLeftAlone(t *testing.T) {
	tr, _ := healthRig(t)
	for name, c := range map[string]struct {
		committed bool
		age       time.Duration
	}{"committed": {true, time.Hour}, "a setup still running": {false, time.Second}} {
		folders, _ := writeJournal(t, "1.1.0", c.committed, c.age)
		if recoverCutOff(tr, folders, time.Now(), func(string, string, string) error { t.Fatal(name); return nil },
			func(update.ApplyResult) { t.Fatal(name) }, func(string, ...any) {}) {
			t.Fatal(name)
		}
	}
}
