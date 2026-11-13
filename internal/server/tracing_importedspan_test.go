package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// seedSpannedImport is seedTracingImport with a recorded coverage span on the run.
func seedSpannedImport(t *testing.T, store casestore.Store, caseID, importerName, file string, evs []evidence.Event, first, last *time.Time) casestore.ImportRecord {
	t.Helper()
	c, err := store.Open(context.Background(), casestore.CaseID(caseID))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	events := make([]evidence.Event, len(evs))
	copy(events, evs)
	for i := range events {
		line := int64(i + 2)
		events[i].Source = importerName
		events[i].Line = line
		events[i].EvidenceID = evidence.EventID(caseID, importerName, file, line, 0)
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: importerName, Quality: evidence.SourceQuality{CoverageFirst: first, CoverageLast: last}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for _, ev := range events {
			rep.Rows++
			if err := emit(ev); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
	meta := casestore.ImportMeta{Importer: importerName, FileName: file, HostLabel: "web-a", TZRule: clock.RuleUTCField,
		AckGating: true, AckText: "ack"}
	res, err := c.Import(context.Background(), meta, strings.NewReader("body "+file), run)
	if err != nil {
		t.Fatal(err)
	}
	return res.Record
}

// tracingEventsAt is tracingEvents with the start time as a parameter.
func tracingEventsAt(base time.Time, n int) []evidence.Event {
	out := make([]evidence.Event, n)
	for i := range out {
		v := float64(i)
		out[i] = evidence.Event{Kind: "cpu", Host: "web-a", Start: base.Add(time.Duration(i) * time.Second),
			Value: &v, Unit: "percent", TZRule: "utc-field"}
		out[i].End = out[i].Start.Add(time.Second)
	}
	return out
}

// The owner imported an IIS day that ends before the incident. The lane must say
// which times the files hold, so the empty lane is not read as "nothing happened".
func TestTracingLaneSaysWhatImportedFilesCover(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		first := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		last := time.Date(2001, 1, 1, 1, 0, 0, 0, time.UTC)
		evs := tracingEventsAt(first.Add(time.Minute), 2) // events inside the span, outside the window
		imp := seedSpannedImport(t, e.store, id, "iis-w3c", "u_ex010101.log", evs, &first, &last)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "u_ex010101.log", ImportID: imp.ID})

		res := e.json(http.MethodPost, tracingPath(id), `{`+tracingWindowBody+`}`)
		var out tracingResponse
		decodeBody(t, res, &out)
		l := tracingLaneOf(t, out, "iis")
		if l.State != calltrace.StateOutOfRange {
			t.Fatalf("iis state = %q, want out-of-range", l.State)
		}
		if l.ImportedFrom == nil || !l.ImportedFrom.Equal(first) || l.ImportedTo == nil || !l.ImportedTo.Equal(last) {
			t.Errorf("imported span = %v..%v, want %v..%v", l.ImportedFrom, l.ImportedTo, first, last)
		}
	})
}

// A file with no recorded span sends no range at all.
func TestTracingLaneSendsNoSpanWhenAFileHasNone(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		imp := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", tracingEvents(2))
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		var out tracingResponse
		decodeBody(t, res, &out)
		if l := tracingLaneOf(t, out, "iis"); l.ImportedFrom != nil || l.ImportedTo != nil {
			t.Errorf("span sent without a recorded range: %v..%v", l.ImportedFrom, l.ImportedTo)
		}
	})
}

func TestImportedSpanAdd(t *testing.T) {
	d := func(h int) *time.Time { v := time.Date(2001, 1, 1, h, 0, 0, 0, time.UTC); return &v }
	var s importedSpan
	s.add(d(2), d(4))
	s.add(d(1), d(3))
	s.add(d(3), d(6))
	if !s.known || !s.from.Equal(*d(1)) || !s.to.Equal(*d(6)) {
		t.Fatalf("widened span = %v..%v known=%v, want 01:00..06:00 known", s.from, s.to, s.known)
	}
	s.add(d(1), nil)
	if !s.known || !s.partial || !s.from.Equal(*d(1)) || !s.to.Equal(*d(6)) {
		t.Fatalf("a file with no end must mark partial and keep the range: %v..%v known=%v partial=%v", s.from, s.to, s.known, s.partial)
	}
	s.add(d(0), d(9))
	if !s.known || !s.from.Equal(*d(0)) || !s.to.Equal(*d(9)) {
		t.Fatalf("a ranged file after a partial one must still widen: %v..%v known=%v", s.from, s.to, s.known)
	}
	var first importedSpan
	first.add(nil, d(1))
	if first.known || !first.partial {
		t.Fatal("a nil first file must leave the span unknown and partial")
	}
	first.add(d(1), d(2))
	if !first.known || !first.partial || !first.from.Equal(*d(1)) || !first.to.Equal(*d(2)) {
		t.Fatalf("a ranged file after a nil one must set the range: %v..%v known=%v partial=%v", first.from, first.to, first.known, first.partial)
	}
}

// One ranged file and one file with no range: the range is sent and the lane says it is partial.
func TestImportedSpanMixedFiles(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		first := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		last := time.Date(2001, 1, 1, 1, 0, 0, 0, time.UTC)
		evs := tracingEventsAt(first.Add(time.Minute), 2)
		imp := seedSpannedImport(t, e.store, id, "iis-w3c", "u_ex010101.log", evs, &first, &last)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "u_ex010101.log", ImportID: imp.ID})
		imp2 := seedSpannedImport(t, e.store, id, "iis-w3c", "u_ex010102.log", tracingEventsAt(first.Add(2*time.Minute), 2), nil, nil)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "u_ex010102.log", ImportID: imp2.ID})

		res := e.json(http.MethodPost, tracingPath(id), `{`+tracingWindowBody+`}`)
		if !strings.Contains(res.Body.String(), `"importedPartial":true`) {
			t.Errorf("lane JSON lacks importedPartial:true: %s", res.Body.String())
		}
		var out tracingResponse
		decodeBody(t, res, &out)
		l := tracingLaneOf(t, out, "iis")
		if l.ImportedFrom == nil || !l.ImportedFrom.Equal(first) || l.ImportedTo == nil || !l.ImportedTo.Equal(last) {
			t.Errorf("imported span = %v..%v, want %v..%v", l.ImportedFrom, l.ImportedTo, first, last)
		}
		if !l.ImportedPartial {
			t.Error("lane not marked partial")
		}
	})
}
