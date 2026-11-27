package server

import (
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// The status screen shows "used of N GB" and the SQL collector's own state, so the facts carry them from the manifest and the recorder's status page.
func TestBlackboxFactsCarrySizeAndSQL(t *testing.T) {
	last := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	rd := &blackbox.Reading{
		Manifest: blackbox.Manifest{Name: "web-a", IntervalSeconds: 10, Files: []blackbox.FileEntry{{Bytes: 1000, Samples: 5}, {Bytes: 2500, Samples: 7}}},
		Status:   &blackbox.StatusDoc{MaxGB: 5, KeepDays: 14, SQL: blackbox.SQLStatus{Enabled: true, State: "on", LastOK: &last, Failures: 1}},
	}
	f := (&blackboxHub{}).facts(rd, nil)
	if f == nil || f.Bytes != 3500 || f.MaxGB != 5 || f.KeepDays != 14 {
		t.Fatalf("size facts wrong: %+v", f)
	}
	if f.SQL == nil || !f.SQL.Enabled || f.SQL.State != "on" || f.SQL.Failures != 1 || f.SQL.LastOK == nil {
		t.Fatalf("sql facts wrong: %+v", f.SQL)
	}

	// A recorder that does not report SQL (older, or a folder read with no status page) leaves the block out rather than showing "off".
	rd.Status = &blackbox.StatusDoc{}
	if g := (&blackboxHub{}).facts(rd, nil); g.SQL != nil || g.Bytes != 3500 {
		t.Fatalf("no sql report must stay absent, got %+v", g)
	}
	rd.Status = nil
	if g := (&blackboxHub{}).facts(rd, nil); g.SQL != nil || g.MaxGB != 0 {
		t.Fatalf("no status page, no cap: %+v", g)
	}
}
