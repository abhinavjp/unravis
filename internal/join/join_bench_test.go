package join

import (
	"fmt"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
)

// benchData builds nEvents short events (one second long, one per second) and
// nIntervals intervals spread over the same range. With long set, one more
// event spans the whole range and starts first (RV-DAT-2).
func benchData(nEvents, nIntervals int, long bool) ([]Interval, []evidence.Event) {
	events := make([]evidence.Event, 0, nEvents+1)
	if long {
		events = append(events, evidence.Event{EvidenceID: "long", Start: at(-1), End: at(nEvents + 10)})
	}
	for i := 0; i < nEvents; i++ {
		events = append(events, evidence.Event{EvidenceID: fmt.Sprintf("e%07d", i), Start: at(i), End: at(i + 1)})
	}
	iv := make([]Interval, nIntervals)
	step := nEvents / nIntervals
	for i := range iv {
		s := i * step
		iv[i] = Interval{ID: fmt.Sprintf("i%06d", i), Start: at(s), End: at(s + 1)}
	}
	return iv, events
}

func benchOverlap(b *testing.B, long bool) {
	iv, events := benchData(200000, 20000, long)
	tol := Tolerance{Resolution: time.Second}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		Overlap(iv, events, tol, nil)
	}
}

// BenchmarkOverlapShortEvents is the baseline: 200,000 short events, 20,000 intervals.
func BenchmarkOverlapShortEvents(b *testing.B) { benchOverlap(b, false) }

// BenchmarkOverlapOneLongEvent adds one event that spans the whole range.
func BenchmarkOverlapOneLongEvent(b *testing.B) { benchOverlap(b, true) }
