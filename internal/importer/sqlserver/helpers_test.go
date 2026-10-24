package sqlserver

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
)

// Synthetic only: host sql-a, Pacific/Auckland, 2001 dates.

var goldenTol = join.Tolerance{Resolution: 10 * time.Millisecond, Skew: 2 * time.Second, Method: time.Second}

func auckland(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func sqlA(t testing.TB) *importer.HostHint {
	return &importer.HostHint{Label: "sql-a", Role: "sql", TZ: auckland(t), LogicalCores: 8, Runtime: "sql-2019"}
}

func testOpts(t testing.TB) importer.Options {
	return importer.Options{CaseID: "abcdefghijklmnopqrstuvwxyz", ImportNonce: "nonce-1", FileName: "fixture", Host: sqlA(t)}
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func collect(t testing.TB, imp importer.Importer, b []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	if o.Limits == (importer.Limits{}) {
		o.Limits = imp.DefaultLimits()
	}
	return importer.Collect(context.Background(), imp, bytes.NewReader(b), o)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
