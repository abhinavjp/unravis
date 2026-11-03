# TSK-17 ref-thresholds

risk: low — three constants and one sentence.

## Outcome

The wireframe's reference minimum is 60 events (100 for P95) and it has an incident minimum of 20. The "sparse" message names the new numbers.

## Write scope

- `prototype/tracing-paper-PROTOTYPE.html`

## Must not change

- Any other line. No "demo"/"sample" wording (D12). The "19 Oct" line (D11).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Locate (Grep) `const SUFFICIENT_REF = 30` (~line 254). Change to `60`. On the next line add `const SUFFICIENT_REF_P95 = 100;` and `const SUFFICIENT_INC = 20;` (same style as the line above, with a `;` if the neighbours use one).
2. Locate `referenceState` (~255-261). Read how it builds its "sparse" text (~258). Change only the numbers/wording so the text states the new minimum: it must read `Fewer than 60 events in the reference window — too few to compare against.` (keep the sentence's existing structure if it differs; the number must come from `SUFFICIENT_REF`, not a hard-coded 30).
3. Grep the file for the literal `30` near `SUFFICIENT_REF` usages and for the text `30 events`; if any displayed string still says 30 in this context, update it to use the constant. List each hit and what you did in `progress.md`.

## Proof

```
node --check prototype/flow-check.cjs
```

Not applicable to the HTML. Instead: Grep `SUFFICIENT_REF = 60` returns 1 hit, `SUFFICIENT_REF_P95 = 100` 1 hit, `SUFFICIENT_INC = 20` 1 hit, and `= 30` no longer appears next to `SUFFICIENT_REF`. Record the three grep results in `progress.md`.

## Depends on

None.
