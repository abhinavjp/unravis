package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"api-audit-explorer/internal/casestore"
)

// The archive job: write the case file to <name>.part, read it back, rename it
// without replacing anything, and commit under the case lock (spec 7.2). Every
// step before the rename undoes itself completely; a finished file is never
// deleted: when the commit does not happen it is listed as a left-over file.

// Test seams. All are nil or the real thing in production.
var (
	// archiveInject is told about each point of the job. A true answer is a kill
	// at that point: the job returns at once and leaves everything as it is.
	// Points: "started", "part-written", "read-back", "renamed", "commit-lock",
	// "commit-decided".
	archiveInject func(point, part, final string) (kill bool)
	// archiveWriter writes the .part file.
	archiveWriter = casestore.WriteArchivePart
	// archiveCountFn counts what the record will state.
	archiveCountFn = casestore.CountCaseDB
	// archiveFreeFn reports the free bytes of the volume holding a folder.
	archiveFreeFn = archiveFreeBytes
	// archiveNow is the clock of file names and records.
	archiveNow = time.Now
)

func injectAt(point, part, final string) bool {
	f := archiveInject
	return f != nil && f(point, part, final)
}

// isDiskFull recognises "the disk is full" from a failed write.
func isDiskFull(err error) bool {
	var en syscall.Errno
	if !errors.As(err, &en) {
		return false
	}
	if en == syscall.ENOSPC {
		return true
	}
	return runtime.GOOS == "windows" && (en == 112 || en == 39) // ERROR_DISK_FULL, ERROR_HANDLE_DISK_FULL
}

// archiveRun is everything one archive job needs, gathered before it starts.
type archiveRun struct {
	ar       casestore.Archiver
	id       casestore.CaseID
	src      casestore.CaseFileSource
	keys     casestore.Keys
	fk       casestore.FileKey
	kind     string // KeyKindMaster, KeyKindCase or KeyKindTyped
	check    string // key check value
	folder   string
	name     string
	part     string
	final    string
	counts   casestore.Counts
	sampler  samplerParams
	hadSamp  bool
	hasCount bool
}

// runArchive is the job goroutine. It owns the busy lock and the keys until it ends.
func (h *handler) runArchive(ctx context.Context, job *fileJob, run archiveRun) {
	st := h.caseFiles()
	defer close(job.done)
	defer st.busy.Store(false)
	defer run.keys.Zero()
	defer clear(run.fk.Key)

	if injectAt("started", run.part, run.final) {
		return
	}

	// finishStopped puts the case back as it was and ends the job.
	abort := func(stopped bool) {
		rel, err := h.gates.acquire(context.Background(), run.id)
		if err != nil {
			return
		}
		defer rel()
		h.abortArchive(run, stopped)
	}
	fail := func(reason, text string, stopped bool) {
		abort(stopped)
		job.finish(fileJobFailed, reason, text)
	}
	// stopWhy ends the job after a Cancel or a key lock.
	stopWhy := func() {
		abort(true)
		cancelled, locked := job.wasCancelled()
		reason := "stopped"
		if locked && !cancelled {
			reason = "key-locked"
		}
		job.finish(fileJobStopped, reason, archiveStoppedText)
	}
	removePart := func() { _ = os.Remove(run.part) }

	// What the record will state: counted from the database the file is made from.
	if c, err := archiveCountFn(ctx, run.src.DBPath, run.keys.DB); err == nil {
		run.counts, run.hasCount = c, true
	} else if ctx.Err() != nil {
		stopWhy()
		return
	} else {
		fail("check-failed", archiveCheckText, false)
		return
	}

	job.setPhase("writing")
	w, err := archiveWriter(ctx, run.part, run.src, run.fk, job.step)
	if err != nil {
		removePart() // the writer removes it too; a stand-in writer may not
		switch {
		case ctx.Err() != nil:
			stopWhy()
		case isDiskFull(err):
			fail("no-space", archiveNoSpaceText, false)
		default:
			fail("check-failed", archiveCheckText, false)
		}
		return
	}
	if injectAt("part-written", run.part, run.final) {
		return
	}

	job.setPhase("checking")
	size, sum, err := casestore.HashFile(ctx, run.part, job.step)
	if err != nil {
		removePart()
		if ctx.Err() != nil {
			stopWhy()
		} else {
			fail("check-failed", archiveCheckText, false)
		}
		return
	}
	if size != w.Bytes || sum != w.SHA256 {
		removePart()
		fail("check-failed", archiveCheckText, false)
		return
	}
	if injectAt("read-back", run.part, run.final) {
		return
	}
	// The last moment Cancel is free: after the rename a finished file exists.
	if ctx.Err() != nil {
		removePart()
		stopWhy()
		return
	}

	job.setPhase("finishing")
	if err := moveNoReplace(run.part, run.final); err != nil {
		removePart()
		fail("check-failed", archiveCheckText, false)
		return
	}
	if injectAt("renamed", run.part, run.final) {
		return
	}

	file := casestore.ArchiveFile{
		Name: run.name, Folder: run.folder, Bytes: w.Bytes, SHA256: w.SHA256,
		KeyKind: run.kind, KeyCheck: run.check, Made: archiveNow().UTC(), Counts: run.counts,
	}
	rel, err := h.gates.acquire(context.Background(), run.id)
	if err != nil {
		h.leftOver(run)
		job.finish(fileJobFailed, "check-failed", archiveCheckText)
		return
	}
	defer rel()
	if injectAt("commit-lock", run.part, run.final) {
		return
	}
	if !job.takeCommit() {
		// Cancel (or the key lock) got here first. The finished file stays and is
		// listed before the case goes back as it was.
		h.leftOver(run)
		h.abortArchive(run, true)
		cancelled, locked := job.wasCancelled()
		reason := "stopped"
		if locked && !cancelled {
			reason = "key-locked"
		}
		job.finish(fileJobStopped, reason, archiveStoppedText)
		return
	}
	if injectAt("commit-decided", run.part, run.final) {
		return
	}
	cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run.ar.CommitArchive(cctx, run.id, file); err != nil {
		// The file is complete but the case was not marked: list the file and leave
		// the mark to the recovery hook, which puts the case back.
		h.leftOver(run)
		reason, text := "check-failed", archiveCheckText
		if errors.Is(err, casestore.ErrLocked) || errors.Is(err, casestore.ErrCaseLocked) {
			reason, text = "key-locked", archiveStoppedText
		}
		job.finish(fileJobFailed, reason, text)
		return
	}
	job.mu.Lock()
	job.bytes = w.Bytes
	job.mu.Unlock()
	job.finish(fileJobDone, "", "")
}

// abortArchive returns the case to the state it was in and starts the sampler
// again when it was running. The caller holds the case gate. When the store
// cannot be written (the key is locked) the mark stays and recovery does it.
func (h *handler) abortArchive(run archiveRun, stopped bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run.ar.AbortArchive(ctx, run.id, stopped); err != nil {
		return
	}
	if run.hadSamp {
		h.restartSampler(run.id, run.sampler)
	}
}

// leftOver lists a finished case file that no archive record claims.
func (h *handler) leftOver(run archiveRun) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = run.ar.AddUnresolved(ctx, casestore.UnresolvedFile{
		Name: run.name, Folder: run.folder, CaseID: run.id, At: archiveNow().UTC(),
	})
}

// removePartNamed removes the .part of a name the Archiving mark names (A16: no
// other .part file is ever touched). A missing file is not an error.
func removePartNamed(folder, name string) {
	if folder == "" || name == "" || filepath.Base(name) != name {
		return
	}
	_ = os.Remove(filepath.Join(folder, name+".part"))
}
