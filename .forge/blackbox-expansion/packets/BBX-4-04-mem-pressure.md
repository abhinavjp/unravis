# BBX-4-04 Memory pressure: hard faults, kernel pool, pagefile
Backlog: BL-115 | Group: 4 | Model: efficient | Reason: a fixed counter list through the existing engine; mechanical data wiring.
Depends on: BBX-1-05, BBX-1-06 | Blocks: none

## Goal
Sample hard page faults, kernel pool bytes and pagefile usage every 15 s (machine-wide).

## Context
- New file `internal/blackbox/mempressure.go` (no tag) + test `internal/blackbox/mempressure_test.go` (fake source). Copy the group pattern from BBX-2-01 (init RegisterGroup("memPressure", false, ...); mandatory).
- Researched paths (UNVERIFIED): `\Memory\Pages Input/sec` (hard faults), `\Memory\Pool Paged Bytes`, `\Memory\Pool Nonpaged Bytes`, `\Paging File(_Total)\% Usage`.
- `MemPressureGroup{HardFaultsPerSec *float64, PoolPagedBytes *uint64, PoolNonPagedBytes *uint64, PageFileUsagePct *float64}` (groups_types.go).
- Rules: **machine-wide**; **two baselines before a rate** (Pages Input/sec); **valid-status check**; **missing never 0**; **no text**.

## Do (literal)
1. Constant paths slice. Specs: HardFaultsPerSec = KindPDHRate; PoolPagedBytes/PoolNonPagedBytes = KindGauge (uint64); PageFileUsagePct = KindGauge.
2. Factory: NewPDHSource + NewEngine; on PDH error return it.
3. Collect: src.Collect → eng.Process → build MemPressureGroup (absent fields nil); nil if nothing emitted.

## Do not
- Do not emit 0 for missing; no extra logic; no goroutine/ticker; no text; do not change engine/registry.

## Tests to write first (fake CounterSource)
- `TestMemPressureMapsFields`, `TestMemPressureMissingOmitted`, `TestMemPressureEmptyNil`, `TestMemPressureRateGated` (Pages Input/sec gated).

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: reused handle; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestMemPressureMissingOmitted)
- [ ] No text captured
- [ ] Two baselines before a rate; valid-status check
- [ ] Budget gate BBX-1-09 still passes

## Live-check needed?
YES — `typeperf -q Memory` / `Paging File` on a real server; compare Pool Paged Bytes to perfmon. Fake-tested only until then.

## Report back
Files changed, each checklist item ticked or failed with output, exact paths used. Full report to `.forge/blackbox-expansion/reports/BBX-4-04.md`.