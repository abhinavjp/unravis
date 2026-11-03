package investigate

import (
	"math"
	"testing"
)

// persistBins builds one bin per marker: 'h' above the limit, 'm' below it, 's' a
// thin minute (skipped), 'g' a gap minute. The status reads the marker from peak.
func persistBins(marks string) ([]*bin, func(*bin) minStatus) {
	out := make([]*bin, 0, len(marks))
	for _, c := range marks {
		b := &bin{}
		switch c {
		case 'h':
			b.peak = 1
		case 'm':
			b.peak = 2
		case 'g':
			b.peak, b.gap = 1, "short"
		}
		out = append(out, b)
	}
	return out, func(b *bin) minStatus {
		switch b.peak {
		case 1:
			return stHit
		case 2:
			return stMiss
		}
		return stSkip
	}
}

func TestPersistShare(t *testing.T) {
	cases := []struct {
		name  string
		marks string
		from  int
		want  float64
	}{
		{"all hits", "hhhhh", 0, 1},
		{"half hits", "hhmm", 0, 0.5},
		{"gap minute ignored", "hhgmmm", 0, 0.4},
		{"thin minute ignored", "hsmh", 0, 2.0 / 3},
		{"starts at from", "mmhhhh", 2, 1},
		{"empty tail", "", 0, 1},
		{"only gaps", "ggg", 0, 1},
		{"from past the end", "hhmm", 4, 1},
		{"from far past the end", "hhmm", 99, 1},
		{"negative from", "hhmm", -1, 1},
	}
	for _, c := range cases {
		bins, status := persistBins(c.marks)
		if got := persistShare(bins, c.from, status); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: persistShare = %v, want %v", c.name, got, c.want)
		}
	}
}

// tierRow returns the row of one tier from the facts.
func tierRow(t *testing.T, f Facts, tier string) TierOnset {
	t.Helper()
	for _, ti := range f.Tiers {
		if ti.Tier == tier {
			return ti
		}
	}
	t.Fatalf("no %s tier in %+v", tier, f.Tiers)
	return TierOnset{}
}

func TestOnsetDroppedWhenItDoesNotPersist(t *testing.T) {
	// Five elevated minutes, then the rest of the incident falls back below the limit.
	f := Analyze(stepScenario(mod{from: at(10, 20), to: at(10, 25), addC: 400})).Facts
	row := tierRow(t, f, "c")
	if row.At != nil || row.Tested {
		t.Errorf("c row = %+v, want no onset and not tested", row)
	}
	if row.Why != "moved briefly but did not stay up" {
		t.Errorf("c why = %q", row.Why)
	}
}

func TestOnsetKeptWhenItPersists(t *testing.T) {
	f := Analyze(stepScenario(mod{from: at(10, 20), to: at(11, 30), addC: 400})).Facts
	row := tierRow(t, f, "c")
	if row.At == nil || !row.At.Equal(at(10, 20)) || !row.Tested {
		t.Errorf("c row = %+v, want an onset at 10:20", row)
	}
}
