package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// RV-SRV-2: shutdown (CancelImports) stops the archive and verify jobs, waits a
// bounded time for them, removes the partial file and puts the case back. None of
// these tests waits with a sleep: each one waits on a channel, with a generous
// limit that only a hang reaches.

const shutdownTestLimit = 30 * time.Second

// callCancelImports runs CancelImports and fails the test if it does not return.
func callCancelImports(t *testing.T, a *archPC) {
	t.Helper()
	returned := make(chan struct{})
	go func() {
		CancelImports(a.h)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(shutdownTestLimit):
		t.Fatal("CancelImports did not return")
	}
}

func jobEnded(t *testing.T, j *fileJob) {
	t.Helper()
	select {
	case <-j.done:
	default:
		t.Fatal("CancelImports returned while the job was still running")
	}
}

// A writer that is slow: it has written part of the file and then waits for its
// context. This is the archive a closing program meets in the middle of a write.
func TestShutdownStopsASlowArchiveAndLeavesNoPartialFile(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("slow", 3)
	dir := a.folder()
	started := make(chan struct{})
	archiveWriter = func(ctx context.Context, part string, _ casestore.CaseFileSource, _ casestore.FileKey, _ func(int64)) (casestore.ArchiveWritten, error) {
		if err := os.WriteFile(part, []byte("half of an archive"), 0o600); err != nil {
			return casestore.ArchiveWritten{}, err
		}
		close(started)
		<-ctx.Done() // a stand-in writer: it does not remove its part (runArchive must)
		return casestore.ArchiveWritten{}, ctx.Err()
	}
	t.Cleanup(func() { archiveWriter = casestore.WriteArchivePart })
	if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	job := a.handler().caseFiles().jobs.get(jobArchive, casestore.CaseID(id))
	// Whatever happens below, the writer must not outlive the test.
	t.Cleanup(func() { job.cancel(); <-job.done })
	select {
	case <-started:
	case <-time.After(shutdownTestLimit):
		t.Fatal("the writer never started")
	}
	if got := partsIn(t, dir); len(got) != 1 {
		t.Fatalf("the test needs a partial file on disk: %v", got)
	}

	callCancelImports(t, a)

	jobEnded(t, job)
	d := job.dto()
	if d.State != fileJobStopped || d.Reason != "stopped" {
		t.Fatalf("job = %+v (a shutdown is not a key lock)", d)
	}
	if got := partsIn(t, dir); len(got) != 0 {
		t.Fatalf("a partial file was left: %v", got)
	}
	if got := caseFilesIn(t, dir); len(got) != 0 {
		t.Fatalf("a case file was presented: %v", got)
	}
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" || a.eventCount(id) != 3 {
		t.Fatalf("the case is not as it was: state %s mark %q", a.state(id), a.entry(id).Mark)
	}
	if a.handler().caseFiles().busy.Load() {
		t.Fatal("the busy lock was not released")
	}
	if o := a.orphans(); len(o.Files) != 0 {
		t.Fatalf("a left-over file was listed: %+v", o)
	}
}

// The real writer has finished its .part file and the job is about to read it
// back: the stop removes the file, and nothing is presented as an archive.
func TestShutdownRemovesAFinishedPartThatWasNotReadBack(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("part", 3)
	dir := a.folder()
	p := pauseAt(t, "part-written")
	if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	if got := partsIn(t, dir); len(got) != 1 {
		t.Fatalf("the real writer left no part: %v", got)
	}
	job := a.handler().caseFiles().jobs.get(jobArchive, casestore.CaseID(id))
	// The first phase of shutdown cancels without waiting, so the pause can be
	// released only after the job has been told to stop.
	a.handler().caseFiles().jobs.beginShutdown()
	p.release()
	callCancelImports(t, a)

	jobEnded(t, job)
	if d := job.dto(); d.State != fileJobStopped || d.Reason != "stopped" {
		t.Fatalf("job = %+v", d)
	}
	if len(partsIn(t, dir)) != 0 || len(caseFilesIn(t, dir)) != 0 {
		t.Fatalf("folder = %v", listNames(t, dir, ""))
	}
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" || a.eventCount(id) != 3 {
		t.Fatal("the case is not as it was")
	}
}

// The file is finished and renamed and the job waits for the case lock: shutdown
// wins the commit. The finished file is kept and listed as a left-over file, the
// case is not marked as archived, and the stop is not blamed on the key.
func TestShutdownBeforeTheCommitKeepsTheFinishedFileListedAndTheCaseOpen(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("late", 3)
	dir := a.folder()
	p := pauseAt(t, "commit-lock")
	if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	job := a.handler().caseFiles().jobs.get(jobArchive, casestore.CaseID(id))
	a.handler().caseFiles().jobs.beginShutdown()
	p.release()
	callCancelImports(t, a)

	jobEnded(t, job)
	if d := job.dto(); d.State != fileJobStopped || d.Reason != "stopped" {
		t.Fatalf("job = %+v", d)
	}
	if got := caseFilesIn(t, dir); len(got) != 1 {
		t.Fatalf("the finished file must stay: %v", got)
	}
	if len(partsIn(t, dir)) != 0 {
		t.Fatalf("a part was left: %v", partsIn(t, dir))
	}
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" {
		t.Fatalf("the case was changed: state %s mark %q", a.state(id), a.entry(id).Mark)
	}
	if o := a.orphans(); len(o.Files) != 1 {
		t.Fatalf("the finished file is not listed as a left-over: %+v", o)
	}
}

// A verify job is stopped between its read-back and its test restore: it ends as
// stopped, the temporary store is gone, the check does not count as passed, and
// the case file is as it was.
func TestShutdownStopsARunningVerifyAndLeavesNothingBehind(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id, dir, name := a.archivedCase("verify", 3)
	before := a.fileSum(filepath.Join(dir, name))
	p := pauseAt(t, "verify-hashed")
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/verify", `{}`, "archived"); res.Code != http.StatusAccepted {
		t.Fatalf("verify = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	job := a.handler().caseFiles().jobs.get(jobVerify, casestore.CaseID(id))
	a.handler().caseFiles().jobs.beginShutdown()
	p.release()
	callCancelImports(t, a)

	jobEnded(t, job)
	if d := job.dto(); d.State != fileJobStopped || d.Reason != "stopped" {
		t.Fatalf("job = %+v", d)
	}
	if verifyLeftovers(a.root) {
		t.Fatal("a temporary store was left")
	}
	if a.verifyState(id).Verified {
		t.Fatal("a stopped check counted as passed")
	}
	if a.state(id) != casestore.StateArchived || a.entry(id).Mark != "" || !a.dbExists(id) {
		t.Fatal("the case changed")
	}
	if a.fileSum(filepath.Join(dir, name)) != before {
		t.Fatal("the case file changed")
	}
	if a.handler().caseFiles().busy.Load() {
		t.Fatal("the busy lock was not released")
	}
}

// Shutdown never hangs on a job that does not end, and a job that is registered
// after shutdown began is stopped at once.
func TestFileJobsShutdownIsBoundedAndStopsLateJobs(t *testing.T) {
	var r fileJobs
	ctx, cancel := context.WithCancel(context.Background())
	stuck := newFileJob(jobArchive, casestore.CaseID("c1"), cancel) // its done never closes
	r.put(stuck)
	t.Cleanup(cancel)

	returned := make(chan struct{})
	go func() {
		r.shutdown(10 * time.Millisecond)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(shutdownTestLimit):
		t.Fatal("shutdown waited for a job that never ends")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("the stuck job was not cancelled")
	}
	if stuck.takeCommit() {
		t.Fatal("a job stopped by shutdown may still commit")
	}

	lctx, lcancel := context.WithCancel(context.Background())
	late := newFileJob(jobVerify, casestore.CaseID("c2"), lcancel)
	r.put(late)
	select {
	case <-lctx.Done():
	default:
		t.Fatal("a job registered after shutdown began was not stopped")
	}
}
