package setup

import (
	"os"
	"path/filepath"
	"testing"
)

// A ".part" left in the backup folder by a killed run must not stop the next backup copy (RV-SUP-2, W-48). The first backup of a run
// is named "1-<file>", so that is the name a stale part of an earlier run in the same folder collides with.
func TestKeepCopyReplacesAStalePartFromAKilledRun(t *testing.T) {
	e := newEnv(t, "1.0.0")
	tx := newTestTxn(t, e.sys)
	dir := t.TempDir()
	tx.backups = filepath.Join(dir, "backups")
	if err := os.MkdirAll(tx.backups, 0o750); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "x")
	if err := os.WriteFile(src, []byte("the real file"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(tx.backups, "1-x.part")
	if err := os.WriteFile(stale, []byte("half of an old copy"), 0o600); err != nil {
		t.Fatal(err)
	}

	had, err := tx.keepCopy(src, "test file")
	if err != nil || !had {
		t.Fatalf("a stale .part blocked the backup copy: had=%v err=%v", had, err)
	}
	got, err := os.ReadFile(filepath.Join(tx.backups, "1-x"))
	if err != nil || string(got) != "the real file" {
		t.Fatalf("backup content %q err %v", got, err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("the .part is still there: %v", err)
	}
}

// Only a plain file is removed. Anything else at that name is not setup's: the copy is refused and the thing is left alone.
func TestKeepCopyRefusesANonFileAtThePartName(t *testing.T) {
	e := newEnv(t, "1.0.0")
	tx := newTestTxn(t, e.sys)
	dir := t.TempDir()
	tx.backups = filepath.Join(dir, "backups")
	inTheWay := filepath.Join(tx.backups, "1-x.part")
	if err := os.MkdirAll(inTheWay, 0o750); err != nil { // a folder; a link would be refused the same way (Lstat, not Stat)
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inTheWay, "keep.txt"), []byte("not setup's"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "x")
	if err := os.WriteFile(src, []byte("the real file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if had, err := tx.keepCopy(src, "test file"); err == nil || had {
		t.Fatalf("a folder at the part name must refuse the copy: had=%v err=%v", had, err)
	}
	if _, err := os.Stat(filepath.Join(inTheWay, "keep.txt")); err != nil {
		t.Fatalf("what was in the way was touched: %v", err)
	}
}

func TestKeepCopyWithNoStalePartStillCopies(t *testing.T) {
	e := newEnv(t, "1.0.0")
	tx := newTestTxn(t, e.sys)
	dir := t.TempDir()
	tx.backups = filepath.Join(dir, "backups")
	if err := os.MkdirAll(tx.backups, 0o750); err != nil { // already there, so the run's working-folder list is not needed
		t.Fatal(err)
	}
	src := filepath.Join(dir, "x")
	if err := os.WriteFile(src, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	had, err := tx.keepCopy(src, "test file")
	if err != nil || !had {
		t.Fatalf("had=%v err=%v", had, err)
	}
	if got, err := os.ReadFile(filepath.Join(tx.backups, "1-x")); err != nil || string(got) != "v1" {
		t.Fatalf("%q %v", got, err)
	}
}
