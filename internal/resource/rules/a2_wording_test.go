package rules_test

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
)

// A 28 s stall with the SLOWLOG threshold stated: when the buffer coverage does
// not hold the whole interval the gap wording is shown, never the threshold
// wording.
func TestSlowlogWindowGapBeatsThresholdWording(t *testing.T) {
	const threshold = "commands slower than 10000 microseconds"
	other := span("redis-slowlog", "slow-command", "redis-a", hms(0, 50, 0), 20, "id", "1")
	for _, c := range []struct {
		name  string
		first *time.Time
		last  *time.Time
		want  string
	}{
		{"starts inside the stall", tp(hms(1, 2, 20)), tp(hms(1, 5, 0)),
			"Not observed (gap or sampling: the Redis slow-log window on redis-a starts at 2001-10-25T01:02:20Z, after this interval starts)."},
		{"ends inside the stall", tp(hms(0, 30, 0)), tp(hms(1, 2, 10)),
			"Not observed (gap or sampling: the Redis slow-log window on redis-a ends at 2001-10-25T01:02:10Z, before this interval ends)."},
		{"unknown", nil, nil,
			"Not observed (gap or sampling: the Redis slow-log window on redis-a is unknown)."},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := evidence.SourceQuality{Threshold: threshold, CoverageFirst: c.first, CoverageLast: c.last, TimezoneRule: clock.RuleUTCField}
			res := slowlogCase(t, q, other)
			got := absenceRow(t, res).Wording
			if got != c.want {
				t.Errorf("absence = %q\nwant      %q", got, c.want)
			}
			if strings.Contains(got, "only records") {
				t.Errorf("threshold wording shown over a coverage gap: %q", got)
			}
			if strings.Contains(got, "session") {
				t.Errorf("SLOWLOG wording reads as a session: %q", got)
			}
		})
	}
}

// Negative: a window that holds the whole 28 s stall, ids without a gap and a
// stated threshold give the threshold wording and none of the gap sentences.
func TestSlowlogHeldWindowGivesNoGapSentence(t *testing.T) {
	q := evidence.SourceQuality{Threshold: "commands slower than 10000 microseconds",
		CoverageFirst: tp(hms(1, 2, 3)), CoverageLast: tp(hms(1, 2, 31)), TimezoneRule: clock.RuleUTCField}
	res := slowlogCase(t, q, span("redis-slowlog", "slow-command", "redis-a", hms(0, 50, 0), 20, "id", "1"))
	got := absenceRow(t, res).Wording
	if !strings.HasPrefix(got, "Not observed in Redis SLOWLOG.") {
		t.Errorf("absence = %q", got)
	}
	for _, g := range res.Gaps {
		if strings.Contains(g, "slow-log window") || strings.Contains(g, "slow-log buffer") {
			t.Errorf("gap sentence for a window that holds the interval: %q", g)
		}
	}
}

func TestSlowlogOverlapReadsAsBuffer(t *testing.T) {
	h := newHarness(t)
	stallFixture(h)
	res := h.eval(hms(0, 40, 0), hms(1, 20, 0), stall)
	found := false
	for _, w := range wordings(res, "RC6") {
		if strings.Contains(w, "SLOWLOG session") {
			t.Errorf("SLOWLOG overlap reads as a session: %q", w)
		}
		if strings.HasPrefix(w, "The Redis SLOWLOG buffer held a slow command") {
			found = true
		}
	}
	if !found {
		t.Errorf("no buffer overlap row: %q", wordings(res, "RC6"))
	}
	// Other sources still read as a session.
	row(t, res, "RC5", "An ASP.NET Core console log session had a late heartbeat of 1.5 s overlapping this interval (Correlation).")
}

func TestNoSlowlogImportGap(t *testing.T) {
	h := newHarness(t)
	h.add(metric("redis-info", "redis-a", hms(1, 0, 0), 1, "count", "metric", "blocked_clients"))
	h.meta["redis-info"] = meta{host: "redis-a"}
	res := h.eval(hms(0, 40, 0), hms(1, 20, 0), stall)
	if !hasGap(res, "Not observed (gap or sampling: no Redis SLOWLOG import for redis-a).") {
		t.Errorf("no-import gap missing: %q", res.Gaps)
	}
}

// RC8 words a marker that starts shortly after the interval ends with
// MarkerAfter, and a marker beyond the lead time with no row.
func TestMarkerAfterEpisode(t *testing.T) {
	h := newHarness(t)
	h.add(point("win-events", "apppool-recycle", "web-a", hms(1, 3, 31), "reason", "Time"),
		point("win-events", "apppool-recycle", "web-a", hms(1, 30, 0), "reason", "Time"))
	h.meta["win-events"] = meta{host: "web-a"}
	res := h.eval(hms(0, 40, 0), hms(1, 40, 0), stall)
	r := row(t, res, "RC8", "This interval ends 1 min before a recycle event of the application pool (reason: Time) on web-a (Correlation).")
	if r.Label != audit.EvidenceCorrelation || len(r.Evidence) == 0 || r.Episode == nil {
		t.Errorf("marker after row = %+v", r)
	}
	n := 0
	for _, w := range wordings(res, "RC8") {
		if strings.HasPrefix(w, "This interval ends") {
			n++
		}
		if strings.HasPrefix(w, "This interval starts") {
			t.Errorf("a marker after the interval read as before it: %q", w)
		}
	}
	if n != 1 {
		t.Errorf("marker after rows = %d, want 1 (the later marker is beyond the lead time): %q", n, wordings(res, "RC8"))
	}
}

// A host with counters but no application log gets the app log gap; a host that
// has an application log does not.
func TestNoAppLogGap(t *testing.T) {
	h := newHarness(t)
	stallFixture(h)
	res := h.eval(hms(0, 40, 0), hms(1, 20, 0), stall)
	if hasGap(res, "Not observed (gap or sampling: no app log for web-a).") {
		t.Errorf("app log gap for a host that has app logs: %q", res.Gaps)
	}

	h = newHarness(t)
	every(hms(1, 0, 0), hms(1, 5, 0), 15*time.Second, func(t time.Time) {
		h.add(pdh("web-b", t, "Processor", "_Total", "% Processor Time", 20, "percent-host"))
	})
	h.meta["win-counters"] = meta{host: "web-b"}
	res = h.eval(hms(0, 40, 0), hms(1, 20, 0), stall)
	if !hasGap(res, "Not observed (gap or sampling: no app log for web-b).") {
		t.Errorf("app log gap missing: %q", res.Gaps)
	}
	if !hasGap(res, "Not observed (gap or sampling: no per-process counters for web-b).") {
		t.Errorf("per-process gap missing: %q", res.Gaps)
	}
}
