package schedule

import (
	"testing"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

// Bad fixtures plant the harness sentinel in a cell that a row rejects on, so
// the harness can check it never reaches an error, report, gap or attribute.
func hangfireBad() []byte {
	return []byte(hfHead +
		"job-alpha,default,0 * * * *,UTC,2001-03-24T05:00:00Z,,\n" +
		"job-beta,default," + importertest.Sentinel + " * * * *,UTC,,,\n" +
		"job-gamma,default,0 * * * *," + importertest.Sentinel + ",,,\n")
}

func agentBad() []byte {
	return []byte(agHead +
		agRow("job-alpha", "1", "4", "1", "1", "0", "0", "0", "20010101", "99991231", "30000", "235959") +
		agRow("job-beta", "1", "4", "1", "1", "0", "0", "0", importertest.Sentinel, "99991231", "30000", "235959") +
		agRow("job-gamma", "1", "4", "1", "1", "0", "0", "0", "20010101", "99991231", importertest.Sentinel, "235959"))
}

func hangfireFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:    fixture(t, "hangfire_good.csv"),
		Bad:     hangfireBad(),
		Options: importer.Options{Window: win()},
	}
}

func agentFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:    fixture(t, "agent_good.csv"),
		Bad:     agentBad(),
		Options: importer.Options{Window: win(), Host: &importer.HostHint{Label: "sql-a", Role: "sql", TZ: berlin(t)}},
	}
}

func TestHangfireConformance(t *testing.T) {
	importertest.Run(t, NewHangfire(), hangfireFixtures(t))
}

func TestAgentConformance(t *testing.T) {
	importertest.Run(t, NewAgent(), agentFixtures(t))
}

func FuzzHangfire(f *testing.F) {
	importertest.FuzzParse(f, NewHangfire(), hangfireFixtures(f))
}

func FuzzAgent(f *testing.F) {
	importertest.FuzzParse(f, NewAgent(), agentFixtures(f))
}
