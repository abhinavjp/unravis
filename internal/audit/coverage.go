package audit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	AccountIDThreshold = 0.80
	MaxCoverageDays    = 31
	MaxCoverageRows    = 1000
)

var ErrBroadScanRefused = errors.New("broad scan refused on a live source")

// ErrDocsExaminedBudget is returned (wrapped in ErrLimit) when the collection
// holds more documents than --docs-examined-budget allows a census to read.
var ErrDocsExaminedBudget = errors.New("collection is above the docs-examined budget")

// ErrCollectionSizeCheck is returned when the $collStats count that guards a
// census could not be read. It never carries driver or connection text.
var ErrCollectionSizeCheck = errors.New("could not check the collection size")

type CoverageInput struct {
	SourceID string    `json:"sourceId"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
}

// FieldCoverage is how many of a field's documents are non-null.
type FieldCoverage struct {
	NonNull int64   `json:"nonNull"`
	Pct     float64 `json:"pct"` // 0..100, 1 dp
}

type CoverageRow struct {
	Kind               string        `json:"kind"` // synchronous|asynchronous|hangfire|unknown (same switch as pipeline.go)
	Day                string        `json:"day"`  // YYYY-MM-DD UTC of RequestInitiatedAt, else CreatedOn
	Docs               int64         `json:"docs"`
	RequestInitiatedAt FieldCoverage `json:"requestInitiatedAt"`
	CreatedOn          FieldCoverage `json:"createdOn"`
	ApiTime            FieldCoverage `json:"apiTime"`
	AccountID          FieldCoverage `json:"accountId"`
}

type CoverageReport struct {
	SourceID          string        `json:"sourceId"`
	Rows              []CoverageRow `json:"rows"`
	Total             CoverageRow   `json:"total"`             // Kind "all", Day ""
	TenantAttribution string        `json:"tenantAttribution"` // "available" | cov.tenantUnavailable text
	Provenance        []Provenance  `json:"provenance"`
}

type CoverageProber interface {
	Coverage(ctx context.Context, in CoverageInput) (CoverageReport, error)
}

// ValidateCoverageInput requires a source and a window of more than zero and at
// most MaxCoverageDays long.
func ValidateCoverageInput(in CoverageInput) error {
	if in.SourceID == "" {
		return invalidInputf("sourceId is required")
	}
	if !in.End.After(in.Start) {
		return invalidInputf("coverage end must be after start")
	}
	if in.End.Sub(in.Start) > time.Duration(MaxCoverageDays)*24*time.Hour {
		return invalidInputf("coverage window must not exceed %d days", MaxCoverageDays)
	}
	return nil
}

// TenantAttribution says whether accountId is populated enough to attribute
// figures to tenants: at least AccountIDThreshold of the documents.
func TenantAttribution(nonNull, docs int64) string {
	if docs > 0 && float64(nonNull)/float64(docs) >= AccountIDThreshold {
		return "available"
	}
	return fmt.Sprintf("tenant attribution unavailable (%.1f %% populated)", CoveragePct(nonNull, docs))
}

// CoveragePct is nonNull as a percentage of docs, rounded to 1 dp. It is 0 when
// docs is 0.
func CoveragePct(nonNull, docs int64) float64 {
	if docs == 0 {
		return 0
	}
	return math.Round(float64(nonNull)*1000/float64(docs)) / 10
}
