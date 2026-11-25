package localci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	shaA = strings.Repeat("a", 40)
	shaB = strings.Repeat("b", 40)
)

// testNaming is the naming the tests use. The names are the tests' own; the core knows none.
func testNaming(postRequired bool) StatusNaming {
	return StatusNaming{PostRequired: postRequired, Gates: []GateRule{
		{Kind: KindPR, Branch: "develop", Workflow: "quick.yml", Context: "quick-gate"},
		{Kind: KindPR, Branch: "main", Workflow: "full.yml", Context: "full-gate"},
		{Kind: KindPush, Branch: "main", Workflow: "full.yml", Context: "full-gate"},
		{Kind: KindPush, Branch: "develop", Workflow: "quick.yml", Context: "quick-gate"},
		{Kind: KindPush, Branch: "develop", Workflow: "full.yml", Context: "full-gate"},
	}}
}

func quickPR(n int, sha string) Request {
	return Request{ID: fmt.Sprintf("r%d", n), Kind: KindPR, Repo: "o/r", Ref: "feature", Base: "develop", Number: n, SHA: sha, Workflow: "quick.yml"}
}

// ---------------------------------------------------------------- description

func TestStatusDescriptionRedactsCutsAndFlattens(t *testing.T) {
	red := func(s string) string { return strings.ReplaceAll(s, "hunter2secret", "***") }
	got := StatusDescription("failed:\n\tstep 3  hunter2secret\x00 and ghp_abcdefghijklmnopqrstuv", red)
	if got != "failed: step 3 *** and ***" {
		t.Fatalf("got %q", got)
	}
	long := StatusDescription(strings.Repeat("é", 300), nil)
	if r := []rune(long); len(r) != StatusDescMax || !strings.HasSuffix(long, "...") {
		t.Fatalf("not cut to %d characters: %d %q", StatusDescMax, len(r), long)
	}
	if got := StatusDescription("short", nil); got != "short" {
		t.Fatalf("got %q", got)
	}
}

// ---------------------------------------------------------------- the gh reporter

func TestGHReporterPostsOneStatusWithoutALink(t *testing.T) {
	var gotArgs []string
	g := &GHReporter{Env: []string{"GH_TOKEN=agent"}, Redact: func(s string) string { return strings.ReplaceAll(s, "SECRET", "***") },
		exec: func(_ context.Context, env []string, args ...string) ([]byte, error) {
			if len(env) != 1 || env[0] != "GH_TOKEN=agent" {
				t.Errorf("env %v", env)
			}
			gotArgs = args
			return nil, nil
		}}
	if err := g.Report(context.Background(), "o/r", shaA, "local/quick-gate", StatusFailure, "failed: SECRET"); err != nil {
		t.Fatal(err)
	}
	line := strings.Join(gotArgs, " ")
	for _, want := range []string{"api --method POST repos/o/r/statuses/" + shaA, "state=failure", "context=local/quick-gate", "description=failed: ***"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %q", want, line)
		}
	}
	if strings.Contains(line, "target_url") || strings.Contains(line, "SECRET") {
		t.Errorf("no link, no secret: %q", line)
	}
}

func TestGHReporterRefusesWhatItCannotPost(t *testing.T) {
	g := &GHReporter{exec: func(context.Context, []string, ...string) ([]byte, error) {
		t.Error("nothing may be posted")
		return nil, nil
	}}
	for name, c := range map[string][4]string{
		"repo":  {"orrepo", shaA, "local/x", StatusPending},
		"sha":   {"o/r", "main", "local/x", StatusPending},
		"state": {"o/r", shaA, "local/x", "done"},
		"name":  {"o/r", shaA, "", StatusPending},
		"ctrl":  {"o/r", shaA, "local/\nx", StatusPending},
	} {
		if err := g.Report(context.Background(), c[0], c[1], c[2], c[3], "d"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestGHReporterClassifiesErrors(t *testing.T) {
	for msg, transient := range map[string]bool{
		"gh: HTTP 502: Bad Gateway":                       true,
		"gh: HTTP 500":                                    true,
		"HTTP 403: API rate limit exceeded":               true,
		"dial tcp: lookup api.github.com: no such host":   true,
		"gh: HTTP 422: Validation Failed":                 false,
		"gh: HTTP 404: Not Found":                         false,
		"gh: HTTP 401: Bad credentials":                   false,
		"gh: HTTP 403: Resource not accessible by token":  false,
		"HTTP 403: You have exceeded a secondary abuse x": true,
	} {
		g := &GHReporter{exec: func(context.Context, []string, ...string) ([]byte, error) { return nil, errors.New(msg) }}
		err := g.Report(context.Background(), "o/r", shaA, "local/x", StatusPending, "d")
		if IsTransientStatusError(err) != transient {
			t.Errorf("%q: transient=%v, want %v", msg, !transient, transient)
		}
	}
}

// ---------------------------------------------------------------- retry

func TestRetryRepeatsTransientFailuresWithBackoff(t *testing.T) {
	fake := &FakeReporter{Fail: func(n int, _ StatusCall) error {
		if n <= 2 {
			return &PostError{Err: errors.New("HTTP 502"), Transient: true}
		}
		return nil
	}}
	var waits []time.Duration
	r := &RetryReporter{Next: fake, Sleep: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }}
	if err := r.Report(context.Background(), "o/r", shaA, "local/x", StatusPending, "d"); err != nil {
		t.Fatal(err)
	}
	if fake.Tries() != 3 || len(fake.Calls()) != 1 {
		t.Fatalf("tries %d, posted %d", fake.Tries(), len(fake.Calls()))
	}
	if len(waits) != 2 || waits[0] != time.Second || waits[1] != 2*time.Second {
		t.Fatalf("backoff %v, want 1s then 2s", waits)
	}
}

func TestRetryGivesUpOnPermanentErrorsAndAfterTheLastTry(t *testing.T) {
	perm := &FakeReporter{Fail: func(int, StatusCall) error { return &PostError{Err: errors.New("HTTP 422")} }}
	r := &RetryReporter{Next: perm, Sleep: func(context.Context, time.Duration) error { t.Error("no wait for a permanent error"); return nil }}
	if err := r.Report(context.Background(), "o/r", shaA, "local/x", StatusPending, "d"); err == nil || perm.Tries() != 1 {
		t.Fatalf("permanent: tries %d err %v", perm.Tries(), err)
	}
	always := &FakeReporter{Fail: func(int, StatusCall) error { return &PostError{Err: errors.New("HTTP 500"), Transient: true} }}
	r = &RetryReporter{Next: always, Attempts: 3, Sleep: func(context.Context, time.Duration) error { return nil }}
	err := r.Report(context.Background(), "o/r", shaA, "local/x", StatusPending, "d")
	if err == nil || always.Tries() != 3 || !strings.Contains(err.Error(), "gave up after 3 tries") {
		t.Fatalf("tries %d err %v", always.Tries(), err)
	}
	// a stop while waiting ends the retries at once
	r = &RetryReporter{Next: always, Sleep: func(context.Context, time.Duration) error { return context.Canceled }}
	before := always.Tries()
	if err := r.Report(context.Background(), "o/r", shaA, "local/x", StatusPending, "d"); err == nil || always.Tries() != before+1 {
		t.Fatalf("stopped wait: %v", err)
	}
}

// ---------------------------------------------------------------- dedup

func TestDedupDoesNotRepeatAPostTheHostAlreadyHas(t *testing.T) {
	fake := &FakeReporter{}
	d := &DedupReporter{Next: fake}
	post := func(sha, name, state, desc string) {
		t.Helper()
		if err := d.Report(context.Background(), "o/r", sha, name, state, desc); err != nil {
			t.Fatal(err)
		}
	}
	post(shaA, "local/x", StatusPending, "queued")
	post(shaA, "local/x", StatusPending, "queued")              // same: dropped
	post(shaA, "local/x", StatusPending, "running, job 1 of 3") // same state, new description: posted
	post(shaA, "local/x", StatusSuccess, "passed in 3s")        // new state
	post(shaA, "local/x", StatusSuccess, "passed in 3s")        // dropped
	post(shaB, "local/x", StatusSuccess, "passed in 3s")        // other commit
	post(shaA, "local/y", StatusSuccess, "passed in 3s")        // other context
	post(shaA, "local/x", StatusPending, "queued")              // an older state again: it is a change, posted
	if got := len(fake.Calls()); got != 6 {
		t.Fatalf("%d posts, want 6: %+v", got, fake.Calls())
	}
}

func TestDedupRemembersAcrossRestartsAndForgetsFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status", "ledger.json")
	fake := &FakeReporter{}
	if err := (&DedupReporter{Next: fake, Path: path}).Report(context.Background(), "o/r", shaA, "local/x", StatusPending, "queued"); err != nil {
		t.Fatal(err)
	}
	again := &DedupReporter{Next: fake, Path: path} // a new process
	if err := again.Report(context.Background(), "o/r", shaA, "local/x", StatusPending, "queued"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls()) != 1 {
		t.Fatalf("a restart repeated a post: %+v", fake.Calls())
	}
	// a failed post is not remembered, so the next try goes out
	bad := &FakeReporter{Fail: func(n int, _ StatusCall) error {
		if n == 1 {
			return errors.New("boom")
		}
		return nil
	}}
	d := &DedupReporter{Next: bad, Path: filepath.Join(t.TempDir(), "l.json")}
	if err := d.Report(context.Background(), "o/r", shaA, "local/x", StatusError, "e"); err == nil {
		t.Fatal("the failure must come back")
	}
	if err := d.Report(context.Background(), "o/r", shaA, "local/x", StatusError, "e"); err != nil || len(bad.Calls()) != 1 {
		t.Fatalf("retry after failure: %v %d", err, len(bad.Calls()))
	}
	// old entries are pruned
	now := time.Now()
	old := &DedupReporter{Next: fake, Path: filepath.Join(t.TempDir(), "l.json"), Keep: time.Hour, Now: func() time.Time { return now }}
	_ = old.Report(context.Background(), "o/r", shaB, "local/z", StatusPending, "queued")
	now = now.Add(2 * time.Hour)
	_ = old.Report(context.Background(), "o/r", shaB, "local/w", StatusPending, "queued")
	b, _ := os.ReadFile(old.Path)
	if strings.Contains(string(b), "local/z") {
		t.Fatalf("the old entry is still in the ledger: %s", b)
	}
}

// ---------------------------------------------------------------- the shadow guard

func TestShadowGuardOnlyLetsTheShadowPrefixThrough(t *testing.T) {
	fake := &FakeReporter{}
	g := &ShadowGuard{Next: fake} // the zero value is shadow mode
	ctx := context.Background()
	for _, bad := range []string{"quick-gate", "full-gate", "build", "locally/x", "Local/x", ""} {
		if err := g.Report(ctx, "o/r", shaA, bad, StatusSuccess, "d"); !errors.Is(err, ErrNotShadow) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if err := g.Report(ctx, "o/r", shaA, "local/quick-gate", StatusSuccess, "d"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls()) != 1 {
		t.Fatalf("%+v", fake.Calls())
	}
	if err := (&ShadowGuard{Next: fake, PostRequired: true}).Report(ctx, "o/r", shaA, "quick-gate", StatusSuccess, "d"); err != nil {
		t.Fatalf("required mode: %v", err)
	}
	if err := (&ShadowGuard{Next: fake, Prefix: "shadow/"}).Report(ctx, "o/r", shaA, "local/x", StatusSuccess, "d"); !errors.Is(err, ErrNotShadow) {
		t.Fatalf("a custom prefix replaces the default: %v", err)
	}
}

// ---------------------------------------------------------------- naming

func TestNamingShadowAndRequired(t *testing.T) {
	shadow, req := testNaming(false), testNaming(true)
	pr := quickPR(1, shaA)
	if n, ok := shadow.GateName(pr); !ok || n != "local/quick-gate" {
		t.Fatalf("shadow gate %q %v", n, ok)
	}
	if n, ok := req.GateName(pr); !ok || n != "quick-gate" {
		t.Fatalf("required gate %q %v", n, ok)
	}
	// the zero value is shadow: nothing but the explicit flag turns the real names on
	if (StatusNaming{}).PostRequired {
		t.Fatal("PostRequired must default to false")
	}
	// push to main
	push := Request{Kind: KindPush, Repo: "o/r", Ref: "main", SHA: shaA, Workflow: "full.yml"}
	if n, _ := shadow.GateName(push); n != "local/full-gate" {
		t.Fatalf("push gate %q", n)
	}
	// a PR into main
	if n, _ := shadow.GateName(Request{Kind: KindPR, Base: "main", Workflow: "full.yml"}); n != "local/full-gate" {
		t.Fatalf("pr into main %q", n)
	}
	// no rule, no status
	if _, ok := shadow.GateName(Request{Kind: KindPR, Base: "other", Workflow: "quick.yml"}); ok {
		t.Fatal("a branch without a rule must get no gate")
	}
	if _, ok := shadow.GateName(Request{Kind: KindSchedule, Ref: "main", Workflow: "full.yml"}); ok {
		t.Fatal("a schedule request must get no gate")
	}
}

func TestNamingJobsAreLocalPrefixedAndQualifiedWhenTwoWorkflowsShareABranch(t *testing.T) {
	n := testNaming(false)
	// one workflow gates a PR into develop: the job name is local/<job>
	if got := n.JobName(quickPR(1, shaA), "lint"); got != "local/lint" {
		t.Fatalf("got %q", got)
	}
	// two workflows run for a push to develop: the workflow stem is put in between
	push := Request{Kind: KindPush, Ref: "develop", Workflow: "full.yml"}
	if got := n.JobName(push, "lint"); got != "local/full/lint" {
		t.Fatalf("got %q", got)
	}
	// a job named like the gate would clash with it
	if got := n.JobName(quickPR(1, shaA), "quick-gate"); got != "" {
		t.Fatalf("job equal to the gate must be skipped, got %q", got)
	}
	// required mode never makes per-job names required: they keep the prefix
	if got := testNaming(true).JobName(quickPR(1, shaA), "lint"); got != "local/lint" {
		t.Fatalf("got %q", got)
	}
	if got := n.JobName(quickPR(1, shaA), "  "); got != "" {
		t.Fatalf("blank job: %q", got)
	}
}

// ---------------------------------------------------------------- the poster

func TestPosterPostsInOrderAndCoalescesWhatHasNotGoneOut(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{})
	var mu sync.Mutex
	var seen []string
	var once sync.Once
	rep := reporterFunc(func(_ context.Context, _, _, name, state, desc string) error {
		once.Do(func() {
			close(entered)
			<-gate // the first post is slow
		})
		mu.Lock()
		seen = append(seen, name+" "+state+" "+desc)
		mu.Unlock()
		return nil
	})
	p := &StatusPoster{Reporter: rep}
	defer p.Close(time.Second)
	p.Post("o/r", shaA, "local/a", StatusPending, "queued")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first post never started")
	}
	p.Post("o/r", shaA, "local/b", StatusPending, "queued")
	p.Post("o/r", shaA, "local/a", StatusPending, "running") // replaces nothing in flight: a is busy
	p.Post("o/r", shaA, "local/b", StatusSuccess, "passed")  // replaces b's queued, which has not gone out
	close(gate)
	if !p.Flush(5 * time.Second) {
		t.Fatal("the poster did not go idle")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"local/a pending queued", "local/b success passed", "local/a pending running"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("got  %v\nwant %v", seen, want)
	}
}

func TestPosterFailureNeverReachesTheCallerAndShowsInHealth(t *testing.T) {
	var logged []string
	var mu sync.Mutex
	p := &StatusPoster{
		Reporter: &FakeReporter{Fail: func(_ int, c StatusCall) error {
			if c.Name == "local/bad" {
				return errors.New("HTTP 422 nope")
			}
			return nil
		}},
		Log: func(f string, a ...any) { mu.Lock(); logged = append(logged, fmt.Sprintf(f, a...)); mu.Unlock() },
	}
	defer p.Close(time.Second)
	p.Post("o/r", shaA, "local/bad", StatusFailure, "x")
	p.Post("o/r", shaA, "local/good", StatusSuccess, "y")
	if !p.Flush(5 * time.Second) {
		t.Fatal("idle")
	}
	h := p.Health()
	if h.Posted != 1 || h.Failed != 1 || h.Waiting != 0 || !strings.Contains(h.LastError, "local/bad") || h.LastPost.IsZero() || h.LastErrorAt.IsZero() {
		t.Fatalf("%+v", h)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || !strings.Contains(logged[0], "422") {
		t.Fatalf("log %v", logged)
	}
}

func TestPosterCloseFlushesThenIgnoresLaterPosts(t *testing.T) {
	fake := &FakeReporter{}
	p := &StatusPoster{Reporter: fake}
	p.Post("o/r", shaA, "local/a", StatusPending, "queued")
	p.Close(5 * time.Second)
	if len(fake.Calls()) != 1 {
		t.Fatalf("close must flush: %+v", fake.Calls())
	}
	p.Post("o/r", shaA, "local/a", StatusSuccess, "late")
	if len(fake.Calls()) != 1 {
		t.Fatal("a closed poster posts nothing")
	}
}

type reporterFunc func(ctx context.Context, repo, sha, name, state, desc string) error

func (f reporterFunc) Report(ctx context.Context, repo, sha, name, state, desc string) error {
	return f(ctx, repo, sha, name, state, desc)
}

// ---------------------------------------------------------------- the service

type svcRig struct {
	t    *testing.T
	dir  string
	q    *Queue
	fake *FakeReporter
	poll *StatusPoster
	svc  *StatusService
	now  time.Time
}

func newSvcRig(t *testing.T, naming StatusNaming) *svcRig {
	t.Helper()
	dir := t.TempDir()
	q, err := OpenQueue(QueueDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	r := &svcRig{t: t, dir: dir, q: q, fake: &FakeReporter{}, now: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)}
	r.poll = &StatusPoster{Reporter: r.fake}
	t.Cleanup(func() { r.poll.Close(time.Second) })
	r.svc = &StatusService{Poster: r.poll, Naming: naming, Dir: dir, Now: func() time.Time { return r.now }}
	return r
}

// lines flushes and lists the posts as "name state: description", in the order they went out.
func (r *svcRig) lines() []string {
	r.t.Helper()
	if !r.poll.Flush(5 * time.Second) {
		r.t.Fatal("the poster did not go idle")
	}
	var out []string
	for _, c := range r.fake.Calls() {
		out = append(out, c.Name+" "+c.State+": "+c.Description)
	}
	return out
}

func (r *svcRig) expect(want ...string) {
	r.t.Helper()
	got := r.lines()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		r.t.Fatalf("posts\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func (r *svcRig) enqueue(req Request) Request {
	r.t.Helper()
	req.ID = ""
	return mustEnqueue(r.t, r.q, req)
}

func (r *svcRig) claim() Request {
	r.t.Helper()
	c, err := r.q.Claim("a1", nil)
	if err != nil || c == nil {
		r.t.Fatalf("claim: %v %v", c, err)
	}
	return *c
}

func (r *svcRig) progress(req Request) *ProgressWriter {
	w := &ProgressWriter{StateDir: r.dir, ID: req.ID, Now: func() time.Time { return r.now }}
	w.Reset()
	return w
}

func jobEv(kind, key, label, status, failed string, start, end time.Time) RunEvent {
	return RunEvent{Kind: kind, Key: key, JobID: key, Label: label, Status: status, FailedStep: failed, Start: start, End: end}
}

func TestServiceStatesInOrderForAPassingRun(t *testing.T) {
	r := newSvcRig(t, testNaming(false))
	r.enqueue(quickPR(5, shaA))
	r.svc.ObserveQueue(r.q)
	r.svc.ObserveQueue(r.q) // looked at twice: the same request is not announced twice
	r.expect("local/quick-gate pending: queued")

	req := r.claim()
	since := r.now
	r.svc.Started(req)
	r.expect("local/quick-gate pending: queued", "local/quick-gate pending: running")

	w := r.progress(req)
	w.Event(RunEvent{Kind: EventPlan, Total: 3})
	t0 := r.now
	w.Event(jobEv(EventJobStart, "lint", "lint", "", "", t0, time.Time{}))
	r.svc.Progress(req)
	r.expect("local/quick-gate pending: queued", "local/quick-gate pending: running", "local/quick-gate pending: running, job 1 of 3")

	w.Event(jobEv(EventJobEnd, "lint", "lint", "success", "", t0, t0.Add(4*time.Second)))
	w.Event(jobEv(EventJobStart, "test", "test", "", "", t0.Add(4*time.Second), time.Time{}))
	r.svc.Progress(req)
	r.svc.Progress(req) // nothing changed: nothing posted
	want := []string{
		"local/quick-gate pending: queued", "local/quick-gate pending: running", "local/quick-gate pending: running, job 1 of 3",
		"local/lint success: passed in 4s",
		"local/quick-gate pending: running, job 2 of 3",
	}
	r.expect(want...)

	w.Event(jobEv(EventJobEnd, "test", "test", "success", "", t0.Add(4*time.Second), t0.Add(10*time.Second)))
	w.Event(jobEv(EventJobStart, "build", "build", "", "", t0.Add(10*time.Second), time.Time{}))
	w.Event(jobEv(EventJobEnd, "build", "build", "skipped", "", t0, t0))
	r.now = since.Add(75 * time.Second)
	done, err := r.q.Complete(req.ID, "run1", "success", "")
	if err != nil {
		t.Fatal(err)
	}
	r.svc.Finished(req, since, nil, done)
	got := r.lines()
	if last := got[len(got)-1]; last != "local/quick-gate success: passed in 1m15s (2 jobs)" {
		t.Fatalf("last post %q\nall: %v", last, got)
	}
	if !contains(got, "local/test success: passed in 6s") {
		t.Fatalf("the job that ended before the finish must be posted too: %v", got)
	}
	for _, l := range got {
		if strings.Contains(l, "local/build") {
			t.Fatalf("a skipped job gets no status: %v", got)
		}
	}
	if _, err := ReadProgress(r.dir, req.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ProgressPath(r.dir, req.ID)); err == nil {
		t.Fatal("the progress file must be removed at the end")
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestServiceFailureNamesTheJobAndStep(t *testing.T) {
	r := newSvcRig(t, testNaming(false))
	req := r.claimNew(quickPR(7, shaA))
	since := r.now
	r.svc.Started(req)
	w := r.progress(req)
	w.Event(RunEvent{Kind: EventPlan, Total: 2})
	w.Event(jobEv(EventJobStart, "go", "go vet", "", "", r.now, time.Time{}))
	w.Event(jobEv(EventJobEnd, "go", "go vet", "failure", "step 3 go vet ./...", r.now, r.now.Add(9*time.Second)))
	r.now = since.Add(12 * time.Second)
	done, _ := r.q.Complete(req.ID, "run1", "failure", "")
	r.svc.Finished(req, since, nil, done)
	got := r.lines()
	if last := got[len(got)-1]; last != "local/quick-gate failure: failed in 12s: job go vet, step 3 go vet ./..." {
		t.Fatalf("last %q\n%v", last, got)
	}
	if !contains(got, "local/go vet failure: failed in 9s: step 3 go vet ./...") {
		t.Fatalf("per-job status missing: %v", got)
	}
	// the run's own reason wins when the engine gave one
	r2 := newSvcRig(t, testNaming(false))
	req2 := r2.claimNew(quickPR(8, shaA))
	r2.svc.Started(req2)
	done, _ = r2.q.Complete(req2.ID, "run2", "failure", "job lint: step 2 npm ci")
	r2.svc.Finished(req2, r2.now, nil, done)
	if got := r2.lines(); got[len(got)-1] != "local/quick-gate failure: failed in 0ms: job lint: step 2 npm ci" {
		t.Fatalf("%v", got)
	}
}

func (r *svcRig) claimNew(req Request) Request {
	r.enqueue(req)
	return r.claim()
}

func TestServiceSupersededPostsNothingCancelledPostsError(t *testing.T) {
	// superseded while queued: the newer head has its own statuses
	r := newSvcRig(t, testNaming(false))
	old := r.enqueue(quickPR(3, shaA))
	r.svc.ObserveQueue(r.q)
	newer := r.enqueue(quickPR(3, shaB))
	if _, err := r.q.Supersede(old.ID, newer.ID); err != nil {
		t.Fatal(err)
	}
	r.svc.ObserveQueue(r.q)
	got := r.lines()
	for _, c := range r.fake.Calls() {
		if c.SHA == shaA && c.State != StatusPending {
			t.Fatalf("the superseded head got a final status: %+v", c)
		}
	}
	if !contains(got, "local/quick-gate pending: queued") {
		t.Fatalf("%v", got)
	}

	// superseded while running: Finished posts nothing new
	r = newSvcRig(t, testNaming(false))
	run := r.claimNew(quickPR(4, shaA))
	r.svc.Started(run)
	n := len(r.lines())
	if _, err := r.q.Supersede(run.ID, "other"); err != nil {
		t.Fatal(err)
	}
	done, _ := r.q.Complete(run.ID, "", "cancelled", "")
	if done.State != StateSuperseded {
		t.Fatalf("state %s", done.State)
	}
	r.svc.Finished(run, r.now, context.Canceled, done)
	if got := r.lines(); len(got) != n {
		t.Fatalf("a superseded run posted: %v", got[n:])
	}

	// cancelled while running: error "cancelled"
	r = newSvcRig(t, testNaming(false))
	run = r.claimNew(quickPR(5, shaA))
	r.svc.Started(run)
	if _, err := r.q.Cancel(run.ID, "by hand"); err != nil {
		t.Fatal(err)
	}
	done, _ = r.q.Complete(run.ID, "", "cancelled", "")
	r.svc.Finished(run, r.now, context.Canceled, done)
	got = r.lines()
	if got[len(got)-1] != "local/quick-gate error: cancelled" {
		t.Fatalf("%v", got)
	}

	// cancelled while queued: error "cancelled"
	r = newSvcRig(t, testNaming(false))
	q := r.enqueue(quickPR(6, shaA))
	r.svc.ObserveQueue(r.q)
	if _, err := r.q.Cancel(q.ID, "by hand"); err != nil {
		t.Fatal(err)
	}
	r.svc.ObserveQueue(r.q)
	r.expect("local/quick-gate pending: queued", "local/quick-gate error: cancelled")
}

func TestServiceACrashOrAnErrorIsAnError(t *testing.T) {
	r := newSvcRig(t, testNaming(false))
	run := r.claimNew(quickPR(9, shaA))
	r.svc.Started(run)
	done, _ := r.q.Complete(run.ID, "", "error", "the child process ended with exit status 2")
	r.svc.Finished(run, r.now, errors.New("the child process ended with exit status 2"), done)
	got := r.lines()
	if last := got[len(got)-1]; last != "local/quick-gate error: error after 0ms: the child process ended with exit status 2" {
		t.Fatalf("%q", last)
	}
}

func TestServiceRequestWithoutARuleGetsNoStatuses(t *testing.T) {
	r := newSvcRig(t, testNaming(false))
	req := Request{Kind: KindPR, Repo: "o/r", Ref: "f", Base: "unknown-branch", Number: 1, SHA: shaA, Workflow: "quick.yml"}
	run := r.claimNew(req)
	r.svc.ObserveQueue(r.q)
	r.svc.Started(run)
	done, _ := r.q.Complete(run.ID, "x", "success", "")
	r.svc.Finished(run, r.now, nil, done)
	r.expect()
}

func TestServiceRequiredModePostsTheRealGateAndPrefixedJobs(t *testing.T) {
	r := newSvcRig(t, testNaming(true))
	run := r.claimNew(quickPR(2, shaA))
	r.svc.Started(run)
	w := r.progress(run)
	w.Event(RunEvent{Kind: EventPlan, Total: 1})
	w.Event(jobEv(EventJobEnd, "lint", "lint", "success", "", r.now, r.now.Add(time.Second)))
	done, _ := r.q.Complete(run.ID, "x", "success", "")
	r.svc.Finished(run, r.now, nil, done)
	got := r.lines()
	if got[0] != "quick-gate pending: running" || !contains(got, "local/lint success: passed in 1s") {
		t.Fatalf("%v", got)
	}
	if h := r.svc.Health(); h == nil || h.Mode != "required" {
		t.Fatalf("health %+v", h)
	}
	var nilSvc *StatusService
	if nilSvc.Health() != nil {
		t.Fatal("a nil service has no health")
	}
	if h := newSvcRig(t, testNaming(false)).svc.Health(); h.Mode != "shadow" {
		t.Fatalf("mode %q", h.Mode)
	}
}

func TestServiceForgetPostsNothing(t *testing.T) {
	r := newSvcRig(t, testNaming(false))
	run := r.claimNew(quickPR(2, shaA))
	r.svc.Started(run)
	n := len(r.lines())
	r.svc.Forget(run)
	r.svc.ObserveQueue(r.q)
	if got := r.lines(); len(got) != n {
		t.Fatalf("%v", got)
	}
}

// ---------------------------------------------------------------- the agent posts

func TestAgentPostsTheLifeOfARequestAndAPostingFailureNeverFailsTheRun(t *testing.T) {
	fake := &FakeReporter{Fail: func(_ int, c StatusCall) error {
		if c.Name == "local/lint" { // every post of one job fails for good
			return &PostError{Err: errors.New("HTTP 422")}
		}
		return nil
	}}
	poster := &StatusPoster{Reporter: fake}
	defer poster.Close(time.Second)
	var rig *agentRig
	rig = newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) {
		w := &ProgressWriter{StateDir: rig.dir, ID: r.ID}
		w.Reset()
		w.Event(RunEvent{Kind: EventPlan, Total: 2})
		w.Event(jobEv(EventJobStart, "lint", "lint", "", "", time.Now(), time.Time{}))
		w.Event(jobEv(EventJobEnd, "lint", "lint", "success", "", time.Now(), time.Now()))
		w.Event(jobEv(EventJobStart, "test", "test", "", "", time.Now(), time.Time{}))
		time.Sleep(60 * time.Millisecond) // long enough for a tick to see the progress
		w.Event(jobEv(EventJobEnd, "test", "test", "success", "", time.Now(), time.Now()))
		return Outcome{RunID: "run-" + r.ID, Conclusion: "success"}, nil
	})
	rig.agent.Status = &StatusService{Poster: poster, Naming: testNaming(false), Dir: rig.dir}
	req := mustEnqueue(t, rig.q, quickPR(1, shaA))
	rig.start()
	waitFor(t, "the request to be done", func() bool { return rig.get(req.ID).State == StateDone })
	if got := rig.get(req.ID); got.Conclusion != "success" {
		t.Fatalf("a posting failure changed the run: %+v", got)
	}
	waitFor(t, "the last status", func() bool {
		poster.Flush(time.Second)
		cs := fake.Calls()
		return len(cs) > 0 && cs[len(cs)-1].State == StatusSuccess && cs[len(cs)-1].Name == "local/quick-gate"
	})
	var gate []string
	for _, c := range fake.Calls() {
		if c.Name == "local/quick-gate" {
			gate = append(gate, c.State)
		}
		if c.Name == "quick-gate" {
			t.Fatalf("shadow mode posted the required name: %+v", c)
		}
	}
	if len(gate) < 2 || gate[0] != StatusPending || gate[len(gate)-1] != StatusSuccess {
		t.Fatalf("gate states %v, want pending ... success", gate)
	}
	if h := poster.Health(); h.Failed == 0 || h.LastError == "" {
		t.Fatalf("the failed post must show in health: %+v", h)
	}
	waitFor(t, "the heartbeat to carry the status health", func() bool {
		hb, _ := ReadAgents(rig.dir)
		return len(hb) == 1 && hb[0].Status != nil && hb[0].Status.Mode == "shadow" && hb[0].Status.Failed > 0
	})
}

func TestAgentRunnerErrorPostsErrorAndSupersededPostsNothing(t *testing.T) {
	fake := &FakeReporter{}
	poster := &StatusPoster{Reporter: fake}
	defer poster.Close(time.Second)
	rig := newAgentRig(t, "a1", 1, func(context.Context, Request) (Outcome, error) { return Outcome{}, errors.New("child crashed") })
	rig.agent.Status = &StatusService{Poster: poster, Naming: testNaming(false), Dir: rig.dir}
	req := mustEnqueue(t, rig.q, quickPR(1, shaA))
	rig.start()
	waitFor(t, "done", func() bool { return rig.get(req.ID).State == StateDone })
	waitFor(t, "the error status", func() bool {
		poster.Flush(time.Second)
		cs := fake.Calls()
		return len(cs) > 0 && cs[len(cs)-1].State == StatusError
	})
	last := fake.Calls()[len(fake.Calls())-1]
	if !strings.Contains(last.Description, "child crashed") || last.Name != "local/quick-gate" {
		t.Fatalf("%+v", last)
	}
}

// ---------------------------------------------------------------- the progress writer

func TestProgressWriterLeavesASnapshotThatReadsBack(t *testing.T) {
	dir := t.TempDir()
	w := &ProgressWriter{StateDir: dir, ID: "r1"}
	w.Event(RunEvent{Kind: EventPlan, Total: 4})
	t0 := time.Now()
	w.Event(jobEv(EventJobStart, "a", "a", "", "", t0, time.Time{}))
	w.Event(jobEv(EventJobEnd, "a", "a", "failure", "step 2 go test", time.Time{}, t0.Add(time.Second)))
	p, err := ReadProgress(dir, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 4 || len(p.Jobs) != 1 || !p.Jobs[0].Ended() || p.Jobs[0].FailedStep != "step 2 go test" || p.Jobs[0].Start.IsZero() {
		t.Fatalf("%+v", p)
	}
	w.Reset()
	if p, _ := ReadProgress(dir, "r1"); p.Total != 0 || len(p.Jobs) != 0 {
		t.Fatalf("a reset starts empty: %+v", p)
	}
	if _, err := ReadProgress(dir, "../x"); err == nil {
		t.Fatal("a bad id is refused")
	}
	if p, err := ReadProgress(dir, "nothing-here"); err != nil || p.Total != 0 {
		t.Fatalf("a missing snapshot is empty: %+v %v", p, err)
	}
}

// ---------------------------------------------------------------- the core stays free of names

func TestStatusFilesHardCodeNoBranchOrContextNames(t *testing.T) {
	for _, f := range []string{"status.go", "status_service.go", "compare.go", "fallback.go", "progress.go"} {
		b, err := os.ReadFile(f) //nolint:gosec // the test lists its own package files
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, bad := range []string{`"develop"`, `"master"`, `"main"`, `"fast-build"`, `"build"`, `"fast-gate.yml"`, `"ci.yml"`} {
				if strings.Contains(line, bad) {
					t.Errorf("%s:%d hard-codes %s: %s", f, i+1, bad, strings.TrimSpace(line))
				}
			}
		}
	}
}
