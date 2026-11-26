package join

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
)

var t0 = time.Date(2001, 2, 3, 4, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func ev(id string, s, e int) evidence.Event {
	return evidence.Event{EvidenceID: id, Start: at(s), End: at(e)}
}

func TestToleranceTotal(t *testing.T) {
	tol := Tolerance{Resolution: time.Second, Skew: 2 * time.Second, Method: 500 * time.Millisecond}
	if tol.Total() != 3500*time.Millisecond {
		t.Fatalf("total %v", tol.Total())
	}
	if (Tolerance{Resolution: -time.Hour, Skew: time.Second}).Total() != time.Second {
		t.Fatal("negative term counted")
	}
}

func TestOverlapClasses(t *testing.T) {
	iv := []Interval{
		{"none", at(0), at(10)},
		{"one", at(100), at(110)},
		{"two", at(200), at(210)},
	}
	events := []evidence.Event{
		ev("e-far", 1000, 1001),
		ev("e1", 105, 106),
		ev("e2a", 205, 206), ev("e2b", 209, 300),
	}
	m := Overlap(iv, events, Tolerance{}, nil)
	want := []Match{
		{"none", evidence.JoinNone, nil},
		{"one", evidence.JoinUnique, []string{"e1"}},
		{"two", evidence.JoinAmbiguous, []string{"e2a", "e2b"}},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %+v\nwant %+v", m, want)
	}
	if CountUnique(m) != 1 {
		t.Fatalf("CountUnique = %d", CountUnique(m))
	}
}

func TestOverlapOneEventTwoIntervals(t *testing.T) {
	// One long event is the only candidate of two intervals: it cannot explain
	// both, so neither is counted.
	iv := []Interval{{"a", at(100), at(110)}, {"b", at(200), at(210)}, {"c", at(500), at(510)}}
	events := []evidence.Event{ev("long", 105, 205), ev("solo", 505, 506)}
	m := Overlap(iv, events, Tolerance{}, nil)
	want := []Match{
		{"a", evidence.JoinAmbiguous, []string{"long"}},
		{"b", evidence.JoinAmbiguous, []string{"long"}},
		{"c", evidence.JoinUnique, []string{"solo"}},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %+v\nwant %+v", m, want)
	}
	if n := CountUnique(m); n != 1 {
		t.Fatalf("CountUnique = %d, want 1", n)
	}
	// A shared event that is one of several candidates elsewhere does not
	// demote the interval where it is the only candidate.
	iv = []Interval{{"a", at(100), at(110)}, {"b", at(200), at(210)}}
	events = []evidence.Event{ev("long", 105, 205), ev("other", 206, 207)}
	m = Overlap(iv, events, Tolerance{}, nil)
	if m[0].Class != evidence.JoinUnique || m[1].Class != evidence.JoinAmbiguous || CountUnique(m) != 1 {
		t.Fatalf("shared but not exclusive: %+v", m)
	}
}

func TestOverlapBoundariesAndTolerance(t *testing.T) {
	iv := []Interval{{"a", at(100), at(110)}}
	cases := []struct {
		name string
		e    evidence.Event
		tol  Tolerance
		want evidence.JoinClass
	}{
		{"touches end", ev("e", 110, 120), Tolerance{}, evidence.JoinUnique},
		{"touches start", ev("e", 90, 100), Tolerance{}, evidence.JoinUnique},
		{"just after", ev("e", 111, 120), Tolerance{}, evidence.JoinNone},
		{"just before", ev("e", 90, 99), Tolerance{}, evidence.JoinNone},
		{"after within tolerance", ev("e", 112, 120), Tolerance{Resolution: time.Second, Skew: time.Second}, evidence.JoinUnique},
		{"before within tolerance", ev("e", 95, 97), Tolerance{Skew: 3 * time.Second}, evidence.JoinUnique},
		{"spans interval", ev("e", 0, 1000), Tolerance{}, evidence.JoinUnique},
		{"inside interval", ev("e", 103, 103), Tolerance{}, evidence.JoinUnique},
	}
	for _, c := range cases {
		if got := Overlap(iv, []evidence.Event{c.e}, c.tol, nil)[0].Class; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestOverlapQualifiesAndEdgeCases(t *testing.T) {
	iv := []Interval{{"a", at(100), at(110)}}
	events := []evidence.Event{ev("slow", 105, 106), ev("fast", 105, 105)}
	events[0].Kind, events[1].Kind = "slow-op", "request"
	m := Overlap(iv, events, Tolerance{}, func(e evidence.Event) bool { return e.Kind == "slow-op" })
	if m[0].Class != evidence.JoinUnique || m[0].Candidates[0] != "slow" {
		t.Fatalf("qualifies ignored: %+v", m[0])
	}
	if m := Overlap(iv, []evidence.Event{ev("e", 105, 105), ev("e", 105, 105)}, Tolerance{}, nil); m[0].Class != evidence.JoinUnique {
		t.Fatalf("duplicate id counted twice: %+v", m[0])
	}
	if m := Overlap([]Interval{{"rev", at(110), at(100)}}, []evidence.Event{ev("e", 105, 105)}, Tolerance{}, nil); m[0].Class != evidence.JoinUnique {
		t.Fatalf("reversed interval: %+v", m[0])
	}
	if got := Overlap(nil, events, Tolerance{}, nil); len(got) != 0 {
		t.Fatal("no intervals must give no matches")
	}
	if m := Overlap(iv, nil, Tolerance{}, nil); m[0].Class != evidence.JoinNone || m[0].Candidates != nil {
		t.Fatalf("no events: %+v", m[0])
	}
	// Inputs untouched.
	evs := []evidence.Event{ev("b", 300, 301), ev("a", 100, 101)}
	Overlap(iv, evs, Tolerance{}, nil)
	if evs[0].EvidenceID != "b" {
		t.Fatal("input reordered")
	}
}

// The indexed scan must agree with a plain O(n*m) check.
func TestOverlapMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for round := 0; round < 50; round++ {
		var events []evidence.Event
		for i := 0; i < 60; i++ {
			s := rng.Intn(1000)
			events = append(events, ev(fmt.Sprintf("e%03d", i), s, s+rng.Intn(80)))
		}
		var ivs []Interval
		for i := 0; i < 20; i++ {
			s := rng.Intn(1000)
			ivs = append(ivs, Interval{fmt.Sprintf("i%d", i), at(s), at(s + rng.Intn(30))})
		}
		tol := Tolerance{Skew: time.Duration(rng.Intn(5)) * time.Second}
		got := Overlap(ivs, events, tol, nil)
		for i, v := range ivs {
			var want []string
			for _, e := range events {
				if !e.End.Before(v.Start.Add(-tol.Total())) && !e.Start.After(v.End.Add(tol.Total())) {
					want = append(want, e.EvidenceID)
				}
			}
			if len(want) != len(got[i].Candidates) {
				t.Fatalf("round %d interval %s: got %v want %v", round, v.ID, got[i].Candidates, want)
			}
			for j := range want {
				if want[j] != got[i].Candidates[j] { // both sorted by ID
					t.Fatalf("round %d interval %s: got %v want %v", round, v.ID, got[i].Candidates, want)
				}
			}
		}
	}
}

// overlapBrute is the plain O(n*m) reading of the Overlap contract, used to
// check the tree-based lookup including result order and classes.
func overlapBrute(iv []Interval, ev []evidence.Event, tol Tolerance, q func(evidence.Event) bool) []Match {
	slack := tol.Total()
	out := make([]Match, 0, len(iv))
	for _, v := range iv {
		s, e := v.Start, v.End
		if e.Before(s) {
			s, e = e, s
		}
		lo, hi := s.Add(-slack), e.Add(slack)
		seen := map[string]bool{}
		var ids []string
		for _, x := range ev {
			if q != nil && !q(x) {
				continue
			}
			xe := x.End
			if xe.Before(x.Start) {
				xe = x.Start
			}
			if !xe.Before(lo) && !x.Start.After(hi) && !seen[x.EvidenceID] {
				seen[x.EvidenceID] = true
				ids = append(ids, x.EvidenceID)
			}
		}
		sort.Strings(ids)
		m := Match{IntervalID: v.ID, Class: evidence.JoinNone, Candidates: ids}
		switch {
		case len(ids) == 1:
			m.Class = evidence.JoinUnique
		case len(ids) > 1:
			m.Class = evidence.JoinAmbiguous
		}
		out = append(out, m)
	}
	claims := map[string]int{}
	for _, m := range out {
		if m.Class == evidence.JoinUnique {
			claims[m.Candidates[0]]++
		}
	}
	for i := range out {
		if out[i].Class == evidence.JoinUnique && claims[out[i].Candidates[0]] > 1 {
			out[i].Class = evidence.JoinAmbiguous
		}
	}
	return out
}

// Long events, inverted ranges, repeated IDs and a qualifies filter must give
// the same matches, classes and candidate order as the brute-force reading
// (RV-DAT-2: the lookup no longer depends on one long event).
func TestOverlapTreeMatchesBruteForceWithLongEvents(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 200; round++ {
		var events []evidence.Event
		n := rng.Intn(80)
		for i := 0; i < n; i++ {
			s := rng.Intn(1000)
			var e int
			switch rng.Intn(6) {
			case 0:
				e = s + 500 + rng.Intn(1000) // a long event
			case 1:
				e = s - rng.Intn(20) // End before Start
			default:
				e = s + rng.Intn(15)
			}
			id := fmt.Sprintf("e%03d", i)
			if rng.Intn(10) == 0 && i > 0 {
				id = fmt.Sprintf("e%03d", rng.Intn(i)) // repeated ID
			}
			events = append(events, ev(id, s, e))
		}
		var ivs []Interval
		for i, m := 0, rng.Intn(25); i < m; i++ {
			s := rng.Intn(1100)
			ivs = append(ivs, Interval{fmt.Sprintf("i%d", i), at(s), at(s + rng.Intn(30) - 5)})
		}
		tol := Tolerance{Resolution: time.Duration(rng.Intn(3)) * time.Second, Skew: time.Duration(rng.Intn(5)) * time.Second}
		var q func(evidence.Event) bool
		if round%3 == 0 {
			q = func(e evidence.Event) bool { return e.EvidenceID[len(e.EvidenceID)-1] != '7' }
		}
		got := Overlap(ivs, events, tol, q)
		want := overlapBrute(ivs, events, tol, q)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d:\ngot  %+v\nwant %+v", round, got, want)
		}
	}
}

// One event spanning the whole range is a candidate of every interval and
// must not hide the short events (RV-DAT-2).
func TestOverlapOneLongEventAmongMany(t *testing.T) {
	events := []evidence.Event{ev("long", -5, 100000)}
	var ivs []Interval
	for i := 0; i < 50; i++ {
		events = append(events, ev(fmt.Sprintf("s%03d", i), i*100, i*100+1))
		ivs = append(ivs, Interval{fmt.Sprintf("i%d", i), at(i * 100), at(i*100 + 1)})
	}
	for i, m := range Overlap(ivs, events, Tolerance{}, nil) {
		want := []string{"long", fmt.Sprintf("s%03d", i)}
		if m.Class != evidence.JoinAmbiguous || !reflect.DeepEqual(m.Candidates, want) {
			t.Fatalf("interval %d: %+v want candidates %v", i, m, want)
		}
	}
}
