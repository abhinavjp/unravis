package bbsource

import (
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const groupLine = `{"v":1,"run":"r","seq":2,"t":"2001-09-09T01:47:00Z","cpu":5,` +
	`"sqlHost":{"instances":[{"instance":"PROD2","batchRequestsPerSec":120,"pageLifeExpectancy":3000,"deadlocksPerSec":0},` +
	`{"instance":"MSSQLSERVER","batchRequestsPerSec":80,"memoryGrantsPending":2}]},` +
	`"http":{"queues":[{"pool":"Pool A","currentQueueSize":3,"maxQueueItemAge":1500,"rejectedRequests":0}]},` +
	`"iis":{"workers":[{"pool":"Pool A","pid":4,"activeRequests":7,"pingLatencyMs":12},{"pool":"Pool A","pid":9,"activeRequests":1}]},` +
	`"tcp":{"retransmitsPerSec":1.5,"outboundQueue":0}}` + "\n"

func find(evs []evidence.Event, object, counter, sqlinst string) *evidence.Event {
	for i := range evs {
		a := evs[i].Attrs
		if evs[i].Kind == KindCounter && a["object"] == object && a["counter"] == counter && a["sqlinstance"] == sqlinst {
			return &evs[i]
		}
	}
	return nil
}

func TestGroupsBecomeCountersWithTheWindowsNames(t *testing.T) {
	evs, rep, err := parse(t, good+groupLine, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Two SQL instances are told apart by their place (the default instance first), never by name.
	p1, p2 := find(evs, "SQLServer:SQL Statistics", "Batch Requests/sec", "1"), find(evs, "SQLServer:SQL Statistics", "Batch Requests/sec", "2")
	if p1 == nil || p2 == nil || *p1.Value != 80 || *p2.Value != 120 {
		t.Fatalf("batch requests: %v %v", p1, p2)
	}
	for _, e := range evs {
		for _, v := range e.Attrs {
			if strings.Contains(v, "PROD") || strings.Contains(v, "Pool") || strings.Contains(v, "MSSQL") {
				t.Errorf("an instance name reached the timeline: %v", e.Attrs)
			}
		}
	}
	if e := find(evs, "SQLServer:Buffer Manager", "Page life expectancy", "2"); e == nil || *e.Value != 3000 || e.Unit != "" {
		t.Errorf("page life expectancy: %+v", e)
	}
	if e := find(evs, "SQLServer:Locks", "Number of Deadlocks/sec", "2"); e == nil || *e.Value != 0 || e.Attrs["instance"] != "_Total" {
		t.Errorf("a zero that was read stays a zero: %+v", e)
	}
	if find(evs, "SQLServer:Locks", "Lock Waits/sec", "1") != nil {
		t.Error("a figure that was not read became an event")
	}
	if e := find(evs, "HTTP Service Request Queues", "MaxQueueItemAge", ""); e == nil || *e.Value != 1500 || e.Attrs["instance"] != "queue-1" {
		t.Errorf("http queue: %+v", e)
	}
	if e := find(evs, "HTTP Service Request Queues", "RejectedRequests", ""); e == nil {
		t.Error("the HTTP.sys counter keeps the name the recorder reads")
	}
	// Two workers of one pool are two series, told apart by process id.
	seen := map[string]float64{}
	for _, e := range evs {
		if e.Attrs["object"] == "W3SVC_W3WP" && e.Attrs["counter"] == "Active Requests" {
			seen[e.Attrs["instance"]] = *e.Value
		}
	}
	if seen["pid-4"] != 7 || seen["pid-9"] != 1 || len(seen) != 2 {
		t.Errorf("iis workers: %v", seen)
	}
	if e := find(evs, "Blackbox IIS", "Worker Ping Latency (ms)", ""); e == nil || e.Unit != "ms" {
		t.Errorf("ping: %+v", e)
	}
	if e := find(evs, "Blackbox TCP", "Retransmits/sec", ""); e == nil || *e.Value != 1.5 || e.Unit != "per-sec" {
		t.Errorf("tcp: %+v", e)
	}
	// A single SQL instance is number 1, so a second one never renames it.
	one := `{"v":1,"run":"r","seq":3,"t":"2001-09-09T01:47:15Z","sqlHost":{"instances":[{"instance":"X","userConnections":9}]}}` + "\n"
	evs, _, _ = parse(t, good+one, importer.Options{})
	if e := find(evs, "SQLServer:General Statistics", "User Connections", "1"); e == nil || *e.Value != 9 {
		t.Errorf("single instance: %+v", e)
	}
	if !contains(rep.KnownGaps, GapGroupsNotRead) {
		t.Error("the groups still not read must be said")
	}
	ids := map[string]bool{}
	for _, e := range evs {
		if ids[e.EvidenceID] {
			t.Fatalf("duplicate evidence id %s", e.EvidenceID)
		}
		ids[e.EvidenceID] = true
	}
}

func TestABadGroupNumberLeavesOutTheSampleButKeepsItsStamp(t *testing.T) {
	bad := `{"v":1,"run":"r","seq":1,"t":"2001-09-09T01:47:00Z","cpu":5,"tcp":{"retransmitsPerSec":-3}}` + "\n"
	evs, rep, err := parse(t, bad+good2, importer.Options{Limits: importer.Limits{MaxRejectFraction: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || len(counters(evs, "% Processor Time")) != 1 {
		t.Errorf("rejected %d, cpu events %d", rep.Rejected, len(counters(evs, "% Processor Time")))
	}
	starts := 0
	for _, e := range evs {
		if e.Kind == KindRunStart {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("%d run starts", starts)
	}
}

func TestTooManyInstancesAreCut(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"v":1,"run":"r","seq":1,"t":"2001-09-09T01:47:00Z","http":{"queues":[`)
	for i := 0; i < maxPools+30; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"pool":"p` + itoa(int64(i)) + `","currentQueueSize":1}`)
	}
	b.WriteString("]}}\n")
	evs, rp, err := parse(t, b.String(), importer.Options{Limits: importer.Limits{MaxRejectFraction: 1}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Attrs["counter"] == "CurrentQueueSize" {
			n++
		}
	}
	if n != maxPools {
		t.Errorf("%d queue events, want the cap %d", n, maxPools)
	}
	if !contains(rp.KnownGaps, GapGroupCut) {
		t.Errorf("the cut must be said: %v", rp.KnownGaps)
	}
}

func TestPartialTCPSumsAreLeftOutAndSaid(t *testing.T) {
	data := `{"v":1,"run":"r","seq":1,"t":"2001-09-09T01:47:00Z","tcp":{"outboundQueue":3,"outboundQueuePartial":true,"discardedPerSec":1,"retransmitsPerSec":2}}` + "\n"
	evs, rep, err := parse(t, data, importer.Options{Limits: importer.Limits{MaxRejectFraction: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if find(evs, "Blackbox TCP", "Outbound Queue", "") != nil || find(evs, "Blackbox TCP", "Discarded Packets/sec", "") == nil {
		t.Error("only the partial sum is left out")
	}
	if !contains(rep.KnownGaps, GapPartialSum) {
		t.Errorf("known gaps %v", rep.KnownGaps)
	}
}

func TestADamagedGroupCostsOnlyItself(t *testing.T) {
	data := `{"v":1,"run":"r","seq":1,"t":"2001-09-09T01:47:00Z","cpu":5,"tcp":"x","http":{"queues":"y"}}` + "\n"
	evs, rep, err := parse(t, data, importer.Options{Limits: importer.Limits{MaxRejectFraction: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(counters(evs, "% Processor Time")) != 1 || rep.Rejected != 0 {
		t.Errorf("cpu events %d, rejected %d", len(counters(evs, "% Processor Time")), rep.Rejected)
	}
	if !contains(rep.KnownGaps, GapGroupDamaged) {
		t.Errorf("known gaps %v", rep.KnownGaps)
	}
}
