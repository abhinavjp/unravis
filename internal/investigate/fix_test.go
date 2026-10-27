package investigate

import (
	"testing"
	"time"
)

// Regression tests for the P5 review findings (reports/p5-review.md). Each one
// uses the failing input the review describes.

// tailSlowdown makes one call in ten from the given time on gain addC ms of C, as
// a slow query in SQL Server or Mongo would. The mean of C moves by a tenth of
// addC, the slow end moves by all of it.
func tailSlowdown(in Input, from time.Time, addC float64) Input {
	for i := range in.Requests {
		r := &in.Requests[i]
		if i%10 != 0 || r.Start.Before(from) {
			continue
		}
		c := *r.C + addC
		r.C = &c
		end := r.End.Add(time.Duration(addC * float64(time.Millisecond)))
		r.End = &end
	}
	return in
}

// F1: a suspect whose own data was never read must never be green. The slowdown
// sits in only some calls, so the audit means stay below the mark; the old rule
// read "mean below the mark" as "ruled out" for every SQL, Mongo and host suspect.
func TestFixF1TailSlowdownRulesNothingOutWithoutItsOwnData(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 41, ref: ref, inc: inc, perMin: 30, routes: []string{"Orders.Get"}})
	in = tailSlowdown(in, at(10, 20), 900)
	lim := 1000
	in.PoolLimit = &lim
	in.ReleasesEntered = true
	r := Analyze(in)

	if r.Facts.Onset.Status != "found" {
		t.Fatalf("onset = %+v", r.Facts.Onset)
	}
	for _, p := range r.Facts.Parts.Items {
		if p.Name == "C" && p.Change.Meaningful {
			t.Fatalf("the test needs the audit mean of C below the mark: %+v", p.Change)
		}
	}
	for _, s := range r.Suspects {
		switch s.Group {
		case GroupD, GroupM, GroupH:
			if s.Colour == Green || s.Result == ResContra {
				t.Errorf("%s is %s/%s although its own data was never read: %s", s.ID, s.Result, s.Colour, s.Why)
			}
			if s.Result != ResCannotTest || s.Colour != Grey {
				t.Errorf("%s = %s/%s, want Cannot test yet / grey", s.ID, s.Result, s.Colour)
			}
		}
	}
	for _, id := range []string{"S4", "S6", "S7"} {
		wantSuspect(t, r, id, ResCannotTest, Grey)
	}
	// The slow end of C moved, so the audit's own reading of C says so.
	if c := r.Signals.Flags[SigCLarge]; c.Value != Yes {
		t.Errorf("c_large = %+v, want yes from the slow end", c)
	}
}

// F1: with no part moving at all but the calls clearly slower, the audit cannot
// say any single part held flat.
func TestFixF1UnplacedSlowdownLeavesPartsUnknown(t *testing.T) {
	th := Current()
	flat := func(name string) PartFacts {
		return PartFacts{Name: name, Tested: true, Change: compareMs(ptr(50), ptr(50), th), Tail: compareMs(ptr(80), ptr(80), th)}
	}
	var f Facts
	f.Counts.Used = 10
	f.Change = OverallShift{Known: true, Meaningful: true, P95: compareMs(ptr(100), ptr(900), th), Mean: compareMs(ptr(50), ptr(60), th), P50: compareMs(ptr(50), ptr(50), th)}
	f.Parts.Items = []PartFacts{flat("A"), flat("C"), flat("P"), flat("O")}
	f.Parts.OTested = true
	s := ComputeSignals(Input{Audit: AuditPresent}, f, th)
	for _, n := range []string{SigALarge, SigCLarge, SigPLarge, SigOLarge} {
		if s.Flags[n].Value != Unknown {
			t.Errorf("%s = %+v, want unknown while the calls got slower and no part moved", n, s.Flags[n])
		}
	}
	// With the calls not slower, a flat part is a real no.
	f.Change = OverallShift{Known: true, P95: compareMs(ptr(100), ptr(100), th), Mean: compareMs(ptr(50), ptr(50), th), P50: compareMs(ptr(50), ptr(50), th)}
	s = ComputeSignals(Input{Audit: AuditPresent}, f, th)
	if s.Flags[SigCLarge].Value != No {
		t.Errorf("c_large = %+v, want no", s.Flags[SigCLarge])
	}
}

// F2: Step 10 must agree with the origin the headline reports. The sql-pool day
// names the action time as the origin, so Step 10 cannot say "Cannot test yet".
func TestFixF2StepTenAgreesWithTheOrigin(t *testing.T) {
	ref, inc := stdWindows()
	lim := 10
	sqlPool := gen(genCfg{seed: 12, ref: ref, inc: inc, perMin: 30, iis: true,
		mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 450}}})
	sqlPool.PoolLimit = &lim
	r := Analyze(sqlPool)
	if r.Origin.Status != OriginNamed || r.Origin.Tier != "c" {
		t.Fatalf("origin = %+v, the test needs a named origin", r.Origin)
	}
	if got := r.Steps[10].Readiness; got == CannotTestYet {
		t.Errorf("step 10 = %q while the origin is named: %v", got, r.Steps[10].Reasons)
	}

	together := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	together.LaneOnsets = map[string]time.Time{"sql": at(10, 20).Add(30 * time.Second)}
	cases := map[string]Input{
		"named":         sqlPool,
		"no clear":      together,
		"nothing dated": tailSlowdown(gen(genCfg{seed: 41, ref: ref, inc: inc, perMin: 30, routes: []string{"Orders.Get"}}), at(10, 20), 900),
		"quiet":         gen(genCfg{seed: 21, ref: ref, inc: inc, perMin: 30, iis: true}),
		"no audit":      {Incident: inc, Reference: ref, Audit: AuditMissing},
		"audit step":    stepScenario(mod{from: at(10, 20), to: at(11, 30), addA: 400}),
		"outside step":  stepScenario(mod{from: at(10, 20), to: at(11, 30), addO: 400}),
		"surge":         stepScenario(mod{from: at(10, 20), to: at(11, 30), countMul: 3}),
		"empty":         {},
	}
	for name, in := range cases {
		r := Analyze(in)
		st := r.Steps[10]
		cannot := st.Readiness == CannotTestYet
		switch r.Origin.Status {
		case OriginNotEnough:
			if !cannot {
				t.Errorf("%s: origin %q but step 10 = %q", name, r.Origin.Status, st.Readiness)
			}
		default:
			if cannot {
				t.Errorf("%s: origin %q but step 10 = %q: %v", name, r.Origin.Status, st.Readiness, st.Reasons)
			}
		}
	}
}
