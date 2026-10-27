package investigate

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
)

func suspect(t *testing.T, r Result, id string) Suspect {
	t.Helper()
	for _, s := range r.Suspects {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("suspect %s not in the result", id)
	return Suspect{}
}

func wantSuspect(t *testing.T, r Result, id string, res SuspectResult, c Colour) {
	t.Helper()
	s := suspect(t, r, id)
	if s.Result != res || s.Colour != c {
		t.Errorf("%s = %s/%s, want %s/%s: %s", id, s.Result, s.Colour, res, c, s.Why)
	}
}

func withLimit(in Input, n int) Input {
	in.PoolLimit = &n
	return in
}

func TestEvaluateCStepNamesThePoolSuspect(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 10)
	in.ReleasesEntered = true
	r := Analyze(in)
	wantSuspect(t, r, "S3", ResConsistent, Red)
	wantSuspect(t, r, "L2", ResContra, Green)
	wantSuspect(t, r, "S1", ResContra, Green)
	wantSuspect(t, r, "S5", ResContra, Green)
	wantSuspect(t, r, "L1", ResContra, Green)
	wantSuspect(t, r, "S4", ResContra, Green) // A did not rise, so the audit contradicts it
	wantSuspect(t, r, "D1", ResCannotTest, Grey)
	wantSuspect(t, r, "Q1", ResPartly, Grey)
	wantSuspect(t, r, "C1", ResPartly, Grey)
	wantSuspect(t, r, "Q2", ResContra, Green)
	for _, id := range []string{"H2", "M2", "H3"} {
		wantSuspect(t, r, id, ResCannotTest, Grey)
	}
	for _, s := range r.Suspects {
		if s.ID == "M3" || s.ID == "M4" {
			t.Errorf("%s is not applicable in production and must not be ranked", s.ID)
		}
	}
	if len(r.NotApplicable) != 2 {
		t.Errorf("not applicable = %+v", r.NotApplicable)
	}
	if r.Suspects[0].ID != "S3" || r.Suspects[0].Rank != 1 {
		t.Errorf("first suspect = %s rank %d", r.Suspects[0].ID, r.Suspects[0].Rank)
	}
	// The order is state group first: consistent, partly, cannot test yet, contradicted.
	last := -1
	for _, s := range r.Suspects {
		if sr := stateRank(s.Result); sr < last {
			t.Fatalf("%s (%s) is out of order", s.ID, s.Result)
		} else {
			last = sr
		}
	}
	if r.Version != Version || r.Thresholds.Version != Version || r.Thresholds.ChangeMinMs != 100 {
		t.Errorf("thresholds not returned: %+v", r.Thresholds.Version)
	}
}

func TestEvaluateAuditSetUpPointsAtAuditWrite(t *testing.T) {
	r := Analyze(stepScenario(mod{from: at(10, 20), to: at(11, 30), addA: 400}))
	wantSuspect(t, r, "S5", ResConsistent, Red)
	wantSuspect(t, r, "S3", ResContra, Green)
}

func TestEvaluateOutsideTimePointsAtQueueSuspects(t *testing.T) {
	r := Analyze(stepScenario(mod{from: at(10, 20), to: at(11, 30), addO: 400}))
	wantSuspect(t, r, "S1", ResConsistent, Red)
	wantSuspect(t, r, "S2", ResConsistent, Red)
	wantSuspect(t, r, "S3", ResContra, Green)
}

func TestEvaluateSurgeIsLoad(t *testing.T) {
	r := Analyze(stepScenario(mod{from: at(10, 20), to: at(11, 30), countMul: 3}))
	s := suspect(t, r, "L1")
	if s.Result != ResConsistent {
		t.Errorf("L1 = %s: %s", s.Result, s.Why)
	}
}

func TestEvaluateNoIncidentRanksNothing(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 21, ref: ref, inc: inc, perMin: 30, iis: true})
	r := Analyze(in)
	for _, s := range r.Suspects {
		if s.Colour == Red || s.Colour == Orange {
			if s.Group != GroupQ {
				t.Errorf("%s is %s on a day with no incident", s.ID, s.Colour)
			}
		}
	}
	if r.Origin.Status == OriginNamed {
		t.Errorf("origin named with no incident: %+v", r.Origin)
	}
	if !strings.Contains(r.Headline, "No meaningful slowdown") {
		t.Errorf("headline = %q", r.Headline)
	}
	if r.Confidence.Word != ConfidenceNone {
		t.Errorf("confidence = %s", r.Confidence.Word)
	}
}

func TestMissingDataNeverContradicts(t *testing.T) {
	ref, inc := stdWindows()
	r := Analyze(Input{Incident: inc, Reference: ref, Audit: AuditMissing})
	for _, s := range r.Suspects {
		if s.Result == ResContra || s.Result == ResConsistent {
			t.Errorf("%s reads %s with no data at all", s.ID, s.Result)
		}
		if s.Colour == Green || s.Colour == Red || s.Colour == Orange {
			t.Errorf("%s is %s with no data at all", s.ID, s.Colour)
		}
	}
	// Without IIS, S1 is untested, not ruled out.
	rr, ii := stdWindows()
	in := gen(genCfg{seed: 4, ref: rr, inc: ii, perMin: 30, mods: []mod{{from: at(10, 20), to: at(11, 30), addO: 400}}})
	res := Analyze(in)
	wantSuspect(t, res, "S1", ResCannotTest, Grey)
	if !strings.Contains(suspect(t, res, "S1").Why, "no IIS data") {
		t.Errorf("S1 why = %q", suspect(t, res, "S1").Why)
	}
}

func TestLaneOnsetsMakeTheOrigin(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	in.Lanes = map[string]Tri{"D1": Yes, "H2": Yes}
	in.LaneOnsets = map[string]time.Time{"sql": at(10, 14)}
	in.LaneSources = []string{"sql-snapshots"}
	r := Analyze(in)
	if r.Origin.Status != OriginNamed || r.Origin.Tier != "sql" {
		t.Fatalf("origin = %+v", r.Origin)
	}
	roles := map[string]string{}
	for _, tr := range r.Origin.Roles {
		roles[tr.Tier] = tr.Role
	}
	if roles["sql"] != RoleOrigin || roles["c"] != RoleVictim {
		t.Errorf("roles = %v", roles)
	}
	wantSuspect(t, r, "D1", ResConsistent, Red)
	wantSuspect(t, r, "H2", ResCannotTest, Grey) // grey in Release 1 whatever a lane says
	if r.Steps[10].Readiness == Ready && r.Steps[8].Readiness != Ready {
		t.Errorf("step 10 is Ready while step 8 is %s", r.Steps[8].Readiness)
	}
}

func TestNoClearOriginWhenTiersMoveTogether(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	in.LaneOnsets = map[string]time.Time{"sql": at(10, 20).Add(30 * time.Second)}
	r := Analyze(in)
	if r.Origin.Status != OriginNone {
		t.Fatalf("origin = %+v", r.Origin)
	}
	if !strings.Contains(r.Origin.ClockNote, "1 s") {
		t.Errorf("clock note = %q", r.Origin.ClockNote)
	}
	if r.Confidence.Word != ConfidenceLow && r.Confidence.Word != ConfidenceNone {
		t.Errorf("confidence = %s", r.Confidence.Word)
	}
	for _, s := range r.Suspects {
		if s.Colour == Red && s.Group != GroupQ {
			t.Errorf("%s is red although the tiers cannot be separated", s.ID)
		}
	}
}

func TestTiersFarApartAreSeparable(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	in.LaneOnsets = map[string]time.Time{"sql": at(10, 20).Add(5 * time.Minute)}
	r := Analyze(in)
	if r.Origin.Status != OriginNamed || r.Origin.Tier != "c" {
		t.Errorf("origin = %+v", r.Origin)
	}
}

func TestConfidenceWords(t *testing.T) {
	// Audit alone: Low at best.
	ref, inc := stdWindows()
	auditOnly := gen(genCfg{seed: 31, ref: ref, inc: inc, perMin: 30, mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400, countMul: 3}}})
	r := Analyze(auditOnly)
	if r.Origin.Status == OriginNamed && r.Confidence.Word != ConfidenceLow {
		t.Errorf("audit alone gave %s", r.Confidence.Word)
	}
	// With IIS: Medium when an origin is named.
	withIIS := stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400})
	withIIS.LaneOnsets = map[string]time.Time{"sql": at(10, 40)}
	r = Analyze(withIIS)
	if r.Origin.Status != OriginNamed || r.Confidence.Word != ConfidenceMedium {
		t.Errorf("origin %+v confidence %+v", r.Origin, r.Confidence)
	}
	if r.Confidence.Overturn == "" || len(r.Confidence.SourcesUsed) == 0 || len(r.Confidence.SourcesMissing) == 0 {
		t.Errorf("panel is incomplete: %+v", r.Confidence)
	}
}

func TestAnalyzeIsDeterministic(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 10)
	a, b := Analyze(in), Analyze(in)
	if !reflect.DeepEqual(a, b) {
		t.Error("two runs on the same input differ")
	}
	if a.SeriesMinutes == 0 || a.Compact().Facts.Series != nil {
		t.Errorf("series: %d, compact keeps %d", a.SeriesMinutes, len(a.Compact().Facts.Series))
	}
}

// walkStrings collects every string in a value, with the path of each.
func walkStrings(v reflect.Value, path string, out map[string]string) {
	switch v.Kind() {
	case reflect.String:
		out[path] = v.String()
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				walkStrings(v.Field(i), path+"."+v.Type().Field(i).Name, out)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkStrings(v.Index(i), path+"[]", out)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walkStrings(v.MapIndex(k), path+"{}", out)
		}
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			walkStrings(v.Elem(), path, out)
		}
	}
}

// assertWording fails on a causal word or a percent sign in any generated text.
func assertWording(t *testing.T, name string, r Result) {
	t.Helper()
	strs := map[string]string{}
	walkStrings(reflect.ValueOf(r), "", strs)
	for path, s := range strs {
		if err := evidence.LintCausal(s); err != nil {
			t.Errorf("%s %s: %q: %v", name, path, s, err)
		}
		if strings.Contains(s, "%") {
			t.Errorf("%s %s: %q holds a percent sign", name, path, s)
		}
	}
}

func TestGeneratedWordingIsNonCausal(t *testing.T) {
	ref, inc := stdWindows()
	lim := func(in Input) Input { return withLimit(in, 10) }
	cases := map[string]Input{
		"c step":  lim(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400})),
		"a step":  stepScenario(mod{from: at(10, 20), to: at(11, 30), addA: 400}),
		"o step":  stepScenario(mod{from: at(10, 20), to: at(11, 30), addO: 400}),
		"surge":   stepScenario(mod{from: at(10, 20), to: at(11, 30), countMul: 3}),
		"no data": {Incident: inc, Reference: ref, Audit: AuditMissing},
		"quiet":   gen(genCfg{seed: 21, ref: ref, inc: inc, perMin: 30, iis: true}),
		"zero":    {},
		"released": func() Input {
			in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400})
			in.ReleasesEntered = true
			in.Releases = []Release{{At: at(10, 5), Label: "build 42"}}
			return in
		}(),
		"lanes": func() Input {
			in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400})
			in.Lanes = map[string]Tri{"D1": Yes, "M1": No}
			in.LaneOnsets = map[string]time.Time{"sql": at(10, 15)}
			return in
		}(),
	}
	for name, in := range cases {
		assertWording(t, name, Analyze(in))
	}
}
