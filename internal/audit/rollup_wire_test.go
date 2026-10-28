package audit

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRollupWireRoundTripsThroughJSON(t *testing.T) {
	b := NewRollupBuilder(time.Hour)
	at := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	d1, d2 := 120.0, 4500.0
	b.AddRow(at, "synchronous", "valid", StatusSuccess, &d1, 1000)
	b.AddRow(at.Add(2*time.Hour), "synchronous", "valid", StatusServerError, &d2, 1000)
	b.AddCounts(b.SlotStart(at), RollupCounts{Total: 3, Background: 3})
	raw, err := json.Marshal(b.Wire())
	if err != nil {
		t.Fatal(err)
	}
	var w RollupWire
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	back, err := RollupBuilderFromWire(w, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Wire(), b.Wire()) || back.Groups() != b.Groups() {
		t.Fatalf("round trip changed the rollup:\n got %+v\nwant %+v", back.Wire(), b.Wire())
	}
	merged := NewRollupBuilder(time.Hour)
	merged.Merge(back)
	merged.Merge(b)
	if got := merged.Wire().Slots[0].Counts.Total; got != 8 {
		t.Fatalf("merged total = %d, want 8", got)
	}
}

func TestRollupWireRefusesAnotherWidth(t *testing.T) {
	b := NewRollupBuilder(time.Hour)
	if _, err := RollupBuilderFromWire(b.Wire(), 30*time.Minute); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input", err)
	}
	w := b.Wire()
	w.Slots = []RollupWireSlot{{Start: 7}}
	if _, err := RollupBuilderFromWire(w, time.Hour); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("misaligned slot: err = %v, want invalid input", err)
	}
}
