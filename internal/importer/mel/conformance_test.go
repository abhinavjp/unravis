package mel

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

const sentinel = "SENTINEL-7f3a"

const stdoutName = "stdout_20010304050607_1234.log"

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func zone(t testing.TB, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("zone %s unavailable: %v", name, err)
	}
	return loc
}

func baseOpts(loc *time.Location) importer.Options {
	return importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "nonce-1",
		Host:         &importer.HostHint{Label: "web-a", Role: "app", TZ: loc, Runtime: "dotnet-8"},
		Pseudonymize: func(kind, v string) string { return kind + "-0001" },
	}
}

func fixtures(t testing.TB, good, bad, fileName string) importertest.Fixtures {
	o := baseOpts(time.UTC)
	o.FileName = fileName
	return importertest.Fixtures{
		Good: readFixture(t, good), Bad: readFixture(t, bad), Options: o, NoFreeText: true,
	}
}

func consoleFixtures(t testing.TB) importertest.Fixtures {
	return fixtures(t, "console_basic.log", "console_bad.log", "")
}

func jsonFixtures(t testing.TB) importertest.Fixtures {
	return fixtures(t, "json_basic.log", "json_bad.log", "")
}

func stdoutFixtures(t testing.TB) importertest.Fixtures {
	return fixtures(t, "stdout_basic.log", "stdout_bad.log", stdoutName)
}

func TestConformanceConsole(t *testing.T) { importertest.Run(t, NewConsole(), consoleFixtures(t)) }

func TestConformanceJSON(t *testing.T) { importertest.Run(t, NewJSON(), jsonFixtures(t)) }

func TestConformanceStdout(t *testing.T) { importertest.Run(t, NewStdout(), stdoutFixtures(t)) }

func FuzzParseConsole(f *testing.F) { importertest.FuzzParse(f, NewConsole(), consoleFixtures(f)) }

func FuzzParseJSON(f *testing.F) { importertest.FuzzParse(f, NewJSON(), jsonFixtures(f)) }

func FuzzParseStdout(f *testing.F) {
	fx := stdoutFixtures(f)
	f.Add(readFixture(f, "console_single.log"))
	importertest.FuzzParse(f, NewStdout(), fx)
}

// collect parses data with imp and returns the events, report and error.
func collect(t testing.TB, imp importer.Importer, data string, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), imp, bytes.NewReader([]byte(data)), o)
}
