package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/datadir"
)

func TestJournalFileOwnerRule(t *testing.T) {
	const self = "S-1-5-21-2000"
	for _, c := range []struct {
		owner, self string
		want        bool
	}{
		{"S-1-5-18", "", true},
		{"S-1-5-32-544", "", true},
		{self, self, true},
		{strings.ToLower(self), self, true},
		{"S-1-5-21-1000", self, false},
		{"", "", false},
		{"S-1-5-21-1000", "", false},
		{"S-1-5-32-545", self, false}, // Users
	} {
		if got := journalFileOwnerOK(c.owner, c.self); got != c.want {
			t.Errorf("journalFileOwnerOK(%q, %q) = %v, want %v", c.owner, c.self, got, c.want)
		}
	}
}

func TestCheckNoOthersWriteIsTheListHalfOfCheckAdminOnly(t *testing.T) {
	good, _ := ParseSDDL("O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)")
	if err := checkNoOthersWrite("x", good); err != nil {
		t.Fatalf("an admin-only list was refused: %v", err)
	}
	userOwned, _ := ParseSDDL("O:BUD:P(A;;FA;;;SY)(A;;FA;;;BA)")
	if err := checkNoOthersWrite("x", userOwned); err != nil {
		t.Fatalf("the list half must not look at the owner: %v", err)
	}
	if err := checkAdminOnly("x", userOwned); err == nil {
		t.Fatal("checkAdminOnly accepted an owner that is Users")
	}
	loose, _ := ParseSDDL("O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;BU)")
	if err := checkNoOthersWrite("x", loose); err == nil {
		t.Fatal("a list that lets Users change the item was accepted")
	}
	if err := checkNoOthersWrite("x", datadir.FolderSecurity{Owner: "S-1-5-18", NullDACL: true}); err == nil {
		t.Fatal("no list at all was accepted")
	}
}

func TestParseJournalEqualsLoadJournal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, JournalName)
	want := JournalFile{Op: OpUpgrade, Version: "1.2.0", Started: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC),
		Undos: []Undo{{Kind: UndoRemoveFile, Path: filepath.Join(dir, "a.txt")}}}
	data, err := json.MarshalIndent(want, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, string(data))
	byPath, err := LoadJournal(p)
	if err != nil {
		t.Fatal(err)
	}
	byBytes, err := ParseJournal(data, p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(byPath.f, byBytes.f) || byPath.path != byBytes.path {
		t.Fatalf("LoadJournal %+v, ParseJournal %+v", byPath, byBytes)
	}

	bad := filepath.Join(dir, "bad.json")
	writeFile(t, bad, "{ not json")
	_, e1 := LoadJournal(bad)
	_, e2 := ParseJournal([]byte("{ not json"), bad)
	if e1 == nil || e2 == nil || e1.Error() != e2.Error() {
		t.Fatalf("errors differ: %v / %v", e1, e2)
	}
	if _, err := LoadJournal(filepath.Join(dir, "none.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing journal gave %v, want ErrNotExist", err)
	}
}

func TestFakeReadFileProvenRefusesALinkAndAnOversizeFile(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := filepath.Join(e.root, "j")
	if err := e.f.Sec.MkdirSDDL(dir, AdminOnlySDDL); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(dir, "real.json")
	writeFile(t, real, "hello")
	if got, err := e.f.Sec.ReadFileProven(real, 100); err != nil || string(got) != "hello" {
		t.Fatalf("a plain file in an admin-only folder: %q, %v", got, err)
	}

	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if _, err := e.f.Sec.ReadFileProven(link, 100); !errors.Is(err, ErrNotProven) {
		t.Fatalf("a link gave %v, want ErrNotProven", err)
	}

	if _, err := e.f.Sec.ReadFileProven(real, 4); !errors.Is(err, ErrNotProven) {
		t.Fatalf("a file over max gave %v, want ErrNotProven", err)
	}
	if got, err := e.f.Sec.ReadFileProven(real, 5); err != nil || string(got) != "hello" {
		t.Fatalf("a file of exactly max bytes: %q, %v", got, err)
	}

	if _, err := e.f.Sec.ReadFileProven(filepath.Join(dir, "none.json"), 100); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing file gave %v, want ErrNotExist", err)
	}
	if _, err := e.f.Sec.ReadFileProven(dir, 100); !errors.Is(err, ErrNotProven) {
		t.Fatalf("a folder gave %v, want ErrNotProven", err)
	}
}

func TestFakeReadFileProvenInheritsTheFolderList(t *testing.T) {
	e := newEnv(t, "1.2.0")
	dir := filepath.Join(e.root, "j")
	if err := e.f.Sec.MkdirSDDL(dir, AdminOnlySDDL); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "journal.json")
	writeFile(t, p, "x")
	if _, err := e.f.Sec.ReadFileProven(p, 10); err != nil {
		t.Fatalf("a file inherits the admin-only list of its folder: %v", err)
	}

	// The folder gives Users write: the file inherits it and is refused.
	if err := e.f.Sec.SetSDDL(dir, AdminOnlySDDL+"(A;OICI;FA;;;BU)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.f.Sec.ReadFileProven(p, 10); !errors.Is(err, ErrNotProven) {
		t.Fatalf("a file in a folder Users can write gave %v, want ErrNotProven", err)
	}

	// The file's own list wins over the folder's.
	if err := e.f.Sec.SetSDDL(dir, AdminOnlySDDL); err != nil {
		t.Fatal(err)
	}
	if err := e.f.Sec.SetSDDL(p, "O:S-1-5-21-1000D:P(A;;FA;;;SY)(A;;FA;;;BA)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.f.Sec.ReadFileProven(p, 10); err == nil || !errors.Is(err, ErrNotProven) || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("a file owned by a person gave %v", err)
	}

	// A folder nobody gave a list is the person's own: refused.
	plain := filepath.Join(e.root, "plain")
	writeFile(t, filepath.Join(plain, "journal.json"), "x")
	if _, err := e.f.Sec.ReadFileProven(filepath.Join(plain, "journal.json"), 10); !errors.Is(err, ErrNotProven) {
		t.Fatalf("a person's own file gave %v, want ErrNotProven", err)
	}

	// A fault is returned as it is.
	boom := errors.New("boom")
	e.f.Faults.Set("sec.ReadFileProven:"+p, boom)
	if _, err := e.f.Sec.ReadFileProven(p, 10); !errors.Is(err, boom) {
		t.Fatalf("fault not returned: %v", err)
	}
}
