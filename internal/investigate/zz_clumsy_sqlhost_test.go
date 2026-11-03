package investigate

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// Clumsy-user and hostile-input checks for the Phase 06 additions: burst counts,
// the SQL event lane and the host disk lane.

var zzT0 = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

func zzWin(fromMin, toMin int) Window {
	return Window{Start: zzT0.Add(time.Duration(fromMin) * time.Minute), End: zzT0.Add(time.Duration(toMin) * time.Minute)}
}

func TestClumsyCountBurstEdges(t *testing.T) {
	inc, ref := zzWin(0, 60), zzWin(-60, 0)
	at := func(d time.Duration) time.Time { return zzT0.Add(d) }

	// Windows invalid, reversed or zero: no panic and no NaN or Inf rate.
	for _, w := range [][2]Window{{{}, {}}, {inc, {}}, {{}, ref}, {{Start: inc.End, End: inc.Start}, ref}} {
		b := CountBurst([]time.Time{at(time.Minute), at(-time.Minute), {}}, w[0], w[1])
		for _, r := range []float64{b.IncRate(), b.RefRate()} {
			if math.IsNaN(r) || math.IsInf(r, 0) || r < 0 {
				t.Errorf("rate %v for %+v", r, w)
			}
		}
	}
	if b := CountBurst(nil, inc, ref); b.Inc != 0 || b.Ref != 0 || b.Peak5 != 0 {
		t.Errorf("no rows: %+v", b)
	}

	// Window edges: Start is in, End is out; one instant is never in both windows.
	b := CountBurst([]time.Time{inc.Start, inc.End, ref.Start, ref.End.Add(-time.Nanosecond), at(-61 * time.Minute)}, inc, ref)
	if b.Inc != 1 || b.Ref != 2 {
		t.Errorf("edges: %+v", b)
	}
	// Overlapping windows: a row is counted once, in the incident.
	b = CountBurst([]time.Time{at(10 * time.Minute)}, inc, zzWin(-5, 30))
	if b.Inc != 1 || b.Ref != 0 {
		t.Errorf("overlap: %+v", b)
	}

	// Peak5 is half-open: rows exactly 5 minutes apart are two bursts.
	if b = CountBurst([]time.Time{at(0), at(5 * time.Minute)}, inc, ref); b.Peak5 != 1 {
		t.Errorf("rows 5m apart: Peak5 %d, want 1", b.Peak5)
	}
	if b = CountBurst([]time.Time{at(5*time.Minute - time.Nanosecond), at(0)}, inc, ref); b.Peak5 != 2 {
		t.Errorf("rows just under 5m apart (unsorted): Peak5 %d, want 2", b.Peak5)
	}
	same := []time.Time{at(time.Minute), at(time.Minute), at(time.Minute), at(time.Minute)}
	if b = CountBurst(same, inc, ref); b.Inc != 4 || b.Peak5 != 4 {
		t.Errorf("duplicates: %+v", b)
	}
}

// A large input is one sorted pass: no quadratic loop on a million rows at one instant.
func TestClumsyCountBurstLarge(t *testing.T) {
	ts := make([]time.Time, 1_000_000)
	for i := range ts {
		ts[i] = zzT0.Add(time.Minute)
	}
	done := make(chan BurstCount, 1)
	go func() { done <- CountBurst(ts, zzWin(0, 60), zzWin(-60, 0)) }()
	select {
	case b := <-done:
		if b.Inc != len(ts) || b.Peak5 != len(ts) {
			t.Errorf("%+v", b)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("CountBurst did not finish")
	}
}

// The SQL lane never answers No from a window it cannot read.
func TestClumsyApplySQLEventsBadWindows(t *testing.T) {
	for name, inc := range map[string]Window{
		"zero":       {},
		"reversed":   {Start: zzT0.Add(time.Hour), End: zzT0},
		"open start": {End: zzT0.Add(time.Hour)},
		"empty":      {Start: zzT0, End: zzT0},
	} {
		in := Input{SQLPresent: true, Incident: inc, Reference: zzWin(-60, 0)}
		in.ApplySQLEvents()
		if v, ok := in.Lanes["D2"]; ok {
			t.Errorf("%s incident window, no rows: D2 = %v, want no answer", name, v)
		}
	}
}

func TestClumsyApplySQLEventsOddRows(t *testing.T) {
	inc, ref := zzWin(0, 60), zzWin(-60, 0)
	run := func(evs ...SQLEvent) Input {
		in := Input{SQLPresent: true, SQLEvents: evs, Incident: inc, Reference: ref}
		in.ApplySQLEvents()
		return in
	}
	if in := run(); in.Lanes["D2"] != No {
		t.Errorf("zero events: %v", in.Lanes)
	}
	one := run(SQLEvent{T: zzT0.Add(time.Minute), Kind: "deadlock"})
	if v, ok := one.Lanes["D2"]; ok {
		t.Errorf("one deadlock gave %v", v)
	}
	// Rows with a zero time, a far-future time, unknown and empty kinds.
	odd := run(SQLEvent{Kind: "deadlock"}, SQLEvent{T: zzT0.AddDate(1000, 0, 0), Kind: "deadlock"},
		SQLEvent{T: zzT0.Add(time.Minute), Kind: "bogus"}, SQLEvent{T: zzT0.Add(time.Minute), Kind: ""})
	if odd.Lanes["D2"] != No {
		t.Errorf("odd rows: %v", odd.Lanes)
	}
	// Calling it twice does not list the lane source twice.
	one.ApplySQLEvents()
	if n := strings.Count(strings.Join(one.LaneSources, ","), "sql-events"); n != 1 {
		t.Errorf("lane sources %v", one.LaneSources)
	}
	var five []SQLEvent
	for i := 0; i < 5; i++ {
		five = append(five, SQLEvent{T: zzT0.Add(time.Duration(i) * time.Minute), Kind: "deadlock"})
	}
	if in := run(five...); in.Lanes["D2"] != Yes {
		t.Errorf("five deadlocks: %v", in.Lanes)
	}
	// A No from another reader is replaced by a real Yes; a Yes is never replaced.
	in := Input{SQLPresent: true, Incident: inc, Reference: ref, Lanes: map[string]Tri{"D2": No}, SQLEvents: five}
	in.ApplySQLEvents()
	if in.Lanes["D2"] != Yes {
		t.Errorf("No not replaced by a Yes: %v", in.Lanes)
	}
	in = Input{SQLPresent: true, Incident: inc, Reference: ref, Lanes: map[string]Tri{"D2": Yes}}
	in.ApplySQLEvents()
	if in.Lanes["D2"] != Yes {
		t.Errorf("another reader's Yes overwritten: %v", in.Lanes)
	}
}

// Disk readings that are not real numbers must not read as "the disk was fine"
// (or as "the disk was slow").
func TestClumsyHostDiskGarbage(t *testing.T) {
	for name, v := range map[string]*float64{
		"NaN": f64(math.NaN()), "+Inf": f64(math.Inf(1)), "-Inf": f64(math.Inf(-1)), "negative": f64(-3),
	} {
		res := HostLane(hostInput(), hostH(sqlHostSamples("sqlservr.exe", 95, v, v)))
		if got, ok := res.Lanes["D9"]; ok {
			t.Errorf("%s disk readings gave D9 = %v", name, got)
		}
	}
	// Garbage in the reference only: the slow incident still reads on its own.
	res := HostLane(hostInput(), hostH(sqlHostSamples("sqlservr.exe", 95, f64(math.NaN()), f64(30))))
	if res.Lanes["D9"] != Yes {
		t.Errorf("NaN reference, slow incident: D9 = %v", res.Lanes["D9"])
	}
	// Zero latency is a real reading: not slow, so No.
	if res := HostLane(hostInput(), hostH(sqlHostSamples("sqlservr.exe", 95, f64(0), f64(0)))); res.Lanes["D9"] != No {
		t.Errorf("zero latency: D9 = %v", res.Lanes["D9"])
	}
}

func TestClumsyHostSQLOddSamples(t *testing.T) {
	in := hostInput()
	_ = HostLane(in, hostH(nil))
	// Many samples at one instant, an empty process name and a padded upper-case one.
	var ss []HostSample
	for i := 0; i < 400; i++ {
		ss = append(ss, HostSample{T: hostT0.Add(45 * time.Minute), CPU: f64(95), DiskMs: f64(40), Procs: []HostProc{{Name: ""}, {Name: " SQLSERVR.EXE "}}})
	}
	res := HostLane(in, hostH(ss))
	if v, ok := res.Lanes["D9"]; ok {
		t.Errorf("one minute of readings gave D9 = %v", v)
	}
	// A recording that does not touch the incident window.
	far := []HostSample{{T: hostT0.Add(1000 * time.Hour), CPU: f64(99), DiskMs: f64(99), Procs: []HostProc{{Name: "sqlservr"}}}}
	if res := HostLane(in, hostH(far)); res.Lanes["D9"] != "" || res.Lanes["D10"] != "" {
		t.Errorf("future samples: %v", res.Lanes)
	}
	// Zero windows with SQL samples: no panic, no D9.
	if res := HostLane(Input{}, hostH(sqlHostSamples("sqlservr", 95, f64(4), f64(30)))); res.Lanes["D9"] != "" {
		t.Errorf("zero windows: %v", res.Lanes)
	}
}

// ApplyHost with nil maps, and another reader's Yes kept for D9 and D10.
func TestClumsyApplyHostKeepsYes(t *testing.T) {
	h := hostH(sqlHostSamples("sqlservr.exe", 95, f64(4), f64(6))) // fast disk: D9 No, H1 Yes, D10 Yes
	in := hostInput()
	in.Lanes = nil
	in.ApplyHost(h)
	if in.Lanes["D9"] != No || in.Lanes["D10"] != Yes {
		t.Fatalf("nil lanes: %v", in.Lanes)
	}
	in = hostInput()
	in.Lanes = map[string]Tri{"D9": Yes, "D10": Yes}
	flat := hostH(sqlHostSamples("sqlservr.exe", 30, f64(4), f64(6))) // flat CPU: D10 No
	in.ApplyHost(flat)
	if in.Lanes["D9"] != Yes || in.Lanes["D10"] != Yes {
		t.Errorf("another reader's Yes overwritten: %v", in.Lanes)
	}
}

// Inputs without SQL or HTTPERR rows leave no trace of the new fields in the JSON.
func TestClumsyNoLeakWithoutSQLRows(t *testing.T) {
	in := Input{
		Incident:   zzWin(0, 30),
		Reference:  zzWin(-30, 0),
		Audit:      AuditPresent,
		IISPresent: true,
		IIS:        []IISRow{{Start: zzT0.Add(time.Minute), End: zzT0.Add(2 * time.Minute), TakenMs: 50, Path: "/a"}},
	}
	res := checkSane(t, "no sql rows", in)
	facts, err := json.Marshal(ComputeFacts(in, Current()))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sqlPresent", "sqlEvents", "sqlKinds", "httpErr", "iisSubStatus", "subStatus", "sql-events", `"gate":`, `"Gate":`} {
		if strings.Contains(string(b), k) {
			t.Errorf("result JSON contains %q", k)
		}
		if strings.Contains(string(facts), k) {
			t.Errorf("facts JSON contains %q", k)
		}
	}
	// A SQL source with no rows, and hostile rows, still analyse cleanly.
	checkSane(t, "sql present, no rows", Input{Incident: in.Incident, Reference: in.Reference, SQLPresent: true})
	checkSane(t, "sql odd rows", Input{Incident: in.Incident, Reference: in.Reference, SQLPresent: true,
		SQLEvents:      []SQLEvent{{Kind: "call", T: zzT0, DurationMs: f64(math.MaxFloat64)}, {Kind: "call", DurationMs: f64(-1)}},
		HTTPErrPresent: true, HTTPErr: []HTTPErrRow{{}, {T: zzT0, Reason: strings.Repeat("x", 100000), Status: "abc"}}})
}
