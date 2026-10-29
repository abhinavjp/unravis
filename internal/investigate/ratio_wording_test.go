package investigate

import (
	"regexp"
	"strings"
	"testing"
)

// L9-02: a share is shown to people as a percentage ("73%"), never as a raw ratio like "0.73".
func TestSharesAreShownAsPercentages(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 5, ref: ref, inc: inc, perMin: 30, routes: []string{"Orders.Get"},
		mods: []mod{{from: at(10, 0), to: at(11, 30), addC: 40, addA: 10}}})
	r := Analyze(in)
	c := scn1Part(t, r.Facts.Parts, "C")
	if c.Share == nil {
		t.Fatalf("premise: C needs a share: %+v", c)
	}
	raw := regexp.MustCompile(`\b[01]\.\d{2}\b`)
	pct := regexp.MustCompile(`holds \d{1,3}% of the extra time`)
	if !pct.MatchString(r.Facts.Parts.DominantWhy) {
		t.Errorf("DominantWhy = %q, want a percentage", r.Facts.Parts.DominantWhy)
	}
	for i, st := range r.Steps {
		for _, f := range st.Findings {
			if (strings.Contains(f, "share of the extra time") || strings.Contains(f, "Audit share")) && raw.MatchString(f) {
				t.Errorf("step %d finding %q shows a raw ratio", i, f)
			}
		}
	}
	found := false
	for _, f := range r.Steps[3].Findings {
		if regexp.MustCompile(`^C: share of the extra time \d{1,3}%\.$`).MatchString(f) {
			found = true
		}
	}
	if !found {
		t.Errorf("no percentage share finding for C in %v", r.Steps[3].Findings)
	}
}

func TestPercentOfRoundsToWholeNumbers(t *testing.T) {
	for in, want := range map[float64]string{0.73: "73%", 0.726: "73%", 0.5: "50%", 1: "100%", 0: "0%", 0.994: "99%"} {
		if got := percentOf(in); got != want {
			t.Errorf("percentOf(%v) = %q, want %q", in, got, want)
		}
	}
}
