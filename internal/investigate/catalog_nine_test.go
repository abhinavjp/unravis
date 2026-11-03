package investigate

import (
	"reflect"
	"testing"
)

func TestCatalogNine(t *testing.T) {
	cat := Catalog()
	pos := map[string]int{}
	for i, e := range cat {
		if _, dup := pos[e.ID]; dup {
			t.Fatalf("%s appears twice", e.ID)
		}
		pos[e.ID] = i
		if e.Cap != "" && e.ID != "S9" {
			t.Errorf("%s has Cap %q", e.ID, e.Cap)
		}
	}
	for _, id := range []string{"S8", "S9", "S10", "S11", "S12", "D16", "D17", "M11", "M12"} {
		if _, ok := pos[id]; !ok {
			t.Fatalf("%s missing", id)
		}
	}
	after := func(id, prev string) {
		if pos[id] != pos[prev]+1 {
			t.Errorf("%s at %d, want right after %s at %d", id, pos[id], prev, pos[prev])
		}
	}
	after("S8", "S7")
	after("S9", "S8")
	after("S10", "S9")
	after("S11", "S10")
	after("S12", "S11")
	after("D16", "D15")
	after("D17", "D16")
	after("M11", "M10")
	after("M12", "M11")

	idx := catalogIndex()
	if len(idx) != len(cat) {
		t.Errorf("catalogIndex holds %d of %d entries", len(idx), len(cat))
	}
	if idx["S9"].Cap != "orange" {
		t.Errorf("S9 Cap = %q", idx["S9"].Cap)
	}
	s1 := idx["S1"]
	if !reflect.DeepEqual(s1.Must, []string{SigOLarge}) {
		t.Errorf("S1 Must = %v", s1.Must)
	}
	found := false
	for _, s := range s1.Expect {
		found = found || s == SigQueueBurst
	}
	if !found {
		t.Errorf("S1 Expect = %v", s1.Expect)
	}
	for _, id := range []string{"D16", "D17"} {
		e := idx[id]
		if !e.LaneRequired || e.Tier != "sql" || !reflect.DeepEqual(e.Must, []string{SigCLarge}) {
			t.Errorf("%s = %+v", id, e)
		}
	}
	for _, id := range []string{"M11", "M12"} {
		e := idx[id]
		if !e.LaneRequired || e.Tier != "mongo" || !reflect.DeepEqual(e.Must, []string{SigALarge}) {
			t.Errorf("%s = %+v", id, e)
		}
	}
	if idx["S8"].Tier != "a" {
		t.Errorf("S8 tier = %q", idx["S8"].Tier)
	}
	for _, id := range []string{"S8", "S9", "S10", "S11", "S12"} {
		e := idx[id]
		if e.LaneRequired {
			t.Errorf("%s must not be LaneRequired", id)
		}
		if id != "S8" && e.Tier != "o" {
			t.Errorf("%s tier = %q", id, e.Tier)
		}
	}
}
