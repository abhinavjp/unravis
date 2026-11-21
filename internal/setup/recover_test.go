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
