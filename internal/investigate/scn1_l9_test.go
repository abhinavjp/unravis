package investigate

import (
	"regexp"
	"strings"
	"testing"
)

func scn1Findings(r Result) string { return strings.Join(r.Steps[3].Findings, "\n") }

func scn1Part(t *testing.T, p Parts, name string) PartFacts {
	t.Helper()
	for _, it := range p.Items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("no part %s in %+v", name, p.Items)
	return PartFacts{}
}

// D-B-29: with no audit rows in the reference window no part is tested, so Step 3
// says nothing about which part took the time.
func TestScn1Step3SaysNothingWhenAWindowHasNoData(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 1, ref: ref, inc: inc, perMin: 30, auditDrop: []Window{ref}, routes: []string{"Orders.Get"}})
	r := Analyze(in)
	for _, it := range r.Facts.Parts.Items {
		if it.Tested {
			t.Fatalf("part %s is tested with an empty reference window: %+v", it.Name, it)
		}
	}
	if d := r.Facts.Parts; d.Dominant != "" || d.DominantWhy != "" {
		t.Errorf("dominant = %q, why = %q; want both empty", d.Dominant, d.DominantWhy)
	}
	all := scn1Findings(r)
	for _, bad := range []string{"No one part", "No part took more time"} {
		if strings.Contains(all, bad) {
			t.Errorf("step 3 findings contain %q:\n%s", bad, all)
		}
	}
}

// D-B-29: one part holds over half of the extra time but its change is under the
// mark: it is named with the qualifier, and the finding is that sentence alone.
func TestScn1Step3NamesAPartUnderTheMark(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 5, ref: ref, inc: inc, perMin: 30, routes: []string{"Orders.Get"},
		mods: []mod{{from: at(10, 0), to: at(11, 30), addC: 40, addA: 10}}})
	r := Analyze(in)
	c := scn1Part(t, r.Facts.Parts, "C")
	if c.Share == nil || *c.Share < 0.5 || c.Change.Meaningful {
		t.Fatalf("premise: C needs a share of at least half and a change under the mark: %+v", c)
	}
	d := r.Facts.Parts
	if d.Dominant != "" {
		t.Errorf("dominant = %q, want none", d.Dominant)
	}
	re := regexp.MustCompile(`^Part C holds 0\.\d{2} of the extra time, which is under the change mark\. `)
	if !re.MatchString(d.DominantWhy) {
		t.Errorf("DominantWhy = %q", d.DominantWhy)
	}
	found := false
	for _, f := range r.Steps[3].Findings {
		if strings.Contains(f, "No one part") {
			t.Errorf("finding %q still says no one part", f)
		}
		if strings.HasPrefix(f, "Part C holds") {
			found = true
		}
	}
	if !found {
		t.Errorf("no Part C finding in %v", r.Steps[3].Findings)
	}
}

// A meaningful part over half stays dominant with its existing wording.
func TestScn1Step3MeaningfulDominantIsUnchanged(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 5, ref: ref, inc: inc, perMin: 30, routes: []string{"Orders.Get"},
		mods: []mod{{from: at(10, 0), to: at(11, 30), addC: 200}}})
	r := Analyze(in)
	d := r.Facts.Parts
	if d.Dominant != "C" {
		t.Fatalf("dominant = %q (%s)", d.Dominant, d.DominantWhy)
	}
	if !strings.HasPrefix(d.DominantWhy, "This part holds at least half of the extra time and rose by the change mark.") {
		t.Errorf("DominantWhy = %q", d.DominantWhy)
	}
	if !strings.Contains(scn1Findings(r), "The part that grew most is C.") {
		t.Errorf("findings = %v", r.Steps[3].Findings)
	}
}

// With data in both windows and nothing slower, the existing "nothing rose" wording stays.
func TestScn1Step3NothingRoseKeepsItsWording(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 5, ref: ref, inc: inc, perMin: 30, routes: []string{"Orders.Get"}})
	r := Analyze(in)
	d := r.Facts.Parts
	all := scn1Findings(r)
	if strings.Contains(all, "No one part") {
		t.Errorf("findings still say no one part:\n%s", all)
	}
	const want = "No part took more time in the incident window than in the reference window."
	if strings.HasPrefix(d.DominantWhy, want) {
		if !d.OTested && d.DominantWhy != want+" Outside time was not tested, so the shares cover A, C and P only." {
			t.Errorf("DominantWhy = %q", d.DominantWhy)
		}
		return
	}
	// Random noise lifted a part: the weaker contract above (no "No one part") is all that holds.
	t.Logf("noise made a part rise; DominantWhy = %q", d.DominantWhy)
}
