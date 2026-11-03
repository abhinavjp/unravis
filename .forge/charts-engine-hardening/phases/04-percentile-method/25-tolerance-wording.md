# TSK-25 tolerance-wording

risk: low — one sentence.

## Outcome

The "Rules used" card tells the reader that P95 values are read from a histogram and are within about 10% of the exact value.

## Write scope

- `prototype/tracing-paper-PROTOTYPE.html`

## Must not change

- Any other list item. No "demo"/"sample" wording (D12).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. In the "Rules used" card, directly after the three `<li>` items added by TSK-22 and still before the "Results found" `<li>`, add one `<li>` with the same markup:
   `P95 values are read from a histogram of response times, so they are within about 10% of the exact value.`

## Proof

Grep `within about 10% of the exact value` returns 1 hit.

## Depends on

TSK-22.
