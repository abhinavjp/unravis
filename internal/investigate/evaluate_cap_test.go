package investigate

import (
	"strings"
	"testing"
	"time"
)

func capSuspect(id, tier string, at time.Time) Suspect {
	return Suspect{ID: id, Group: GroupS, Result: ResConsistent, Tier: tier, OnsetAt: ptrTime(at), Why: "Why."}
}

func capColour(sps []Suspect) {
	var f Facts
	f.Onset.Status = "found"
	colour(sps, Input{}, f, Signals{}, Current())
}

func TestColourCapOrange(t *testing.T) {
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

	t.Run("capped suspect alone is orange", func(t *testing.T) {
		sps := []Suspect{capSuspect("S9", "o", t0)}
		capColour(sps)
		if sps[0].Colour != Orange {
			t.Fatalf("S9 colour = %q, want orange", sps[0].Colour)
		}
		if !strings.HasSuffix(sps[0].Why, "not called the first.") {
			t.Fatalf("S9 why = %q", sps[0].Why)
		}
	})

	t.Run("uncapped control alone is red", func(t *testing.T) {
		sps := []Suspect{capSuspect("S1", "o", t0)}
		capColour(sps)
		if sps[0].Colour != Red {
			t.Fatalf("S1 colour = %q, want red", sps[0].Colour)
		}
		if !strings.HasSuffix(sps[0].Why, " Its tier moved first.") {
			t.Fatalf("S1 why = %q", sps[0].Why)
		}
	})

	t.Run("same tier same onset keeps S1 red", func(t *testing.T) {
		sps := []Suspect{capSuspect("S9", "o", t0), capSuspect("S1", "o", t0)}
		capColour(sps)
		if sps[0].Colour != Orange || sps[1].Colour != Red {
			t.Fatalf("S9 %q S1 %q, want orange and red", sps[0].Colour, sps[1].Colour)
		}
	})

	t.Run("capped earlier than a later load tier", func(t *testing.T) {
		sps := []Suspect{capSuspect("S9", "o", t0), capSuspect("L1", tierLoad, t0.Add(30*time.Minute))}
		capColour(sps)
		if sps[0].Colour != Orange || sps[1].Colour != Orange {
			t.Fatalf("S9 %q L1 %q, want both orange", sps[0].Colour, sps[1].Colour)
		}
		if !strings.HasSuffix(sps[1].Why, " Its tier moved after an earlier one.") {
			t.Fatalf("L1 why = %q", sps[1].Why)
		}
	})
}
