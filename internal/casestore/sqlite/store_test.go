package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

func TestCreateValidatesTitle(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for name, title := range map[string]string{
		"empty": "", "blank": "   ", "control": "a\x00b", "newline": "a\nb", "too long": strings.Repeat("x", 201),
	} {
		if _, err := s.Create(ctx, title); !errors.Is(err, casestore.ErrBadTitle) {
			t.Errorf("%s: Create = %v, want ErrBadTitle", name, err)
		}
	}
	info, err := s.Create(ctx, "  Padded title  ")
	if err != nil || info.Title != "Padded title" {
		t.Fatalf("Create = %+v, %v", info, err)
	}
	if !validID(info.ID) {
		t.Fatalf("ID %q does not match the case ID pattern", info.ID)
	}
	if entries := caseFolders(s.root); len(entries) != 1 {
		t.Fatalf("a rejected title left folders behind: %d entries", len(entries))
	}
}

func TestIDsAreValidatedBeforeAnyPathJoin(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	victim := filepath.Join(filepath.Dir(root), "victim")
	if err := os.MkdirAll(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := []casestore.CaseID{
		"", "..", "../victim", `..\victim`, "aaaaaaaaaaaaaaaaaaaaaaaaaa/..", "AAAAAAAAAAAAAAAAAAAAAAAAAA",
		"aaaaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaaaaa1", "aaaaaaaaaaaaaaaaaaaaaaaaa\x00",
		"C:\\Windows", "aaaaaaaaaaaaaaaaaaaaaaaaa.", "aaaaaaaaaaaaaaaaaaaaaaaaa ",
	}
	for _, id := range bad {
		if _, err := s.Open(ctx, id); !errors.Is(err, casestore.ErrBadID) {
			t.Errorf("Open(%q) = %v, want ErrBadID", id, err)
		}
		if err := s.CloseCase(ctx, id); !errors.Is(err, casestore.ErrBadID) {
			t.Errorf("CloseCase(%q) = %v, want ErrBadID", id, err)
		}
		if err := s.Erase(ctx, id); !errors.Is(err, casestore.ErrBadID) {
			t.Errorf("Erase(%q) = %v, want ErrBadID", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(victim, "keep.txt")); err != nil {
		t.Fatalf("an Erase of a hostile ID touched files outside the cases folder: %v", err)
	}
}

func TestImportMetaIsCleaned(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
	m := tMeta(`C:\Users\someone\Desktop\..\export file.csv`)
	m.HostLabel = "web-a\x00\x1b[31m"
	res, err := c.Import(ctx, m, tBody("m"), tRun(tEvents(c, "m", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Record.FileName != "export file.csv" || strings.ContainsAny(res.Record.HostLabel, "\x00\x1b") {
		t.Fatalf("record = %+v", res.Record)
	}
	res, err = c.Import(ctx, tMeta("/a/b/c.csv"), tBody("m2"), tRun(nil))
	if err != nil || res.Record.FileName != "c.csv" {
		t.Fatalf("unix path name = %+v, %v", res.Record, err)
	}
	for _, name := range []string{"../x", "a b", strings.Repeat("a", 65)} {
		m := tMeta("n.csv")
		m.Importer = name
		if _, err := c.Import(ctx, m, tBody("n"), tRun(nil)); !errors.Is(err, ErrInvalid) {
			t.Errorf("importer name %q: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestListOldestFirstAndUnreadableCaseCanBeErased(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	s, root := newTestStore(t, func(o *Options) {
		o.Now = func() time.Time { now = now.Add(time.Hour); return now }
	})
	a := mustCreate(t, s, "first")
	b := mustCreate(t, s, "second")
	infos, err := s.List(ctx)
	if err != nil || len(infos) != 2 || infos[0].ID != a.ID || infos[1].ID != b.ID {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	// Stray entries are ignored.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "not-a-case-id"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A manifest that cannot be read still lists the case, so the user can erase it.
	if err := os.WriteFile(filepath.Join(root, string(a.ID), manifestName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	infos, err = s.List(ctx)
	if err != nil || len(infos) != 2 {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	var got casestore.CaseInfo
	for _, i := range infos {
		if i.ID == a.ID {
			got = i
		}
	}
	if got.Title != "(unreadable case)" {
		t.Fatalf("unreadable case listed as %+v", got)
	}
	if _, err := s.Open(ctx, a.ID); !errors.Is(err, ErrDamaged) {
		t.Fatalf("Open with a damaged manifest = %v, want ErrDamaged", err)
	}
	if err := s.Erase(ctx, a.ID); err != nil {
		t.Fatalf("Erase of an unreadable case: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, string(a.ID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder survived: %v", err)
	}
}

func TestOpenRefusesAnInconsistentFolder(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	a := mustCreate(t, s, "A")
	b := mustCreate(t, s, "B")
	dirA := filepath.Join(root, string(a.ID))
	dirB := filepath.Join(root, string(b.ID))

	// B's manifest copied into A's folder: the manifest names another case.
	if err := os.WriteFile(filepath.Join(dirA, manifestName), readFile(t, filepath.Join(dirB, manifestName)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, a.ID); !errors.Is(err, ErrDamaged) {
		t.Fatalf("foreign manifest: Open = %v, want ErrDamaged", err)
	}
	// B's key over A's key: wrong key for A's database.
	if err := os.Remove(filepath.Join(dirB, manifestName)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, b.ID); !errors.Is(err, ErrDamaged) {
		t.Fatalf("missing manifest: Open = %v, want ErrDamaged", err)
	}
	// A missing key means the case was erased, or lost its key: not found.
	if err := os.Remove(filepath.Join(dirB, keyName)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, b.ID); !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("missing key: Open = %v, want ErrNotFound", err)
	}
	// A garbage key file is reported as such, without its content.
	if err := os.WriteFile(filepath.Join(dirB, keyName), []byte("not a sealed blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, b.ID); !errors.Is(err, casestore.ErrKeyFile) {
		t.Fatalf("garbage key: Open = %v, want ErrKeyFile", err)
	}
	if err := s.Erase(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	if _, err := c.db.ExecContext(ctx, "UPDATE meta SET v='99' WHERE k='schema_version'"); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, info.ID); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("Open = %v, want ErrNewerSchema", err)
	}
}

func TestOpenGivesOneHandlePerOpenOverOneDatabase(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c1, err := s.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c1 == c2 {
		t.Fatal("two Opens gave one handle; F0 requires a handle per Open")
	}
	mustImport(t, c1.(*Case), "a", 2)
	if err := c1.Close(); err != nil {
		t.Fatal(err)
	}
	// Still usable through the second reference.
	if got, err := c2.Imports(ctx); err != nil || len(got) != 1 {
		t.Fatalf("after the first Close: %v, %v", got, err)
	}
	if err := c2.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c2.Close(); err != nil {
		t.Fatalf("Close twice: %v", err)
	}
	if _, err := c2.Imports(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed handle = %v, want ErrClosed", err)
	}
	// A new Open after the last Close reads the same data.
	c3 := mustOpen(t, s, info.ID)
	if got, _ := c3.Imports(ctx); len(got) != 1 {
		t.Fatalf("reopened: %d imports", len(got))
	}
}

func TestPseudonymKeySurvivesReopenAndDiesWithTheCase(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	before := c.Pseudonymize("user", "alice@example.test")
	_ = c.Close()
	c2 := mustOpen(t, s, info.ID)
	if got := c2.Pseudonymize("user", "alice@example.test"); got != before {
		t.Fatalf("pseudonym changed across reopen: %s vs %s", before, got)
	}
	_ = c2.Close()
	if err := s.Erase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	info2 := mustCreate(t, s, "Case")
	c3 := mustOpen(t, s, info2.ID)
	if c3.Pseudonymize("user", "alice@example.test") == before {
		t.Fatal("a new case shares the erased case's pseudonym key")
	}
}

func TestInfoAndManifestSurviveReopen(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	res := mustImport(t, c, "a", 7)
	if err := s.CloseCase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if got := c.Info(); got.Imports != 1 || got.Closed == nil || got.Bytes <= 0 {
		t.Fatalf("Info after CloseCase = %+v", got)
	}
	_ = c.Close()

	s2 := newStoreAt(t, root, s.opt.Protector)
	infos, _ := s2.List(ctx)
	if len(infos) != 1 || infos[0].Imports != 1 || infos[0].Closed == nil || infos[0].Title != "Case" {
		t.Fatalf("List = %+v", infos)
	}
	c2 := mustOpen(t, s2, info.ID)
	recs, err := c2.Imports(ctx)
	if err != nil || len(recs) != 1 {
		t.Fatalf("imports = %+v, %v", recs, err)
	}
	r := recs[0]
	if r.SHA256 != res.Record.SHA256 || r.Rows != 7 || r.FileName != "a.csv" || r.Status != "ok" ||
		r.HostLabel != "web-a" || len(r.KnownGaps) != 1 || !r.Imported.Equal(res.Record.Imported) {
		t.Fatalf("record after reopen = %+v, want %+v", r, res.Record)
	}
	// The acknowledgement text is mirrored in the sealed manifest.
	man, err := readManifest(filepath.Join(root, string(info.ID)), s.opt.Protector)
	if err != nil || len(man.Imports) != 1 || !man.Imports[0].AckGating || man.Imports[0].AckText == "" || man.Imports[0].Events != 7 {
		t.Fatalf("manifest = %+v, %v", man, err)
	}
}
