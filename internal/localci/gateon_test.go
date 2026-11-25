package localci

import (
	"context"
	"os"
	"strings"
	"testing"
)

// BL-586d (Blocker R in docs/ci/CUTOVER.md): after the cutover GitHub no longer starts fast-gate.yml, so its on:
// is workflow_dispatch alone. The base-branch map, not on:, says which workflow serves which base, and these tests
// pin both halves: a configured gate runs whatever its on: says, and nothing else about trust got weaker.

func gateWF(on string) string {
	return "name: Fast gate\n" + on + `jobs:
  go:
    runs-on: ubuntu-24.04
    steps:
      - name: Say
        run: |
          echo "workflow=base event=${{ github.event_name }} marker=$(cat marker.txt)"
`
}

const dispatchOnlyOn = "on:\n  workflow_dispatch:\n"

// commitWorkflowT commits file on a branch of the origin and pushes it; the new tip is returned.
func commitWorkflowT(t *testing.T, o *origin, branch, file, content string) string {
	t.Helper()
	gitT(t, o.work, "checkout", "-q", branch)
	writeT(t, o.work, ".github/workflows/"+file, content)
	sha := commitT(t, o.work, "workflow "+file)
	gitT(t, o.work, "push", "-q", "origin", branch)
	return sha
}

// triggers are every shape of on: a configured gate may have; none of them is a reason to refuse it.
var gateTriggers = map[string]string{
	"workflow_dispatch only": dispatchOnlyOn,
	"pull_request only":      "on:\n  pull_request:\n",
	"push only":              "on:\n  push:\n",
	"schedule only":          "on:\n  schedule:\n    - cron: '0 3 * * *'\n",
	"empty on":               "on: {}\n",
	"no on key":              "",
}

func TestRunPRConfiguredGateRunsWhateverItsOnSays(t *testing.T) {
	needBash(t)
	for name, on := range gateTriggers {
		t.Run(name, func(t *testing.T) {
			o := newOrigin(t)
			commitWorkflowT(t, o, "develop", "fast-gate.yml", gateWF(on))
			h := &fakeHost{pr: okPR(o, 1, o.feature, "acme")}
			opts, console := prOptions(t, o, h)
			res, err := RunPR(context.Background(), opts)
			if err != nil {
				t.Fatalf("%v\n%s", err, console.String())
			}
			if res.Run.Conclusion != "success" {
				t.Fatalf("%+v\n%s", res.Run, console.String())
			}
			// the steps still see a pull request: the gate is written for the merge commit of one
			if log := logOf(t, opts.Store, res.RunID, "go"); !strings.Contains(log, "event=pull_request") || !strings.Contains(log, "marker=feature") {
				t.Fatalf("the run is a pull_request run on the merge:\n%s", log)
			}
			if res.Entry.Event != "pull_request" || res.Entry.PR != 1 || res.Entry.Workflow != "fast-gate.yml" {
				t.Fatalf("%+v", res.Entry)
			}
		})
	}
}

// A collaborator's pull request still runs the base branch's definition, which is now dispatch-only.
func TestRunPRCollaboratorRunsADispatchOnlyBaseDefinition(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	// the base workflow with only its on: changed, so the pull request's own edit of the echo line still merges cleanly
	dispatchOnlyBase := strings.Replace(baseWorkflow, "on:\n  pull_request:\n    branches: [develop]\n", dispatchOnlyOn, 1)
	if dispatchOnlyBase == baseWorkflow {
		t.Fatal("the fixture changed: the on: block is not where this test expects it")
	}
	commitWorkflowT(t, o, "develop", "fast-gate.yml", dispatchOnlyBase)
	h := &fakeHost{pr: okPR(o, 3, o.evil, "Alice"), collabs: []string{"alice"}}
	opts, console := prOptions(t, o, h)
	res, err := RunPR(context.Background(), opts)
	if err != nil {
		t.Fatalf("%v\n%s", err, console.String())
	}
	log := logOf(t, opts.Store, res.RunID, "go")
	if res.Run.Conclusion != "success" || !strings.Contains(log, "workflow=base") || strings.Contains(log, "workflow=PR") {
		t.Fatalf("a collaborator's pull request must run the base definition:\n%s", log)
	}
}

// The relaxed on: check must not have loosened who may run: a stranger and a fork never get a checkout.
func TestRunPRStrangerAndForkStillRefusedWithADispatchOnlyGate(t *testing.T) {
	o := newOrigin(t)
	commitWorkflowT(t, o, "develop", "fast-gate.yml", gateWF(dispatchOnlyOn))
	for name, tc := range map[string]struct {
		author, headOwner string
		fork              bool
		want              string
	}{
		"stranger": {"mallory", "acme", false, "mallory"},
		"fork":     {"acme", "mallory", true, "fork"},
	} {
		t.Run(name, func(t *testing.T) {
			pr := okPR(o, 3, o.evil, tc.author)
			pr.HeadRepoOwner, pr.IsFork = tc.headOwner, tc.fork
			h := &fakeHost{pr: pr, collabs: []string{"alice"}}
			opts, _ := prOptions(t, o, h)
			res, err := RunPR(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if res.Run.Conclusion != "failure" || !strings.Contains(res.Entry.Reason, tc.want) {
				t.Fatalf("%+v", res.Entry)
			}
			if exists(opts.Store.RepoPath()) {
				t.Fatal("nothing of a refused pull request may be fetched")
			}
			if entries, _ := os.ReadDir(opts.Store.WorkPath()); len(entries) != 0 {
				t.Fatalf("a refused pull request got a worktree: %v", entries)
			}
		})
	}
}

// A base the map does not name, and a workflow name that is not a plain file name, are refused before anything
// is fetched or recorded, whatever the workflow's on: says.
func TestRunPRWorkflowNotNamedByTheMapIsRefused(t *testing.T) {
	o := newOrigin(t)
	for name, tc := range map[string]struct {
		workflows map[string]string
		workflow  string
		want      string
	}{
		"base not in the map":         {map[string]string{"master": "fast-gate.yml"}, "", "no workflow is set for pull requests into \"develop\""},
		"empty map":                   {map[string]string{}, "", "no workflow is set"},
		"map entry is a path":         {map[string]string{"develop": ".github/workflows/fast-gate.yml"}, "", "must be a file name"},
		"map entry climbs out":        {map[string]string{"develop": "../fast-gate.yml"}, "", "must be a file name"},
		"operator workflow is a path": {map[string]string{}, "sub/fast-gate.yml", "must be a file name"},
	} {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{pr: okPR(o, 1, o.feature, "acme")}
			opts, _ := prOptions(t, o, h)
			opts.Workflows, opts.Workflow = tc.workflows, tc.workflow
			res, err := RunPR(context.Background(), opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v (result %+v)", err, res)
			}
			if exists(opts.Store.RepoPath()) {
				t.Fatal("a refused workflow fetches nothing")
			}
			if idx, _ := opts.Store.ReadIndex(); len(idx) != 0 {
				t.Fatalf("a refusal before the run leaves no record: %+v", idx)
			}
		})
	}
}

// The named workflow still has to exist at the commit that runs: a map entry for a file that is not there fails
// as it always did.
func TestRunPRNamedWorkflowMissingFromTheCommitFails(t *testing.T) {
	o := newOrigin(t)
	h := &fakeHost{pr: okPR(o, 1, o.feature, "acme")}
	opts, _ := prOptions(t, o, h)
	opts.Workflows = map[string]string{"develop": "not-there.yml"}
	res, err := RunPR(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "not-there.yml is not in") {
		t.Fatalf("err = %v", err)
	}
	if res == nil || res.Run == nil || res.Run.Conclusion != "error" {
		t.Fatalf("%+v", res)
	}
}

func refOpts(o *origin, branch, sha, workflow, event string) RefOptions {
	return RefOptions{Repo: "acme/widgets", Ref: branch, SHA: sha, Workflow: workflow, Event: event, OriginURL: o.bare}
}

func TestRunRefPushConfiguredGateRunsWhateverItsOnSays(t *testing.T) {
	needBash(t)
	for name, on := range gateTriggers {
		t.Run(name, func(t *testing.T) {
			o := newOrigin(t)
			sha := commitWorkflowT(t, o, "develop", "fast-gate.yml", gateWF(on))
			ro := refOpts(o, "develop", sha, "fast-gate.yml", "push")
			ro.Store = newStore(t)
			res, err := RunRef(context.Background(), ro)
			if err != nil {
				t.Fatal(err)
			}
			if res.Run.Conclusion != "success" {
				t.Fatalf("%+v", res.Run)
			}
			if log := logOf(t, ro.Store, res.RunID, "go"); !strings.Contains(log, "event=push") {
				t.Fatalf("the run is a push run:\n%s", log)
			}
			if res.Entry.Event != "push" || res.Entry.HeadSHA != sha || res.Entry.HeadRef != "develop" {
				t.Fatalf("%+v", res.Entry)
			}
		})
	}
}

// A schedule run is not a gate: the schedule trigger says when it fires, so a workflow without one is refused.
func TestRunRefScheduleStillNeedsASchedule(t *testing.T) {
	o := newOrigin(t)
	sha := commitWorkflowT(t, o, "develop", "fast-gate.yml", gateWF(dispatchOnlyOn))
	ro := refOpts(o, "develop", sha, "fast-gate.yml", "schedule")
	ro.Store = newStore(t)
	res, err := RunRef(context.Background(), ro)
	if err == nil || !strings.Contains(err.Error(), "does not run on schedule") {
		t.Fatalf("err = %v", err)
	}
	if res == nil || res.Run == nil || res.Run.Conclusion != "error" {
		t.Fatalf("%+v", res)
	}
}

// A workflow that has the schedule trigger runs on schedule.
func TestRunRefScheduleWithTheTriggerRuns(t *testing.T) {
	needBash(t)
	o := newOrigin(t)
	sha := commitWorkflowT(t, o, "develop", "fast-gate.yml", gateWF("on:\n  schedule:\n    - cron: '0 3 * * *'\n"))
	ro := refOpts(o, "develop", sha, "fast-gate.yml", "schedule")
	ro.Store = newStore(t)
	if res, err := RunRef(context.Background(), ro); err != nil || res.Run.Conclusion != "success" {
		t.Fatalf("%v %+v", err, res)
	}
}

// A push run takes only a plain file name and a real commit id, as before.
func TestRunRefPushStillRefusesBadArguments(t *testing.T) {
	o := newOrigin(t)
	for name, mut := range map[string]func(*RefOptions){
		"path workflow": func(r *RefOptions) { r.Workflow = "sub/fast-gate.yml" },
		"dots":          func(r *RefOptions) { r.Workflow = ".." },
		"no workflow":   func(r *RefOptions) { r.Workflow = "" },
		"bad sha":       func(r *RefOptions) { r.SHA = "develop" },
		"option branch": func(r *RefOptions) { r.Ref = "--upload-pack=x" },
		"bad event":     func(r *RefOptions) { r.Event = "workflow_dispatch" },
	} {
		t.Run(name, func(t *testing.T) {
			ro := refOpts(o, "develop", o.develop, "fast-gate.yml", "push")
			ro.Store = newStore(t)
			mut(&ro)
			if _, err := RunRef(context.Background(), ro); err == nil || !strings.HasPrefix(err.Error(), "run-ref:") {
				t.Fatalf("err = %v", err)
			}
			if exists(ro.Store.RepoPath()) {
				t.Fatal("a refused request fetches nothing")
			}
		})
	}
}

func TestWorkflowRunnableAsEvent(t *testing.T) {
	body := "jobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: echo\n"
	for name, tc := range map[string]struct {
		on    string
		event string
		want  bool
	}{
		"listed":                 {"on:\n  push:\n", "push", true},
		"dispatch as pr":         {dispatchOnlyOn, "pull_request", true},
		"dispatch as push":       {dispatchOnlyOn, "push", true},
		"dispatch as dispatch":   {dispatchOnlyOn, "workflow_dispatch", true},
		"dispatch as schedule":   {dispatchOnlyOn, "schedule", false},
		"pr as push":             {"on:\n  pull_request:\n", "push", false},
		"push as dispatch":       {"on:\n  push:\n", "workflow_dispatch", false},
		"schedule as pr":         {"on:\n  schedule:\n    - cron: '0 3 * * *'\n", "pull_request", false},
		"empty on, any event":    {"on: {}\n", "pull_request", true},
		"no on key, any event":   {"", "schedule", true},
		"unknown event is never": {dispatchOnlyOn, "release", false},
	} {
		t.Run(name, func(t *testing.T) {
			w, uns, err := ParseWorkflow(".github/workflows/w.yml", []byte("name: w\n"+tc.on+body))
			if err != nil || len(uns) != 0 {
				t.Fatalf("%v %v", err, uns)
			}
			if got := w.RunnableAsEvent(tc.event); got != tc.want {
				t.Fatalf("RunnableAsEvent(%q) = %v, want %v", tc.event, got, tc.want)
			}
		})
	}
}
