# TSK-20 Merge-sentinel, a11y and wireframe conformance reviews
Risk: medium. Worker size: strong. Reason: whole-branch security and regression review.

## Outcome
Three review notes in `.forge/blackbox-wizard-popup/reviews/`: `merge-sentinel.md`, `a11y.md`, `wireframe.md`. Each lists findings with file:line and severity, or "none".

## Write scope
- `.forge/blackbox-wizard-popup/reviews/*.md` (new)

## Must not change
Any source file (findings go to TSK-21).

## Changes
- Merge-sentinel over `git diff <base>...HEAD`: drives route guards, no OS text, payload keys, probe off, Apply unchanged, no deferred work built.
- A11y: dialog focus on open, heading focus per step, Esc leave layer, Tab trap, `aria-current`, `aria-labelledby`, `role="alert"` errors, 40-44 px targets, reduced motion, 640 px layout.
- Wireframe conformance against variant A, using decisions.md rulings where they override the wireframe (B-01..B-09).

## Proof
The three files exist and each names the commit reviewed.

## Depends on
TSK-19.
