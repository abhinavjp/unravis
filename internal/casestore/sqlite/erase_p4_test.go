package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// recovererFor builds a Recoverer whose Erasing hook is the store's own Erase,
// the way the program wires it.
func recovererFor(t *testing.T, s *Store) *casestore.Recoverer {
	t.Helper()
	r, err := casestore.NewRecoverer(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register(casestore.MarkErasing, func(ctx context.Context, id casestore.CaseID) error { return s.Erase(ctx, id) }); err != nil {
		t.Fatal(err)
	}
	return r
}

// crashAt runs Erase and lets it "die" (panic) at the named step.
func crashAt(t *testing.T, s *Store, id casestore.CaseID, step string) {
	t.Helper()
	s.opt.eraseStep = func(got string) {
		if got == step {
			panic("process died at " + step)
		}
	}
	func() {
		defer func() { _ = recover() }()
		_ = s.Erase(context.Background(), id)
	}()
	s.opt.eraseStep = nil
}

// INJ-5: a process that dies at any point after the Erasing mark leaves a case
// that start-up recovery finishes, with the OnErase work done exactly once more.
func TestEraseKilledAfterTheMarkIsFinishedByRecovery(t *testing.T) {
	ctx := context.Background()
	for _, step := range []string{"marked", "files", "hook"} {
		t.Run(step, func(t *testing.T) {
			var hookDone int
			hook := func(context.Context, casestore.CaseID) error { hookDone++; return nil }
			s, root := newTestStore(t, func(o *Options) { o.OnErase = hook })
			info := mustCreate(t, s, "Case")
			c := mustOpen(t, s, info.ID)
			mustImport(t, c, "a", 3)
			crashAt(t, s, info.ID, step)

			dir := filepath.Join(root, string(info.ID))
			if _, err := os.Lstat(filepath.Join(dir, eraseMarker)); err != nil {
				t.Fatalf("no Erasing mark left after a kill at %s: %v", step, err)
			}
			// A new process on the same folder.
			s2 := newStoreAt(t, root, s.opt.Protector, func(o *Options) { o.OnErase = hook })
			rows, err := s2.List(ctx)
			if err != nil || len(rows) != 1 || rows[0].Mark != casestore.MarkErasing {
				t.Fatalf("List = %+v, %v; want one row marked Erasing", rows, err)
			}
			mark, err := s2.Mark(ctx, info.ID)
			if err != nil || mark != casestore.MarkErasing {
				t.Fatalf("Mark = %q, %v; want Erasing", mark, err)
			}
			rep, err := recovererFor(t, s2).RecoverAll(ctx)
			if err != nil || rep.Marked != 1 || rep.Recovered != 1 || len(rep.Failed) != 0 {
				t.Fatalf("RecoverAll = %+v, %v", rep, err)
			}
			if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("folder survived recovery: %v", err)
			}
			if hookDone < 1 {
				t.Fatal("OnErase never completed")
			}
			if rows, _ := s2.List(ctx); len(rows) != 0 {
				t.Fatalf("List after recovery = %+v", rows)
			}
		})
	}
}

// INJ-7: the key is locked after the Erasing mark. Nothing in the finish needs the
// key, so unlocking (here: simply the next recovery call, no restart) ends the erase.
func TestEraseOfAKeyedCaseFinishesWithTheKeyLocked(t *testing.T) {
	ctx := context.Background()
	s, root := ckStore(t)
	info := ckCreate(t, s, "Keyed")
	crashAt(t, s, info.ID, "marked")
	s.LockAllCaseKeys()
	dir := filepath.Join(root, string(info.ID))
	rep, err := recovererFor(t, s).RecoverAll(ctx)
	if err != nil || rep.Recovered != 1 {
		t.Fatalf("RecoverAll = %+v, %v", rep, err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder survived: %v", err)
	}
}

// RecoverOne on a case that is already gone is "nothing to do", not a failure.
func TestRecoverOneOfAFinishedEraseIsClean(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Case")
	crashAt(t, s, info.ID, "files")
	r := recovererFor(t, s)
	if mark, err := r.RecoverOne(ctx, info.ID); mark != "" || err != nil {
		t.Fatalf("RecoverOne = %q, %v", mark, err)
	}
	if mark, err := r.RecoverOne(ctx, info.ID); mark != "" || !errors.Is(err, casestore.ErrNotFound) {
		t.Fatalf("second RecoverOne = %q, %v; want ErrNotFound and no mark", mark, err)
	}
}
