# TSK-26 docs-greyed-out

risk: low — documentation only.

## Outcome

`docs/user/guided-analysis.md` has a short section explaining when a comparison is greyed out and why.

## Write scope

- `docs/user/guided-analysis.md`

## Must not change

- Existing text, including the threshold version line updated in TSK-02. Any other file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Read the file; find the section on thresholds / "why a result may be unknown" (Grep `unknown`, `thresholds`). Add a new `##` section right after it (or at the end if none fits) titled `When a comparison is greyed out`.
2. Content, in plain words, 5 short bullets or sentences:
   - A rate is compared only if the reference window has at least 60 events and the incident window at least 20.
   - A reference P95 is compared only if the reference has at least 100 events.
   - The reference events must be spread over the window; if nearly all fall in one or two minutes, the comparison is greyed out. At very low traffic (under one event per minute on average) this spread check is skipped.
   - Rates are counted over minutes that have data on both sides; minutes with missing data are left out, not counted as zero.
   - P95 values come from a histogram and are within about 10% of the exact value.
3. Do not name internal field or function names.

## Proof

Grep `When a comparison is greyed out` returns 1 hit in the file.

## Depends on

TSK-16, TSK-23.
