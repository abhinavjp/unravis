package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// --- memPressure: paging rates and the memory lists ---

func TestMemPressureExtraCounters(t *testing.T) {
	src := &memPressureFakeSource{readings: []CounterReading{
		memReading(`\Memory\Pages/sec`, 40),
		memReading(`\Memory\Page Faults/sec`, 900),
		memReading(`\Memory\Standby Cache Normal Priority Bytes`, 2<<30),
		memReading(`\Memory\Modified Page List Bytes`, 64<<20),
		memReading(`\Memory\Free & Zero Page List Bytes`, 512<<20),
	}}
	g := newMemPressureTestGroup(t, src)
	var grp *MemPressureGroup
	for i := 0; i < 3; i++ {
		grp = collectMemPressure(t, g, time.Unix(1000, 0).Add(time.Duration(i)*15*time.Second))
		if grp == nil {
			t.Fatalf("tick %d: nil group", i+1)
		}
	}
	if grp.PagesPerSec == nil || *grp.PagesPerSec != 40 {
		t.Fatalf("PagesPerSec = %v, want 40", grp.PagesPerSec)
	}
	if grp.PageFaultsPerSec == nil || *grp.PageFaultsPerSec != 900 {
		t.Fatalf("PageFaultsPerSec = %v, want 900", grp.PageFaultsPerSec)
	}
	if grp.StandbyBytes == nil || *grp.StandbyBytes != 2<<30 {
		t.Fatalf("StandbyBytes = %v", grp.StandbyBytes)
	}
	if grp.ModifiedBytes == nil || *grp.ModifiedBytes != 64<<20 {
		t.Fatalf("ModifiedBytes = %v", grp.ModifiedBytes)
	}
	if grp.FreeBytes == nil || *grp.FreeBytes != 512<<20 {
		t.Fatalf("FreeBytes = %v", grp.FreeBytes)
	}
}

// A counter this Windows lacks leaves its field nil and never zero.
func TestMemPressureExtraMissingStaysNil(t *testing.T) {
	src := &memPressureFakeSource{readings: []CounterReading{
		memReading(`\Memory\Modified Page List Bytes`, 0), // a real zero is a measurement
		memReading(`\Memory\Pool Paged Bytes`, 1<<20),
	}}
	grp := collectMemPressure(t, newMemPressureTestGroup(t, src), time.Unix(1000, 0))
	if grp == nil {
		t.Fatal("nil group")
	}
	if grp.StandbyBytes != nil || grp.FreeBytes != nil || grp.PagesPerSec != nil {
		t.Fatalf("absent counters must be nil: %+v", grp)
	}
	if grp.ModifiedBytes == nil || *grp.ModifiedBytes != 0 {
		t.Fatalf("ModifiedBytes = %v, want a real 0", grp.ModifiedBytes)
	}
}

// --- diskCpu: modern CPU load and the switch rate ---

func TestDiskCPUUtilityAndSwitches(t *testing.T) {
	pu, cs := `\Processor Information(_Total)\% Processor Utility`, `\System\Context Switches/sec`
	src := &fakeDiskSource{paths: []string{pu, cs}, readings: []CounterReading{okReading(pu, 137.5), okReading(cs, 52000)}}
	g := newTestDiskCPU(src)
	var grp *DiskCPUGroup
	for i := 0; i < 3; i++ {
		grp = collectDiskCPU(t, g, time.Unix(1000, 0).Add(time.Duration(i)*15*time.Second))
	}
	if grp == nil || grp.CPU == nil {
		t.Fatalf("group = %+v, want a cpu block", grp)
	}
	if grp.CPU.UtilityPct == nil || *grp.CPU.UtilityPct != 137.5 {
		t.Fatalf("UtilityPct = %v, want 137.5 (boost is never capped at 100)", grp.CPU.UtilityPct)
	}
	if grp.CPU.ContextSwitchesPerSec == nil || *grp.CPU.ContextSwitchesPerSec != 52000 {
		t.Fatalf("ContextSwitchesPerSec = %v, want 52000", grp.CPU.ContextSwitchesPerSec)
	}
}

// --- tcp: connection failures and per-adapter packet loss ---

// stepSource climbs the cumulative counters named in climb by step per Collect.
type stepSource struct {
	tcpFakeSource
	climb map[string]float64
}

func (s *stepSource) Collect() ([]CounterReading, error) {
	for i := range s.readings {
		if st, ok := s.climb[s.readings[i].Path]; ok {
			s.readings[i].Value += st
		}
	}
	return s.readings, s.err
}

func TestTCPPacketLossAndFailures(t *testing.T) {
	src := &stepSource{climb: map[string]float64{}}
	add := func(p string, v, step float64) {
		src.paths = append(src.paths, p)
		src.readings = append(src.readings, tcpReading(p, v))
		if step != 0 {
			src.climb[p] = step
		}
	}
	add(`\TCPv4\Segments Retransmitted/sec`, 1, 0)
	add(`\TCPv6\Segments Retransmitted/sec`, 1, 0)
	add(`\TCPv4\Connections Reset`, 10, 0)
	add(`\TCPv6\Connections Reset`, 10, 0)
	add(`\TCPv4\Connection Failures`, 5, 15)  // +1/s
	add(`\TCPv6\Connection Failures`, 5, 30)  // +2/s
	add(`\Network Interface(eth0)\Packets Received Discarded`, 0, 150) // +10/s
	add(`\Network Interface(eth1)\Packets Received Discarded`, 0, 75)  // +5/s
	add(`\Network Interface(eth0)\Packets Outbound Errors`, 0, 30)     // +2/s
	g := newTCPTestGroup(t, &src.tcpFakeSource, tcpOKCounts)
	g.src = src
	g.rebuild(src.paths)
	grp := tcpCollectTicks(t, g, 3)
	if grp.ConnFailuresPerSec == nil || *grp.ConnFailuresPerSec != 3 {
		t.Fatalf("ConnFailuresPerSec = %v, want 3 (v4 1/s + v6 2/s)", grp.ConnFailuresPerSec)
	}
	if grp.DiscardedPerSec == nil || *grp.DiscardedPerSec != 15 {
		t.Fatalf("DiscardedPerSec = %v, want 15 (summed over adapters)", grp.DiscardedPerSec)
	}
	if grp.OutErrorsPerSec == nil || *grp.OutErrorsPerSec != 2 {
		t.Fatalf("OutErrorsPerSec = %v, want 2", grp.OutErrorsPerSec)
	}
}

// A host with hundreds of virtual adapters must not make the sampler follow
// hundreds of series: each per-adapter counter is capped.
func TestTCPAdapterCap(t *testing.T) {
	src := &tcpFakeSource{}
	for i := 0; i < tcpMaxInterfaces*3; i++ {
		for _, c := range []string{`Output Queue Length`, `Packets Received Discarded`, `Packets Outbound Errors`} {
			p := fmt.Sprintf(`\Network Interface(nic%03d)\%s`, i, c)
			src.paths = append(src.paths, p)
			src.readings = append(src.readings, tcpReading(p, 1))
		}
	}
	g := newTCPTestGroup(t, src, tcpOKCounts)
	g.rebuild(src.paths)
	for name, n := range map[string]int{"queue": len(g.queueKeys), "discarded": len(g.discKeys), "outErrors": len(g.outErrKeys)} {
		if n != tcpMaxInterfaces {
			t.Fatalf("%s keys = %d, want the cap %d", name, n, tcpMaxInterfaces)
		}
	}
}
