package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// A1-A6, A12, A14 to A17 and the injection points INJ-1 to INJ-4, INJ-7, INJ-9 and
// INJ-10 of the P3 proof. The scenarios use the real routes, the real SQLite
// store and real case files; only the points named in archive_run.go are faked.

const archivedBelief = "archived"

func TestA_HappyPathArchiveVerifyFreeRestore(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("Checkout slowdown", 5)
	if res := a.req(http.MethodPut, "/api/cases/"+id+"/workspace", `{"story":"SENTINEL-story-p3"}`, ""); res.Code != http.StatusOK {
		t.Fatalf("workspace = %d %s", res.Code, res.Body)
	}
	if res := a.req(http.MethodPost, "/api/cases/"+id+"/ledger", `{"text":"SENTINEL-theory-p3"}`, ""); res.Code != http.StatusCreated {
		t.Fatalf("theory = %d %s", res.Code, res.Body)
	}
	dir := a.folder()

	// Archive an Open case without closing it first.
	j := a.archiveDone(id, dir, "open")
	if !strings.HasPrefix(j.Name, "checkout-slowdown-"+shortCode(casestore.CaseID(id))) || !strings.HasSuffix(j.Name, casestore.CaseFileExt) {
		t.Fatalf("file name = %q", j.Name)
	}
	if got := caseFilesIn(t, dir); len(got) != 1 || got[0] != j.Name || len(partsIn(t, dir)) != 0 {
		t.Fatalf("folder = %v parts %v", got, partsIn(t, dir))
	}
	info := a.entry(id)
	if casestore.StateOf(info) != casestore.StateArchived || info.Archive == nil || info.Archive.Current() == nil || info.Archive.Freed() {
		t.Fatalf("info = %+v archive %+v", info, info.Archive)
	}
	cur := info.Archive.Current()
	if cur.Counts.Events != 5 || cur.Counts.Imports != 1 || cur.KeyKind != casestore.KeyKindMaster || len(cur.KeyEnding()) != 4 {
		t.Fatalf("current file = %+v", cur)
	}
	// The list: archived cases leave the main list and have their own total (A11/A12 of the list).
	page := a.listAll()
	if _, ok := page.find(id); ok || page.ArchivedTotal != 1 {
		t.Fatalf("main list = %+v", page)
	}
	res := a.req(http.MethodGet, "/api/cases?archived=1", "", "")
	var arch casesPage
	decodeBody(t, res, &arch)
	if c, ok := arch.find(id); !ok || c.State != "archived" || c.Archive == nil || c.Archive.Current == nil || c.Archive.Current.KeyEnding == "" {
		t.Fatalf("archived list = %s", res.Body)
	}

	// Verify, then Free with the confirmation, then restore as new (twice).
	a.verifyDone(id)
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/free", `{"confirm":"wrong"}`, archivedBelief), http.StatusBadRequest, "confirm-mismatch", "")
	if res := a.freeReq(id); res.Code != http.StatusOK {
		t.Fatalf("free = %d %s", res.Code, res.Body)
	}
	if _, err := os.Stat(filepath.Join(a.root, id, "case.db")); !os.IsNotExist(err) {
		t.Fatalf("the working database is still there: %v", err)
	}
	if !a.entry(id).Archive.Freed() {
		t.Fatal("not freed")
	}
	if got := caseFilesIn(t, dir); len(got) != 1 {
		t.Fatalf("the case file was touched by Free: %v", got)
	}
	archReason(t, a.req(http.MethodPost, "/api/cases/"+id+"/restore-in-place", `{}`, archivedBelief), http.StatusConflict, "case-changed", "")

	var newIDs []string
	for i := 0; i < 2; i++ {
		res := a.req(http.MethodPost, "/api/cases/"+id+"/restore-as-new", `{}`, archivedBelief)
		if res.Code != http.StatusCreated {
			t.Fatalf("restore as new %d = %d %s", i, res.Code, res.Body)
		}
		var dto caseDTO
		decodeBody(t, res, &dto)
		newIDs = append(newIDs, dto.ID)
		if dto.ID == id || dto.State != "closed" || dto.Closed == nil || dto.Closed.IsZero() {
			t.Fatalf("restored = %+v", dto)
		}
		if n := a.eventCount(dto.ID); n != 5 {
			t.Fatalf("restored events = %d", n)
		}
	}
	if newIDs[0] == newIDs[1] {
		t.Fatal("two restores made one case")
	}
	// Theories and story came with it (A11).
	view := a.req(http.MethodGet, "/api/cases/"+newIDs[0]+"/workspace", "", "")
	if !strings.Contains(view.Body.String(), "SENTINEL-story-p3") {
		t.Fatalf("story missing: %s", view.Body)
	}
	if lg := a.req(http.MethodGet, "/api/cases/"+newIDs[0]+"/ledger", "", ""); !strings.Contains(lg.Body.String(), "SENTINEL-theory-p3") {
		t.Fatalf("theory missing: %s", lg.Body)
	}
	// The history of the new case and the restore lines of the original.
	nv := a.req(http.MethodGet, "/api/cases/"+newIDs[0], "", "")
	if !strings.Contains(nv.Body.String(), "restored-from-file") || !strings.Contains(nv.Body.String(), "restored-from-case") {
		t.Fatalf("history = %s", nv.Body)
	}
	if got := len(a.entry(id).Archive.Restores); got != 2 {
		t.Fatalf("restore lines = %d", got)
	}
	// A15: the restored case can be archived again; it has its own file.
	j2 := a.archiveDone(newIDs[0], dir, "closed")
	if j2.Name == j.Name || len(caseFilesIn(t, dir)) != 2 {
		t.Fatalf("second archive %q, files %v", j2.Name, caseFilesIn(t, dir))
	}
}

func TestA12_ZeroImportsCanBeArchivedAndRestored(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	res := a.do(http.MethodPost, "/api/cases", `{"title":"Empty case"}`)
	var dto caseDTO
	decodeBody(t, res, &dto)
	dir := a.folder()
	a.archiveDone(dto.ID, dir, "open")
	if c := a.entry(dto.ID).Archive.Current(); c == nil || c.Counts.Imports != 0 || c.Counts.Events != 0 {
		t.Fatalf("counts = %+v", c)
	}
	a.verifyDone(dto.ID)
	if res := a.req(http.MethodPost, "/api/cases/"+dto.ID+"/restore-as-new", `{}`, archivedBelief); res.Code != http.StatusCreated {
		t.Fatalf("restore = %d %s", res.Code, res.Body)
	}
}

func TestA1_FolderRulesGiveM8AndChangeNothing(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("rules", 2)
	good := a.folder()
	file := filepath.Join(good, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A folder that exists, is long, but leaves too little room for the file name.
	long := good
	for len(long) < 215 {
		long = filepath.Join(long, strings.Repeat("d", 20))
	}
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Skipf("cannot make a long folder here: %v", err)
	}
	// A folder of exactly 241 characters.
	exact := good
	for len(exact) < 241-21 {
		exact = filepath.Join(exact, strings.Repeat("e", 20))
	}
	exact = filepath.Join(exact, strings.Repeat("f", 241-len(exact)-1))
	if len(exact) != 241 {
		t.Fatalf("test path length = %d", len(exact))
	}
	_ = os.MkdirAll(exact, 0o700)

	cases := map[string]string{
		"empty":            "",
		"relative":         "relative\\folder",
		"dot dot":          filepath.Join(good, "..", "elsewhere"),
		"dot dot inside":   good + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(good),
		"UNC share":        `\\server\share\folder`,
		"UNC long form":    `\\?\UNC\server\share\folder`,
		"device path":      `\\.\C:\folder`,
		"not a folder":     file,
		"missing":          filepath.Join(good, "nope"),
		"data folder":      a.root,
		"inside data":      filepath.Join(a.root, id),
		"too long":         long,
		"241 characters":   exact,
		"reserved name":    filepath.Join(good, "CON"),
		"stream":           good + ":stream",
		"trailing space":   good + " ",
		"wildcard":         filepath.Join(good, "*"),
		"url":              "file:///C:/folder",
		"other drive form": "C:folder",
	}
	for name, p := range cases {
		res := a.archiveReq(id, p, "open")
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d %s", name, res.Code, res.Body)
			continue
		}
		archReason(t, res, http.StatusBadRequest, "bad-folder", archiveFolderText)
	}
	if st := a.state(id); st != casestore.StateOpen || a.entry(id).Mark != "" {
		t.Fatalf("state = %s mark %q after refused folders", st, a.entry(id).Mark)
	}
	if got := listNames(t, good, ""); len(got) != 3 { // a-file, the long folder's first part, the 241 one
		t.Logf("folder holds %v", got)
	}
	// The answer holds no path (M8 is a fixed sentence).
	res := a.archiveReq(id, `\\server\share\secret-folder`, "open")
	if strings.Contains(res.Body.String(), "secret-folder") {
		t.Fatalf("the answer echoes the path: %s", res.Body)
	}
}

func TestA2_DiskFullRemovesThePartAndGivesM9(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("disk", 3)
	dir := a.folder()

	// INJ-1: the disk fills while the file is written.
	archiveWriter = func(_ context.Context, part string, _ casestore.CaseFileSource, _ casestore.FileKey, _ func(int64)) (casestore.ArchiveWritten, error) {
		_ = os.WriteFile(part, []byte("partial"), 0o600)
		return casestore.ArchiveWritten{}, fmt.Errorf("write: %w", syscall.ENOSPC)
	}
	t.Cleanup(func() { archiveWriter = casestore.WriteArchivePart })
	if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	j := a.wait(id, "archive")
	if j.State != fileJobFailed || j.Reason != "no-space" || j.Error != archiveNoSpaceText {
		t.Fatalf("job = %+v", j)
	}
	if len(partsIn(t, dir)) != 0 || len(caseFilesIn(t, dir)) != 0 {
		t.Fatalf("folder = %v", listNames(t, dir, ""))
	}
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" || a.eventCount(id) != 3 {
		t.Fatal("the case is not as it was")
	}
	// The retry works once there is room, and a new file name is used.
	archiveWriter = casestore.WriteArchivePart
	a.archiveDone(id, dir, "open")

	// The free-space check (1.2 times the case) refuses before anything starts.
	b := a.seed("disk 2", 3)
	archiveFreeFn = func(string) (uint64, error) { return 10, nil }
	t.Cleanup(func() { archiveFreeFn = archiveFreeBytes })
	archReason(t, a.archiveReq(b, dir, "open"), http.StatusInsufficientStorage, "no-space", archiveNoSpaceText)
	if a.state(b) != casestore.StateOpen || a.entry(b).Mark != "" {
		t.Fatal("a refused archive left a mark")
	}
}

func TestA3_ChangedByteGivesM10AndARetryUsesANewName(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("flip", 3)
	dir := a.folder()
	// INJ-2: one byte of the finished .part changes before the read-back.
	archiveWriter = func(ctx context.Context, part string, src casestore.CaseFileSource, fk casestore.FileKey, step func(int64)) (casestore.ArchiveWritten, error) {
		w, err := casestore.WriteArchivePart(ctx, part, src, fk, step)
		if err == nil {
			b, rerr := os.ReadFile(part)
			if rerr == nil && len(b) > 100 {
				b[len(b)/2] ^= 0xff
				_ = os.WriteFile(part, b, 0o600)
			}
		}
		return w, err
	}
	t.Cleanup(func() { archiveWriter = casestore.WriteArchivePart })
	first := a.archiveReq(id, dir, "open")
	var started fileJobDTO
	decodeBody(t, first, &started)
	j := a.wait(id, "archive")
	if j.State != fileJobFailed || j.Reason != "check-failed" || j.Error != archiveCheckText {
		t.Fatalf("job = %+v", j)
	}
	if len(partsIn(t, dir)) != 0 || len(caseFilesIn(t, dir)) != 0 {
		t.Fatalf("folder = %v", listNames(t, dir, ""))
	}
	if a.state(id) != casestore.StateOpen {
		t.Fatalf("state = %s", a.state(id))
	}
	archiveWriter = casestore.WriteArchivePart
	retry := a.archiveDone(id, dir, "open")
	if retry.Name == started.Name {
		t.Fatalf("the retry reused the name %q", retry.Name)
	}
}

func TestA4_LockingTheKeyStopsTheJobAndRemovesThePart(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("lock", 3)
	dir := a.folder()
	p := pauseAt(t, "part-written") // INJ-7
	if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	if res := a.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	p.release()
	a.handler().caseFiles().jobs.waitAll()
	if len(partsIn(t, dir)) != 0 || len(caseFilesIn(t, dir)) != 0 {
		t.Fatalf("folder = %v", listNames(t, dir, ""))
	}
	// The routes answer 423 while locked.
	for _, r := range []struct{ m, p, b string }{
		{http.MethodPost, "/api/cases/" + id + "/archive", `{"path":` + q(dir) + `}`},
		{http.MethodGet, "/api/cases/" + id + "/archive", ""},
		{http.MethodPost, "/api/cases/" + id + "/verify", `{}`},
		{http.MethodPost, "/api/cases/" + id + "/free", `{"confirm":"x"}`},
		{http.MethodPost, "/api/cases/" + id + "/restore-in-place", `{}`},
		{http.MethodPost, "/api/cases/" + id + "/restore-as-new", `{}`},
		{http.MethodGet, "/api/cases/orphan-files", ""},
	} {
		if res := a.req(r.m, r.p, r.b, "open"); res.Code != http.StatusLocked {
			t.Errorf("%s %s while locked = %d %s", r.m, r.p, res.Code, res.Body)
		}
	}
	// Unlocked again (a new start), the case is back as it was and nothing is marked.
	a.restart()
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" || a.eventCount(id) != 3 {
		t.Fatalf("after unlock: state %s mark %q", a.state(id), a.entry(id).Mark)
	}
	a.archiveDone(id, dir, "open")
}

func TestA5_AnImportRunningGivesM1(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("import", 2)
	dir := a.folder()
	hd := a.handler()
	hd.jobs.mu.Lock()
	if hd.jobs.m == nil {
		hd.jobs.m = map[string]*importJob{}
	}
	hd.jobs.m["held"] = &importJob{id: "held", caseID: casestore.CaseID(id), state: jobRunning}
	hd.jobs.mu.Unlock()
	// INJ-8: an import is running.
	archReason(t, a.archiveReq(id, dir, "open"), http.StatusConflict, "import-running", importRunningText)
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" {
		t.Fatal("a refused archive left a mark")
	}
	hd.jobs.mu.Lock()
	delete(hd.jobs.m, "held")
	hd.jobs.mu.Unlock()
	// The busy lock was given back.
	a.archiveDone(id, dir, "open")
}

func TestA6_PressingArchiveTwiceGivesM3(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("twice", 2)
	dir := a.folder()
	p := pauseAt(t, "part-written")
	if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	res := a.archiveReq(id, dir, "open")
	if res.Code != http.StatusConflict {
		t.Fatalf("second press = %d %s", res.Code, res.Body)
	}
	if f := failureOf(t, res); f.Reason != "archive-running" || f.Error != archiveRunningText {
		t.Fatalf("second press = %+v", f)
	}
	p.release()
	if j := a.wait(id, "archive"); j.State != fileJobDone {
		t.Fatalf("job = %+v", j)
	}
	// After it, a press from a page that still believes "open" is the changed-elsewhere answer.
	archReason(t, a.archiveReq(id, dir, "open"), http.StatusConflict, "case-changed", caseChangedText)
}

func TestA14_CancelRemovesThePartAndTheCaseIsBackAsItWas(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("cancel", 3)
	dir := a.folder()
	p := pauseAt(t, "read-back")
	if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	if len(partsIn(t, dir)) != 1 {
		t.Fatalf("no .part while writing: %v", listNames(t, dir, ""))
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- a.req(http.MethodDelete, "/api/cases/"+id+"/archive", "", "archiving") }()
	waitCancelled(t, a, id)
	p.release()
	res := <-done
	if res.Code != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Code, res.Body)
	}
	j := a.jobOf(id, "archive")
	if j.State != fileJobStopped || j.Error != archiveStoppedText {
		t.Fatalf("job = %+v", j)
	}
	if len(partsIn(t, dir)) != 0 || len(caseFilesIn(t, dir)) != 0 {
		t.Fatalf("folder = %v", listNames(t, dir, ""))
	}
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" || a.eventCount(id) != 3 {
		t.Fatal("the case is not as it was")
	}
	// A Cancel after the end is M2.
	archReason(t, a.req(http.MethodDelete, "/api/cases/"+id+"/archive", "", "archiving"), http.StatusConflict, "case-changed", caseChangedText)
}

func TestINJ9_CancelAndCommitExactlyOneWins(t *testing.T) {
	// Cancel first: the job has the gate but has not decided yet. Cancel wins, the finished
	// file is listed, the case is back as it was.
	t.Run("cancel wins", func(t *testing.T) {
		a := newArchPC(t, cfsPassA)
		id := a.seed("race-1", 3)
		dir := a.folder()
		p := pauseAt(t, "commit-lock")
		if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
			t.Fatalf("archive = %d %s", res.Code, res.Body)
		}
		p.waitReached(t)
		done := make(chan int, 1)
		go func() { done <- a.req(http.MethodDelete, "/api/cases/"+id+"/archive", "", "archiving").Code }()
		waitCancelled(t, a, id)
		p.release()
		if code := <-done; code != http.StatusOK {
			t.Fatalf("cancel = %d", code)
		}
		if j := a.jobOf(id, "archive"); j.State != fileJobStopped {
			t.Fatalf("job = %+v", j)
		}
		if a.state(id) != casestore.StateOpen {
			t.Fatalf("state = %s", a.state(id))
		}
		files := caseFilesIn(t, dir)
		if len(files) != 1 {
			t.Fatalf("the finished file must stay: %v", files)
		}
		if o := a.orphans(); len(o.Files) != 1 || o.Files[0].Name != files[0] {
			t.Fatalf("orphans = %+v", o)
		}
	})
	// Commit first: the decision is taken, Cancel is too late (M2) and the case is Archived.
	t.Run("commit wins", func(t *testing.T) {
		a := newArchPC(t, cfsPassA)
		id := a.seed("race-2", 3)
		dir := a.folder()
		p := pauseAt(t, "commit-decided")
		if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
			t.Fatalf("archive = %d %s", res.Code, res.Body)
		}
		p.waitReached(t)
		archReason(t, a.req(http.MethodDelete, "/api/cases/"+id+"/archive", "", "archiving"), http.StatusConflict, "case-changed", caseChangedText)
		p.release()
		if j := a.wait(id, "archive"); j.State != fileJobDone {
			t.Fatalf("job = %+v", j)
		}
		if a.state(id) != casestore.StateArchived {
			t.Fatalf("state = %s", a.state(id))
		}
		if o := a.orphans(); len(o.Files) != 0 {
			t.Fatalf("orphans = %+v", o)
		}
	})
}

// waitCancelled waits until the job has recorded the Cancel.
func waitCancelled(t *testing.T, a *archPC, id string) {
	t.Helper()
	job := a.handler().caseFiles().jobs.get(jobArchive, casestore.CaseID(id))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c, _ := job.wasCancelled(); c {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("Cancel was not recorded")
}

// INJ-10: the key is locked between the rename and the mark. The finished file is
// listed, the case goes back as it was once the key is unlocked again.
func TestINJ10_KeyLockedBetweenRenameAndMark(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("late-lock", 3)
	dir := a.folder()
	p := pauseAt(t, "commit-lock")
	if res := a.archiveReq(id, dir, "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	p.waitReached(t)
	if res := a.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	p.release()
	a.handler().caseFiles().jobs.waitAll()
	if j := a.handler().caseFiles().jobs.get(jobArchive, casestore.CaseID(id)).dto(); j.State != fileJobStopped || j.Reason != "key-locked" {
		t.Fatalf("job = %+v", j)
	}
	if got := caseFilesIn(t, dir); len(got) != 1 {
		t.Fatalf("the finished file must stay: %v", got)
	}
	a.restart()
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" || a.eventCount(id) != 3 {
		t.Fatalf("state %s mark %q", a.state(id), a.entry(id).Mark)
	}
	if o := a.orphans(); len(o.Files) != 1 {
		t.Fatalf("orphans = %+v", o)
	}
}

// A13, A16, INJ-3 and INJ-4: a kill at each point of the job, then a restart,
// leaves the case as it was; a finished file is never deleted; no other .part is
// touched.
func TestA13_KillAtEveryPointThenRestartLeavesTheOriginalState(t *testing.T) {
	points := []struct {
		point   string
		hasPart bool
		hasFile bool
	}{
		{"started", false, false},
		{"part-written", true, false},
		{"read-back", true, false}, // INJ-3
		{"renamed", false, true},   // INJ-4
		{"commit-lock", false, true},
		{"commit-decided", false, true},
	}
	for _, pt := range points {
		for _, state := range []string{"open", "closed"} {
			t.Run(pt.point+"/"+state, func(t *testing.T) {
				a := newArchPC(t, cfsPassA)
				id := a.seed("kill "+pt.point, 4)
				if state == "closed" {
					if res := a.req(http.MethodPost, "/api/cases/"+id+"/close", "", "open"); res.Code != http.StatusOK {
						t.Fatalf("close = %d %s", res.Code, res.Body)
					}
				}
				dir := a.folder()
				stray := filepath.Join(dir, "somebody-elses.unravis-case.part")
				strayTxt := filepath.Join(dir, "notes.txt")
				for _, p := range []string{stray, strayTxt} {
					if err := os.WriteFile(p, []byte("not ours"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				killAt(t, pt.point)
				if res := a.archiveReq(id, dir, state); res.Code != http.StatusAccepted {
					t.Fatalf("archive = %d %s", res.Code, res.Body)
				}
				a.waitKilled(id)
				if got := len(partsIn(t, dir)) - 1; (got == 1) != pt.hasPart {
					t.Fatalf("parts after the kill = %v", partsIn(t, dir))
				}
				if got := len(caseFilesIn(t, dir)); (got == 1) != pt.hasFile {
					t.Fatalf("files after the kill = %v", caseFilesIn(t, dir))
				}
				before := caseFilesIn(t, dir)
				archiveInject = nil

				// The unlock of the restart already ran the recovery of marked cases.
				a.restart()
				info := a.entry(id)
				if string(casestore.StateOf(info)) != state || info.Mark != "" || (info.Archive != nil && info.Archive.Pending != nil) {
					t.Fatalf("after restart: state %s mark %q archive %+v", casestore.StateOf(info), info.Mark, info.Archive)
				}
				if a.eventCount(id) != 4 {
					t.Fatalf("events = %d", a.eventCount(id))
				}
				if got := partsIn(t, dir); len(got) != 1 || got[0] != filepath.Base(stray) {
					t.Fatalf("parts after recovery = %v (only the stray one stays)", got)
				}
				if _, err := os.Stat(strayTxt); err != nil {
					t.Fatal("an unrelated file was removed")
				}
				after := caseFilesIn(t, dir)
				if strings.Join(after, ",") != strings.Join(before, ",") {
					t.Fatalf("finished files changed: %v -> %v", before, after)
				}
				o := a.orphans()
				if pt.hasFile != (len(o.Files) == 1) {
					t.Fatalf("orphans = %+v", o)
				}
				// A second recovery changes nothing.
				if rep2, err := a.rec.RecoverAll(context.Background()); err != nil || rep2.Recovered != 0 {
					t.Fatalf("second recovery = %+v %v", rep2, err)
				}
				// The retry works and the case then archives for real.
				a.archiveDone(id, dir, state)
				if a.state(id) != casestore.StateArchived {
					t.Fatalf("state = %s", a.state(id))
				}
			})
		}
	}
}

func TestA17_OverTheLimitGivesM16(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("big", 3)
	dir := a.folder()
	old := archiveMaxDB
	archiveMaxDB = 1
	t.Cleanup(func() { archiveMaxDB = old })
	archReason(t, a.archiveReq(id, dir, "open"), http.StatusUnprocessableEntity, "too-large", archiveTooLargeText)
	if a.state(id) != casestore.StateOpen || a.entry(id).Mark != "" {
		t.Fatal("a refused archive left a mark")
	}
	archiveMaxDB = old
	a.archiveDone(id, dir, "open")
}
