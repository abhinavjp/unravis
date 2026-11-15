# P-14 Tracing buckets carry busy time (API lane)

model: sonnet, effort: high. Reason: threads a new number through an accumulator that merges sources; null must stay null.
risk: medium. Review batch: RB-2. Wave: W0. Requirement: R-04 (BL-010), integration point I-1.

## Outcome
Each bucket of the API lane in `POST /api/cases/{id}/tracing` carries `busyMs`: the summed duration of the calls that started in that bucket. A bucket with no timed calls has no `busyMs`.

## Write scope
- `internal/server/tracing.go` (struct `tracingBucket` only)
- `internal/server/tracing_audit_latency.go`
- `internal/server/tracing_audit.go`
- `internal/server/tracing_audit_latency_test.go` (existing or new)

## Must not change
- Any other field or JSON tag of `tracingBucket` (line 397). `tracingLane`, `caseTracing`, the axis code. Other server files. Strict request decoding.

## Changes
1. First read and write down (in your report): what `audit.RollupWireBin.Sum` holds (`internal/audit/rollup_wire.go:27`, tag `sum`), its unit, and whether it sums only timed calls. Follow it to where the store fills it. Expected: milliseconds of the audit duration. If the unit is not milliseconds, convert; if you cannot tell, STOP and report.
2. `tracing.go`, in `tracingBucket`, after the `MaxMs` field add:
```go
	// BusyMs is the summed duration, in milliseconds, of the calls that started in this bucket.
	// Nil means it was not recorded; it is never a stand-in zero.
	BusyMs *float64 `json:"busyMs,omitempty"`
```
3. `tracing_audit_latency.go`: `latencyAcc` (line 13) gains a `sum float64` and a `hasSum bool`. `add` (line 21) adds the bin's `Sum` when the bin has timed calls; merging two accumulators adds sums. `figures()` (line 81) returns the sum as an extra value, or a second small method `busy() *float64` returns nil when `hasSum` is false.
4. `tracing_audit.go`: where `readAuditLane` (line 224) copies figures into each `tracingBucket`, set `BusyMs` from the accumulator.
5. Tests: one bin `Count 2, Sum 3000` → `BusyMs` 3000; two sources merged → sums added; a bucket with `Count 0` → `BusyMs` nil and the JSON has no `busyMs` key (marshal and check with `strings.Contains`); a clipped bucket still reports its sum.
6. Check the demo path: find the demo `RollupReader` (`grep -rn "RollupWireBin{" internal/audit/`) and confirm it fills `Sum`. If it does not, report it as a follow-up for the orchestrator (do not edit `internal/audit` here; P-13 owns that package in this wave).

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/server/` → no output.
- [ ] `go test ./internal/server/ -run 'Latency|TracingAudit' -count=1` → `ok`.
- [ ] `go test ./internal/server/ -count=1` (about 2.5 min, run in background) → `ok`.
- [ ] Report states the unit of `Sum` with the file:line that proves it.
- [ ] Report states whether the demo rollup fills `Sum` (demo path) and that the live path is verified in P-44/P-46.

## Depends on
Nothing. First Go writer in `internal/server`; P-18, P-19, P-20, P-21, P-39 follow in that order.
