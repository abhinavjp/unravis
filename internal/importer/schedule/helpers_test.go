package schedule

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const sentinel = "SENTINEL-7f3a"

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func berlin(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// win is the standard test window: three days from 2001-03-24 00:00 UTC.
func win() importer.Window {
	return importer.Window{From: utc("2001-03-24T00:00:00Z"), To: utc("2001-03-27T00:00:00Z")}
}

func hangfireOpts() importer.Options {
	return importer.Options{CaseID: "casecase8888", ImportNonce: "nonce-1", FileName: "export.csv", Window: win()}
}

func agentOpts(t testing.TB) importer.Options {
	o := hangfireOpts()
	o.Host = &importer.HostHint{Label: "sql-a", Role: "sql", TZ: berlin(t)}
	return o
}

// parse runs Parse and returns events, the report and the error.
func parse(imp importer.Importer, data []byte, opts importer.Options) ([]evidence.Event, importer.Report, error) {
	return importer.Collect(context.Background(), imp, bytes.NewReader(data), opts)
}

func mustParse(t testing.TB, imp importer.Importer, data []byte, opts importer.Options) ([]evidence.Event, importer.Report) {
	t.Helper()
	ev, rep, err := parse(imp, data, opts)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, e := range ev {
		if err := e.Validate(imp.AllowedAttrs()); err != nil {
			t.Fatalf("event invalid: %v", err)
		}
	}
	return ev, rep
}

func ofKind(ev []evidence.Event, kind string) []evidence.Event {
	var out []evidence.Event
	for _, e := range ev {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func forJob(ev []evidence.Event, kind, job string) []evidence.Event {
	var out []evidence.Event
	for _, e := range ev {
		if e.Kind == kind && e.Attrs["job"] == job {
			out = append(out, e)
		}
	}
	return out
}

func starts(ev []evidence.Event) []string {
	var out []string
	for _, e := range ev {
		out = append(out, e.Start.Format(time.RFC3339))
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func wantQuarantine(t testing.TB, err error, reason importer.Reason, column string) {
	t.Helper()
	var q *importer.QuarantineError
	if !errors.As(err, &q) {
		t.Fatalf("want quarantine %s, got %v", reason, err)
	}
	if q.Reason != reason || q.Column != column {
		t.Fatalf("quarantine = %s/%q, want %s/%q", q.Reason, q.Column, reason, column)
	}
}

func hasReason(rep importer.Report, line int64, reason importer.Reason) bool {
	for _, e := range rep.Errors {
		if e.Line == line && e.Reason == reason {
			return true
		}
	}
	return false
}

// lenient lets a test keep rejected rows without tripping the reject fraction.
func lenient(o importer.Options) importer.Options {
	o.Limits.MaxRejectFraction = 1
	return o
}
