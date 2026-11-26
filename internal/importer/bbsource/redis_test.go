package bbsource

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

func redisSample(seq int, at string, group string) string {
	return `{"v":1,"run":"r","seq":` + itoa(int64(seq)) + `,"t":"` + at + `","redis":` + group + `}` + "\n"
}

func ofKind(evs []evidence.Event, kind string) (out []evidence.Event) {
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func metric(evs []evidence.Event, key string) []evidence.Event {
	var out []evidence.Event
	for _, e := range ofKind(evs, KindRedisMetric) {
		if e.Attrs["metric"] == key {
			out = append(out, e)
		}
	}
	return out
}

func TestRedisFiguresReadLikeTheInfoImporter(t *testing.T) {
	g1 := `{"state":"ok","numbers":{"used_memory":1000,"connected_clients":7,"latest_fork_usec":5000,"used_cpu_user":10,"uptime_in_seconds":100},"words":{"role":"slave","redis_version":"3.0.504"}}`
	g2 := `{"state":"ok","numbers":{"used_memory":2000,"connected_clients":8,"used_cpu_user":10.15,"uptime_in_seconds":115}}`
	evs, rep, err := parse(t, redisSample(1, "2001-09-09T01:47:00Z", g1)+redisSample(2, "2001-09-09T01:47:15Z", g2), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m := metric(evs, "used_memory"); len(m) != 2 || *m[1].Value != 2000 || m[0].Unit != "bytes" || m[0].Attrs["role"] != "replica" {
		t.Errorf("used_memory %+v", m)
	}
	if m := metric(evs, "latest_fork_usec"); len(m) != 1 || *m[0].Value != 5 || m[0].Unit != "ms" {
		t.Errorf("fork time is milliseconds: %+v", m)
	}
	// CPU is the rate between two samples in percent of one core; the first sample has none.
	if m := metric(evs, "used_cpu_user"); len(m) != 1 || *m[0].Value < 0.99 || *m[0].Value > 1.01 || m[0].Unit != "percent-one-core" {
		t.Errorf("cpu %+v", m)
	}
	if len(ofKind(evs, KindRedisStart)) != 0 {
		t.Error("a steady uptime is no restart")
	}
	for _, e := range evs {
		for _, v := range e.Attrs {
			if strings.Contains(v, "3.0.504") {
				t.Errorf("a version word reached an event: %v", e.Attrs)
			}
		}
	}
	if !contains(rep.KnownGaps, GapGroupsNotRead) {
		t.Error("known gap missing")
	}
}

func TestRedisRestartIsFoundFromUptime(t *testing.T) {
	g := func(up int) string { return `{"state":"ok","numbers":{"uptime_in_seconds":` + itoa(int64(up)) + `}}` }
	evs, _, err := parse(t, redisSample(1, "2001-09-09T01:47:00Z", g(5000))+redisSample(2, "2001-09-09T01:47:15Z", g(12)), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st := ofKind(evs, KindRedisStart)
	if len(st) != 1 || !st[0].Start.Equal(st[0].End) || st[0].Start.Format("15:04:05") != "01:47:03" {
		t.Errorf("redis start %+v", st)
	}
}

func TestRedisSlowAndLatencyAreReadOnceAndWithTableNamesOnly(t *testing.T) {
	g := `{"state":"ok","slow":[{"id":5,"t":1000000100,"usec":250000,"cmd":"KEYS","arg":"secret-key"},{"id":6,"t":1000000101,"usec":9000,"cmd":"x-custom-SENTINEL"}],` +
		`"latency":[{"event":"fork","t":1000000090,"ms":120,"maxMs":300},{"event":"weird-SENTINEL","t":1000000091,"ms":9}]}`
	evs, _, err := parse(t, redisSample(1, "2001-09-09T01:47:00Z", g)+redisSample(2, "2001-09-09T01:47:15Z", g), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	slow := ofKind(evs, KindSlowCommand)
	if len(slow) != 2 || slow[0].Attrs["command"] != "KEYS" || slow[0].DurationMs == nil || *slow[0].DurationMs != 250 || slow[1].Attrs["command"] != "other" {
		t.Errorf("slow %+v", slow)
	}
	if slow[0].End.Sub(slow[0].Start) != 250*time.Millisecond {
		t.Errorf("a slow command starts its duration before its end: %v %v", slow[0].Start, slow[0].End)
	}
	lat := ofKind(evs, KindLatencySpike)
	if len(lat) != 2 || lat[0].Attrs["event"] != "fork" || lat[1].Attrs["event"] != "other" || *lat[0].Value != 120 {
		t.Errorf("latency %+v", lat)
	}
	for _, e := range evs {
		for _, v := range e.Attrs {
			if strings.Contains(v, "SENTINEL") || strings.Contains(v, "secret") {
				t.Errorf("recording text reached an event: %v", e.Attrs)
			}
		}
	}
}

func TestRedisDownGivesNoFigures(t *testing.T) {
	evs, _, err := parse(t, redisSample(1, "2001-09-09T01:47:00Z", `{"state":"down"}`)+good2, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ofKind(evs, KindRedisMetric)) != 0 {
		t.Error("a Redis that was down gave figures")
	}
}

func TestRedisBadReadingsCostOnlyThemselves(t *testing.T) {
	g := `{"state":"ok","numbers":{"used_memory":-5,"connected_clients":4},` +
		`"slow":[{"id":5,"t":1000000100,"usec":2000,"cmd":"get"},{"id":6,"t":0,"usec":1,"cmd":"get"}],` +
		`"latency":[{"event":"fork","t":1000000090,"ms":-1},{"event":"command","t":1000000091,"ms":7}]}`
	evs, rep, err := parse(t, redisSample(1, "2001-09-09T01:47:00Z", g)+good2, importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(metric(evs, "used_memory")) != 0 || len(metric(evs, "connected_clients")) != 1 {
		t.Error("only the bad figure is left out")
	}
	if len(ofKind(evs, KindSlowCommand)) != 1 || len(ofKind(evs, KindLatencySpike)) != 1 {
		t.Errorf("good neighbours of a bad entry were lost: %d slow, %d latency", len(ofKind(evs, KindSlowCommand)), len(ofKind(evs, KindLatencySpike)))
	}
	if !contains(rep.KnownGaps, GapGroupDamaged) || !contains(rep.KnownGaps, GapRedisEdges) {
		t.Errorf("known gaps %v", rep.KnownGaps)
	}
}

func TestRedisEntriesFromBeforeTheFileBelongToTheFileBefore(t *testing.T) {
	// The file starts at 01:47:00 (unix 1000000020); a spike 10 minutes before it was read in the file before.
	g := `{"state":"ok","latency":[{"event":"fork","t":1000000020,"ms":1}],"slow":[{"id":1,"t":1000000020,"usec":10,"cmd":"get"}]}`
	old := `{"state":"ok","latency":[{"event":"fork","t":999999300,"ms":9}],"slow":[{"id":0,"t":999999300,"usec":10,"cmd":"get"}]}`
	evs, _, err := parse(t, redisSample(1, "2001-09-09T01:47:00Z", old+"")+redisSample(2, "2001-09-09T01:47:15Z", g), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ofKind(evs, KindLatencySpike)) != 1 || len(ofKind(evs, KindSlowCommand)) != 1 {
		t.Errorf("%d latency, %d slow", len(ofKind(evs, KindLatencySpike)), len(ofKind(evs, KindSlowCommand)))
	}
}

func TestRedisRestartHasNoCPURateAcrossIt(t *testing.T) {
	a := `{"state":"ok","numbers":{"uptime_in_seconds":5000,"used_cpu_user":0.5}}`
	b := `{"state":"ok","numbers":{"uptime_in_seconds":10,"used_cpu_user":30}}`
	evs, _, err := parse(t, redisSample(1, "2001-09-09T01:47:00Z", a)+redisSample(2, "2001-09-09T01:47:15Z", b), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(metric(evs, "used_cpu_user")) != 0 {
		t.Error("a CPU rate was computed across a restart")
	}
	st := ofKind(evs, KindRedisStart)
	if len(st) != 1 || st[0].Severity != "info" {
		t.Errorf("redis start %+v", st)
	}
}
