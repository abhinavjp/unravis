package sqlite

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// copyTree copies a case folder as a crash would leave it: whatever bytes are on
// disk right now, committed or not.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		if strings.HasSuffix(path, "-shm") {
			return nil // the shared-memory index is rebuilt from the log on the next open
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// C17: an import is one transaction. A run that fails after emitting events keeps
// no import row, no event, and no manifest entry.
func TestImportFailureMidRunKeepsNothing(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "c17")
	c := mustOpen(t, s, info.ID)
	events := tEvents(c, "half", 20)
	boom := errors.New("importer broke")
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: testImporter}
		_, _ = io.Copy(io.Discard, r)
		for i, e := range events {
			if i == 12 {
				return rep, boom
			}
			if err := emit(e); err != nil {
				return rep, err
			}
		}
		return rep, nil
	}
	if _, err := c.Import(context.Background(), tMeta("half.csv"), tBody("half"), run); err == nil {
		t.Fatal("a broken run was accepted")
	}
	if imports, evs, _ := counts(t, c); imports != 0 || evs != 0 {
		t.Fatalf("a failed import left %d imports and %d events", imports, evs)
	}
	if list, _ := c.Imports(context.Background()); len(list) != 0 {
		t.Fatalf("Imports lists %d entries after a failed import", len(list))
	}
	// The same file can be tried again at once.
	mustImport(t, c, "half", 20)
	if imports, evs, _ := counts(t, c); imports != 1 || evs != 20 {
		t.Fatalf("after the retry: %d imports, %d events", imports, evs)
	}
}

// C17: the process is killed in the middle of an import. A copy of the folder, taken
// while the transaction is open and uncommitted, opens with nothing partial.
func TestKillMidImportLeavesNothingPartial(t *testing.T) {
	prot := storetest.RandomProtector()
	root := filepath.Join(t.TempDir(), "cases")
	s := newStoreAt(t, root, prot)
	t.Cleanup(func() { _ = s.Close() })
	info := mustCreate(t, s, "killed")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "whole", 4) // one finished import stays through the kill

	events := tEvents(c, "second", 200)
	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	defer free()
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: testImporter}
		_, _ = io.Copy(io.Discard, r)
		for i, e := range events {
			if i == 150 {
				close(reached)
				<-release
			}
			if err := emit(e); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.Import(context.Background(), tMeta("second.csv"), tBody("second"), run)
		done <- err
	}()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the import never reached its midpoint")
	}
	// The "kill": what is on disk right now is all a restart gets.
	dir, err := s.caseDir(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	crashed := filepath.Join(t.TempDir(), "cases", filepath.Base(dir))
	if err := os.MkdirAll(crashed, 0o700); err != nil {
		t.Fatal(err)
	}
	copyTree(t, dir, crashed)
	free()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	s2 := newStoreAt(t, filepath.Dir(crashed), prot)
	t.Cleanup(func() { _ = s2.Close() })
	c2 := mustOpen(t, s2, info.ID)
	imports, evs, _ := counts(t, c2)
	if imports != 1 || evs != 4 {
		t.Fatalf("restart after a kill mid-import: %d imports, %d events; want the one finished import only (1, 4)", imports, evs)
	}
	list, err := c2.Imports(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("Imports after the kill: %v %v", list, err)
	}
	// And the file can be imported again, whole.
	if _, err := c2.Import(context.Background(), tMeta("second.csv"), tBody("second"), tRun(tEvents(c2, "second", 200))); err != nil {
		t.Fatalf("import after the kill: %v", err)
	}
	if imports, evs, _ := counts(t, c2); imports != 2 || evs != 204 {
		t.Fatalf("after the second try: %d imports, %d events", imports, evs)
	}
}

// A cold Open and a CloseCase that race must both finish. Open holds the store's
// mutex while it waits for the folder lock; CloseCase used to take the two the
// other way round and waited out the folder lock's five seconds.
func TestColdOpenRacingCloseCaseDoesNotDeadlock(t *testing.T) {
	prot := storetest.RandomProtector()
	root := filepath.Join(t.TempDir(), "cases")
	first := newStoreAt(t, root, prot)
	info := mustCreate(t, first, "race")
	_ = first.Close()
	for i := 0; i < 25; i++ {
		s := newStoreAt(t, root, prot)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			c, err := s.Open(context.Background(), info.ID)
			if err == nil {
				_ = c.Close()
			}
			errs <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				errs <- s.CloseCase(context.Background(), info.ID)
			} else {
				errs <- s.ReopenCase(context.Background(), info.ID)
			}
		}()
		began := time.Now()
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !errors.Is(err, casestore.ErrCaseChanged) {
				t.Fatalf("round %d: %v", i, err)
			}
		}
		if took := time.Since(began); took > 3*time.Second {
			t.Fatalf("round %d took %s: a lock-order wait", i, took)
		}
		_ = s.Close()
	}
}
