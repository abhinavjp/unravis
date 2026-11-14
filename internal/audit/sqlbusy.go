package audit

import (
	"context"
	"time"
)

// SQLBusyInput asks for the database time the API calls recorded, per step.
type SQLBusyInput struct {
	Start         time.Time
	End           time.Time
	BucketSeconds int // one of 60, 300, 600, 900
	// SourceIDs selects which connected audit sources contribute. A source is a
	// connected database/collection (audit.Source.ID), not a field on a stored
	// record: the repository reads each selected source and sums its steps. Empty
	// reads every connected source (the historical behaviour); non-empty reads
	// only these ids, so the busy time is not summed across sources the caller did
	// not select. An id that is not connected fails the call closed, as Rollup does.
	SourceIDs []string
}

// SQLBusyBin is one step. BusyMs is the summed database time, in milliseconds,
// of the calls that STARTED in the step. Calls is how many calls had the number.
type SQLBusyBin struct {
	Start  time.Time `json:"start"`
	BusyMs float64   `json:"busyMs"`
	Calls  int64     `json:"calls"`
}

// SQLBusyResult: Recorded is false when the source does not store the number at all.
type SQLBusyResult struct {
	Bins     []SQLBusyBin `json:"bins"`
	Recorded bool         `json:"recorded"`
}

// SQLBusyReader is implemented by a source that can answer it. Read-only.
// A source without a reader answers "not recorded" (no result), never 0.
type SQLBusyReader interface {
	SQLBusy(ctx context.Context, in SQLBusyInput) (SQLBusyResult, error)
}

const (
	sqlBusyMaxSpan  = 31 * 24 * time.Hour
	sqlBusyMaxSteps = 120
)

// Validate checks the window: End after Start, at most 31 days, a step length
// from the allowed set and at most 120 steps.
func (in SQLBusyInput) Validate() error {
	if !in.End.After(in.Start) {
		return invalidInputf("end must be after start")
	}
	if in.End.Sub(in.Start) > sqlBusyMaxSpan {
		return invalidInputf("the window must be at most 31 days")
	}
	switch in.BucketSeconds {
	case 60, 300, 600, 900:
	default:
		return invalidInputf("bucketSeconds must be 60, 300, 600 or 900")
	}
	if in.Steps() > sqlBusyMaxSteps {
		return invalidInputf("the window must be at most 120 steps")
	}
	return nil
}

// Steps is how many steps the window has: the span divided by the step length,
// rounded up. It is 0 when BucketSeconds is not positive.
func (in SQLBusyInput) Steps() int {
	if in.BucketSeconds <= 0 || !in.End.After(in.Start) {
		return 0
	}
	step := time.Duration(in.BucketSeconds) * time.Second
	return int((in.End.Sub(in.Start) + step - 1) / step)
}
