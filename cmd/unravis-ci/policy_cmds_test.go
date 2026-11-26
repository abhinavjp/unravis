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

func policyState(t *testing.T, machines ...string) (state string, ids []string) {
	t.Helper()
	state = t.TempDir()
	for _, m := range machines {
		hb := localci.Heartbeat{Name: m, PID: os.Getpid(), Slots: 1, State: "ready", Started: time.Now(), LastSeen: time.Now(), Jobs: []localci.AgentJob{}}
		if err := writeHeartbeat(state, hb); err != nil {
			t.Fatal(err)
		}
	}
	q, err := localci.OpenQueue(localci.QueueDir(state))
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		r, _, err := q.Enqueue(localci.Request{Kind: localci.KindPR, Repo: "o/r", Ref: "f", Base: "develop", Number: n,
			SHA: strings.Repeat(string(rune('a'+n)), 40), Workflow: "fast-gate.yml"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	return state, ids
}

func TestPolicySetRefusesThenForces(t *testing.T) {
	state, ids := policyState(t, "m1", "m2")
	code, out, _ := cli(t, nil, "policy", "set", "o/r", "--only", "m1", "--state", state)
	if code != exitOK || !strings.Contains(out, "only these: m1") {
		t.Fatalf("only m1: %d\n%s", code, out)
	}
	code, _, e := cli(t, nil, "policy", "set", "o/r", "--only", "m9", "--state", state)
	if code != exitFailed || !strings.Contains(e, "2 queued runs would have no machine") || !strings.Contains(e, "--force") {
		t.Fatalf("only m9 strands both: %d\n%s", code, e)
	}
	// Nothing was written: show still says only m1.
	code, out, _ = cli(t, nil, "policy", "show", "o/r", "--state", state, "--json")
	var show policyShowJSON
	if code != exitOK || json.Unmarshal([]byte(out), &show) != nil || show.Policy.Mode != localci.PolicyOnly || len(show.Queued) != 2 {
		t.Fatalf("show: %d\n%s", code, out)
	}
	if show.Queued[0].Why.Code != localci.WaitReady {
		t.Fatalf("m1 is free: %+v", show.Queued[0].Why)
	}
	// Dry run with --json: the impact, exit 1 for the refusal, nothing written.
	code, out, _ = cli(t, nil, "policy", "set", "o/r", "--only", "m9", "--state", state, "--json", "--dry-run")
	var ch struct {
		localci.PolicyChange
		Line string `json:"line"`
	}
	if code != exitFailed || json.Unmarshal([]byte(out), &ch) != nil || !ch.Refused || strings.Join(ch.Impact.Stranded, ",") != strings.Join(ids, ",") {
		t.Fatalf("dry run json: %d\n%s", code, out)
	}
	code, out, _ = cli(t, nil, "policy", "set", "o/r", "--only", "m9", "--force", "--state", state)
	if code != exitOK || !strings.Contains(out, "forced") {
		t.Fatalf("forced: %d\n%s", code, out)
	}
	_, out, _ = cli(t, nil, "policy", "show", "o/r", "--state", state)
	if !strings.Contains(out, "only these: m9") || !strings.Contains(out, "are not allowed to run o/r") {
		t.Fatalf("show text after force:\n%s", out)
	}
	_, out, _ = cli(t, nil, "queue", "list", "--state-dir", state)
	if !strings.Contains(out, "waiting: the machines that fit") {
		t.Fatalf("queue list shows why:\n%s", out)
	}
	code, out, _ = cli(t, nil, "policy", "set", "o/r", "--any", "--state", state)
	if code != exitOK || !strings.Contains(out, "any machine") {
		t.Fatalf("back to any: %d\n%s", code, out)
	}
}

func TestPolicySetUsage(t *testing.T) {
	state := t.TempDir()
	for _, args := range [][]string{
		{"policy", "set", "o/r", "--state", state},
		{"policy", "set", "o/r", "--any", "--only", "m1", "--state", state},
		{"policy", "set", "not-a-repo", "--any", "--state", state},
		{"policy", "set", "o/r", "--only", "../x", "--state", state},
		{"policy", "show", "--state", state},
		{"policy"},
		{"policy", "drop"},
		{"machines", "allow", "m1", "--state", state},
		{"machines", "deny", "../x", "--project", "o/r", "--state", state},
		{"machines", "resume", "--state", state},
		{"machines", "zap"},
	} {
		if code, _, _ := cli(t, nil, args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
}

func TestMachinesAllowDenyAndList(t *testing.T) {
	state, _ := policyState(t, "m1", "m2")
	code, out, _ := cli(t, nil, "machines", "deny", "m1", "--project", "o/r", "--state", state)
	if code != exitOK || !strings.Contains(out, "any except these: m1") {
		t.Fatalf("deny m1: %d\n%s", code, out)
	}
	code, _, e := cli(t, nil, "machines", "deny", "m2", "--project", "o/r", "--state", state)
	if code != exitFailed || !strings.Contains(e, "would have no machine") {
		t.Fatalf("deny m2 is refused: %d\n%s", code, e)
	}
	code, out, _ = cli(t, nil, "machines", "deny", "m9", "--project", "o/r", "--state", state)
	if code != exitOK || !strings.Contains(out, "no machine named m9") {
		t.Fatalf("an unknown name is noted: %d\n%s", code, out)
	}
	code, out, _ = cli(t, nil, "machines", "--json", "--state", state)
	var ms machinesJSON
	if code != exitOK || json.Unmarshal([]byte(out), &ms) != nil || len(ms.Machines) != 2 {
		t.Fatalf("machines json: %d\n%s", code, out)
	}
	if ms.Machines[0].Name != "m1" || strings.Join(ms.Machines[0].Denied, ",") != "o/r" || len(ms.Machines[1].Denied) != 0 {
		t.Fatalf("denied per machine: %+v", ms.Machines)
	}
	code, out, _ = cli(t, nil, "machines", "allow", "m1", "--project", "o/r", "--state", state)
	if code != exitOK || !strings.Contains(out, "any except these: m9") {
		t.Fatalf("allow m1: %d\n%s", code, out)
	}
	_, out, _ = cli(t, nil, "machines", "list", "--state", state)
	if !strings.Contains(out, "m1") || strings.Contains(out, "denied for") {
		t.Fatalf("machines text:\n%s", out)
	}
}

func TestMachinesResume(t *testing.T) {
	state, _ := policyState(t, "m1")
	if code, out, _ := cli(t, nil, "machines", "resume", "m1", "--state", state); code != exitOK || !strings.Contains(out, "not draining") {
		t.Fatalf("not draining: %d\n%s", code, out)
	}
	if code, _, _ := cli(t, nil, "drain", "--state", state, "--name", "m1"); code != exitOK {
		t.Fatal("drain")
	}
	_, out, _ := cli(t, nil, "machines", "--state", state)
	if !strings.Contains(out, "drain asked") {
		t.Fatalf("the mark shows:\n%s", out)
	}
	code, out, _ := cli(t, nil, "machines", "resume", "m1", "--state", state, "--json")
	if code != exitOK || !strings.Contains(out, `"resumed"`) {
		t.Fatalf("resume: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(localci.AgentsDir(state), "m1.drain")); err == nil {
		t.Fatal("the mark must be gone")
	}
	// Ctrl-C drain: no mark, the heartbeat says draining.
	hb := localci.Heartbeat{Name: "m1", PID: os.Getpid(), Slots: 1, State: "draining", Started: time.Now(), LastSeen: time.Now(), Jobs: []localci.AgentJob{}}
	if err := writeHeartbeat(state, hb); err != nil {
		t.Fatal(err)
	}
	if code, _, e := cli(t, nil, "machines", "resume", "m1", "--state", state); code != exitFailed || !strings.Contains(e, "Ctrl-C") {
		t.Fatalf("a Ctrl-C drain: %d\n%s", code, e)
	}
	// A mark for a machine that has stopped.
	hb.State, hb.LastSeen = "ready", time.Now().Add(-time.Hour)
	if err := writeHeartbeat(state, hb); err != nil {
		t.Fatal(err)
	}
	if err := localci.RequestDrain(state, "m1"); err != nil {
		t.Fatal(err)
	}
	if code, _, e := cli(t, nil, "machines", "resume", "m1", "--state", state); code != exitFailed || !strings.Contains(e, "not running") {
		t.Fatalf("a stopped machine: %d\n%s", code, e)
	}
	if code, _, e := cli(t, nil, "machines", "resume", "m7", "--state", state); code != exitFailed || !strings.Contains(e, "no machine") {
		t.Fatalf("an unknown machine: %d\n%s", code, e)
	}
}
