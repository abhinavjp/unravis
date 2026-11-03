package investigate

import (
	"reflect"
	"regexp"
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
	wantSuspect(t, r, "S4", ResCannotTest, Grey) // needs driver data the audit does not hold: not checked, never ruled out
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

// wholePercent is the one percent form generated text may hold: a share such as "73%".
var wholePercent = regexp.MustCompile(`\b[0-9]{1,3}%`)

// assertWording fails on a causal word or a stray percent sign (only whole percentages such as "73%" are allowed) in any generated text.
func assertWording(t *testing.T, name string, r Result) {
	t.Helper()
	strs := map[string]string{}
	walkStrings(reflect.ValueOf(r), "", strs)
	for path, s := range strs {
		if err := evidence.LintCausal(s); err != nil {
			t.Errorf("%s %s: %q: %v", name, path, s, err)
		}
		if strings.Contains(wholePercent.ReplaceAllString(s, ""), "%") {
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

func TestTiersSixtySecondsApartAreTogether(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	in.LaneOnsets = map[string]time.Time{"sql": at(10, 20).Add(60 * time.Second)}
	r := Analyze(in)
	if r.Origin.Status != OriginNone {
		t.Fatalf("origin = %+v", r.Origin)
	}
	if !strings.Contains(r.Origin.ClockNote, "1 s") {
		t.Errorf("clock note = %q", r.Origin.ClockNote)
	}
	for _, s := range r.Suspects {
		if s.Colour == Red && s.Group != GroupQ {
			t.Errorf("%s is red although the tiers cannot be separated", s.ID)
		}
	}
}

func TestTiersTwoMinutesApartAreSeparable(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	in.LaneOnsets = map[string]time.Time{"sql": at(10, 20).Add(120 * time.Second)}
	r := Analyze(in)
	if r.Origin.Status != OriginNamed || r.Origin.Tier != "c" {
		t.Errorf("origin = %+v", r.Origin)
	}
}

func TestUnreadLaneBlocksALaterOrigin(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	in.Lanes = map[string]Tri{"D1": Yes, "H2": Yes}
	in.LaneSources = []string{"sql-snapshots"}
	// Without the unread lane, the application tier is the earliest and is named.
	base := Analyze(in)
	if base.Origin.Status != OriginNamed || base.Origin.Tier != "c" {
		t.Fatalf("baseline origin = %+v", base.Origin)
	}
	in.LaneUnread = map[string]string{"sql": "configured but gave no readable onset"}
	r := Analyze(in)
	if r.Origin.Status == OriginNamed {
		t.Fatalf("origin named although a configured lane was not read: %+v", r.Origin)
	}
	if r.Origin.Status != OriginNotEnough {
		t.Errorf("origin status = %s, want %s", r.Origin.Status, OriginNotEnough)
	}
	for _, s := range r.Suspects {
		if s.Colour == Red && s.Group != GroupQ {
			t.Errorf("%s is red although a configured lane was not read", s.ID)
		}
	}
}

func TestUnreadLaneNeedsAConfiguredLane(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	in.LaneOnsets = map[string]time.Time{"sql": at(10, 14)}
	in.LaneSources = []string{"sql-snapshots"}
	want := Analyze(in)
	in.LaneUnread = map[string]string{}
	if got := Analyze(in); !reflect.DeepEqual(got, want) {
		t.Error("an empty LaneUnread changed the result")
	}
	// A lane that has an onset is not unread even if the map names it.
	in.LaneUnread = map[string]string{"sql": "stale"}
	if got := Analyze(in); !reflect.DeepEqual(got, want) {
		t.Error("a dated lane was turned into an unread tier")
	}
}

func TestLaneAndAppTierThreeSecondsApartAreTogether(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	in.LaneOnsets = map[string]time.Time{"sql": at(10, 20).Add(3 * time.Second)}
	r := Analyze(in)
	if r.Origin.Status != OriginNone {
		t.Fatalf("origin = %+v", r.Origin)
	}
	if !strings.Contains(r.Origin.ClockNote, "1 s") || !strings.Contains(r.Origin.ClockNote, "Lane clocks (SQL, Mongo, host) are read at ±5 s.") {
		t.Errorf("clock note = %q", r.Origin.ClockNote)
	}
}

// A lane onset 63 s after an app-tier onset is together with the lane margin
// (60 s bucket + 5 s) but would have been separable with the app margin (61 s).
func TestLaneMarginIsWiderThanTheAppMargin(t *testing.T) {
	in := withLimit(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400}), 1000)
	in.LaneOnsets = map[string]time.Time{"sql": at(10, 20).Add(63 * time.Second)}
	r := Analyze(in)
	if r.Origin.Status != OriginNone {
		t.Fatalf("origin = %+v", r.Origin)
	}
	for _, s := range r.Suspects {
		if s.Colour == Red && s.Group != GroupQ {
			t.Errorf("%s is red although the tiers cannot be separated", s.ID)
		}
	}
	th := Current()
	if got := pairSep(th, "c", "a"); got != 61*time.Second {
		t.Errorf("app pair separation = %v, want 61s", got)
	}
	for _, pair := range [][2]string{{"sql", "c"}, {"c", "mongo"}, {"host", "load"}} {
		if got := pairSep(th, pair[0], pair[1]); got != 65*time.Second {
			t.Errorf("pairSep(%s, %s) = %v, want 65s", pair[0], pair[1], got)
		}
	}
}

func TestTwoAppTiersThreeSecondsApartStayOrdered(t *testing.T) {
	// App-tier onsets are read by the minute, so the closest two app tiers can sit is
	// one minute apart; two minutes apart they stay ordered exactly as before.
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 1, ref: ref, inc: inc, perMin: 30, iis: true, mods: []mod{
		{from: at(10, 20), to: at(11, 30), addA: 300},
		{from: at(10, 22), to: at(11, 30), addC: 400},
	}})
	r := Analyze(withLimit(in, 1000))
	if r.Origin.Status != OriginNamed || r.Origin.Tier != "a" {
		t.Fatalf("origin = %+v", r.Origin)
	}
	if strings.Contains(r.Origin.ClockNote, "Lane clocks") {
		t.Errorf("clock note mentions lanes with no lane dated: %q", r.Origin.ClockNote)
	}
}

// loadFirstScenario has the arrival rate moving first (three times the calls from
// 10:10) and the action time ten minutes later. With retries on, half of the calls
// repeat themselves 2 s later from 10:10.
func loadFirstScenario(retries bool) Input {
	ref, inc := stdWindows()
	cfg := genCfg{seed: 7, ref: ref, inc: inc, perMin: 30, iis: true, clients: 5000,
		mods: []mod{{from: at(10, 10), to: at(11, 30), countMul: 3}, {from: at(10, 20), to: at(11, 30), addC: 400}}}
	if retries {
		cfg.retryEvery = 2
		cfg.retryWindow = Window{at(10, 10), at(11, 30)}
	}
	return withLimit(gen(cfg), 1000)
}

func TestRetriesBeforeLoadAreNotNamedAsLoad(t *testing.T) {
	r := Analyze(loadFirstScenario(true))
	if r.Signals.Flags[SigRetriesUp].Value != Yes {
		t.Fatalf("retries signal = %+v", r.Signals.Flags[SigRetriesUp])
	}
	if r.Origin.Status != OriginNone || r.Origin.Tier != "" || r.Origin.Label != "" || r.Origin.At != nil {
		t.Fatalf("origin = %+v", r.Origin)
	}
	if !strings.Contains(r.Origin.Note, "Load rose after clients started retrying or failing; that is retrying, not new demand.") {
		t.Errorf("note = %q", r.Origin.Note)
	}
	for _, tr := range r.Origin.Roles {
		if tr.Role == RoleOrigin {
			t.Errorf("%s is still the origin", tr.Tier)
		}
		if tr.Tier == "load" && tr.Role != RoleTogether {
			t.Errorf("load role = %s, want %s", tr.Role, RoleTogether)
		}
	}
}

func TestLoadStillNamedWithoutRetries(t *testing.T) {
	r := Analyze(loadFirstScenario(false))
	if r.Signals.Flags[SigRetriesUp].Value == Yes || r.Signals.Flags[SigErrorsPrecede].Value == Yes {
		t.Fatalf("flags = %+v / %+v", r.Signals.Flags[SigRetriesUp], r.Signals.Flags[SigErrorsPrecede])
	}
	if r.Origin.Status != OriginNamed || r.Origin.Tier != "load" {
		t.Errorf("origin = %+v", r.Origin)
	}
}

func TestRetriesBeforeLoadGiveOrangeNotRed(t *testing.T) {
	r := Analyze(loadFirstScenario(true))
	orange := 0
	for _, s := range r.Suspects {
		if s.Tier != "load" || s.Result != ResConsistent {
			continue
		}
		if s.Colour == Red {
			t.Errorf("%s (load) is red although retries came first", s.ID)
		}
		if s.Colour == Orange {
			orange++
		}
	}
	if orange == 0 {
		t.Error("no consistent load suspect was found to check")
	}
	wantSuspect(t, r, "L1", ResConsistent, Orange)
}

func TestLoadSuspectStillRedWithoutRetries(t *testing.T) {
	r := Analyze(loadFirstScenario(false))
	wantSuspect(t, r, "L1", ResConsistent, Red)
}

// loadFirstErrors is loadFirstScenario without retries, with every call from errFrom
// on a server error: errors, not retries, are the only thing that could explain load.
func loadFirstErrors(errFrom time.Time) Input {
	ref, inc := stdWindows()
	cfg := genCfg{seed: 7, ref: ref, inc: inc, perMin: 30, iis: true, clients: 5000, serverErrorFrom: errFrom,
		mods: []mod{{from: at(10, 10), to: at(11, 30), countMul: 3}, {from: at(10, 20), to: at(11, 30), addC: 400}}}
	return withLimit(gen(cfg), 1000)
}

// The errors half of the guard stands on its own: with retries off, a first server
// error before the load onset still means load is not named as the origin.
func TestErrorsBeforeLoadAreNotNamedAsLoad(t *testing.T) {
	r := Analyze(loadFirstErrors(at(10, 5)))
	if r.Signals.Flags[SigRetriesUp].Value == Yes {
		t.Fatalf("retries signal = %+v, want not yes", r.Signals.Flags[SigRetriesUp])
	}
	if r.Signals.Flags[SigErrorsPrecede].Value != Yes {
		t.Fatalf("errors signal = %+v", r.Signals.Flags[SigErrorsPrecede])
	}
	if r.Origin.Status != OriginNone || r.Origin.Tier != "" || r.Origin.At != nil {
		t.Fatalf("origin = %+v", r.Origin)
	}
	if !strings.Contains(r.Origin.Note, "Load rose after clients started retrying or failing; that is retrying, not new demand.") {
		t.Errorf("note = %q", r.Origin.Note)
	}
	wantSuspect(t, r, "L1", ResConsistent, Orange)
}

// A first server error after the load onset (but before t0) does not explain load:
// load moved first and stays the origin, and its suspect stays red.
func TestErrorsAfterLoadOnsetStillNameLoad(t *testing.T) {
	r := Analyze(loadFirstErrors(at(10, 15)))
	if r.Signals.Flags[SigErrorsPrecede].Value != Yes {
		t.Fatalf("errors signal = %+v, want yes (first error is before t0)", r.Signals.Flags[SigErrorsPrecede])
	}
	if r.Origin.Status != OriginNamed || r.Origin.Tier != "load" {
		t.Fatalf("origin = %+v", r.Origin)
	}
	wantSuspect(t, r, "L1", ResConsistent, Red)
}

// L3 needs retries up, so when it is consistent load follows retries and it is
// orange, never red.
func TestRetryStormSuspectIsNeverRed(t *testing.T) {
	r := Analyze(loadFirstScenario(true))
	s := suspect(t, r, "L3")
	if s.Result == ResConsistent && s.Colour == Red {
		t.Errorf("L3 is red although it needs retries: %s", s.Why)
	}
	if s.Result == ResConsistent {
		wantSuspect(t, r, "L3", ResConsistent, Orange)
	}
	if loadFollowsRetries(r.Signals, r.Facts) != true {
		t.Error("loadFollowsRetries = false with retries up")
	}
}

func TestLoadFollowsRetriesErrorsHalf(t *testing.T) {
	yes := func(v Tri) Signals {
		return Signals{Flags: map[string]Signal{SigRetriesUp: {Value: No}, SigErrorsPrecede: {Value: v}}}
	}
	load, first := at(10, 10), at(10, 5)
	f := Facts{Tiers: []TierOnset{{Tier: tierLoad, At: &load}}}
	f.Errors.FirstIncServer = &first
	if !loadFollowsRetries(yes(Yes), f) {
		t.Error("error before load onset should explain load")
	}
	late := at(10, 12)
	f.Errors.FirstIncServer = &late
	if loadFollowsRetries(yes(Yes), f) {
		t.Error("error after load onset should not explain load")
	}
	sameMinute := load.Add(30 * time.Second)
	f.Errors.FirstIncServer = &sameMinute
	if !loadFollowsRetries(yes(Yes), f) {
		t.Error("error in the same minute as the load onset should count as at or before")
	}
	f.Tiers[0].At = nil
	f.Errors.FirstIncServer = &late
	if !loadFollowsRetries(yes(Yes), f) {
		t.Error("unknown load onset should keep the signal as it stands")
	}
	if loadFollowsRetries(yes(No), f) {
		t.Error("errors signal no should not explain load")
	}
}

func TestHeldBackWordingSeparatesDroppedFromUnread(t *testing.T) {
	f := Facts{Tiers: []TierOnset{
		{Tier: "load", Why: notPersistedWhy},
		{Tier: "a", Why: "gap"},
		{Tier: "c", Why: "tested", Tested: true},
	}}
	unread, dropped := heldBackTiers(f)
	if len(unread) != 1 || unread[0] != "a" || len(dropped) != 1 || dropped[0] != "load" {
		t.Fatalf("unread = %v, dropped = %v", unread, dropped)
	}
	if got := heldBackClause(nil, dropped); got != "Arrival rate had its onset set aside because it did not stay up" {
		t.Errorf("dropped clause = %q", got)
	}
	if got := heldBackClause(unread, nil); !strings.HasSuffix(got, "could not be read in every minute") || strings.Contains(got, "set aside") {
		t.Errorf("unread clause = %q", got)
	}
	both := heldBackClause(unread, dropped)
	if !strings.Contains(both, "could not be read in every minute, and") || !strings.Contains(both, "set aside") {
		t.Errorf("mixed clause = %q", both)
	}
}

func TestDroppedOnsetNoteSaysSetAside(t *testing.T) {
	// One dated tier, one other whose onset was dropped: the note must not say it
	// "could not be read".
	c := at(10, 20)
	f := Facts{Onset: Onset{Status: "found"}, Tiers: []TierOnset{
		{Tier: "c", At: &c},
		{Tier: "load", Why: notPersistedWhy},
	}}
	o := ComputeOrigin(Input{Audit: AuditPresent}, f, Signals{Flags: map[string]Signal{}}, Current())
	if o.Status != OriginNotEnough {
		t.Fatalf("origin = %+v", o)
	}
	if strings.Contains(o.Note, "could not be read") || !strings.Contains(o.Note, "set aside") {
		t.Errorf("note = %q", o.Note)
	}
}
