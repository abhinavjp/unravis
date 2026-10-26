package casestore_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
)

// fastParams keeps Argon2id cheap in tests; the format carries its parameters.
var fastParams = masterkey.Params{Time: 1, Memory: 16 << 10, Threads: 1}

const (
	cfPass   = "correct horse battery staple"
	cfMarker = "SENTINEL-casefile-9d2c-plaintext"
)

// cfEnv is one "PC": a case folder with its own master-key stand-in.
type cfEnv struct {
	root   string
	prot   sealedfile.Protector
	lookup casestore.ProtectorLookup
	st     *sqlite.Store
}

func newCFEnv(t *testing.T) *cfEnv {
	t.Helper()
	e := &cfEnv{root: filepath.Join(t.TempDir(), "cases"), prot: storetest.RandomProtector()}
	e.lookup = func(kind string) (sealedfile.Protector, error) {
		if kind == casestore.KindMasterKey {
			return e.prot, nil
		}
		return nil, casestore.ErrLocked
	}
	st, err := sqlite.New(sqlite.Options{
		Root: e.root, Protector: storetest.RandomProtector(), Protectors: e.lookup,
		NewCaseKind: func() string { return casestore.KindMasterKey }, MinFreeBytes: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.st = st
	return e
}

var cfBase = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

func cfEvents(id casestore.CaseID, label string, n int) []evidence.Event {
	out := make([]evidence.Event, n)
	for i := range out {
		v := float64(i)
		line := int64(i + 2)
		e := evidence.Event{
			EvidenceID: evidence.EventID(string(id), "test.csv", label, line, 0),
			Source:     "test.csv", Kind: "cpu", Host: "web-a",
			Start: cfBase.Add(time.Duration(i) * time.Second), Value: &v, Unit: "percent", Line: line,
			TZRule: "utc-field", Attrs: map[string]string{"counter": "total", "note": cfMarker},
		}
		e.End = e.Start.Add(500 * time.Millisecond)
		if i%2 == 1 {
			e.Kind, e.Host = "wait", "db-a"
		}
		out[i] = e
	}
	return out
}

func cfImport(t *testing.T, c casestore.Case, label string, n int) {
	t.Helper()
	events := cfEvents(c.Info().ID, label, n)
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "test.csv", KnownGaps: []string{"synthetic test data"}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for _, e := range events {
			rep.Rows++
			if err := emit(e); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
	meta := casestore.ImportMeta{
		Importer: "test.csv", FileName: label + ".csv", HostLabel: "web-a", TZRule: "utc-field",
		AckGating: true, AckText: "Read the gating note for this import.",
	}
	if _, err := c.Import(context.Background(), meta, strings.NewReader("synthetic body "+label+"\n"), run); err != nil {
		t.Fatal(err)
	}
}

// cfSeed makes a case with n events. It leaves the case closed (no handle).
func cfSeed(t *testing.T, e *cfEnv, title string, n int) casestore.CaseInfo {
	t.Helper()
	ctx := context.Background()
	info, err := e.st.Create(ctx, title)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.st.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfImport(t, c, "first", n)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return info
}

func cfExport(t *testing.T, e *cfEnv, info casestore.CaseInfo, dst string, pass string) (int64, error) {
	t.Helper()
	return casestore.ExportCaseFile(context.Background(), e.root, info.ID, info.Title, info.Created, info.Closed,
		e.lookup, dst, []byte(pass), fastParams)
}

func cfRestore(e *cfEnv, path, pass string) (casestore.CaseInfo, error) {
	return casestore.RestoreCaseFile(context.Background(), e.st, e.lookup, path, []byte(pass))
}

func cfEvents2(t *testing.T, e *cfEnv, id casestore.CaseID) []evidence.Event {
	t.Helper()
	c, err := e.st.Open(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	evs, err := c.Events(context.Background(), casestore.EventQuery{Limit: 50000})
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func cfDirEntries(t *testing.T, root string) []string {
	t.Helper()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestCaseFileRoundTripIntoSecondDataFolder(t *testing.T) {
	src, dst := newCFEnv(t), newCFEnv(t)
	info := cfSeed(t, src, "Quarterly audit "+cfMarker, 120)
	file := filepath.Join(t.TempDir(), "case"+casestore.CaseFileExt)
	if n, err := cfExport(t, src, info, file, cfPass); err != nil || n <= 0 {
		t.Fatalf("export = %d, %v", n, err)
	}
	if st, err := os.Stat(file); err != nil || st.Size() <= 0 {
		t.Fatal("no case file")
	}

	got, err := cfRestore(dst, file, cfPass)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == info.ID {
		t.Fatal("restore reused the case id")
	}
	if got.Title != info.Title || got.Imports != 1 {
		t.Fatalf("restored %+v", got)
	}
	if kind, err := dst.st.CaseKind(got.ID); err != nil || kind != casestore.KindMasterKey {
		t.Fatalf("restored case kind = %q, %v", kind, err)
	}

	want := cfEvents2(t, src, info.ID)
	have := cfEvents2(t, dst, got.ID)
	if len(want) != 120 || len(have) != len(want) {
		t.Fatalf("events: source %d, restored %d", len(want), len(have))
	}
	oldTag, newTag := "ev:"+string(info.ID)[:8]+":", "ev:"+string(got.ID)[:8]+":"
	for i := range want {
		a, b := want[i], have[i]
		if !strings.HasPrefix(a.EvidenceID, oldTag) || !strings.HasPrefix(b.EvidenceID, newTag) {
			t.Fatalf("event %d ids %q -> %q", i, a.EvidenceID, b.EvidenceID)
		}
		if strings.TrimPrefix(a.EvidenceID, oldTag) != strings.TrimPrefix(b.EvidenceID, newTag) {
			t.Fatalf("event %d id tail differs: %q vs %q", i, a.EvidenceID, b.EvidenceID)
		}
		a.EvidenceID, b.EvidenceID = "", ""
		if !a.Start.Equal(b.Start) || a.Kind != b.Kind || a.Host != b.Host || *a.Value != *b.Value || a.Attrs["note"] != b.Attrs["note"] || a.Line != b.Line {
			t.Fatalf("event %d differs: %+v vs %+v", i, a, b)
		}
	}
	// Same query on a window and a kind filter.
	q := casestore.EventQuery{Kinds: []string{"wait"}, Limit: 1000}
	qa, qb := cfQuery(t, src, info.ID, q), cfQuery(t, dst, got.ID, q)
	if qa != qb || qa != 60 {
		t.Fatalf("kind query: %d vs %d", qa, qb)
	}
	// The pseudonym key travelled with the case.
	ca, _ := src.st.Open(context.Background(), info.ID)
	cb, _ := dst.st.Open(context.Background(), got.ID)
	defer ca.Close()
	defer cb.Close()
	if ca.Pseudonymize("user", "alice") != cb.Pseudonymize("user", "alice") {
		t.Fatal("pseudonyms differ after restore")
	}
	// The source case is untouched and still opens.
	if again := cfEvents2(t, src, info.ID); len(again) != 120 {
		t.Fatalf("source has %d events after export", len(again))
	}
}

func cfQuery(t *testing.T, e *cfEnv, id casestore.CaseID, q casestore.EventQuery) int {
	t.Helper()
	c, err := e.st.Open(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	evs, err := c.Events(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return len(evs)
}

func TestCaseFileRestoredTwiceMakesTwoCases(t *testing.T) {
	src, dst := newCFEnv(t), newCFEnv(t)
	info := cfSeed(t, src, "Twice", 10)
	file := filepath.Join(t.TempDir(), "twice"+casestore.CaseFileExt)
	if _, err := cfExport(t, src, info, file, cfPass); err != nil {
		t.Fatal(err)
	}
	a, err := cfRestore(dst, file, cfPass)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cfRestore(dst, file, cfPass)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || a.ID == info.ID || b.ID == info.ID {
		t.Fatalf("ids not distinct: %s %s %s", a.ID, b.ID, info.ID)
	}
	list, err := dst.st.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	for _, id := range []casestore.CaseID{a.ID, b.ID} {
		if n := len(cfEvents2(t, dst, id)); n != 10 {
			t.Fatalf("case %s has %d events", id, n)
		}
	}
	// Restoring into the exporting data folder works too and keeps the source.
	c, err := cfRestore(src, file, cfPass)
	if err != nil || c.ID == info.ID {
		t.Fatalf("same-folder restore: %v %v", c.ID, err)
	}
	if n := len(cfEvents2(t, src, info.ID)); n != 10 {
		t.Fatalf("source lost events: %d", n)
	}
}

func TestCaseFileOfAnOpenCase(t *testing.T) {
	src, dst := newCFEnv(t), newCFEnv(t)
	ctx := context.Background()
	info, err := src.st.Create(ctx, "Open while exported")
	if err != nil {
		t.Fatal(err)
	}
	c, err := src.st.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cfImport(t, c, "one", 40)
	file := filepath.Join(t.TempDir(), "open"+casestore.CaseFileExt)
	if _, err := cfExport(t, src, info, file, cfPass); err != nil {
		t.Fatalf("export of an open case: %v", err)
	}
	// The open case keeps working after the export.
	cfImport(t, c, "two", 5)
	got, err := cfRestore(dst, file, cfPass)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(cfEvents2(t, dst, got.ID)); n != 40 {
		t.Fatalf("restored %d events, want the 40 present at export", n)
	}
	evs, err := c.Events(ctx, casestore.EventQuery{Limit: 50000})
	if err != nil || len(evs) != 45 {
		t.Fatalf("source has %d events, %v", len(evs), err)
	}
}

func TestCaseFileRefusesOverwriteAndBadName(t *testing.T) {
	src := newCFEnv(t)
	info := cfSeed(t, src, "Overwrite", 5)
	dir := t.TempDir()
	file := filepath.Join(dir, "one"+casestore.CaseFileExt)
	if _, err := cfExport(t, src, info, file, cfPass); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(file)
	if _, err := cfExport(t, src, info, file, cfPass); !errors.Is(err, casestore.ErrCaseFileExists) {
		t.Fatalf("second export = %v, want ErrCaseFileExists", err)
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatal("existing file was modified")
	}
	if _, err := cfExport(t, src, info, filepath.Join(dir, "bad.txt"), cfPass); err == nil {
		t.Fatal("export accepted a name without the case-file extension")
	}
	// Nothing else was left in the folder: no temp file, no placeholder.
	if names := cfDirEntries(t, dir); len(names) != 1 {
		t.Fatalf("export folder holds %v", names)
	}
	// A missing case is not found.
	other, _ := casestore.NewID()
	if _, err := casestore.ExportCaseFile(context.Background(), src.root, other, "x", time.Now(), nil, src.lookup,
		filepath.Join(dir, "none"+casestore.CaseFileExt), []byte(cfPass), fastParams); !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("missing case = %v", err)
	}
	if names := cfDirEntries(t, dir); len(names) != 1 {
		t.Fatalf("failed export left %v", names)
	}
}

func TestCaseFileHoldsNoPlaintext(t *testing.T) {
	src := newCFEnv(t)
	info := cfSeed(t, src, "Title "+cfMarker, 30)
	file := filepath.Join(t.TempDir(), "p"+casestore.CaseFileExt)
	if _, err := cfExport(t, src, info, file, cfPass); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	keys, _, err := casestore.ReadKeyFileAny(filepath.Join(src.root, string(info.ID), casestore.KeyFileName), src.lookup)
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Zero()
	for _, needle := range [][]byte{
		[]byte(cfMarker), []byte("Title"), []byte(info.ID), []byte("test.csv"), []byte("web-a"), []byte("SQLite format"),
		[]byte("dbKey"), []byte("pseudoKey"), []byte("caseId"), keys.DB, keys.Pseudo, []byte(cfPass),
		[]byte(strings.ToLower(hexOf(keys.DB))), []byte(strings.ToLower(hexOf(keys.Pseudo))),
	} {
		if bytes.Contains(raw, needle) {
			t.Fatalf("case file contains plaintext %q", needle)
		}
	}
	if !bytes.HasPrefix(raw, []byte("UNRVCASE")) {
		t.Fatal("missing magic")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

func TestCaseFileFailuresAreIdenticalAndLeaveNothing(t *testing.T) {
	src, dst := newCFEnv(t), newCFEnv(t)
	info := cfSeed(t, src, "Tamper", 25)
	dir := t.TempDir()
	good := filepath.Join(dir, "good"+casestore.CaseFileExt)
	if _, err := cfExport(t, src, info, good, cfPass); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: the good file restores.
	if _, err := cfRestore(newCFEnv(t), good, cfPass); err != nil {
		t.Fatal(err)
	}
	headerEnd := 41 + int(raw[37])<<24 + int(raw[38])<<16 + int(raw[39])<<8 + int(raw[40])
	if headerEnd <= 41 || headerEnd >= len(raw) {
		t.Fatalf("header end %d of %d", headerEnd, len(raw))
	}
	flip := func(at int) []byte {
		b := append([]byte(nil), raw...)
		b[at] ^= 0x01
		return b
	}
	cases := map[string][]byte{
		"empty":              {},
		"magic only":         raw[:8],
		"wrong magic":        flip(0),
		"version":            flip(9),
		"kdf id":             flip(10),
		"time cost":          flip(14),
		"memory":             flip(17),
		"threads":            flip(19),
		"salt length":        flip(20),
		"salt":               flip(25),
		"header length":      flip(40),
		"header first byte":  flip(41),
		"header middle":      flip(41 + (headerEnd-41)/2),
		"header last byte":   flip(headerEnd - 1),
		"db first byte":      flip(headerEnd),
		"db middle":          flip(headerEnd + (len(raw)-headerEnd)/2),
		"db last byte":       flip(len(raw) - 1),
		"truncated one":      raw[:len(raw)-1],
		"truncated in db":    raw[:headerEnd+100],
		"truncated at db":    raw[:headerEnd],
		"truncated header":   raw[:headerEnd-5],
		"trailing byte":      append(append([]byte(nil), raw...), 0),
		"text file":          []byte("this is not a case file at all, just text\n"),
		"all zero prefix":    make([]byte, 200),
		"valid prefix, junk": append(append([]byte(nil), raw[:41]...), bytes.Repeat([]byte{7}, 400)...),
	}
	for name, content := range cases {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+casestore.CaseFileExt)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := cfRestore(dst, path, cfPass)
		if !errors.Is(err, casestore.ErrCaseFile) || err.Error() != casestore.CaseFileMessage {
			t.Errorf("%s: err = %v", name, err)
		}
		if names := cfDirEntries(t, dst.root); len(names) != 0 {
			t.Errorf("%s: restore left %v", name, names)
		}
	}
	// Wrong passphrase, a near miss, an empty one, a missing file and a folder.
	for name, tc := range map[string]struct{ path, pass string }{
		"wrong pass":   {good, "wrong horse battery staple"},
		"case change":  {good, strings.ToUpper(cfPass)},
		"empty pass":   {good, ""},
		"missing file": {filepath.Join(dir, "nope"+casestore.CaseFileExt), cfPass},
		"a folder":     {dir, cfPass},
	} {
		_, err := cfRestore(dst, tc.path, tc.pass)
		if !errors.Is(err, casestore.ErrCaseFile) || err.Error() != casestore.CaseFileMessage {
			t.Errorf("%s: err = %v", name, err)
		}
		if strings.Contains(err.Error(), dir) {
			t.Errorf("%s: error echoes the path", name)
		}
		if names := cfDirEntries(t, dst.root); len(names) != 0 {
			t.Errorf("%s: restore left %v", name, names)
		}
	}
	// A symbolic-link-like or non-regular source is refused the same way.
	// (Windows symlinks need privileges, so only the folder case runs here.)
}

func TestCaseFileLockedMasterKey(t *testing.T) {
	src, dst := newCFEnv(t), newCFEnv(t)
	info := cfSeed(t, src, "Locked", 3)
	file := filepath.Join(t.TempDir(), "l"+casestore.CaseFileExt)
	if _, err := cfExport(t, src, info, file, cfPass); err != nil {
		t.Fatal(err)
	}
	_, err := casestore.RestoreCaseFile(context.Background(), dst.st, casestore.DPAPIOnly(storetest.RandomProtector()), file, []byte(cfPass))
	if !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("locked restore = %v, want ErrLocked", err)
	}
	if names := cfDirEntries(t, dst.root); len(names) != 0 {
		t.Fatalf("locked restore left %v", names)
	}
	// Export of a case sealed under the master key also needs it.
	_, err = casestore.ExportCaseFile(context.Background(), src.root, info.ID, info.Title, info.Created, nil,
		casestore.DPAPIOnly(storetest.RandomProtector()), filepath.Join(t.TempDir(), "x"+casestore.CaseFileExt), []byte(cfPass), fastParams)
	if !errors.Is(err, casestore.ErrLocked) {
		t.Fatalf("locked export = %v, want ErrLocked", err)
	}
}

// cfRawCaseFile writes a case file around arbitrary database bytes.
func cfRawCaseFile(t *testing.T, path, id string, db []byte, keys casestore.Keys) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "raw.db")
	if err := os.WriteFile(dbPath, db, 0o600); err != nil {
		t.Fatal(err)
	}
	keys.CaseID = id
	_, err := casestore.WriteCaseFile(context.Background(), path, casestore.CaseFileSource{
		DBPath: dbPath, CaseID: id, Title: "Synthetic", Created: time.Now(), Keys: keys,
	}, []byte(cfPass), fastParams)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCaseFileWithUnusableDatabaseIsRolledBack(t *testing.T) {
	dst := newCFEnv(t)
	keys, err := casestore.NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	junk := make([]byte, 64<<10)
	_, _ = rand.Read(junk)
	file := filepath.Join(t.TempDir(), "junk"+casestore.CaseFileExt)
	cfRawCaseFile(t, file, string(mustID(t)), junk, keys)
	// The container is authentic and its hash is right, but the database is not one.
	_, err = cfRestore(dst, file, cfPass)
	if !errors.Is(err, casestore.ErrCaseFile) {
		t.Fatalf("junk database restore = %v", err)
	}
	if names := cfDirEntries(t, dst.root); len(names) != 0 {
		t.Fatalf("junk restore left %v", names)
	}
}

func TestCaseFileWithMismatchedIDIsRolledBack(t *testing.T) {
	src, dst := newCFEnv(t), newCFEnv(t)
	info := cfSeed(t, src, "Mismatch", 8)
	keys, _, err := casestore.ReadKeyFileAny(filepath.Join(src.root, string(info.ID), casestore.KeyFileName), src.lookup)
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Zero()
	db, err := os.ReadFile(filepath.Join(src.root, string(info.ID), "case.db"))
	if err != nil {
		t.Fatal(err)
	}
	// The header claims a different case id than the one inside the database.
	file := filepath.Join(t.TempDir(), "mm"+casestore.CaseFileExt)
	cfRawCaseFile(t, file, string(mustID(t)), db, keys)
	if _, err := cfRestore(dst, file, cfPass); !errors.Is(err, casestore.ErrCaseFile) {
		t.Fatalf("mismatched id restore = %v", err)
	}
	if names := cfDirEntries(t, dst.root); len(names) != 0 {
		t.Fatalf("mismatch restore left %v", names)
	}
}

func mustID(t *testing.T) casestore.CaseID {
	t.Helper()
	id, err := casestore.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRebindCaseDBIsAllOrNothing(t *testing.T) {
	src := newCFEnv(t)
	info := cfSeed(t, src, "Rebind", 30)
	dir := filepath.Join(src.root, string(info.ID))
	keys, _, err := casestore.ReadKeyFileAny(filepath.Join(dir, casestore.KeyFileName), src.lookup)
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Zero()
	work := filepath.Join(t.TempDir(), "w.db")
	if err := copyFile(filepath.Join(dir, "case.db"), work); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	newID := mustID(t)
	// The wrong old id changes nothing.
	wrong := mustID(t)
	if _, err := casestore.RebindCaseDB(ctx, work, keys.DB, string(wrong), string(newID)); err == nil {
		t.Fatal("rebind accepted a wrong old id")
	}
	n, err := casestore.RebindCaseDB(ctx, work, keys.DB, string(info.ID), string(newID))
	if err != nil || n != 30 {
		t.Fatalf("rebind = %d, %v", n, err)
	}
	// A second rebind from the old id now fails: the database no longer says it.
	if _, err := casestore.RebindCaseDB(ctx, work, keys.DB, string(info.ID), string(mustID(t))); err == nil {
		t.Fatal("rebind from a stale id succeeded")
	}
	// Rebinding onto the same 8-character tag moves no event rows.
	if _, err := casestore.RebindCaseDB(ctx, work, keys.DB, string(newID), string(newID)); err != nil {
		t.Fatalf("self rebind: %v", err)
	}
	// Bad ids are refused before anything is opened.
	if _, err := casestore.RebindCaseDB(ctx, work, keys.DB, "x", string(newID)); !errors.Is(err, casestore.ErrBadID) {
		t.Fatalf("bad id = %v", err)
	}
	// A wrong key is refused without echoing it.
	bad := bytes.Repeat([]byte{9}, 32)
	if _, err := casestore.RebindCaseDB(ctx, work, bad, string(newID), string(wrong)); err == nil || strings.Contains(err.Error(), "0909") {
		t.Fatalf("wrong key = %v", err)
	}
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func TestCaseFileSurvivesCancellation(t *testing.T) {
	src, dst := newCFEnv(t), newCFEnv(t)
	info := cfSeed(t, src, "Cancel", 5)
	dir := t.TempDir()
	file := filepath.Join(dir, "c"+casestore.CaseFileExt)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := casestore.ExportCaseFile(ctx, src.root, info.ID, info.Title, info.Created, nil, src.lookup, file, []byte(cfPass), fastParams); err == nil {
		t.Fatal("canceled export succeeded")
	}
	if names := cfDirEntries(t, dir); len(names) != 0 {
		t.Fatalf("canceled export left %v", names)
	}
	if _, err := cfExport(t, src, info, file, cfPass); err != nil {
		t.Fatal(err)
	}
	if _, err := casestore.RestoreCaseFile(ctx, dst.st, dst.lookup, file, []byte(cfPass)); err == nil {
		t.Fatal("canceled restore succeeded")
	}
	if names := cfDirEntries(t, dst.root); len(names) != 0 {
		t.Fatalf("canceled restore left %v", names)
	}
}

// TestCaseFileStreamsLargeDatabases round-trips a synthetic database through the
// container and checks that memory stays far below the database size. The
// default size is 64 MiB; UNRAVIS_SCALE=1 raises it to 1 GiB.
func TestCaseFileStreamsLargeDatabases(t *testing.T) {
	size := int64(64 << 20)
	if os.Getenv("UNRAVIS_SCALE") == "1" {
		size = 1 << 30
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "big.db")
	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	_, _ = rand.Read(chunk)
	h := sha256.New()
	for written := int64(0); written < size; written += int64(len(chunk)) {
		chunk[0] = byte(written >> 20) // vary the content a little
		if _, err := f.Write(chunk); err != nil {
			f.Close()
			t.Skipf("not enough disk space for a %d MiB synthetic case (need about 3x): %v", size>>20, err)
		}
		h.Write(chunk)
	}
	if err := f.Close(); err != nil {
		t.Skipf("not enough disk space for a %d MiB synthetic case: %v", size>>20, err)
	}
	want := h.Sum(nil)

	id := mustID(t)
	keys, err := casestore.NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Zero()
	file := filepath.Join(dir, "big"+casestore.CaseFileExt)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	total, err := casestore.WriteCaseFile(context.Background(), file, casestore.CaseFileSource{
		DBPath: dbPath, CaseID: string(id), Title: "Big", Created: time.Now(), Keys: keys,
	}, []byte(cfPass), fastParams)
	if err != nil {
		if isNoSpace(err) {
			t.Skipf("not enough disk space for the %d MiB case file: %v", size>>20, err)
		}
		t.Fatal(err)
	}
	cf, err := casestore.OpenCaseFile(file, []byte(cfPass))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.db")
	if err := cf.ExtractDB(context.Background(), out); err != nil {
		cf.Close()
		if isNoSpace(err) {
			t.Skipf("not enough disk space for the extracted database: %v", err)
		}
		t.Fatal(err)
	}
	cf.Close()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if total < size {
		t.Fatalf("file is %d bytes for a %d byte database", total, size)
	}
	got := sha256.New()
	rf, _ := os.Open(out)
	if _, err := io.Copy(got, rf); err != nil {
		t.Fatal(err)
	}
	rf.Close()
	if !bytes.Equal(got.Sum(nil), want) {
		t.Fatal("extracted database differs")
	}
	// TotalAlloc counts every allocation, so a whole-file read could not hide.
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("%d MiB database: write+open+extract %v, allocated %d MiB, heap in use after %d MiB",
		size>>20, elapsed.Round(time.Millisecond), allocated>>20, after.HeapAlloc>>20)
	if limit := uint64(160 << 20); allocated > limit {
		t.Fatalf("allocated %d MiB for a %d MiB database, want under %d MiB", allocated>>20, size>>20, limit>>20)
	}
}

func isNoSpace(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not enough space") || strings.Contains(s, "no space") || strings.Contains(s, "disk full")
}

func TestCaseFileFormatConstants(t *testing.T) {
	if casestore.CaseFileExt != ".unravis-case" {
		t.Fatal(casestore.CaseFileExt)
	}
	if casestore.CaseFileMessage != "That file could not be opened with this master key." {
		t.Fatal(casestore.CaseFileMessage)
	}
}

func TestCaseFileRestoreRewritesLedgerEvidencePrefix(t *testing.T) {
	src, dst := newCFEnv(t), newCFEnv(t)
	ctx := context.Background()
	info := cfSeed(t, src, "Ledger travels", 5)
	oldTag := "ev:" + string(info.ID)[:8] + ":"
	blob := []byte(`{"version":1,"hypotheses":[{"evidenceIds":["` + oldTag + `test.csv:abc"]}]}`)

	c, err := src.st.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.(casestore.LedgerBlobStore).SetLedgerBlob(ctx, blob); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	file := filepath.Join(t.TempDir(), "ledger"+casestore.CaseFileExt)
	if _, err := cfExport(t, src, info, file, cfPass); err != nil {
		t.Fatal(err)
	}
	got, err := cfRestore(dst, file, cfPass)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := dst.st.Open(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	have, err := rc.(casestore.LedgerBlobStore).LedgerBlob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newTag := "ev:" + string(got.ID)[:8] + ":"
	if want := strings.ReplaceAll(string(blob), oldTag, newTag); string(have) != want {
		t.Fatalf("restored ledger blob = %s, want %s", have, want)
	}
	// The source keeps its own ledger untouched.
	sc, _ := src.st.Open(ctx, info.ID)
	defer sc.Close()
	if keep, _ := sc.(casestore.LedgerBlobStore).LedgerBlob(ctx); !bytes.Equal(keep, blob) {
		t.Fatalf("source ledger changed: %s", keep)
	}
}
