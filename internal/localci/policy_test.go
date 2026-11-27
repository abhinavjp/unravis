package localci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// policyRig is a state folder with a queue and hand-written heartbeats.
func policyRig(t *testing.T) (dir string, q *Queue) {
	t.Helper()
	dir = t.TempDir()
	q, err := OpenQueue(QueueDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	return dir, q
}

func writeBeat(t *testing.T, dir string, hb Heartbeat) {
	t.Helper()
	if err := os.MkdirAll(AgentsDir(dir), 0o750); err != nil {
		t.Fatal(err)
	}
	if hb.LastSeen.IsZero() {
		hb.LastSeen = time.Now()
	}
	if hb.Slots == 0 {
		hb.Slots = 1
	}
	if hb.State == "" {
		hb.State = "ready"
	}
	if err := writeAtomic(heartbeatPath(dir, hb.Name), hb); err != nil {
		t.Fatal(err)
	}
}

func setPolicy(t *testing.T, dir, repo, mode string, machines ...string) PolicyChange {
	t.Helper()
	pp, err := NewProjectPolicy(mode, machines)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := ChangePolicy(dir, repo, func(ProjectPolicy) (ProjectPolicy, error) { return pp, nil }, true, false)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestPolicyModesAllow(t *testing.T) {
	cases := []struct {
		mode     string
		list     []string
		machine  string
		expected bool
	}{
		{PolicyAny, nil, "m1", true},
		{PolicyOnly, []string{"m1", "m2"}, "m1", true},
		{PolicyOnly, []string{"m1", "m2"}, "M2", true}, // names compare without case, like the file names they are
		{PolicyOnly, []string{"m1"}, "m3", false},
		{PolicyExcept, []string{"m1"}, "m1", false},
		{PolicyExcept, []string{"m1"}, "m2", true},
		{"later-mode", []string{"m1"}, "m1", false}, // a rule this version cannot read allows nobody
		{PolicyOnly, nil, "m1", false},
	}
	for _, c := range cases {
		pp := ProjectPolicy{Mode: c.mode, Machines: c.list}
		if got := pp.Allows(c.machine); got != c.expected {
			t.Errorf("%s %v allows %s = %v, want %v", c.mode, c.list, c.machine, got, c.expected)
		}
	}
}

func TestPolicyAllowDenyEdits(t *testing.T) {
	any := ProjectPolicy{Mode: PolicyAny}
	only := ProjectPolicy{Mode: PolicyOnly, Machines: []string{"m1"}}
	except := ProjectPolicy{Mode: PolicyExcept, Machines: []string{"m1"}}
	cases := []struct {
		name string
		got  ProjectPolicy
		want string
	}{
		{"allow on any", any.AllowMachine("m2"), "any machine"},
		{"allow on only", only.AllowMachine("m2"), "only these: m1, m2"},
		{"allow on except", except.AllowMachine("m1"), "any machine"},
		{"allow on except keeps others", ProjectPolicy{Mode: PolicyExcept, Machines: []string{"m1", "m2"}}.AllowMachine("m1"), "any except these: m2"},
		{"deny on any", any.DenyMachine("m2"), "any except these: m2"},
		{"deny on except", except.DenyMachine("m2"), "any except these: m1, m2"},
		{"deny on only", ProjectPolicy{Mode: PolicyOnly, Machines: []string{"m1", "m2"}}.DenyMachine("m2"), "only these: m1"},
		{"deny the last on only", only.DenyMachine("m1"), "only these: none (no machine may run it)"},
		{"deny twice", except.DenyMachine("m1"), "any except these: m1"},
	}
	for _, c := range cases {
		if d := c.got.Describe(); d != c.want {
			t.Errorf("%s: %q, want %q", c.name, d, c.want)
		}
	}
}

func TestNewProjectPolicyChecks(t *testing.T) {
	if _, err := NewProjectPolicy(PolicyOnly, nil); err == nil {
		t.Error("only with no machine must be refused")
	}
	if _, err := NewProjectPolicy(PolicyAny, []string{"m1"}); err == nil {
		t.Error("any with machines must be refused")
	}
	if _, err := NewProjectPolicy(PolicyExcept, []string{"../x"}); err == nil {
		t.Error("a path as a machine name must be refused")
	}
	if _, err := NewProjectPolicy("some", []string{"m1"}); err == nil {
		t.Error("an unknown mode must be refused")
	}
	pp, err := NewProjectPolicy(PolicyOnly, []string{"m2", "m1", "m2", " "})
	if err != nil || strings.Join(pp.Machines, ",") != "m1,m2" {
		t.Fatalf("names are cleaned and sorted: %+v %v", pp, err)
	}
}

func TestPolicyFileRoundTripAndTolerantRead(t *testing.T) {
	dir := t.TempDir()
	p, err := LoadPolicy(dir)
	if err != nil || len(p.Projects) != 0 || !p.Allows("o/r", "m1") {
		t.Fatalf("a missing file is any machine: %+v %v", p, err)
	}
	setPolicy(t, dir, "Owner/Repo", PolicyOnly, "m1")
	p, err = LoadPolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Allows("owner/repo", "m1") || p.Allows("OWNER/REPO", "m2") {
		t.Fatalf("the project key ignores case: %+v", p.Projects)
	}
	// Setting it back to any removes the entry.
	setPolicy(t, dir, "owner/repo", PolicyAny)
	if p, _ = LoadPolicy(dir); len(p.Projects) != 0 {
		t.Fatalf("any is the default and is not stored: %+v", p.Projects)
	}

	// Unknown fields are ignored; bad keys and names are dropped; an unknown mode allows nobody.
	raw := `{"v":1,"later":"x","projects":{"o/r":{"mode":"except","machines":["m1","../bad"],"note":"x"},"not a repo":{"mode":"any"},"o/s":{"mode":"later-mode"}}}`
	if err := os.WriteFile(PolicyPath(dir), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err = LoadPolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.For("o/r"); got.Mode != PolicyExcept || len(got.Machines) != 1 {
		t.Fatalf("tolerant read: %+v", got)
	}
	if p.Allows("o/s", "m1") {
		t.Fatal("an unknown mode must allow nobody")
	}
	if _, ok := p.Projects["not a repo"]; ok {
		t.Fatal("a key that is not owner/name is dropped")
	}

	// Not JSON, or a newer version: an error (the claim then takes nothing).
	for _, bad := range []string{`{not json`, `{"v":2,"projects":{}}`} {
		if err := os.WriteFile(PolicyPath(dir), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPolicy(dir); err == nil {
			t.Fatalf("%s must be an error", bad)
		}
		if _, err := ChangePolicy(dir, "o/r", func(pp ProjectPolicy) (ProjectPolicy, error) { return pp.DenyMachine("m1"), nil }, true, false); err == nil {
			t.Fatalf("an edit must not write over %s", bad)
		}
	}
}

func TestClaimHonoursPolicy(t *testing.T) {
	dir, q := policyRig(t)
	r := mustEnqueue(t, q, prReq(1, strings.Repeat("a", 40)))
	setPolicy(t, dir, "o/r", PolicyExcept, "m1")
	if got, err := q.Claim("m1", nil); err != nil || got != nil {
		t.Fatalf("a denied agent must not claim: %+v %v", got, err)
	}
	if got, err := q.Claim("M1", nil); err != nil || got != nil {
		t.Fatalf("deny matches the name whatever its case: %+v %v", got, err)
	}
	// Another project's request is still taken by m1.
	other := Request{Kind: KindPR, Repo: "o/other", Ref: "f", Base: "develop", Number: 9, SHA: strings.Repeat("c", 40), Workflow: "fast-gate.yml"}
	o := mustEnqueue(t, q, other)
	if got, err := q.Claim("m1", nil); err != nil || got == nil || got.ID != o.ID {
		t.Fatalf("m1 takes the other project's request: %+v %v", got, err)
	}
	if got, err := q.Claim("m2", nil); err != nil || got == nil || got.ID != r.ID {
		t.Fatalf("an allowed agent claims it: %+v %v", got, err)
	}
}

func TestClaimOnlyThese(t *testing.T) {
	dir, q := policyRig(t)
	r := mustEnqueue(t, q, prReq(1, strings.Repeat("a", 40)))
	setPolicy(t, dir, "o/r", PolicyOnly, "m2")
	if got, _ := q.Claim("m1", nil); got != nil {
		t.Fatalf("m1 is not on the list: %+v", got)
	}
	if got, _ := q.Claim("m2", nil); got == nil || got.ID != r.ID {
		t.Fatalf("m2 is on the list: %+v", got)
	}
}

func TestClaimFailsClosedOnUnreadablePolicy(t *testing.T) {
	dir, q := policyRig(t)
	mustEnqueue(t, q, prReq(1, strings.Repeat("a", 40)))
	if err := os.WriteFile(PolicyPath(dir), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := q.Claim("m1", nil); err == nil || got != nil {
		t.Fatalf("an unreadable policy must stop the claim: %+v %v", got, err)
	}
}

func TestClaimLabelsAndPolicyBothDecide(t *testing.T) {
	dir, q := policyRig(t)
	req := prReq(1, strings.Repeat("a", 40))
	req.Labels = []string{"gpu"}
	mustEnqueue(t, q, req)
	setPolicy(t, dir, "o/r", PolicyOnly, "m1")
	if got, _ := q.Claim("m1", nil); got != nil {
		t.Fatal("policy allows m1 but it lacks the label")
	}
	if got, _ := q.Claim("m2", []string{"gpu"}); got != nil {
		t.Fatal("m2 has the label but policy denies it")
	}
	if got, _ := q.Claim("m1", []string{"gpu"}); got == nil {
		t.Fatal("m1 has the label and is allowed")
	}
}

func TestChangePolicyRefusesToStrand(t *testing.T) {
	dir, q := policyRig(t)
	writeBeat(t, dir, Heartbeat{Name: "m1"})
	writeBeat(t, dir, Heartbeat{Name: "m2", Labels: []string{"gpu"}})
	a := mustEnqueue(t, q, prReq(1, strings.Repeat("a", 40)))
	gpu := prReq(2, strings.Repeat("b", 40))
	gpu.Labels = []string{"gpu"}
	b := mustEnqueue(t, q, gpu)

	deny := func(m string) func(ProjectPolicy) (ProjectPolicy, error) {
		return func(pp ProjectPolicy) (ProjectPolicy, error) { return pp.DenyMachine(m), nil }
	}
	// Denying m1 strands nothing: m2 can take both.
	ch, err := ChangePolicy(dir, "o/r", deny("m1"), false, false)
	if err != nil || !ch.Applied || len(ch.Impact.Stranded) != 0 || ch.Impact.Queued != 2 {
		t.Fatalf("deny m1: %+v %v", ch, err)
	}
	// Denying m2 as well strands both: refused, nothing written.
	ch, err = ChangePolicy(dir, "o/r", deny("m2"), false, false)
	if err != nil || ch.Applied || !ch.Refused || strings.Join(ch.Impact.Stranded, ",") != a.ID+","+b.ID {
		t.Fatalf("deny m2 must be refused with both ids: %+v %v", ch, err)
	}
	if ch.Impact.Line() != "2 queued runs would have no machine" {
		t.Fatalf("impact line: %q", ch.Impact.Line())
	}
	if p, _ := LoadPolicy(dir); p.Allows("o/r", "m2") != true {
		t.Fatal("a refused edit must not be written")
	}
	// A dry run reports the same and writes nothing, forced or not.
	ch, err = ChangePolicy(dir, "o/r", deny("m2"), true, true)
	if err != nil || ch.Applied || ch.Refused || len(ch.Impact.Stranded) != 2 {
		t.Fatalf("dry run: %+v %v", ch, err)
	}
	if p, _ := LoadPolicy(dir); !p.Allows("o/r", "m2") {
		t.Fatal("a dry run must not write")
	}
	// Forced: written, and the impact still says what it did.
	ch, err = ChangePolicy(dir, "o/r", deny("m2"), true, false)
	if err != nil || !ch.Applied || len(ch.Impact.Stranded) != 2 {
		t.Fatalf("forced: %+v %v", ch, err)
	}
	if p, _ := LoadPolicy(dir); p.Allows("o/r", "m2") {
		t.Fatal("a forced edit is written")
	}
	// The same edit again changes nothing.
	ch, err = ChangePolicy(dir, "o/r", deny("m2"), false, false)
	if err != nil || !ch.Unchanged || ch.Applied || ch.Refused {
		t.Fatalf("a repeat edit is unchanged: %+v %v", ch, err)
	}
}

func TestChangePolicyWithNoKnownMachineStrandsNothing(t *testing.T) {
	dir, q := policyRig(t)
	mustEnqueue(t, q, prReq(1, strings.Repeat("a", 40)))
	ch, err := ChangePolicy(dir, "o/r", func(pp ProjectPolicy) (ProjectPolicy, error) { return pp.DenyMachine("m1"), nil }, false, false)
	if err != nil || !ch.Applied || len(ch.Impact.Stranded) != 0 || ch.Impact.NoMachine != 1 {
		t.Fatalf("with no known machine nothing is stranded by the edit: %+v %v", ch, err)
	}
}

func TestChangePolicyRejectsBadProject(t *testing.T) {
	if _, err := ChangePolicy(t.TempDir(), "../x", func(pp ProjectPolicy) (ProjectPolicy, error) { return pp, nil }, false, false); err == nil {
		t.Fatal("a project that is not owner/name must be refused")
	}
}

func TestPolicyLockLeftByADeadProcessIsTakenOver(t *testing.T) {
	dir := t.TempDir()
	lock := PolicyPath(dir) + ".lock"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	setPolicy(t, dir, "o/r", PolicyExcept, "m1")
	if _, err := os.Stat(lock); err == nil {
		t.Fatal("the lock must be removed after the edit")
	}
	if _, err := os.Stat(filepath.Join(dir, PolicyFileName)); err != nil {
		t.Fatal(err)
	}
}

func TestAgentResumeAfterDrainMark(t *testing.T) {
	release := make(chan struct{})
	started := make(chan string, 4)
	rig := newAgentRig(t, "a1", 1, func(ctx context.Context, r Request) (Outcome, error) {
		started <- r.ID
		<-release
		return Outcome{Conclusion: "success"}, nil
	})
	first := mustEnqueue(t, rig.q, prReq(1, strings.Repeat("a", 40)))
	rig.start()
	if id := <-started; id != first.ID {
		t.Fatalf("started %s", id)
	}
	if err := RequestDrain(rig.dir, "a1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "draining state", func() bool { hb, _ := ReadAgents(rig.dir); return len(hb) == 1 && hb[0].State == "draining" })
	if !DrainRequested(rig.dir, "a1") {
		t.Fatal("the drain mark stays while the agent drains")
	}
	second := mustEnqueue(t, rig.q, prReq(2, strings.Repeat("b", 40)))
	cleared, err := ResumeAgent(rig.dir, "a1")
	if err != nil || !cleared {
		t.Fatalf("resume: %v %v", cleared, err)
	}
	waitFor(t, "ready state", func() bool { hb, _ := ReadAgents(rig.dir); return len(hb) == 1 && hb[0].State == "ready" })
	close(release)
	select {
	case id := <-started:
		if id != second.ID {
			t.Fatalf("started %s", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a resumed agent must take the next request")
	}
	select {
	case err := <-rig.done:
		t.Fatalf("a resumed agent must keep running: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if cleared, err := ResumeAgent(rig.dir, "a1"); err != nil || cleared {
		t.Fatalf("a second resume finds no mark: %v %v", cleared, err)
	}
	if _, err := ResumeAgent(rig.dir, "../x"); err == nil {
		t.Fatal("a path as an agent name must be refused")
	}
}

func TestCurrentImpactReadsWithoutWriting(t *testing.T) {
	dir, q := policyRig(t)
	writeBeat(t, dir, Heartbeat{Name: "m1"})
	mustEnqueue(t, q, prReq(1, strings.Repeat("a", 40)))
	mustEnqueue(t, q, prReq(2, strings.Repeat("b", 40)))
	imp, err := CurrentImpact(dir, "O/R")
	if err != nil || imp.Queued != 2 || imp.NoMachine != 0 || len(imp.Stranded) != 0 || len(imp.Machines) != 1 {
		t.Fatalf("any machine: %+v %v", imp, err)
	}
	setPolicy(t, dir, "o/r", PolicyOnly, "m9")
	imp, err = CurrentImpact(dir, "o/r")
	if err != nil || imp.Queued != 2 || imp.NoMachine != 2 || len(imp.Stranded) != 0 {
		t.Fatalf("only m9: %+v %v", imp, err)
	}
	if _, err := os.Stat(filepath.Join(dir, PolicyFileName+".lock")); err == nil {
		t.Fatal("reading left a lock file")
	}
	if _, err := CurrentImpact(dir, "../x"); err == nil {
		t.Fatal("not a project")
	}
	if err := os.WriteFile(PolicyPath(dir), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CurrentImpact(dir, "o/r"); err == nil {
		t.Fatal("an unreadable policy is an error")
	}
}

func TestSortClaimOrderPutsLowerPriorityFirstThenOlder(t *testing.T) {
	t0 := time.Now()
	rs := []Request{
		{ID: "c", Priority: 2, EnqueuedAt: t0},
		{ID: "b", Priority: 1, EnqueuedAt: t0.Add(time.Second)},
		{ID: "a", Priority: 1, EnqueuedAt: t0},
		{ID: "z", Priority: 0, EnqueuedAt: t0.Add(time.Minute)},
	}
	SortClaimOrder(rs)
	if got := rs[0].ID + rs[1].ID + rs[2].ID + rs[3].ID; got != "zabc" {
		t.Fatalf("order %s", got)
	}
}
