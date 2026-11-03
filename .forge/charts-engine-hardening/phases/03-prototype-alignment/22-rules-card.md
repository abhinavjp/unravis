# TSK-22 rules-card

risk: low — wording in a list.

## Outcome

The "Rules used" card states the gate in plain words, in the same style as the other items.

## Write scope

- `prototype/tracing-paper-PROTOTYPE.html`

## Must not change

- Existing list items. No "demo"/"sample" wording (D12). The "19 Oct" line (D11).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Locate the "Rules used" card (~line 1788; Grep `Rules used`). Find the `<li>` that starts "Results found" (or the last list item).
2. Immediately BEFORE that `<li>`, add these `<li>` items (same markup as its siblings, nothing else):
   - `A comparison needs at least 60 events in the reference window (100 for a P95) and at least 20 in the incident window; otherwise it is greyed out.`
   - `Events must be spread over the reference window: if almost all of them fall in one or two minutes, the reference is greyed out.`
   - `Rates are counted over minutes that have data on both sides; minutes with missing data are left out, not counted as zero.`

## Proof

Grep `at least 60 events` returns 1 hit. Load with `preview_start` and confirm `read_console_messages` is clean. UNMEASURED if the browser cannot run.

## Depends on

TSK-19.
