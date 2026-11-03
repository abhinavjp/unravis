package investigate

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// Seeded random awkward inputs for the nine: nothing panics, wording stays clean, S9 is never red,
// a Cannot-test suspect is grey, a Contradicted one is never built on an unknown must reading, and
// suspect ids stay unique.
func TestClumsyP07RandomAwkwardInputs(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	reasons := []string{"QueueFull", "Timer_AppPool", "AppPoolTimer", "AppOffline", "AppShutdown", "Connection_Abandoned_By_AppPool",
		"Timer_HeaderWait", "Timer_EntityBody", "Timer_MinBytesPerSecond", "Connection_Dropped", "", "Weird", "queuefull"}
	statuses := []string{"", "200", "404", "431", "413", "500", "999", "abc"}
	subs := []string{"", "5", "7", "0", "11", "19", "x"}
	odd := []float64{0, 1, 5, 900, -1, math.NaN(), math.Inf(1), 1e12}
	kinds := []string{"deadlock", "call", "attention", "login-failed", "blocked-process", "other"}
	wins := []Window{{}, {Start: at(10, 0), End: at(10, 0)}, {Start: at(11, 0), End: at(10, 0)}}
	randTime := func() time.Time {
		if rng.Intn(20) == 0 {
			return time.Time{}
		}
		return at(8, 0).Add(time.Duration(rng.Intn(5*60)) * time.Minute)
	}
	for iter := 0; iter < 300; iter++ {
		var in Input
		if rng.Intn(3) == 0 {
			in = fullInput()
		} else {
			ref, inc := stdWindows()
			in = Input{Incident: inc, Reference: ref, Audit: AuditPresent}
		}
		if rng.Intn(6) == 0 {
			in.Incident = wins[rng.Intn(len(wins))]
		}
		if rng.Intn(6) == 0 {
			in.Reference = wins[rng.Intn(len(wins))]
		}
		in.HTTPErrPresent = rng.Intn(4) != 0
		in.HTTPErr = nil
		for i, n := 0, rng.Intn(60); i < n; i++ {
			in.HTTPErr = append(in.HTTPErr, HTTPErrRow{T: randTime(), Reason: reasons[rng.Intn(len(reasons))]})
		}
		in.IISPresent = rng.Intn(4) != 0
		in.IISSubStatus = rng.Intn(3) != 0
		if in.IISPresent {
			for i, n := 0, rng.Intn(80); i < n; i++ {
				tm := randTime()
				in.IIS = append(in.IIS, IISRow{Start: tm, End: tm, TakenMs: 5, Path: "/a", Status: statuses[rng.Intn(len(statuses))], SubStatus: subs[rng.Intn(len(subs))]})
			}
		}
		for i := range in.Requests {
			if rng.Intn(3) == 0 {
				v := odd[rng.Intn(len(odd))]
				in.Requests[i].P = &v
			}
		}
		in.SQLPresent = rng.Intn(2) == 0
		in.SQLKinds = map[string]bool{}
		for _, k := range kinds {
			in.SQLKinds[k] = rng.Intn(2) == 0
		}
		in.SQLEvents = nil
		for i, n := 0, rng.Intn(80); i < n; i++ {
			in.SQLEvents = append(in.SQLEvents, SQLEvent{T: randTime(), Kind: kinds[rng.Intn(len(kinds))]})
		}
		in.Lanes = nil
		pre := map[string]Tri{}
		if rng.Intn(3) == 0 {
			pre["D16"], pre["D17"], pre["M11"], pre["M12"] = Yes, Yes, Yes, Yes
			in.Lanes = map[string]Tri{"D16": Yes, "D17": Yes, "M11": Yes, "M12": Yes}
		}
		in.ApplySQLEvents()
		for id, v := range pre {
			if v == Yes && in.Lanes[id] != Yes {
				t.Fatalf("iter %d: lane %s lost its Yes: %q", iter, id, in.Lanes[id])
			}
		}
		r := Analyze(in)
		assertWording(t, "fuzz", r)
		seen := map[string]bool{}
		for _, s := range r.Suspects {
			if seen[s.ID] {
				t.Fatalf("iter %d: suspect %s twice", iter, s.ID)
			}
			seen[s.ID] = true
			if s.ID == "S9" && s.Colour == Red {
				t.Fatalf("iter %d: S9 red", iter)
			}
			if s.Result == ResCannotTest && s.Colour != Grey {
				t.Fatalf("iter %d: %s cannot-test but %s", iter, s.ID, s.Colour)
			}
			if s.Result == ResContra && s.Colour == Red {
				t.Fatalf("iter %d: %s contradicted but red", iter, s.ID)
			}
		}
		for _, id := range []string{"D16", "D17"} {
			if v, ok := in.Lanes[id]; ok && v == No && !in.Incident.Valid() {
				t.Fatalf("iter %d: %s answered No on an unreadable incident window", iter, id)
			}
		}
		for _, id := range []string{"S8", "S10", "S11"} {
			s := suspect(t, r, id)
			if s.Result == ResContra && !in.Incident.Valid() {
				t.Fatalf("iter %d: %s contradicted on an unreadable incident window: %s", iter, id, s.Why)
			}
		}
	}
}
