package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

var _ CoverageProber = (*demoRepository)(nil)

func TestDemoCoverageReportsAccountIdUnavailable(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	repo := NewDemo(now)
	prober, ok := repo.(CoverageProber)
	if !ok {
		t.Fatal("demo repository is not a CoverageProber")
	}
	report, err := prober.Coverage(context.Background(), CoverageInput{SourceID: "demo-west", Start: now.Add(-time.Hour), End: now})
	if err != nil {
		t.Fatal(err)
	}
	if want := "tenant attribution unavailable (0.0 % populated)"; report.TenantAttribution != want {
		t.Fatalf("tenantAttribution = %q, want %q", report.TenantAttribution, want)
	}
	if report.SourceID != "demo-west" || len(report.Rows) == 0 {
		t.Fatalf("report = %#v", report)
	}
	// 3 APIs x 125 samples x 2 windows, all within the hour.
	if report.Total.Kind != "all" || report.Total.Day != "" || report.Total.Docs != 750 {
		t.Fatalf("total = %#v", report.Total)
	}
	if report.Total.AccountID.NonNull != 0 || report.Total.AccountID.Pct != 0 {
		t.Fatalf("accountId = %#v", report.Total.AccountID)
	}
	if report.Total.RequestInitiatedAt.Pct != 100 || report.Total.ApiTime.Pct != 100 || report.Total.CreatedOn.Pct != 100 {
		t.Fatalf("total coverage = %#v", report.Total)
	}
	var sum int64
	for _, r := range report.Rows {
		sum += r.Docs
		if r.Kind != "synchronous" {
			t.Fatalf("kind = %q", r.Kind)
		}
	}
	if sum != report.Total.Docs {
		t.Fatalf("rows sum %d != total %d", sum, report.Total.Docs)
	}
	if len(report.Provenance) != 1 {
		t.Fatalf("provenance = %#v", report.Provenance)
	}
	p := report.Provenance[0]
	if p.Window != "coverage" || p.ScanClass != ScanBroad || p.Completeness != Complete || p.SourceID != "demo-west" {
		t.Fatalf("provenance = %#v", p)
	}
	if p.DocsExamined == nil || *p.DocsExamined != 750 {
		t.Fatalf("docsExamined = %v, want the source's 750 records", p.DocsExamined)
	}
}

func TestDemoCoverageSplitsByDayAndRejectsBadInput(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 10, 0, 0, time.UTC)
	repo := NewDemo(now).(CoverageProber)
	// The demo spans the half hour before 00:10, so it crosses midnight UTC.
	report, err := repo.Coverage(context.Background(), CoverageInput{SourceID: "demo-east", Start: now.Add(-36 * time.Hour), End: now})
	if err != nil {
		t.Fatal(err)
	}
	days := map[string]int64{}
	for _, r := range report.Rows {
		days[r.Day] += r.Docs
	}
	if len(days) != 2 || days["2026-10-23"] == 0 || days["2026-10-24"] == 0 {
		t.Fatalf("days = %v", days)
	}
	if _, err := repo.Coverage(context.Background(), CoverageInput{SourceID: "demo-east", Start: now, End: now}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := repo.Coverage(context.Background(), CoverageInput{SourceID: "nope", Start: now.Add(-time.Hour), End: now}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := repo.Coverage(context.Background(), CoverageInput{SourceID: "demo-east", Start: now.Add(-time.Hour), End: now.Add(-50 * time.Minute)}); err != nil {
		t.Fatalf("empty window err = %v", err)
	}
}
