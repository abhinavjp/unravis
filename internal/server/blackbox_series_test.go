package server

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

func u64(v uint64) *uint64 { return &v }

// fullSample carries a reading in every group the recorder can write.
func fullSample(t time.Time, i int) blackbox.Sample {
	x := float64(i)
	return blackbox.Sample{
		V: 1, Run: "r1", Seq: uint64(i + 1), T: t,
		CPU: f64(10 + x), MemAvail: u64(1 << 30), MemCommit: u64(2 << 30), DiskQueue: f64(1), DiskMs: f64(5), DiskFreePct: f64(40),
		NetIn: u64(uint64(1000 * (i + 1))), NetOut: u64(uint64(500 * (i + 1))),
		Procs:       []blackbox.ProcRow{{PID: 4, Name: "sqlservr", CPUPct: f64(30 + x), WS: u64(1 << 29), Private: u64(1 << 28)}, {PID: 9, Name: "w3wp", CPUPct: f64(2)}},
		HTTP:        &blackbox.HTTPGroup{Queues: []blackbox.HTTPQueue{{Pool: "api", CurrentQueueSize: f64(3), ArrivalRate: f64(20)}}},
		IIS:         &blackbox.IISGroup{Workers: []blackbox.IISWorker{{Pool: "api", PID: 9, ActiveRequests: f64(4), PingLatencyMs: f64(12)}}},
		DiskCPU:     &blackbox.DiskCPUGroup{Disks: []blackbox.DiskSplit{{Name: "C:", ReadMs: f64(2), WriteMs: f64(3)}}, CPU: &blackbox.CPUSplit{QueueLength: f64(1), DPCPct: f64(0.5)}},
		SQLHost:     &blackbox.SQLHostGroup{Instances: []blackbox.SQLHostInstance{{Instance: "SQLEXPRESS", BatchRequestsPerSec: f64(100), PageLifeExpectancy: f64(900)}}},
		CLR:         &blackbox.CLRGroup{Targets: []blackbox.CLRTarget{{Target: "w3wp", Gen0Bytes: u64(1000), PctTimeInGC: f64(2)}}},
		Proc10:      &blackbox.Proc10Group{Targets: []blackbox.Proc10Target{{Target: "w3wp", CPUPct: f64(3), WorkingSet: u64(5000)}}},
		ProcIO:      &blackbox.ProcIOGroup{Targets: []blackbox.ProcIOTarget{{Target: "w3wp", IOReadBytesPerSec: f64(10)}}},
		Events:      &blackbox.EventsGroup{Events: []blackbox.EventEntry{{Channel: 1, EventID: 41, Level: 1, Count: 2, T: t}}},
		TCP:         &blackbox.TCPGroup{RetransmitsPerSec: f64(1), States: []blackbox.TCPStateCount{{State: 5, Count: 12}, {State: 11, Count: 3}}},
		ASPNET:      &blackbox.ASPNETGroup{RequestsQueued: f64(0), RequestWaitTimeMs: f64(8)},
		MemPressure: &blackbox.MemPressureGroup{HardFaultsPerSec: f64(2), PoolPagedBytes: u64(4096)},
		VM:          &blackbox.VMGroup{StealPct: f64(0.1)},
		Dumps:       &blackbox.DumpsGroup{Dumps: []blackbox.DumpEntry{{Name: "w3wp.dmp", Size: 10, MTime: t}}},
		Missing:     []string{"clr: slow"},
	}
}

func TestSeriesChartsEveryGroupAndNeverInventsValues(t *testing.T) {
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	var samples []blackbox.Sample
	for i := 0; i < 20; i++ {
		samples = append(samples, fullSample(start.Add(time.Duration(i)*30*time.Second), i))
	}
	win := bbWindow{Start: start, End: start.Add(15 * time.Minute)}
	ans := buildBBSeries(blackbox.Manifest{IntervalSeconds: 30}, samples, blackbox.ReadReport{}, blackbox.StateInfo{}, win, 60)
	have := map[string]bool{}
	for _, l := range ans.Lines {
		have[l.Key] = true
		if len(l.Points) == 0 {
			t.Fatalf("%s has no points", l.Key)
		}
	}
	for _, k := range []string{
		"cpu", "memAvail", "memCommit", "diskQueue", "diskMs", "diskFreePct", "netInBps", "netOutBps",
		"http.api.currentQueueSize", "iis.api.pingLatencyMs", "diskCpu.C:.readMs", "diskCpu.cpu.dpcPct",
		"sqlHost.SQLEXPRESS.batchRequestsPerSec", "clr.w3wp.gen0Bytes", "proc10.w3wp.workingSet", "procIO.w3wp.ioReadBytesPerSec",
		"tcp.retransmitsPerSec", "tcp.states.established", "tcp.states.time-wait", "aspnet.requestWaitTimeMs",
		"memPressure.hardFaultsPerSec", "vm.stealPct", "events.level1",
	} {
		if !have[k] {
			t.Errorf("no series %q; have %v", k, keysOf(ans.Lines))
		}
	}
	for k := range have {
		if strings.HasPrefix(k, "netIn.") || k == "netIn" || k == "seq" || strings.HasSuffix(k, ".pid") {
			t.Errorf("identity or cumulative counter charted as a series: %s", k)
		}
	}
	if len(ans.Groups) != len(bbSeriesGroups) {
		t.Fatalf("groups = %d", len(ans.Groups))
	}
	for _, g := range ans.Groups {
		if g.Group == "clr" && g.Slow != 20 {
			t.Errorf("clr slow = %d", g.Slow)
		}
		if g.Read != 20 {
			t.Errorf("%s read = %d", g.Group, g.Read)
		}
	}
	if len(ans.Processes) != 2 || ans.Processes[0].Name != "sqlservr" || len(ans.Processes[0].WS) == 0 {
		t.Fatalf("processes = %+v", ans.Processes)
	}
	if len(ans.Dumps) != 1 || len(ans.EventIDs) != 1 || ans.EventIDs[0].Count != 40 {
		t.Fatalf("dumps %+v events %+v", ans.Dumps, ans.EventIDs)
	}
	// 1000 bytes more every 30 s is a rate of 33.3 bytes per second.
	for _, l := range ans.Lines {
		if l.Key == "netInBps" && (l.Points[0][1] < 33 || l.Points[0][1] > 34) {
			t.Fatalf("netIn rate = %v", l.Points[0])
		}
	}
}

func TestSeriesLeavesAHoleWhereNothingWasRead(t *testing.T) {
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	a, b := fullSample(start, 0), fullSample(start.Add(10*time.Minute), 1)
	a.Run, b.Run = "r1", "r2"
	b.CPU = nil
	win := bbWindow{Start: start, End: start.Add(15 * time.Minute)}
	ans := buildBBSeries(blackbox.Manifest{IntervalSeconds: 30}, []blackbox.Sample{a, b}, blackbox.ReadReport{}, blackbox.StateInfo{}, win, 15)
	for _, l := range ans.Lines {
		if l.Key == "cpu" && len(l.Points) != 1 {
			t.Fatalf("a missing reading was drawn as a value: %+v", l.Points)
		}
		if l.Key == "netInBps" {
			t.Fatalf("a rate was made across a restart: %+v", l.Points)
		}
	}
	if len(ans.Gaps) != 1 || ans.Gaps[0].Seconds != 600 {
		t.Fatalf("gaps = %+v", ans.Gaps)
	}
}

func keysOf(ls []bbSeriesLine) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Key)
	}
	return out
}

func TestBBSeriesUnitMsNeedsCapitalMs(t *testing.T) {
	for key, want := range map[string]string{"host.diskMs": "ms", "sqlHost.lockWaitTimeMs": "ms", "iis.items": "count", "x.queueItems": "count"} {
		if got := bbSeriesUnit(key); got != want {
			t.Errorf("bbSeriesUnit(%q) = %q, want %q", key, got, want)
		}
	}
}

// The charts view's log scale hides a 0 and says how many it hid, so the server must keep a real 0 as a point
// (a 0 is a reading) and leave out only what was not read.
func TestSeriesKeepsARealZeroAndLeavesOutWhatWasNotRead(t *testing.T) {
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	samples := []blackbox.Sample{
		{V: 1, Run: "r1", Seq: 1, T: start, CPU: f64(0), DiskQueue: f64(0)},
		{V: 1, Run: "r1", Seq: 2, T: start.Add(30 * time.Second), CPU: f64(5)},
	}
	win := bbWindow{Start: start, End: start.Add(time.Minute)}
	ans := buildBBSeries(blackbox.Manifest{IntervalSeconds: 30}, samples, blackbox.ReadReport{}, blackbox.StateInfo{}, win, 2)
	byKey := map[string]bbSeriesLine{}
	for _, l := range ans.Lines {
		byKey[l.Key] = l
	}
	if cpu := byKey["cpu"]; len(cpu.Points) != 2 || cpu.Points[0][1] != 0 {
		t.Fatalf("cpu points = %v, want a 0 then a 5", cpu.Points)
	}
	if dq := byKey["diskQueue"]; len(dq.Points) != 1 || dq.Points[0][1] != 0 {
		t.Fatalf("diskQueue points = %v, want one 0 and no point where it was not read", dq.Points)
	}
}

// BL-146: a count of what a sum covered is not drawn as a line, and the fields whose name hides their unit get the right one.
// A sample whose group covered only part of what it sums is counted as partial for that group.
func TestSeriesUnitsSkipsAndPartialCover(t *testing.T) {
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	s := fullSample(start, 0)
	s.TCP.OutboundQueue = f64(2)
	s.TCP.OutboundQueueInterfaces = 3
	s.TCP.DiscardedPerSec, s.TCP.DiscardedInterfaces = f64(1), 3
	s.TCP.OutErrorsPerSec, s.TCP.OutErrorsInterfaces = f64(0), 3
	s.TCP.OutboundQueuePartial = true
	s.SQLHost.Instances[0].PageLifeExpectancy = f64(900)
	s2 := fullSample(start.Add(30*time.Second), 1)
	win := bbWindow{Start: start, End: start.Add(time.Minute)}
	ans := buildBBSeries(blackbox.Manifest{IntervalSeconds: 30}, []blackbox.Sample{s, s2}, blackbox.ReadReport{}, blackbox.StateInfo{}, win, 2)
	units := map[string]string{}
	for _, l := range ans.Lines {
		units[l.Key] = l.Unit
	}
	for _, k := range []string{"tcp.outboundQueueInterfaces", "tcp.discardedInterfaces", "tcp.outErrorsInterfaces"} {
		if _, ok := units[k]; ok {
			t.Fatalf("%s, an adapter count, is charted as a measurement", k)
		}
	}
	for k, want := range map[string]string{
		"clr.w3wp.pctTimeInGC": "pct", "sqlHost.SQLEXPRESS.pageLifeExpectancy": "sec", "tcp.outboundQueue": "count", "cpu": "pct",
	} {
		if units[k] != want {
			t.Errorf("%s unit = %q, want %q", k, units[k], want)
		}
	}
	for _, c := range ans.Groups {
		if c.Group == "tcp" && (c.Read != 2 || c.Partial != 1) {
			t.Fatalf("tcp cover = %+v, want 2 read and 1 partial", c)
		}
		if c.Group == "sqlHost" && c.Partial != 0 {
			t.Fatalf("sqlHost cover = %+v, want none partial", c)
		}
	}
	if bbSeriesUnit("http.api.maxQueueItemAge") != "ms" || bbSeriesUnit("proc10.w3wp.elapsedSec") != "sec" {
		t.Fatal("maxQueueItemAge or elapsedSec unit is wrong")
	}
}
