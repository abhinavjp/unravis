package server

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/investigate"
)

// memGrantsCase imports win-counters samples into a real in-memory case.
func memGrantsCase(t *testing.T, evs []evidence.Event) casestore.Case {
	t.Helper()
	ctx := context.Background()
	store := casestore.NewMemory()
	info, err := store.Create(ctx, "metrics")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	for i := range evs {
		evs[i].EvidenceID = evidence.EventID(string(info.ID), "win-counters", "n1", int64(i+1), 0)
		evs[i].Line = int64(i + 1)
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "win-counters"}
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
	if len(evs) > 0 {
		_, err = c.Import(ctx, casestore.ImportMeta{Importer: "win-counters", FileName: "f.txt", AckGating: true, AckText: "ack"},
			strings.NewReader("file"), run)
		if err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func grantSample(at time.Time, v float64) evidence.Event {
	val := v
	return evidence.Event{Source: "win-counters", Kind: "counter", Host: "sql-a", Start: at, End: at, Value: &val,
		TZRule: clock.RuleHeaderBias,
		Attrs:  map[string]string{"object": "SQLServer:Memory Manager", "counter": "Memory Grants Pending"}}
}

// grantSamples is one sample a minute for n minutes from invT0, value i%4 with
// the optional spike at minute 10.
func grantSamples(n int, spike float64) []evidence.Event {
	var evs []evidence.Event
	for i := 0; i < n; i++ {
		v := float64(i % 4)
		if spike > 0 && i == 10 {
			v = spike
		}
		evs = append(evs, grantSample(invT0.Add(time.Duration(i)*time.Minute), v))
	}
	return evs
}

func metricInput() investigate.Input {
	return investigate.Input{Incident: investigate.Window{Start: invT0, End: invT0.Add(30 * time.Minute)}}
}

func TestReadMetricLanesNoCounters(t *testing.T) {
	c := memGrantsCase(t, nil)
	in := metricInput()
	var read investigationRead
	readMetricLanes(context.Background(), c, &in, &read)
	if _, ok := in.Lanes["D6"]; ok || len(read.Warnings) != 0 || len(in.LaneSources) != 0 {
		t.Errorf("lanes %v warnings %v sources %v", in.Lanes, read.Warnings, in.LaneSources)
	}
}

func TestReadMetricLanesNo(t *testing.T) {
	c := memGrantsCase(t, grantSamples(30, 0))
	in := metricInput()
	var read investigationRead
	readMetricLanes(context.Background(), c, &in, &read)
	if in.Lanes["D6"] != investigate.No || len(in.LaneSources) != 1 || in.LaneSources[0] != "SQL Server counters" {
		t.Errorf("lanes %v sources %v warnings %v", in.Lanes, in.LaneSources, read.Warnings)
	}
}

func TestReadMetricLanesYes(t *testing.T) {
	c := memGrantsCase(t, grantSamples(30, 12))
	in := metricInput()
	var read investigationRead
	readMetricLanes(context.Background(), c, &in, &read)
	if in.Lanes["D6"] != investigate.Yes {
		t.Errorf("lanes %v warnings %v", in.Lanes, read.Warnings)
	}
}

func TestReadMetricLanesLowCoverage(t *testing.T) {
	c := memGrantsCase(t, grantSamples(10, 0))
	in := metricInput()
	var read investigationRead
	readMetricLanes(context.Background(), c, &in, &read)
	if _, ok := in.Lanes["D6"]; ok {
		t.Errorf("D6 set on 10 of 30 minutes: %v", in.Lanes)
	}
}

func TestReadMetricLanesKeepsYes(t *testing.T) {
	c := memGrantsCase(t, grantSamples(30, 0))
	in := metricInput()
	in.Lanes = map[string]investigate.Tri{"D6": investigate.Yes}
	var read investigationRead
	readMetricLanes(context.Background(), c, &in, &read)
	if in.Lanes["D6"] != investigate.Yes {
		t.Errorf("Yes overwritten: %v", in.Lanes)
	}
}

// rejectedCase imports mongo-serverstatus connections.rejected samples, one a minute from invT0, one per value.
func rejectedCase(t *testing.T, minutes []int, values []float64) casestore.Case {
	t.Helper()
	ctx := context.Background()
	store := casestore.NewMemory()
	info, err := store.Create(ctx, "metrics")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	var evs []evidence.Event
	for i, m := range minutes {
		at := invT0.Add(time.Duration(m) * time.Minute)
		val := values[i]
		evs = append(evs, evidence.Event{Source: "mongo-serverstatus", Kind: "metric", Host: "mongo-a", Start: at, End: at, Value: &val,
			Unit: "count", TZRule: clock.RuleHeaderBias, EvidenceID: evidence.EventID(string(info.ID), "mongo-serverstatus", "n1", int64(i+1), 0), Line: int64(i + 1),
			Attrs: map[string]string{"metric": "connections.rejected"}})
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "mongo-serverstatus"}
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
	if len(evs) > 0 {
		_, err = c.Import(ctx, casestore.ImportMeta{Importer: "mongo-serverstatus", FileName: "f.jsonl", AckGating: true, AckText: "ack"},
			strings.NewReader("file"), run)
		if err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func rejectedRun(t *testing.T, minutes int, values func(i int) float64, pre map[string]investigate.Tri) (investigate.Input, investigationRead) {
	t.Helper()
	var ms []int
	var vs []float64
	for i := 0; i < minutes; i++ {
		ms = append(ms, i)
		vs = append(vs, values(i))
	}
	c := rejectedCase(t, ms, vs)
	in := metricInput()
	in.Lanes = pre
	var read investigationRead
	readMetricLanes(context.Background(), c, &in, &read)
	return in, read
}

func TestReadMetricLanesM12None(t *testing.T) {
	in, read := rejectedRun(t, 0, nil, nil)
	if _, ok := in.Lanes["M12"]; ok || len(in.LaneSources) != 0 || len(read.Warnings) != 0 {
		t.Errorf("lanes %v sources %v warnings %v", in.Lanes, in.LaneSources, read.Warnings)
	}
}

func TestReadMetricLanesM12Yes(t *testing.T) {
	in, read := rejectedRun(t, 30, func(i int) float64 {
		if i >= 2 {
			return 3
		}
		return 0
	}, nil)
	if in.Lanes["M12"] != investigate.Yes {
		t.Fatalf("lanes %v warnings %v", in.Lanes, read.Warnings)
	}
	n := 0
	for _, s := range in.LaneSources {
		if s == "MongoDB server status" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("sources %v", in.LaneSources)
	}
}

func TestReadMetricLanesM12No(t *testing.T) {
	in, read := rejectedRun(t, 30, func(int) float64 { return 0 }, nil)
	if in.Lanes["M12"] != investigate.No {
		t.Errorf("lanes %v warnings %v", in.Lanes, read.Warnings)
	}
}

func TestReadMetricLanesM12Gappy(t *testing.T) {
	in, _ := rejectedRun(t, 10, func(int) float64 { return 0 }, nil)
	if _, ok := in.Lanes["M12"]; ok {
		t.Errorf("M12 set on 10 of 30 minutes: %v", in.Lanes)
	}
}

func TestReadMetricLanesM12Reset(t *testing.T) {
	vals := []float64{7, 7, 0, 0}
	in, _ := rejectedRun(t, 30, func(i int) float64 {
		if i < len(vals) {
			return vals[i]
		}
		return 0
	}, nil)
	if _, ok := in.Lanes["M12"]; ok {
		t.Errorf("M12 set across a counter reset: %v", in.Lanes)
	}
}

func TestReadMetricLanesM12KeepsYes(t *testing.T) {
	in, _ := rejectedRun(t, 30, func(int) float64 { return 0 }, map[string]investigate.Tri{"M12": investigate.Yes})
	if in.Lanes["M12"] != investigate.Yes {
		t.Errorf("Yes overwritten: %v", in.Lanes)
	}
}
