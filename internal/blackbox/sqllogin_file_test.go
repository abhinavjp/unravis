package blackbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/bbsql"
)

func TestWriteSQLLoginFileIsPrivateHashedAndRemovable(t *testing.T) {
	root := t.TempDir()
	f, err := WriteSQLLoginFile(root, bbsql.StoredLogin{Server: "db01", Login: bbsql.Login{User: "u", Password: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(f.Dir) != root {
		t.Errorf("folder %q is not under the given root", f.Dir)
	}
	b, err := os.ReadFile(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != f.SHA {
		t.Error("the hash is not of the written bytes")
	}
	if !bytes.Contains(b, []byte(`"password":"p"`)) || !bytes.Contains(b, []byte(`"server":"db01"`)) {
		t.Errorf("document %s", b)
	}
	if fi, _ := os.Stat(f.Path); fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("file mode %v lets others read it", fi.Mode())
	}
	RemoveSQLLoginFile(f)
	if _, err := os.Stat(f.Dir); err == nil {
		t.Error("the folder survived the removal")
	}
}

func TestWriteSQLLoginFileRefusesAnInvalidLogin(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteSQLLoginFile(root, bbsql.StoredLogin{Server: "bad;x", Login: bbsql.Login{User: "u", Password: "p"}}); err == nil {
		t.Fatal("wrote an invalid login")
	}
	if ents, _ := os.ReadDir(root); len(ents) != 0 {
		t.Error("left a folder behind")
	}
}

func TestSweepRemovesOnlyLeftoverLoginFolders(t *testing.T) {
	root := t.TempDir()
	f, err := WriteSQLLoginFile(root, bbsql.StoredLogin{Server: "sql01", Login: bbsql.Login{User: "u", Password: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "keep-me")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	// A second leftover the way an older keeper made it, and a plain file with the same prefix that must be left alone.
	old := filepath.Join(root, "unravis-sqllogin-12345")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "login.json"), []byte(`{"password":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(root, "unravis-sqllogin-notes.txt")
	if err := os.WriteFile(stray, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := SweepSQLLoginFiles(root); n != 2 {
		t.Errorf("sweep removed %d folders, want 2", n)
	}
	for _, p := range []string{f.Dir, old} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("leftover login folder %s not removed", filepath.Base(p))
		}
	}
	for _, p := range []string{other, stray} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed", filepath.Base(p))
		}
	}
	if n := SweepSQLLoginFiles(root); n != 0 {
		t.Errorf("a second sweep removed %d, want 0", n)
	}
	if n := SweepSQLLoginFiles(filepath.Join(root, "missing")); n != 0 {
		t.Errorf("a missing folder swept %d, want 0", n)
	}
}
