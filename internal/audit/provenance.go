package audit

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	QueryModeExact = "exact"
	QueryModeHint  = "hint" // "" means exact
)

const (
	ScanNarrow = "narrow"
	ScanBroad  = "broad"
)

const (
	Complete    = "complete"
	RanToEnd    = "ran_to_end"
	Partial     = "partial"
	HintBounded = "hint"
)

const (
	IDCalibrationSample  = 10000
	IDMinCalibrationRows = 1000
	IDMarginPad          = 60 * time.Second
	IDMarginTTL          = time.Hour
)

// IDMargin is the calibrated _id window used by hint mode: how far an ObjectId's
// embedded time may lie from the request's initiation time.
type IDMargin struct {
	MarginMs     int64     `json:"marginMs"`   // observed + pad, rounded up to whole seconds
	ObservedMs   int64     `json:"observedMs"` // p99.9 of |ObjectId time - RequestInitiatedAt|
	PadMs        int64     `json:"padMs"`
	Percentile   float64   `json:"percentile"` // 99.9
	SampleSize   int       `json:"sampleSize"` // rows with a date RequestInitiatedAt
	SampleFrom   time.Time `json:"sampleFrom"` // oldest RequestInitiatedAt in sample
	SampleTo     time.Time `json:"sampleTo"`
	CalibratedAt time.Time `json:"calibratedAt"`
	Label        string    `json:"label"` // HintLabel(m)
}

// Provenance records how a figure was produced, so a reader can tell an exact
// scan from a hint-bounded one and a complete result from a partial one.
type Provenance struct {
	SourceID      string    `json:"sourceId"`
	Window        string    `json:"window"` // current|reference|page|breakdown|coverage
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	QueryMode     string    `json:"queryMode"` // exact|hint (mode actually run)
	ScanClass     string    `json:"scanClass"`
	Completeness  string    `json:"completeness"`
	PartialReason string    `json:"partialReason,omitempty"` // timeout|rowcap|error
	DocsReturned  int64     `json:"docsReturned"`
	DocsExamined  *int64    `json:"docsExamined"` // null = not measured (always on live)
	SampleSize    int64     `json:"sampleSize"`   // records behind the figures
	ElapsedMs     int64     `json:"elapsedMs"`
	MaxTimeMs     int64     `json:"maxTimeMs"`
	Margin        *IDMargin `json:"margin,omitempty"` // hint mode only
	Note          string    `json:"note,omitempty"`   // e.g. hint fallback reason
}

var ErrTooFewCalibrationRows = errors.New("too few rows to calibrate the _id margin")

// CompletenessOf labels a result. A live result is never "complete" while
// docsExamined is unmeasured (§6.4(4)).
func CompletenessOf(p Provenance) string {
	switch {
	case p.QueryMode == QueryModeHint:
		return HintBounded
	case p.PartialReason != "":
		return Partial
	case p.DocsExamined == nil:
		return RanToEnd
	default:
		return Complete
	}
}

// ScanClassOf reports a scan as narrow when it is bounded by an index or by a
// hint over at most 24 hours, and broad otherwise.
func ScanClassOf(mode string, window time.Duration, indexed bool) string {
	if (indexed || mode == QueryModeHint) && window <= 24*time.Hour {
		return ScanNarrow
	}
	return ScanBroad
}

// MarginFromOffsets calibrates the _id margin from observed offsets between an
// ObjectId's embedded time and the request time. It takes the nearest-rank p99.9
// of the absolute offsets and adds pad, rounded up to whole seconds.
func MarginFromOffsets(offsets []time.Duration, pad time.Duration) (observed, margin time.Duration, err error) {
	n := len(offsets)
	if n < IDMinCalibrationRows {
		return 0, 0, ErrTooFewCalibrationRows
	}
	abs := make([]time.Duration, n)
	for i, d := range offsets {
		if d < 0 {
			d = -d
		}
		abs[i] = d
	}
	sort.Slice(abs, func(i, j int) bool { return abs[i] < abs[j] })
	rank := int(math.Ceil(0.999*float64(n))) - 1
	observed = abs[rank]
	margin = ceilToSecond(observed + pad)
	return observed, margin, nil
}

func ceilToSecond(d time.Duration) time.Duration {
	if rem := d % time.Second; rem != 0 {
		return d - rem + time.Second
	}
	return d
}

// HintLabel is the §6.4(2) sentence that accompanies every hint-mode result.
func HintLabel(m IDMargin) string {
	return fmt.Sprintf("bounded by margin %d s, calibrated on %d indexed rows; rows outside %d s would be missed",
		m.MarginMs/1000, m.SampleSize, m.MarginMs/1000)
}
