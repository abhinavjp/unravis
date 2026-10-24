package resource_test

import (
	"context"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/resource"
)

// t0 is a synthetic sample time (2001, as every fixture).
var t0 = time.Date(2001, 10, 25, 1, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

// harness holds one in-memory case and the events waiting to be imported, one
// file per source.
type harness struct {
	t       *testing.T
	ctx     context.Context
	c       casestore.Case
	id      string
	files   int
	pending map[string][]evidence.Event
	order   []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	store := casestore.NewMemory()
	info, err := store.Create(ctx, "resource")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &harness{t: t, ctx: ctx, c: c, id: string(info.ID), pending: map[string][]evidence.Event{}}
}

func (h *harness) add(evs ...evidence.Event) {
	for _, e := range evs {
		if _, ok := h.pending[e.Source]; !ok {
			h.order = append(h.order, e.Source)
		}
		h.pending[e.Source] = append(h.pending[e.Source], e)
	}
}

// commit imports the pending events, one file per source.
func (h *harness) commit() {
	h.t.Helper()
	for _, src := range h.order {
		evs := h.pending[src]
		h.files++
		nonce := "n" + strconv.Itoa(h.files)
		for i := range evs {
			evs[i].EvidenceID = evidence.EventID(h.id, src, nonce, int64(i+1), 0)
			evs[i].Line = int64(i + 1)
		}
		run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			rep := importer.Report{Importer: src}
			if _, err := io.Copy(io.Discard, r); err != nil {
				return rep, err
			}
			for _, e := range evs {
				if err := emit(e); err != nil {
					return rep, err
				}
				rep.Emitted++
			}
			return rep, nil
		}
		_, err := h.c.Import(h.ctx, casestore.ImportMeta{Importer: src, FileName: "f.txt", AckGating: true, AckText: "ack"},
			strings.NewReader("file "+nonce), run)
		if err != nil {
			h.t.Fatalf("import %s: %v", src, err)
		}
	}
	h.pending, h.order = map[string][]evidence.Event{}, nil
}

func (h *harness) catalogue(w importer.Window) resource.Index {
	h.t.Helper()
	h.commit()
	x, err := resource.Catalogue(h.c, w)
	if err != nil {
		h.t.Fatal(err)
	}
	return x
}

func fp(v float64) *float64 { return &v }

// pdh is a win-counters sample. inst may be "".
func pdh(host string, t time.Time, object, inst, counter string, v float64, unit string) evidence.Event {
	a := map[string]string{"object": object, "counter": counter}
	if inst != "" {
		a["instance"] = inst
	}
	return evidence.Event{Source: "win-counters", Kind: "counter", Host: host, Start: t, End: t, Value: fp(v), Unit: unit,
		TZRule: clock.RuleHeaderBias, Attrs: a, Line: 1}
}

func metric(src, host string, t time.Time, v float64, unit string, kv ...string) evidence.Event {
	return evidence.Event{Source: src, Kind: "metric", Host: host, Start: t, End: t, Value: fp(v), Unit: unit,
		TZRule: clock.RuleUTCField, Attrs: attrs(kv...), Line: 1}
}

func point(src, kind, host string, t time.Time, kv ...string) evidence.Event {
	return evidence.Event{Source: src, Kind: kind, Host: host, Start: t, End: t, TZRule: clock.RuleUTCField, Attrs: attrs(kv...), Line: 1}
}

func attrs(kv ...string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func values(s resource.Series) []float64 {
	out := make([]float64, len(s.Samples))
	for i, sm := range s.Samples {
		out[i] = sm.Value
	}
	return out
}

func keys(ss []resource.Series) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Proc.Key())
	}
	return out
}

func eqF(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLookupMapsTheMergedImporterEvents(t *testing.T) {
	tm := t0
	cases := []struct {
		name string
		ev   evidence.Event
		want resource.Metric
		ok   bool
	}{
		{"host cpu", pdh("web-a", tm, "Processor", "_Total", "% Processor Time", 50, "percent-host"),
			resource.Metric{ID: resource.HostCPU, Unit: "percent-host", Scale: 1}, true},
		{"processor information", pdh("web-a", tm, "Processor Information", "_Total", "% Processor Time", 50, "percent-host"),
			resource.Metric{ID: resource.HostCPU, Unit: "percent-host", Scale: 1}, true},
		{"one core is not host cpu", pdh("web-a", tm, "Processor", "0", "% Processor Time", 50, "percent-one-core"), resource.Metric{}, false},
		{"available MBytes", pdh("web-a", tm, "Memory", "", "Available MBytes", 512, ""),
			resource.Metric{ID: resource.HostMemAvailable, Unit: "bytes", Scale: 1 << 20}, true},
		{"committed", pdh("web-a", tm, "Memory", "", "Committed Bytes", 1, ""), resource.Metric{ID: resource.HostMemCommitted, Unit: "bytes", Scale: 1}, true},
		{"pages", pdh("web-a", tm, "Memory", "", "Pages/sec", 1, ""), resource.Metric{ID: resource.HostPages, Unit: "per-sec", Scale: 1}, true},
		{"disk sec", pdh("web-a", tm, "PhysicalDisk", "0 C:", "Avg. Disk sec/Transfer", 0.03, ""),
			resource.Metric{ID: resource.HostDiskSecPerTx, Unit: "ms", Scale: 1000}, true},
		{"disk queue", pdh("web-a", tm, "LogicalDisk", "C:", "Current Disk Queue Length", 2, ""), resource.Metric{ID: resource.HostDiskQueue, Unit: "count", Scale: 1}, true},
		{"net", pdh("web-a", tm, "Network Interface", "Adapter 1", "Bytes Total/sec", 9, ""), resource.Metric{ID: resource.HostNetBytes, Unit: "per-sec", Scale: 1}, true},
		{"proc cpu v2", pdh("web-a", tm, "Process V2", "w3wp:1240", "% Processor Time", 9, "percent-host"), resource.Metric{ID: resource.ProcCPU, Unit: "percent-host", Scale: 1}, true},
		{"proc private", pdh("web-a", tm, "Process", "w3wp#1", "Private Bytes", 9, ""), resource.Metric{ID: resource.ProcPrivate, Unit: "bytes", Scale: 1}, true},
		{"proc ws", pdh("web-a", tm, "Process V2", "w3wp:1240", "Working Set", 9, ""), resource.Metric{ID: resource.ProcWS, Unit: "bytes", Scale: 1}, true},
		{"proc handles", pdh("web-a", tm, "Process V2", "w3wp:1240", "Handle Count", 9, ""), resource.Metric{ID: resource.ProcHandles, Unit: "count", Scale: 1}, true},
		{"proc threads", pdh("web-a", tm, "Process V2", "w3wp:1240", "Thread Count", 9, ""), resource.Metric{ID: resource.ProcThreads, Unit: "count", Scale: 1}, true},
		{"gen2 pdh", pdh("web-a", tm, ".NET CLR Memory", "w3wp", "# Gen 2 Collections", 9, ""),
			resource.Metric{ID: resource.CLRGen2, Unit: "count", Scale: 1, Cumulative: true}, true},
		{"gc time pdh", pdh("web-a", tm, ".NET CLR Memory", "w3wp", "% Time in GC", 9, ""), resource.Metric{ID: resource.CLRGCTime, Unit: "percent", Scale: 1}, true},
		{"aspnet queued", pdh("web-a", tm, "ASP.NET", "", "Requests Queued", 9, ""), resource.Metric{ID: resource.ASPNetQueued, Unit: "count", Scale: 1}, true},
		{"aspnet versioned", pdh("web-a", tm, "ASP.NET v4.0.30319", "", "Requests Current", 9, ""), resource.Metric{ID: resource.ASPNetCurrent, Unit: "count", Scale: 1}, true},
		{"aspnet wait", pdh("web-a", tm, "ASP.NET", "", "Request Wait Time", 9, ""), resource.Metric{ID: resource.ASPNetWaitMs, Unit: "ms", Scale: 1}, true},
		{"aspnet apps is not aspnet", pdh("web-a", tm, "ASP.NET Applications", "", "Requests Queued", 9, ""), resource.Metric{}, false},
		{"w3wp active", pdh("web-a", tm, "W3SVC_W3WP", "pid-1240", "Active Requests", 9, ""), resource.Metric{ID: resource.IISWPActive, Unit: "count", Scale: 1}, true},
		{"procsnap cpu", metric("win-procsnap", "web-a", tm, 9, "percent-host", "metric", "cpu", "process", "w3wp", "pid", "1240"),
			resource.Metric{ID: resource.ProcCPU, Unit: "percent-host", Scale: 1}, true},
		{"procsnap private", metric("win-procsnap", "web-a", tm, 9, "bytes", "metric", "private-bytes", "process", "w3wp", "pid", "1240"),
			resource.Metric{ID: resource.ProcPrivate, Unit: "bytes", Scale: 1}, true},
		{"dotnet tp queue", metric("dotnet-counters", "web-a", tm, 9, "count", "provider", "System.Runtime", "counter", "ThreadPool Queue Length"),
			resource.Metric{ID: resource.NetTPQueue, Unit: "count", Scale: 1}, true},
		{"dotnet tp queue meter", metric("dotnet-counters", "web-a", tm, 9, "count", "provider", "System.Runtime", "counter", "dotnet.thread_pool.queue.length"),
			resource.Metric{ID: resource.NetTPQueue, Unit: "count", Scale: 1}, true},
		{"dotnet tp threads", metric("dotnet-counters", "web-a", tm, 9, "count", "counter", "ThreadPool Thread Count"),
			resource.Metric{ID: resource.NetTPThreads, Unit: "count", Scale: 1}, true},
		{"dotnet gen2 rate", metric("dotnet-counters", "web-a", tm, 1, "per-sec", "counter", "Gen 2 GC Count"),
			resource.Metric{ID: resource.CLRGen2, Unit: "per-sec", Scale: 1}, true},
		{"dotnet gen2 meter", metric("dotnet-counters", "web-a", tm, 1, "per-sec", "counter", "dotnet.gc.collections", "generation", "gen2"),
			resource.Metric{ID: resource.CLRGen2, Unit: "per-sec", Scale: 1}, true},
		{"dotnet gen0 meter is not gen2", metric("dotnet-counters", "web-a", tm, 1, "per-sec", "counter", "dotnet.gc.collections", "generation", "gen0"),
			resource.Metric{}, false},
		{"dotnet gc time", metric("dotnet-counters", "web-a", tm, 1, "percent", "counter", "% Time in GC since last GC"),
			resource.Metric{ID: resource.CLRGCTime, Unit: "percent", Scale: 1}, true},
		{"dotnet pause", metric("dotnet-counters", "web-a", tm, 1, "ms", "counter", "dotnet.gc.pause.time"),
			resource.Metric{ID: resource.NetGCPause, Unit: "ms", Scale: 1}, true},
		{"kestrel", metric("dotnet-counters", "web-a", tm, 1, "count", "counter", "kestrel.queued_requests"),
			resource.Metric{ID: resource.KestrelQueued, Unit: "count", Scale: 1}, true},
		{"core current requests", metric("dotnet-counters", "web-a", tm, 1, "count", "counter", "Current Requests"),
			resource.Metric{ID: resource.ASPNetCurrent, Unit: "count", Scale: 1}, true},
		{"dotnet working set", metric("dotnet-counters", "web-a", tm, 1, "bytes", "counter", "Working Set"),
			resource.Metric{ID: resource.ProcWS, Unit: "bytes", Scale: 1}, true},
		{"mongo queue", metric("mongo-serverstatus", "mongo-a", tm, 3, "count", "metric", "globalLock.currentQueue.total"),
			resource.Metric{ID: resource.MongoQueue, Unit: "count", Scale: 1}, true},
		{"mongo conns", metric("mongo-serverstatus", "mongo-a", tm, 3, "count", "metric", "connections.current"),
			resource.Metric{ID: resource.MongoConns, Unit: "count", Scale: 1}, true},
		{"mongo dirty", metric("mongo-serverstatus", "mongo-a", tm, 3, "bytes", "metric", "wiredTiger.cache.tracked dirty bytes in the cache"),
			resource.Metric{ID: resource.MongoWTDirty, Unit: "bytes", Scale: 1}, true},
		{"mongostat qw", metric("mongostat-json", "mongo-a", tm, 3, "count", "metric", "qw"), resource.Metric{ID: resource.MongoQueue, Unit: "count", Scale: 1}, true},
		{"mongostat dirty", metric("mongostat-json", "mongo-a", tm, 3, "percent", "metric", "dirty"), resource.Metric{ID: resource.MongoWTDirty, Unit: "percent", Scale: 1}, true},
		{"opcounters are not mapped", metric("mongo-serverstatus", "mongo-a", tm, 3, "count", "metric", "opcounters.query"), resource.Metric{}, false},
		{"redis mem (pending)", metric("redis-info", "redis-a", tm, 3, "bytes", "metric", "used_memory", "role", "master"),
			resource.Metric{ID: resource.RedisMem, Unit: "bytes", Scale: 1}, true},
		{"redis blocked (pending)", metric("redis-info", "redis-a", tm, 3, "count", "metric", "blocked_clients"),
			resource.Metric{ID: resource.RedisBlocked, Unit: "count", Scale: 1}, true},
		{"another source's metric attr", metric("win-procsnap", "web-a", tm, 3, "count", "metric", "blocked_clients"), resource.Metric{}, false},
		{"sql counter", pdh("sql-a", tm, "SQLServer:Wait Statistics", "Average wait time (ms)", "Lock waits", 3, ""), resource.Metric{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resource.Lookup(tc.ev)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("Lookup = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestMetricsListEveryPlanIDAndThePendingSources(t *testing.T) {
	want := []string{"aspnet.current", "aspnet.queued", "aspnet.wait_ms", "clr.gc_time", "clr.gen2",
		"host.cpu", "host.disk.queue", "host.disk.sec_per_transfer", "host.mem.available", "host.mem.committed",
		"host.net.bytes", "host.pages", "iis.wp.active", "kestrel.queued", "mongo.conns", "mongo.queue",
		"mongo.wt.dirty", "net.gc_pause", "net.tp.queue", "net.tp.threads", "proc.cpu", "proc.handles",
		"proc.private", "proc.threads", "proc.ws", "redis.blocked", "redis.clients", "redis.mem", "redis.ops"}
	var got []string
	pending := map[string]string{}
	for _, mi := range resource.Metrics() {
		got = append(got, mi.ID)
		if len(mi.Sources) == 0 {
			t.Errorf("%s has no source", mi.ID)
		}
		pending[mi.ID] = strings.Join(mi.Pending, ",")
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("ids\n got %v\nwant %v", got, want)
	}
	for id, p := range map[string]string{
		"redis.mem": "", "redis.ops": "", "host.cpu": "linux-sadf",
		"proc.cpu": "linux-pidstat", "proc.ws": "linux-pidstat", "mongo.queue": "", "clr.gen2": "",
	} {
		if pending[id] != p {
			t.Errorf("%s pending %q, want %q", id, pending[id], p)
		}
	}
}

func TestHostSeriesScaleSpansAndCadence(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ {
		h.add(pdh("web-a", at(time.Duration(i)*15*time.Second), "Memory", "", "Available MBytes", float64(500+i), ""))
	}
	x := h.catalogue(importer.Window{})
	ss := x.Series(resource.HostMemAvailable, resource.Filter{Host: "web-a"})
	if len(ss) != 1 {
		t.Fatalf("series %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Interval != 15*time.Second || s.Label != `Memory\Available MBytes` || !s.Proc.IsZero() || s.Gap != "" {
		t.Fatalf("series %+v", s)
	}
	if s.Samples[0].Value != 500*(1<<20) {
		t.Errorf("value %v, want 500 MiB in bytes", s.Samples[0].Value)
	}
	for i, sm := range s.Samples {
		end := at(time.Duration(i) * 15 * time.Second)
		if !sm.End.Equal(end) || !sm.Start.Equal(end.Add(-15*time.Second)) || sm.EvidenceID == "" || sm.HasDelta {
			t.Errorf("sample %d %+v", i, sm)
		}
	}
	if c := x.Coverage(s); c != 1 {
		t.Errorf("coverage %v, want 1", c)
	}
	if got := x.Series(resource.HostMemAvailable, resource.Filter{Host: "web-b"}); len(got) != 0 {
		t.Errorf("filter on another host gave %d series", len(got))
	}
}

func TestLegacyInstanceIsRekeyedPerSampleByItsPID(t *testing.T) {
	h := newHarness(t)
	// w3wp#1 is pid 1240 for three samples, then pid 2210 (the pool recycled and
	// the instance number moved), and one sample has no ID Process.
	pids := []int{1240, 1240, 1240, 2210, 2210, 0}
	priv := []float64{100, 110, 120, 50, 60, 70}
	gen2 := []float64{10, 12, 12, 1, 3, 4}
	for i := range pids {
		tm := at(time.Duration(i) * 15 * time.Second)
		h.add(pdh("web-a", tm, "Process", "w3wp#1", "Private Bytes", priv[i], ""))
		h.add(pdh("web-a", tm, ".NET CLR Memory", "w3wp#1", "# Gen 2 Collections", gen2[i], ""))
		if pids[i] > 0 {
			h.add(pdh("web-a", tm, "Process", "w3wp#1", "ID Process", float64(pids[i]), ""))
			h.add(pdh("web-a", tm, ".NET CLR Memory", "w3wp#1", "Process ID", float64(pids[i]), ""))
		}
	}
	x := h.catalogue(importer.Window{})

	ss := x.Series(resource.ProcPrivate, resource.Filter{Process: "w3wp"})
	if got := strings.Join(keys(ss), " "); got != "w3wp#1 w3wp:1240 w3wp:2210" {
		t.Fatalf("private series %q", got)
	}
	if !eqF(values(ss[1]), []float64{100, 110, 120}) || !eqF(values(ss[2]), []float64{50, 60}) || !eqF(values(ss[0]), []float64{70}) {
		t.Errorf("values %v %v %v", values(ss[0]), values(ss[1]), values(ss[2]))
	}
	if ss[0].Gap != resource.GapLegacyInstance || ss[1].Gap != "" || ss[1].Label != `Process\Private Bytes` {
		t.Errorf("gaps %q %q label %q", ss[0].Gap, ss[1].Gap, ss[1].Label)
	}
	if ss[0].Interval != 15*time.Second {
		t.Errorf("a one-sample series takes the cadence of its source on the host, got %v", ss[0].Interval)
	}
	if !contains(x.Gaps(), resource.GapLegacyInstance) {
		t.Errorf("index gaps %v", x.Gaps())
	}

	// Gen 2 deltas stay within one pid: the move to pid 2210 is a new series,
	// never a negative delta.
	g := x.Series(resource.CLRGen2, resource.Filter{PID: 1240})
	if len(g) != 1 {
		t.Fatalf("gen2 1240 series %d", len(g))
	}
	assertDeltas(t, g[0], []string{"-", "2", "0"})
	g = x.Series(resource.CLRGen2, resource.Filter{PID: 2210})
	assertDeltas(t, g[0], []string{"-", "2"})

	// pid 2210 is a new pid for w3wp on web-a.
	var np []resource.Marker
	for _, m := range x.Markers() {
		if m.Kind == resource.KindNewPID {
			np = append(np, m)
		}
	}
	if len(np) != 1 || np[0].Proc.Key() != "w3wp:2210" || np[0].Edge != resource.EdgeStart ||
		!np[0].End.Equal(at(45*time.Second)) || !np[0].Start.Equal(at(30*time.Second)) ||
		np[0].Text != "New process id for w3wp (w3wp:2210 first seen) on web-a" {
		t.Fatalf("new-pid markers %+v", np)
	}
}

// assertDeltas compares the deltas of s with want ("-" no delta, "reset").
func assertDeltas(t *testing.T, s resource.Series, want []string) {
	t.Helper()
	var got []string
	for _, sm := range s.Samples {
		switch {
		case sm.Reset:
			got = append(got, "reset")
		case sm.HasDelta:
			got = append(got, strconv.FormatFloat(sm.Delta, 'g', -1, 64))
		default:
			got = append(got, "-")
		}
		if sm.HasDelta && sm.Delta < 0 {
			t.Errorf("negative delta %v", sm.Delta)
		}
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("deltas %v, want %v", got, want)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestCumulativeCounterResetIsNotANegativeDelta(t *testing.T) {
	cases := []struct {
		name string
		vals []float64
		want []string
	}{
		{"steady", []float64{10, 15, 15, 16}, []string{"-", "5", "0", "1"}},
		{"reset", []float64{10, 15, 3, 4}, []string{"-", "5", "reset", "1"}},
		{"reset to zero", []float64{7, 0, 0}, []string{"-", "reset", "0"}},
		{"one sample", []float64{7}, []string{"-"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			for i, v := range tc.vals {
				h.add(pdh("web-a", at(time.Duration(i)*time.Minute), ".NET CLR Memory", "dotnet:3000", "# Gen 2 Collections", v, ""))
				h.add(pdh("web-a", at(time.Duration(i)*time.Minute), ".NET CLR Memory", "dotnet:3000", "Process ID", 3000, ""))
			}
			x := h.catalogue(importer.Window{})
			ss := x.Series(resource.CLRGen2, resource.Filter{})
			if len(ss) != 1 || !ss[0].Metric.Cumulative || ss[0].Proc.PID != 3000 {
				t.Fatalf("series %+v", ss)
			}
			assertDeltas(t, ss[0], tc.want)
		})
	}
}

func TestW3SVCInstanceJoinsTheProcessV2PID(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		tm := at(time.Duration(i) * 15 * time.Second)
		h.add(pdh("web-a", tm, "Process V2", "w3wp:1240", "Working Set", 1e9, ""))
		h.add(pdh("web-a", tm, "Process V2", "_Total:0", "Working Set", 9e9, ""))
		h.add(pdh("web-a", tm, "Process V2", "Idle:0", "% Processor Time", 90, "percent-host"))
		h.add(pdh("web-a", tm, "W3SVC_W3WP", "pid-1240", "Active Requests", float64(i), ""))
		h.add(pdh("web-a", tm, "W3SVC_W3WP", "_Total", "Active Requests", float64(i), ""))
	}
	x := h.catalogue(importer.Window{})
	ws := x.Series(resource.ProcWS, resource.Filter{})
	if got := strings.Join(keys(ws), " "); got != "w3wp:1240" {
		t.Fatalf("working set series %q (aggregate instances name no process)", got)
	}
	if cpu := x.Series(resource.ProcCPU, resource.Filter{}); len(cpu) != 0 {
		t.Errorf("the idle instance gave %d cpu series", len(cpu))
	}
	act := x.Series(resource.IISWPActive, resource.Filter{PID: 1240})
	if len(act) != 1 || act[0].Proc.Key() != ws[0].Proc.Key() {
		t.Fatalf("W3SVC series %+v", act)
	}
	all := x.Series(resource.IISWPActive, resource.Filter{})
	if len(all) != 2 || !all[0].Proc.IsZero() || all[0].Label != `W3SVC_W3WP(_Total)\Active Requests` {
		t.Fatalf("W3SVC _Total is a host series: %+v", all)
	}
}

func TestProcSnapSplitsAReusedPIDAtItsStart(t *testing.T) {
	h := newHarness(t)
	w := importer.Window{From: t0, To: at(10 * time.Minute)}
	firstStart := t0.Add(-time.Hour) // before the window
	secondStart := at(150 * time.Second)
	h.add(point("win-procsnap", "process-start", "web-a", firstStart, "process", "w3wp", "pid", "1240"))
	h.add(point("win-procsnap", "process-start", "web-a", secondStart, "process", "w3wp", "pid", "1240"))
	gone := point("win-procsnap", "process-gone", "web-a", at(60*time.Second), "process", "w3wp", "pid", "1240")
	gone.End = at(120 * time.Second)
	h.add(gone)
	for i := 0; i < 6; i++ {
		h.add(metric("win-procsnap", "web-a", at(time.Duration(i)*time.Minute), float64(100+i), "bytes",
			"metric", "private-bytes", "process", "w3wp", "pid", "1240"))
	}
	x := h.catalogue(w)
	ss := x.Series(resource.ProcPrivate, resource.Filter{PID: 1240})
	if len(ss) != 2 || !ss[0].Proc.Start.Equal(firstStart) || !ss[1].Proc.Start.Equal(secondStart) {
		t.Fatalf("series %+v", ss)
	}
	if !eqF(values(ss[0]), []float64{100, 101, 102}) || !eqF(values(ss[1]), []float64{103, 104, 105}) {
		t.Errorf("values %v %v", values(ss[0]), values(ss[1]))
	}
	var got []string
	for _, m := range x.Markers() {
		got = append(got, m.Kind+"|"+m.Edge+"|"+m.Text)
		if err := evidence.LintCausal(m.Text); err != nil {
			t.Errorf("marker text %q: %v", m.Text, err)
		}
	}
	want := []string{
		"process-gone|stop|Process no longer listed for w3wp:1240 on web-a",
		"process-start|start|Process start for w3wp:1240 on web-a",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("markers\n got %v\nwant %v", got, want)
	}
}

func TestDotnetCountersPIDTakesTheNameOfTheSamePID(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		tm := at(time.Duration(i) * 10 * time.Second)
		h.add(pdh("web-a", tm, "Process V2", "dotnet:3000", "Private Bytes", 1e9, ""))
		h.add(metric("dotnet-counters", "web-a", tm, float64(i), "count", "provider", "System.Runtime",
			"counter", "ThreadPool Queue Length", "pid", "3000"))
		h.add(metric("dotnet-counters", "web-a", tm, 0.5, "per-sec", "provider", "System.Runtime",
			"counter", "Gen 2 GC Count", "pid", "3000"))
		h.add(metric("dotnet-counters", "web-a", tm, 1, "count", "provider", "System.Runtime",
			"counter", "ThreadPool Queue Length", "pid", "4000"))
	}
	x := h.catalogue(importer.Window{})
	q := x.Series(resource.NetTPQueue, resource.Filter{})
	if got := strings.Join(keys(q), " "); got != "dotnet:3000 pid-4000" {
		t.Fatalf("queue series %q", got)
	}
	g := x.Series(resource.CLRGen2, resource.Filter{Process: "dotnet"})
	if len(g) != 1 || g[0].Metric.Cumulative || g[0].Metric.Unit != "per-sec" {
		t.Fatalf("a dotnet-counters gen2 rate is not cumulative: %+v", g)
	}
	assertDeltas(t, g[0], []string{"-", "-", "-"})
}

func TestMarkersFromEventsAndUptimeDrops(t *testing.T) {
	h := newHarness(t)
	w := importer.Window{From: t0, To: at(time.Hour)}
	h.add(point("win-events", "apppool-recycle", "web-a", at(time.Minute), "provider", "Microsoft-Windows-WAS", "id", "5074", "reason", "time-interval"))
	h.add(point("win-events", "worker-process-failure", "web-a", at(2*time.Minute), "provider", "Microsoft-Windows-WAS", "id", "5011"))
	h.add(point("win-events", "host-unexpected-shutdown", "web-b", at(3*time.Minute), "id", "6008"))
	h.add(point("win-events", "win-event", "web-a", at(3*time.Minute), "id", "1"))
	h.add(point("log4net-text", "app-start", "web-a", at(4*time.Minute)))
	h.add(point("ancm-stdout", "app-session", "web-a", at(5*time.Minute)))
	h.add(point("log4net-text", "app-stop", "web-a", at(6*time.Minute)))
	h.add(point("redis-log", "redis-start", "redis-a", at(7*time.Minute)))
	h.add(point("mongod-json", "mongod-start", "mongo-a", at(8*time.Minute)))
	h.add(point("win-events", "apppool-recycle", "web-a", at(2*time.Hour))) // outside the window

	// serverStatus uptime: a drop with a mongod-start in it (recorded already),
	// then a drop with none (derived).
	for i, up := range []float64{100, 160, 40, 100, 160, 5} {
		h.add(metric("mongo-serverstatus", "mongo-a", at(time.Duration(i)*time.Minute+30*time.Second), up, "count", "metric", "uptime"))
	}
	h.add(point("mongo-serverstatus", "mongod-start", "mongo-a", at(90*time.Second)))
	// Elapsed Time of one process drops: a restart under the same pid.
	for i, el := range []float64{600, 615, 3, 18} {
		h.add(pdh("web-a", at(time.Duration(i)*15*time.Second+10*time.Minute), "Process V2", "w3wp:1240", "Elapsed Time", el, ""))
	}
	x := h.catalogue(w)
	var got []string
	for _, m := range x.Markers() {
		got = append(got, m.Kind+"|"+m.Edge+"|"+m.Host+"|"+m.Text)
		if len(m.EvidenceIDs) == 0 {
			t.Errorf("marker %s has no evidence id", m.Kind)
		}
		if err := evidence.LintCausal(m.Text); err != nil {
			t.Errorf("marker text %q: %v", m.Text, err)
		}
	}
	want := []string{
		"apppool-recycle|restart|web-a|Application pool recycle event (reason: time-interval) on web-a",
		"mongod-start|start|mongo-a|mongod start on mongo-a",
		"worker-process-failure|stop|web-a|Worker process failure event (event id 5011) on web-a",
		"host-unexpected-shutdown|restart|web-b|Unexpected shutdown event on web-b",
		"app-start|start|web-a|Application start entry on web-a",
		"uptime-reset|restart|mongo-a|uptime dropped on mongo-a", // spans 4:30..5:30
		"app-session|start|web-a|Application session entry on web-a",
		"app-stop|stop|web-a|Application stop entry on web-a",
		"redis-start|start|redis-a|Redis start on redis-a",
		"mongod-start|start|mongo-a|mongod start on mongo-a",
		`uptime-reset|restart|web-a|Process V2\Elapsed Time dropped for w3wp:1240 on web-a`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("markers\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(x.Series(resource.ProcCPU, resource.Filter{})) != 0 {
		t.Error("Elapsed Time is not a listed series")
	}
}

func TestSpansStayUTCAcrossALocalDSTChange(t *testing.T) {
	// US Eastern left daylight time at 2001-10-28 06:00 UTC: the local clock read
	// 01:00 to 02:00 twice. Header-bias samples are UTC, so every span is one
	// cadence long and the times only go forward.
	h := newHarness(t)
	start := time.Date(2001, 10, 28, 5, 50, 0, 0, time.UTC)
	for i := 0; i < 21; i++ {
		h.add(pdh("web-a", start.Add(time.Duration(i)*time.Minute), "Processor", "_Total", "% Processor Time", 40, "percent-host"))
	}
	w := importer.Window{From: start, To: start.Add(20 * time.Minute)}
	x := h.catalogue(w)
	ss := x.Series(resource.HostCPU, resource.Filter{})
	if len(ss) != 1 || len(ss[0].Samples) != 21 {
		t.Fatalf("series %+v", ss)
	}
	var prev time.Time
	for i, sm := range ss[0].Samples {
		if sm.End.Sub(sm.Start) != time.Minute || !sm.End.After(prev) || sm.End.Location() != time.UTC {
			t.Errorf("sample %d spans %v..%v", i, sm.Start, sm.End)
		}
		prev = sm.End
	}
	if c := x.Coverage(ss[0]); c != 1 {
		t.Errorf("coverage %v, want 1", c)
	}
}

func TestCoverageIsTheShareOfExpectedBuckets(t *testing.T) {
	cases := []struct {
		name    string
		minutes []int
		w       importer.Window
		want    float64
	}{
		{"six of ten", []int{1, 2, 3, 4, 5, 6}, importer.Window{From: t0, To: at(10 * time.Minute)}, 0.6},
		{"all", []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, importer.Window{From: t0, To: at(10 * time.Minute)}, 1},
		{"open window uses the extent", []int{0, 1, 2, 3, 4}, importer.Window{}, 1},
		{"a hole", []int{0, 1, 2, 6, 7, 8, 9}, importer.Window{From: t0, To: at(10 * time.Minute)}, 0.7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			for _, m := range tc.minutes {
				h.add(pdh("web-a", at(time.Duration(m)*time.Minute), "Memory", "", "Pages/sec", 1, ""))
			}
			x := h.catalogue(tc.w)
			ss := x.Series(resource.HostPages, resource.Filter{})
			if len(ss) != 1 {
				t.Fatalf("series %d", len(ss))
			}
			if c := x.Coverage(ss[0]); c != tc.want {
				t.Errorf("coverage %v, want %v", c, tc.want)
			}
		})
	}
}

func TestOneSampleWithNoCadenceIsAPoint(t *testing.T) {
	h := newHarness(t)
	h.add(metric("mongo-serverstatus", "mongo-a", t0, 4, "count", "metric", "globalLock.currentQueue.total"))
	x := h.catalogue(importer.Window{})
	ss := x.Series(resource.MongoQueue, resource.Filter{})
	if len(ss) != 1 || ss[0].Interval != 0 || ss[0].Gap != resource.GapNoCadence ||
		!ss[0].Samples[0].Start.Equal(ss[0].Samples[0].End) || x.Coverage(ss[0]) != 0 {
		t.Fatalf("series %+v", ss)
	}
}

func TestPendingSadfIdleBecomesHostCPU(t *testing.T) {
	h := newHarness(t)
	h.add(metric("linux-sadf", "mongo-a", t0, 40, "percent", "metric", "%idle"))
	h.add(metric("linux-sadf", "mongo-a", at(10*time.Minute), 25, "percent", "metric", "%idle"))
	x := h.catalogue(importer.Window{})
	ss := x.Series(resource.HostCPU, resource.Filter{Host: "mongo-a"})
	if len(ss) != 1 || !eqF(values(ss[0]), []float64{60, 75}) || ss[0].Metric.Unit != "percent-host" {
		t.Fatalf("series %+v", ss)
	}
}

func TestEmptyCase(t *testing.T) {
	h := newHarness(t)
	x := h.catalogue(importer.Window{From: t0, To: at(time.Hour)})
	for _, mi := range resource.Metrics() {
		if ss := x.Series(mi.ID, resource.Filter{}); len(ss) != 0 {
			t.Errorf("%s: %d series", mi.ID, len(ss))
		}
	}
	if len(x.Markers()) != 0 || len(x.Gaps()) != 0 || x.Partial() || x.Coverage(resource.Series{}) != 0 {
		t.Fatalf("empty index %+v", x)
	}
	if _, err := resource.Catalogue(nil, importer.Window{}); err == nil {
		t.Error("a nil case gave no error")
	}
}

// budgetCase stops Each after n events with ErrScanBudget, as the store does.
type budgetCase struct {
	casestore.Case
	n int
}

func (b budgetCase) Each(ctx context.Context, q casestore.EventQuery, fn func(evidence.Event) error) error {
	i := 0
	err := b.Case.Each(ctx, q, func(e evidence.Event) error {
		if i >= b.n {
			return casestore.ErrScanBudget
		}
		i++
		return fn(e)
	})
	return err
}

type failCase struct{ casestore.Case }

func (failCase) Each(context.Context, casestore.EventQuery, func(evidence.Event) error) error {
	return casestore.ErrHandleClosed
}

func TestScanBudgetGivesAPartialIndex(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 10; i++ {
		h.add(pdh("web-a", at(time.Duration(i)*time.Minute), "Memory", "", "Pages/sec", 1, ""))
	}
	h.commit()
	x, err := resource.Catalogue(budgetCase{h.c, 4}, importer.Window{})
	if err != nil {
		t.Fatal(err)
	}
	ss := x.Series(resource.HostPages, resource.Filter{})
	if !x.Partial() || !contains(x.Gaps(), resource.GapPartial) || len(ss) != 1 || len(ss[0].Samples) != 4 {
		t.Fatalf("partial %v gaps %v series %+v", x.Partial(), x.Gaps(), ss)
	}
	if _, err := resource.Catalogue(failCase{h.c}, importer.Window{}); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Fatalf("store error %v", err)
	}
}

func TestSeriesReturnsCopies(t *testing.T) {
	h := newHarness(t)
	h.add(pdh("web-a", t0, "Memory", "", "Pages/sec", 1, ""), pdh("web-a", at(time.Minute), "Memory", "", "Pages/sec", 2, ""))
	x := h.catalogue(importer.Window{})
	ss := x.Series(resource.HostPages, resource.Filter{})
	ss[0].Samples[0].Value = 99
	if again := x.Series(resource.HostPages, resource.Filter{}); again[0].Samples[0].Value != 1 {
		t.Fatal("Series shares its samples with the index")
	}
}

func TestFixedWordingPassesTheCausalLint(t *testing.T) {
	texts := []string{resource.GapLegacyInstance, resource.GapPartial, resource.GapNoCadence}
	// Every event marker text, through a synthetic event of each kind.
	kinds := []string{"apppool-recycle", "apppool-idle-shutdown", "worker-process-failure", "host-unexpected-shutdown",
		"app-start", "app-session", "app-stop", "process-start", "process-gone", "redis-start", "redis-stop",
		"mongod-start", "mongod-stop"}
	h := newHarness(t)
	for i, k := range kinds {
		h.add(point("synthetic", k, "web-a", at(time.Duration(i)*time.Minute), "process", "w3wp", "pid", "1240", "reason", "time-interval", "id", "5009"))
	}
	x := h.catalogue(importer.Window{})
	ms := x.Markers()
	if len(ms) != len(kinds) {
		t.Fatalf("markers %d, want %d", len(ms), len(kinds))
	}
	for _, m := range ms {
		texts = append(texts, m.Text)
	}
	sort.Strings(texts)
	for _, s := range texts {
		if err := evidence.LintCausal(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
}
