package cicli

import (
	"strings"
	"testing"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/hsaction"
	"api-audit-explorer/internal/localci"
	"api-audit-explorer/internal/releasehold"
)

// Every action of the registry has a command, and the command runs the action: it leaves the action's activity entry,
// with the actor cli (or HOMESTRETCH_ACTOR). internal/releasedash has the same table for POST /api/action/<id>.
func TestEveryActionIsCallableFromTheCLI(t *testing.T) {
	state, ids := policyState(t, "m1", "m2")
	store, err := localci.OpenStore(state)
	if err != nil {
		t.Fatal(err)
	}
	const runID = "20260101-000000-push-develop-ci"
	if err := store.AppendIndex(localci.IndexEntry{RunID: runID, Workflow: "ci.yml", Event: "push", Repo: "o/r", HeadRef: "develop",
		HeadSHA: strings.Repeat("4", 40), Conclusion: "failure"}, nil); err != nil {
		t.Fatal(err)
	}
	holdID := func() string {
		hs, err := (releasehold.Store{Dir: state}).List()
		if err != nil || len(hs) == 0 {
			t.Fatalf("holds: %v %v", hs, err)
		}
		return hs[len(hs)-1].ID
	}
	env := []string{"USERNAME=pat"}
	rows := []struct {
		id   string
		args func() []string
	}{
		{"queue.top", func() []string { return []string{"queue", "top", ids[1]} }},
		{"queue.pause", func() []string { return []string{"queue", "pause"} }},
		{"queue.resume", func() []string { return []string{"queue", "resume"} }},
		{"queue.cancel", func() []string { return []string{"cancel", ids[0]} }},
		{"queue.retry", func() []string { return []string{"rerun", runID} }},
		{"machines.allow", func() []string { return []string{"machines", "allow", "m1", "--project", "o/r"} }},
		{"machines.deny", func() []string { return []string{"machines", "deny", "m2", "--project", "o/r"} }},
		{"policy.set", func() []string { return []string{"policy", "set", "o/r", "--any"} }},
		{"machines.drain", func() []string { return []string{"drain", "--name", "m1"} }},
		{"machines.resume", func() []string { return []string{"machines", "resume", "m1"} }},
		{"hold.add", func() []string {
			return []string{"hold", "add", "--project", "unravis", "--until", "2h", "--reason", "freeze"}
		}},
		{"hold.lift", func() []string { return []string{"hold", "lift", holdID(), "--reason", "done"} }},
	}
	covered := map[string]bool{}
	for _, row := range rows {
		covered[row.id] = true
		args := append(row.args(), "--state", state)
		if row.id == "hold.add" || row.id == "hold.lift" {
			args[len(args)-2] = "--state-dir"
		}
		if code, out, e := cli(t, env, args...); code != exitOK {
			t.Errorf("%s (%s): %d %q %q", row.id, strings.Join(args, " "), code, out, e)
		}
	}
	for _, a := range hsaction.All() {
		if !covered[a.ID] {
			t.Errorf("%s has no CLI row", a.ID)
		}
	}
	es, _, err := (activitylog.Log{Dir: state}).Read(activitylog.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	logged := map[string]bool{}
	for _, e := range es {
		if e.Actor != activitylog.ActorCLI || e.Person != "pat" || e.Outcome != "done" {
			t.Errorf("entry %+v", e)
		}
		logged[e.Action] = true
	}
	for _, a := range hsaction.All() {
		if !logged[a.ID] {
			t.Errorf("%s left no activity entry", a.ID)
		}
	}
	// HOMESTRETCH_ACTOR names the actor.
	if code, _, e := cli(t, []string{"USERNAME=pat", "HOMESTRETCH_ACTOR=claude"}, "queue", "pause", "--state", state); code != exitOK {
		t.Fatalf("pause as claude: %d %q", code, e)
	}
	es, _, _ = (activitylog.Log{Dir: state}).Read(activitylog.Filter{})
	if last := es[len(es)-1]; last.Actor != activitylog.ActorClaude || last.Action != "queue.pause" {
		t.Fatalf("last entry %+v", last)
	}
}
