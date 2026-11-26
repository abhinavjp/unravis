package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/localci"
)

// seedQueue puts a pull request, a push and a second pull request in the queue of a new state folder, in that
// order, and returns the folder and the three requests.
func seedQueue(t *testing.T) (dir string, q *localci.Queue, reqs [3]localci.Request) {
	t.Helper()
	dir = t.TempDir()
	q, err := localci.OpenQueue(localci.QueueDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range []localci.Request{
		{Kind: localci.KindPR, Repo: "o/r", Ref: "f1", Base: "develop", Number: 11, SHA: "aaaa1111", Workflow: "fast-gate.yml"},
		{Kind: localci.KindPush, Repo: "o/r", Ref: "develop", SHA: "bbbb2222", Workflow: "ci.yml"},
		{Kind: localci.KindPR, Repo: "o/r", Ref: "f2", Base: "develop", Number: 22, SHA: "cccc3333", Workflow: "fast-gate.yml"},
	} {
		got, added, err := q.Enqueue(r)
		if err != nil || !added {
			t.Fatalf("enqueue %d: %v %v", i, added, err)
		}
		reqs[i] = got
	}
	return dir, q, reqs
}

func TestQueueTopByIDAndByPullRequestNumberPutsItFirstInTheListing(t *testing.T) {
	dir, _, reqs := seedQueue(t)
	code, out, errOut := cli(t, nil, "queue", "top", reqs[1].ID, "--state-dir", dir)
	if code != exitOK || !strings.Contains(out, reqs[1].ID) || !strings.Contains(out, "first in line") {
		t.Fatalf("top by id: %d %q %q", code, out, errOut)
	}
	code, out, errOut = cli(t, nil, "queue", "top", "22", "--state", dir)
	if code != exitOK || !strings.Contains(out, reqs[2].ID) {
		t.Fatalf("top by pr number (with the --state alias): %d %q %q", code, out, errOut)
	}
	code, out, _ = cli(t, nil, "queue", "list", "--state-dir", dir, "--json")
	var got queueListJSON
	if code != exitOK || json.Unmarshal([]byte(out), &got) != nil {
		t.Fatalf("list --json: %d %q", code, out)
	}
	if len(got.Requests) != 3 || got.Requests[0].ID != reqs[2].ID || got.Requests[1].ID != reqs[1].ID || got.Requests[2].ID != reqs[0].ID {
		var ids []string
		for _, r := range got.Requests {
			ids = append(ids, r.ID)
		}
		t.Fatalf("order after top 1 then 2: %v", ids)
	}
}

func TestQueueTopRefusesARunningFinishedOrUnknownRequestWithAClearMessage(t *testing.T) {
	dir, q, reqs := seedQueue(t)
	if c, err := q.Claim("a", nil); err != nil || c == nil || c.ID != reqs[0].ID {
		t.Fatalf("setup claim: %+v %v", c, err)
	}
	if _, err := q.Cancel(reqs[1].ID, "test"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ arg, want string }{
		{reqs[0].ID, "is running"},
		{"11", "is running"},
		{reqs[1].ID, "already ended"},
		{"q-20260101-000000-000-000000", "no such request"},
		{"99", "no waiting request"},
	} {
		code, out, errOut := cli(t, nil, "queue", "top", c.arg, "--state-dir", dir)
		if code != exitFailed || out != "" || !strings.Contains(errOut, c.want) {
			t.Errorf("queue top %s: code %d out %q err %q, want exit 1 and %q", c.arg, code, out, errOut, c.want)
		}
	}
	if code, _, _ := cli(t, nil, "queue", "top", "--state-dir", dir); code != exitUsage {
		t.Fatalf("no argument: exit %d, want usage", code)
	}
}

func TestQueuePauseAndResumeUseThePauseFileTheAgentWatches(t *testing.T) {
	dir := t.TempDir()
	if code, out, errOut := cli(t, nil, "queue", "pause", "--state-dir", dir); code != exitOK || !strings.Contains(out, "paused") {
		t.Fatalf("pause: %d %q %q", code, out, errOut)
	}
	if !localci.IsPaused(dir) {
		t.Fatal("no PAUSE file after queue pause")
	}
	if _, err := os.Stat(filepath.Join(dir, pauseFileName)); err != nil {
		t.Fatalf("the file serve looks at is missing: %v", err)
	}
	// serve's own check says paused, with the file as the reason
	if why := pauseReason(filepath.Join(dir, pauseFileName), "", time.Now()); why == "" {
		t.Fatal("serve's pauseReason does not see the file queue pause made")
	}
	if code, out, _ := cli(t, nil, "queue", "pause", "--state-dir", dir); code != exitOK || !strings.Contains(out, "already paused") {
		t.Fatalf("pause twice: %d %q", code, out)
	}
	_, out, _ := cli(t, nil, "status", "--state", dir, "--no-release-pause", "--json")
	var st statusJSON
	if err := json.Unmarshal([]byte(out), &st); err != nil || !st.Paused || st.PausedReason == "" {
		t.Fatalf("status --json does not say paused: %v %+v", err, st)
	}
	if code, out, errOut := cli(t, nil, "queue", "resume", "--state-dir", dir); code != exitOK || !strings.Contains(out, "resumed") {
		t.Fatalf("resume: %d %q %q", code, out, errOut)
	}
	if localci.IsPaused(dir) {
		t.Fatal("still paused after queue resume")
	}
	if code, out, _ := cli(t, nil, "queue", "resume", "--state-dir", dir); code != exitOK || !strings.Contains(out, "not paused") {
		t.Fatalf("resume twice: %d %q", code, out)
	}
}

func TestQueueListTextAndFiltersAndTheStateFlagMixUp(t *testing.T) {
	dir, q, reqs := seedQueue(t)
	if _, err := q.Claim("a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Cancel(reqs[2].ID, "test"); err != nil {
		t.Fatal(err)
	}
	code, out, _ := cli(t, nil, "queue", "list", "--state-dir", dir)
	if code != exitOK || !strings.Contains(out, "queue: 1 waiting, 1 running") || !strings.Contains(out, reqs[0].ID) || strings.Contains(out, reqs[2].ID) {
		t.Fatalf("default listing shows the active requests only: %d %q", code, out)
	}
	for filter, want := range map[string][]string{"queued": {reqs[1].ID}, "running": {reqs[0].ID}, "cancelled": {reqs[2].ID}, "all": {reqs[0].ID, reqs[1].ID, reqs[2].ID}} {
		_, out, _ := cli(t, nil, "queue", "list", "--state-dir", dir, "--state", filter, "--json")
		var got queueListJSON
		if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Requests) != len(want) || got.Filter != filter {
			t.Fatalf("--state %s: %v %q", filter, err, out)
		}
	}
	code, _, errOut := cli(t, nil, "queue", "list", "--state", dir)
	if code != exitUsage || !strings.Contains(errOut, "--state-dir") {
		t.Fatalf("a folder given as --state must say to use --state-dir: %d %q", code, errOut)
	}
}

func TestQueueListJSONOfAnEmptyOrMissingFolderIsAnEmptyList(t *testing.T) {
	_, out, _ := cli(t, nil, "queue", "list", "--json", "--state-dir", filepath.Join(t.TempDir(), "none"))
	var got queueListJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Requests == nil || len(got.Requests) != 0 || got.Paused {
		t.Fatalf("%v %+v", err, got)
	}
	if !strings.Contains(out, `"requests": []`) {
		t.Fatalf("an empty list must print as [] and not null: %s", out)
	}
}

func TestStatusJSONCarriesTheFieldsOfTheTextOutput(t *testing.T) {
	dir, q, reqs := seedQueue(t)
	if _, err := q.Claim("alpha", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Cancel(reqs[1].ID, "by test"); err != nil {
		t.Fatal(err)
	}
	hb := localci.Heartbeat{Name: "alpha", PID: 4242, Slots: 2, Labels: []string{"gpu"}, State: "ready", LastSeen: time.Now(),
		Jobs: []localci.AgentJob{{ID: reqs[0].ID, Label: "pr 11"}}, Status: &localci.StatusHealth{Mode: "shadow", Posted: 3}}
	if err := os.MkdirAll(localci.AgentsDir(dir), 0o750); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(hb)
	if err := os.WriteFile(filepath.Join(localci.AgentsDir(dir), "alpha.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	old := hb
	old.Name, old.LastSeen = "beta", time.Now().Add(-time.Hour)
	b, _ = json.Marshal(old)
	if err := os.WriteFile(filepath.Join(localci.AgentsDir(dir), "beta.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	code, text, _ := cli(t, nil, "status", "--state", dir, "--no-release-pause")
	if code != exitOK {
		t.Fatal(code)
	}
	code, out, errOut := cli(t, nil, "status", "--state", dir, "--no-release-pause", "--json")
	if code != exitOK || errOut != "" {
		t.Fatalf("status --json: %d %q", code, errOut)
	}
	var st statusJSON
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if st.StateDir != dir || st.Paused || len(st.Agents) != 2 || st.Queue.Waiting != 1 || st.Queue.Running != 1 || len(st.Queue.Requests) != 2 || len(st.Finished) != 1 {
		t.Fatalf("status json: %+v", st)
	}
	a, g := st.Agents[0], st.Agents[1]
	if a.Name != "alpha" || a.State != "ready" || a.Slots != 2 || a.Running != 1 || a.PID != 4242 || len(a.Labels) != 1 || a.Statuses == nil || a.Statuses.Posted != 3 {
		t.Fatalf("agent: %+v", a)
	}
	if g.Name != "beta" || g.State != "gone" {
		t.Fatalf("a stale heartbeat is gone, as in the text: %+v", g)
	}
	for _, r := range append(append([]localci.Request{}, st.Queue.Requests...), st.Finished...) {
		if !strings.Contains(text, r.ID) {
			t.Errorf("the text output lacks %s that the JSON has", r.ID)
		}
	}
	if st.Queue.Requests[0].State != localci.StateRunning || st.Finished[0].State != localci.StateCancelled {
		t.Fatalf("states: %+v / %+v", st.Queue.Requests, st.Finished)
	}
	// --recent 0 leaves out the finished ones, as in the text
	_, out, _ = cli(t, nil, "status", "--state", dir, "--no-release-pause", "--json", "--recent", "0")
	if err := json.Unmarshal([]byte(out), &st); err != nil || len(st.Finished) != 0 || !strings.Contains(out, `"finished": []`) {
		t.Fatalf("--recent 0: %v %s", err, out)
	}
}

func TestStatusJSONOfAnEmptyFolderHasEmptyListsNotNull(t *testing.T) {
	_, out, _ := cli(t, nil, "status", "--state", t.TempDir(), "--no-release-pause", "--json")
	for _, want := range []string{`"agents": []`, `"requests": []`, `"finished": []`, `"paused": false`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}

func TestRunsJSONHasTheFieldsOfTheTableAndTheLimit(t *testing.T) {
	dir := t.TempDir()
	store, err := localci.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	for i, e := range []localci.IndexEntry{
		{RunID: "run-a", Workflow: "ci.yml", Event: "push", Conclusion: "success", Start: start, End: start.Add(90 * time.Second)},
		{RunID: "run-b", Workflow: "fast-gate.yml", Event: "pull_request", PR: 7, Conclusion: "failure", Reason: "job build failed", Start: start, End: start.Add(5 * time.Second)},
		{RunID: "run-c", Workflow: "ci.yml", Event: "push", Conclusion: "error", Reason: "no checkout"},
	} {
		if err := store.AppendIndex(e, nil); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	code, out, errOut := cli(t, nil, "runs", "--state", dir, "--json")
	var rows []map[string]any
	if code != exitOK || errOut != "" || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 3 {
		t.Fatalf("runs --json: %d %q %q", code, out, errOut)
	}
	b := rows[1]
	if b["run_id"] != "run-b" || b["conclusion"] != "failure" || b["pr"] != float64(7) || b["workflow"] != "fast-gate.yml" || b["reason"] != "job build failed" || b["duration_seconds"] != float64(5) {
		t.Fatalf("row b: %v", b)
	}
	if rows[0]["duration_seconds"] != float64(90) || rows[2]["duration_seconds"] != float64(0) {
		t.Fatalf("durations: %v %v", rows[0], rows[2])
	}
	_, out, _ = cli(t, nil, "runs", "--state", dir, "--json", "--limit", "1")
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0]["run_id"] != "run-c" {
		t.Fatalf("--limit 1 keeps the latest: %v %s", err, out)
	}
	_, out, _ = cli(t, nil, "runs", "--state", t.TempDir(), "--json")
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("no runs is an empty array, got %q", out)
	}
	_, out, _ = cli(t, nil, "runs", "--state", dir)
	if !strings.Contains(out, "run-b") || !strings.Contains(out, "#7") {
		t.Fatalf("the table output changed: %q", out)
	}
}

func TestReservedExitCodesAreDistinctAndUnused(t *testing.T) {
	codes := map[int]string{exitOK: "ok", exitFailed: "failed", exitUsage: "usage", exitUnsupported: "unsupported", exitStopped: "stopped", exitHeld: "held"}
	if len(codes) != 6 || exitStopped != 4 || exitHeld != 5 {
		t.Fatalf("exit codes collide or moved: %v", codes)
	}
	var out strings.Builder
	usage(&out)
	if !strings.Contains(out.String(), "4 stopped and 5 held are reserved") {
		t.Fatal("the usage text must say 4 and 5 are reserved")
	}
}
