package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/localci"
)

// safeBuf is a buffer that the serve goroutines and the test can use together.
type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(within)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func queueOf(t *testing.T, state string) *localci.Queue {
	t.Helper()
	q, err := localci.OpenQueue(localci.QueueDir(state))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestServeUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"argument":      {"serve", "extra"},
		"bad repo":      {"serve", "--repo", "norepo"},
		"bad name":      {"serve", "--name", "a b"},
		"zero slots":    {"serve", "--slots", "0"},
		"bad workflow":  {"serve", "--workflow-for", "develop"},
		"bad push":      {"serve", "--push-workflow", "=x.yml"},
		"bad var":       {"serve", "--var", "novalue"},
		"status args":   {"status", "extra"},
		"cancel none":   {"cancel"},
		"rerun none":    {"rerun"},
		"drain args":    {"drain", "extra"},
		"run-item none": {"run-item"},
	} {
		if code, _, _ := cli(t, nil, args...); code != exitUsage {
			t.Errorf("%s: exit %d, want %d", name, code, exitUsage)
		}
	}
}

func TestPauseReason(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "PAUSE")
	if got := pauseReason(pf, "", time.Now()); got != "" {
		t.Fatalf("no pause file: %q", got)
	}
	if err := os.WriteFile(pf, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := pauseReason(pf, "", time.Now()); !strings.Contains(got, "pause file") {
		t.Fatalf("pause file: %q", got)
	}
	_ = os.Remove(pf)
	// a release that is running: lock-info.json written now by this very process
	rel := t.TempDir()
	li := `{"started":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","pid":` + itoa(os.Getpid()) + `}`
	if err := os.WriteFile(filepath.Join(rel, "lock-info.json"), []byte(li), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := pauseReason(pf, rel, time.Now()); !strings.Contains(got, "release") {
		t.Fatalf("release running: %q", got)
	}
	if got := pauseReason(pf, t.TempDir(), time.Now()); got != "" {
		t.Fatalf("no release: %q", got)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestStatusCancelRerunDrain(t *testing.T) {
	state := filepath.Join(t.TempDir(), "store")
	if code, out, _ := cli(t, nil, "status", "--state", state); code != exitOK || !strings.Contains(out, "none") || !strings.Contains(out, "0 waiting") {
		t.Fatalf("empty status: %d\n%s", code, out)
	}
	if code, _, e := cli(t, nil, "drain", "--state", state); code != exitFailed || !strings.Contains(e, "no agent") {
		t.Fatalf("drain with no agent: %d %q", code, e)
	}
	q := queueOf(t, state)
	pr, _, err := q.Enqueue(localci.Request{Kind: localci.KindPR, Repo: "o/r", Number: 7, Ref: "feat", Base: "develop", SHA: "1111111111111111111111111111111111111111", Workflow: "fast-gate.yml"})
	if err != nil {
		t.Fatal(err)
	}
	push, _, err := q.Enqueue(localci.Request{Kind: localci.KindPush, Repo: "o/r", Ref: "develop", SHA: "2222222222222222222222222222222222222222", Workflow: "ci.yml"})
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ := cli(t, nil, "status", "--state", state)
	if code != exitOK || !strings.Contains(out, "2 waiting, 0 running") || !strings.Contains(out, pr.ID) || !strings.Contains(out, "pr 7 into develop") || !strings.Contains(out, "push develop") {
		t.Fatalf("status: %d\n%s", code, out)
	}
	if strings.Index(out, pr.ID) > strings.Index(out, push.ID) {
		t.Fatalf("a pull request must be listed before a push:\n%s", out)
	}

	// cancel by pull request number, and by id
	if code, out, _ := cli(t, nil, "cancel", "7", "--state", state); code != exitOK || !strings.Contains(out, pr.ID) {
		t.Fatalf("cancel 7: %d\n%s", code, out)
	}
	if code, _, e := cli(t, nil, "cancel", "7", "--state", state); code != exitFailed || !strings.Contains(e, "no queued or running") {
		t.Fatalf("cancel 7 again: %d %q", code, e)
	}
	if code, out, _ := cli(t, nil, "cancel", push.ID, "--state", state, "--reason", "not needed"); code != exitOK || !strings.Contains(out, "cancelled") {
		t.Fatalf("cancel id: %d\n%s", code, out)
	}
	if code, _, _ := cli(t, nil, "cancel", "q-nope", "--state", state); code == exitOK {
		t.Fatalf("cancel of a made-up id must fail")
	}
	got, err := q.Get(push.ID)
	if err != nil || got.State != localci.StateCancelled || got.Reason != "not needed" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, out, _ := cli(t, nil, "status", "--state", state); !strings.Contains(out, "0 waiting") || !strings.Contains(out, "cancelled") {
		t.Fatalf("status after cancel:\n%s", out)
	}

	// rerun a pull request by number (the host says it is open and where the head is)
	withHost(t, &fakeHost{pr: localci.PullRequest{Number: 7, State: "OPEN", BaseRef: "develop", HeadRef: "feat", HeadSHA: "3333333333333333333333333333333333333333"}})
	code, out, _ = cli(t, nil, "rerun", "7", "--state", state, "--repo", "o/r")
	if code != exitOK || !strings.Contains(out, "queued pr 7 into develop") || !strings.Contains(out, "fast-gate.yml") {
		t.Fatalf("rerun 7: %d\n%s", code, out)
	}
	if code, out, _ = cli(t, nil, "rerun", "7", "--state", state, "--repo", "o/r"); code != exitOK || !strings.Contains(out, "already queued") {
		t.Fatalf("rerun 7 twice must not add a second request: %d\n%s", code, out)
	}
	withHost(t, &fakeHost{pr: localci.PullRequest{Number: 8, State: "CLOSED", BaseRef: "develop"}})
	if code, _, e := cli(t, nil, "rerun", "8", "--state", state, "--repo", "o/r"); code != exitFailed || !strings.Contains(e, "not open") {
		t.Fatalf("rerun of a closed pull request: %d %q", code, e)
	}
	withHost(t, &fakeHost{pr: localci.PullRequest{Number: 9, State: "OPEN", BaseRef: "release"}})
	if code, _, e := cli(t, nil, "rerun", "9", "--state", state, "--repo", "o/r"); code != exitUsage || !strings.Contains(e, "release") {
		t.Fatalf("rerun with no workflow for the base: %d %q", code, e)
	}

	// rerun from a recorded run id
	store, err := localci.OpenStore(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendIndex(localci.IndexEntry{RunID: "20260101-000000-push-develop-ci", Workflow: "ci.yml", Event: "push", Repo: "o/r", HeadRef: "develop", HeadSHA: "4444444444444444444444444444444444444444", Conclusion: "failure"}, nil); err != nil {
		t.Fatal(err)
	}
	code, out, _ = cli(t, nil, "rerun", "20260101-000000-push-develop-ci", "--state", state)
	if code != exitOK || !strings.Contains(out, "queued push develop") {
		t.Fatalf("rerun run id: %d\n%s", code, out)
	}
	if code, _, e := cli(t, nil, "rerun", "20260101-nothing", "--state", state); code != exitFailed || !strings.Contains(e, "no run") {
		t.Fatalf("rerun of an unknown run: %d %q", code, e)
	}

	// drain: only an agent that is alive gets the mark
	hb := localci.Heartbeat{Name: "a1", PID: os.Getpid(), Slots: 2, State: "ready", Started: time.Now(), LastSeen: time.Now(), Jobs: []localci.AgentJob{}}
	if err := writeHeartbeat(state, hb); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := cli(t, nil, "status", "--state", state); !strings.Contains(out, "a1") || !strings.Contains(out, "ready") {
		t.Fatalf("status with an agent:\n%s", out)
	}
	if code, out, _ := cli(t, nil, "drain", "--state", state); code != exitOK || !strings.Contains(out, "a1") {
		t.Fatalf("drain: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(localci.AgentsDir(state), "a1.drain")); err != nil {
		t.Fatalf("the drain mark was not made: %v", err)
	}
	if code, _, _ := cli(t, nil, "drain", "--state", state, "--name", "../x"); code != exitUsage {
		t.Fatalf("a drain name with a path must be refused")
	}
}

// writeHeartbeat leaves a heartbeat file the way an agent does.
func writeHeartbeat(state string, hb localci.Heartbeat) error {
	if err := os.MkdirAll(localci.AgentsDir(state), 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(localci.AgentsDir(state), hb.Name+".json"), mustJSON(hb), 0o600)
}

func TestRunItemByHandAndAsChild(t *testing.T) {
	bare, clean, _ := prSetup(t)
	state := filepath.Join(t.TempDir(), "store")
	withHost(t, &fakeHost{url: bare, pr: localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "acme", HeadRepoOwner: "acme"}})
	q := queueOf(t, state)
	r, _, err := q.Enqueue(localci.Request{Kind: localci.KindPR, Repo: "acme/widgets", Number: 1, Ref: "feature", Base: "develop", SHA: clean, Workflow: "fast-gate.yml"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _, e := cli(t, nil, "run-item", "q-missing", "--state", state); code != exitUsage || !strings.Contains(e, "no such request") {
		t.Fatalf("unknown id: %d %q", code, e)
	}
	// by hand: runs and prints the result, leaves no outcome file and the request where it was
	code, out, errOut := cli(t, nil, "run-item", r.ID, "--state", state, "--origin", bare)
	if code != exitOK || !strings.Contains(out, "success") || !strings.Contains(out, "run id:") {
		t.Fatalf("by hand: %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(localci.OutcomePath(state, r.ID)); err == nil {
		t.Fatalf("a hand run must not leave an outcome file")
	}
	// as a child: the outcome file is left for the agent; stdin held open until the end
	pr, pw := io.Pipe()
	old := childStdin
	childStdin = pr
	t.Cleanup(func() { childStdin = old })
	code, out, errOut = cli(t, nil, "run-item", r.ID, "--state", state, "--origin", bare, "--child")
	_ = pw.Close()
	if code != exitOK {
		t.Fatalf("child: %d\n%s\n%s", code, out, errOut)
	}
	b, err := os.ReadFile(localci.OutcomePath(state, r.ID))
	if err != nil || !strings.Contains(string(b), `"conclusion": "success"`) {
		t.Fatalf("outcome: %v %s", err, b)
	}
}

func TestRunItemChildStopsWhenStdinCloses(t *testing.T) {
	bare, clean, _ := prSetup(t)
	state := filepath.Join(t.TempDir(), "store")
	withHost(t, &fakeHost{url: bare, pr: localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "acme", HeadRepoOwner: "acme"}})
	r, _, err := queueOf(t, state).Enqueue(localci.Request{Kind: localci.KindPR, Repo: "acme/widgets", Number: 1, Ref: "feature", Base: "develop", SHA: clean, Workflow: "fast-gate.yml"})
	if err != nil {
		t.Fatal(err)
	}
	old := childStdin
	childStdin = strings.NewReader("") // closed at once: the agent wants it stopped
	t.Cleanup(func() { childStdin = old })
	code, out, _ := cli(t, nil, "run-item", r.ID, "--state", state, "--origin", bare, "--child")
	if code == exitOK && strings.Contains(out, ": success") {
		t.Fatalf("a child whose stdin is closed must not run to success:\n%s", out)
	}
	if _, err := os.Stat(localci.OutcomePath(state, r.ID)); err != nil {
		t.Fatalf("the child must still leave an outcome: %v", err)
	}
}

// TestServeRunsAPullRequestEndToEnd runs the real serve command against a local origin: it starts paused (the
// pause file), finds the open pull request, queues it, and runs it once the file is gone; Ctrl-C (the context)
// drains it.
func TestServeRunsAPullRequestEndToEnd(t *testing.T) {
	bare, clean, _ := prSetup(t)
	state := filepath.Join(t.TempDir(), "store")
	pr := localci.PullRequest{Number: 1, State: "OPEN", BaseRef: "develop", HeadRef: "feature", HeadSHA: clean, Author: "acme", HeadRepoOwner: "acme"}
	withHost(t, &fakeHost{url: bare, pr: pr, list: []localci.PullRequest{pr}})
	if err := os.MkdirAll(state, 0o750); err != nil {
		t.Fatal(err)
	}
	pauseFile := filepath.Join(state, pauseFileName)
	if err := os.WriteFile(pauseFile, []byte("paused\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var so, se safeBuf
	done := make(chan int, 1)
	go func() {
		done <- realMain(ctx, []string{"serve", "--repo", "acme/widgets", "--state", state, "--origin", bare, "--in-process", "--no-push",
			"--no-keep-awake", "--no-release-pause", "--interval", "100ms", "--name", "t1"}, &so, &se, os.Environ())
	}()

	waitFor(t, "the pull request to be queued", 30*time.Second, func() bool {
		return strings.Contains(so.String(), "queued pr 1 into develop")
	})
	waitFor(t, "the pause to be noticed", 30*time.Second, func() bool { return strings.Contains(so.String(), "paused: the pause file") })
	time.Sleep(2500 * time.Millisecond) // longer than a claim interval: a paused agent takes nothing
	if q, r := queueOf(t, state).Depth(); q != 1 || r != 0 {
		t.Fatalf("paused: %d queued %d running, want 1 and 0\n%s", q, r, so.String())
	}
	if _, out, _ := cli(t, nil, "status", "--state", state); !strings.Contains(out, "claiming is paused") || !strings.Contains(out, "t1") || !strings.Contains(out, "paused") {
		t.Fatalf("status while paused:\n%s", out)
	}

	if err := os.Remove(pauseFile); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to finish", 90*time.Second, func() bool {
		es, _ := (&localci.Store{Dir: state}).ReadIndex()
		return len(es) == 1 && es[0].Conclusion == "success" && es[0].PR == 1
	})
	waitFor(t, "the request to be done", 10*time.Second, func() bool {
		rs, _ := queueOf(t, state).List()
		return len(rs) == 1 && rs[0].State == localci.StateDone && rs[0].Conclusion == "success" && rs[0].RunID != ""
	})
	// a restart does not run the same head again: the run index says it was handled (catch-up skips it)
	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("serve exit %d\n%s\n%s", code, so.String(), se.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("serve did not stop after the context ended\n%s", so.String())
	}
	if _, err := os.Stat(filepath.Join(localci.AgentsDir(state), "t1.json")); err == nil {
		t.Fatalf("the heartbeat file must be removed when the agent stops")
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	var so2 safeBuf
	done2 := make(chan int, 1)
	go func() {
		done2 <- realMain(ctx2, []string{"serve", "--repo", "acme/widgets", "--state", state, "--origin", bare, "--in-process", "--no-push",
			"--no-keep-awake", "--no-release-pause", "--interval", "100ms", "--name", "t1"}, &so2, io.Discard, os.Environ())
	}()
	time.Sleep(1500 * time.Millisecond)
	cancel2()
	<-done2
	if strings.Contains(so2.String(), "queued pr 1") {
		t.Fatalf("a head that already has a result must not be queued again on restart:\n%s", so2.String())
	}
}

func TestTrustCfgArgvReachesTheChild(t *testing.T) {
	got := trustCfg{protect: []string{"a/b"}, trusted: []string{"develop", "master"}, allowEnv: []string{"MY_TOKEN"}}.argv()
	want := []string{"--protect", "a/b", "--trusted-base", "develop", "--trusted-base", "master", "--allow-env", "MY_TOKEN"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
	// and run-item accepts every flag the agent hands a child
	if code, _, e := cli(t, nil, append([]string{"run-item", "q-none", "--state", t.TempDir()}, got...)...); code != exitUsage || strings.Contains(e, "flag provided but not defined") {
		t.Fatalf("run-item must know the flags of a child: %d %s", code, e)
	}
}

func TestRunKeepDurationIsCutAtAHundredYearsInsteadOfWrappingAround(t *testing.T) {
	if got := runKeepDuration(213504); got != time.Duration(maxRunKeepDays)*24*time.Hour {
		t.Fatalf("213504 days gave %v", got)
	}
	if got := runKeepDuration(7); got != 7*24*time.Hour {
		t.Fatalf("7 days gave %v", got)
	}
	if runKeepDuration(-1) >= 0 || runKeepDuration(0) != 0 {
		t.Fatal("-1 must stay negative and 0 the default")
	}
}
