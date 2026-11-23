package keeper

import (
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// The keeper GETs only a fixed list of paths from a Blackbox. The SQL sample files are one more path,
// and only by their exact hourly name: nothing else under /sql/ is reachable through the keeper.
var testHour = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func TestKeeperAllowsOnlyTheSQLSampleFilePath(t *testing.T) {
	name := blackbox.SQLFileNameFor(testHour)
	for _, p := range []string{"/manifest", "/status", "/diag", "/files/" + blackbox.FileNameFor(testHour), "/sql/files/" + name} {
		if !bbPathAllowed(p) {
			t.Errorf("%s must be allowed", p)
		}
	}
	for _, p := range []string{
		"", "/sql", "/sql/", "/sql/files", "/sql/files/", "/sql/files/state.json", "/sql/files/status.json", "/sql/state.json", "/sql/" + name,
		"/sql/files/../manifest", "/sql/files/" + name + "/x", "/sql/files/" + name + "?x=1", "/files/" + name, "/sql/files/" + blackbox.FileNameFor(testHour),
		"//sql/files/" + name, "/SQL/files/" + name,
	} {
		if bbPathAllowed(p) {
			t.Errorf("%q must not be allowed", p)
		}
	}
}
