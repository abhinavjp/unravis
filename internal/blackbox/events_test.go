package blackbox

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// BBX-4-01 event log tailer: pure aggregation tests with fake event rows.
// No Windows needed: the query func is injected into eventsGroup.

func TestAggregateEventsCounts(t *testing.T) {
	t0 := time.Unix(1000, 0)
	rows := []rawEvent{
		{Channel: 1, EventID: 51, Level: 3, T: t0.Add(2 * time.Second)},
		{Channel: 1, EventID: 51, Level: 3, T: t0.Add(5 * time.Second)},
		{Channel: 1, EventID: 51, Level: 3, T: t0.Add(3 * time.Second)},
		{Channel: 1, EventID: 51, Level: 4, T: t0.Add(4 * time.Second)},
		{Channel: 2, EventID: 1000, Level: 2, T: t0.Add(6 * time.Second)},
		{Channel: 1, EventID: 51, Level: 3, T: t0}, // at the watermark: already counted
	}
	got := aggregateEvents(rows, t0)
	want := []EventEntry{
		{Channel: 2, EventID: 1000, Level: 2, Count: 1, T: t0.Add(6 * time.Second)},
		{Channel: 1, EventID: 51, Level: 3, Count: 3, T: t0.Add(5 * time.Second)},
		{Channel: 1, EventID: 51, Level: 4, Count: 1, T: t0.Add(4 * time.Second)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aggregateEvents = %+v, want %+v (grouped by channel/id/level, counted, latest T, T desc)", got, want)
	}
}

func TestAggregateEventsCap(t *testing.T) {
	base := time.Unix(2000, 0)
	rows := make([]rawEvent, maxEventsPerTick+44)
	for i := range rows {
		rows[i] = rawEvent{Channel: 1, EventID: uint32(i), Level: 2, T: base.Add(time.Duration(i) * time.Second)}
	}
	got := aggregateEvents(rows, time.Time{})
	if len(got) != maxEventsPerTick {
		t.Fatalf("len = %d, want capped at %d", len(got), maxEventsPerTick)
	}
	if !got[0].T.Equal(base.Add(time.Duration(len(rows)-1) * time.Second)) {
		t.Fatalf("newest row dropped: first T = %v", got[0].T)
	}
	for i := 1; i < len(got); i++ {
		if got[i].T.After(got[i-1].T) {
			t.Fatalf("not sorted by T desc at %d: %v after %v", i, got[i].T, got[i-1].T)
		}
	}
}

func TestAggregateEventsNoText(t *testing.T) {
	// Compile-time shape: rawEvent and EventEntry carry IDs and numbers only.
	shapes := []struct {
		typ    reflect.Type
		fields map[string]reflect.Kind
	}{
		{reflect.TypeOf(rawEvent{}), map[string]reflect.Kind{
			"Channel": reflect.Uint8, "EventID": reflect.Uint32, "Level": reflect.Uint8, "T": reflect.Struct,
		}},
		{reflect.TypeOf(EventEntry{}), map[string]reflect.Kind{
			"Channel": reflect.Uint8, "EventID": reflect.Uint32, "Level": reflect.Uint8, "Count": reflect.Uint32, "T": reflect.Struct,
		}},
	}
	for _, s := range shapes {
		if s.typ.NumField() != len(s.fields) {
			t.Fatalf("%s has %d fields, want %d (no extra text field)", s.typ.Name(), s.typ.NumField(), len(s.fields))
		}
		for i := 0; i < s.typ.NumField(); i++ {
			f := s.typ.Field(i)
			kind, ok := s.fields[f.Name]
			if !ok || f.Type.Kind() != kind {
				t.Fatalf("%s.%s: unexpected field (kind %v); event log must stay IDs and numbers only", s.typ.Name(), f.Name, f.Type.Kind())
			}
			if f.Type.Kind() == reflect.String {
				t.Fatalf("%s.%s is a string: message text must never be captured", s.typ.Name(), f.Name)
			}
		}
	}
}

func TestEventsWatermark(t *testing.T) {
	now := time.Unix(3000, 0)
	all := []rawEvent{
		{Channel: 1, EventID: 51, Level: 3, T: now.Add(-time.Hour)}, // historical: never backfilled
		{Channel: 1, EventID: 51, Level: 3, T: now.Add(10 * time.Second)},
		{Channel: 1, EventID: 51, Level: 3, T: now.Add(20 * time.Second)},
	}
	var sincs []time.Time
	g := &eventsGroup{query: func(since time.Time) ([]rawEvent, error) {
		sincs = append(sincs, since)
		var out []rawEvent
		for _, r := range all {
			if r.T.After(since) {
				out = append(out, r)
			}
		}
		return out, nil
	}}

	// First tick: the watermark starts at now, not zero or the past.
	v, err := g.Collect(now, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(sincs) != 1 || !sincs[0].Equal(now) {
		t.Fatalf("first query since = %v, want now %v (no historical backfill)", sincs, now)
	}
	eg, ok := v.(*EventsGroup)
	if !ok || len(eg.Events) != 1 {
		t.Fatalf("first result = %+v, want one entry", v)
	}
	if eg.Events[0].Count != 2 || !eg.Events[0].T.Equal(now.Add(20*time.Second)) {
		t.Fatalf("first entry = %+v, want Count 2 (historical row excluded), latest T", eg.Events[0])
	}

	// Second tick: only rows newer than the advanced watermark are read.
	all = append(all, rawEvent{Channel: 1, EventID: 51, Level: 3, T: now.Add(25 * time.Second)})
	v, err = g.Collect(now.Add(30*time.Second), 15)
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if len(sincs) != 2 || !sincs[1].Equal(now.Add(20*time.Second)) {
		t.Fatalf("second query since = %v, want advanced watermark %v", sincs, now.Add(20*time.Second))
	}
	eg = v.(*EventsGroup)
	if len(eg.Events) != 1 || eg.Events[0].Count != 1 || !eg.Events[0].T.Equal(now.Add(25*time.Second)) {
		t.Fatalf("second entry = %+v, want only the newer row (Count 1)", eg.Events)
	}

	// Third tick: nothing new -> nil group (omitted), never a zero entry.
	v, err = g.Collect(now.Add(45*time.Second), 15)
	if err != nil || v != nil {
		t.Fatalf("empty tick = (%+v, %v), want (nil, nil): no events is omitted, never 0", v, err)
	}
	if len(sincs) != 3 || !sincs[2].Equal(now.Add(25*time.Second)) {
		t.Fatalf("third query since = %v, want watermark held at %v", sincs, now.Add(25*time.Second))
	}
}

func TestEventsAccessDeniedOmitted(t *testing.T) {
	g := &eventsGroup{query: func(time.Time) ([]rawEvent, error) {
		return nil, errors.New("access denied")
	}}
	v, err := g.Collect(time.Unix(4000, 0), 0)
	if err == nil || v != nil {
		t.Fatalf("Collect = (%+v, %v), want (nil, error) on access denied", v, err)
	}
	// Through the budgeted collector: the sample field stays nil (never a
	// zero-filled group) and the fixed name lands in Missing.
	c := NewGroupCollector(&stubCollector{}, []Group{g}, NewBudget(15))
	s, err := c.Collect(time.Unix(4000, 0))
	if err != nil {
		t.Fatalf("tick failed: %v", err)
	}
	if s.Events != nil {
		t.Fatalf("access-denied group was zero-filled: %+v", s.Events)
	}
	if !missingHas(s, "events") {
		t.Fatalf("Missing = %v, want events", s.Missing)
	}
}

func TestEventsRegistered(t *testing.T) {
	registryMu.Lock()
	reg, ok := registry["events"]
	registryMu.Unlock()
	if !ok {
		t.Fatal("events group not registered")
	}
	if !reg.optional {
		t.Fatal("events group must be optional")
	}
}
