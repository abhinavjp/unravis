package blackbox

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"
)

// tcpFakeSource is a fake CounterSource with expansion bookkeeping: Paths
// feeds the spec build, RefreshInstances records re-expansion calls.
// resetStep, when non-zero, advances the cumulative Connections Reset
// counters on every Collect (KindRate semantics: totals climb over time).
type tcpFakeSource struct {
	paths     []string
	readings  []CounterReading
	resetStep float64
	refreshes int
	closed    bool
	err       error
}

func (f *tcpFakeSource) Collect() ([]CounterReading, error) {
	if f.resetStep != 0 {
		for i := range f.readings {
			switch f.readings[i].Path {
			case `\TCPv4\Connections Reset`, `\TCPv6\Connections Reset`:
				f.readings[i].Value += f.resetStep
			}
		}
	}
	return f.readings, f.err
}
func (f *tcpFakeSource) Close() error                       { f.closed = true; return nil }
func (f *tcpFakeSource) Paths() []string                    { return f.paths }
func (f *tcpFakeSource) RefreshInstances() error            { f.refreshes++; return nil }

func tcpReading(path string, v float64) CounterReading {
	return CounterReading{Path: path, Value: v, OK: true}
}

func tcpTestPaths() []string {
	return []string{
		`\TCPv4\Segments Retransmitted/sec`,
		`\TCPv6\Segments Retransmitted/sec`,
		`\TCPv4\Connections Reset`,
		`\TCPv6\Connections Reset`,
		`\Network Interface(eth0)\Output Queue Length`,
	}
}

func tcpTestReadings() []CounterReading {
	return []CounterReading{
		tcpReading(`\TCPv4\Segments Retransmitted/sec`, 1.5),
		tcpReading(`\TCPv6\Segments Retransmitted/sec`, 2.5),
		// Cumulative totals (KindRate), not per-second values. With
		// resetStep 7.5 each family climbs 7.5 per 15 s tick: 0.5/s each,
		// so the v4+v6 delta rate sums to 1/s.
		tcpReading(`\TCPv4\Connections Reset`, 100),
		tcpReading(`\TCPv6\Connections Reset`, 200),
		tcpReading(`\Network Interface(eth0)\Output Queue Length`, 2),
	}
}

func tcpOKCounts() ([]TCPStateCount, error) {
	return []TCPStateCount{{State: 5, Count: 7}, {State: 2, Count: 3}}, nil
}

func newTCPTestGroup(t *testing.T, src *tcpFakeSource, counts func() ([]TCPStateCount, error)) *tcpGroup {
	t.Helper()
	gi, err := newTCPGroup(GroupDeps{Source: src, Cores: 4})
	if err != nil {
		t.Fatalf("newTCPGroup: %v", err)
	}
	g := gi.(*tcpGroup)
	g.counts = counts // inject the table query: no Windows needed
	return g
}

// tcpCollectTicks runs n ticks 15 s apart and returns the last group.
func tcpCollectTicks(t *testing.T, g *tcpGroup, n int) *TCPGroup {
	t.Helper()
	now := time.Unix(1000, 0)
	var grp *TCPGroup
	for i := 0; i < n; i++ {
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		grp, _ = v.(*TCPGroup)
		if grp == nil {
			t.Fatalf("tick %d: nil group with valid input", i+1)
		}
	}
	return grp
}

func TestTCPStateCountsNoAddresses(t *testing.T) {
	// TCPStateCount must carry only State+Count: no address/port/PID field.
	typ := reflect.TypeOf(TCPStateCount{})
	if typ.NumField() != 2 {
		t.Fatalf("TCPStateCount has %d fields, want only State+Count", typ.NumField())
	}
	for _, name := range []string{"State", "Count"} {
		if _, ok := typ.FieldByName(name); !ok {
			t.Fatalf("TCPStateCount missing field %s", name)
		}
	}
	// Fake MIB_TCPTABLE2 rows with addresses, ports and PIDs filled in:
	// only dwState may survive the scan.
	const rows = 4
	buf := make([]byte, 4+rows*tcpRowStride)
	binary.LittleEndian.PutUint32(buf, rows)
	for i, s := range []uint32{5, 5, 2, 11} {
		off := 4 + i*tcpRowStride
		binary.LittleEndian.PutUint32(buf[off:], s)          // dwState
		binary.LittleEndian.PutUint32(buf[off+4:], 0x0100007F) // local addr
		binary.LittleEndian.PutUint32(buf[off+8:], 0x5000)     // local port
		binary.LittleEndian.PutUint32(buf[off+12:], 0x08080808) // remote addr
		binary.LittleEndian.PutUint32(buf[off+16:], 0x1BB)     // remote port
		binary.LittleEndian.PutUint32(buf[off+20:], 4242)      // owning PID
	}
	want := []TCPStateCount{{State: 2, Count: 1}, {State: 5, Count: 2}, {State: 11, Count: 1}}
	if got := tcpStateCountsFromTable(buf); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %+v, want %+v (state counts only)", got, want)
	}
	// The scan is capped: rows beyond maxTCPStateRows are never counted.
	big := make([]byte, 4+(maxTCPStateRows+4)*tcpRowStride)
	binary.LittleEndian.PutUint32(big, maxTCPStateRows+4)
	for i := 0; i < maxTCPStateRows+4; i++ {
		binary.LittleEndian.PutUint32(big[4+i*tcpRowStride:], 5)
	}
	got := tcpStateCountsFromTable(big)
	if len(got) != 1 || got[0].State != 5 || got[0].Count != maxTCPStateRows {
		t.Fatalf("capped scan = %+v, want one state 5 count %d", got, maxTCPStateRows)
	}
	// A truncated buffer counts complete rows only, never panics.
	if got := tcpStateCountsFromTable(buf[:4+tcpRowStride+10]); len(got) != 1 || got[0].State != 5 || got[0].Count != 1 {
		t.Fatalf("truncated table = %+v, want only the first complete row", got)
	}
}

func TestTCPRatesGated(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings(), resetStep: 7.5}
	g := newTCPTestGroup(t, src, tcpOKCounts)
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		grp := v.(*TCPGroup)
		if grp.RetransmitsPerSec != nil || grp.ResetsPerSec != nil {
			t.Fatalf("tick %d: rate emitted before two baselines", i+1)
		}
		if grp.OutboundQueue == nil || *grp.OutboundQueue != 2 {
			t.Fatalf("tick %d: gauge must not wait for baselines", i+1)
		}
	}
	v, err := g.Collect(now.Add(30*time.Second), 15)
	if err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	grp := v.(*TCPGroup)
	if grp.RetransmitsPerSec == nil || *grp.RetransmitsPerSec != 4 {
		t.Fatalf("RetransmitsPerSec = %v, want 4 after two baselines", grp.RetransmitsPerSec)
	}
	if grp.ResetsPerSec == nil || *grp.ResetsPerSec != 1 {
		t.Fatalf("ResetsPerSec = %v, want 1 after two baselines", grp.ResetsPerSec)
	}
}

// TestTCPResetsIsRate locks the BBX-4-02 fix: Connections Reset is a
// cumulative counter, so resetsPerSec must be the per-second delta
// (KindRate), never the raw cumulative total that climbs with uptime.
func TestTCPResetsIsRate(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	g := newTCPTestGroup(t, src, tcpOKCounts)
	now := time.Unix(1000, 0)
	setResets := func(v4, v6 float64) {
		src.readings[2] = tcpReading(`\TCPv4\Connections Reset`, v4)
		src.readings[3] = tcpReading(`\TCPv6\Connections Reset`, v6)
	}
	collect := func(sec int) *TCPGroup {
		t.Helper()
		v, err := g.Collect(now.Add(time.Duration(sec)*time.Second), 1)
		if err != nil {
			t.Fatalf("tick at +%ds: %v", sec, err)
		}
		grp, _ := v.(*TCPGroup)
		if grp == nil {
			t.Fatalf("tick at +%ds: nil group with valid input", sec)
		}
		return grp
	}
	// Two baselines at cumulative 100 (v4) / 200 (v6): nothing emitted yet.
	setResets(100, 200)
	if grp := collect(0); grp.ResetsPerSec != nil {
		t.Fatalf("baseline 1 emitted resetsPerSec %v", *grp.ResetsPerSec)
	}
	if grp := collect(1); grp.ResetsPerSec != nil {
		t.Fatalf("baseline 2 emitted resetsPerSec %v", *grp.ResetsPerSec)
	}
	// 100 -> 160 over 1 s: delta rate 60, NOT the raw cumulative total.
	// v6 stays flat: a constant cumulative counter contributes 0, not 200.
	setResets(160, 200)
	grp := collect(2)
	if grp.ResetsPerSec == nil || *grp.ResetsPerSec != 60 {
		t.Fatalf("ResetsPerSec = %v, want 60 (delta rate, not the cumulative total)", grp.ResetsPerSec)
	}
	// Retransmit counters are true /sec PDH rates: still emitted as-is.
	if grp.RetransmitsPerSec == nil || *grp.RetransmitsPerSec != 4 {
		t.Fatalf("RetransmitsPerSec = %v, want 4 (PDH rate unchanged)", grp.RetransmitsPerSec)
	}
	// Second interval: 160 -> 250 over 1 s -> 90.
	setResets(250, 200)
	if grp := collect(3); grp.ResetsPerSec == nil || *grp.ResetsPerSec != 90 {
		t.Fatalf("ResetsPerSec = %v, want 90 on the second interval", grp.ResetsPerSec)
	}
	// Counter reset: the cumulative value drops -> no negative rate, and
	// the engine re-gates behind two fresh baselines before emitting again.
	setResets(10, 200)
	if grp := collect(4); grp.ResetsPerSec != nil {
		t.Fatalf("counter reset emitted %v; want nil (never a negative rate)", *grp.ResetsPerSec)
	}
	if grp := collect(5); grp.ResetsPerSec != nil {
		t.Fatalf("post-reset baseline emitted %v; want nil", *grp.ResetsPerSec)
	}
	setResets(40, 200)
	if grp := collect(6); grp.ResetsPerSec == nil || *grp.ResetsPerSec != 30 {
		t.Fatalf("ResetsPerSec = %v, want 30 after re-gating (40-10 over 1 s)", grp.ResetsPerSec)
	}
}

func TestTCPMissingOmitted(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings(), resetStep: 7.5}
	// Invalid retransmit counters: the field must stay nil, never 0.
	src.readings[0] = CounterReading{Path: src.paths[0], Value: 99, Status: 1, OK: false}
	src.readings[1] = CounterReading{Path: src.paths[1], Value: 99, Status: 1, OK: false}
	grp := tcpCollectTicks(t, newTCPTestGroup(t, src, tcpOKCounts), 3)
	if grp.RetransmitsPerSec != nil {
		t.Fatalf("invalid counter emitted %v; missing must stay nil, never 0", *grp.RetransmitsPerSec)
	}
	if grp.ResetsPerSec == nil || *grp.ResetsPerSec != 1 || grp.OutboundQueue == nil {
		t.Fatalf("valid fields lost: %+v", grp)
	}
	// One family missing: the machine-wide sum stays nil too (no partial 0).
	src2 := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	src2.readings[1] = CounterReading{Path: src2.paths[1], Value: 99, Status: 1, OK: false}
	grp = tcpCollectTicks(t, newTCPTestGroup(t, src2, tcpOKCounts), 3)
	if grp.RetransmitsPerSec != nil {
		t.Fatalf("partial v4+v6 sum emitted %v; want nil", *grp.RetransmitsPerSec)
	}
	// A failed state query leaves States nil but keeps the rates.
	failCounts := func() ([]TCPStateCount, error) { return nil, errors.New("GetTcpTable2 failed") }
	src3 := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	grp = tcpCollectTicks(t, newTCPTestGroup(t, src3, failCounts), 3)
	if grp.States != nil {
		t.Fatalf("failed state query must leave States nil, got %+v", grp.States)
	}
	if grp.RetransmitsPerSec == nil || grp.ResetsPerSec == nil {
		t.Fatal("rates must survive a failed state query")
	}
	// Both sides fail: the whole group is omitted (nil), never zero-filled.
	bad := &tcpFakeSource{paths: tcpTestPaths(), err: errors.New("PDH collect failed")}
	g := newTCPTestGroup(t, bad, failCounts)
	v, err := g.Collect(time.Unix(1000, 0), 0)
	if err != nil || v != nil {
		t.Fatalf("both failed: (%v, %v), want (nil, nil)", v, err)
	}
}

func TestTCPSumV4V6(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings(), resetStep: 7.5}
	// A second interface: the outbound queue is the sum over interfaces.
	src.paths = append(src.paths, `\Network Interface(eth1)\Output Queue Length`)
	src.readings = append(src.readings, tcpReading(`\Network Interface(eth1)\Output Queue Length`, 3))
	grp := tcpCollectTicks(t, newTCPTestGroup(t, src, tcpOKCounts), 3)
	if grp.RetransmitsPerSec == nil || *grp.RetransmitsPerSec != 4 { // 1.5 + 2.5
		t.Fatalf("RetransmitsPerSec = %v, want 4 (v4+v6 summed)", grp.RetransmitsPerSec)
	}
	if grp.ResetsPerSec == nil || *grp.ResetsPerSec != 1 { // (7.5 + 7.5) / 15 s
		t.Fatalf("ResetsPerSec = %v, want 1 (v4+v6 delta rates summed)", grp.ResetsPerSec)
	}
	if grp.OutboundQueue == nil || *grp.OutboundQueue != 5 { // 2 + 3
		t.Fatalf("OutboundQueue = %v, want 5 (sum over interfaces)", grp.OutboundQueue)
	}
	want := []TCPStateCount{{State: 5, Count: 7}, {State: 2, Count: 3}}
	if !reflect.DeepEqual(grp.States, want) {
		t.Fatalf("States = %+v, want %+v", grp.States, want)
	}
}

func TestTCPRefreshOnChange(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	changed := false
	gi, err := newTCPGroup(GroupDeps{Source: src, Cores: 4, ProcessSetChanged: func() bool { return changed }})
	if err != nil {
		t.Fatal(err)
	}
	g := gi.(*tcpGroup)
	g.counts = tcpOKCounts
	now := time.Unix(1000, 0)
	if _, err := g.Collect(now, 0); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 0 {
		t.Fatalf("refreshes = %d, want 0 without a process-set change", src.refreshes)
	}
	changed = true
	src.paths = append(src.paths, `\Network Interface(eth1)\Output Queue Length`)
	src.readings = append(src.readings, tcpReading(`\Network Interface(eth1)\Output Queue Length`, 3))
	v, err := g.Collect(now.Add(15*time.Second), 15)
	if err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 on change", src.refreshes)
	}
	if grp := v.(*TCPGroup); grp.OutboundQueue == nil || *grp.OutboundQueue != 5 {
		t.Fatalf("OutboundQueue = %v, want 5 after re-expansion", grp.OutboundQueue)
	}
	changed = false
	if _, err := g.Collect(now.Add(30*time.Second), 15); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want no re-expand without a change", src.refreshes)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if src.closed {
		t.Fatal("injected source must not be closed by the group")
	}
}

func TestTCPGroupRegistered(t *testing.T) {
	registryMu.Lock()
	reg, ok := registry["tcp"]
	registryMu.Unlock()
	if !ok {
		t.Fatal("tcp group not registered from init()")
	}
	if !reg.optional {
		t.Fatal("tcp group must be optional (ships off when it breaks budget)")
	}
}