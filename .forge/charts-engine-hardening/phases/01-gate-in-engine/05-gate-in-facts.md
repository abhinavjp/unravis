# TSK-05 gate-in-facts

risk: medium — touches `Facts`, which feeds every signal; kept behaviour-neutral (computed, not applied).

## Outcome

`Facts` carries a computed `Gate` (not serialized). Nothing reads it yet, so no verdict changes.

## Write scope

- `internal/investigate/facts.go`
- `internal/investigate/facts_test.go` (append one test)

## Must not change

- JSON output of `Facts` (the new field is `json:"-"`).
- `fill`, `Change` computations, `summarize`, goldens.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. In `type Facts struct` (facts.go:12), add after `Gaps []Span ...` a field `Gate Gate \`json:"-"\`` with the comment `// Gate says whether the reference can be compared against. Computed in groups(); never serialized.`
2. In `func (p *prep) groups()` (facts.go:570), immediately AFTER the two lines `fill(&p.facts.Incident, p.inc, p.incBins)` and `fill(&p.facts.Ref, p.ref, p.refBins)` and BEFORE `f := &p.facts`, add: build `counts := make([]int, 0, len(p.refBins))`; loop `for _, b := range p.refBins { if b.gap == "" { counts = append(counts, b.n()) } }`; then after `f := &p.facts` is declared, set `f.Gate = evaluateGate(f.Ref.N, f.Incident.N, counts, p.th)`. (`b.n()` and `b.gap` are the bin helpers already used by `testable(bins)` at facts.go:552 and `markGaps`. `Percentiles.N` is the duration count of the window.) Do NOT change any other line. Do NOT apply the gate yet.
3. Test, append to `facts_test.go`: `TestFactsGateIsComputed` — `f := ComputeFacts(stepScenario(mod{}), Current())`; assert `f.Gate.RefEvents == f.Ref.N`, `f.Gate.IncEvents == f.Incident.N`, and `f.Gate.RefEvents > 0`. (`stepScenario(extra mod) Input` is at facts_test.go:8.)

## Proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./internal/investigate
```

PASS, goldens unchanged by this task.

## Depends on

TSK-02 (goldens green), TSK-04 (`evaluateGate`).
