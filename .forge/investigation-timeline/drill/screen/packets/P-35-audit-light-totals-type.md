# P-35 `light` totals on a request row (type and demo)

model: sonnet, effort: medium. Reason: a shared type that many JSON fixtures touch; null must stay null.
risk: medium. Review batch: RB-3, then RB-6. Wave: W1. Requirements: R-08, R-11 (BL-012).

## Outcome
A request row in list answers (`/activity`, `/requests`, slowest) can carry `light: { sqlMs, redisMs, httpMs }`. Absent block = not recorded. The demo data fills it for most rows and leaves it out for some, so the "not recorded" path is shown too.

## Write scope
- `internal/audit/models.go` (struct `RequestRecord`, line 369)
- `internal/audit/demo.go` (near `demoPerf`, line 613, and where demo rows are built)
- `internal/audit/demo_test.go` or the nearest existing test file for demo rows

## Must not change
- Any existing field or JSON tag of `RequestRecord`. `RecordPerf` (`internal/audit/record_perf.go`). The detail `perf` block. `internal/store`, `internal/server`.

## Changes
1. `models.go`: above `RequestRecord` add
```go
// RecordLight is the three light totals of one call for list rows.
// A nil number means "not recorded", never zero.
type RecordLight struct {
	SQLMs   *float64 `json:"sqlMs"`
	RedisMs *float64 `json:"redisMs"`
	HTTPMs  *float64 `json:"httpMs"`
}
```
   and at the end of `RequestRecord`: `Light *RecordLight \`json:"light,omitempty"\``. With `omitempty` every existing golden JSON stays the same when the block is nil.
2. `demo.go`: add `func demoLight(r RequestRecord, index int) *RecordLight` that takes its three numbers from `demoPerf(r, index)` (same record → same numbers as its detail), returns nil when `demoPerf` returns nil, and returns nil for every 7th row (`index%7 == 6`) so the demo shows "not recorded" rows. Set `Light` where the demo builds list rows (find the callers of the demo list builders used by activity and slowest; `grep -n "demoPerf(" internal/audit/`).
3. Check: `grep -rn "RequestRecord{" internal/ cmd/ --include=*_test.go | wc -l` and run the full audit tests; no golden file may change. If one does, the field is not `omitempty`-safe there: STOP and report.
4. Tests: a demo activity answer has at least one row with `Light != nil` and one with `Light == nil`; for a row with both, `Light.SQLMs` equals the detail's `perf.SQLMs`; marshalled row without the block has no `"light"` key.
5. Web side is already done by P-11 (`RequestRecord.light?`).

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/audit/` → no output.
- [ ] `go test ./internal/audit/ -count=1` → `ok`.
- [ ] `go test ./internal/store/ ./internal/mcp/ -count=1` → `ok` (they marshal `RequestRecord`).
- [ ] `git status --short internal/ | grep -v "internal/audit/"` shows nothing new from this packet.
- [ ] Demo path only. The live path is P-36.

## Depends on
P-13 (audit package order: P-13 → P-35 → P-37). Does not need the probe.
