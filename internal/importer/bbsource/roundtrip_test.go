package bbsource

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/bbredis"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/importer"
)

func fp(v float64) *float64 { return &v }
func up(v uint64) *uint64   { return &v }

// TestTheReaderReadsWhatTheRecorderWrites marshals real blackbox.Sample values, with every group this reader knows, and
// reads them back. A tag that drifts between the recorder and this reader fails here, not on a server.
func TestTheReaderReadsWhatTheRecorderWrites(t *testing.T) {
	t0 := time.Date(2001, 9, 9, 1, 47, 0, 0, time.UTC)
	mk := func(seq uint64, at time.Time) blackbox.Sample {
		return blackbox.Sample{
			V: blackbox.FormatVersion, Run: "run-1", Seq: seq, T: at,
			CPU: fp(12.5), MemAvail: up(4 << 30), MemCommit: up(9 << 30), DiskQueue: fp(0.5), DiskMs: fp(3.2), DiskFreePct: fp(55),
			HTTP: &blackbox.HTTPGroup{Queues: []blackbox.HTTPQueue{{Pool: "p", CurrentQueueSize: fp(3), MaxQueueItemAge: fp(1500), RejectedRequests: fp(0), ArrivalRate: fp(9)}}},
			IIS:  &blackbox.IISGroup{Workers: []blackbox.IISWorker{{Pool: "p", PID: 4321, ActiveRequests: fp(7), ActiveThreads: fp(12), PingLatencyMs: fp(11)}}},
			SQLHost: &blackbox.SQLHostGroup{Instances: []blackbox.SQLHostInstance{{Instance: "MSSQLSERVER", BatchRequestsPerSec: fp(120),
				UserConnections: fp(40), PageLifeExpectancy: fp(3000), DeadlocksPerSec: fp(0), LogFlushWaitTimeMs: fp(2)}}},
			TCP: &blackbox.TCPGroup{RetransmitsPerSec: fp(1.5), ResetsPerSec: fp(0.1), OutboundQueue: fp(2), ConnFailuresPerSec: fp(0), DiscardedPerSec: fp(0), OutErrorsPerSec: fp(0)},
			Redis: &blackbox.RedisGroup{State: blackbox.RedisOK,
				Numbers: map[string]float64{"used_memory": 1000, "connected_clients": 7, "uptime_in_seconds": 100 + float64(seq)*15, "used_cpu_user": float64(seq)},
				Words:   map[string]string{"role": "master"},
				Slow:    []bbredis.SlowEntry{{ID: 1, Unix: t0.Unix(), Usec: 250000, Cmd: "keys"}},
				Latency: []bbredis.LatencyEntry{{Event: "fork", Unix: t0.Unix(), Last: 120, Max: 300}}},
			Events: &blackbox.EventsGroup{Events: []blackbox.EventEntry{{Channel: 1, EventID: 7034, Level: 2, Count: 3, T: at}}},
			SQLLog: &blackbox.SQLLogGroup{Errors: []blackbox.SQLLogEvent{{Instance: "MSSQLSERVER", Kind: blackbox.SQLLogKindError, Number: 823, Severity: 24, Count: 1, T: at}},
				Health: []blackbox.SQLHealthEvent{{Instance: "MSSQLSERVER", Kind: blackbox.SQLHealthDeadlock, Count: 1, T: at}}},
			Dumps: &blackbox.DumpsGroup{Dumps: []blackbox.DumpEntry{{Name: "w3wp.dmp", Size: 123456, MTime: t0}}},
		}
	}
	var b strings.Builder
	for i := 0; i < 4; i++ {
		raw, err := json.Marshal(mk(uint64(i+1), t0.Add(time.Duration(i)*15*time.Second)))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteString("\n")
	}
	evs, rep, err := parse(t, b.String(), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 0 {
		t.Fatalf("the reader rejected %d rows the recorder wrote", rep.Rejected)
	}
	want := map[string]bool{
		"counter:% Processor Time": false, "counter:Batch Requests/sec": false, "counter:Page life expectancy": false,
		"counter:MaxQueueItemAge": false, "counter:Active Requests": false, "counter:Retransmits/sec": false,
		"metric:used_memory": false, "metric:used_cpu_user": false, "slow-command:": false, "latency-spike:": false,
		"win-event:": false, "error:": false, "deadlock:": false, "dump-file:": false, "recorder-start:": false,
	}
	for _, e := range evs {
		k := e.Kind + ":" + e.Attrs["counter"] + e.Attrs["metric"]
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("no %q event from a recording the recorder wrote", k)
		}
	}
	// Each of the 4 samples carries the same slow entry, spike and dump: they are shown once.
	for kind, n := range map[string]int{KindSlowCommand: 1, KindLatencySpike: 1, KindDumpFile: 1} {
		if got := len(ofKind(evs, kind)); got != n {
			t.Errorf("%d %s events, want %d", got, kind, n)
		}
	}
}
