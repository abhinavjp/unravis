# Progress: Phase 01 gate-in-engine

## Done

- TSK-01 thresholds-fields: DONE. Version 2026-11-02, 8 new fields, Entries rows, test pins.
- TSK-02 thresholds-fixtures: DONE. History has 2 entries (old kept; new fingerprint 24d742613e8b9663). 5 goldens regenerated; plan.spec.ts:94 and guided-analysis.md:50 updated.
- TSK-03 spread-rule: DONE. `spreadOK` in gate.go, `TestSpreadOK` 8 subtests.
- TSK-04 gate-evaluator: DONE. `Gate`, `evaluateGate`, `TestEvaluateGate`.
- TSK-05 gate-in-facts: DONE. `Facts.Gate` (`json:"-"`) computed in `groups()` from covered reference bins; `TestFactsGateIsComputed`. Not applied.
- TSK-06 gate-applies: DONE after owner ruling. Gate applied in groups() (PerSec nil when !RateOK, Ref.P95 nil when !P95OK).
- TSK-07 gate-scenario-tests: DONE. TestGateTooFewReferenceEvents, ExactlyEnough, P95Boundary, SpikyReferenceIsGrey, DoesNotChangeASteadyRun (gateInput keeps n reference requests at an even stride).

## Evidence

- TSK-01: `go build ./internal/investigate` + `go test -run "TestThresholds{VersionLiteral,FingerprintMoves,MatchDecisions,Names}"` PASS (`ok api-audit-explorer/internal/investigate 0.738s`).
- TSK-02: `go test ./internal/investigate` PASS (`ok ... 1.545s`); `go build ./...` PASS. Golden diff: 5 goldens +59/-15 total with thresholds.history.json (+4). Changed lines only: `version` (x2 per file), the 8 new threshold entries, and the comma after `originBucketSeconds`.
- TSK-03: `go test -run TestSpreadOK -v` PASS, 8 subtests.
- TSK-04: `go test -run "TestSpreadOK|TestEvaluateGate"` PASS (`ok ... 0.760s`).
- TSK-05: `go build ./...` OK; `go test ./internal/investigate` PASS (`ok ... 1.891s`).
- TSK-06: with the gate applied, `go test ./internal/...` FAIL: `TestFactsGuardsOnSmallGroups` (facts_test.go:160) `guards: {N:60 ... P95:<nil> P99:<nil>}`. Edit reverted; after revert `go test ./internal/...` PASS (all packages ok, internal/server 105.9s).

## Not fixed

- TSK-06 BLOCKED. `TestFactsGuardsOnSmallGroups` builds a 60-event reference (perMin 1) and asserts P95 shown, P99 withheld. With the owner values the reference P95 needs >= 100 events (GateRefMinEventsP95) and `audit.P99MinSampleCount` is also 100. No event count gives "P95 shown and P99 withheld", so the packet's allowed fix (raise perMin in the test setup) cannot make this test pass. Needs an owner ruling: either change the test's assertion (P95 is now withheld below 100; the P99 part is then trivially true), or move the P95 gate to differ from 100. Production code was not changed in the revert; the two `nil` assignments are removed.
- TSK-07 not started.
- `2026-10-27` hits outside scope left untouched: all are other things (build dates, wiring tests, buildout/ docs, web/dist bundle, prototype/video note `HOW-THINGS-ARE-CALCULATED.md:20` quoting the old threshold version, prototype/research text, old history entry which is expected).
- Line endings: repo files are CRLF; `gofmt -w` made thresholds.go LF, I converted thresholds.go, gate.go, gate_test.go, facts.go, facts_test.go back to CRLF. `gofmt -l` flags every file in the package for that reason (pre-existing).

## UNMEASURED

- Second-reviewer pass on TSK-06 (not reached).

## Test-only generator raises (TSK-06)

(none; the single failing test could not be fixed by a raise)

## Ruling

Ruling: test assertion updated to the new P95 gate (owner decision 2026-11-02). `TestFactsGuardsOnSmallGroups`: the 60-event case now asserts P95 == nil and P99 == nil; a second case (perMin 2, 120 reference events) asserts P95 shown; the under-20-calls assertion is unchanged. No other existing test changed.

## Final evidence (after ruling)

- `go build ./...` OK. `go test ./internal/...` PASS (all packages ok; no FAIL lines).
- Goldens (I1): changed lines only `version` (10x), `originBucketSeconds` comma, and the 8 new threshold entries. No golden touched since TSK-02 regeneration; they pass with the gate applied.
- UNMEASURED: second-reviewer pass on TSK-06 (still owed before the phase gate).
- Note: TSK-06 Not-fixed entry above is superseded by the ruling.
