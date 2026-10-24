package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Tests for fix6: unknown markers count against the file cap with boundaries,
// and the schedule importers enforce Limits.MaxEvents. Fixtures are synthetic.

// monthlyDay31CSV builds rows monthly schedules that each run on day 31, one
// distinct synthetic job per row.
func monthlyDay31CSV(rows int) []byte {
	var sb strings.Builder
	sb.WriteString(agHead)
	for i := 0; i < rows; i++ {
		// freq 16 = monthly, interval 31 = day of month, subday 1 = once a day at 12:00.
		sb.WriteString(agRow(fmt.Sprintf("job-%05d", i), "1", "16", "31", "1", "0", "0", "1",
			"20010101", "99991231", "120000", "235959"))
	}
	return []byte(sb.String())
}

func TestMonthlyDay31RowsStayWithinFileCap(t *testing.T) {
	o := agentWindow(t, "2001-01-01T00:00:00Z", "2002-01-01T00:00:00Z")
	ev, rep, err := parse(NewAgent(), monthlyDay31CSV(20_000), o)
	if err != nil {
		t.Fatalf("a chatty file must be cut, not refused: %v", err)
	}
	// Boundaries and unknown markers together never pass the cap.
	if len(ev) > maxBoundaries {
		t.Fatalf("events = %d, cap is %d", len(ev), maxBoundaries)
	}
	if len(ev) != maxBoundaries {
		t.Errorf("events = %d, want the file cap %d: 20000 rows overflow it", len(ev), maxBoundaries)
	}
	for _, e := range ev {
		if e.Kind != KindBoundary && e.Kind != KindUnknown {
			t.Fatalf("unexpected kind %q", e.Kind)
		}
	}
	if !hasGap(rep, gapTruncated) || !hasGap(rep, gapEventCap) {
		t.Errorf("gaps = %v, want the truncation and event cap sentences", rep.KnownGaps)
	}
}

func TestUnknownMarkersHitTheFileCap(t *testing.T) {
	var emitted int
	r := newRun(context.Background(), NameAgent, agentOpts(t), func(evidence.Event) error {
		emitted++
		return nil
	})
	// Boundaries stop one slot short, so the last slot is left for a truncation marker.
	r.nCapped = maxBoundaries - 1
	if err := r.reserve(false); !errors.Is(err, errBoundaryCap) {
		t.Fatalf("ordinary event at cap-1: err = %v, want errBoundaryCap", err)
	}
	if err := r.reserve(true); err != nil {
		t.Fatalf("truncation marker in the last slot: %v", err)
	}
	if err := r.reserve(true); !errors.Is(err, errBoundaryCap) {
		t.Fatalf("event past the cap: err = %v, want errBoundaryCap", err)
	}
	// A marker past the cap is dropped, not an error, and the file says so.
	before := emitted
	if err := r.sendUnknownSpan(1, 0, "job-x", reasonWrap, "", r.from, r.to); err != nil {
		t.Fatalf("marker past the cap: %v", err)
	}
	if emitted != before || r.nCapped != maxBoundaries {
		t.Fatalf("emitted %d more, nCapped %d: the cap was passed", emitted-before, r.nCapped)
	}
	if !r.gaps[gapEventCap] {
		t.Error("event cap sentence not recorded")
	}
}

func TestEventCapSentencePassesLintCausal(t *testing.T) {
	if err := evidence.LintCausal(gapEventCap); err != nil {
		t.Errorf("%q: %v", gapEventCap, err)
	}
}

func TestMaxEventsIsEnforcedBySchedule(t *testing.T) {
	// Three hourly jobs over the three-day window.
	csv := hfHead +
		"job-a,default,0 * * * *,UTC,,,\n" +
		"job-b,default,0 * * * *,UTC,,,\n" +
		"job-c,default,0 * * * *,UTC,,,\n"

	all, _, err := parse(NewHangfire(), []byte(csv), hangfireOpts())
	if err != nil || len(all) == 0 {
		t.Fatalf("unlimited parse: %d events, err %v", len(all), err)
	}
	n := int64(len(all))

	o := hangfireOpts()
	o.Limits = importer.Limits{MaxEvents: n - 1}
	ev, _, err := parse(NewHangfire(), []byte(csv), o)
	wantLimit(t, err, "events")
	if !errors.Is(err, importer.ErrLimit) {
		t.Errorf("err = %v, want it to match importer.ErrLimit", err)
	}
	if ev != nil {
		t.Errorf("a refused file returns no events, got %d", len(ev))
	}

	o.Limits = importer.Limits{MaxEvents: n}
	ev, _, err = parse(NewHangfire(), []byte(csv), o)
	if err != nil {
		t.Fatalf("exactly MaxEvents must pass: %v", err)
	}
	if int64(len(ev)) != n {
		t.Errorf("events = %d, want %d", len(ev), n)
	}
}
