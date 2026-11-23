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
	SweepSQLLoginFiles(root)
	if _, err := os.Stat(f.Dir); err == nil {
		t.Error("leftover login folder not removed")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("an unrelated folder was removed")
	}
}
