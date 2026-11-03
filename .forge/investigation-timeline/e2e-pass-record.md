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

## Phase 07 (background-run)

Exe: `dist\unravis.exe` from `build.ps1` (GUI subsystem, stamped `v0.1.0-rc.10-5-g3a3fe96-dirty`). Build ran the web check/build, `go test ./...` and `go vet ./...` clean and passed the PE subsystem checks. Branch feat/investigation-timeline. Nothing committed.

| Check | Result | Date |
|---|---|---|
| `build.ps1` (npm ci, check, build, go test, go vet, five exes, subsystem check) | PASS | 2026-11-03 |
| Main + tracing e2e, `run-e2e.ps1 -Exe dist\unravis.exe` (--demo --no-browser) | 324 of 329 pass; 5 fail, all because this exe is version-stamped, not Phase 07 (see below) | 2026-11-03 |
| Re-run of those 5 ids (HELP-03, UPD-04, UPD-05, UPD-05a, UPD-06) on an unstamped GUI build of the same tree (`-Only`) | 5 of 5 PASS | 2026-11-03 |
| Smoke a: `--version` prints and exits 0 (redirected stdout, 21 ms) | PASS | 2026-11-03 |
| Smoke e: `--background` starts, listens, writes session.json, no browser child process, support log says `launch=none` | PASS | 2026-11-03 |
| Smoke b: second `--background` exits 0 in 17 ms, no new server or process, session port unchanged | PASS | 2026-11-03 |
| Smoke c: `--quit` from a second process: exit 0, first copy exits 0 within 10 s, session.json removed | PASS | 2026-11-03 |
| Smoke d: `--quit` with nothing running: exit 0 and "Unravis is not running." | PASS | 2026-11-03 |

The 5 stamped-build failures: the UPD-04/05/05a/06 specs expect a development build (`data-state` "dev") and get "unreachable" because a stamped version tries the network update check; HELP-03 compares the build line "(build abc, date)" to `--version` "(commit abc, built date)". The same 5 specs pass on an unstamped build, and none touches Phase 07 code. Known defects DOC-02 and KEEP-09 stay expected-fail; the archive suite was not run (cannot run, see above).

Smoke notes: `tools/e2e/phase07-smoke.ps1 [-Exe <path>]`. `--no-browser` never reaches the single-instance gate (main.go), so checks b, c, d use `--background` for the first and second copy. The script uses a temp `AUDIT_CONFIG_DIR` and `--port 0`, and removes its processes and temp dir. If another Unravis already holds the per-user instance name, it marks b, c, d, e BLOCKED instead of running `--quit` against that copy. Manual tray, shortcut and sign-in checks were not run here and stay PENDING in `docs/background-run-acceptance.md`.

## UI loop round 1 (2026-11-04)

Unstamped gate exe rebuilt after the round-1 fixes (commit 45cb98e). Full e2e: 327 of 329 pass; 2 flaky (KEY-07 archive-folder race, HAND-17 geometry) pass alone 3 of 3 each. TRACE/CASE/HAND subset 81 of 81. Go `go test ./...` and Linux vet clean; web check 0 errors, 2143 node tests, build and distChunks pass. Findings table: `ui-loop/review-r1-scenarios.md`. Known defects DOC-02, KEEP-09 unchanged.

## UI loop round 2 (2026-11-04)

Build: unstamped plain `go build` after Go sweep round 2 (`5eb00ea`) and UI-R2-01..12 web fixes. TRACE, CASE and HAND subset: 81 of 81 pass. First run had CASE-02 fail on a Windows `ERR_NO_BUFFER_SPACE` asset load; re-run of CASE-02 and HAND-17 passed. HAND-17 is flaky (measures right after first paint). UI-R2-13 (demo-only provenance time) skipped; UI-R2-03 (closed-case timeline) awaits owner decision.
