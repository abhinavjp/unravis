package investigate

import (
	"api-audit-explorer/internal/evidence"
	"strings"
	"testing"
	"time"
)

func stepsOf(in Input) []Step {
	th := Current()
	f := ComputeFacts(in, th)
	s := ComputeSignals(in, f, th)
	return Steps(in, f, s, th)
}

func wantStep(t *testing.T, steps []Step, n int, want Readiness) {
	t.Helper()
	if steps[n].Readiness != want {
		t.Errorf("step %d (%s) = %q, want %q; reasons %v", n, steps[n].Name, steps[n].Readiness, want, steps[n].Reasons)
	}
}

func fullInput() Input {
	in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400})
	lim := 100
	in.PoolLimit = &lim
	in.ReleasesEntered = true
	return in
}

// Every step of every case returns one of the four words, and a step whose data is
// missing never reads Ready. The fixed table below lists, per scenario, the steps
// that must not be Ready.
func TestEveryStepReturnsAReadinessWord(t *testing.T) {
	ref, inc := stdWindows()
	noAudit := Input{Incident: inc, Reference: ref, Audit: AuditMissing}
	failedAudit := Input{Incident: inc, Reference: ref, Audit: AuditFailed}
	overlap := fullInput()
	overlap.Reference = Window{at(10, 30), at(11, 0)}
	badWin := fullInput()
	badWin.Incident = Window{}
	noIIS := func() Input {
		r, i := stdWindows()
		in := gen(genCfg{seed: 3, ref: r, inc: i, perMin: 30, mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}}})
		return in
	}()
	empty := Input{Incident: inc, Reference: ref, Audit: AuditPresent}
	refAfter := fullInput()
	refAfter.Reference, refAfter.Incident = Window{at(12, 0), at(13, 0)}, Window{at(10, 0), at(11, 30)}

	cases := []struct {
		name    string
		in      Input
		notDone []int // steps that must not be Ready
	}{
		{"zero input", Input{}, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"audit missing", noAudit, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"audit failed", failedAudit, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"windows overlap", overlap, []int{0, 1, 7, 8, 9}},
		{"incident window missing", badWin, []int{0, 1, 2, 3, 4, 7, 8, 9, 10}},
		{"no IIS", noIIS, []int{1, 3, 6, 7, 8, 9}},
		{"no calls", empty, []int{0, 1, 2, 3, 4, 6, 7, 8, 9, 10}},
		{"reference after incident", refAfter, []int{0, 1, 2, 7, 8, 9}},
		{"everything entered", fullInput(), []int{1, 6, 7, 8, 9}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			steps := stepsOf(c.in)
			if len(steps) != StepCount {
				t.Fatalf("got %d steps", len(steps))
			}
			for i, s := range steps {
				if s.Number != i || s.Name != StepName(i) || s.Nutshell == "" {
					t.Errorf("step %d card is incomplete: %+v", i, s)
				}
				if !IsReadinessWord(s.Readiness) {
					t.Errorf("step %d readiness %q is not one of the four words", i, s.Readiness)
				}
				if strings.EqualFold(string(s.Readiness), "pass") {
					t.Errorf("step %d reads pass", i)
				}
				if s.Readiness != Ready && len(s.Reasons) == 0 {
					t.Errorf("step %d is %q with no reason", i, s.Readiness)
				}
			}
			for _, n := range c.notDone {
				if steps[n].Readiness == Ready {
					t.Errorf("step %d (%s) is Ready but its data is missing: %+v", n, steps[n].Name, steps[n])
				}
			}
		})
	}
}

func TestStepReadinessSpecifics(t *testing.T) {
	steps := stepsOf(fullInput())
	wantStep(t, steps, 0, Ready)
	wantStep(t, steps, 1, PartlyReady)
	wantStep(t, steps, 2, Ready)
	wantStep(t, steps, 3, Ready)
	wantStep(t, steps, 4, Ready)
	wantStep(t, steps, 5, Ready)
	wantStep(t, steps, 6, PartlyReady)
	for _, n := range []int{7, 8, 9} {
		wantStep(t, steps, n, CannotTestYet)
	}

	// No pool limit: Step 4 is partly ready. No change list: Step 5 cannot be tested.
	in := fullInput()
	in.PoolLimit, in.ReleasesEntered = nil, false
	steps = stepsOf(in)
	wantStep(t, steps, 4, PartlyReady)
	wantStep(t, steps, 5, CannotTestYet)

	// An entered, empty list is "No change listed", and the step is ready.
	in = fullInput()
	steps = stepsOf(in)
	if f := strings.Join(steps[5].Findings, " "); !strings.Contains(f, "No change listed") {
		t.Errorf("step 5 findings = %v", steps[5].Findings)
	}

	// Without IIS, outside time is untested: Step 3 is partly ready.
	r, i := stdWindows()
	noIIS := gen(genCfg{seed: 3, ref: r, inc: i, perMin: 30, mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}}})
	steps = stepsOf(noIIS)
	wantStep(t, steps, 3, PartlyReady)

	// A long gap leaves untested spans on the steps that read minutes.
	ref, inc := stdWindows()
	gapped := gen(genCfg{seed: 5, ref: ref, inc: inc, perMin: 30, iis: true,
		mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}},
		gaps: []Window{{at(10, 50), at(10, 58)}}})
	steps = stepsOf(gapped)
	wantStep(t, steps, 2, PartlyReady)
	if len(steps[2].UntestedSpans) == 0 {
		t.Errorf("no untested span on step 2: %+v", steps[2])
	}
}

func TestLaneSeamReadsSteps7To9(t *testing.T) {
	in := fullInput()
	in.Lanes = map[string]Tri{}
	for _, e := range Catalog() {
		if e.Group == GroupD {
			in.Lanes[e.ID] = No
		}
	}
	steps := stepsOf(in)
	wantStep(t, steps, 7, Ready)
	wantStep(t, steps, 8, CannotTestYet)

	// Mongo and host hold H2, M2 and H3 grey in Release 1, so they stop at Partly ready.
	for _, e := range Catalog() {
		if e.Group == GroupM || e.Group == GroupH {
			in.Lanes[e.ID] = No
		}
	}
	steps = stepsOf(in)
	wantStep(t, steps, 8, PartlyReady)
	wantStep(t, steps, 9, PartlyReady)
}

func TestReferenceAfterIncidentIsFlagged(t *testing.T) {
	in := fullInput()
	in.Reference, in.Incident = Window{at(12, 0), at(13, 0)}, Window{at(10, 0), at(11, 30)}
	if !referenceAfter(in) {
		t.Fatal("referenceAfter is false")
	}
	steps := stepsOf(in)
	wantStep(t, steps, 0, PartlyReady)
	if !strings.Contains(strings.Join(steps[0].Reasons, " "), "after the incident window") {
		t.Errorf("reasons = %v", steps[0].Reasons)
	}
}

func TestFailedFrameBlocksLaterSteps(t *testing.T) {
	in := fullInput()
	in.Reference = Window{in.Incident.Start.Add(time.Minute), in.Incident.End}
	steps := stepsOf(in)
	wantStep(t, steps, 0, Failed)
	for i := 1; i < StepCount; i++ {
		if steps[i].Readiness == Ready || steps[i].Readiness == PartlyReady {
			t.Errorf("step %d is %q after a failed frame", i, steps[i].Readiness)
		}
	}
}

func TestFixedWordsAreNonCausalAndHaveNoPercent(t *testing.T) {
	var texts []string
	for i := 0; i < StepCount; i++ {
		texts = append(texts, StepName(i), StepNutshell(i))
	}
	for _, e := range append(Catalog(), NotApplicable()...) {
		texts = append(texts, e.Title, e.Check, e.Overturn, e.Needs)
	}
	texts = append(texts, ReleaseCaveat)
	for _, s := range texts {
		if err := evidence.LintCausal(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
		if strings.Contains(s, "%") {
			t.Errorf("%q holds a percent sign", s)
		}
	}
}

func TestGreyReasonIsNeverALazyPlural(t *testing.T) {
	if got := greyReason(1); strings.Contains(got, "(s)") || !strings.HasPrefix(got, "1 suspect is ") {
		t.Fatalf("one: %q", got)
	}
	if got := greyReason(2); strings.Contains(got, "(s)") || !strings.HasPrefix(got, "2 suspects are ") {
		t.Fatalf("two: %q", got)
	}
}
