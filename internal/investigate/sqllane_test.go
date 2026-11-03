package investigate

import (
	"testing"
	"time"
)

func TestApplySQLEventsD2(t *testing.T) {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	inc := Window{Start: base, End: base.Add(60 * time.Minute)}
	ref := Window{Start: base.Add(-120 * time.Minute), End: base}
	dl := func(offs ...int) []SQLEvent {
		var out []SQLEvent
		for _, m := range offs {
			out = append(out, SQLEvent{T: base.Add(time.Duration(m) * time.Minute), Kind: "deadlock"})
		}
		return out
	}
	refDL := func(n int) []SQLEvent {
		var offs []int
		for i := 0; i < n; i++ {
			offs = append(offs, -1-i*5)
		}
		return dl(offs...)
	}
	cat := func(a, b []SQLEvent) []SQLEvent { return append(append([]SQLEvent{}, a...), b...) }

	cases := []struct {
		name    string
		in      Input
		wantNil bool
		want    Tri // "" means key absent
		wantSrc int
	}{
		{name: "no sql source", in: Input{SQLEvents: dl(1, 2, 3, 4, 5, 6)}, wantNil: true},
		{name: "storm", in: Input{SQLPresent: true, SQLEvents: dl(1, 2, 3, 4, 5, 6)}, want: Yes, wantSrc: 1},
		{name: "none", in: Input{SQLPresent: true}, want: No, wantSrc: 1},
		{name: "few", in: Input{SQLPresent: true, SQLEvents: dl(1, 2)}, want: "", wantSrc: 1},
		{name: "same rate", in: Input{SQLPresent: true, SQLEvents: cat(dl(1, 2, 3, 4, 5, 6), refDL(12))}, want: "", wantSrc: 1},
		{name: "yes kept", in: Input{SQLPresent: true, Lanes: map[string]Tri{"D2": Yes}}, want: Yes, wantSrc: 1},
		{name: "kind not listed", in: Input{SQLPresent: true, SQLKinds: map[string]bool{"login-failed": true}}, want: "", wantSrc: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			in.Incident, in.Reference = inc, ref
			in.ApplySQLEvents()
			if c.wantNil {
				if in.Lanes != nil || len(in.LaneSources) != 0 {
					t.Fatalf("expected untouched, got %v %v", in.Lanes, in.LaneSources)
				}
				return
			}
			got, ok := in.Lanes["D2"]
			if c.want == "" {
				if ok {
					t.Errorf("D2 should be absent, got %v", got)
				}
			} else if got != c.want {
				t.Errorf("D2 = %q, want %q", got, c.want)
			}
			n := 0
			for _, s := range in.LaneSources {
				if s == "sql-events" {
					n++
				}
			}
			if n != c.wantSrc {
				t.Errorf("sql-events in LaneSources %d times, want %d", n, c.wantSrc)
			}
		})
	}
}

// sqlLaneRun applies the SQL lane over events and kinds with a 60-minute
// incident and a 120-minute reference.
func sqlLaneRun(lanes map[string]Tri, kinds map[string]bool, evs []SQLEvent) Input {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	in := Input{
		SQLPresent: true, SQLKinds: kinds, SQLEvents: evs, Lanes: lanes,
		Incident:  Window{Start: base, End: base.Add(60 * time.Minute)},
		Reference: Window{Start: base.Add(-120 * time.Minute), End: base},
	}
	in.ApplySQLEvents()
	return in
}

// sqlRows builds rows of one kind at minute offsets from the incident start.
func sqlRows(kind string, offs ...float64) []SQLEvent {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	var out []SQLEvent
	for _, m := range offs {
		out = append(out, SQLEvent{T: base.Add(time.Duration(m * float64(time.Minute))), Kind: kind})
	}
	return out
}

// sqlSpread builds n rows of one kind evenly across minutes [from, to).
func sqlSpread(kind string, n int, from, to float64) []SQLEvent {
	offs := make([]float64, n)
	for i := range offs {
		offs[i] = from + (to-from)*float64(i)/float64(n)
	}
	return sqlRows(kind, offs...)
}

func sqlCat(parts ...[]SQLEvent) []SQLEvent {
	var out []SQLEvent
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func checkLane(t *testing.T, in Input, id string, want Tri) {
	t.Helper()
	got, ok := in.Lanes[id]
	if want == "" {
		if ok {
			t.Errorf("%s should be absent, got %v", id, got)
		}
		return
	}
	if got != want {
		t.Errorf("%s = %q, want %q", id, got, want)
	}
}

func TestApplySQLEventsD16(t *testing.T) {
	attnKind := map[string]bool{"attention": true}
	bothKinds := map[string]bool{"attention": true, "call": true}
	cases := []struct {
		name  string
		lanes map[string]Tri
		kinds map[string]bool
		evs   []SQLEvent
		want  Tri
	}{
		{"no attention kind", nil, map[string]bool{"deadlock": true}, sqlRows("attention", 1, 2, 3, 4, 5), ""},
		{"nil kinds", nil, nil, sqlRows("attention", 1, 2, 3, 4, 5), ""},
		{"three in four minutes", nil, attnKind, sqlRows("attention", 10, 12, 14), Yes},
		{"two of hundred calls", nil, bothKinds, sqlCat(sqlSpread("call", 100, 0, 60), sqlRows("attention", 5, 25)), Yes},
		{"two of two hundred calls", nil, bothKinds, sqlCat(sqlSpread("call", 200, 0, 60), sqlRows("attention", 5, 25)), ""},
		{"no rows", nil, attnKind, nil, No},
		{"yes kept", map[string]Tri{"D16": Yes}, attnKind, nil, Yes},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkLane(t, sqlLaneRun(c.lanes, c.kinds, c.evs), "D16", c.want)
		})
	}
}

func TestApplySQLEventsD17(t *testing.T) {
	kinds := map[string]bool{"login-failed": true}
	cases := []struct {
		name  string
		lanes map[string]Tri
		kinds map[string]bool
		evs   []SQLEvent
		want  Tri
	}{
		{"no login kind", nil, map[string]bool{"deadlock": true}, sqlRows("login-failed", 1, 2, 3, 4, 5, 6), ""},
		{"storm", nil, kinds, sqlRows("login-failed", 10, 11, 12, 13, 13.5, 14), Yes},
		{"none", nil, kinds, nil, No},
		{"same rate", nil, kinds, sqlCat(sqlRows("login-failed", 10, 11, 12, 13, 13.5, 14), sqlSpread("login-failed", 12, -120, 0)), No},
		{"rose but not three times", nil, kinds, sqlCat(sqlRows("login-failed", 10, 11, 12, 13, 13.5, 14), sqlSpread("login-failed", 6, -120, 0)), ""},
		{"yes kept", map[string]Tri{"D17": Yes}, kinds, nil, Yes},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkLane(t, sqlLaneRun(c.lanes, c.kinds, c.evs), "D17", c.want)
		})
	}
}
