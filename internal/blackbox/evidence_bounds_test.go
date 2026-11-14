package blackbox

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// Wave-6 evidence bounds: the TCP native-table allocation ceiling and retry
// bound (W6-03), and partial interface queues / refresh-failure independence
// (W6-04). These use the injected seams, so no Windows or live TCP is needed.

// W6-03: an oversize native size is rejected BEFORE any allocation. readInto
// must never be called, proving the whole table is not allocated just to count
// a prefix.
func TestTCPTableOversizeRejectedBeforeAllocation(t *testing.T) {
	readCalled := false
	allocBytes := 0
	sizeOf := func() (uint32, error) { return maxTCPTableBytes + 1, nil }
	readInto := func(buf []byte) (uint32, error) {
		readCalled = true
		allocBytes = len(buf)
		return uint32(len(buf)), nil
	}
	res, err := tcpTableSnapshot(sizeOf, readInto)
	if err != nil {
		t.Fatalf("oversize: %v", err)
	}
	if readCalled {
		t.Fatalf("oversize table allocated/read %d bytes before the ceiling check", allocBytes)
	}
	if !res.truncated || len(res.counts) != 0 {
		t.Fatalf("oversize = %+v, want truncated with no unlabeled complete counts", res)
	}
}

// W6-03: empty, unreadable and incomplete are distinct outcomes.
func TestTCPTableEmptyUnreadableIncomplete(t *testing.T) {
	// Empty: a zero-size table is complete, not truncated, no error.
	res, err := tcpTableSnapshot(func() (uint32, error) { return 0, nil },
		func([]byte) (uint32, error) { t.Fatal("read on empty table"); return 0, nil })
	if err != nil || res.truncated || len(res.counts) != 0 {
		t.Fatalf("empty = %+v err=%v, want no counts, not truncated, no error", res, err)
	}
	// Unreadable: the size query fails -> error (States nil), not a zero count.
	_, err = tcpTableSnapshot(func() (uint32, error) { return 0, errTCPTableUnreadable },
		func([]byte) (uint32, error) { t.Fatal("read after size error"); return 0, nil })
	if !errors.Is(err, errTCPTableUnreadable) {
		t.Fatalf("unreadable err = %v, want errTCPTableUnreadable", err)
	}
	// Incomplete: oversize -> truncated, no error.
	res, err = tcpTableSnapshot(func() (uint32, error) { return maxTCPTableBytes + 100, nil },
		func([]byte) (uint32, error) { t.Fatal("read on oversize"); return 0, nil })
	if err != nil || !res.truncated {
		t.Fatalf("incomplete = %+v err=%v, want truncated, no error", res, err)
	}
	// A small complete table counts normally and is not truncated.
	buf := make([]byte, 4+2*tcpRowStride)
	binary.LittleEndian.PutUint32(buf, 2)
	binary.LittleEndian.PutUint32(buf[4:], 5)
	binary.LittleEndian.PutUint32(buf[4+tcpRowStride:], 2)
	res, err = tcpTableSnapshot(func() (uint32, error) { return uint32(len(buf)), nil },
		func(b []byte) (uint32, error) { copy(b, buf); return uint32(len(buf)), nil })
	if err != nil || res.truncated || len(res.counts) != 2 {
		t.Fatalf("complete = %+v err=%v, want 2 state counts, not truncated", res, err)
	}
}

// W6-03: a table that keeps growing is retried a bounded number of times, then
// reported incomplete — never an unbounded retry loop.
func TestTCPTableBoundedRetryOnGrowth(t *testing.T) {
	sizeCalls := 0
	sizeOf := func() (uint32, error) { sizeCalls++; return 4 + tcpRowStride, nil }
	readInto := func([]byte) (uint32, error) { return 0, errTCPTableGrew }
	res, err := tcpTableSnapshot(sizeOf, readInto)
	if err != nil {
		t.Fatalf("growth: %v", err)
	}
	if sizeCalls != tcpTableMaxAttempts {
		t.Fatalf("size calls = %d, want bounded %d attempts", sizeCalls, tcpTableMaxAttempts)
	}
	if !res.truncated {
		t.Fatalf("persistent growth = %+v, want truncated", res)
	}
}

// W6-03: a single growth retry that then succeeds returns the counts.
func TestTCPTableRetryThenSucceeds(t *testing.T) {
	buf := make([]byte, 4+tcpRowStride)
	binary.LittleEndian.PutUint32(buf, 1)
	binary.LittleEndian.PutUint32(buf[4:], 5)
	reads := 0
	res, err := tcpTableSnapshot(
		func() (uint32, error) { return uint32(len(buf)), nil },
		func(b []byte) (uint32, error) {
			reads++
			if reads == 1 {
				return 0, errTCPTableGrew
			}
			copy(b, buf)
			return uint32(len(buf)), nil
		})
	if err != nil || res.truncated || len(res.counts) != 1 || res.counts[0].Count != 1 {
		t.Fatalf("retry-then-succeed = %+v err=%v, want one state 5 count 1", res, err)
	}
}

// W6-03: the group labels a truncated table, and distinguishes it from an empty
// or unreadable one.
func TestTCPGroupStatesTruncatedVsEmptyVsUnreadable(t *testing.T) {
	now := time.Unix(1000, 0)
	// Truncated: StatesTruncated set even when no counts could be read.
	g := newTCPTestGroup(t, &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()},
		func() (tcpStates, error) { return tcpStates{truncated: true}, nil })
	v, err := g.Collect(now, 0)
	if err != nil {
		t.Fatal(err)
	}
	grp := v.(*TCPGroup)
	if !grp.StatesTruncated || grp.States != nil {
		t.Fatalf("truncated group = %+v, want StatesTruncated and no unlabeled counts", grp)
	}
	// Empty: no states, not truncated.
	g2 := newTCPTestGroup(t, &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()},
		func() (tcpStates, error) { return tcpStates{}, nil })
	v2, _ := g2.Collect(now, 0)
	grp2 := v2.(*TCPGroup)
	if grp2.StatesTruncated || grp2.States != nil {
		t.Fatalf("empty group = %+v, want no states and not truncated", grp2)
	}
	// Unreadable: error leaves States nil and not truncated; rates survive.
	g3 := newTCPTestGroup(t, &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings(), resetStep: 7.5},
		func() (tcpStates, error) { return tcpStates{}, errTCPTableUnreadable })
	grp3 := tcpCollectTicks(t, g3, 3)
	if grp3.States != nil || grp3.StatesTruncated {
		t.Fatalf("unreadable group = %+v, want States nil, not truncated", grp3)
	}
	if grp3.RetransmitsPerSec == nil {
		t.Fatal("rates must survive an unreadable state query")
	}
}

// W6-04: one valid zero plus one unreadable interface is a PARTIAL sum with a
// covered-interface count, never a false complete machine-wide 0.
func TestTCPPartialInterfaceQueueNotFalseZero(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	src.paths = append(src.paths, `\Network Interface(eth1)\Output Queue Length`)
	src.readings = append(src.readings, tcpReading(`\Network Interface(eth1)\Output Queue Length`, 0))
	// eth0 reads a valid zero; eth1 is unreadable.
	src.readings[4] = tcpReading(`\Network Interface(eth0)\Output Queue Length`, 0)
	src.readings[5] = CounterReading{Path: `\Network Interface(eth1)\Output Queue Length`, Value: 99, Status: 1, OK: false}
	grp := tcpCollectTicks(t, newTCPTestGroup(t, src, tcpOKCounts), 3)
	if grp.OutboundQueue == nil || *grp.OutboundQueue != 0 {
		t.Fatalf("OutboundQueue = %v, want the covered sum 0", grp.OutboundQueue)
	}
	if !grp.OutboundQueuePartial {
		t.Fatal("partial interface sum published as a complete total (false zero)")
	}
	if grp.OutboundQueueInterfaces != 1 {
		t.Fatalf("covered interfaces = %d, want 1", grp.OutboundQueueInterfaces)
	}
}

// W6-04: with every interface readable the total is complete (not partial).
func TestTCPCompleteInterfaceQueue(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	src.paths = append(src.paths, `\Network Interface(eth1)\Output Queue Length`)
	src.readings = append(src.readings, tcpReading(`\Network Interface(eth1)\Output Queue Length`, 3))
	grp := tcpCollectTicks(t, newTCPTestGroup(t, src, tcpOKCounts), 3)
	if grp.OutboundQueue == nil || *grp.OutboundQueue != 5 {
		t.Fatalf("OutboundQueue = %v, want 5 (2+3)", grp.OutboundQueue)
	}
	if grp.OutboundQueuePartial {
		t.Fatal("complete interface sum marked partial")
	}
	if grp.OutboundQueueInterfaces != 2 {
		t.Fatalf("covered interfaces = %d, want 2", grp.OutboundQueueInterfaces)
	}
}

// W6-04: a RefreshInstances failure must not suppress the independent state
// query (or the rates). The previous expansion is kept.
func TestTCPRefreshFailureKeepsStates(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings(), refreshErr: errors.New("refresh failed")}
	gi, err := newTCPGroup(GroupDeps{Source: src, Cores: 4, ProcessSetChanged: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	g := gi.(*tcpGroup)
	g.counts = tcpOKCounts
	v, err := g.Collect(time.Unix(1000, 0), 0)
	if err != nil {
		t.Fatalf("refresh failure failed the whole tick: %v", err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 (attempted)", src.refreshes)
	}
	grp, ok := v.(*TCPGroup)
	if !ok || grp.States == nil {
		t.Fatalf("states suppressed by a PDH refresh failure: %+v", v)
	}
	want := []TCPStateCount{{State: 5, Count: 7}, {State: 2, Count: 3}}
	if len(grp.States) != 2 || grp.States[0] != want[0] || grp.States[1] != want[1] {
		t.Fatalf("States = %+v, want %+v", grp.States, want)
	}
}

// W6-06 across the real goroutine boundary: the group collects in its budget
// worker, and the acknowledgement (marshal) happens on the tick goroutine, just
// as the recorder does. The committed cursor is an atomic pointer, so the
// handoff is safe and the next worker reads forward without replay.
func TestEventsAckAcrossWorkerBoundary(t *testing.T) {
	now := time.Unix(11000, 0)
	f := &fakeEventSource{records: [2][]rawEvent{
		{ev(1, 1, 51, 3, now.Add(time.Second))},
	}}
	g := &eventsGroup{query: f.query}
	c := NewGroupCollector(&stubCollector{}, []Group{g}, NewBudget(15))

	// Tick 1: the worker collects record 1; the tick goroutine acks by marshaling.
	s1, err := c.Collect(now)
	if err != nil {
		t.Fatal(err)
	}
	if s1.Events == nil || len(s1.Events.Events) != 1 {
		t.Fatalf("tick 1 events = %+v, want record 1", s1.Events)
	}
	s1.Events.Ack() // ack on the tick goroutine, after the durable write
	// Tick 2: a new worker reads forward from the committed cursor; record 1 is
	// not replayed, only the new record 2.
	f.records[0] = append(f.records[0], ev(1, 2, 51, 3, now.Add(20*time.Second)))
	s2, err := c.Collect(now.Add(15 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s2.Events == nil || len(s2.Events.Events) != 1 || s2.Events.Events[0].Count != 1 {
		t.Fatalf("tick 2 events = %+v, want only record 2 (no replay across the ack)", s2.Events)
	}
	if _, err := json.Marshal(s2.Events); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
