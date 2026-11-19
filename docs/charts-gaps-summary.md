# Blackbox charts: gaps closed (19 Nov 2026)

What was built, what was checked, what was not.

## Built (all from the existing `POST /api/blackbox/{id}/series` answer; no new collector, no new route)

1. **What happened** view: a timeline (no recording, crash dumps, restarts, Windows events), a list (dumps, restarts, gaps, recorder notes), Windows events by log, level, ID and count (with a plain meaning for nine well-known IDs), and recording health (state, samples, gaps, unread files, damaged lines). Dumps and restarts are also drawn as dotted lines on the charts.
2. **First-time help**: a 3-step card (can be hidden, comes back with "Show help"), quick questions that pick counters, and "What does this mean?" under the table (click a row; the line is marked on the chart).
3. **Status chips**: Saved copy, Running late, Partly ready. Each has a tooltip.
4. **Scale words**: "Even steps" and "Times-10 steps", each with a tooltip.
5. **Latest compared** and **Everything** views.
6. **Highest at** column in the table.

The demo Blackbox "web-a" now carries two crash dumps (one with a hostile file name), a restart and some Windows events, so these views have something to show.

## Checked

- Web tests: 3,470 pass (new: `blackbox.charts.happened.test.cjs`, 18 tests). `svelte-check` clean.
- Go: new `TestDemoRecordingCarriesWhatHappened`; the series tests pass. `internal/server` still has the known Linux-only failures on master (Windows-only behaviour); count unchanged.
- Browser (real Chromium, demo Blackbox): all four views, light and dark, 720 px wide, keyboard (tab order, Esc closes only the pop-up), empty state (nothing picked), error state (route aborted, then "Try again"), wrong answer shape, 15 min window, Busiest programs tab. No console errors. Three new e2e specs in `tools/e2e/specs/bb-charts.spec.ts` pass on Linux against the demo; CI runs them on Windows.
- Safety: a hostile answer (500 dumps named `"><svg onload=…>`, 300 gaps, 300 notes with HTML kinds, HTML in the state sentence) made no elements, ran no script, drew at most 60 marks, listed at most 60 rows, and showed no folder. No `{@html}` anywhere; the chart tooltip is plain text; the view has one server call (the existing series route, same auth).
- Memory (Chromium, demo, collected heap): 20 open/use/close cycles. Closed-pop-up heap 15.3 MB after the first, 17.0 after 5, 17.4 after 10, 17.8 after 20 (about +0.05 MB per cycle, cache warm-up). DOM nodes (11,084) and listeners (177) flat from cycle 5 to 20. Open for 20 s: 0 growth. The chart object is freed when you leave the Charts view or close the pop-up.
- Speed (demo): pop-up to drawn chart about 120 ms. One long task of about 150 ms (chart set-up). The series read is async and aborts when the window changes.

## Not checked on live data

- Values against Perfmon (BL-145 stays open).
- A real Blackbox with real dumps, restarts and Windows events: the lists come from the recorder's own fields, but only the demo was read. Windows events carry only counts and IDs (the recorder keeps no message text), so the "Meaning" column is filled for nine known IDs only.
- Windows CI, the Windows e2e run, and the narrow-window look on a real small screen.
- Heap numbers were taken on Linux Chromium; Edge on Windows may differ.

## Left out on purpose

- "Compare shapes" (normalising lines) and a palette change (owner decision Q3).
