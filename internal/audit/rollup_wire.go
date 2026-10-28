package audit

import (
	"sort"
	"time"
)

// RollupWire is a RollupBuilder written out as plain data, so that a rollup made in another process (the key keeper hosts the sources
// opened from saved links whose password it holds, P14b) can be sent back and merged with the local one.
type RollupWire struct {
	Width  int64            `json:"width"` // seconds
	Groups int64            `json:"groups"`
	Slots  []RollupWireSlot `json:"slots"`
}

// RollupWireSlot is one time slot: its start in Unix seconds, its counters and its latency bins.
type RollupWireSlot struct {
	Start  int64           `json:"start"`
	Counts RollupCounts    `json:"counts"`
	Bins   []RollupWireBin `json:"bins,omitempty"`
}

// RollupWireBin is one latency histogram bin of a slot.
type RollupWireBin struct {
	Bin   int     `json:"bin"`
	Count int64   `json:"count"`
	Sum   float64 `json:"sum"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
}

// Wire writes the builder out, slots and bins in order.
func (b *RollupBuilder) Wire() RollupWire {
	out := RollupWire{Width: b.width, Groups: b.groups, Slots: make([]RollupWireSlot, 0, len(b.slots))}
	for start, s := range b.slots {
		slot := RollupWireSlot{Start: start, Counts: s.counts}
		for bin, v := range s.bins {
			slot.Bins = append(slot.Bins, RollupWireBin{Bin: bin, Count: v.count, Sum: v.sum, Min: v.min, Max: v.max})
		}
		sort.Slice(slot.Bins, func(i, j int) bool { return slot.Bins[i].Bin < slot.Bins[j].Bin })
		out.Slots = append(out.Slots, slot)
	}
	sort.Slice(out.Slots, func(i, j int) bool { return out.Slots[i].Start < out.Slots[j].Start })
	return out
}

// RollupBuilderFromWire reads a builder back. A slot width that is not the one asked for, or a slot or bin that does not fit it, is
// refused as invalid input.
func RollupBuilderFromWire(w RollupWire, width time.Duration) (*RollupBuilder, error) {
	b := NewRollupBuilder(width)
	if w.Width != b.width || b.width <= 0 || w.Groups < 0 {
		return nil, invalidInputf("the rollup does not match the requested time slots")
	}
	for _, s := range w.Slots {
		if s.Start%b.width != 0 {
			return nil, invalidInputf("the rollup does not match the requested time slots")
		}
		dst := b.slot(s.Start)
		dst.counts.merge(s.Counts)
		for _, v := range s.Bins {
			if v.Bin < 0 || v.Count < 0 {
				return nil, invalidInputf("the rollup holds a bin that cannot be read")
			}
			addRollupBin(dst.bins, v.Bin, v.Count, v.Sum, v.Min, v.Max)
		}
	}
	b.groups = w.Groups
	return b, nil
}
