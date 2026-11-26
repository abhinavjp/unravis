package localci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dayDur = 24 * time.Hour

func idAt(t time.Time, label string) string {
	return t.UTC().Format("20060102-150405") + "-" + label
}

// mkRun makes runs/<id> with a log of size bytes; a non-zero ended also writes run.json with that time.
func mkRun(t *testing.T, s *Store, id string, size int, ended time.Time) {
	t.Helper()
	writeCacheFile(t, filepath.Join(s.RunsPath(), id, "logs", "j", "01-step.log"), size, ended)
	if !ended.IsZero() {
		writeCacheFile(t, filepath.Join(s.RunsPath(), id, "run.json"), 2, ended)
	}
}

func mkWork(t *testing.T, s *Store, name string, size int, mod time.Time) {
	t.Helper()
	writeCacheFile(t, filepath.Join(s.WorkPath(), name, "src", "main.go"), size, mod)
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func newTrimStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTrimRunsRemovesOnlyOldFinishedRunsAndTheirWorkFolders(t *testing.T) {
	s := newTrimStore(t)
	now := time.Now()
	oldID := idAt(now.Add(-10*dayDur), "pr1-ci")
	newID := idAt(now.Add(-1*dayDur), "pr2-ci")
	liveID := idAt(now.Add(-30*time.Minute), "pr3-ci")
	mkRun(t, s, oldID, 100, now.Add(-10*dayDur))
	mkWork(t, s, oldID, 50, now.Add(-10*dayDur))
	mkWork(t, s, oldID+"-j3", 50, now.Add(-10*dayDur)) // the extra checkout of one job
	mkRun(t, s, newID, 100, now.Add(-1*dayDur))
	mkWork(t, s, newID, 50, now.Add(-1*dayDur))
	mkRun(t, s, liveID, 100, time.Time{}) // no run.json: still going
	mkWork(t, s, liveID, 50, now.Add(-30*time.Minute))
	idx := filepath.Join(s.RunsPath(), "index.jsonl")
	if err := os.WriteFile(idx, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: now, ActiveSince: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Runs != 1 || res.Work != 2 || res.Freed != 202 {
		t.Fatalf("result %+v, want 1 run, 2 work folders, 202 bytes", res)
	}
	for p, want := range map[string]bool{
		filepath.Join(s.RunsPath(), oldID):             false,
		filepath.Join(s.WorkPath(), oldID):             false,
		filepath.Join(s.WorkPath(), oldID+"-j3"):       false,
		filepath.Join(s.RunsPath(), newID):             true,
		filepath.Join(s.WorkPath(), newID):             true,
		filepath.Join(s.RunsPath(), liveID):            true,
		filepath.Join(s.WorkPath(), liveID):            true,
		idx:                                            true,
		filepath.Join(s.RunsPath(), newID, "run.json"): true,
	} {
		if pathExists(p) != want {
			t.Errorf("%s: exists=%v, want %v", p, !want, want)
		}
	}
}

func TestTrimRunsNeverTouchesAFolderThatMayBelongToARunningRequest(t *testing.T) {
	s := newTrimStore(t)
	now := time.Now()
	// A run with no run.json that began 9 days ago, while a request claimed 10 days ago still runs: it may be that
	// request's run, so it stays even though it is older than 7 days.
	maybe := idAt(now.Add(-9*dayDur), "pr1-ci")
	// One that began before that request was claimed cannot be its run: a crashed run, removed.
	crashed := idAt(now.Add(-12*dayDur), "pr2-ci")
	mkRun(t, s, maybe, 10, time.Time{})
	mkRun(t, s, crashed, 10, time.Time{})
	res, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: now, ActiveSince: now.Add(-10 * dayDur)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Runs != 1 || !pathExists(filepath.Join(s.RunsPath(), maybe)) || pathExists(filepath.Join(s.RunsPath(), crashed)) {
		t.Fatalf("result %+v; maybe exists=%v crashed exists=%v", res, pathExists(filepath.Join(s.RunsPath(), maybe)), pathExists(filepath.Join(s.RunsPath(), crashed)))
	}
}

func TestTrimRunsSizeRuleRemovesOldestFinishedFirstAndKeepsTheLastTwoHours(t *testing.T) {
	s := newTrimStore(t)
	now := time.Now()
	a := idAt(now.Add(-5*dayDur), "a")
	b := idAt(now.Add(-3*dayDur), "b")
	c := idAt(now.Add(-6*time.Hour), "c")
	d := idAt(now.Add(-10*time.Minute), "d")
	for i, id := range []string{a, b, c, d} {
		mkRun(t, s, id, 1000, now.Add([]time.Duration{-5 * dayDur, -3 * dayDur, -6 * time.Hour, -10 * time.Minute}[i]))
	}
	// total 4000+8 (run.json); limit 2500 removes a then b (3000 -> ...), stops once under.
	res, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: now, Limit: 2500})
	if err != nil {
		t.Fatal(err)
	}
	if res.Runs != 2 || pathExists(filepath.Join(s.RunsPath(), a)) || pathExists(filepath.Join(s.RunsPath(), b)) || !pathExists(filepath.Join(s.RunsPath(), c)) || !pathExists(filepath.Join(s.RunsPath(), d)) {
		t.Fatalf("result %+v", res)
	}
	// A limit nothing can satisfy still keeps what ended in the last two hours.
	if _, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: now, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if pathExists(filepath.Join(s.RunsPath(), c)) || !pathExists(filepath.Join(s.RunsPath(), d)) {
		t.Fatalf("c exists=%v (want gone), d exists=%v (want kept)", pathExists(filepath.Join(s.RunsPath(), c)), pathExists(filepath.Join(s.RunsPath(), d)))
	}
}

func TestTrimRunsNegativeLimitTurnsTheSizeRuleOff(t *testing.T) {
	s := newTrimStore(t)
	now := time.Now()
	id := idAt(now.Add(-3*dayDur), "a")
	mkRun(t, s, id, 5000, now.Add(-3*dayDur))
	if res, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: now, Limit: -1}); err != nil || res.Runs != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrimRunsDoesNotFollowALinkInsideTheRoot(t *testing.T) {
	s := newTrimStore(t)
	now := time.Now()
	outside := t.TempDir()
	keepMe := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(keepMe, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldID := idAt(now.Add(-10*dayDur), "pr1-ci")
	// 1. a run folder that is itself a link to a folder outside the store
	if err := os.Symlink(outside, filepath.Join(s.RunsPath(), idAt(now.Add(-11*dayDur), "linked"))); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	// 2. a work folder that is a link
	if err := os.Symlink(outside, filepath.Join(s.WorkPath(), oldID)); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	// 3. a link inside an old run folder
	mkRun(t, s, oldID, 10, now.Add(-10*dayDur))
	if err := os.Symlink(outside, filepath.Join(s.RunsPath(), oldID, "artifacts")); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}

	res, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !pathExists(keepMe) {
		t.Fatal("the file outside the store was removed through a link")
	}
	if len(res.Skipped) < 2 {
		t.Fatalf("the two linked folders must be reported as left alone: %+v", res)
	}
	for _, sk := range res.Skipped {
		if !strings.Contains(sk, "link") {
			t.Errorf("skipped line %q does not say it is a link", sk)
		}
	}
	// The linked work folder and the linked run folder are left as they are; the real old run (which holds a
	// link of its own) goes.
	if !pathExists(filepath.Join(s.WorkPath(), oldID)) || !pathExists(filepath.Join(s.RunsPath(), idAt(now.Add(-11*dayDur), "linked"))) {
		t.Fatal("a link in runs/ or work/ was removed")
	}
	if pathExists(filepath.Join(s.RunsPath(), oldID)) {
		t.Fatal("the old run folder stayed")
	}
}

func TestTrimRunsRemovesALinkInsideAnOldRunWithoutFollowingIt(t *testing.T) {
	s := newTrimStore(t)
	now := time.Now()
	outside := t.TempDir()
	keepMe := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(keepMe, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldID := idAt(now.Add(-10*dayDur), "pr1-ci")
	mkRun(t, s, oldID, 10, now.Add(-10*dayDur))
	if err := os.Symlink(outside, filepath.Join(s.RunsPath(), oldID, "artifacts")); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	if _, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: now}); err != nil {
		t.Fatal(err)
	}
	if pathExists(filepath.Join(s.RunsPath(), oldID)) {
		t.Fatal("the old run folder stayed")
	}
	if !pathExists(keepMe) {
		t.Fatal("the file the link pointed at was removed")
	}
}

func TestTrimRunsRefusesAPathOutsideItsRoot(t *testing.T) {
	s := newTrimStore(t)
	outside := t.TempDir()
	for _, name := range []string{"..", "../" + filepath.Base(outside), filepath.ToSlash(outside), `..\x`, "a/b", "", "."} {
		if p, why := safeChild(s, s.RunsPath(), name); why == "" {
			t.Errorf("safeChild(%q) gave %q, want a refusal", name, p)
		}
	}
	// A root that is not the store's own folder (runs/ replaced by a link elsewhere) is refused too.
	other := t.TempDir()
	writeCacheFile(t, filepath.Join(other, "20200101-000000-x", "run.json"), 1, time.Now().Add(-100*dayDur))
	if err := os.RemoveAll(s.RunsPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, s.RunsPath()); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	res, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Runs != 0 || !pathExists(filepath.Join(other, "20200101-000000-x", "run.json")) {
		t.Fatalf("a runs/ folder that links outside the store was trimmed: %+v", res)
	}
}

func TestTrimRunsKeepsTheRunWhenItsWorkFolderWillNotGo(t *testing.T) {
	s := newTrimStore(t)
	now := time.Now()
	id := idAt(now.Add(-10*dayDur), "pr1-ci")
	mkRun(t, s, id, 10, now.Add(-10*dayDur))
	mkWork(t, s, id, 10, now.Add(-10*dayDur))
	res, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: now, RemoveWork: func(context.Context, string) error { return errors.New("in use") }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Runs != 0 || !pathExists(filepath.Join(s.RunsPath(), id, "run.json")) || len(res.Skipped) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestTrimRunsOrphanWorkFolderGoesByItsOwnAge(t *testing.T) {
	s := newTrimStore(t)
	now := time.Now()
	old := idAt(now.Add(-10*dayDur), "gone")
	fresh := idAt(now.Add(-time.Hour), "fresh")
	mkWork(t, s, old, 10, now.Add(-10*dayDur))
	mkWork(t, s, fresh, 10, now.Add(-time.Hour))
	if _, err := s.TrimRuns(context.Background(), TrimRunsOptions{Now: now}); err != nil {
		t.Fatal(err)
	}
	if pathExists(filepath.Join(s.WorkPath(), old)) || !pathExists(filepath.Join(s.WorkPath(), fresh)) {
		t.Fatalf("old exists=%v fresh exists=%v", pathExists(filepath.Join(s.WorkPath(), old)), pathExists(filepath.Join(s.WorkPath(), fresh)))
	}
}

func TestAgentRemovesOldFinishedRunFoldersAndLogsIt(t *testing.T) {
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) {
		<-ctx.Done() // the request stays running
		return Outcome{}, ctx.Err()
	})
	s, err := OpenStore(rig.dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	oldID := idAt(now.Add(-10*dayDur), "pr1-ci")
	liveID := idAt(now, "pr9-ci")
	mkRun(t, s, oldID, 10, now.Add(-10*dayDur))
	mkRun(t, s, liveID, 10, time.Time{})
	mustEnqueue(t, rig.q, prReq(9, strings.Repeat("b", 40)))
	rig.start()
	waitFor(t, "the old run folder to go", func() bool { return !pathExists(filepath.Join(s.RunsPath(), oldID)) })
	waitFor(t, "the log line", func() bool { return strings.Contains(rig.log.String(), "removed 1 old run folders") })
	if !pathExists(filepath.Join(s.RunsPath(), liveID)) {
		t.Fatal("the run folder of a running request was removed")
	}
}

func TestAgentRunKeepNegativeRemovesNothing(t *testing.T) {
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) { return Outcome{Conclusion: "success"}, nil })
	rig.agent.RunKeep = -1
	s, err := OpenStore(rig.dir)
	if err != nil {
		t.Fatal(err)
	}
	oldID := idAt(time.Now().Add(-10*dayDur), "pr1-ci")
	mkRun(t, s, oldID, 10, time.Now().Add(-10*dayDur))
	req := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("c", 40)))
	rig.start()
	waitFor(t, "done", func() bool { return rig.get(req.ID).State == StateDone })
	if !pathExists(filepath.Join(s.RunsPath(), oldID)) {
		t.Fatal("RunKeep -1 must keep every run")
	}
}
