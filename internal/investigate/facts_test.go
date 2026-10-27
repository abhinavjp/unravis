package investigate

import (
	"testing"
	"time"
)

func stepScenario(extra mod) Input {
	ref, inc := stdWindows()
	cfg := genCfg{seed: 1, ref: ref, inc: inc, perMin: 30, iis: true, mods: []mod{extra}}
	return gen(cfg)
}

func tierAt(f Facts, tier string) *time.Time {
	for _, ti := range f.Tiers {
		if ti.Tier == tier {
			return ti.At
		}
	}
	return nil
}

func TestFactsCStep(t *testing.T) {
	in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400})
	f := ComputeFacts(in, Current())
	if f.Onset.Status != "found" || f.Onset.T0 == nil || !f.Onset.T0.Equal(at(10, 20)) {
		t.Fatalf("onset = %+v", f.Onset)
	}
	if f.Shape.Kind != "step" {
		t.Errorf("shape = %+v", f.Shape)
	}
	if f.Scope.Kind != "everything" {
		t.Errorf("scope = %+v", f.Scope)
	}
	if f.Parts.Dominant != "C" || !f.Parts.OTested {
		t.Errorf("parts = %+v", f.Parts)
	}
	if !f.Change.Meaningful {
		t.Errorf("change = %+v", f.Change)
	}
	if c := tierAt(f, "c"); c == nil || !c.Equal(at(10, 20)) {
		t.Errorf("c onset = %v", c)
	}
	if tierAt(f, "a") != nil || tierAt(f, "o") != nil || tierAt(f, "load") != nil {
		t.Errorf("other tiers moved: %+v", f.Tiers)
	}
	if f.Load.Reading != "calls got slower" {
		t.Errorf("load = %+v", f.Load)
	}
}

func TestFactsOStep(t *testing.T) {
	in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addO: 600})
	f := ComputeFacts(in, Current())
	if f.Parts.Dominant != "O" {
		t.Errorf("parts = %+v", f.Parts)
	}
	if f.Parts.IISMatchRate == nil || *f.Parts.IISMatchRate < 0.99 {
		t.Errorf("match rate = %v", f.Parts.IISMatchRate)
	}
	// O sits outside D, so D itself did not move.
	if f.Change.Meaningful {
		t.Errorf("D change = %+v", f.Change)
	}
	if o := tierAt(f, "o"); o == nil || !o.Equal(at(10, 20)) {
		t.Errorf("o onset = %v", o)
	}
}

func TestFactsNoIncident(t *testing.T) {
	f := ComputeFacts(stepScenario(mod{}), Current())
	if f.Onset.Status != "not found" || f.Change.Meaningful {
		t.Errorf("onset %+v change %+v", f.Onset, f.Change)
	}
	if f.Scope.Kind != "none" || f.Shape.Kind != "none" {
		t.Errorf("scope %+v shape %+v", f.Scope, f.Shape)
	}
}

func TestFactsLoadSurge(t *testing.T) {
	f := ComputeFacts(stepScenario(mod{from: at(10, 20), to: at(11, 30), countMul: 3}), Current())
	if l := tierAt(f, "load"); l == nil || !l.Equal(at(10, 20)) {
		t.Errorf("load onset = %v", l)
	}
	if f.Load.LambdaDx != "up" || f.Load.Reading == "" {
		t.Errorf("load = %+v", f.Load)
	}
}

func TestFactsShortGapIsNotZero(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 2, ref: ref, inc: inc, perMin: 30, mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}},
		gaps: []Window{{at(10, 30), at(10, 32)}}})
	f := ComputeFacts(in, Current())
	if len(f.Gaps) != 1 || f.Gaps[0].Kind != "short" || f.Gaps[0].Minutes != 2 {
		t.Fatalf("gaps = %+v", f.Gaps)
	}
	if f.Onset.Status != "found" || !f.Onset.T0.Equal(at(10, 20)) {
		t.Errorf("onset = %+v", f.Onset)
	}
	// 88 testable minutes of 30 calls: the rate must not drop for the gap.
	if f.Incident.PerSec == nil || *f.Incident.PerSec < 0.49 || *f.Incident.PerSec > 0.51 {
		t.Errorf("rate = %v", f.Incident.PerSec)
	}
}

func TestFactsLongGapResetsOnset(t *testing.T) {
	ref, inc := stdWindows()
	// The change starts at 10:20 but a ten minute gap sits inside the first run.
	in := gen(genCfg{seed: 3, ref: ref, inc: inc, perMin: 30, mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}},
		gaps: []Window{{at(10, 22), at(10, 32)}}})
	f := ComputeFacts(in, Current())
	if len(f.Gaps) != 1 || f.Gaps[0].Kind != "long" {
		t.Fatalf("gaps = %+v", f.Gaps)
	}
	if f.Onset.T0 == nil || !f.Onset.T0.Equal(at(10, 32)) {
		t.Errorf("onset = %+v", f.Onset)
	}
}

func TestFactsPeriodic(t *testing.T) {
	ref, inc := stdWindows()
	var mods []mod
	for _, m := range []int{5, 20, 35, 50, 65} {
		mods = append(mods, mod{from: at(10, m), to: at(10, m+6), addC: 600})
	}
	f := ComputeFacts(gen(genCfg{seed: 4, ref: ref, inc: inc, perMin: 30, mods: mods}), Current())
	if f.Shape.Kind != "periodic" || f.Shape.PeriodMinutes != 15 {
		t.Errorf("shape = %+v", f.Shape)
	}
}

func TestFactsRamp(t *testing.T) {
	ref, inc := stdWindows()
	var mods []mod
	// C rises 60 ms a minute from 10:10 and then holds.
	for i := 0; i < 10; i++ {
		mods = append(mods, mod{from: at(10, 10+i), to: at(10, 11+i), addC: float64(60 * (i + 1))})
	}
	mods = append(mods, mod{from: at(10, 20), to: at(11, 30), addC: 600})
	f := ComputeFacts(gen(genCfg{seed: 5, ref: ref, inc: inc, perMin: 30, mods: mods}), Current())
	if f.Shape.Kind != "ramp" {
		t.Errorf("shape = %+v onset = %+v", f.Shape, f.Onset)
	}
}

func TestFactsScopeOneRoute(t *testing.T) {
	f := ComputeFacts(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 500, routes: []string{"Orders.Save"}}), Current())
	if f.Scope.Kind != "one route" || len(f.Scope.Routes) != 1 || f.Scope.Routes[0] != "Orders.Save" {
		t.Errorf("scope = %+v", f.Scope)
	}
}

func TestFactsGuardsOnSmallGroups(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 6, ref: ref, inc: inc, perMin: 1})
	f := ComputeFacts(in, Current())
	// 60 reference calls: P95 shown, P99 withheld.
	if f.Ref.P95 == nil || f.Ref.P99 != nil {
		t.Errorf("guards: %+v", f.Ref.Percentiles)
	}
	small := Input{Incident: inc, Reference: ref, Audit: AuditPresent, Requests: in.Requests[:10]}
	f = ComputeFacts(small, Current())
	if f.Ref.P95 != nil || f.Incident.P95 != nil {
		t.Errorf("a P95 was shown for fewer than 20 calls: %+v %+v", f.Ref, f.Incident)
	}
}

func TestFactsIISMissingLeavesOUntested(t *testing.T) {
	ref, inc := stdWindows()
	f := ComputeFacts(gen(genCfg{seed: 7, ref: ref, inc: inc, perMin: 30}), Current())
	if f.Parts.OTested || f.Parts.OWhy == "" {
		t.Errorf("parts = %+v", f.Parts)
	}
	for _, it := range f.Parts.Items {
		if it.Name == "O" && (it.Tested || it.ExcessMs != nil || it.Share != nil) {
			t.Errorf("O was filled without IIS: %+v", it)
		}
	}
}
