package blackbox

import (
	"encoding/binary"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// tcpFakeSource is a fake CounterSource with expansion bookkeeping: Paths
// feeds the spec build, RefreshInstances records re-expansion calls.
// resetStep, when non-zero, advances the cumulative Connections Reset
// counters on every Collect (KindRate semantics: totals climb over time).
type tcpFakeSource struct {
	paths      []string
	readings   []CounterReading
	resetStep  float64
	refreshes  int
	closed     bool
	err        error
	refreshErr error
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
func (f *tcpFakeSource) Close() error {
	tcpFakeMu.Lock()
	f.closed = true
	tcpFakeMu.Unlock()
	return nil
}
func (f *tcpFakeSource) Paths() []string         { return f.paths }
func (f *tcpFakeSource) RefreshInstances() error { f.refreshes++; return f.refreshErr }

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

func tcpOKCounts() (tcpStates, error) {
	return tcpStates{counts: []TCPStateCount{{State: 5, Count: 7}, {State: 2, Count: 3}}}, nil
}

func newTCPTestGroup(t *testing.T, src *tcpFakeSource, counts func() (tcpStates, error)) *tcpGroup {
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
		binary.LittleEndian.PutUint32(buf[off:], s)             // dwState
		binary.LittleEndian.PutUint32(buf[off+4:], 0x0100007F)  // local addr
		binary.LittleEndian.PutUint32(buf[off+8:], 0x5000)      // local port
		binary.LittleEndian.PutUint32(buf[off+12:], 0x08080808) // remote addr
		binary.LittleEndian.PutUint32(buf[off+16:], 0x1BB)      // remote port
		binary.LittleEndian.PutUint32(buf[off+20:], 4242)       // owning PID
	}
	want := []TCPStateCount{{State: 2, Count: 1}, {State: 5, Count: 2}, {State: 11, Count: 1}}
	if got, trunc := tcpStateCountsFromTable(buf); !reflect.DeepEqual(got, want) || trunc {
		t.Fatalf("counts = %+v trunc=%v, want %+v complete (state counts only)", got, trunc, want)
	}
	// The scan is capped: rows beyond maxTCPStateRows are never counted, and
	// the result is labeled truncated, never an unlabeled complete count.
	big := make([]byte, 4+(maxTCPStateRows+4)*tcpRowStride)
	binary.LittleEndian.PutUint32(big, maxTCPStateRows+4)
	for i := 0; i < maxTCPStateRows+4; i++ {
		binary.LittleEndian.PutUint32(big[4+i*tcpRowStride:], 5)
	}
	got, trunc := tcpStateCountsFromTable(big)
	if len(got) != 1 || got[0].State != 5 || got[0].Count != maxTCPStateRows || !trunc {
		t.Fatalf("capped scan = %+v trunc=%v, want one state 5 count %d truncated", got, trunc, maxTCPStateRows)
	}
	// A truncated buffer counts complete rows only, never panics, and is labeled.
	if got, trunc := tcpStateCountsFromTable(buf[:4+tcpRowStride+10]); len(got) != 1 || got[0].State != 5 || got[0].Count != 1 || !trunc {
		t.Fatalf("truncated table = %+v trunc=%v, want only the first complete row, truncated", got, trunc)
	}
	// An empty table (zero rows) is complete, not truncated.
	empty := make([]byte, 4)
	if got, trunc := tcpStateCountsFromTable(empty); len(got) != 0 || trunc {
		t.Fatalf("empty table = %+v trunc=%v, want no counts and not truncated", got, trunc)
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
	failCounts := func() (tcpStates, error) { return tcpStates{}, errors.New("GetTcpTable2 failed") }
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

func TestTCPRefreshOnTimer(t *testing.T) {
	src := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	// A process-set change must not trigger the expansion any more.
	gi, err := newTCPGroup(GroupDeps{Source: src, Cores: 4, ProcessSetChanged: func() bool { return true }})
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
		t.Fatalf("refreshes = %d, want 0 on the first collect", src.refreshes)
	}
	if _, err := g.Collect(now.Add(15*time.Second), 15); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 0 {
		t.Fatalf("refreshes = %d, want 0 before the timer is due", src.refreshes)
	}
	src.paths = append(src.paths, `\Network Interface(eth1)\Output Queue Length`)
	src.readings = append(src.readings, tcpReading(`\Network Interface(eth1)\Output Queue Length`, 3))
	v, err := g.Collect(now.Add(wildcardRefreshEvery), 15)
	if err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 when due", src.refreshes)
	}
	if grp := v.(*TCPGroup); grp.OutboundQueue == nil || *grp.OutboundQueue != 5 {
		t.Fatalf("OutboundQueue = %v, want 5 after re-expansion", grp.OutboundQueue)
	}
	if _, err := g.Collect(now.Add(wildcardRefreshEvery+15*time.Second), 15); err != nil {
		t.Fatal(err)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want no re-expand inside the period", src.refreshes)
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

// The slow Network Interface expansion runs in the background: the first Collect
// returns at once with the rate-only source, and the wildcard source is swapped
// in by a later Collect once it is ready (PERF-16).
func TestTCPWildcardWarmsInBackground(t *testing.T) {
	rate := &tcpFakeSource{paths: tcpTestPaths()[:4], readings: tcpTestReadings()[:4]}
	wild := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	g := &tcpGroup{src: rate, cores: 4, owned: true, counts: tcpOKCounts,
		openWild: func([]string) (CounterSource, error) {
			started <- struct{}{}
			<-release
			return wild, nil
		}}
	g.rebuild(tcpPathsOf(rate))
	now := time.Unix(1000, 0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := g.Collect(now, 0); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Collect waited for the slow wildcard expansion")
	}
	<-started
	v, _ := g.Collect(now.Add(15*time.Second), 15)
	if grp := v.(*TCPGroup); grp.OutboundQueue != nil {
		t.Fatalf("OutboundQueue = %v before the expansion finished", *grp.OutboundQueue)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for g.src != CounterSource(wild) && time.Now().Before(deadline) {
		_, _ = g.Collect(now.Add(30*time.Second), 15)
		time.Sleep(5 * time.Millisecond)
	}
	if g.src != CounterSource(wild) {
		t.Fatal("the wildcard source was never swapped in")
	}
	if !rate.closed {
		t.Fatal("the rate-only source must be closed after the swap")
	}
	v, _ = g.Collect(now.Add(45*time.Second), 15)
	if grp := v.(*TCPGroup); grp.OutboundQueue == nil {
		t.Fatal("OutboundQueue missing after the swap")
	}
}

func TestTCPWildcardWarmFailureKeepsRateSource(t *testing.T) {
	rate := &tcpFakeSource{paths: tcpTestPaths()[:4], readings: tcpTestReadings()[:4]}
	opened := make(chan struct{})
	g := &tcpGroup{src: rate, cores: 4, owned: true, counts: tcpOKCounts,
		openWild: func([]string) (CounterSource, error) {
			defer close(opened)
			return nil, errors.New("no wildcard")
		}}
	g.rebuild(tcpPathsOf(rate))
	now := time.Unix(1000, 0)
	_, _ = g.Collect(now, 0)
	<-opened
	deadline := time.Now().Add(2 * time.Second)
	for g.warm != nil && time.Now().Before(deadline) {
		_, _ = g.Collect(now.Add(15*time.Second), 15)
		time.Sleep(5 * time.Millisecond)
	}
	if g.warm != nil {
		t.Fatal("the failed expansion was never taken")
	}
	if g.src != CounterSource(rate) || rate.closed {
		t.Fatal("a failed expansion must keep the rate-only source")
	}
	// One attempt only.
	_, _ = g.Collect(now.Add(30*time.Second), 15)
	if g.warm != nil {
		t.Fatal("a second expansion was started")
	}
}

// Closing while the expansion is still running does not leak the new source.
func TestTCPCloseWhileWarmingClosesTheLateSource(t *testing.T) {
	rate := &tcpFakeSource{paths: tcpTestPaths()[:4], readings: tcpTestReadings()[:4]}
	wild := &tcpFakeSource{paths: tcpTestPaths(), readings: tcpTestReadings()}
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	g := &tcpGroup{src: rate, cores: 4, owned: true, counts: tcpOKCounts,
		openWild: func([]string) (CounterSource, error) {
			started <- struct{}{}
			<-release
			return wild, nil
		}}
	g.rebuild(tcpPathsOf(rate))
	_, _ = g.Collect(time.Unix(1000, 0), 0)
	<-started
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if !rate.closed {
		t.Fatal("rate source not closed")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if closedFlag(wild) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the late wildcard source was never closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var tcpFakeMu sync.Mutex

func closedFlag(f *tcpFakeSource) bool {
	tcpFakeMu.Lock()
	defer tcpFakeMu.Unlock()
	return f.closed
}
