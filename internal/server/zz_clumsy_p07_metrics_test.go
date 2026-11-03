package server

import (
	"context"
	"testing"
	"time"

	"api-audit-explorer/internal/investigate"
)

// A bad counter (negative) must never read as a quiet one. The store refuses NaN and infinite values at import, so those cannot arrive.
func TestClumsyP07M12BadCounterNeverNo(t *testing.T) {
	cases := map[string]func(i int) float64{
		"negative": func(i int) float64 { return -3 },
	}
	for name, f := range cases {
		in, _ := rejectedRun(t, 30, f, nil)
		if v, ok := in.Lanes["M12"]; ok {
			t.Errorf("%s: M12 = %q, want no answer", name, v)
		}
	}
}

// Bad incident windows: no lane work at all.
func TestClumsyP07M12BadWindow(t *testing.T) {
	c := rejectedCase(t, []int{0, 1, 2}, []float64{0, 5, 9})
	for name, w := range map[string]investigate.Window{
		"zero":     {},
		"inverted": {Start: invT0.Add(time.Hour), End: invT0},
		"empty":    {Start: invT0, End: invT0},
	} {
		in := investigate.Input{Incident: w}
		var read investigationRead
		readMetricLanes(context.Background(), c, &in, &read)
		if len(in.Lanes) != 0 || len(in.LaneSources) != 0 {
			t.Errorf("%s: lanes %v sources %v", name, in.Lanes, in.LaneSources)
		}
	}
}

// Rejections that happen outside the incident are not a Yes; a nil lane map is safe.
func TestClumsyP07M12RowsOutsideWindow(t *testing.T) {
	c := rejectedCase(t, []int{-60, -59, -58, 100, 101}, []float64{0, 4, 9, 20, 40})
	in := metricInput()
	var read investigationRead
	readMetricLanes(context.Background(), c, &in, &read)
	if v := in.Lanes["M12"]; v == investigate.Yes {
		t.Errorf("rejections outside the window read Yes")
	}
}

// A different reader's Yes is kept, even when the series is clean.
func TestClumsyP07MetricLaneKeepsYesAndNilMap(t *testing.T) {
	in := investigate.Input{}
	setMetricLane(&in, "M12", investigate.No, "MongoDB server status")
	if in.Lanes["M12"] != investigate.No {
		t.Fatalf("nil map: %v", in.Lanes)
	}
	in.Lanes["M12"] = investigate.Yes
	setMetricLane(&in, "M12", investigate.No, "MongoDB server status")
	setMetricLane(&in, "M12", investigate.No, "MongoDB server status")
	if in.Lanes["M12"] != investigate.Yes || len(in.LaneSources) != 1 {
		t.Errorf("lanes %v sources %v", in.Lanes, in.LaneSources)
	}
}
