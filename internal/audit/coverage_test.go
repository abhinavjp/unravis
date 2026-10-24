package audit

import (
	"errors"
	"testing"
	"time"
)

func TestTenantAttribution(t *testing.T) {
	cases := []struct {
		nonNull, docs int64
		want          string
	}{
		{80, 100, "available"},
		{79, 100, "tenant attribution unavailable (79.0 % populated)"},
		{0, 0, "tenant attribution unavailable (0.0 % populated)"},
	}
	for _, tc := range cases {
		if got := TenantAttribution(tc.nonNull, tc.docs); got != tc.want {
			t.Errorf("TenantAttribution(%d, %d) = %q, want %q", tc.nonNull, tc.docs, got, tc.want)
		}
	}
}

func TestValidateCoverageInput(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	valid := CoverageInput{SourceID: "source-a", Start: start, End: start.Add(time.Hour)}
	if err := ValidateCoverageInput(valid); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}

	atLimit := valid
	atLimit.End = start.Add(time.Duration(MaxCoverageDays) * 24 * time.Hour)
	if err := ValidateCoverageInput(atLimit); err != nil {
		t.Fatalf("31-day input rejected: %v", err)
	}

	cases := map[string]CoverageInput{
		"end equals start": {SourceID: "source-a", Start: start, End: start},
		"end before start": {SourceID: "source-a", Start: start, End: start.Add(-time.Second)},
		"over 31 days":     {SourceID: "source-a", Start: start, End: start.Add(time.Duration(MaxCoverageDays)*24*time.Hour + time.Second)},
		"empty source":     {SourceID: "", Start: start, End: start.Add(time.Hour)},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateCoverageInput(in)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want wrapping ErrInvalidInput", err)
			}
		})
	}
}
