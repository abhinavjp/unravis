package mongodlog

import (
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

// fixtures are the synthetic files the F0 harness runs. Good carries free text (the ns
// cell) into an attribute, so the harness derives its own formula cells from it.
func fixtures(t testing.TB) importertest.Fixtures {
	t.Helper()
	return importertest.Fixtures{
		Good: readFixture(t, "good.log"),
		Bad:  readFixture(t, "bad.log"),
		Options: importer.Options{
			CaseID: "caseabcdefgh", ImportNonce: "nonce-1", FileName: "mongod.log",
			Hosts: hostsOpts().Hosts,
		},
	}
}

func TestConformance(t *testing.T) {
	importertest.Run(t, New(), fixtures(t))
}

func FuzzParse(f *testing.F) {
	importertest.FuzzParse(f, New(), fixtures(f))
}

// The card caps (2 GiB, 10M rows, 256 KiB per line) are ceilings: a caller limit can lower
// them and never raise them.
func TestCapsAreCeilings(t *testing.T) {
	eff := effectiveLimits(importer.Limits{MaxBytes: 1 << 50, MaxRows: 1 << 50, MaxLineBytes: 1 << 30})
	if eff.MaxBytes != 2<<30 || eff.MaxRows != 10_000_000 || eff.MaxLineBytes != 256<<10 {
		t.Fatalf("caps raised: %+v", eff)
	}
	eff = effectiveLimits(importer.Limits{MaxBytes: 1000, MaxRows: 7, MaxLineBytes: 99})
	if eff.MaxBytes != 1000 || eff.MaxRows != 7 || eff.MaxLineBytes != 99 {
		t.Fatalf("caps not lowered: %+v", eff)
	}
}

// A line that is not strict UTF-8 is one rejected row (reason encoding); the rest is read.
func TestInvalidUTF8LineIsOneRejectedRow(t *testing.T) {
	lines := manyGood(40)
	lines[10] = "\xff\xfe not text"
	evs, rep, err := parse(t, logData(lines...), opts())
	if err != nil || len(evs) != 39 || rep.Rows != 40 || rep.Rejected != 1 || rep.Emitted != 39 {
		t.Fatalf("%v events %d report %+v", err, len(evs), rep)
	}
	if len(rep.Errors) != 1 || rep.Errors[0].Reason != importer.ReasonEncoding || rep.Errors[0].Line != 11 {
		t.Fatalf("errors: %+v", rep.Errors)
	}
}

// A line at exactly 256 KiB parses; one byte more refuses the file and nothing after it is
// emitted.
func TestLineCapAt256KiB(t *testing.T) {
	prefix := logLineOf("2001-09-09T01:00:05.000+00:00", idSlowQuery, "Slow query", `{"durationMillis":1,"x":""}`)
	pad := maxLineLen - len(prefix)
	mk := func(extra int) string {
		return logLineOf("2001-09-09T01:00:05.000+00:00", idSlowQuery, "Slow query",
			`{"durationMillis":1,"x":"`+strings.Repeat("a", pad+extra)+`"}`)
	}
	if ok := mk(0); len(ok) != maxLineLen {
		t.Fatalf("test line is %d bytes", len(ok))
	}
	if evs, _, err := parse(t, logData(mk(0)), opts()); err != nil || len(evs) != 1 {
		t.Fatalf("at limit: %v", err)
	}
	lines := append(manyGood(2), mk(1))
	lines = append(lines, manyGood(5)...)
	evs, _, err := parse(t, logData(lines...), opts())
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "line-bytes" || !errors.Is(err, importer.ErrLimit) {
		t.Fatalf("over limit: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("%d events returned with an error", len(evs))
	}
}
