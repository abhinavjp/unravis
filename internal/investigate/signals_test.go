package investigate

import "testing"

func sigs(in Input) (Facts, Signals) {
	th := Current()
	f := ComputeFacts(in, th)
	return f, ComputeSignals(in, f, th)
}

func want(t *testing.T, s Signals, name string, v Tri) {
	t.Helper()
	if got := s.Flags[name]; got.Value != v {
		t.Errorf("%s = %s (%s), want %s", name, got.Value, got.Why, v)
	}
}

func TestSignalsCStep(t *testing.T) {
	in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400})
	_, s := sigs(in)
	want(t, s, SigP95Up, Yes)
	want(t, s, SigCLarge, Yes)
	want(t, s, SigALarge, No)
	want(t, s, SigOLarge, No)
	want(t, s, SigOOrALarge, No)
	want(t, s, SigArrivalsUp, No)
	want(t, s, SigScopeAll, Yes)
	want(t, s, SigScopeNarrow, No)
	want(t, s, SigRetriesUp, No)
	want(t, s, SigClockAnomaly, No)
	want(t, s, SigAuditDip, No)
	if s.Split.Dominant != "C" {
		t.Errorf("split = %+v", s.Split)
	}
}

func TestSignalsPoolNeedsLimit(t *testing.T) {
	in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 3000})
	_, s := sigs(in)
	want(t, s, SigLAtLimit, Unknown)
	if s.Flags[SigLAtLimit].Missing != "pool limit" || s.Pool.Entered {
		t.Errorf("pool = %+v flag = %+v", s.Pool, s.Flags[SigLAtLimit])
	}
	lim := 100
	in.PoolLimit = &lim
	_, s = sigs(in)
	want(t, s, SigPoolWouldFill, Yes)
	want(t, s, SigLAtLimit, Yes)
	lim = 1000
	_, s = sigs(in)
	want(t, s, SigPoolWouldFill, No)
}

func TestSignalsTimeline(t *testing.T) {
	in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400})
	_, s := sigs(in)
	want(t, s, SigChangesLead, Unknown)
	if s.Flags[SigChangesLead].Missing != "releases" {
		t.Errorf("missing = %q", s.Flags[SigChangesLead].Missing)
	}
	in.ReleasesEntered = true
	_, s = sigs(in)
	want(t, s, SigChangesLead, No)
	if s.Timeline.Caveat == "" {
		t.Error("the typed-list caveat is missing")
	}
	in.Releases = []Release{{At: at(10, 40), Label: "after"}, {At: at(8, 0), Label: "early"}, {At: at(10, 5), Label: "lead"}}
	_, s = sigs(in)
	want(t, s, SigChangesLead, Yes)
	if len(s.Timeline.Leading) != 1 || s.Timeline.Leading[0] != "lead" {
		t.Errorf("leading = %v", s.Timeline.Leading)
	}
	if s.Timeline.Items[0].Label != "early" || s.Timeline.Items[0].Relation != "long before the start" ||
		s.Timeline.Items[2].Relation != "after the start" {
		t.Errorf("items = %+v", s.Timeline.Items)
	}
}

func TestSignalsRetriesAndErrors(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 9, ref: ref, inc: inc, perMin: 30, clients: 5000,
		mods:            []mod{{from: at(10, 20), to: at(11, 30), addC: 400}},
		retryEvery:      2,
		retryWindow:     Window{at(10, 20), at(11, 30)},
		serverErrorFrom: at(10, 20)})
	_, s := sigs(in)
	want(t, s, SigRetriesUp, Yes)
	want(t, s, SigErrorsPrecede, Yes)
	in2 := gen(genCfg{seed: 9, ref: ref, inc: inc, perMin: 30, clients: 5000,
		mods:            []mod{{from: at(10, 20), to: at(11, 30), addC: 400}},
		serverErrorFrom: at(10, 40)})
	_, s = sigs(in2)
	want(t, s, SigErrorsPrecede, No)
}

func TestSignalsAuditMissing(t *testing.T) {
	ref, inc := stdWindows()
	in := Input{Incident: inc, Reference: ref, Audit: AuditMissing}
	_, s := sigs(in)
	for _, n := range []string{SigArrivalsUp, SigP95Up, SigCLarge, SigScopeAll, SigRetriesUp} {
		want(t, s, n, Unknown)
		if s.Flags[n].Missing != "audit" {
			t.Errorf("%s missing = %q", n, s.Flags[n].Missing)
		}
	}
}

func TestSignalsIISMissingGivesUnknownO(t *testing.T) {
	ref, inc := stdWindows()
	_, s := sigs(gen(genCfg{seed: 7, ref: ref, inc: inc, perMin: 30, mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}}}))
	want(t, s, SigOLarge, Unknown)
	want(t, s, SigOOrALarge, Unknown)
	if s.Flags[SigOLarge].Missing != "iis" {
		t.Errorf("missing = %q", s.Flags[SigOLarge].Missing)
	}
	want(t, s, SigAuditDip, Unknown)
}

func TestSignalsAuditDip(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 11, ref: ref, inc: inc, perMin: 30, iis: true,
		auditDrop: []Window{{at(10, 30), at(11, 30)}}})
	f, s := sigs(in)
	want(t, s, SigAuditDip, Yes)
	if f.Coverage.RefShare == nil || *f.Coverage.RefShare < 0.99 {
		t.Errorf("coverage = %+v", f.Coverage)
	}
}
