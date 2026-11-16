package blackbox

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// BBX-4-01 event log tailer: pure tests with a fake per-channel query. No
// Windows needed: the query func is injected into eventsGroup. The fake models
// stable record IDs, a forward cursor, a fair attempt budget, unreadable
// records and log resets (W6-01/W6-02/W6-06).

// fakeEventSource models one channel's records in ascending record-ID order.
// unreadable marks record IDs that fail to render (they still charge the
// budget). It reads forward from the cursor, never newest-first.
type fakeEventSource struct {
	records    [2][]rawEvent
	unreadable map[uint64]bool
	calls      int
	lastBudget [2]int
}

func (f *fakeEventSource) query(channel uint8, afterID uint64, floor time.Time, budget int, deadline time.Time) (eventChannelRead, error) {
	idx := int(channel) - 1
	f.calls++
	f.lastBudget[idx] = budget
	res := eventChannelRead{lastID: afterID}
	if budget <= 0 {
		return res, nil
	}
	for _, r := range f.records[idx] {
		if r.RecordID <= afterID {
			continue // already consumed
		}
		if afterID == 0 && !floor.IsZero() && !r.T.After(floor) {
			continue // historical: no backfill on the first read
		}
		if res.attempted >= budget {
			res.truncated = true // backlog remains beyond the budget
			break
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			res.truncated = true
			break
		}
		res.attempted++
		res.lastID = r.RecordID
		if f.unreadable[r.RecordID] {
			res.failed++ // fetched but unreadable: charges budget, never a row
			continue
		}
		res.rows = append(res.rows, r)
	}
	return res, nil
}

func ev(ch uint8, id uint64, eventID uint32, level uint8, t time.Time) rawEvent {
	return rawEvent{Channel: ch, EventID: eventID, Level: level, T: t, RecordID: id}
}

func TestAggregateEventsCounts(t *testing.T) {
	t0 := time.Unix(1000, 0)
	rows := []rawEvent{
		ev(1, 1, 51, 3, t0.Add(2*time.Second)),
		ev(1, 2, 51, 3, t0.Add(5*time.Second)),
		ev(1, 3, 51, 3, t0.Add(3*time.Second)),
		ev(1, 4, 51, 4, t0.Add(4*time.Second)),
		ev(2, 1, 1000, 2, t0.Add(6*time.Second)),
	}
	got := aggregateEvents(rows)
	want := []EventEntry{
		{Channel: 2, EventID: 1000, Level: 2, Count: 1, T: t0.Add(6 * time.Second)},
		{Channel: 1, EventID: 51, Level: 3, Count: 3, T: t0.Add(5 * time.Second)},
		{Channel: 1, EventID: 51, Level: 4, Count: 1, T: t0.Add(4 * time.Second)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aggregateEvents = %+v, want %+v (grouped by channel/id/level, counted, latest T, T desc)", got, want)
	}
}

// Equal timestamps are distinct records: both count (the old timestamp watermark
// discarded the second). Dedupe is by record ID, not by time (W6-01).
func TestAggregateEventsEqualTimestamps(t *testing.T) {
	t0 := time.Unix(1000, 0)
	rows := []rawEvent{
		ev(1, 10, 51, 3, t0),
		ev(1, 11, 51, 3, t0), // same T, different record ID: a real second event
		ev(1, 10, 51, 3, t0), // replayed record ID: counted once
	}
	got := aggregateEvents(rows)
	if len(got) != 1 || got[0].Count != 2 {
		t.Fatalf("equal-timestamp events = %+v, want one entry Count 2 (distinct IDs counted, replay deduped)", got)
	}
}

func TestAggregateEventsCap(t *testing.T) {
	base := time.Unix(2000, 0)
	rows := make([]rawEvent, maxEventsPerTick+44)
	for i := range rows {
		rows[i] = ev(1, uint64(i+1), uint32(i), 2, base.Add(time.Duration(i)*time.Second))
	}
	got := aggregateEvents(rows)
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
			"Channel": reflect.Uint8, "EventID": reflect.Uint32, "Level": reflect.Uint8, "T": reflect.Struct, "RecordID": reflect.Uint64,
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

// The first read starts at now (no historical backfill); later reads advance a
// per-channel record-ID cursor, and the cursor commits only on ack (W6-01/06).
func TestEventsCursorForwardAndAck(t *testing.T) {
	now := time.Unix(3000, 0)
	f := &fakeEventSource{records: [2][]rawEvent{
		{ev(1, 1, 51, 3, now.Add(-time.Hour)), ev(1, 2, 51, 3, now.Add(10*time.Second)), ev(1, 3, 51, 3, now.Add(20*time.Second))},
	}}
	g := &eventsGroup{query: f.query}

	// First tick: historical record 1 is not backfilled; records 2 and 3 read.
	v, err := g.Collect(now, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	eg := v.(*EventsGroup)
	if len(eg.Events) != 1 || eg.Events[0].Count != 2 {
		t.Fatalf("first batch = %+v, want Count 2 (historical excluded)", eg.Events)
	}
	// The cursor has NOT committed yet (no ack): a re-collect re-emits the batch.
	if g.committed.load() != (eventCursors{}) {
		t.Fatalf("committed advanced inside Collect: %+v (must wait for ack)", g.committed.load())
	}
	v2, err := g.Collect(now.Add(15*time.Second), 15)
	if err != nil {
		t.Fatalf("re-collect: %v", err)
	}
	if !reflect.DeepEqual(v2.(*EventsGroup).Events, eg.Events) {
		t.Fatalf("unacked batch not re-emitted: %+v vs %+v", v2.(*EventsGroup).Events, eg.Events)
	}

	// Acknowledge (the recorder calls Ack after Store.Append accepts the sample).
	eg.Ack()
	if g.committed.load().ch[0] != 3 {
		t.Fatalf("ack did not commit cursor 3: %+v", g.committed.load())
	}
	// Next tick reads forward from record 3: only the new record 4.
	f.records[0] = append(f.records[0], ev(1, 4, 51, 3, now.Add(25*time.Second)))
	v3, err := g.Collect(now.Add(30*time.Second), 15)
	if err != nil {
		t.Fatalf("third Collect: %v", err)
	}
	eg3 := v3.(*EventsGroup)
	if len(eg3.Events) != 1 || eg3.Events[0].Count != 1 || !eg3.Events[0].T.Equal(now.Add(25*time.Second)) {
		t.Fatalf("third batch = %+v, want only record 4 (forward progress, no replay)", eg3.Events)
	}
	// Nothing new and no quality signal: omitted, never a zero entry.
	eg3.Ack()
	v4, err := g.Collect(now.Add(45*time.Second), 15)
	if err != nil || v4 != nil {
		t.Fatalf("empty tick = (%+v, %v), want (nil, nil)", v4, err)
	}
}

// Cap overflow makes bounded forward progress, never a silent skip: the unread
// backlog is read on later ticks (W6-01).
func TestEventsCapOverflowForwardProgress(t *testing.T) {
	now := time.Unix(4000, 0)
	f := &fakeEventSource{}
	for i := 1; i <= maxEventsPerTick+50; i++ {
		f.records[0] = append(f.records[0], ev(1, uint64(i), 51, 3, now.Add(time.Duration(i)*time.Second)))
	}
	g := &eventsGroup{query: f.query}
	ticks := 0
	sawTruncated := false
	for {
		ticks++
		v, err := g.Collect(now.Add(time.Duration(ticks)*time.Second), 1)
		if err != nil {
			t.Fatalf("tick %d: %v", ticks, err)
		}
		if v == nil {
			break
		}
		eg := v.(*EventsGroup)
		sawTruncated = sawTruncated || eg.Truncated
		eg.Ack()
		if ticks > 10 {
			t.Fatal("forward progress did not drain the backlog within 10 ticks")
		}
	}
	if !sawTruncated {
		t.Fatal("cap overflow never labeled truncated")
	}
	if got := g.committed.load().ch[0]; got != maxEventsPerTick+50 {
		t.Fatalf("cursor = %d, want all %d records consumed (no silent skip)", got, maxEventsPerTick+50)
	}
}

// Fair channel budgeting: a busy System channel must not starve Application
// (W6-01). Both channels make progress in the same tick.
func TestEventsFairChannelBudget(t *testing.T) {
	now := time.Unix(5000, 0)
	f := &fakeEventSource{}
	for i := 1; i <= maxEventsPerTick; i++ {
		f.records[0] = append(f.records[0], ev(1, uint64(i), 51, 3, now.Add(time.Duration(i)*time.Second)))
	}
	f.records[1] = []rawEvent{ev(2, 1, 1000, 2, now.Add(time.Second))}
	g := &eventsGroup{query: f.query}
	v, err := g.Collect(now, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	eg := v.(*EventsGroup)
	var appCount uint32
	for _, e := range eg.Events {
		if e.Channel == 2 {
			appCount += e.Count
		}
	}
	if appCount != 1 {
		t.Fatalf("Application starved by System: appCount=%d, want 1 (fair budget)", appCount)
	}
	if f.lastBudget[1] <= 0 {
		t.Fatalf("Application got no budget: %d", f.lastBudget[1])
	}
}

// A record that becomes visible later with an older timestamp is still read:
// the cursor is a record ID, not a timestamp (W6-01 delayed visibility).
func TestEventsDelayedRecord(t *testing.T) {
	now := time.Unix(6000, 0)
	f := &fakeEventSource{records: [2][]rawEvent{
		{ev(1, 1, 51, 3, now.Add(10*time.Second))},
	}}
	g := &eventsGroup{query: f.query}
	v, _ := g.Collect(now, 0)
	v.(*EventsGroup).Ack() // ack record 1
	// Record 2 has a NEWER record ID but an OLDER timestamp than record 1.
	f.records[0] = append(f.records[0], ev(1, 2, 51, 3, now.Add(5*time.Second)))
	v2, err := g.Collect(now.Add(15*time.Second), 15)
	if err != nil || v2 == nil {
		t.Fatalf("delayed record not read: (%+v, %v)", v2, err)
	}
	eg := v2.(*EventsGroup)
	if len(eg.Events) != 1 || eg.Events[0].Count != 1 {
		t.Fatalf("delayed record = %+v, want it counted (record-ID cursor, not timestamp)", eg.Events)
	}
}

// Failed renders charge the budget and surface partial quality; an unreadable
// Level is never emitted as 0 (W6-02).
func TestEventsFailedRendersChargeBudget(t *testing.T) {
	now := time.Unix(7000, 0)
	f := &fakeEventSource{unreadable: map[uint64]bool{}}
	for i := 1; i <= maxEventsPerTick+100; i++ {
		f.records[0] = append(f.records[0], ev(1, uint64(i), 51, 0, now.Add(time.Duration(i)*time.Second)))
		f.unreadable[uint64(i)] = true
	}
	g := &eventsGroup{query: f.query}
	v, err := g.Collect(now, 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	eg := v.(*EventsGroup)
	if eg.Unreadable == 0 {
		t.Fatal("failed renders not surfaced as partial quality")
	}
	if len(eg.Events) != 0 {
		t.Fatalf("unreadable records emitted as rows: %+v (unreadable Level must never be 0)", eg.Events)
	}
	if f.lastBudget[0] > maxEventsPerTick {
		t.Fatalf("budget not enforced: %d", f.lastBudget[0])
	}
	eg.Ack()
	if g.committed.load().ch[0] == 0 {
		t.Fatal("cursor did not advance over unreadable records (would rescan forever)")
	}
	if g.committed.load().ch[0] > maxEventsPerTick {
		t.Fatalf("scan exceeded the attempt budget: %d", g.committed.load().ch[0])
	}
}

// A log clear/reset restarts record IDs; the group re-reads the new generation
// and emits an explicit numeric loss marker, never a silent stall (W6-01).
func TestEventsLogReset(t *testing.T) {
	now := time.Unix(8000, 0)
	f := &fakeEventSource{records: [2][]rawEvent{
		{ev(1, 100, 51, 3, now.Add(time.Second))},
	}}
	g := &eventsGroup{query: f.query}
	v, _ := g.Collect(now, 0)
	v.(*EventsGroup).Ack() // commit cursor at record 100
	if g.committed.load().ch[0] != 100 {
		t.Fatalf("precondition cursor = %d, want 100", g.committed.load().ch[0])
	}
	g.query = func(channel uint8, afterID uint64, floor time.Time, budget int, deadline time.Time) (eventChannelRead, error) {
		if channel == 1 && afterID >= 100 {
			return eventChannelRead{
				rows:      []rawEvent{ev(1, 1, 51, 3, now.Add(2*time.Second))},
				lastID:    1,
				attempted: 1,
				reset:     true,
				lost:      afterID,
			}, nil
		}
		return eventChannelRead{lastID: afterID}, nil
	}
	v2, err := g.Collect(now.Add(15*time.Second), 15)
	if err != nil || v2 == nil {
		t.Fatalf("reset not handled: (%+v, %v)", v2, err)
	}
	eg := v2.(*EventsGroup)
	if eg.Lost == 0 {
		t.Fatal("log reset emitted no loss marker (silent gap)")
	}
	if len(eg.Events) != 1 {
		t.Fatalf("new generation not read after reset: %+v", eg.Events)
	}
}

// Append failure + retry: a batch that is never acknowledged is retained and
// re-emitted; once acknowledged it commits and is not replayed (W6-06).
func TestEventsAppendFailureRetry(t *testing.T) {
	now := time.Unix(9000, 0)
	f := &fakeEventSource{records: [2][]rawEvent{
		{ev(1, 1, 51, 3, now.Add(time.Second))},
	}}
	g := &eventsGroup{query: f.query}
	v1, _ := g.Collect(now, 0)
	eg1 := v1.(*EventsGroup)
	// The append failed before the batch was serialized: no ack. The next tick
	// must re-emit the SAME batch, not skip it.
	v2, _ := g.Collect(now.Add(15*time.Second), 15)
	if !reflect.DeepEqual(v2.(*EventsGroup).Events, eg1.Events) {
		t.Fatalf("failed-append batch not retried: %+v vs %+v", v2.(*EventsGroup).Events, eg1.Events)
	}
	if g.committed.load() != (eventCursors{}) {
		t.Fatal("cursor committed without a successful append")
	}
	// The retry succeeds: the recorder acknowledges after the durable write.
	v2.(*EventsGroup).Ack()
	if g.committed.load().ch[0] != 1 {
		t.Fatalf("successful retry did not commit: %+v", g.committed.load())
	}
	f.records[0] = append(f.records[0], ev(1, 2, 51, 3, now.Add(20*time.Second)))
	v3, _ := g.Collect(now.Add(30*time.Second), 15)
	eg3 := v3.(*EventsGroup)
	if len(eg3.Events) != 1 || eg3.Events[0].Count != 1 || !eg3.Events[0].T.Equal(now.Add(20*time.Second)) {
		t.Fatalf("post-retry batch = %+v, want only the new record (no duplicate)", eg3.Events)
	}
}

func TestEventsAccessDeniedOmitted(t *testing.T) {
	g := &eventsGroup{query: func(uint8, uint64, time.Time, int, time.Time) (eventChannelRead, error) {
		return eventChannelRead{}, errors.New("access denied")
	}}
	v, err := g.Collect(time.Unix(4000, 0), 0)
	if err == nil || v != nil {
		t.Fatalf("Collect = (%+v, %v), want (nil, error) on access denied", v, err)
	}
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
