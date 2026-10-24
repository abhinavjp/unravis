package timeline

import (
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
)

// Fix 7: an audit interval can end hours after window.To. Every file here is
// synthetic: web-a, sql-a, 2001 dates.

// trailingCase builds a case whose only interval starts inside the window and
// ends 3 h after window.To, with one sql event starting evStart seconds after t0.
func trailingCase(t *testing.T, evStart int) (View, importer.Window) {
	t.Helper()
	c := newCase(t)
	w := importer.Window{From: at(0), To: at(1000)}
	end := w.To.Add(3 * time.Hour)
	load(t, c, web(ev{kind: "request", start: 500, dur: 50}))
	load(t, c, sql(ev{kind: "lock-wait", start: evStart, dur: 100}))
	iv := []join.Interval{{ID: "audit-1", Start: at(500), End: end}}
	return mustBuild(t, c, w, iv), w
}

// An event that starts after window.To plus the edge margin but still overlaps
// a trailing interval is read, and the absence wording is not printed.
func TestEventInsideTrailingIntervalIsListed(t *testing.T) {
	v, w := trailingCase(t, 1000+2*3600) // 2 h after window.To
	var found bool
	for _, r := range rowsOf(v, KindOverlap) {
		if r.Source == "sql-xe" && r.Join == evidence.JoinUnique {
			found = true
		}
	}
	if !found {
		t.Fatalf("the late event must be listed: %+v", v.Rows)
	}
	for _, r := range rowsOf(v, KindAbsence) {
		t.Fatalf("no absence row expected, got %q", r.Wording)
	}
	if v.Truncated {
		t.Fatal("the read must not be truncated")
	}
	if !v.Window.To.Equal(w.To) {
		t.Fatalf("the user-visible window must not widen: %v", v.Window)
	}
}

// An event that starts after the latest interval end plus the edge margin is not read.
func TestEventAfterLatestIntervalEndIsNotRead(t *testing.T) {
	v, _ := trailingCase(t, 1000+3*3600+int(edgeMargin/time.Second)+60)
	if over := rowsOf(v, KindOverlap); len(over) != 1 || over[0].Source != "iis-w3c" {
		t.Fatalf("only the web event overlaps: %+v", v.Rows)
	}
	abs := rowsOf(v, KindAbsence)
	if len(abs) != 1 || abs[0].Source != "sql-xe" {
		t.Fatalf("sql must show no event: %+v", v.Rows)
	}
	if v.Unlisted != 0 {
		t.Fatalf("the late event must not be read: unlisted %d", v.Unlisted)
	}
}
