# TSK-04 gate-evaluator

risk: low — pure function with a table test.

## Outcome

`type Gate` and `func evaluateGate(...)` exist in `gate.go` and are proven by table tests. Not used by the engine yet.

## Write scope

- `internal/investigate/gate.go` (append)
- `internal/investigate/gate_test.go` (append)

## Must not change

- `spreadOK` and its test. Every other file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

`gate.go`, append:

```go
// Gate says whether the reference holds enough events, spread over enough
// minutes, to compare against. It is computed once per run and never serialized.
type Gate struct {
	RefEvents int
	IncEvents int
	RateOK    bool // rate comparison allowed
	P95OK     bool // reference P95 comparison allowed
	SpreadOK  bool
}

func evaluateGate(refN, incN int, refMinuteCounts []int, th Thresholds) Gate
```

(That block is the exact signature and fields; write the body.) Body:

- `SpreadOK = spreadOK(refMinuteCounts, th)`
- `RateOK = refN >= th.GateRefMinEvents && incN >= th.GateIncMinEvents && SpreadOK`
- `P95OK = RateOK && refN >= th.GateRefMinEventsP95`
- `RefEvents = refN`, `IncEvents = incN`.

`gate_test.go`, append `TestEvaluateGate` with `th := Current()` and a helper `even(n int) []int` returning 10 minutes that share `n` events evenly (each `n/10`, remainder added to the first minute). Rows:

- ref 59, inc 20, `even(59)` → RateOK false, P95OK false, SpreadOK true
- ref 60, inc 20, `even(60)` → RateOK true, P95OK false
- ref 100, inc 20, `even(100)` → RateOK true, P95OK true
- ref 100, inc 19, `even(100)` → RateOK false, P95OK false
- ref 200, inc 50, counts `[200,0,0,0,0,0,0,0,0,0]` → SpreadOK false, RateOK false, P95OK false

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestSpreadOK|TestEvaluateGate"
```

PASS.

## Depends on

TSK-03.
