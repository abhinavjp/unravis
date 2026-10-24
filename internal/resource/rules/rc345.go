package rules

import (
	"fmt"
	"sort"
	"strconv"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/resource"
)

const familyRedisTimeout = "redis-timeout"

// appLogSources are the application log importers.
var appLogSources = []string{"log4net-text", "log4net-xml", "serilog-clef", "serilog-text",
	"mel-console", "mel-json", "ancm-stdout", "node-jsonl"}

/* ---------------------------------------------------------------- RC3 */

// rc3 tests host CPU, available memory and disk time per transfer.
func (e *eval) rc3() {
	e.runCondition(condition{rule: "RC3", metric: resource.HostCPU, title: "High host CPU",
		share:      "Host CPU at or above " + num(e.th.CPUHigh) + " % on %s",
		thresholds: "host CPU at or above " + num(e.th.CPUHigh) + " %", test: atLeast(e.th.CPUHigh)})
	e.runCondition(condition{rule: "RC3", metric: resource.HostMemAvailable, title: "Low available memory",
		share:      "Available memory below " + bytesText(e.th.MemLowBytes) + " on %s",
		thresholds: "available memory below " + bytesText(e.th.MemLowBytes), lower: true,
		test: func(s resource.Sample) (float64, bool) { return s.Value, s.Value < e.th.MemLowBytes }})
	e.runCondition(condition{rule: "RC3", metric: resource.HostDiskSecPerTx, title: "Slow disk transfers",
		share:      "Disk time per transfer above " + num(e.th.DiskMs) + " ms on %s",
		thresholds: "disk time per transfer above " + num(e.th.DiskMs) + " ms", test: above(e.th.DiskMs)})
	for _, id := range []string{resource.HostCPU, resource.HostMemAvailable} {
		byHost := map[string][]resource.Series{}
		for _, s := range e.idx.Series(id, resource.Filter{}) {
			byHost[s.Host] = append(byHost[s.Host], s)
		}
		for host, ss := range byHost {
			var best resource.Sample
			found := false
			unit := ""
			for _, s := range ss {
				var sm resource.Sample
				var ok bool
				if id == resource.HostCPU {
					sm, ok = e.peak(s)
				} else {
					sm, ok = e.lowest(s)
				}
				if ok && (!found || (id == resource.HostCPU && sm.Value > best.Value) || (id != resource.HostCPU && sm.Value < best.Value)) {
					best, found, unit = sm, true, s.Metric.Unit
				}
			}
			if !found {
				continue
			}
			if id == resource.HostCPU {
				e.summary(host, "host CPU peak "+value(best.Value, unit))
			} else {
				e.summary(host, "lowest available memory "+value(best.Value, unit))
			}
		}
	}
}

/* ---------------------------------------------------------------- RC4 */

// rc4 tests time in GC, Gen 2 collections and time paused by GC.
func (e *eval) rc4() {
	e.runCondition(condition{rule: "RC4", metric: resource.CLRGCTime, title: "High time in GC",
		share:      "Time in GC at or above " + num(e.th.GCTimePct) + " % in a process on %s",
		thresholds: "time in GC at or above " + num(e.th.GCTimePct) + " %", test: atLeast(e.th.GCTimePct)})
	e.runCondition(condition{rule: "RC4", metric: resource.CLRGen2, title: "Gen 2 collections",
		share:      "At least " + num(e.th.Gen2Delta) + " new Gen 2 collection in one sample in a process on %s",
		thresholds: "at least " + num(e.th.Gen2Delta) + " new Gen 2 collection between two samples",
		test: func(s resource.Sample) (float64, bool) {
			return s.Delta, s.HasDelta && s.Delta >= e.th.Gen2Delta
		}})
	e.runCondition(condition{rule: "RC4", metric: resource.NetGCPause, title: "Time paused by GC",
		share:      "Time paused by GC above " + num(e.th.GCPauseMin) + " in a process on %s",
		thresholds: "time paused by GC above " + num(e.th.GCPauseMin), test: above(e.th.GCPauseMin)})

	dotnet := e.importsOf("dotnet-counters")
	core := e.importsOf("mel-console", "mel-json", "ancm-stdout")
	for host := range core {
		if host != "" && len(dotnet[host]) == 0 {
			e.gap(evidence.NotObservedGap("no runtime counters for this process on " + host))
		}
	}
}

/* ---------------------------------------------------------------- RC5 */

// rc5 tests the thread-pool, ASP.NET and Kestrel queues, Redis timeout log
// entries with more busy workers than the minimum, and late heartbeats.
func (e *eval) rc5() {
	e.runCondition(condition{rule: "RC5", metric: resource.NetTPQueue, title: "Thread-pool queue",
		share:      "Thread-pool queue length above 0 in " + itoa(e.th.TPQueueSamples) + " or more samples in a process on %s",
		thresholds: "thread-pool queue length above 0 in " + itoa(e.th.TPQueueSamples) + " or more samples",
		minSamples: e.th.TPQueueSamples, test: above(0)})
	e.runCondition(condition{rule: "RC5", metric: resource.ASPNetQueued, title: "ASP.NET requests queued",
		share: "ASP.NET Requests Queued above 0 on %s", thresholds: "ASP.NET Requests Queued above 0", test: above(0)})
	e.runCondition(condition{rule: "RC5", metric: resource.KestrelQueued, title: "Kestrel queue",
		share: "Kestrel queue length above 0 in a process on %s", thresholds: "Kestrel queue length above 0", test: above(0)})

	// .NET Framework hosts have no thread-pool counter.
	tp := map[string]bool{}
	for _, s := range e.idx.Series(resource.NetTPQueue, resource.Filter{}) {
		tp[s.Host] = true
	}
	for _, s := range e.idx.Series(resource.ASPNetQueued, resource.Filter{}) {
		if !tp[s.Host] {
			e.gap(s.Host + ": no thread-pool counter exists; ASP.NET Requests Queued shown instead")
		}
	}

	// Redis timeout entries with WORKER busy above min.
	var picked []evidence.Event
	counts := map[string]int{}
	for _, ev := range e.events["log-entry"] {
		counts[ev.Host]++
		b, errB := strconv.ParseFloat(ev.Attrs["worker-busy"], 64)
		m, errM := strconv.ParseFloat(ev.Attrs["worker-min"], 64)
		if errB == nil && errM == nil && b > m {
			picked = append(picked, ev)
		}
	}
	e.pointShare("RC5", "Redis timeout entries with busy workers", "Redis timeout entries with WORKER busy above min on %s",
		"Redis timeout log entries whose WORKER busy count is above its min", picked)
	hosts := make([]string, 0, len(counts))
	for h := range counts {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		e.summary(h, fmt.Sprintf("%d Redis timeout entries", counts[h]))
	}

	e.overlap("RC5", []string{"heartbeat-late"}, "late heartbeat overlapping a stall, joined per host and source")
}
