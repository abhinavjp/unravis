# TSK-18 covered-rate-refcmp

risk: medium — `refCmp` feeds several charts; a wrong rate flips verdicts across the wireframe.

## Outcome

`refCmp` computes rates over covered minutes on both sides, adds the incident minimum, and takes a new 5th parameter `incCov`. Every call site passes it.

## Write scope

- `prototype/tracing-paper-PROTOTYPE.html`

## Must not change

- The returned object's field names. Anything outside `refCmp` and its call sites.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Locate `function refCmp` (~line 1200). Read it fully first; it takes the incident total, the reference total and span/coverage values and returns an object with at least `rate`, `why` and a verdict.
2. Add a 5th parameter `incCov` (share of the incident window that is covered, 0..1). Meaning: `incCov = 1 − (gap minutes ÷ incident span)`.
3. Rate maths (replace the existing rate expression): `incRate = tot / (incSpan * incCov / MIN)` and `refRate = refTot / (refSpan * cov / MIN)`, where `cov` is the reference coverage the function already receives; `rate = incRate / refRate` (use the file's existing minute constant — `MIN` — and span variable names as they are written in the function; if a name differs, use the function's own).
4. Incident minimum: before the rate maths, `if (tot < SUFFICIENT_INC) return { rate: null, why: 'too few events in the incident window' }` merged into the shape the function returns for "no rate" today (keep its other fields).
5. Update all call sites (~1204, ~1205 twice, ~1212; Grep `refCmp(` to find each). For an audit-kind call pass `incCov = 1 - sum(gapIv)/incSpan` using the interval-sum helper/variable already used there; for a lane call pass `1 - gapsum/incSpan` using the lane gap total already computed there. If a call site has no incident coverage in scope, pass `1`.
6. Flag in `progress.md`: at the ~1204 call site the zero-event incident case now returns the grey "too few events" result instead of a rate of 0 (decision recorded in plan.md, open decision 5).

## Proof

Grep `refCmp(` shows every call has 5 arguments. Open the file in the built-in browser via `preview_start` with `url` set to the file's `file:///` path (or run `node prototype/flow-check.cjs` if Playwright exists) and confirm there is no console error: `read_console_messages` returns none. Record the result in `progress.md`; if neither tool can run, write UNMEASURED.

## Depends on

TSK-17.
