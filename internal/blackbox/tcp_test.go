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
type tcpFakeSource struct {
	paths     []string
	readings  []CounterReading
	refreshes int
	closed    bool
	err       error
}

func (f *tcpFakeSource) Collect() ([]CounterReading, error) { return f.readings, f.err }
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
		tcpReading(`\TCPv4\Connections Reset`, 0.25),
		tcpReading(`\TCPv6\Connections Reset`, 0.75),
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
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
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

func TestTCPMissingOmitted(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
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
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	// A second interface: the outbound queue is the sum over interfaces.
	src.paths = append(src.paths, `\Network Interface(eth1)\Output Queue Length`)
	src.readings = append(src.readings, tcpReading(`\Network Interface(eth1)\Output Queue Length`, 3))
	grp := tcpCollectTicks(t, newTCPTestGroup(t, src, tcpOKCounts), 3)
	if grp.RetransmitsPerSec == nil || *grp.RetransmitsPerSec != 4 { // 1.5 + 2.5
		t.Fatalf("RetransmitsPerSec = %v, want 4 (v4+v6 summed)", grp.RetransmitsPerSec)
	}
	if grp.ResetsPerSec == nil || *grp.ResetsPerSec != 1 { // 0.25 + 0.75
		t.Fatalf("ResetsPerSec = %v, want 1 (v4+v6 summed)", grp.ResetsPerSec)
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