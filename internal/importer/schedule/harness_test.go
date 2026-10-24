package schedule

import (
	"fmt"
	"strings"
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

// agentLarge is a valid history file over twice the head size in which every
// row is a distinct run. The harness would otherwise repeat the Good file's
// rows, and the importer counts a repeated run once.
func agentLarge() []byte {
	var sb strings.Builder
	sb.WriteString("job_name,run_date,run_time,run_duration\n")
	for i := 0; i < 6000; i++ {
		fmt.Fprintf(&sb, "job-%05d,20010324,100000,500\n", i)
	}
	return []byte(sb.String())
}

func agentFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:    fixture(t, "agent_good.csv"),
		Large:   agentLarge(),
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
