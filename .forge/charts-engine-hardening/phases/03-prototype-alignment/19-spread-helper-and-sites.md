# TSK-19 spread-helper-and-sites

risk: medium — wires a new check into the reference state used by every comparison.

## Outcome

`spreadOk(counts)` exists in the wireframe with the same rule as the engine, and `refCmp`/`referenceState` call it so a one-burst reference is grey.

## Write scope

- `prototype/tracing-paper-PROTOTYPE.html`

## Must not change

- The Go rule: this helper mirrors `spreadOK` in `internal/investigate/gate.go` exactly (mean-based skip).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Next to `referenceState` (~255), add:
   `function spreadOk(counts)` — `counts` = events per covered reference minute. Rules in order: empty → `false`; `mean = sum/len`; `mean < 1` → `true` (skipped at a trickle); share of minutes with an event `>= 0.5` → `true`; `sum > 0 && max/sum <= 0.25` → `true`; else `false`. Add the constants `SPREAD_MIN_SHARE = 0.5`, `SPREAD_MAX_MINUTE = 0.25`, `SPREAD_SKIP_BELOW = 1` beside `SUFFICIENT_REF`.
2. Build `counts` where the reference is evaluated:
   - audit kinds: for each covered reference minute `m` (where `available(m)`), `n = sum over opsFor(kind) of opMinute(op, m).n`.
   - lanes: iterate the imports exactly the way `laneWin` (~1265) does, take `laneMinute(key, m).n` per covered minute.
   Read those helpers first; use their own signatures.
3. In `refCmp` (or in `referenceState` if that is where the reference is judged — read which one decides `rate: null`), after the minimum checks: `if (!spreadOk(counts)) return { rate: null, why: 'reference events are bunched in a few minutes' }`, in the same shape as the other "no rate" returns. Pass `counts` into `refCmp` as a 6th parameter if it is not otherwise reachable, and update every call site.
4. Do not add any other visible text.

## Proof

Grep `spreadOk(` shows the definition plus at least one use. Run `node prototype/flow-check.cjs`, or load the file with `preview_start` and confirm `read_console_messages` shows no error. Record the result; UNMEASURED if neither can run.

## Depends on

TSK-18.
