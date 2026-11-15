# P-47 Clumsy-user pass: try to break the screen

model: sonnet, effort: medium. Reason: exploratory testing with a written list of what broke; adds regression tests only for real bugs.
risk: low. Review batch: RB-7. Wave: W10. Requirement: owner rule (one break-the-app review after each phase).

HARD LIMITS: you do not fix product code. Read-only use of live data: change and delete nothing that belongs to the owner. You do not spawn subagents.

## Outcome
`screen/reviews/clumsy.md` lists every way the screen misbehaved, with steps to repeat it, and each real bug has a failing e2e scenario marked `test.fail` so it is not forgotten.

## Write scope
- `.forge/investigation-timeline/drill/screen/reviews/clumsy.md` (new)
- `tools/e2e/specs/slow-call-screen.spec.ts` (append `SCALL-C..` scenarios only; do not change SCALL-01..06)

## Must not change
- Product code. Existing scenarios.

## Changes
On the installed rc.35 (live case first, then the demo for the pretend states), try at least these, one line each in the report (`OK` or `BROKE: steps → what happened → expected`):
1. Double-click and triple-click a diamond; click two diamonds fast.
2. Press Esc many times; Esc while the screen is still finding the call; Esc with focus in the custom minutes box.
3. Browser Back, Forward, Back, Forward quickly; Back during loading; reload during loading; open the screen's address in a second tab.
4. Custom window: 0, negative, 99999, letters, empty, pasted text with spaces; Apply with nothing changed.
5. Earlier / Later twenty times fast; drag the overview box out of the strip, past both ends, and release outside the window; drag with a second mouse button.
6. Switch every source off; switch all on; toggle while loading.
7. Chips: click the three chips fast in a row; a chip while "asking" is shown; a call with no user id.
8. Resize from 1440 wide down to 800 wide and back while the screen is open; zoom the browser to 150 % and 67 %.
9. Keyboard only: reach every control with Tab; the overview box with arrow keys; a list row with Enter; focus is always visible.
10. Leave the screen open 10 minutes (session, stale data), then use it.
11. Open in Analysis, then Back: where does it land?
12. Demo: every pretend button twice; pretend while a read is running.
13. A call at the very edge of the case's data (first or last diamond); a slot whose record cannot be found (old link after the data moved on).
For each `BROKE` on the demo: append a scenario `SCALL-Cnn: <what>` with `test.fail()` and the steps. For a `BROKE` seen only on live data: steps in the report only.
Rate each break: blocker / major / minor. Name the packet that owns the fix.

## Proof
- [ ] `clumsy.md` has all 13 items with a result each, and a summary count `OK n, BROKE m (blocker a, major b, minor c)`.
- [ ] `cd tools/e2e && npm run e2e -- -Only SCALL` → SCALL-01..06 still pass; any `SCALL-C` scenario is reported as expected-fail.
- [ ] `git status --short` shows only the two files in the write scope from this packet.

## Depends on
P-46.
