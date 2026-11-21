package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A cut-off run that created a file: the journal says "remove it". The person's own journal is replayed by a normal run.
func TestRecoverPutsBackACutOffRun(t *testing.T) {
	e := newEnv(t, "1.2.0")
	if res := e.r.Recover(false, ""); res.Found || res.Err != nil {
		t.Fatalf("with no journal there is nothing to do: %+v", res)
	}
	made := filepath.Join(e.root, "half-installed.txt")
	writeFile(t, made, "left by the cut-off run")
	writeFile(t, filepath.Join(UserJournalDir(e.dirs), JournalName),
		`{"op":"upgrade","version":"1.2.0","started":"2026-11-21T10:00:00Z","undos":[{"step":"copy-files","kind":"remove-file","note":"file created","path":`+quote(made)+`}]}`)
	if !e.r.JournalPending(false) || e.r.JournalPending(true) {
		t.Fatal("the person's journal is pending, the machine's is not")
	}
	if res := e.r.Recover(true, ""); res.Found {
		t.Fatalf("an elevated run never replays the person's journal: %+v", res)
	}
	res := e.r.Recover(false, "")
	if !res.Found || res.Err != nil || len(res.Left) != 0 {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(made); !os.IsNotExist(err) {
		t.Fatal("the file the cut-off run made must be removed")
	}
	if e.r.JournalPending(false) {
		t.Fatal("the journal is retired once it is put back")
	}
}

func TestRecoverLeavesACommittedJournalAlone(t *testing.T) {
	e := newEnv(t, "1.2.0")
	kept := filepath.Join(e.root, "good-install.txt")
	writeFile(t, kept, "part of a finished install")
	writeFile(t, filepath.Join(UserJournalDir(e.dirs), JournalName),
		`{"op":"upgrade","version":"1.2.0","started":"2026-11-21T10:00:00Z","committed":true,"undos":[{"step":"copy-files","kind":"remove-file","path":`+quote(kept)+`}]}`)
	if res := e.r.Recover(false, ""); res.Found {
		t.Fatalf("a finished run is never undone: %+v", res)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatal("a finished install lost a file")
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// The setup program that runs --recover is itself a file the cut-off run replaced. Windows will not delete a running program, so the
// undo moves it aside and puts the old one back; here a file that cannot be deleted stands for it.
func TestUndoMovesAFileThatCannotBeDeletedAside(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "unravis-setup.exe")
	backup := filepath.Join(dir, "backup.exe")
	writeFile(t, locked, "new setup")
	writeFile(t, backup, "old setup")
	// A non-empty folder at that name cannot be removed by os.Remove, as a running exe cannot; the move aside still works.
	if err := os.Remove(locked); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(locked, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeOrAside(locked); err != nil {
		t.Fatalf("a file that cannot be deleted must be moved aside: %v", err)
	}
	if _, err := os.Stat(locked); !os.IsNotExist(err) {
		t.Fatal("the path must be free for the old file")
	}
	if err := os.Rename(backup, locked); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(locked); string(got) != "old setup" {
		t.Fatalf("%q", got)
	}
}

func TestACommittedJournalIsNotPending(t *testing.T) {
	e := newEnv(t, "1.2.0")
	writeFile(t, filepath.Join(UserJournalDir(e.dirs), JournalName), `{"op":"upgrade","version":"1.2.0","started":"2026-11-21T10:00:00Z","committed":true,"undos":[]}`)
	if e.r.JournalPending(false) {
		t.Fatal("a finished run's journal is not pending")
	}
}
