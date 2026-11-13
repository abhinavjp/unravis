# P-46 Browser acceptance on LIVE data, at both sizes

model: sonnet, effort: medium. Reason: follows a fixed checklist but must judge what it sees and say what is not verified.
risk: medium. Review batch: RB-7. Wave: W9. Requirements: all (R-01..R-19); fills plan.md section 14.

HARD LIMITS: you test and report. You edit no source file and fix nothing. Read-only use of the app: do not delete or change the owner's cases, connections or settings; do not switch anything on in SQL Server. No token, password, user name, host name or record text in the report or in screenshots' file names.

## Outcome
`screen/reviews/acceptance.md` says, line by line, what works on the installed rc.35 with the owner's live sources, what was seen only on the demo, and what could not be checked.

## Write scope
- `.forge/investigation-timeline/drill/screen/reviews/acceptance.md` (new)
- screenshots under `.forge/investigation-timeline/drill/screen/reviews/shots/`

## Must not change
- Everything else.

## Changes
Use the built-in browser on the installed app (the URL the app opens). Viewports: 1440x800 and 1100x760. For each line write `LIVE: seen` / `LIVE: not seen (what happened)` / `LIVE: could not check (why)`, and for demo-only items `DEMO: seen`.
1. Open a live case → Investigation Timeline → click a red diamond. The Slow call screen opens as a full page (no dialog).
2. Three lanes (API, web server, database) are fully visible together, no inner scroll bar, at both sizes; scroll the page: the timeline stays pinned. Screenshot each size.
3. Lane width means time; height means running at once; hover a step: the readout shows numbers; a step with no data is hatched and says not recorded.
4. Window control: presets, custom before / after, Earlier / Later, Back to the call; the lanes and both lists follow; the line under it names the window in plain words.
5. Overview strip: shows two hours; drag the box; arrow keys move it; the caption explains it. Ask: would a first-time user understand it? Quote the caption.
6. Database lane source line and the inspector sentence match the real state of the owner's database (which state is it: live day, export file, API view, off, read-only, late, denied, events only, not read?). Name the state seen. States not available live: `LIVE: could not check`, then check them with the demo's pretend switch → `DEMO: seen`.
7. Nearby calls: the three chips; the columns Database / Redis / Web calls show numbers or "not recorded" (never 0 for missing). Pick one row with a database time, open the same record's detail and check the number is the same.
8. All sources tab is separate from Nearby calls and in time order.
9. Back button, Esc, browser Back: timeline returns with the same window and no reload spinner. Browser Forward returns to the screen. Reload on the screen: same call.
10. Open in Analysis: Run Analysis opens on that call's slot.
11. Words: read every sentence on the screen once. List any that a first-time investigator would not understand, and any that use a banned word (because, due to, caused, waiting on, blocked by, led to, proven).
12. Time taken to open the screen on live data (seconds, by stopwatch from click to lanes drawn).
End the file with three lists: `Verified on live data`, `Verified on demo only`, `Not verified`.

## Proof
- [ ] `acceptance.md` has all 12 numbered items, each with a LIVE or DEMO result.
- [ ] Two lane-fit screenshots exist (one per size).
- [ ] The three closing lists exist; nothing is in "Verified on live data" without a `LIVE: seen` line above.
- [ ] `Select-String -Path <acceptance.md> -Pattern '[A-Za-z0-9_\-]{32,}'` → nothing.
- [ ] If no live source is connected: the whole file says DEMO and the first line is `BLOCKED FOR LIVE: no live source`. Report that to the orchestrator; nothing is ticked in the backlog.

## Depends on
P-45.
