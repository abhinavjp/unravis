# P-46 — Browser acceptance of the Slow call screen, on the installed rc.35

App under test: the installed Unravis 0.1.0-rc.35 (user scope), running in **Live mode** against the owner's own
sources. A second pass ran the same rc.35 binary in **demo mode** (its own empty data folder, free port) because the
Slow call screen could not be opened on the live case (see the limitation below). Both passes used a real Chromium
browser pointed at the app's own URL, at viewports **1440x800** and **1100x760**. Nothing in the app was changed:
no case, connection, setting or database was edited; no password was entered; nothing was switched on in SQL Server.

## The live limitation (read first)

The owner's live case has real imported data: eight IIS web logs and saved SQL Server logs. On the Investigation
Timeline the **database lane drew 48 non-empty bars and 14 red diamonds**, and the **web-server lane drew 46 non-empty
bars and 26 red diamonds** — live tracing works. But the **API audit lane is "saved, not connected"**: its database is
saved on this computer without a saved password, so no API requests were read and the lane is hatched with the reason
"This case saved audit databases that are not connected on this computer, so no API requests were read."

Only an **API-lane** red diamond opens the Slow call screen (the click handler matches the audit lane's flagged marks
only). The SQL and IIS diamonds are markers; clicking them selects a time slot, it does not open the screen. With the
audit lane empty there is **no diamond that opens the screen on this live case**, so every item that needs the screen
is `LIVE: could not check` and was verified on the demo instead. Connecting the saved audit database (entering its
password) is the owner's decision; it was not done here.

Live evidence shot: `shots/live-timeline-lanes-1440x800.png` (three lanes, live SQL + IIS data, hatched audit lane).

## The twelve items

1. **Open a live case, timeline, click a red diamond, screen opens as a full page.**
   `LIVE: could not check` — the live case's audit lane has 0 red diamonds (saved, not connected), and only audit
   diamonds open the screen; clicking a SQL diamond (14 present) did not open it.
   `DEMO: seen` — an audit diamond opened the screen as a full page; open dialog count = 0 at both sizes.

2. **Three lanes fully visible together, no inner scroll, at both sizes; page scroll keeps the timeline pinned.**
   `LIVE: could not check` (screen not openable live).
   `DEMO: seen` — at 1440x800 the three lanes occupy y 346-606 (87 px each) inside an 800 px viewport; at 1100x760 they
   occupy y 346-566 (73 px each) inside 760 px. Every ancestor from the pinned block to the screen has
   scrollHeight == clientHeight and overflow-y visible (no inner scroll bar). After scrolling the page 400 px the pinned
   block's top is 345 (still in view) at both sizes. Screenshots: `shots/demo-lane-fit-1440x800.png`,
   `shots/demo-lane-fit-1100x760.png`.

3. **Lane width = time, height = running at once; hover readout shows numbers; no-data step hatched + "not recorded".**
   `LIVE: seen (timeline only)` — the live web-server lane shows hatched "not recorded" where it has nothing and the
   audit lane is fully hatched with its reason; live SQL/IIS bars carry real counts.
   `DEMO: seen (screen)` — legend reads "Bar width = how long it took / Faint shape = how many were running at once /
   Hatched = not recorded, which is unknown, not zero". Hovering a step filled the readout with numbers
   ("API calls running at once 3.2", "slowest 5% (P95) 1 min 6 s") and "not recorded" for the web lane.

4. **Window control: presets, custom before/after, Earlier/Later, Back to the call; lanes and both lists follow; plain-word window line.**
   `LIVE: could not check`. `DEMO: seen` — ±5 then ±15 changed both the window line and the nearby hint; Custom 7/9 gave
   "Showing 07:53 to 08:09 (7 min before, 9 min after this call)"; Later gave "moved away from this call" and enabled
   "Back to this call", which returned to "15 min before, 15 min after". The line always names the window in plain words.

5. **Overview strip: two hours, drag the box, arrow keys move it, caption explains it.**
   `LIVE: could not check`. `DEMO: seen` — the strip is present with the caption:
   "The two hours around this call, one column per 5 minutes. The outlined box is what the big chart shows. The red line is the slow call."
   Dragging the box started **0** reads while dragging and **1** read on release, and the window line changed
   ("Showing 07:56 to 08:26 (moved away from this call)"); an arrow key also moved the window. A first-time user can
   follow the caption: it names the two hours, the column size, the box and the red line without jargon.

6. **Database lane source line + inspector sentence match the real state; name the state; unavailable states via demo pretend.**
   `LIVE: seen (timeline states)` — database lane: "From saved logs" (saved SQL log export); web lane: "From saved logs";
   audit lane: "saved, not connected"; one listed SQL file source: "not imported". These are the real live states.
   `LIVE: could not check (screen inspector)`. `DEMO: seen` — the screen's real state was "Seen by the API" with its own
   sentence; the pretend switch showed all nine states, each with a distinct sentence: Query Store live, Query Store file,
   Seen by the API, off, read-only, switched-on-later, permission denied, event markers only, not read. The off / denied /
   not-read states drew **no** database shape and showed the short "not recorded" note; event-markers drew no shape and no
   note; none of them ever claimed Query Store was on.

7. **Nearby calls: three chips; Database / Redis / Web calls show numbers or "not recorded" (never 0 for missing); row-detail cross-check.**
   `LIVE: could not check`. `DEMO: seen (chips + columns)` — chips "Everyone / Only user … / Only …" (three); a row showed
   Database 3.6 s, Redis 3.6 s, Web calls 5.4 s, while rows without those readings showed "not recorded" (never 0).
   Cross-check: the "this call" row's Took (68.0 s) equals the "About this call" total (68.0 s), and a clicked row's Took
   (60.0 s) equals the Selected detail's total (60.0 s). **Not completed:** the per-call Database figure (3.6 s) is not
   restated in the Selected detail, so that specific number could not be cross-checked against the record detail.

8. **All sources tab separate from Nearby calls and in time order.**
   `LIVE: could not check`. `DEMO: seen` — a separate tab ("All sources in time order (3)") distinct from "Nearby calls";
   its rows are oldest-first by time (all 08:00:00 UTC here, correctly ordered).

9. **Back button, Esc, browser Back return to the timeline with the same window and no reload; Forward returns; reload keeps the call.**
   `LIVE: could not check`. `DEMO: seen` — Back button, Esc and browser Back each closed the screen, showed the timeline,
   kept the same window line and started **no** new tracing read; browser Forward reopened the screen; a reload on the
   screen returned the same call. **However the history dead-steps reproduce (demo, real browser):** after
   "Open in Analysis", Back landed on the timeline and a second Back stayed on the timeline (a dead step) before a third
   Back left it; and Back, Back, Forward, Forward ended timeline, timeline (the second Forward did nothing). These are the
   deferred history defects R2-01 and R2-02, confirmed still present.

10. **Open in Analysis: Run Analysis opens on that call's slot.**
    `LIVE: could not check`. `DEMO: not confirmed` — the button opened Run Analysis, but the analysis range line read the
    default last-15-minutes window (08:45-09:00), not the call's 08:00 slot. The call's-slot handoff could not be confirmed
    from the range line; flagged for follow-up (this is the RB-4 "Open in Analysis hour" unproven item).

11. **Words: read every sentence once; list unclear or banned wording.**
    `DEMO: seen` (full screen text read). **No banned word** appears anywhere on the screen (because, due to, caused,
    waiting on, blocked by, led to, proven — none present). Candidate terms a first-time investigator may need explained:
    "slowest 5% (P95)", "running at once", and "Seen by the API (time inside the database)". Each has a nearby explainer
    ("How to read the chart", the lane source sentence), so they are acceptable but worth a glossary link. All missing
    readings say "not recorded … unknown, not zero", never an invented 0.

12. **Time to open the screen on live data (click to lanes drawn).**
    `LIVE: could not check` (screen not openable live). `DEMO: seen` — 46 ms at 1440x800 and 84 ms at 1100x760 from the
    diamond click to the lanes being drawn (well under a second; no spinner observed).

## Verified on live data

- Live tracing draws real SQL and IIS lanes with counts and red diamonds (48 / 46 non-empty bars; 14 / 26 diamonds).
- The audit lane's "saved, not connected" hatched state and its plain-word reason render correctly on live data.
- The "not imported" source state renders for a listed-but-never-imported SQL file.
- Hatched = not recorded (unknown, not zero) is shown on live lanes that have no data.

## Verified on demo only

- Screen opens as a full page from an audit diamond (no dialog), at both sizes.
- Three-lane fit with no inner scroll and a pinned timeline at 1440x800 and 1100x760 (lane heights 87 px / 73 px).
- Hover readout numbers; hatched / not-recorded steps; legend wording.
- Window presets, custom before/after, Earlier/Later, Back to the call, plain-word window line.
- Overview strip caption, drag reads only on release, arrow-key move.
- All nine database states with distinct sentences; off/denied/not-read draw no shape; never claims Query Store on.
- Nearby chips and Database/Redis/Web columns (numbers or "not recorded"); total-time row-to-detail cross-check.
- All sources tab separate and in time order.
- Back / Esc / browser Back / Forward / reload behaviour with no extra tracing read.
- No banned words; missing readings never shown as 0.
- Open time 46-84 ms.

## Not verified

- The whole Slow call screen on LIVE data (blocked: the live case's API audit database is saved but not connected, so no
  audit red diamond exists; only audit diamonds open the screen). Owner action: connect the saved audit database.
- Open in Analysis landing on the call's own slot (demo showed the default range instead) — **now FIXED in rc.36 (P46-01); see "Fixer pass re-verification" below.**
- The per-call Database number cross-checked against the record detail (the detail does not restate it).
- The all-sources double-count when two saved days share an hour (deferred F-03 / SEC-01): not exercisable on the current
  live case or the demo; remains open.
- The history dead-steps are confirmed present (R2-01, R2-02) but not fixed; they stay in deferred.md for a fixer pass. **Now FIXED in rc.36; see "Fixer pass re-verification" below. One sub-case (Back then reload) remains out of scope.**

## Fixer pass re-verification (rc.36) — R2-01, R2-02, P46-01 now FIXED

The three defects above (items 9 and 10) were fixed through the owning packet (appState.svelte.ts, P-23) by one bounded
fixer, then re-verified by the coordinator in a real Chromium browser on the **rc.36 demo exe** (own temp config + free
port; the live config/session was untouched; the launch token was read at runtime and never written to any report). The
installed app was then silently upgraded rc.35 → rc.36 (cases preserved, 2 = 2). Gates re-run by the coordinator: targeted
slowcall+drill 110 pass, svelte-check 0 errors, full npm test 3002 pass / 0 fail, Go build/vet/test exit 0.

- **Item 9 — R2-01 (Open in Analysis then Back): FIXED.** A one-shot flag replaces the call's history entry with the
  analysis entry instead of pushing a duplicate timeline. Real-browser Back sequence after Open in Analysis:
  screen → analysis → timeline → analysis → cases. Every Back moves; no dead step on the timeline.
- **Item 9 — R2-02 (Back, Back, Forward, Forward): FIXED.** The call target is kept in history and only re-stored when a
  call is actually shown, so Forward reopens the screen. Real-browser sequence: screen → timeline → other → timeline →
  screen (the second Forward reopens, no dead step). **Remaining limitation (out of scope, not fixed):** "Back *then
  reload*" — a call entry written before a reload reads as an empty screen afterward (a per-page-load life mark in
  wayfinding.ts), so Forward onto it is still a dead step. Fixing that needs wayfinding.ts/screenFlow.ts, outside this
  fixer's write scope; it stays in deferred.md.
- **Item 10 — P46-01 (Open in Analysis lands on the call's slot): FIXED.** A one-shot flag makes Open in Analysis run the
  handed-off window. Real-browser analysis range line after Open in Analysis: **"Showing 20 Oct 08:00:00 to 08:01:00 UTC"**
  — the call's 08:00 slot, not the 08:45-09:00 default — and the same line after a reload.
- **Focus return (RB-4 unproven item): SEEN.** After the screen closes, focus lands on the chart overlay (a DIV), not the
  document body.
- **Reload after Forward (RB-4 unproven item): SEEN.** Forward reopened the screen, then a reload restored the same call
  (same "About this call" facts and the same window line).

Still demo-only: the live case's API audit database remains saved-but-not-connected (P46-03), so the screen is still
unreachable on live data; all of the above is verified on the rc.36 demo. Owner action to enable live screen acceptance:
connect the saved audit database, then re-run P-46 live. P46-02 (nearby per-call figure not restated in the Selected
detail) remains open as recorded above.