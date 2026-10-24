package sqlite

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

func TestWrongKey(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "a", 5)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// Seal a different, well-formed key pair over the real one.
	other, err := casestore.NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	other.CaseID = string(info.ID) // well-formed for this case, so only the key bytes are wrong
	keyPath := filepath.Join(s.root, string(info.ID), keyName)
	if err := casestore.WriteKeyFile(keyPath, s.opt.Protector, other); err != nil {
		t.Fatal(err)
	}
	_, err = s.Open(context.Background(), info.ID)
	if !errors.Is(err, ErrWrongKey) {
		t.Fatalf("Open with the wrong key = %v, want ErrWrongKey", err)
	}
	if err != nil && strings.Contains(err.Error(), hex.EncodeToString(other.DB)) {
		t.Fatal("error text carries key material")
	}
}

func TestNoKeyBytesOrPlaintextInFiles(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, sentinel+" title")
	dir := filepath.Join(s.root, string(info.ID))
	keys, err := casestore.ReadKeyFile(filepath.Join(dir, keyName), s.opt.Protector)
	if err != nil {
		t.Fatal(err)
	}
	needles := [][]byte{keys.DB, keys.Pseudo, []byte(hex.EncodeToString(keys.DB)), []byte(hex.EncodeToString(keys.Pseudo)),
		[]byte(sentinel), []byte("SQLite format 3"), []byte("CREATE TABLE")}

	c := mustOpen(t, s, info.ID)
	evs := tEvents(c, "s", 200)
	for i := range evs {
		evs[i].Host = sentinel + "-host"
		evs[i].Attrs = map[string]string{"counter": sentinel}
	}
	body := bytes.NewReader([]byte(sentinel + " body\n"))
	midImport := map[string][]byte{}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep, err := tRun(evs)(r, emit)
		// While the transaction is open, whatever has reached the WAL and SHM must be ciphertext.
		for _, n := range []string{dbName, dbName + "-wal", dbName + "-shm"} {
			if b, rerr := os.ReadFile(filepath.Join(dir, n)); rerr == nil {
				midImport[n] = b
			}
		}
		return rep, err
	}
	if _, err := c.Import(ctx, tMeta(sentinel+".csv"), body, run); err != nil {
		t.Fatal(err)
	}
	// Positive control: the sentinel really is stored, and readable through the store.
	got, err := c.Events(ctx, casestore.EventQuery{Limit: 1})
	if err != nil || len(got) != 1 || got[0].Attrs["counter"] != sentinel {
		t.Fatalf("sentinel not stored: %+v, %v", got, err)
	}
	if len(midImport[dbName]) == 0 {
		t.Fatal("could not read case.db during the import")
	}
	for name, b := range midImport {
		if hasAny(b, needles...) {
			t.Fatalf("%s holds key bytes or plaintext during the import", name)
		}
	}
	check := func(stage string) {
		t.Helper()
		seen := 0
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Logf("%s: cannot read %s (%v)", stage, e.Name(), err)
				continue
			}
			seen++
			// case.key holds the keys sealed, never plain; every file must be free of both.
			if hasAny(b, needles...) {
				t.Fatalf("%s: %s holds key bytes or plaintext", stage, e.Name())
			}
		}
		if seen < 3 {
			t.Fatalf("%s: only %d files could be scanned", stage, seen)
		}
	}
	check("open")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	check("closed")
	raw := readFile(t, filepath.Join(dir, dbName))
	if len(raw) < 4096 || bytes.HasPrefix(raw, []byte("SQLite format 3\x00")) {
		t.Fatal("case.db has a plaintext SQLite header")
	}
}

// TestKillBeforeCommit runs an import in a child process that dies with os.Exit
// after emitting events but before commit.
func TestKillBeforeCommit(t *testing.T) {
	if os.Getenv("CS_KILL_CHILD") == "1" {
		killChild()
		return
	}
	ctx := context.Background()
	key := bytes.Repeat([]byte{0x42}, 32)
	prot, err := storetest.NewProtector(key)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "cases")
	s := newStoreAt(t, root, prot)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "kept", 10)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestKillBeforeCommit$")
	cmd.Env = append(os.Environ(), "CS_KILL_CHILD=1", "CS_ROOT="+root, "CS_ID="+string(info.ID), "CS_KEY="+hex.EncodeToString(key))
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("child did not die at the kill point: %v\n%s", err, out)
	}

	s2 := newStoreAt(t, root, prot)
	c2 := mustOpen(t, s2, info.ID) // quick_check runs here
	imports, events, quarantined := counts(t, c2)
	if imports != 1 || events != 10 || quarantined != 0 {
		t.Fatalf("after the kill: %d imports, %d events, %d quarantined; want 1, 10, 0", imports, events, quarantined)
	}
	if err := c2.IntegrityCheck(ctx); err != nil {
		t.Fatal(err)
	}
	// The killed file can be imported again.
	if _, err := c2.Import(ctx, tMeta("killed.csv"), tBody("killed"), tRun(tEvents(c, "k", 4))); err != nil {
		t.Fatalf("re-import after kill: %v", err)
	}
}

func killChild() {
	key, _ := hex.DecodeString(os.Getenv("CS_KEY"))
	prot, err := storetest.NewProtector(key)
	if err != nil {
		os.Exit(10)
	}
	s, err := New(Options{Root: os.Getenv("CS_ROOT"), Protector: prot, MinFreeBytes: -1})
	if err != nil {
		os.Exit(11)
	}
	c, err := s.Open(context.Background(), casestore.CaseID(os.Getenv("CS_ID")))
	if err != nil {
		os.Exit(12)
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		_, _ = io.Copy(io.Discard, r)
		for _, e := range tEvents(c, "doomed", 3000) {
			if err := emit(e); err != nil {
				os.Exit(13)
			}
		}
		os.Exit(3) // no commit, no rollback, no close
		return importer.Report{}, nil
	}
	_, _ = c.Import(context.Background(), tMeta("doomed.csv"), tBody("doomed"), run)
	os.Exit(14)
}

func TestQuickCheckCatchesCorruption(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	dbPath := filepath.Join(s.root, string(info.ID), dbName)
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "a", 3000)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	good := readFile(t, dbPath)
	if len(good) < 4096*6 {
		t.Fatalf("database is only %d bytes", len(good))
	}
	for _, off := range []int{4096*3 + 100, 4096*4 + 7, len(good) - 4096 + 50} {
		bad := append([]byte(nil), good...)
		bad[off] ^= 0xff
		if err := os.WriteFile(dbPath, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := s.Open(ctx, info.ID)
		if !errors.Is(err, ErrDamaged) {
			t.Fatalf("flipped byte at %d: Open = %v, want ErrDamaged", off, err)
		}
	}
	// The undamaged file still opens, so the checks above were about the flip.
	if err := os.WriteFile(dbPath, good, 0o600); err != nil {
		t.Fatal(err)
	}
	c2, err := s.Open(ctx, info.ID)
	if err != nil {
		t.Fatalf("restored database: %v", err)
	}
	_ = c2.Close()
}

func TestManifestCrossCheck(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	dir := filepath.Join(s.root, string(info.ID))
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "first", 4)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	old := readFile(t, filepath.Join(dir, dbName))
	c = mustOpen(t, s, info.ID)
	mustImport(t, c, "second", 4)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// A database rolled back to an earlier state is missing an import the manifest records.
	if err := os.WriteFile(filepath.Join(dir, dbName), old, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, info.ID); !errors.Is(err, ErrDamaged) {
		t.Fatalf("rolled-back database: Open = %v, want ErrDamaged", err)
	}
}

func TestCrashBetweenCommitAndMirror(t *testing.T) {
	ctx := context.Background()
	fail := true
	s, _ := newTestStore(t, func(o *Options) {
		o.afterCommit = func() error {
			if fail {
				return errors.New("simulated crash before the manifest write")
			}
			return nil
		}
	})
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "a", 4) // committed; manifest not updated
	if got := c.Info().Imports; got != 1 {
		t.Fatalf("in-memory manifest = %d imports", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen with the on-disk manifest still at zero imports: the database is
	// ahead, which is accepted and mirrored again.
	fail = false
	c2, err := s.Open(ctx, info.ID)
	if err != nil {
		t.Fatalf("Open with the database ahead of the manifest: %v", err)
	}
	defer c2.Close()
	infos, _ := s.List(ctx)
	if len(infos) != 1 || infos[0].Imports != 1 {
		t.Fatalf("List = %+v, want the import mirrored", infos)
	}
	recs, _ := c2.Imports(ctx)
	if len(recs) != 1 || recs[0].FileName != "a.csv" {
		t.Fatalf("imports = %+v", recs)
	}
}

func TestCapsRefusedWithCaseUntouched(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		opt  func(*Options)
		body func() io.Reader
		n    int
	}{
		{"file bytes, streamed", func(o *Options) { o.MaxFileBytes = 1000 }, func() io.Reader { return bytes.NewReader(make([]byte, 5000)) }, 3},
		{"file bytes, reported size", func(o *Options) { o.MaxFileBytes = 1000 }, func() io.Reader { return sized(50_000) }, 3},
		{"file rows", func(o *Options) { o.MaxFileRows = 5 }, func() io.Reader { return tBody("rows") }, 10},
		{"database size inside sqlite", func(o *Options) { o.MaxDBBytes = 256 << 10 }, func() io.Reader { return tBody("big") }, 6000},
		{"case bytes, reported size", func(o *Options) { o.MaxCaseBytes = 256 << 10 }, func() io.Reader { return sized(1 << 20) }, 3},
		{"all cases bytes", func(o *Options) { o.MaxTotalBytes = 1 }, func() io.Reader { return tBody("total") }, 3},
		{"free disk floor", func(o *Options) { o.MinFreeBytes = 1 << 60 }, func() io.Reader { return tBody("disk") }, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore(t, tc.opt)
			c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
			_, err := c.Import(ctx, tMeta("cap.csv"), tc.body(), tRun(tEvents(c, "cap", tc.n)))
			if !errors.Is(err, casestore.ErrCapExceeded) {
				t.Fatalf("Import = %v, want ErrCapExceeded", err)
			}
			t.Logf("refused: %v", err)
			imports, events, quarantined := counts(t, c)
			if imports != 0 || events != 0 || quarantined != 0 {
				t.Fatalf("refused import left %d imports, %d events, %d quarantined", imports, events, quarantined)
			}
			if got := c.Info().Imports; got != 0 {
				t.Fatalf("manifest records %d imports", got)
			}
			if err := c.IntegrityCheck(ctx); err != nil {
				t.Fatalf("database damaged by the refused import: %v", err)
			}
		})
	}
	t.Run("a small import still fits under the case cap", func(t *testing.T) {
		s, _ := newTestStore(t, func(o *Options) { o.MaxDBBytes = 256 << 10 })
		c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
		if _, err := c.Import(ctx, tMeta("big.csv"), tBody("big"), tRun(tEvents(c, "big", 6000))); !errors.Is(err, casestore.ErrCapExceeded) {
			t.Fatalf("big import = %v", err)
		}
		mustImport(t, c, "small", 5)
		if imports, events, _ := counts(t, c); imports != 1 || events != 5 {
			t.Fatalf("after the small import: %d imports, %d events", imports, events)
		}
	})
}

func TestEraseMakesSavedCopyUnreadable(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Case")
	dir := filepath.Join(root, string(info.ID))
	c := mustOpen(t, s, info.ID)
	evs := tEvents(c, "e", 300)
	for i := range evs {
		evs[i].Attrs = map[string]string{"counter": sentinel}
	}
	if _, err := c.Import(ctx, tMeta("e.csv"), tBody("e"), tRun(evs)); err != nil {
		t.Fatal(err)
	}
	keys, err := casestore.ReadKeyFile(filepath.Join(dir, keyName), s.opt.Protector)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// An attacker's saved copy of the database alone (backup, shadow copy).
	saved := t.TempDir()
	if err := os.WriteFile(filepath.Join(saved, dbName), readFile(t, filepath.Join(dir, dbName)), 0o600); err != nil {
		t.Fatal(err)
	}
	readCopy := func(k casestore.Keys) (int, error) {
		db, err := s.openDB(ctx, saved, k, false)
		if err != nil {
			return 0, err
		}
		defer db.Close()
		var n int
		err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&n)
		return n, mapOpenErr(err)
	}
	// Positive control: with the key, the copy reads.
	if n, err := readCopy(keys); err != nil || n != 300 {
		t.Fatalf("control read of the copy = %d, %v", n, err)
	}

	hookCalls := 0
	s.opt.OnErase = func(_ context.Context, got casestore.CaseID) error {
		hookCalls++
		if got != info.ID {
			t.Errorf("OnErase id = %s", got)
		}
		return nil
	}
	if err := s.Erase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if hookCalls != 1 {
		t.Fatalf("OnErase called %d times", hookCalls)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("case folder survived erase: %v", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("%d entries left under the cases folder", len(entries))
	}
	if _, err := casestore.ReadKeyFile(filepath.Join(dir, keyName), s.opt.Protector); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file still readable: %v", err)
	}
	if _, err := s.Open(ctx, info.ID); !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("Open after erase = %v", err)
	}
	// Without the key, the copy is noise: a fresh key does not read it, and it holds no plaintext.
	fresh, _ := casestore.NewKeys()
	if _, err := readCopy(fresh); err == nil {
		t.Fatal("saved copy opened with a key that is not the case key")
	}
	raw := readFile(t, filepath.Join(saved, dbName))
	if hasAny(raw, []byte(sentinel), []byte("SQLite format 3"), keys.DB, keys.Pseudo) {
		t.Fatal("saved copy holds plaintext")
	}
	// Erase twice is fine, and a second hook call is made (the hook must be idempotent).
	if err := s.Erase(ctx, info.ID); err != nil {
		t.Fatalf("second erase: %v", err)
	}
	if hookCalls != 2 {
		t.Fatalf("OnErase called %d times after the retry", hookCalls)
	}
}

func TestEraseWhileOpenAndRetryAfterHookFailure(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "a", 5)
	fail := true
	s.opt.OnErase = func(context.Context, casestore.CaseID) error {
		if fail {
			return errors.New("export deletion failed")
		}
		return nil
	}
	if err := s.Erase(ctx, info.ID); err == nil {
		t.Fatal("hook failure was swallowed")
	}
	// The files and key are already gone; only the hook work is outstanding, and
	// the folder keeps just the pending-erase marker so a retry can find it.
	dir := filepath.Join(root, string(info.ID))
	left, _ := os.ReadDir(dir)
	if len(left) != 1 || left[0].Name() != eraseMarker {
		t.Fatalf("after a failed hook the folder holds %d entries, want only the marker", len(left))
	}
	if _, err := s.Open(ctx, info.ID); !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("Open of a half-erased case = %v, want ErrNotFound", err)
	}
	if _, err := c.Events(ctx, casestore.EventQuery{}); !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("handle after erase = %v, want ErrNotFound", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close after erase: %v", err)
	}
	fail = false
	if err := s.Erase(ctx, info.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("folder left after a successful retry: %d entries", len(entries))
	}
}

func TestSweepBoundary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2001, 5, 6, 7, 8, 9, 0, time.UTC)
	s, _ := newTestStore(t, func(o *Options) { o.Now = func() time.Time { return now } })
	info := mustCreate(t, s, "Closed")
	open := mustCreate(t, s, "Open")
	if err := s.CloseCase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Sweep(ctx, now.Add(30*24*time.Hour)); err != nil || len(got) != 0 {
		t.Fatalf("at exactly 30 days = %v, %v", got, err)
	}
	got, err := s.Sweep(ctx, now.Add(30*24*time.Hour+time.Second))
	if err != nil || len(got) != 1 || got[0] != info.ID {
		t.Fatalf("after 30 days = %v, %v", got, err)
	}
	if infos, _ := s.List(ctx); len(infos) != 1 || infos[0].ID != open.ID {
		t.Fatalf("List = %+v", infos)
	}
}

func TestConcurrentImportsAreSerialised(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	// A second handle from a second Store on the same folder: another window, same user.
	s2 := newStoreAt(t, root, s.opt.Protector)
	var active, peak atomic.Int32
	run := func(label string) casestore.RunFunc {
		return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			n := active.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(60 * time.Millisecond)
			rep, err := tRun(tEvents(c, label, 50))(r, emit)
			active.Add(-1)
			return rep, err
		}
	}
	const workers = 6
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			label := string(rune('a' + i))
			_, errs[i] = c.Import(ctx, tMeta(label+".csv"), tBody(label), run(label))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	if p := peak.Load(); p != 1 {
		t.Fatalf("%d imports ran at once", p)
	}
	imports, events, _ := counts(t, c)
	if imports != workers || events != workers*50 {
		t.Fatalf("%d imports, %d events", imports, events)
	}
	recs, _ := c.Imports(ctx)
	for i, r := range recs {
		if r.ID != int64(i+1) {
			t.Fatalf("import IDs not sequential: %+v", recs)
		}
	}
	_ = c.Close()

	// The second store opens the same case afterwards and sees all of it, and its
	// own imports are serialised against the folder lock.
	c2 := mustOpen(t, s2, info.ID)
	if got := c2.Info().Imports; got != workers {
		t.Fatalf("second store sees %d imports", got)
	}
	mustImport(t, c2, "later", 2)
}

func TestTwoStoresImportAtOnce(t *testing.T) {
	ctx := context.Background()
	s1, root := newTestStore(t)
	info := mustCreate(t, s1, "Case")
	// Both stores hold the case open and import concurrently; the folder lock and
	// SQLite's busy timeout serialise them. Run funcs are short, under the lock wait.
	s2 := newStoreAt(t, root, s1.opt.Protector)
	c1 := mustOpen(t, s1, info.ID)
	c2 := mustOpen(t, s2, info.ID)
	var wg sync.WaitGroup
	var fails atomic.Int32
	for i, c := range []*Case{c1, c2, c1, c2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			label := string(rune('p' + i))
			run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
				time.Sleep(40 * time.Millisecond)
				return tRun(tEvents(c, label, 20))(r, emit)
			}
			if _, err := c.Import(ctx, tMeta(label+".csv"), tBody(label), run); err != nil {
				t.Errorf("import %s: %v", label, err)
				fails.Add(1)
			}
		}()
	}
	wg.Wait()
	if fails.Load() != 0 {
		return
	}
	_ = c1.Close()
	_ = c2.Close()
	c3 := mustOpen(t, s1, info.ID)
	if imports, events, _ := counts(t, c3); imports != 4 || events != 80 {
		t.Fatalf("%d imports, %d events", imports, events)
	}
}

func TestDuplicateHashReturnsEarlierRecord(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
	first := mustImport(t, c, "dup", 3)
	res, err := c.Import(ctx, tMeta("again.csv"), tBody("dup"), tRun(tEvents(c, "zz", 9)))
	if !errors.Is(err, casestore.ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	if res.Record.Status != "duplicate" || res.Record.ID != first.Record.ID || res.Record.SHA256 != first.Record.SHA256 {
		t.Fatalf("duplicate result = %+v, first = %+v", res.Record, first.Record)
	}
	if imports, events, _ := counts(t, c); imports != 1 || events != 3 {
		t.Fatalf("%d imports, %d events", imports, events)
	}
	// The connection is still usable after the duplicate path.
	mustImport(t, c, "other", 2)
}

func TestAckGatingZeroRefusedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
	_, err := c.db.ExecContext(ctx, `INSERT INTO imports(importer, file_name, sha256, imported_utc, status, ack_gating, ack_text)
		VALUES('x','x','h',0,'ok',0,'text')`)
	if err == nil {
		t.Fatal("a row with ack_gating=0 was accepted")
	}
	// And through the API: no row, importer never called.
	called := false
	run := func(io.Reader, func(evidence.Event) error) (importer.Report, error) {
		called = true
		return importer.Report{}, nil
	}
	m := tMeta("a.csv")
	m.AckGating = false
	if _, err := c.Import(ctx, m, tBody("a"), run); !errors.Is(err, casestore.ErrAckRequired) || called {
		t.Fatalf("Import with ack_gating=false: err=%v called=%v", err, called)
	}
	m.AckGating, m.AckText = true, "   "
	if _, err := c.Import(ctx, m, tBody("a"), run); !errors.Is(err, casestore.ErrAckRequired) || called {
		t.Fatalf("Import with a blank acknowledgement: err=%v called=%v", err, called)
	}
}

func TestQuarantineIsARowAndKeepsNoBytes(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	dir := filepath.Join(s.root, string(info.ID))
	c := mustOpen(t, s, info.ID)
	body := []byte("header,sql_text\n" + sentinel + "\n")
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		for _, e := range tEvents(c, "q", 5) {
			_ = emit(e)
		}
		return importer.Report{Rows: 1}, &importer.QuarantineError{Reason: importer.ReasonUnscrubbedColumn, Column: "sql_text"}
	}
	res, err := c.Import(ctx, tMeta("q.csv"), bytes.NewReader(body), run)
	if !errors.Is(err, importer.ErrQuarantine) {
		t.Fatalf("err = %v, want ErrQuarantine", err)
	}
	if res.Record.Status != "quarantined" || res.Record.Reason != importer.ReasonUnscrubbedColumn || res.Record.SHA256 == "" {
		t.Fatalf("result = %+v", res.Record)
	}
	imports, events, quarantined := counts(t, c)
	if imports != 0 || events != 0 || quarantined != 1 {
		t.Fatalf("%d imports, %d events, %d quarantined", imports, events, quarantined)
	}
	qs, err := c.Quarantines(ctx)
	if err != nil || len(qs) != 1 || qs[0].Reason != "unscrubbed-column" || qs[0].Column != "sql_text" || qs[0].FileName != "q.csv" {
		t.Fatalf("quarantine rows = %+v, %v", qs, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if hasAny(readFile(t, filepath.Join(dir, e.Name())), []byte(sentinel)) {
			t.Fatalf("%s holds raw file content", e.Name())
		}
	}
	if len(entries) > 6 {
		t.Fatalf("unexpected files in the case folder: %d", len(entries))
	}
}

func TestImporterLimitRollsBack(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		for _, e := range tEvents(c, "l", 20) {
			_ = emit(e)
		}
		return importer.Report{}, &importer.LimitError{Limit: "rows", Max: 10, At: 11}
	}
	_, err := c.Import(ctx, tMeta("l.csv"), tBody("l"), run)
	var le *importer.LimitError
	if !errors.As(err, &le) || !errors.Is(err, importer.ErrLimit) {
		t.Fatalf("err = %v", err)
	}
	if imports, events, q := counts(t, c); imports != 0 || events != 0 || q != 0 {
		t.Fatalf("%d imports, %d events, %d quarantined", imports, events, q)
	}
}

func TestBadEventsRollBack(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	c := mustOpen(t, s, mustCreate(t, s, "Case").ID)
	for name, mutate := range map[string]func([]evidence.Event){
		"no evidence id":        func(e []evidence.Event) { e[3].EvidenceID = "" },
		"zero start":            func(e []evidence.Event) { e[3].Start = time.Time{} },
		"duplicate evidence id": func(e []evidence.Event) { e[4].EvidenceID = e[1].EvidenceID },
	} {
		evs := tEvents(c, "b", 8)
		mutate(evs)
		if _, err := c.Import(ctx, tMeta("b.csv"), tBody("b"), tRun(evs)); err == nil {
			t.Fatalf("%s: import succeeded", name)
		}
		if imports, events, _ := counts(t, c); imports != 0 || events != 0 {
			t.Fatalf("%s: %d imports, %d events left", name, imports, events)
		}
	}
	mustImport(t, c, "b", 8) // the same body imports once the events are right
}

// failingTransport records any attempt to leave the machine.
type failingTransport struct{ calls atomic.Int32 }

func (f *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls.Add(1)
	return nil, errors.New("network access is not allowed in case store tests")
}

func TestNoNetworkCalls(t *testing.T) {
	ft := &failingTransport{}
	old := http.DefaultTransport
	http.DefaultTransport = ft
	t.Cleanup(func() { http.DefaultTransport = old })

	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "n", 10)
	_, _ = c.Events(ctx, casestore.EventQuery{})
	_ = c.Pseudonymize("user", "x")
	_ = s.CloseCase(ctx, info.ID)
	_, _ = s.List(ctx)
	_ = c.Close()
	if err := s.Erase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if n := ft.calls.Load(); n != 0 {
		t.Fatalf("%d network calls", n)
	}
}

func TestLogsCarryNoRowContent(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var lines []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
	}
	s, _ := newTestStore(t, func(o *Options) { o.Logf = logf })
	info := mustCreate(t, s, sentinel+" title")
	c := mustOpen(t, s, info.ID)
	evs := tEvents(c, "g", 5)
	for i := range evs {
		evs[i].Attrs = map[string]string{"counter": sentinel}
		evs[i].Host = sentinel
	}
	res, err := c.Import(ctx, tMeta("g.csv"), bytes.NewReader([]byte(sentinel)), tRun(evs))
	if err != nil {
		t.Fatal(err)
	}
	q := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		return importer.Report{}, &importer.QuarantineError{Reason: importer.ReasonFieldCount, Column: "c1"}
	}
	_, _ = c.Import(ctx, tMeta("g2.csv"), bytes.NewReader([]byte(sentinel+"2")), q)
	_ = s.CloseCase(ctx, info.ID)
	_ = c.Close()
	_ = s.Erase(ctx, info.ID)

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(lines, "\n")
	if containsFold(joined, sentinel) || containsFold(joined, "title") {
		t.Fatalf("a log line carries row or title content:\n%s", joined)
	}
	for _, want := range []string{string(info.ID), "g.csv", res.Record.SHA256, "rows=5", "field-count"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("logs lack %q:\n%s", want, joined)
		}
	}
}
