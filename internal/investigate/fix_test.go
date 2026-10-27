package investigate

import (
	"strings"
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

// F3: red means "most likely" and goes only to a tier that is clearly first. With
// no dated onset nothing is first, so a lone supported suspect is orange at most.
func TestFixF3NoDatedOnsetNeverPaintsRed(t *testing.T) {
	ref, inc := stdWindows()
	slow := tailSlowdown(gen(genCfg{seed: 41, ref: ref, inc: inc, perMin: 30, routes: []string{"Orders.Get"}}), at(10, 20), 900)
	cases := map[string]Input{"tail slowdown": slow, "demo": demoInput(t)}
	for name, in := range cases {
		r := Analyze(in)
		if r.Origin.Status != OriginNotEnough {
			t.Logf("%s: origin %q", name, r.Origin.Status)
		}
		for _, s := range r.Suspects {
			if s.Colour != Red || s.Group == GroupQ {
				continue
			}
			if s.OnsetAt == nil {
				t.Errorf("%s: %s is red with no dated onset: %s", name, s.ID, s.Why)
			}
		}
		if s := findSuspect(r, "L2"); s != nil && s.Colour == Red {
			t.Errorf("%s: L2 is red with origin %q", name, r.Origin.Status)
		}
	}
}

// F5: a plateau in the incident that the reference has too is the normal level of
// traffic, not a full pool. The demo day has such a plateau.
func TestFixF5EqualPlateauIsNotAFullPool(t *testing.T) {
	r := Analyze(demoInput(t))
	l := r.Facts.Load
	if l.Reading == "a pool or cap looks full" {
		t.Errorf("reading = %q although the reference peak is %d and the incident peak %d", l.Reading, l.RefPeak, l.IncPeak)
	}
	if l.FlatTop {
		t.Errorf("flat top %d reported against a reference peak of %d", l.FlatTopAt, l.RefPeak)
	}
}

// burst makes every call in the window start on the same second of its minute, so
// the in-flight peak is the whole minute's count while the rate and the time each
// call takes stay as they were.
func burst(in Input, w Window) Input {
	for i := range in.Requests {
		r := &in.Requests[i]
		if !w.Contains(r.Start) {
			continue
		}
		d := r.End.Sub(r.Start)
		s := minuteOf(r.Start).Add(10 * time.Second)
		r.Start = s
		e := s.Add(d)
		r.End = &e
	}
	return in
}

// F5: a flat top well above the reference peak, with rate and time unchanged, is
// still read as a pool or cap; the same plateau in both windows is not.
func TestFixF5NewPlateauAboveTheReferenceIsReadAsFull(t *testing.T) {
	ref, inc := stdWindows()
	base := func() Input {
		return gen(genCfg{seed: 61, ref: ref, inc: inc, perMin: 30, routes: []string{"Orders.Get"}})
	}

	r := Analyze(burst(base(), inc))
	l := r.Facts.Load
	if l.Reading != "a pool or cap looks full" || !l.FlatTop {
		t.Errorf("new plateau: reading %q flat %v (peaks ref %d inc %d, dx %s/%s/%s)", l.Reading, l.FlatTop, l.RefPeak, l.IncPeak, l.LambdaDx, l.WDx, l.LDx)
	}

	both := burst(burst(base(), inc), ref)
	l = Analyze(both).Facts.Load
	if l.Reading == "a pool or cap looks full" || l.FlatTop {
		t.Errorf("same plateau in both windows: reading %q flat %v (peaks ref %d inc %d)", l.Reading, l.FlatTop, l.RefPeak, l.IncPeak)
	}
}

// F6: with a tier whose own data was not read (O with no IIS), the first dated tier
// is not named, because the unread one could have moved earlier.
func TestFixF6UnreadTierBlocksTheOrigin(t *testing.T) {
	th := Current()
	a, c := at(10, 20), at(10, 25)
	f := Facts{Onset: Onset{Status: "found", T0: &a}, Tiers: []TierOnset{
		{Tier: "load", Tested: true},
		{Tier: "a", At: &a, Tested: true},
		{Tier: "c", At: &c, Tested: true},
		{Tier: "o", Tested: false, Why: "There is no IIS source in this case."},
	}}
	in := Input{Audit: AuditPresent}
	o := ComputeOrigin(in, f, ComputeSignals(in, f, th), th)
	if o.Status == OriginNamed {
		t.Fatalf("origin = %+v: A named first while O was never read", o)
	}
	if o.Status != OriginNotEnough || !strings.Contains(o.Note, "Outside time (O)") {
		t.Errorf("origin = %+v, want not enough data naming O", o)
	}
	// The same two onsets with O read and flat name A.
	f.Tiers[3].Tested = true
	o = ComputeOrigin(in, f, ComputeSignals(in, f, th), th)
	if o.Status != OriginNamed || o.Tier != "a" {
		t.Errorf("origin = %+v, want A named when every tier was read", o)
	}
}

// F7: t0 is a whole minute, so a release made inside that minute leads the change.
func TestFixF7ReleaseInsideTheStartMinuteLeads(t *testing.T) {
	th := Current()
	t0 := at(10, 20)
	f := Facts{Onset: Onset{Status: "found", T0: &t0}}
	in := Input{ReleasesEntered: true, Releases: []Release{{At: t0.Add(30 * time.Second), Label: "C1"}}}
	tl := computeTimeline(in, f, th)
	if tl.Verdict != Yes || len(tl.Leading) != 1 || tl.Items[0].Relation != "just before the start" {
		t.Errorf("timeline = %+v", tl)
	}
	if m := tl.Items[0].MinutesBefore; m == nil || *m != 0 {
		t.Errorf("minutes before = %v", m)
	}
	in.Releases[0].At = t0.Add(time.Minute + time.Second)
	if tl := computeTimeline(in, f, th); tl.Items[0].Relation != "after the start" {
		t.Errorf("a release in the next minute reads %q", tl.Items[0].Relation)
	}
}

// F8: an exact split between two parts names neither; an unnormalised audit path
// still matches its IIS rows; a systematic negative O is noted in Step 3.
func TestFixF8PartsAndIISEdges(t *testing.T) {
	ref, inc := stdWindows()
	base := func() Input {
		return gen(genCfg{seed: 71, ref: ref, inc: inc, perMin: 30, iis: true, routes: []string{"Orders.Get"}})
	}

	tie := base()
	for i := range tie.Requests {
		r := &tie.Requests[i]
		a, c := 10.0, 100.0
		if inc.Contains(r.Start) {
			a, c = 110, 200
		}
		r.A, r.C = &a, &c
		e := r.Start.Add(time.Duration((a + c + 5) * float64(time.Millisecond)))
		r.End = &e
	}
	if d := Analyze(tie).Facts.Parts; d.Dominant != "" {
		t.Errorf("an even split named %q: %s", d.Dominant, d.DominantWhy)
	}

	messy := base()
	for i := range messy.Requests {
		messy.Requests[i].Path = "/API/Orders.Get/"
	}
	clean := Analyze(base()).Facts.Parts.IISMatched
	if got := Analyze(messy).Facts.Parts.IISMatched; got == 0 || got != clean {
		t.Errorf("matched %d with an unnormalised path, %d with a normalised one", got, clean)
	}

	neg := base()
	for i := range neg.IIS {
		neg.IIS[i].TakenMs -= 400
	}
	r := Analyze(neg)
	if r.Facts.Parts.OClamped == 0 {
		t.Fatalf("no clamped pairs: %+v", r.Facts.Parts)
	}
	if r.Steps[3].Readiness == Ready || !strings.Contains(strings.Join(r.Steps[3].Reasons, " "), "below zero") {
		t.Errorf("step 3 = %q %v", r.Steps[3].Readiness, r.Steps[3].Reasons)
	}
}
