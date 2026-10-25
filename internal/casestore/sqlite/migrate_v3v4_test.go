package sqlite

// TSK-20: the back-compat proof. A case sealed the way the build before the master
// key sealed it (case.key a bare DPAPI blob, schema 3) must open here with no
// master key, read the same, migrate to schema 4 exactly once, and take tags; and
// after the master key is set and the case re-wrapped it must read the same, open
// only while the keyring is unlocked, and keep case.db byte for byte.
//
// No fixture file is committed. buildV3Case makes the old case with this build's
// own code and then pins it to what the old build wrote: the two schema-4 tables
// dropped and schema_version 3 (the same technique as TestVersionThreeCaseMigrates
// ToFourOnce). The DPAPI protector is the AES-GCM stand-in the neighbouring tests
// use, so nothing here is Windows-only. The data is synthetic.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/masterkey"
)

// v3Case is an old-style case on disk: its folder, the DPAPI stand-in that sealed
// it and its id.
type v3Case struct {
	root string
	dir  string
	p    *storetest.Protector
	id   casestore.CaseID
	want snapshot // every read of the case taken while it sat at schema 3
}

// buildV3Case writes a case the old way: two imports (4 and 6 events), schema 3,
// key file and manifest sealed by the DPAPI kind only. The case is closed (no open
// handle, database checkpointed by Close) when it returns.
func buildV3Case(t *testing.T) v3Case {
	t.Helper()
	p := storetest.RandomProtector()
	root := filepath.Join(t.TempDir(), "cases")
	s := newStoreAt(t, root, p)
	info := mustCreate(t, s, "Old v3 case")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "alpha", 4)
	mustImport(t, c, "beta", 6)
	if v := schemaOf(t, c); v != strconv.Itoa(schemaVersion) {
		t.Fatalf("a new case is at schema %s", v)
	}
	downgradeToV3(t, c)
	// The baseline is read on the same handle after the downgrade: the data tables
	// are exactly what the old build had, and nothing here reads a tag table.
	want := takeSnapshot(t, c)
	if want.NEvents != 10 {
		t.Fatalf("baseline events = %d, want 10", want.NEvents)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, string(info.ID))
	// Positive controls: it really is the old on-disk shape.
	for _, n := range []string{keyName, manifestName} {
		raw := readFile(t, filepath.Join(dir, n))
		if len(raw) == 0 || raw[0] == '{' {
			t.Fatalf("%s has a v2 header; the old build wrote a bare blob", n)
		}
		if k, err := casestore.FileKind(filepath.Join(dir, n)); err != nil || k != casestore.KindDPAPI {
			t.Fatalf("%s kind = %q, %v", n, k, err)
		}
	}
	return v3Case{root: root, dir: dir, p: p, id: info.ID, want: want}
}

// oldBuild is a store as the old build ran it: DPAPI protector only, no master key.
func (v v3Case) oldBuild(t *testing.T) *Store { return newStoreAt(t, v.root, v.p) }

// withKey is this build with a master key keyring wired in (a DPAPI stand-in of
// its own, as after a restart).
func (v v3Case) withKey(t *testing.T, kr *masterkey.Keyring) *Store {
	return newStoreAt(t, v.root, v.p, func(o *Options) { o.Protectors = keyringLookup(kr) })
}

// snapshot is every read the product makes of a case, as JSON so two snapshots
// compare without time-location noise.
type snapshot struct {
	Info        string
	Imports     string
	Events      string
	Quarantines string
	Series      string
	NEvents     int
}

func takeSnapshot(t *testing.T, c *Case) snapshot {
	t.Helper()
	ctx := context.Background()
	j := func(v any, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	info := c.Info()
	imports, err := c.Imports(ctx)
	if err != nil {
		t.Fatal(err)
	}
	events, err := c.Events(ctx, casestore.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	quar, err := c.Quarantines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Both event kinds over the whole span, plus a second bucket width, so a changed
	// event, host or unit shows up in the statistics too.
	var series []casestore.SeriesResult
	for _, bucket := range []int64{1000, 3000} {
		res, err := casestore.Series(ctx, c, casestore.SeriesQuery{
			From: testBase, To: testBase.Add(30 * time.Second), BucketMs: bucket,
			Sel: []string{"kind=cpu", "kind=wait;host=db-a;source=" + testImporter, "kind=cpu;field=duration"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Partial {
			t.Fatal("series partial")
		}
		series = append(series, res)
	}
	return snapshot{
		Info: j(info, nil), Imports: j(imports, nil), Events: j(events, nil),
		Quarantines: j(quar, nil), Series: j(series, nil), NEvents: len(events),
	}
}

func (a snapshot) mustEqual(t *testing.T, what string, b snapshot) {
	t.Helper()
	if a.NEvents != b.NEvents {
		t.Errorf("%s: events %d vs %d", what, a.NEvents, b.NEvents)
	}
	for _, f := range []struct{ name, x, y string }{
		{"Info", a.Info, b.Info}, {"Imports", a.Imports, b.Imports}, {"Events", a.Events, b.Events},
		{"Quarantines", a.Quarantines, b.Quarantines}, {"Series", a.Series, b.Series},
	} {
		if f.x != f.y {
			t.Errorf("%s: %s differs:\n before %.300s\n after  %.300s", what, f.name, f.x, f.y)
		}
	}
}

// folderFiles hashes every file of the case folder by name.
func folderFiles(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][32]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out[e.Name()] = sha256.Sum256(readFile(t, filepath.Join(dir, e.Name())))
	}
	return out
}

func fileNames(m map[string][32]byte) []string {
	var n []string
	for k := range m {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// metaRows counts the schema_version rows and returns the version.
func versionRows(t *testing.T, c *Case) (string, int) {
	t.Helper()
	return schemaOf(t, c), scalar(t, c, "SELECT COUNT(*) FROM meta WHERE k='schema_version'")
}

// (a) A case sealed the old way opens with no master key, reads identically,
// migrates to schema 4 exactly once, reopens twice cleanly, and takes tags.
func TestV3CaseOpensWithNoMasterKeyAndMigratesOnce(t *testing.T) {
	ctx := context.Background()
	v := buildV3Case(t)
	keyBefore := readFile(t, filepath.Join(v.dir, keyName))

	want := v.want

	// First open on this build, no master key at all (Protectors nil).
	s := v.oldBuild(t)
	c := mustOpen(t, s, v.id)
	if ver, rows := versionRows(t, c); ver != "4" || rows != 1 {
		t.Fatalf("after first open: schema %s, %d version rows", ver, rows)
	}
	if !tableExists(t, c, "case_sources") || !tableExists(t, c, "case_envs") {
		t.Fatal("the migration did not add the tag tables")
	}
	got := takeSnapshot(t, c)
	want.mustEqual(t, "first open", got)
	if got.NEvents != 10 {
		t.Fatalf("events after migration = %d", got.NEvents)
	}
	if i := c.Info(); i.Envs != 0 || i.Sources != 0 || i.Imports != 2 {
		t.Errorf("Info after migration: %+v", i)
	}
	if es, err := c.Envs(ctx); err != nil || len(es) != 0 {
		t.Errorf("Envs: %v %v", es, err)
	}
	if ss, err := c.Sources(ctx); err != nil || len(ss) != 0 {
		t.Errorf("Sources: %v %v", ss, err)
	}
	// Opening did not re-seal anything: still a v1 DPAPI case, key file untouched.
	if k, err := s.CaseKind(v.id); err != nil || k != casestore.KindDPAPI {
		t.Fatalf("CaseKind after open = %q, %v", k, err)
	}
	if !bytes.Equal(keyBefore, readFile(t, filepath.Join(v.dir, keyName))) {
		t.Error("opening an old case rewrote case.key")
	}
	objs := objects(t, c)
	imports, err := c.Imports(ctx)
	if err != nil || len(imports) != 2 {
		t.Fatalf("imports: %v %v", imports, err)
	}

	// A source added after the migration is tagged normally, and so is an import.
	if err := c.SetEnvs(ctx, []string{"Production", "Staging"}); err != nil {
		t.Fatal(err)
	}
	late := mustImport(t, c, "gamma", 3)
	tagA, err := c.AddSource(ctx, casestore.SourceTag{Kind: "iis", Env: "Production", Portal: "Admin", API: "Orders", ImportID: imports[0].ID, Classification: casestore.ClassAudit})
	if err != nil {
		t.Fatal(err)
	}
	tagB, err := c.AddSource(ctx, casestore.SourceTag{Kind: "sql", Label: "late import", Env: "Staging", ImportID: late.Record.ID})
	if err != nil {
		t.Fatal(err)
	}
	if i := c.Info(); i.Envs != 2 || i.Sources != 2 || i.Imports != 3 {
		t.Errorf("Info after tagging: %+v", i)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen twice more: no second migration (version stays 4 with one row, schema
	// objects stable, tags and the new import kept), key file still untouched.
	for round := 1; round <= 2; round++ {
		c = mustOpen(t, v.oldBuild(t), v.id)
		if ver, rows := versionRows(t, c); ver != "4" || rows != 1 {
			t.Fatalf("reopen %d: schema %s, %d rows", round, ver, rows)
		}
		if !reflect.DeepEqual(objs, objects(t, c)) {
			t.Errorf("reopen %d: schema objects changed", round)
		}
		if es, _ := c.Envs(ctx); !reflect.DeepEqual(es, []string{"Production", "Staging"}) {
			t.Errorf("reopen %d: Envs %v", round, es)
		}
		ss, err := c.Sources(ctx)
		if err != nil || len(ss) != 2 || !reflect.DeepEqual(ss[0], tagA) || !reflect.DeepEqual(ss[1], tagB) {
			t.Errorf("reopen %d: Sources %+v %v", round, ss, err)
		}
		if i := c.Info(); i.Envs != 2 || i.Sources != 2 || i.Imports != 3 {
			t.Errorf("reopen %d: Info %+v", round, i)
		}
		if n := scalar(t, c, "SELECT COUNT(*) FROM events"); n != 13 {
			t.Errorf("reopen %d: %d events, want 13", round, n)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(keyBefore, readFile(t, filepath.Join(v.dir, keyName))) {
		t.Error("case.key changed across the reopens")
	}
	if k, _ := v.oldBuild(t).CaseKind(v.id); k != casestore.KindDPAPI {
		t.Errorf("kind at the end = %q", k)
	}
}

// (b) After the master key is set and the case re-wrapped, the case reads the
// same, opens only while the keyring is unlocked, and case.db is byte-identical.
func TestV3CaseRewrapsToMasterKeyAndKeepsItsDatabase(t *testing.T) {
	for _, migrateFirst := range []bool{true, false} {
		name := "rewrap-before-first-open-on-this-build"
		if migrateFirst {
			name = "migrated-then-rewrapped"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			v := buildV3Case(t)
			dbPath := filepath.Join(v.dir, dbName)

			want := v.want
			// Untouched since it was written: the hash every later step is held to.
			dbHash0 := fileHash(t, dbPath)

			if migrateFirst {
				// Open with no master key (migrates to 4), close.
				c := mustOpen(t, v.oldBuild(t), v.id)
				if schemaOf(t, c) != "4" {
					t.Fatal("not migrated")
				}
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
				if fileHash(t, dbPath) == dbHash0 {
					t.Fatal("positive control: migrating should have changed case.db")
				}
			}

			// Set the master key (its own data folder) and unlock it.
			kdir := t.TempDir()
			if err := masterkey.Setup(kdir, []byte("correct horse battery")); err != nil {
				t.Fatal(err)
			}
			kr, err := masterkey.Unlock(kdir, []byte("correct horse battery"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(kr.Lock)

			before := folderFiles(t, v.dir)
			dbHash := before[dbName]
			if !migrateFirst && dbHash != dbHash0 {
				t.Fatal("case.db changed before the re-wrap")
			}
			s := v.withKey(t, kr)
			if k, err := s.CaseKind(v.id); err != nil || k != casestore.KindDPAPI {
				t.Fatalf("CaseKind before = %q, %v", k, err)
			}
			if err := s.Rewrap(ctx, v.id); err != nil {
				t.Fatal(err)
			}

			// case.db untouched; key file is v2 with the exact header; manifest too.
			after := folderFiles(t, v.dir)
			if after[dbName] != dbHash || sha256.Sum256(readFile(t, dbPath)) != dbHash {
				t.Fatal("case.db bytes changed by the re-wrap")
			}
			if !reflect.DeepEqual(fileNames(before), fileNames(after)) {
				t.Errorf("files before %v, after %v", fileNames(before), fileNames(after))
			}
			for _, n := range []string{keyName, manifestName} {
				raw := readFile(t, filepath.Join(v.dir, n))
				if !bytes.HasPrefix(raw, []byte("{\"v\":2,\"p\":\"masterkey\"}\n")) || len(raw) <= len("{\"v\":2,\"p\":\"masterkey\"}\n") {
					t.Errorf("%s is not v2 header + blob: %.40q", n, raw)
				}
				if after[n] == before[n] {
					t.Errorf("%s was not re-sealed", n)
				}
			}
			if k, err := s.CaseKind(v.id); err != nil || k != casestore.KindMasterKey {
				t.Fatalf("CaseKind after = %q, %v", k, err)
			}

			// Unlocked: opens (a new process: fresh store, other DPAPI stand-in) and
			// reads exactly what the old build read.
			s2 := newStoreAt(t, v.root, storetest.RandomProtector(), func(o *Options) { o.Protectors = keyringLookup(kr) })
			c := mustOpen(t, s2, v.id)
			if ver, rows := versionRows(t, c); ver != "4" || rows != 1 {
				t.Fatalf("schema %s, %d rows", ver, rows)
			}
			want.mustEqual(t, "after re-wrap", takeSnapshot(t, c))
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			// Locked: a clear error, no panic, no partial open, nothing written.
			folder := folderFiles(t, v.dir)
			kr.Lock()
			openLocked := func(st *Store) {
				t.Helper()
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("Open panicked while locked: %v", r)
					}
				}()
				h, err := st.Open(ctx, v.id)
				if !errors.Is(err, casestore.ErrLocked) || !errors.Is(err, masterkey.ErrLocked) {
					t.Fatalf("Open while locked = %v, want ErrLocked", err)
				}
				if h != nil {
					t.Fatal("a handle came back with the error: partial open")
				}
				st.mu.Lock()
				n := len(st.open)
				st.mu.Unlock()
				if n != 0 {
					t.Fatalf("%d case(s) registered open after a refused Open", n)
				}
			}
			openLocked(newStoreAt(t, v.root, storetest.RandomProtector(), func(o *Options) { o.Protectors = keyringLookup(kr) }))
			openLocked(v.oldBuild(t)) // the old DPAPI-only wiring, no keyring at all
			openLocked(newStoreAt(t, v.root, v.p))
			if got := folderFiles(t, v.dir); !reflect.DeepEqual(got, folder) {
				t.Errorf("a refused open changed the case folder: %v -> %v", fileNames(folder), fileNames(got))
			}
			if _, err := os.Stat(dbPath + "-wal"); err == nil {
				t.Error("a refused open left a -wal file: the database was opened")
			}

			// Unlock again: opens and reads the same.
			kr2, err := masterkey.Unlock(kdir, []byte("correct horse battery"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(kr2.Lock)
			c = mustOpen(t, newStoreAt(t, v.root, storetest.RandomProtector(), func(o *Options) { o.Protectors = keyringLookup(kr2) }), v.id)
			want.mustEqual(t, "after unlock again", takeSnapshot(t, c))
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// An old case opened by a build WITH a master key set up but not yet applied to
// the case (the usual state right after the user sets a key) still opens through
// the DPAPI path, with the keyring locked or unlocked.
func TestV3CaseStillOpensWhenAMasterKeyExistsButIsNotApplied(t *testing.T) {
	ctx := context.Background()
	v := buildV3Case(t)
	kdir := t.TempDir()
	if err := masterkey.Setup(kdir, []byte("correct horse battery")); err != nil {
		t.Fatal(err)
	}
	kr, err := masterkey.Unlock(kdir, []byte("correct horse battery"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Lock)
	for _, locked := range []bool{false, true} {
		if locked {
			kr.Lock()
		}
		c := mustOpen(t, v.withKey(t, kr), v.id)
		if schemaOf(t, c) != "4" {
			t.Fatalf("locked=%v: schema %s", locked, schemaOf(t, c))
		}
		if n := takeSnapshot(t, c).NEvents; n != 10 {
			t.Fatalf("locked=%v: %d events", locked, n)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if k, err := v.withKey(t, kr).CaseKind(v.id); err != nil || k != casestore.KindDPAPI {
			t.Fatalf("locked=%v: kind %q %v", locked, k, err)
		}
	}
	_ = ctx
}
