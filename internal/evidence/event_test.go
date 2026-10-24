package evidence

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
)

func goodEvent() Event {
	t0 := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	d := 12.5
	return Event{
		EvidenceID: EventID("abcdefghijklmnopqrstuvwxyz", "toy", "nonce", 3, 0),
		Source:     "toy", Kind: "request", Host: "web-a",
		Start: t0, End: t0.Add(time.Second), DurationMs: &d, Unit: "ms",
		Severity: "info", Line: 3, TZRule: clock.RuleUTCField,
		Attrs: map[string]string{"status": "200"},
	}
}

func TestValidImporterLabel(t *testing.T) {
	for label, want := range map[string]bool{
		"Symptom": true, "Correlation": true, "Strong correlation": false, "Proven cause": false, "": false, "symptom": false,
	} {
		if got := ValidImporterLabel(label); got != want {
			t.Errorf("ValidImporterLabel(%q) = %v, want %v", label, got, want)
		}
	}
}

func TestEventValidate(t *testing.T) {
	allow := map[string]bool{"status": true}
	if err := goodEvent().Validate(allow); err != nil {
		t.Fatalf("good event rejected: %v", err)
	}
	nan := math.NaN()
	neg := -1.0
	cases := map[string]func(e *Event){
		"empty id":        func(e *Event) { e.EvidenceID = "" },
		"no source":       func(e *Event) { e.Source = "" },
		"no kind":         func(e *Event) { e.Kind = "" },
		"long host":       func(e *Event) { e.Host = strings.Repeat("h", 65) },
		"non-utc start":   func(e *Event) { e.Start = e.Start.In(time.FixedZone("x", 3600)) },
		"zero start":      func(e *Event) { e.Start = time.Time{} },
		"end before":      func(e *Event) { e.End = e.Start.Add(-time.Second) },
		"nan value":       func(e *Event) { e.Value = &nan },
		"negative dur":    func(e *Event) { e.DurationMs = &neg },
		"bad severity":    func(e *Event) { e.Severity = "fatal" },
		"negative line":   func(e *Event) { e.Line = -1 },
		"bad tz rule":     func(e *Event) { e.TZRule = "guess" },
		"unlisted attr":   func(e *Event) { e.Attrs["other"] = "x" },
		"long attr value": func(e *Event) { e.Attrs["status"] = strings.Repeat("a", 257) },
		"bad utf8 attr":   func(e *Event) { e.Attrs["status"] = "\xff" },
		"17 attrs": func(e *Event) {
			e.Attrs = map[string]string{}
			for i := 0; i < 17; i++ {
				e.Attrs[string(rune('a'+i))] = "x"
			}
		},
	}
	all := map[string]bool{"status": true}
	for i := 0; i < 17; i++ {
		all[string(rune('a'+i))] = true
	}
	for name, mut := range cases {
		e := goodEvent()
		mut(&e)
		err := e.Validate(all)
		if err == nil || !errors.Is(err, ErrEventInvalid) {
			t.Errorf("%s: want ErrEventInvalid, got %v", name, err)
		}
	}
	// Exactly at the bounds is fine.
	e := goodEvent()
	e.Attrs = map[string]string{"status": strings.Repeat("a", 256)}
	if err := e.Validate(allow); err != nil {
		t.Errorf("256-byte value rejected: %v", err)
	}
	// Nil allowlist permits no attributes.
	if err := goodEvent().Validate(nil); err == nil {
		t.Error("nil allowlist accepted an attribute")
	}
	// Error text never carries a value.
	e = goodEvent()
	e.Attrs["status"] = strings.Repeat("SENTINEL-7f3a", 30)
	if err := e.Validate(allow); err == nil || strings.Contains(err.Error(), "SENTINEL") {
		t.Errorf("error leaks value or missing: %v", err)
	}
}

func TestEventClone(t *testing.T) {
	e := goodEvent()
	c := e.Clone()
	*c.DurationMs = 99
	c.Attrs["status"] = "500"
	if *e.DurationMs != 12.5 || e.Attrs["status"] != "200" {
		t.Fatal("Clone aliases the original")
	}
}

func TestEventID(t *testing.T) {
	const c = "abcdefghijklmnopqrstuvwxyz"
	a := EventID(c, "iis-w3c", "n1", 7, 0)
	if !strings.HasPrefix(a, "ev:abcdefgh:iis-w3c:") || len(a) != len("ev:abcdefgh:iis-w3c:")+16 {
		t.Fatalf("shape: %s", a)
	}
	if a != EventID(c, "iis-w3c", "n1", 7, 0) {
		t.Fatal("not deterministic")
	}
	for _, other := range []string{
		EventID(c, "iis-w3c", "n2", 7, 0), EventID(c, "iis-w3c", "n1", 8, 0),
		EventID(c, "iis-w3c", "n1", 7, 1), EventID(c, "mongod-json", "n1", 7, 0),
		EventID("zyxwvutsrqponmlkjihgfedcba", "iis-w3c", "n1", 7, 0),
	} {
		if other == a {
			t.Fatal("collision across inputs")
		}
	}
	// Field boundaries are separated: line 1 ordinal 23 differs from line 12 ordinal 3.
	if EventID(c, "x", "n", 1, 23) == EventID(c, "x", "n", 12, 3) {
		t.Fatal("ambiguous encoding")
	}
}

func TestJoinIDAndPrefixes(t *testing.T) {
	const c = "abcdefghijklmnopqrstuvwxyz"
	ids := []string{"b", "a", "c"}
	j1 := JoinID(c, "B1-overlap", ids)
	if ids[0] != "b" {
		t.Fatal("JoinID sorted the caller's slice")
	}
	if j1 != JoinID(c, "B1-overlap", []string{"c", "a", "b"}) {
		t.Fatal("order matters")
	}
	if j1 == JoinID(c, "B1-overlap", []string{"a", "b"}) || j1 == JoinID(c, "other", ids) {
		t.Fatal("collision")
	}
	if !strings.HasPrefix(j1, "jn:abcdefgh:B1-overlap:") {
		t.Fatalf("shape: %s", j1)
	}
	p := CasePrefixes(c)
	if len(p) != 2 || p[0] != "ev:abcdefgh:" || p[1] != "jn:abcdefgh:" {
		t.Fatalf("prefixes: %v", p)
	}
	if !strings.HasPrefix(EventID(c, "x", "n", 1, 0), p[0]) || !strings.HasPrefix(j1, p[1]) {
		t.Fatal("prefix does not cover IDs")
	}
}
