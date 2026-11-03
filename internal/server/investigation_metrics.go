package server

import (
	"context"
	"math"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/investigate"
	"api-audit-explorer/internal/resource"
)

// Marks for metric-fed lane answers (FETCHABILITY-AUDIT 6c), local to the
// server reader. Not in Thresholds, so they do not move Version.
const (
	memGrantsPendingMin = 10.0
	metricLaneCoverage  = 0.8
)

// readMetricLanes builds the resource catalogue for the incident window once
// and answers the lanes that read a metric series. A catalogue error is a
// warning, never a failed run.
func readMetricLanes(ctx context.Context, c casestore.Case, in *investigate.Input, read *investigationRead) {
	if !in.Incident.Valid() {
		return
	}
	// No counter or server-status file in the case: nothing to read, so no scan at all.
	imports, err := c.Imports(ctx)
	if err != nil {
		read.Warnings = append(read.Warnings, "The resource counters could not be read for the incident window.")
		return
	}
	held := false
	for _, f := range imports {
		if f.Importer == "win-counters" || f.Importer == "sql-pdh" || f.Importer == "mongo-serverstatus" {
			held = true
		}
	}
	if !held {
		return
	}
	x, err := resource.CatalogueContext(ctx, c, importer.Window{From: in.Incident.Start, To: in.Incident.End})
	if err != nil {
		read.Warnings = append(read.Warnings, "The resource counters could not be read for the incident window.")
		return
	}
	if x.Partial() {
		read.Warnings = append(read.Warnings, "The resource counters were too many to read in full, so the counter-based lane readings were not used.")
		return
	}
	if v := memGrantsLane(x); v != investigate.Unknown {
		setMetricLane(in, "D6", v, "SQL Server counters")
	}
	if v := mongoRejectedLane(x); v != investigate.Unknown {
		setMetricLane(in, "M12", v, "MongoDB server status")
	}
}

// setMetricLane records a lane answer (never over a Yes) and names its source once.
func setMetricLane(in *investigate.Input, id string, v investigate.Tri, source string) {
	if in.Lanes == nil {
		in.Lanes = map[string]investigate.Tri{}
	}
	if in.Lanes[id] != investigate.Yes {
		in.Lanes[id] = v
	}
	for _, s := range in.LaneSources {
		if s == source {
			return
		}
	}
	in.LaneSources = append(in.LaneSources, source)
}

// memGrantsLane reads SQL memory grants pending (D6).
func memGrantsLane(x resource.Index) investigate.Tri {
	ss := x.Series(resource.SQLMemGrantsPending, resource.Filter{})
	if len(ss) == 0 {
		return investigate.Unknown
	}
	peak, cov := 0.0, 0.0
	for _, s := range ss {
		// A pending count cannot be negative: a series that holds one is a bad
		// counter, not a quiet one, and it must not read as a No.
		bad := false
		for _, sm := range s.Samples {
			if sm.Value < 0 || math.IsNaN(sm.Value) || math.IsInf(sm.Value, 0) {
				bad = true
			}
			if sm.Value > peak {
				peak = sm.Value
			}
		}
		if bad {
			continue
		}
		if v := x.Coverage(s); v > cov {
			cov = v
		}
	}
	if peak >= memGrantsPendingMin {
		return investigate.Yes
	}
	if cov >= metricLaneCoverage {
		return investigate.No
	}
	return investigate.Unknown
}

// mongoRejectedLane reads connections.rejected (M12). Yes when any connection
// was refused in the incident; No only on a full, unbroken series of zeros.
func mongoRejectedLane(x resource.Index) investigate.Tri {
	ss := x.Series(resource.MongoConnsRejected, resource.Filter{})
	if len(ss) == 0 {
		return investigate.Unknown
	}
	sum, cov, reset := 0.0, 0.0, false
	for _, s := range ss {
		// A count that is negative or not a finite number is a bad counter, not a
		// quiet one: that series is left out, so it can never read as a No.
		bad, part := false, 0.0
		for _, sm := range s.Samples {
			if sm.Value < 0 || math.IsNaN(sm.Value) || math.IsInf(sm.Value, 0) || math.IsNaN(sm.Delta) || math.IsInf(sm.Delta, 0) {
				bad = true
			}
			if sm.HasDelta {
				part += sm.Delta
			}
			reset = reset || sm.Reset
		}
		if bad {
			continue
		}
		sum += part
		if v := x.Coverage(s); v > cov {
			cov = v
		}
	}
	if sum > 0 {
		return investigate.Yes
	}
	if !reset && cov >= metricLaneCoverage {
		return investigate.No
	}
	return investigate.Unknown
}
