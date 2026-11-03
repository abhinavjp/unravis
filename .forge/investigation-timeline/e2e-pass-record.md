# Phase 05 end-to-end pass record (3 Nov 2026)

Exe: `.dev\run\scn\unravis-gate.exe` (demo build with the latest web build). Branch feat/investigation-timeline.

## Results

| Suite | Ids | Result | Date |
|---|---|---|---|
| Web type check (`npm run check`) | 409 files | 0 errors, 0 warnings | 2026-11-03 |
| Web unit tests (`node --test tests/*.test.cjs`) | 2107 tests | 2107 pass | 2026-11-03 |
| Web build + `distChunks.test.cjs` | 2 tests | pass; index 520.59 kB (gzip 168.62), charts 654.44 kB (gzip 218.39), same as before Phase 05 | 2026-11-03 |
| Go `./internal/server/...` | all | pass (105 s) | 2026-11-03 |
| Main e2e, full | 329 tests | 327 pass; HELP-08 and HAND-16 failed once under load and passed on re-run alone (HAND-16 now waits for the page before measuring). Known defects DOC-02, KEEP-09 stay expected-fail. 0 skipped, no `fixme` | 2026-11-03 |
| Timeline screen | TRACE-01..16 | all pass | 2026-11-03 |
| Bar hand-off and case home | HAND-01..19 | all pass | 2026-11-03 |
| Guided archive (informational) | `-Archive` | cannot run on this build: the archived specs open Guided through the case-home button "Open guided analysis", which WP-64 removed, so each times out after 60 s (22 of 22 tried failed; run stopped). The old 75 of 80 baseline was before WP-64. Needs the owner decision on an entry hook (see docs/guided-unlisted.md) | 2026-11-03 |

## Re-run only the new screens

```
cd tools/e2e
powershell -NoProfile -ExecutionPolicy Bypass -File run-e2e.ps1 -Exe <path to unravis exe> -Only "TRACE-01,TRACE-02,TRACE-03"
```

Name every id you want, comma separated (TRACE-01 to TRACE-16, HAND-01 to HAND-19); a range is not accepted. Add `-Archive` for the guided archive suite.

## What each spec file covers

- `specs/tracing-screen.spec.ts` (TRACE-01..16): the Investigation Timeline screen. Region order, three lanes and the shared UTC axis, bars, lane toggles, zoom and brush, scrub pill, P50/P95 line, tooltips, window checks, empty/failed/cancel states, no-source case, privacy (one read-only request, windows only), UTC/Local switch, hidden tooltip not drawn.
- `specs/tracing-handoff.spec.ts` (HAND-01..19): case home and hand-off. Start tracing, click a bar to open the analysis with a 5-minute window, empty and hidden-lane bars, double click, reload, back to case home, 375 px, keyboard, closed and saved cases, no forbidden endpoints, wireframe order and colours, a real mouse click, narrow lane labels.
- The other 30 files in `specs/` are the earlier suites, unchanged in purpose. The old guided specs moved to `archive/guided/`.

## Wireframe match

Verdict: the screens match the wireframe in order, text, tokens and now lane colours (bars use the `--lane-*` tokens). Remaining gaps are the triage "B" items (out of scope or superseded), HTTP.sys markers are not drawn and wait for the owner to confirm, and the P95 right axis has no `0` tick (latent: the server sends no latency figures yet).

## Defects found by these specs and fixed

Bar click lost to pointer capture, hidden tooltip box still drawn (`.tip[hidden]`), lane labels overlapping panels under 480 px. Lane colours were added at the same time.

## Related notes

- [gate-checklist-phase05.md](gate-checklist-phase05.md)
- [wireframe-match-tracing.md](wireframe-match-tracing.md)
- [wireframe-gap-triage.md](wireframe-gap-triage.md)
