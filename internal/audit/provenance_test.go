package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCompletenessOf(t *testing.T) {
	n := int64(7)
	cases := map[string]struct {
		p    Provenance
		want string
	}{
		"hint":                         {Provenance{QueryMode: QueryModeHint}, HintBounded},
		"hint with partial reason":     {Provenance{QueryMode: QueryModeHint, PartialReason: "timeout"}, HintBounded},
		"exact partial":                {Provenance{QueryMode: QueryModeExact, PartialReason: "timeout"}, Partial},
		"exact no docsExamined":        {Provenance{QueryMode: QueryModeExact}, RanToEnd},
		"empty mode no docsExamined":   {Provenance{}, RanToEnd},
		"exact with docsExamined":      {Provenance{QueryMode: QueryModeExact, DocsExamined: &n}, Complete},
		"empty mode with docsExamined": {Provenance{DocsExamined: &n}, Complete},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := CompletenessOf(tc.p); got != tc.want {
				t.Fatalf("CompletenessOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScanClassOf(t *testing.T) {
	cases := []struct {
		mode    string
		window  time.Duration
		indexed bool
		want    string
	}{
		{QueryModeExact, time.Hour, false, ScanBroad},
		{QueryModeExact, time.Hour, true, ScanNarrow},
		{QueryModeHint, 24 * time.Hour, false, ScanNarrow},
		{QueryModeHint, 24*time.Hour + time.Second, false, ScanBroad},
	}
	for _, tc := range cases {
		if got := ScanClassOf(tc.mode, tc.window, tc.indexed); got != tc.want {
			t.Errorf("ScanClassOf(%q, %v, %v) = %q, want %q", tc.mode, tc.window, tc.indexed, got, tc.want)
		}
	}
}

func repeatOffset(d time.Duration, n int) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = d
	}
	return out
}

func TestMarginFromOffsets(t *testing.T) {
	pad := 60 * time.Second

	t.Run("too few rows", func(t *testing.T) {
		_, _, err := MarginFromOffsets(repeatOffset(0, 999), pad)
		if !errors.Is(err, ErrTooFewCalibrationRows) {
			t.Fatalf("err = %v, want ErrTooFewCalibrationRows", err)
		}
	})

	t.Run("nearest rank ignores the single outlier", func(t *testing.T) {
		offsets := append(repeatOffset(0, 1000), 900*time.Second)
		observed, margin, err := MarginFromOffsets(offsets, pad)
		if err != nil {
			t.Fatal(err)
		}
		if observed != 0 || margin != 60*time.Second {
			t.Fatalf("observed %v margin %v, want 0s and 60s", observed, margin)
		}
	})

	t.Run("negative offsets use absolute value", func(t *testing.T) {
		offsets := repeatOffset(-5*time.Second, 1000)
		offsets = append(offsets, 0)
		observed, margin, err := MarginFromOffsets(offsets, pad)
		if err != nil {
			t.Fatal(err)
		}
		if observed != 5*time.Second || margin != 65*time.Second {
			t.Fatalf("observed %v margin %v, want 5s and 65s", observed, margin)
		}
	})

	t.Run("margin rounds up to whole seconds", func(t *testing.T) {
		observed, margin, err := MarginFromOffsets(repeatOffset(200*time.Millisecond, 1000), pad)
		if err != nil {
			t.Fatal(err)
		}
		if observed != 200*time.Millisecond || margin != 61*time.Second {
			t.Fatalf("observed %v margin %v, want 200ms and 61s", observed, margin)
		}
	})
}

func assertJSONKeys(t *testing.T, v any, present []string, absent []string) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range present {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON of %T is missing key %q: %s", v, k, raw)
		}
	}
	for _, k := range absent {
		if _, ok := m[k]; ok {
			t.Errorf("JSON of %T has key %q, want it omitted: %s", v, k, raw)
		}
	}
	return m
}

func TestProvenanceJSONFieldNames(t *testing.T) {
	margin := IDMargin{}
	withMargin := assertJSONKeys(t, Provenance{Margin: &margin}, []string{
		"sourceId", "window", "start", "end", "queryMode", "scanClass", "completeness",
		"docsReturned", "docsExamined", "sampleSize", "elapsedMs", "maxTimeMs", "margin",
	}, []string{"partialReason", "note"})
	if string(withMargin["docsExamined"]) != "null" {
		t.Errorf("docsExamined = %s, want null", withMargin["docsExamined"])
	}

	assertJSONKeys(t, Provenance{}, []string{"docsExamined"}, []string{"margin", "partialReason", "note"})

	assertJSONKeys(t, margin, []string{
		"marginMs", "observedMs", "padMs", "percentile", "sampleSize",
		"sampleFrom", "sampleTo", "calibratedAt", "label",
	}, nil)

	assertJSONKeys(t, CoverageInput{}, []string{"sourceId", "start", "end"}, nil)
	assertJSONKeys(t, FieldCoverage{}, []string{"nonNull", "pct"}, nil)
	assertJSONKeys(t, CoverageRow{}, []string{
		"kind", "day", "docs", "requestInitiatedAt", "createdOn", "apiTime", "accountId",
	}, nil)
	assertJSONKeys(t, CoverageReport{}, []string{
		"sourceId", "rows", "total", "tenantAttribution", "provenance",
	}, nil)
}

func TestHintLabelWording(t *testing.T) {
	got := HintLabel(IDMargin{MarginMs: 61000, SampleSize: 10000})
	want := "bounded by margin 61 s, calibrated on 10000 indexed rows; rows outside 61 s would be missed"
	if got != want {
		t.Errorf("HintLabel = %q, want %q", got, want)
	}
}

func TestHintReasonIsShortAndShared(t *testing.T) {
	if got := HintReason(ErrTooFewCalibrationRows); got != "calibration needs at least 1000 rows" {
		t.Fatalf("HintReason = %q", got)
	}
	if got := HintReason(fmt.Errorf("wrapped: %w", ErrTooFewCalibrationRows)); got != "calibration needs at least 1000 rows" {
		t.Fatalf("wrapped HintReason = %q", got)
	}
	if got := HintReason(errors.New("anything else")); got != "calibration failed" {
		t.Fatalf("other HintReason = %q", got)
	}
}
