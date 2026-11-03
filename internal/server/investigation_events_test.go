package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/investigate"
)

func importedBody() investigationRequest {
	return investigationRequest{SourceIDs: []string{"demo-west"},
		Incident:  investigationWindow{invT0.Add(time.Hour), invT0.Add(150 * time.Minute)},
		Reference: investigationWindow{invT0, invT0.Add(time.Hour)}}
}

func importedEvent(i int, src, kind string, at time.Time, attrs map[string]string) evidence.Event {
	return evidence.Event{EvidenceID: fmt.Sprintf("%s-%d", kind, i), Source: src, Kind: kind, Start: at, End: at, Attrs: attrs}
}

// importedCase answers each query with the rows of the source and kinds it asks
// for. A probe (Limit 1) gets at most one row.
func importedCase(imports []casestore.ImportRecord, rows ...evidence.Event) *stubTraceCase {
	return &stubTraceCase{imports: imports, events: func(q casestore.EventQuery) []evidence.Event {
		var out []evidence.Event
		for _, e := range rows {
			if len(q.Sources) == 1 && q.Sources[0] == e.Source && containsKind(q.Kinds, e.Kind) {
				out = append(out, e)
			}
		}
		if q.Limit == 1 && len(out) > 1 {
			out = out[:1]
		}
		return out
	}}
}

func containsKind(ks []string, k string) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

func TestReadImportedEventsNone(t *testing.T) {
	c := &stubTraceCase{imports: []casestore.ImportRecord{stubImport("sqlserver-xe", invT0, invT0.Add(3*time.Hour))}}
	var read investigationRead
	in, err := buildInvestigationInput(context.Background(), c, importedBody(), nil, investigate.AuditPresent, &read)
	if err != nil {
		t.Fatal(err)
	}
	if in.SQLPresent || in.HTTPErrPresent || len(c.queries) != 0 {
		t.Errorf("SQL %v HTTPERR %v queries %d", in.SQLPresent, in.HTTPErrPresent, len(c.queries))
	}
	if _, ok := in.Lanes["D2"]; ok {
		t.Errorf("D2 set without a SQL source: %v", in.Lanes)
	}
}

func TestReadImportedEventsSQL(t *testing.T) {
	var rows []evidence.Event
	for i := 0; i < 6; i++ {
		rows = append(rows, importedEvent(i, sourceSQLXE, "deadlock", invT0.Add(time.Hour+time.Duration(10+i)*time.Minute), nil))
	}
	c := importedCase([]casestore.ImportRecord{stubImport(sourceSQLXE, invT0, invT0.Add(3*time.Hour))}, rows...)
	var read investigationRead
	in, err := buildInvestigationInput(context.Background(), c, importedBody(), nil, investigate.AuditPresent, &read)
	if err != nil {
		t.Fatal(err)
	}
	if !in.SQLPresent || len(in.SQLEvents) != 6 || !in.SQLKinds["deadlock"] || in.SQLKinds["attention"] {
		t.Fatalf("SQL %v events %d kinds %v", in.SQLPresent, len(in.SQLEvents), in.SQLKinds)
	}
	if in.Lanes["D2"] != investigate.Yes {
		t.Errorf("D2 = %v", in.Lanes["D2"])
	}
	for _, q := range c.queries {
		if len(q.Sources) != 1 || q.Sources[0] != sourceSQLXE || q.Limit == 0 {
			t.Errorf("query = %+v", q)
		}
	}
}

func TestReadImportedEventsSQLKindProbe(t *testing.T) {
	at := invT0.Add(time.Hour + 5*time.Minute)
	d := 1500.0
	call := importedEvent(1, sourceSQLXE, "call", at, nil)
	call.End, call.Start, call.DurationMs = at, at.Add(-time.Duration(d)*time.Millisecond), &d
	c := importedCase([]casestore.ImportRecord{stubImport(sourceSQLXE, invT0, invT0.Add(3*time.Hour))}, call)
	var read investigationRead
	in, _ := buildInvestigationInput(context.Background(), c, importedBody(), nil, investigate.AuditPresent, &read)
	if !in.SQLKinds["call"] || in.SQLKinds["blocked-process"] || in.SQLKinds["attention"] {
		t.Errorf("kinds = %v", in.SQLKinds)
	}
	if len(in.SQLEvents) != 1 || !in.SQLEvents[0].T.Equal(at) || in.SQLEvents[0].DurationMs == nil || *in.SQLEvents[0].DurationMs != d {
		t.Errorf("events = %+v", in.SQLEvents)
	}
}

func TestReadImportedEventsSQLCap(t *testing.T) {
	c := &stubTraceCase{
		imports: []casestore.ImportRecord{stubImport(sourceSQLXE, invT0, invT0.Add(3*time.Hour))},
		events: func(casestore.EventQuery) []evidence.Event {
			return make([]evidence.Event, casestore.MaxEventQuery)
		},
	}
	var read investigationRead
	in, _ := buildInvestigationInput(context.Background(), c, importedBody(), nil, investigate.AuditPresent, &read)
	if in.SQLPresent || len(read.Warnings) == 0 {
		t.Errorf("capped SQL read was used: %v %+v", in.SQLPresent, read)
	}
}

func TestReadImportedEventsHTTPErr(t *testing.T) {
	rows := []evidence.Event{
		importedEvent(1, sourceHTTPErr, "httperr", invT0.Add(time.Hour+time.Minute), map[string]string{"reason": "Timer_HeaderWait", "status": "408"}),
		importedEvent(2, sourceHTTPErr, "httperr", invT0.Add(time.Hour+2*time.Minute), map[string]string{"reason": "Timer_HeaderWait"}),
	}
	c := importedCase([]casestore.ImportRecord{stubImport(sourceHTTPErr, invT0, invT0.Add(3*time.Hour))}, rows...)
	var read investigationRead
	in, err := buildInvestigationInput(context.Background(), c, importedBody(), nil, investigate.AuditPresent, &read)
	if err != nil {
		t.Fatal(err)
	}
	if !in.HTTPErrPresent || len(in.HTTPErr) != 2 || in.HTTPErr[0].Reason != "Timer_HeaderWait" || in.HTTPErr[0].Status != "408" {
		t.Fatalf("HTTPERR = %v %+v", in.HTTPErrPresent, in.HTTPErr)
	}
	if in.SQLPresent {
		t.Errorf("SQL present from an HTTPERR file")
	}
}

func TestReadImportedEventsUnvalidatedIgnored(t *testing.T) {
	rec := stubImport(sourceSQLXE, invT0, invT0.Add(3*time.Hour))
	rec.TZRule = ""
	c := importedCase([]casestore.ImportRecord{rec}, importedEvent(1, sourceSQLXE, "deadlock", invT0.Add(time.Hour), nil))
	var read investigationRead
	in, _ := buildInvestigationInput(context.Background(), c, importedBody(), nil, investigate.AuditPresent, &read)
	if in.SQLPresent || len(c.queries) != 0 {
		t.Errorf("unvalidated sql-xe read: %v, %d queries", in.SQLPresent, len(c.queries))
	}
}

func TestReadImportedEventsIISStatus(t *testing.T) {
	e := iisEvent(1, invT0.Add(time.Minute), 120)
	e.Attrs["status"], e.Attrs["substatus"] = "404", "5"
	c := &stubTraceCase{
		imports: []casestore.ImportRecord{stubImport(investigationIISSource, invT0, invT0.Add(3*time.Hour))},
		events:  func(casestore.EventQuery) []evidence.Event { return []evidence.Event{e} },
	}
	var read investigationRead
	in, _ := buildInvestigationInput(context.Background(), c, importedBody(), nil, investigate.AuditPresent, &read)
	if len(in.IIS) != 1 || in.IIS[0].Status != "404" || in.IIS[0].SubStatus != "5" || !in.IISSubStatus {
		t.Errorf("IIS = %+v sub %v", in.IIS, in.IISSubStatus)
	}
}
