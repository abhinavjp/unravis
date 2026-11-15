# BBX-1-02 Counter engine core (BL-121 rules)
Backlog: BL-121 | Group: 1 | Model: performance | Reason: rate/baseline/status rules are the correctness core every counter group depends on.
Depends on: none | Blocks: BBX-1-05, BBX-1-06, BBX-1-09, all counter groups

## Goal
Add a pure-Go counter engine that turns raw counter readings into emitted fields while enforcing the four BL-121 rules: English names, two baselines before any rate, valid-status check on every reading, multi-core CPU not capped at 100. OS-independent and fake-testable.

## Context
- New file `internal/blackbox/engine.go`, test `internal/blackbox/engine_test.go`. Package `blackbox`.
- The PDH source that produces readings is a later packet (BBX-1-06). This engine consumes readings via a struct, so tests need no Windows counters.
- Rules that apply (handoff §3, BL-121): **English counter names, two baselines before a rate, valid-status check, multi-core CPU not capped at 100.** Also **unreadable means missing, never 0**.
- PDH counter status: a reading is valid only when its status is 0 (ERROR_SUCCESS). The PDH source sets `OK=false` for any non-zero status; the engine double-checks.

## Do
1. Create `engine.go` with:
   - `type CounterKind uint8` with `KindGauge`, `KindRate` (cumulative raw → engine computes per-second delta), `KindPDHRate` (PDH already returns per-second; engine still gates it).
   - `type CounterSpec struct { Path string; Field string; Kind CounterKind }` — Path is the English PDH path, Field is the output key.
   - `type Reading struct { Path string; Value float64; Status uint32; OK bool }`.
   - `type CounterSource interface { Collect() ([]Reading, error); Close() error }` — implemented by PDH (later) and fakes.
   - `type Engine struct` holding specs, per-path history (last valid value, last valid time, count of valid baselines), and cores int (>=1).
   - `func NewEngine(specs []CounterSpec, cores int) *Engine` — clamp cores to >=1.
   - `func (e *Engine) Process(now time.Time, readings []Reading) map[string]float64` — for each reading matched to a spec by Path:
     - Skip unless `r.OK && r.Status == 0` (valid-status check). An invalid reading does NOT advance the baseline count.
     - Gauge: emit `Field = Value` immediately (no clamp — a 4-core process at 350% stays 350).
     - KindRate / KindPDHRate: only emit after **two valid baseline samples** have been recorded for that path (i.e. the first two valid samples emit nothing; the third valid sample emits the first rate). For KindRate compute `(Value - prevValue) / dtSeconds` using the previous valid sample's time; guard dt<=0 and counter resets (Value < prevValue → treat as reset, skip emission this cycle, restart baseline). For KindPDHRate emit `Value` (already per-second) once baselines are satisfied.
     - Return only emitted fields; absent fields mean "missing", never 0.
2. Track baselines per path so a counter that appears later (wildcard re-expand) starts its own two-baseline count.
3. No clamping to 100 anywhere.

## Do not
- Do not emit a rate on the first or second valid sample (two baselines REQUIRED before a rate).
- Do not cap percentages at 100 (multi-core rule).
- Do not emit 0 for an invalid/missing reading — omit the field.
- Do not call Windows APIs; the engine is pure.
- Do not allocate a new map per reading; one output map per Process call is fine.

## Tests to write first (fake readings)
- `TestEngineGaugeEmitsImmediately` — gauge valid → field present, value unchanged.
- `TestEngineMultiCoreNotCapped` — gauge 350 on 4 cores → 350 (no clamp).
- `TestEngineRateNeedsTwoBaselines` — KindRate: sample1 → absent, sample2 → absent, sample3 → rate == (v3-v2)/dt.
- `TestEnginePDHRateGated` — KindPDHRate absent for first two valid samples, present on third.
- `TestEngineInvalidStatusOmitted` — Status!=0 or OK=false → field absent, baseline count not advanced.
- `TestEngineCounterResetSkips` — cumulative value drops → no negative rate, emission skipped, baseline restarts.
- `TestEngineZeroDtGuard` — two samples same timestamp → no divide-by-zero, no emission.
- `TestEngineMissingNeverZero` — a spec with no reading produces no field (assert key absent, not 0).

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: no goroutine/ticker; Process reuses history maps
- [ ] Missing reading is omitted, never 0 (TestEngineMissingNeverZero)
- [ ] No text captured (paths/fields are fixed identifiers)
- [ ] Two baselines before any rate (TestEngineRateNeedsTwoBaselines, TestEnginePDHRateGated)
- [ ] Multi-core CPU not capped at 100 (TestEngineMultiCoreNotCapped)
- [ ] Valid-status check on every reading (TestEngineInvalidStatusOmitted)

## Live-check needed?
No for the engine logic. English counter PATHS are verified on a real server with `typeperf -q` (see group 2/4 packets); the engine itself is path-agnostic.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-02.md`.