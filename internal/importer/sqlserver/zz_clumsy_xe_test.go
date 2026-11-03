package sqlserver

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
)

// Clumsy-user and hostile-author checks for the Phase 06 XE kinds
// (call, blocked-process, attention).

func clumsyOpts(t testing.TB) importer.Options {
	o := xeHostOpts(t)
	o.Limits = NewXE().DefaultLimits()
	o.Limits.MaxRejectFraction = 1
	return o
}

// Header only: no events, no error, no coverage invented.
func TestClumsyXEHeaderOnly(t *testing.T) {
	evs, rep, err := collect(t, NewXE(), []byte("timestamp_utc,name,duration_us\r\n"), testOpts(t))
	if err != nil || len(evs) != 0 || rep.Emitted != 0 {
		t.Fatalf("events %d err %v", len(evs), err)
	}
	if rep.Quality.CoverageFirst != nil || rep.Quality.CoverageLast != nil {
		t.Errorf("coverage invented for a header-only file: %+v", rep.Quality)
	}
}

func TestClumsyXEEmptyFile(t *testing.T) {
	for _, in := range []string{"", "\n", "\r\n\r\n"} {
		evs, _, err := collect(t, NewXE(), []byte(in), testOpts(t))
		if err == nil && len(evs) != 0 {
			t.Errorf("%q gave %d events", in, len(evs))
		}
	}
}

// Hostile duration cells on completed-call rows: each is rejected with
// bad-number, none reaches an event, and a good row next to them survives.
func TestClumsyXECallDurations(t *testing.T) {
	bad := []string{"-1", "-0.0001", "abc", "NaN", "nan", "Inf", "+Inf", "-Inf", "Infinity", "1e400", "1e30",
		"604800000001", "0x1p80", "1,5", "12 ms", "=1+1"}
	for _, d := range bad {
		csv := "timestamp_utc,name,duration_us\n" +
			"2001-06-15 02:00:10,rpc_completed," + fmt.Sprintf("%q", d) + "\n" +
			"2001-06-15 02:00:11,rpc_completed,1000\n"
		evs, rep, err := collect(t, NewXE(), []byte(csv), clumsyOpts(t))
		if err != nil {
			t.Errorf("%q: err %v", d, err)
			continue
		}
		if len(evs) != 1 || rep.Rejected != 1 {
			t.Errorf("%q: events %d rejected %d, want 1 and 1", d, len(evs), rep.Rejected)
		}
	}
	// Edge values that are legal.
	good := map[string]float64{"0": 0, "604800000000": 604800000, "1e3": 1, "0.5": 0.0005}
	for d, wantMs := range good {
		csv := "timestamp_utc,name,duration_us\n2001-06-15 02:00:10,rpc_completed," + d + "\n"
		evs, _, err := collect(t, NewXE(), []byte(csv), clumsyOpts(t))
		if err != nil || len(evs) != 1 || evs[0].DurationMs == nil || *evs[0].DurationMs != wantMs {
			t.Errorf("%q: events %d err %v", d, len(evs), err)
			continue
		}
		if evs[0].Start.After(evs[0].End) {
			t.Errorf("%q: start after end", d)
		}
	}
}

// A call row with only the ms-style column (or none) keeps no duration: the
// importer never reads a "duration" column as microseconds, so a wrong unit
// cannot make a call 1000x slow.
func TestClumsyXECallWrongUnitColumn(t *testing.T) {
	csv := "timestamp_utc,name,duration_ms\n2001-06-15 02:00:10,rpc_completed,2500000\n"
	evs, _, err := collect(t, NewXE(), []byte(csv), clumsyOpts(t))
	if err != nil || len(evs) != 1 || evs[0].DurationMs != nil || !evs[0].Start.Equal(evs[0].End) {
		t.Fatalf("events %d err %v ev %+v", len(evs), err, evs)
	}
	// A wait with only the us column keeps no duration either.
	csv = "timestamp_utc,name,wait_type,duration_us\n2001-06-15 02:00:10,wait_info,LCK_M_X,2500000\n"
	evs, _, err = collect(t, NewXE(), []byte(csv), clumsyOpts(t))
	if err != nil || len(evs) != 1 || evs[0].DurationMs != nil {
		t.Fatalf("wait: events %d err %v", len(evs), err)
	}
}

// Report / XML columns still quarantine the file, whatever the kind of row and
// however the header is dressed.
func TestClumsyXEReportColumnsQuarantine(t *testing.T) {
	heads := []string{
		"timestamp_utc,name,blocked_process_report",
		"timestamp_utc,name,Blocked Process Report",
		"timestamp_utc,name,xml_report",
		"timestamp_utc,name,event_data",
		"timestamp_utc,name,deadlock_graph_XML",
		"timestamp_utc,name,duration_us,\"sql text\"",
		"timestamp_utc,name,duration_us,batch_text",
		"timestamp_utc,name,duration_us,Statement",
		"timestamp_utc,name,client_app_name",
		"timestamp_utc,name,database_name",
		"timestamp_utc,name,Login",
		"timestamp_utc,name,message",
	}
	for _, h := range heads {
		csv := h + "\n2001-06-15 02:00:10,blocked_process_report,<x/>\n"
		evs, _, err := collect(t, NewXE(), []byte(csv), clumsyOpts(t))
		var q *importer.QuarantineError
		if !errors.As(err, &q) || q.Reason != importer.ReasonUnscrubbedColumn || len(evs) != 0 {
			t.Errorf("%q: err %v events %d", h, err, len(evs))
		}
	}
}

// Blocked-process and attention rows that carry text in columns the importer
// does read or ignore: no text reaches an event, and nothing but the fixed
// event word is kept.
func TestClumsyXEBlockedAttentionCarryText(t *testing.T) {
	csv := "timestamp_utc,name,wait_type,component,state,duration_us,duration_ms,error_number,severity\n" +
		"2001-06-15 02:00:10,blocked_process_report,SELECT secret FROM t,free text here,drop table,1000,5,abc,99\n" +
		"2001-06-15 02:00:11,attention,LCK_M_X,x,y,1000,5,12,13\n"
	evs, _, err := collect(t, NewXE(), []byte(csv), clumsyOpts(t))
	if err != nil || len(evs) != 2 {
		t.Fatalf("events %d err %v", len(evs), err)
	}
	for i, e := range evs {
		if e.DurationMs != nil || e.Value != nil || len(e.Attrs) != 1 || e.Attrs["event"] == "" {
			t.Errorf("event %d keeps more than the event word: %+v", i, e)
		}
		if !e.Start.Equal(e.End) {
			t.Errorf("event %d has a span: %v..%v", i, e.Start, e.End)
		}
	}
	// The event-name cell itself carrying text is rejected, not stored.
	csv = "timestamp_utc,name\n2001-06-15 02:00:10,blocked_process_report <process id=1/>\n"
	evs, rep, err := collect(t, NewXE(), []byte(csv), clumsyOpts(t))
	if err != nil || len(evs) != 0 || rep.Rejected != 1 {
		t.Errorf("event name with markup: events %d rejected %d err %v", len(evs), rep.Rejected, err)
	}
}

// Time zone offsets, future and out-of-order timestamps, duplicate rows.
func TestClumsyXETimes(t *testing.T) {
	csv := "timestamp_utc,name,duration_us\n" +
		"2001-06-15 02:00:10+05:30,rpc_completed,1000\n" + // non-zero offset in a UTC field: rejected
		"2001-06-15T02:00:10-07:00,rpc_completed,1000\n" + // rejected
		"2001-06-15T02:00:10+00:00,rpc_completed,1000\n" + // zero offset: fine
		"2001-06-15T02:00:10Z,rpc_completed,1000\n" +
		"2099-01-01 00:00:00,rpc_completed,1000\n" + // far future: kept, coverage widens
		"1999-01-01 00:00:00,rpc_completed,1000\n" + // out of order, earlier
		"2001-06-15 02:00:10,rpc_completed,1000\n" + // duplicate of an earlier row
		"2001-06-15 02:00:10,rpc_completed,1000\n" +
		"0001-01-01 00:00:00,rpc_completed,1000\n" + // the unset time: rejected, never aborts the import
		"0001-01-08 00:00:00,rpc_completed,604800000000\n" + // derived start lands on the unset time: rejected
		"9999-12-31 23:59:59,rpc_completed,1000\n"
	evs, rep, err := collect(t, NewXE(), []byte(csv), clumsyOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 4 {
		t.Errorf("rejected %d, want 4 (two offset rows, two unset-time rows)", rep.Rejected)
	}
	seen := map[string]bool{}
	for _, e := range evs {
		if seen[e.EvidenceID] {
			t.Errorf("duplicate evidence id %s for duplicate rows", e.EvidenceID)
		}
		seen[e.EvidenceID] = true
		if e.Start.After(e.End) {
			t.Errorf("start after end: %+v", e)
		}
	}
	if rep.Quality.CoverageFirst == nil || rep.Quality.CoverageLast == nil ||
		rep.Quality.CoverageFirst.After(*rep.Quality.CoverageLast) {
		t.Errorf("coverage %+v", rep.Quality)
	}
	if !rep.Quality.CoverageLast.Equal(time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)) {
		t.Errorf("coverage last %v", rep.Quality.CoverageLast)
	}
}

// Ragged rows, a lone quote, a NUL byte and a giant cell must not panic or hang.
func TestClumsyXERaggedRows(t *testing.T) {
	big := make([]byte, 100<<10)
	for i := range big {
		big[i] = 'a'
	}
	csv := "timestamp_utc,name,duration_us\n" +
		"2001-06-15 02:00:10\n" +
		"2001-06-15 02:00:10,rpc_completed\n" +
		"2001-06-15 02:00:10,rpc_completed,1,2,3\n" +
		"\"2001-06-15 02:00:10,rpc_completed,1\n" +
		"2001-06-15 02:00:10,rpc_completed,\x00\n" +
		"2001-06-15 02:00:10,rpc_completed," + string(big) + "\n" +
		"2001-06-15 02:00:12,rpc_completed,1000\n"
	_, _, err := collect(t, NewXE(), []byte(csv), clumsyOpts(t))
	_ = err // an error is fine; a panic or hang is not
}
