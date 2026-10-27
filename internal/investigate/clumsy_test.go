package investigate

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Engine-level clumsy-user tests: odd, thin or contradictory input. Whatever goes
// in, the engine must answer with a readiness word for every step, never panic,
// never produce a number JSON cannot carry (NaN, Inf), never speak in causes, and
// never show a pass or a finger-pointing colour on data that cannot support one.

func isReadinessWord(r Readiness) bool {
	switch r {
	case Ready, PartlyReady, CannotTestYet, Failed:
		return true
	}
	return false
}

// checkSane holds for every input.
func checkSane(t *testing.T, name string, in Input) Result {
	t.Helper()
	var res Result
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("%s: panic: %v", name, p)
			}
		}()
		res = Analyze(in)
	}()
	if len(res.Steps) != StepCount {
		t.Fatalf("%s: %d steps", name, len(res.Steps))
	}
	for i, s := range res.Steps {
		if !isReadinessWord(s.Readiness) {
			t.Errorf("%s: step %d readiness %q is not one of the four words", name, i, s.Readiness)
		}
	}
	if _, err := json.Marshal(res); err != nil {
		t.Errorf("%s: result does not marshal: %v", name, err)
	}
	if res.Version != Version {
		t.Errorf("%s: version %q", name, res.Version)
	}
	assertWording(t, name, res)
	return res
}

// noPointing fails when a non-quality suspect is red or orange.
func noPointing(t *testing.T, name string, res Result) {
	t.Helper()
	for _, s := range res.Suspects {
		if s.Group != GroupQ && (s.Colour == Red || s.Colour == Orange) {
			t.Errorf("%s: %s is %s: %s", name, s.ID, s.Colour, s.Why)
		}
	}
	if res.Origin.Status == OriginNamed {
		t.Errorf("%s: origin named: %+v", name, res.Origin)
	}
}

func req(id string, start time.Time, durMs float64) Request {
	end := start.Add(time.Duration(durMs * float64(time.Millisecond)))
	a, c, p := 10.0, durMs-10, 5.0
	if c < 0 {
		c = 0
	}
	return Request{ID: id, Route: "Orders.Get", Start: start, End: &end, A: &a, C: &c, P: &p,
		Path: NormalizePath("/api/Orders.Get"), Client: "u1", Outcome: "success"}
}

func TestClumsyEmptyCase(t *testing.T) {
	ref, inc := stdWindows()
	cases := map[string]Input{
		"zero value":            {},
		"no windows":            {Audit: AuditPresent},
		"windows, audit absent": {Incident: inc, Reference: ref, Audit: AuditMissing},
		"windows, audit failed": {Incident: inc, Reference: ref, Audit: AuditFailed},
		"windows, no calls":     {Incident: inc, Reference: ref, Audit: AuditPresent, ReleasesEntered: true},
		"IIS but no audit":      {Incident: inc, Reference: ref, Audit: AuditMissing, IISPresent: true},
		"windows reversed":      {Incident: Window{inc.End, inc.Start}, Reference: ref, Audit: AuditPresent},
		"windows overlap":       {Incident: inc, Reference: Window{at(9, 30), at(10, 30)}, Audit: AuditPresent},
	}
	for name, in := range cases {
		res := checkSane(t, name, in)
		noPointing(t, name, res)
		for i, s := range res.Steps {
			if s.Readiness == Ready {
				t.Errorf("%s: step %d is Ready on an empty case", name, i)
			}
		}
		if res.Confidence.Word != ConfidenceNone {
			t.Errorf("%s: confidence %q", name, res.Confidence.Word)
		}
		for _, s := range res.Suspects {
			if s.Colour == Green {
				t.Errorf("%s: %s is green (ruled out) with nothing read", name, s.ID)
			}
		}
	}
}

func TestClumsyOneRequestOnly(t *testing.T) {
	ref, inc := stdWindows()
	for name, in := range map[string]Input{
		"in the incident":  {Incident: inc, Reference: ref, Audit: AuditPresent, Requests: []Request{req("a", at(10, 30), 9000)}},
		"in the reference": {Incident: inc, Reference: ref, Audit: AuditPresent, Requests: []Request{req("a", at(9, 30), 90)}},
		"one in each": {Incident: inc, Reference: ref, Audit: AuditPresent,
			Requests: []Request{req("a", at(9, 30), 90), req("b", at(10, 30), 9000)}},
	} {
		res := checkSane(t, name, in)
		noPointing(t, name, res)
		if res.Facts.Onset.Status == "found" {
			t.Errorf("%s: an onset was found from one call", name)
		}
		if res.Steps[2].Readiness == Ready || res.Steps[3].Readiness == Ready {
			t.Errorf("%s: steps 2/3 are Ready on one call", name)
		}
		if res.Facts.Incident.P95 != nil && res.Facts.Incident.N < GroupSizeP95Floor(res) {
			t.Errorf("%s: a P95 is shown for %d call(s)", name, res.Facts.Incident.N)
		}
		if res.Confidence.Word == ConfidenceHigh || res.Confidence.Word == ConfidenceMedium {
			t.Errorf("%s: confidence %q on one call", name, res.Confidence.Word)
		}
	}
}

// GroupSizeP95Floor is the group size the result itself quotes.
func GroupSizeP95Floor(res Result) int { return res.Thresholds.GroupSizeP95 }

func TestClumsyAllRequestsAtTheSameMillisecond(t *testing.T) {
	ref, inc := stdWindows()
	t0 := at(10, 30)
	var rs []Request
	for i := 0; i < 600; i++ {
		rs = append(rs, req(fmt.Sprintf("a%d", i), t0, 120))
	}
	in := Input{Incident: inc, Reference: ref, Audit: AuditPresent, Requests: rs}
	res := checkSane(t, "same ms, incident only", in)
	noPointing(t, "same ms, incident only", res)
	if res.Facts.Onset.Status == "found" {
		t.Errorf("onset found when every call is in one minute: %+v", res.Facts.Onset)
	}

	// The same instant in both windows: both groups are one point.
	var both []Request
	for i := 0; i < 300; i++ {
		both = append(both, req(fmt.Sprintf("r%d", i), at(9, 30), 100))
		both = append(both, req(fmt.Sprintf("i%d", i), at(10, 30), 100))
	}
	res = checkSane(t, "same ms, both windows", Input{Incident: inc, Reference: ref, Audit: AuditPresent, Requests: both})
	noPointing(t, "same ms, both windows", res)
	if res.Facts.Onset.Status == "found" {
		t.Errorf("onset found with identical windows")
	}

	// Zero-length calls: start equals end.
	var zero []Request
	for i := 0; i < 200; i++ {
		zero = append(zero, req(fmt.Sprintf("z%d", i), at(9, 30).Add(time.Duration(i)*time.Second), 0))
		zero = append(zero, req(fmt.Sprintf("y%d", i), at(10, 30).Add(time.Duration(i)*time.Second), 0))
	}
	res = checkSane(t, "zero length", Input{Incident: inc, Reference: ref, Audit: AuditPresent, Requests: zero})
	noPointing(t, "zero length", res)
}

// Two hosts whose clocks differ by 2 s: the audit says one thing, the IIS file
// another. A 2 s skew is inside the 3 s match tolerance, so calls still pair up; a
// skew of 5 s is outside it, so outside time cannot be read and must not read as
// zero or as flat.
func TestClumsyClocksTwoSecondsApart(t *testing.T) {
	ref, inc := stdWindows()
	build := func(skew time.Duration, mods ...mod) Input {
		in := gen(genCfg{seed: 31, ref: ref, inc: inc, perMin: 30, iis: true, mods: mods})
		for i := range in.IIS {
			in.IIS[i].Start = in.IIS[i].Start.Add(skew)
			in.IIS[i].End = in.IIS[i].End.Add(skew)
		}
		return in
	}
	step := mod{from: at(10, 20), to: at(11, 30), addC: 400}

	near := checkSane(t, "skew 2s", build(2*time.Second, step))
	if near.Facts.Parts.IISMatchRate == nil || *near.Facts.Parts.IISMatchRate < 0.9 {
		t.Errorf("a 2 s skew broke the IIS match: %+v", near.Facts.Parts.IISMatchRate)
	}
	if !near.Facts.Parts.OTested {
		t.Errorf("outside time untested after a 2 s skew: %s", near.Facts.Parts.OWhy)
	}

	far := checkSane(t, "skew 5s", build(5*time.Second, step))
	if far.Facts.Parts.OTested {
		t.Errorf("outside time read although IIS and audit are 5 s apart")
	}
	for _, id := range []string{"S1", "S2"} {
		s := findSuspect(far, id)
		if s == nil || s.Result == ResContra || s.Colour == Green {
			t.Errorf("%s = %+v: an unmatched IIS file must not rule it out", id, s)
		}
	}

	// IIS rows that end before they start (a clock run backwards) are tolerated.
	back := build(0, step)
	for i := range back.IIS {
		if i%7 == 0 {
			back.IIS[i].End = back.IIS[i].Start.Add(-time.Second)
		}
	}
	checkSane(t, "iis backwards", back)

	// Tiers that moved 2 s apart cannot be put in order: no origin is named.
	in := gen(genCfg{seed: 32, ref: ref, inc: inc, perMin: 30, iis: true,
		mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 300, addO: 300}}})
	res := checkSane(t, "tiers together", in)
	if res.Origin.Status == OriginNamed {
		// C and O both moved at 10:20 to the minute: not separable.
		t.Errorf("origin named for tiers that moved together: %+v", res.Origin)
	}
	lanes := gen(genCfg{seed: 33, ref: ref, inc: inc, perMin: 30, iis: true,
		mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}}})
	lanes.LaneOnsets = map[string]time.Time{"sql": at(10, 20).Add(2 * time.Second)}
	lanes.LaneSources = []string{"sqlserver-xe"}
	res = checkSane(t, "lane 2s apart", lanes)
	if res.Origin.Status == OriginNamed && res.Origin.Tier == "c" {
		t.Errorf("C named first although SQL moved 2 s later: %+v", res.Origin)
	}
}

// The reference window comes after the incident window: it may already carry the
// same slowness, so the comparison is flagged and nothing is pointed at.
func TestClumsyReferenceAfterTheIncident(t *testing.T) {
	ref, inc := stdWindows()
	// "Incident" is the quiet morning hour; "reference" is the slow stretch.
	in := gen(genCfg{seed: 41, ref: inc, inc: ref, perMin: 30, iis: true,
		mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}}})
	in.Incident, in.Reference = ref, inc
	res := checkSane(t, "reference after", in)
	if res.Steps[0].Readiness != PartlyReady {
		t.Errorf("step 0 = %q, want Partly ready with the reference after the incident", res.Steps[0].Readiness)
	}
	found := false
	for _, r := range res.Steps[0].Reasons {
		if len(r) > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("step 0 gives no reason")
	}
	noPointing(t, "reference after", res)
}

// A mix of everything awkward at once must still give a sane answer.
func TestClumsyEverythingAtOnce(t *testing.T) {
	ref, inc := stdWindows()
	in := gen(genCfg{seed: 51, ref: ref, inc: inc, perMin: 30, iis: true,
		mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}},
		gaps: []Window{{at(10, 50), at(11, 5)}}})
	in.NoStart = 12
	in.Route = "Not.There"
	lim := 0
	in.PoolLimit = &lim
	in.Releases = []Release{{At: at(10, 19), Label: "<script>alert(1)</script> {{x}} ‮"}}
	in.ReleasesEntered = true
	in.Requests = append(in.Requests, req("neg", at(10, 40), -50), req("huge", at(10, 41), 9e8))
	res := checkSane(t, "everything", in)
	if res.Facts.Counts.Anomalous < 2 {
		t.Errorf("anomalous calls counted: %d, want at least 2", res.Facts.Counts.Anomalous)
	}
}
