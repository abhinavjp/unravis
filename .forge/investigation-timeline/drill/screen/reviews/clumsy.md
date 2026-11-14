# P-47 Clumsy-user pass: try to break the screen

Tested on rc.36 (commit 6dee6e1), not rc.35. Three defects fixed in rc.36 were re-checked under clumsy input and stay fixed (see note below).

How this pass was run. The Slow call screen opens only from a red audit diamond. On the owner's live data the audit database is saved but not connected, so the audit lane draws no red diamond and the screen cannot be opened. No password was typed and nothing was connected (read-only). Screen-specific items run on the DEMO. Timeline-level clumsy actions that work without the screen (resize/zoom, Tab, source/lane toggles, browser Back/Forward, rapid mark clicks) also run on LIVE, on a throwaway copy of the owner config. Each item is marked [DEMO] or [DEMO+LIVE]. Live output is structural only (counts, sizes, states); no name, id, address or record text is stored, and no live screenshot was taken.

## Items

1. [DEMO+LIVE] OK — Double-click and triple-click the audit diamond each open the screen once, with no duplicate and no error; "two diamonds fast" is not applicable (the demo has one audit diamond, SQL/IIS marks only select a slot, and the live timeline draws no marks).
2. [DEMO] OK — Esc many times closes the screen to the timeline and then stays; Esc while the call is still finding, and Esc with focus in the custom minutes box, both close the screen to the timeline.
3. [DEMO+LIVE] OK — Back,Forward,Back,Forward fast steps screen↔timeline with no dead Forward; Back during loading lands on the timeline; reload during loading restores the screen (6 of 6 from a clean tab); the screen's address in a second tab shows the graceful "no session" page; live Back/Forward steps timeline↔case home with no dead step. The known Back-then-reload limitation was seen and is noted below, not re-reported.
4. [DEMO] OK — 0, -5, 99999, letters, empty and 7.5 each show "Type a number from 1 to 120." and leave the window unchanged; pasted " 7 " trims to 7 and applies; Apply with nothing changed is a no-op with no error.
5. [DEMO] OK — Earlier/Later twenty times fast clamp at the data edges (07:00-07:30 and 08:30-09:00) and disable the button; dragging the overview box out of the strip, past both ends and releasing outside, keeps the box inside the strip; a second-button drag does nothing.
6. [DEMO+LIVE] OK — Demo: switching every source off leaves the last one on and disabled (one lane), all-on restores three, and toggling during a read is fine; live: the three lane toggles go off/on and fast all-off/all-on with no error (the one audit source is saved-but-not-imported, so its chip and Select-all are disabled and source on/off does not apply).
7. [DEMO] OK — Three chips fast in a row, and a chip while "asking" is shown, both settle to three rows with every chip enabled; the demo call has a user id, so the no-user-id branch is not reachable on this data.
8. [DEMO+LIVE] OK — 1440→800→1440 and browser zoom 150% (960 css) / 67% (2149 css) show no horizontal overflow; all three lanes fit and the controls stay visible, on both the screen (demo) and the timeline (live).
9. [DEMO+LIVE] OK — Tab reaches every control with a visible focus ring (45 stops on the screen, 32 on the live timeline); arrow keys move the overview box (value 50→55); Enter on a list row selects it.
10. [DEMO] OK — After a full 10-minute idle the screen is still open, the session is alive, and presets, Earlier and the chips still work and return rows.
11. [DEMO] OK — Open in Analysis shows the call's own slot (08:00:00 to 08:01:00), not the default window; Back lands on the timeline, then case home, then start, with no dead step.
12. [DEMO] OK — Every pretend button pressed twice stays on its chosen state; "real" returns to api; a pretend switch while a read is running is accepted.
13. [DEMO] OK — The first and the last diamond each open the screen; a stale call id (record moved on) shows the graceful "could not load" state with Retry and Back to the timeline; the empty-slot "not found" note shares the same Back header (it is not reachable on demo data, which always returns its call).

## Notes

- Known limitation (documented, out of scope, not a new break): Back THEN reload. A call history entry written before a reload reads as an empty screen afterward (a per-page-load life mark), so Forward onto it is a dead step. Seen at item 3; left as the documented limitation.
- rc.36 fixes re-verified under clumsy input and still fixed: R2-01 (Open in Analysis then Back, item 11 — no dead timeline step), R2-02 (Back,Back,Forward,Forward, item 3 — Forward reopens the screen), P46-01 (Open in Analysis shows the call's slot, item 11 — not the default 08:45-09:00).
- Reload-during-loading was checked twice. An early probe that reused one tab across many rapid clear-and-reload cycles alternated screen/Analysis; on the failing tries the active-case key was already missing before the reload. A clean-tab probe (open the timeline once, click the diamond, reload) restores the screen 6 of 6. The alternation is a test-harness state, not a screen defect, so no break is recorded.
- Not reachable on this data: the no-user-id chip branch (item 7, the demo call has a user) and the empty-slot "not found" note (item 13, the demo always returns its call). Both render through the same graceful header that the reachable states use.

## Summary

OK 13, BROKE 0 (blocker 0, major 0, minor 0)

No new deterministic demo break was found, so no `SCALL-C` scenario was appended. SCALL-01..06 are unchanged and run as the regression gate.