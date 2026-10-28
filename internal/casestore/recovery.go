package casestore

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// The marks-and-recovery framework (decisions A7, A15; spec v7 section 9). A move
// that cannot finish in one step (Archiving, Freeing, Erasing) first writes a mark
// into the case, then does its work, then clears the mark. A crash between the two
// leaves the mark, and a hook registered for that mark finishes or undoes the work.
//
// P2 ships the framework and tests it with a fake mark; P3 and P4 register the
// real hooks. Recovery runs at three moments:
//
//   - at start-up, for every marked case (RecoverAll);
//   - at unlock, before the first reply that follows it (RecoverAll again, since
//     a locked store could not be repaired at start-up);
//   - at the start of a call that touches a marked case, under that case's lock
//     (RecoverIfMarked / RecoverOne; the caller holds the lock).
//
// A plain read of an unmarked case runs nothing and writes nothing. A listing
// repairs only the marked rows of the page it returns (RecoverPage).

// RecoveryHook finishes or undoes the interrupted move for one case. It must be
// safe to run twice (a crash during recovery runs it again), and it must clear the
// mark itself when the move is complete (SetMark(id, "")). A hook that returns
// nil without clearing the mark leaves the case marked; the next call tries again.
type RecoveryHook func(ctx context.Context, id CaseID) error

// Recoverer runs the hooks. The zero value is not usable; use NewRecoverer.
type Recoverer struct {
	marks Lifecycle
	list  func(context.Context) ([]CaseInfo, error)

	mu    sync.Mutex
	hooks map[string]RecoveryHook
	locks map[CaseID]*sync.Mutex // one recovery at a time per case
}

// NewRecoverer builds a Recoverer for a store. The store must implement
// Lifecycle (both stores do), or the caller has nothing to recover.
func NewRecoverer(st Store) (*Recoverer, error) {
	l, ok := st.(Lifecycle)
	if !ok {
		return nil, errors.New("casestore: store has no marks")
	}
	return &Recoverer{marks: l, list: st.List, hooks: map[string]RecoveryHook{}, locks: map[CaseID]*sync.Mutex{}}, nil
}

// Register sets the hook for a mark, replacing an earlier one (nil removes it).
// Unknown marks are refused, so a typo cannot hide a case that never recovers.
func (r *Recoverer) Register(mark string, h RecoveryHook) error {
	switch mark {
	case MarkArchiving, MarkFreeing, MarkErasing:
	default:
		return fmt.Errorf("casestore: unknown mark %q", mark)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h == nil {
		delete(r.hooks, mark)
	} else {
		r.hooks[mark] = h
	}
	return nil
}

func (r *Recoverer) hook(mark string) RecoveryHook {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hooks[mark]
}

func (r *Recoverer) lockFor(id CaseID) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.locks[id]
	if m == nil {
		m = new(sync.Mutex)
		r.locks[id] = m
	}
	return m
}

// RecoverOne repairs one case if it carries a mark that has a hook, and returns
// the mark the case has afterwards ("" when none, or when the hook finished).
// An error from the hook is returned with the mark still in place. Concurrent
// calls for one case run the hook once: the second waits, then sees no mark.
func (r *Recoverer) RecoverOne(ctx context.Context, id CaseID) (string, error) {
	m := r.lockFor(id)
	m.Lock()
	defer m.Unlock()
	mark, err := r.marks.Mark(ctx, id)
	if err != nil || mark == "" {
		return "", err
	}
	h := r.hook(mark)
	if h == nil {
		return mark, nil
	}
	if err := h(ctx, id); err != nil {
		return mark, err
	}
	return r.marks.Mark(ctx, id)
}

// RecoverIfMarked is the per-call entry. info is what the caller already holds for
// the case; an unmarked case costs nothing: no store call, no write. A mark that
// is still there afterwards is returned for the caller to answer (M19 or M5 through
// CheckMark, or nil for an archive that is really running).
func (r *Recoverer) RecoverIfMarked(ctx context.Context, info CaseInfo) (string, error) {
	if info.Mark == "" {
		return "", nil
	}
	return r.RecoverOne(ctx, info.ID)
}

// RecoverReport says what a RecoverAll did.
type RecoverReport struct {
	Marked    int      // cases found with a mark
	Recovered int      // cases whose mark is gone afterwards
	Failed    []CaseID // hooks that returned an error
}

// RecoverAll repairs every marked case. One failing case does not stop the rest.
// It returns the list error (for example ErrLocked while the master key is not
// unlocked) so the caller can run it again at unlock.
func (r *Recoverer) RecoverAll(ctx context.Context) (RecoverReport, error) {
	var rep RecoverReport
	all, err := r.list(ctx)
	if err != nil {
		return rep, err
	}
	for _, info := range all {
		if info.Mark == "" {
			continue
		}
		rep.Marked++
		mark, err := r.RecoverOne(ctx, info.ID)
		switch {
		case err != nil:
			rep.Failed = append(rep.Failed, info.ID)
		case mark == "":
			rep.Recovered++
		}
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
	}
	return rep, nil
}

// RecoverPage repairs only the marked rows of one page of a listing and returns
// the page with their marks refreshed. Unmarked rows are returned as they are.
func (r *Recoverer) RecoverPage(ctx context.Context, page []CaseInfo) []CaseInfo {
	out := make([]CaseInfo, len(page))
	copy(out, page)
	for i, info := range out {
		if info.Mark == "" {
			continue
		}
		if mark, err := r.RecoverOne(ctx, info.ID); err == nil || mark != info.Mark {
			out[i].Mark = mark
		}
	}
	return out
}
