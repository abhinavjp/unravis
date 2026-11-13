# P-48 Tick the backlog, only what is live-verified

model: sonnet, effort: low. Reason: a careful, small text edit driven by the acceptance file.
risk: low. Review batch: RB-7 (orchestrator checks the diff). Wave: W11. Requirement: owner rule in the project CLAUDE.md.

## Outcome
`docs/BACKLOG.md` shows the true state of BL-005..BL-019: ticked only where `acceptance.md` says "Verified on live data"; the rest stay open with a short note.

## Write scope
- `docs/BACKLOG.md`

## Must not change
- Any other item's text or state. The section "Backlog (deferred)". No item is deleted or renumbered.

## Changes
1. Read `screen/reviews/acceptance.md` (three closing lists), `clumsy.md` (blockers / majors), `RB-6-security.md` (verdict), `gates.md`.
2. Use plan.md's requirement table (R-nn → BL-nnn) to map each acceptance item to its backlog item.
3. For each of BL-005..BL-019:
   - on the "Verified on live data" list and no open blocker / major against it → change `- [ ]` to `- [x]` and append ` (done YYYY-MM-DD, rc35, live-verified)` with today's date.
   - on "Verified on demo only" → leave `- [ ]` and append ` (built in rc35; demo-only-verified, live check still owed: <what is missing>)`.
   - not verified or blocked → leave `- [ ]` and append ` (rc35: not verified: <reason>)`.
   - BL-016 (investigation): ticked only if `probe/qs-off-history.md` says "live-checked"; else the note says the investigation is written and the live check is owed.
4. Add new items found by P-46 / P-47 (each `BROKE` blocker or major, each owner question) at the end of the open list with the next free BL numbers; one line each, plain words.
5. If rc.35 was not the version installed, use the real one.

## Proof
- [ ] `git diff -- docs/BACKLOG.md` shows changes only on BL-005..BL-019 lines and newly added lines.
- [ ] Every `[x]` added has a matching line in "Verified on live data".
- [ ] No tick was added for a demo-only item (list them in the report).
- [ ] Not committed.

## Depends on
P-46, P-47.
