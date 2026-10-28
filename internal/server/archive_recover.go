package server

import (
	"context"
	"os"
	"path/filepath"

	"api-audit-explorer/internal/casestore"
)

// Recovery of the archive family (P3, spec v7 sections 7.2 and 7.5).
//
// Archiving: the process stopped while a case file was being made. The .part the
// mark names is removed; a finished file (the process stopped after the rename and
// before the commit) is listed as unresolved and never deleted; the case goes back
// to the state it was in. Freeing: the commit point was passed, so the removal of
// the working copy is finished.
//
// Both hooks are safe to run again, and the Archiving one stands aside while a job
// of this process is really running.

// ArchiveRecovery holds what the hooks need. Build it with NewArchiveRecovery,
// register it on the Recoverer before start-up recovery runs, and pass it to the
// server with WithArchiveRecovery so it can tell a live job from a dead one.
type ArchiveRecovery struct {
	store casestore.Archiver
	root  string
	live  func(casestore.CaseID) bool
}

// NewArchiveRecovery builds the hooks for a store and its case root.
func NewArchiveRecovery(store casestore.Archiver, root string) *ArchiveRecovery {
	return &ArchiveRecovery{store: store, root: root}
}

// SetLive tells the hook how to ask whether an archive job of this process is
// still running for a case. Without it no job counts as live.
func (a *ArchiveRecovery) SetLive(f func(casestore.CaseID) bool) { a.live = f }

// Register installs the Archiving and Freeing hooks.
func (a *ArchiveRecovery) Register(r *casestore.Recoverer) error {
	if err := r.Register(casestore.MarkArchiving, a.archiving); err != nil {
		return err
	}
	return r.Register(casestore.MarkFreeing, a.freeing)
}

// CleanVerify removes the temporary stores of Verify. Run once at start-up. An
// error means they are still there ("cleanup pending" in the health panel).
func (a *ArchiveRecovery) CleanVerify() error { return cleanVerifyDir(a.root) }

func (a *ArchiveRecovery) archiving(ctx context.Context, id casestore.CaseID) error {
	if a.live != nil && a.live(id) {
		return nil // a job of this process is working; its own end settles the mark
	}
	info, err := a.store.Entry(ctx, id)
	if err != nil {
		return err
	}
	if info.Archive != nil && info.Archive.Pending != nil {
		p := info.Archive.Pending
		removePartNamed(p.Folder, p.Name)
		if finalExists(p.Folder, p.Name) {
			if err := a.store.AddUnresolved(ctx, casestore.UnresolvedFile{
				Name: p.Name, Folder: p.Folder, CaseID: id, At: archiveNow().UTC(),
			}); err != nil {
				return err // the mark stays: the file must be listed before the case moves on
			}
		}
	}
	return a.store.AbortArchive(ctx, id, true)
}

func (a *ArchiveRecovery) freeing(ctx context.Context, id casestore.CaseID) error {
	return a.store.FinishFree(ctx, id)
}

// finalExists reports whether a finished file of that name is there.
func finalExists(folder, name string) bool {
	if folder == "" || name == "" || filepath.Base(name) != name {
		return false
	}
	fi, err := os.Lstat(filepath.Join(folder, name))
	return err == nil && fi.Mode().IsRegular()
}

// WithArchiveRecovery gives the server the recovery hooks, so a live archive job
// is not mistaken for a dead one.
func WithArchiveRecovery(a *ArchiveRecovery) Option {
	return func(h *handler) {
		h.archiveRec = a
		a.SetLive(func(id casestore.CaseID) bool {
			return h.caseFiles().jobs.running(jobArchive, id) != nil
		})
	}
}
