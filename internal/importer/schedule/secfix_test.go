package schedule

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
)

// secfix M1: a second-by-second cron repeated over many rows must hit the
// per-file work budget and fail fast with a plain LimitError.
func TestSecondCronRowsHitWorkBudget(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(hfHead)
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sb, "job-%05d,default,* * * * * *,UTC,,,\n", i)
	}
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-03-24T00:00:00Z"), To: utc("2001-03-24T00:00:01Z")}
	start := time.Now()
	_, _, err := parse(NewHangfire(), []byte(sb.String()), o)
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("took %v, want a fast refusal", d)
	}
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "work" {
		t.Fatalf("err = %v, want a work LimitError", err)
	}
	if strings.Contains(err.Error(), "job-") || strings.Contains(err.Error(), "* *") {
		t.Errorf("error echoes cell text: %v", err)
	}
}

// A file that already holds its share of boundaries cuts later schedules
// without testing wall times, so many chatty rows still import.
func TestFullFileCutsWithoutWork(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(hfHead)
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&sb, "job-%05d,default,* * * * * *,UTC,,,\n", i)
	}
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-03-24T00:00:00Z"), To: utc("2001-03-24T01:00:00Z")}
	// 3600 boundaries per row: the cap is reached on row 28, then rows are cut
	// at no cost. The work spent before that stays far under the budget.
	_, _, err := parse(NewHangfire(), []byte(sb.String()), o)
	if err != nil {
		t.Fatalf("a full file must be cut, not refused: %v", err)
	}
}

// Pruned spans must not change which boundaries a normal schedule emits.
func TestPrunedCronKeepsBoundaries(t *testing.T) {
	o := hangfireOpts()
	o.Window = importer.Window{From: utc("2001-03-24T10:00:00Z"), To: utc("2001-03-24T10:05:00Z")}
	ev, _ := mustParse(t, NewHangfire(), []byte(hfHead+"job-alpha,default,* * * * *,UTC,,,\n"), o)
	if n := len(ofKind(ev, KindBoundary)); n != 6 {
		t.Errorf("boundaries = %d, want 6 (10:00 to 10:05 inclusive)", n)
	}
}
