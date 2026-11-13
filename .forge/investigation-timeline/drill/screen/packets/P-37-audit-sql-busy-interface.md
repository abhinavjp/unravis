# P-37 "Database time the API saw": reader interface and demo

model: sonnet, effort: medium. Reason: a new small read interface in the shared `audit` package plus a demo answer.
risk: medium. Review batch: RB-3, then RB-6. Wave: W2. Requirements: R-05, R-07 (BL-013 fallback).

## Outcome
`internal/audit` defines a read-only call that returns, per time step, the database time the API calls themselves recorded. The demo implements it. No store or server code yet.

## Write scope
- `internal/audit/sqlbusy.go` (new)
- `internal/audit/sqlbusy_demo.go` (new)
- `internal/audit/sqlbusy_test.go` (new)

## Must not change
- Existing interfaces and their method sets (adding a method to an existing interface breaks every fake in the repo): this is a NEW, separate interface. `internal/store`, `internal/server`.

## Changes
1. Read `internal/audit/activity.go` and `activity_demo.go` first and copy their pattern (input struct with validation, result struct, interface, demo implementation). Say in the report which pattern you followed.
2. `sqlbusy.go`:
```go
// SQLBusyInput asks for the database time the API calls recorded, per step.
type SQLBusyInput struct {
	Start         time.Time
	End           time.Time
	BucketSeconds int // one of 60, 300, 600, 900
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
type SQLBusyReader interface {
	SQLBusy(ctx context.Context, in SQLBusyInput) (SQLBusyResult, error)
}
```
   plus `func (in SQLBusyInput) Validate() error`: End after Start, span at most 31 days, `BucketSeconds` in the allowed set, at most 120 steps.
3. `sqlbusy_demo.go`: the demo source type (the one that implements the activity reader) gains `SQLBusy`: sum `demoLight(...)`'s `SQLMs` (P-35) over the demo rows whose start falls in each step; rows with nil light are not counted; `Recorded` true. Steps with no counted call are left out of `Bins`.
4. Tests: validation cases; the demo answer for the fixed demo clock has at least one bin; the sum of a bin equals the sum of the `Light.SQLMs` of the demo activity rows in that step; a bin never has `Calls` 0.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/audit/` → no output.
- [ ] `go test ./internal/audit/ -run 'SQLBusy' -count=1` → `ok`.
- [ ] `go test ./internal/audit/ -count=1` → `ok`.
- [ ] `go build ./...` proves no existing interface changed.
- [ ] Demo path only; live path is P-38 + P-39.

## Depends on
P-35 (uses `demoLight`). Audit package order: after P-35.
