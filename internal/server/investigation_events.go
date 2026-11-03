package server

import (
	"context"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/investigate"
)

// Imported event sources the engine reads directly (Phase 06, 6a and 6b).
const (
	sourceSQLXE       = "sql-xe"
	sourceSQLErrLog   = "sql-errorlog"
	sourceHTTPErr     = "iis-httperr"
	kindProbeLookback = 30 * 24 * time.Hour
)

// readImportedEvents adds SQL Server and HTTPERR rows from validated sources to
// in. A read that hits the event cap is dropped with a warning, the same rule as
// the IIS read: a partial list would read as a missing burst.
func readImportedEvents(ctx context.Context, c casestore.Case, body investigationRequest, sources []calltrace.SourceInfo, in *investigate.Input, read *investigationRead) error {
	has := map[string]bool{}
	for _, s := range sources {
		if s.Validated {
			has[s.Name] = true
		}
	}
	if !has[sourceSQLXE] && !has[sourceSQLErrLog] && !has[sourceHTTPErr] {
		return nil
	}
	ranges := body.ranges()

	// readKinds reads kinds of one source, one query per stretch, each with its
	// own cap. ok is false when any stretch hit the cap.
	readKinds := func(src string, kinds []string) ([]evidence.Event, bool, error) {
		var evs []evidence.Event
		seen := map[string]bool{}
		for _, rg := range ranges {
			part, err := c.Events(ctx, casestore.EventQuery{
				Window:  importer.Window{From: rg.Start, To: rg.End},
				Sources: []string{src},
				Kinds:   kinds,
				Limit:   casestore.MaxEventQuery,
			})
			if err != nil {
				return nil, false, err
			}
			if len(part) >= casestore.MaxEventQuery {
				return nil, false, nil
			}
			for _, e := range part {
				if e.EvidenceID != "" && seen[e.EvidenceID] {
					continue
				}
				seen[e.EvidenceID] = true
				evs = append(evs, e)
			}
		}
		return evs, true, nil
	}

	if has[sourceSQLXE] || has[sourceSQLErrLog] {
		var evs []evidence.Event
		ok := true
		if has[sourceSQLXE] {
			part, good, err := readKinds(sourceSQLXE, []string{"deadlock", "call", "blocked-process", "attention"})
			if err != nil {
				return err
			}
			evs, ok = append(evs, part...), ok && good
		}
		if ok && has[sourceSQLErrLog] {
			part, good, err := readKinds(sourceSQLErrLog, []string{"login-failed"})
			if err != nil {
				return err
			}
			evs, ok = append(evs, part...), ok && good
		}
		if !ok {
			read.Warnings = append(read.Warnings, "There are too many SQL Server event rows in these windows to read in full, so they were not used.")
		} else {
			in.SQLPresent = true
			for _, e := range evs {
				t := e.Start
				if e.Kind == "call" {
					t = e.End // completion time
				}
				ev := investigate.SQLEvent{T: t.UTC(), Kind: e.Kind}
				if e.DurationMs != nil {
					d := *e.DurationMs
					ev.DurationMs = &d
				}
				in.SQLEvents = append(in.SQLEvents, ev)
			}
			in.SQLKinds = map[string]bool{
				"deadlock":     has[sourceSQLXE], // system_health always records deadlocks
				"login-failed": has[sourceSQLErrLog],
			}
			if has[sourceSQLXE] && len(ranges) > 0 {
				from, to := ranges[0].Start, ranges[len(ranges)-1].End
				for _, k := range []string{"call", "blocked-process", "attention"} {
					// system_health never captures these, so no row at all reads unknown.
					probe, err := c.Events(ctx, casestore.EventQuery{
						Window:  importer.Window{From: from.Add(-kindProbeLookback), To: to},
						Sources: []string{sourceSQLXE},
						Kinds:   []string{k},
						Limit:   1,
					})
					if err != nil {
						return err
					}
					in.SQLKinds[k] = len(probe) > 0
				}
			}
			in.ApplySQLEvents()
		}
	}

	if has[sourceHTTPErr] {
		evs, ok, err := readKinds(sourceHTTPErr, []string{"httperr"})
		if err != nil {
			return err
		}
		if !ok {
			read.Warnings = append(read.Warnings, "There are too many HTTPERR rows in these windows to read in full, so they were not used.")
			return nil
		}
		in.HTTPErrPresent = true
		for _, e := range evs {
			in.HTTPErr = append(in.HTTPErr, investigate.HTTPErrRow{T: e.Start.UTC(), Reason: e.Attrs["reason"], Status: e.Attrs["status"]})
		}
	}
	return nil
}
