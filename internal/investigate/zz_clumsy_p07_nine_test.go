package investigate

import (
	"math"
	"strings"
	"testing"
	"time"
)

var clumsyNineIDs = []string{"S8", "S9", "S10", "S11", "S12", "D16", "D17", "M11", "M12"}

func clumsyNever(t *testing.T, name string, r Result, ids ...string) {
	t.Helper()
	for _, id := range ids {
		s := suspect(t, r, id)
		if s.Colour == Green || s.Result == ResContra {
			t.Errorf("%s: %s = %s/%s (%s), want never a No from missing data", name, id, s.Result, s.Colour, s.Why)
		}
	}
}

// A window that cannot be read must give Grey, never a No, for every suspect fed by event rows.
func TestClumsyP07BadWindowsNeverNo(t *testing.T) {
	wins := map[string]Window{
		"zero":     {},
		"inverted": {Start: at(11, 0), End: at(10, 0)},
		"empty":    {Start: at(10, 0), End: at(10, 0)},
	}
	for name, w := range wins {
		in := fullInput()
		in.Incident = w
		in.HTTPErrPresent = true
		in.IISSubStatus = true
		in.SQLPresent = true
		in.SQLKinds = map[string]bool{"attention": true, "login-failed": true, "call": true}
		in.Lanes = nil
		in.ApplySQLEvents()
		r := Analyze(in)
		clumsyNever(t, "incident "+name, r, "S10", "S11", "S9", "D16", "D17")
		for _, id := range []string{"D16", "D17"} {
			if v, ok := in.Lanes[id]; ok {
				t.Errorf("incident %s: lane %s = %q, want no answer", name, id, v)
			}
		}
		in2 := fullInput()
		in2.Reference = w
		in2.HTTPErrPresent = true
		in2.HTTPErr = []HTTPErrRow{{T: at(10, 5), Reason: "Timer_HeaderWait"}}
		Analyze(in2) // must not panic
	}
}

func TestClumsyP07ZeroAndNilInputs(t *testing.T) {
	for name, in := range map[string]Input{
		"zero":                  {},
		"present nil":           {HTTPErrPresent: true, IISPresent: true, IISSubStatus: true, SQLPresent: true},
		"lanes nil map entries": {Lanes: map[string]Tri{"D16": "", "M11": "weird"}},
	} {
		in.ApplySQLEvents()
		r := Analyze(in)
		clumsyNever(t, name, r, clumsyNineIDs...)
		assertWording(t, name, r)
	}
}

func TestClumsyP07HTTPErrOutsideWindowsAndUnknownReasons(t *testing.T) {
	ref, inc := stdWindows()
	th := Current()
	in := Input{Incident: inc, Reference: ref, HTTPErrPresent: true}
	// rows before the reference, after the incident, at the exact end (half-open), zero time
	for _, tm := range []time.Time{at(8, 59), at(11, 30), at(11, 31), {}} {
		for _, rs := range []string{"QueueFull", "AppOffline", "Timer_HeaderWait", "Connection_Dropped"} {
			in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: tm, Reason: rs})
		}
	}
	// unknown reasons and odd case inside the window
	for m := 0; m < 20; m++ {
		in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: at(10, 10+m), Reason: "Totally_Unknown"}, HTTPErrRow{T: at(10, 10+m), Reason: ""})
	}
	for name, s := range map[string]Signal{
		"queue": sigQueueBurst(in, Facts{}, th, nil),
		"pool":  sigAppPoolFail(in, Facts{}, th, nil),
		"recv":  sigRecvTimeout(in, Facts{}, th, nil),
	} {
		if s.Value != No {
			t.Errorf("%s = %+v, want No (rows outside window, unknown reasons)", name, s)
		}
	}
	// reason case
	in.HTTPErr = []HTTPErrRow{{T: at(10, 10), Reason: "QUEUEFULL"}, {T: at(10, 11), Reason: "queuefull"}, {T: at(10, 12), Reason: "QueueFull"}}
	if s := sigQueueBurst(in, Facts{}, th, nil); s.Value != Yes {
		t.Errorf("case-insensitive queue rows = %+v", s)
	}
}

func TestClumsyP07HugeCounts(t *testing.T) {
	ref, inc := stdWindows()
	th := Current()
	in := Input{Incident: inc, Reference: ref, HTTPErrPresent: true}
	for i := 0; i < 200000; i++ {
		in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: at(10, 0).Add(time.Duration(i) * time.Millisecond), Reason: "Timer_HeaderWait"})
	}
	s := sigRecvTimeout(in, Facts{}, th, nil) // ref empty: rise by absence
	if s.Value != Yes || strings.Contains(s.Why, "%") {
		t.Errorf("got %+v", s)
	}
	// reference equally busy per minute: no rise
	in.HTTPErr = nil
	for i := 0; i < 1000; i++ {
		in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: at(9, 0).Add(time.Duration(i) * 3 * time.Second), Reason: "Timer_HeaderWait"})
		in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: at(10, 0).Add(time.Duration(i) * 5 * time.Second), Reason: "Timer_HeaderWait"})
	}
	if s := sigRecvTimeout(in, Facts{}, th, nil); s.Value != No {
		t.Errorf("busy reference: %+v", s)
	}
}

func TestClumsyP07PayloadOddValues(t *testing.T) {
	ref, inc := stdWindows()
	th := Current()
	mk := func(refV, incV float64, extra ...float64) Input {
		in := Input{Incident: inc, Reference: ref, Audit: AuditPresent}
		n := th.GroupSizeP95 + 5
		for i := 0; i < n; i++ {
			rv, iv := refV, incV
			in.Requests = append(in.Requests, Request{Start: at(9, 10), P: &rv}, Request{Start: at(10, 10), P: &iv})
		}
		for _, e := range extra {
			e := e
			in.Requests = append(in.Requests, Request{Start: at(10, 20), P: &e})
		}
		return in
	}
	if s := sigPayloadUp(mk(5, 5, math.NaN()), Facts{}, th, nil); s.Value == Yes || strings.Contains(s.Why, "NaN") {
		t.Errorf("one NaN: %+v", s)
	}
	// a NaN-heavy incident must never read No (a green) from garbage
	nanIn := mk(5, 5)
	for i := range nanIn.Requests {
		if nanIn.Requests[i].Start.Equal(at(10, 10)) {
			v := math.NaN()
			nanIn.Requests[i].P = &v
		}
	}
	if s := sigPayloadUp(nanIn, Facts{}, th, nil); s.Value == No || strings.Contains(s.Why, "NaN") {
		t.Errorf("all NaN incident: %+v", s)
	}
	if s := sigPayloadUp(mk(5, math.Inf(1)), Facts{}, th, nil); strings.Contains(s.Why, "Inf") || s.Value == Yes {
		t.Errorf("Inf: %+v", s)
	}
	if s := sigPayloadUp(mk(5, -50), Facts{}, th, nil); s.Value == Yes || strings.Contains(s.Why, "-") {
		t.Errorf("negative: %+v", s)
	}
	// rows outside both windows and audit missing
	in := mk(5, 500)
	in.Requests = append(in.Requests, Request{Start: at(12, 0), P: new(float64)})
	if s := sigPayloadUp(in, Facts{}, th, nil); s.Value != Yes {
		t.Errorf("real rise: %+v", s)
	}
	in.Audit = AuditMissing
	if s := sigPayloadUp(in, Facts{}, th, nil); s.Value != Unknown || s.Missing == "" {
		t.Errorf("audit missing: %+v", s)
	}
	// every P nil reads missing, not No
	nilP := mk(5, 5)
	for i := range nilP.Requests {
		nilP.Requests[i].P = nil
	}
	if s := sigPayloadUp(nilP, Facts{}, th, nil); s.Value != Unknown || s.Missing != "payload" {
		t.Errorf("nil P: %+v", s)
	}
}

func TestClumsyP07FilterRejectOdd(t *testing.T) {
	ref, inc := stdWindows()
	th := Current()
	row := func(st, sub string, m int) IISRow {
		return IISRow{Start: at(10, m), End: at(10, m), TakenMs: 5, Path: "/x", Status: st, SubStatus: sub}
	}
	in := Input{Incident: inc, Reference: ref, IISPresent: true, IISSubStatus: true}
	for m := 0; m < 30; m++ {
		in.IIS = append(in.IIS, row("999", "5", m), row("", "", m), row("404", "0", m), row("404", "", m), row("abc", "xyz", m))
	}
	if s := sigFilterReject(in, Facts{}, th, nil); s.Value == Yes {
		t.Errorf("unknown statuses read Yes: %+v", s)
	}
	// present but empty IIS: never No
	empty := Input{Incident: inc, Reference: ref, IISPresent: true, IISSubStatus: true}
	if s := sigFilterReject(empty, Facts{}, th, nil); s.Value == No {
		t.Errorf("empty IIS read No: %+v", s)
	}
	// rejections outside the window, many inside plain rows
	in.IIS = nil
	for m := 0; m < 30; m++ {
		in.IIS = append(in.IIS, row("200", "0", m))
	}
	in.IIS = append(in.IIS, IISRow{Start: at(8, 0), End: at(8, 0), Status: "404", SubStatus: "5"}, IISRow{Start: at(12, 0), End: at(12, 0), Status: "431"})
	if s := sigFilterReject(in, Facts{}, th, nil); s.Value != No {
		t.Errorf("rejections outside window: %+v", s)
	}
	// real ones: 431 with any sub-status
	for m := 0; m < int(th.FilterMin)+2; m++ {
		in.IIS = append(in.IIS, row("431", "", m))
	}
	if s := sigFilterReject(in, Facts{}, th, nil); s.Value != Yes {
		t.Errorf("431 rows: %+v", s)
	}
}

// S9 with a strong signal and nothing else competing must never be Red.
func TestClumsyP07S9NeverRedWithStrongSignal(t *testing.T) {
	for _, mut := range []mod{
		{from: at(10, 20), to: at(11, 30), addO: 400},
		{from: at(10, 20), to: at(11, 30), addC: 400},
		{from: at(10, 20), to: at(11, 30), countMul: 3},
	} {
		in := stepScenario(mut)
		in.IISPresent = true
		in.IISSubStatus = true
		for m := 0; m < 60; m++ {
			in.IIS = append(in.IIS, IISRow{Start: at(10, 21+m/2), End: at(10, 21+m/2), TakenMs: 3, Path: "/x", Status: "404", SubStatus: "7"})
		}
		r := Analyze(in)
		s := suspect(t, r, "S9")
		if s.Colour == Red {
			t.Errorf("S9 Red: %+v", s)
		}
		assertWording(t, "s9", r)
	}
}

func TestClumsyP07LaneNeverOverwritesYes(t *testing.T) {
	// Data says No for D16/D17/D2; another reader already set Yes.
	in := sqlLaneRun(map[string]Tri{"D16": Yes, "D17": Yes, "D2": Yes},
		map[string]bool{"attention": true, "login-failed": true, "deadlock": true}, nil)
	for _, id := range []string{"D16", "D17", "D2"} {
		if in.Lanes[id] != Yes {
			t.Errorf("%s overwritten: %q", id, in.Lanes[id])
		}
	}
	// A second apply with a No must not downgrade a Yes the first apply wrote.
	in = sqlLaneRun(nil, map[string]bool{"attention": true}, sqlRows("attention", 1, 2, 3))
	if in.Lanes["D16"] != Yes {
		t.Fatalf("D16 = %q", in.Lanes["D16"])
	}
	in.SQLEvents = nil
	in.ApplySQLEvents()
	if in.Lanes["D16"] != Yes {
		t.Errorf("D16 downgraded to %q", in.Lanes["D16"])
	}
}

func TestClumsyP07SQLLaneOdd(t *testing.T) {
	// kinds nil: nothing is answered
	in := sqlLaneRun(nil, nil, sqlRows("attention", 1, 2, 3, 4, 5, 6))
	checkLane(t, in, "D16", "")
	checkLane(t, in, "D17", "")
	// rows only outside the incident: not Yes
	in = sqlLaneRun(nil, map[string]bool{"attention": true, "login-failed": true},
		sqlRows("attention", -30, -29, -28, 70, 71, 72))
	if in.Lanes["D16"] == Yes {
		t.Errorf("rows outside window read Yes")
	}
	// huge counts do not panic and read Yes
	var evs []SQLEvent
	for i := 0; i < 100000; i++ {
		evs = append(evs, SQLEvent{T: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Millisecond), Kind: "login-failed"})
	}
	in = sqlLaneRun(nil, map[string]bool{"login-failed": true}, evs)
	checkLane(t, in, "D17", Yes)
	// a reference that is far busier than the incident is not a storm now
	evs = sqlCat(sqlSpread("login-failed", 3, 0, 60), sqlSpread("login-failed", 500, -119, -1))
	in = sqlLaneRun(nil, map[string]bool{"login-failed": true}, evs)
	if in.Lanes["D17"] == Yes {
		t.Errorf("busier reference read Yes")
	}
	// attention captured but empty -> No
	in = sqlLaneRun(nil, map[string]bool{"attention": true}, nil)
	checkLane(t, in, "D16", No)
	// not present: no write
	in2 := Input{SQLKinds: map[string]bool{"attention": true}}
	in2.ApplySQLEvents()
	if in2.Lanes != nil {
		t.Errorf("lanes written without a SQL source")
	}
}

// The D16/D17 incident window cannot be read: no answer, never No.
func TestClumsyP07SQLLaneInvalidIncident(t *testing.T) {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	for name, w := range map[string]Window{"zero": {}, "inverted": {Start: base.Add(time.Hour), End: base}, "empty": {Start: base, End: base}} {
		in := Input{SQLPresent: true, SQLKinds: map[string]bool{"attention": true, "login-failed": true, "deadlock": true},
			Incident: w, Reference: Window{Start: base.Add(-2 * time.Hour), End: base}}
		in.ApplySQLEvents()
		for _, id := range []string{"D2", "D16", "D17"} {
			if v, ok := in.Lanes[id]; ok {
				t.Errorf("%s: %s = %q, want no answer", name, id, v)
			}
		}
	}
}

// Whole-run wording: every nine suspect that fires holds no causal word or percent sign.
func TestClumsyP07WordingWhenFiring(t *testing.T) {
	in := stepScenario(mod{from: at(10, 20), to: at(11, 30), addO: 400})
	in.HTTPErrPresent = true
	in.IISSubStatus = true
	for m := 0; m < 40; m++ {
		for _, rs := range []string{"QueueFull", "AppOffline", "Timer_HeaderWait", "Connection_Dropped", "Timer_EntityBody"} {
			in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: at(10, 20+m), Reason: rs})
		}
	}
	for i := range in.Requests {
		v := float64(5)
		if in.Requests[i].Start.After(at(10, 20)) {
			v = 900
		}
		in.Requests[i].P = &v
	}
	in.SQLPresent = true
	in.SQLKinds = map[string]bool{"attention": true, "login-failed": true}
	in.SQLEvents = nil
	for m := 0; m < 30; m++ {
		in.SQLEvents = append(in.SQLEvents, SQLEvent{T: at(10, 20+m), Kind: "attention"}, SQLEvent{T: at(10, 20+m), Kind: "login-failed"})
	}
	in.ApplySQLEvents()
	in.Lanes["M11"], in.Lanes["M12"] = Yes, Yes
	r := Analyze(in)
	assertWording(t, "firing", r)
	for _, id := range clumsyNineIDs {
		_ = suspect(t, r, id)
	}
}
