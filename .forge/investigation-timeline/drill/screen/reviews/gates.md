# P-44 full gates — report (coordinator run, Run 6 continued)

Run by the orchestrator itself (not an implementer report), against the finished tree at HEAD f6c819c6e52f995f68696e5ab84f3276eb0230f5.
Demo exe for gate 8: `.dev/run/scn/unravis-p42.exe` (all packets + the loop fix). Go toolchain go1.27.3 windows/amd64.
All eight gates ran in the prescribed order from Git Bash, repo root `D:\Projects\api-audit-explorer`. Background bash bbhaghccy, exit 0, `===P44_DONE===`.

| gate | command | result | numbers |
|------|---------|--------|---------|
| 1 | `go build ./...` | PASS | exit 0, no output (clean build) |
| 2 | `go vet ./...` | PASS | exit 0, no output (clean vet) |
| 3 | `go test ./internal/audit/ ./internal/store/ ./internal/casestore/ ./internal/dbhealth/ ./cmd/... -count=1` | PASS | 8 packages ok, 1 no-test-files; exit 0 |
| 4 | `go test ./internal/server/ -count=1` | PASS | ok 140.785s; exit 0 |
| 5 | `cd web && npx svelte-check --threshold error` | PASS | 0 errors, 0 warnings |
| 6 | `cd web && npm test` | PASS | **2999 pass, 0 fail** (baseline 2779 → higher; fail 0) |
| 7 | `cd web && npm run build` | PASS | 125 modules transformed, built in 249ms; exit 0 |
| 8 | `cd tools/e2e && npm run e2e -- -Only DRILL-01,DRILL-02,DRILL-03,SCALL-01,SCALL-02,SCALL-03,SCALL-04,SCALL-05,SCALL-06` | PASS | **9 passed (13.7s)** — DRILL-01/02/03 + SCALL-01..06 |

**8 rows, every row PASS.** Gate 6 pass count 2999 > baseline 2779 and fail 0, as required. No failing test names to list.

---

## Gate 1 — `cd /d/Projects/api-audit-explorer && export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./...`
Last lines of output (build is silent on success):
```
===G1 go build ./...===
G1_EXIT=0
```

## Gate 2 — `go vet ./...`
Last lines of output (vet is silent on success):
```
===G2 go vet ./...===
G2_EXIT=0
```

## Gate 3 — `go test ./internal/audit/ ./internal/store/ ./internal/casestore/ ./internal/dbhealth/ ./cmd/... -count=1`
Last 15 lines of output:
```
===G3 go test audit store casestore dbhealth cmd===
ok  	api-audit-explorer/internal/audit	3.014s
ok  	api-audit-explorer/internal/store	15.440s
ok  	api-audit-explorer/internal/casestore	6.047s
ok  	api-audit-explorer/internal/dbhealth	0.799s
ok  	api-audit-explorer/cmd/audit-explorer	18.830s
ok  	api-audit-explorer/cmd/unravis-blackbox	0.728s
ok  	api-audit-explorer/cmd/unravis-keeper	0.158s
?   	api-audit-explorer/cmd/unravis-perfdata	[no test files]
ok  	api-audit-explorer/cmd/unravis-setup	4.328s
G3_EXIT=0
```

## Gate 4 — `go test ./internal/server/ -count=1`
Last lines of output:
```
===G4 go test server===
ok  	api-audit-explorer/internal/server	140.785s
G4_EXIT=0
```

## Gate 5 — `cd web && npx svelte-check --threshold error`
Last lines of output:
```
===G5 svelte-check===
Loading svelte-check in workspace: d:\Projects\api-audit-explorer\web
Getting Svelte diagnostics...

svelte-check found 0 errors and 0 warnings
G5_EXIT=0
```

## Gate 6 — `cd web && npm test`
Last 15 lines of output:
```
# Subtest: expect ignores a lens that is not inside the new root
ok 2999 - expect ignores a lens that is not inside the new root
  ---
  duration_ms: 93.7221
  type: 'test'
  ...
1..2999
# tests 2999
# suites 0
# pass 2999
# fail 0
# cancelled 0
# skipped 0
# todo 0
# duration_ms 12523.7401
G6_EXIT=0
```

## Gate 7 — `cd web && npm run build`
Last 15 lines of output:
```
vite v8.3.5 building client environment for production...
transforming...
✓ 125 modules transformed.
rendering chunks...
computing gzip size...
../cmd/unravis-setup/ui/index.html                                    3.35 kB │ gzip:  1.56 kB
../cmd/unravis-setup/ui/assets/ibm-plex-mono-latin-400-normal.woff2  14.70 kB
../cmd/unravis-setup/ui/assets/ibm-plex-mono-latin-500-normal.woff2  14.88 kB
../cmd/unravis-setup/ui/assets/ibm-plex-sans-latin-400-normal.woff2  22.58 kB
../cmd/unravis-setup/ui/assets/ibm-plex-sans-latin-500-normal.woff2  24.18 kB
../cmd/unravis-setup/ui/assets/ibm-plex-sans-latin-600-normal.woff2  24.25 kB
../cmd/unravis-setup/ui/assets/index.css                             15.29 kB │ gzip:  3.69 kB
../cmd/unravis-setup/ui/assets/setup.js                              83.58 kB │ gzip: 29.04 kB
✓ built in 249ms
G7_EXIT=0
```

## Gate 8 — `cd tools/e2e && npm run e2e -- -Only DRILL-01,DRILL-02,DRILL-03,SCALL-01,SCALL-02,SCALL-03,SCALL-04,SCALL-05,SCALL-06`
Last 15 lines of output:
```
Running 9 tests using 1 worker

  ok 1 specs\slow-call-screen.spec.ts:52:5 › SCALL-01: a diamond opens the Slow call screen with three lanes and the call's facts (1.4s)
  ok 2 specs\slow-call-screen.spec.ts:73:5 › SCALL-02: Back button, Esc and the browser Back all return to the timeline with the same window (2.5s)
  ok 3 specs\slow-call-screen.spec.ts:111:5 › SCALL-03: a reload on the screen comes back to the same call (893ms)
  ok 4 specs\slow-call-screen.spec.ts:130:5 › SCALL-04: the three lanes fit without an inner scroll at 1440x800 and 1100x760 (988ms)
  ok 5 specs\slow-call-screen.spec.ts:189:5 › SCALL-05: the window drives the lanes and both lists (1.3s)
  ok 6 specs\slow-call-screen.spec.ts:263:5 › SCALL-06: each pretend state shows its own sentence and never the words Query Store is on when it is not (1.2s)
  ok 7 specs\timeline-drill.spec.ts:34:5 › DRILL-01: a click on a red diamond opens the Slow call screen with "Where the time went" and a hint under every section (1.9s)
  ok 8 specs\timeline-drill.spec.ts:62:5 › DRILL-02: a call saved without timings says so (686ms)
  ok 9 specs\timeline-drill.spec.ts:76:5 › DRILL-03: Esc returns to the timeline without another read, with focus inside the chart region; Open in Analysis shows Analysis (1.4s)

  9 passed (13.7s)
G8_EXIT=0
```

---

## Proof checklist (P-44)
- [x] gates.md has 8 rows.
- [x] Every row is PASS (no failing test names to list).
- [x] Gate 6 pass count 2999 > baseline 2779, fail 0.
- [x] `git status --short` lists only gates.md as new from this packet (the report); the generated web/dist + cmd/unravis-setup/ui/assets churn is gate 7's tracked build side effect, not a packet edit. No product file edited by P-44.

## Notes
- Gate 8 ran against `.dev/run/scn/unravis-p42.exe` via `UNRAVIS_EXE` (the run-e2e.ps1 launcher default is unravis-demo.exe; the override pins the packet-complete build). `E2E_REPORT=report/p44-gates.json`.
- This is the coordinator's own gate run (handoff action #4: "Re-run coordinator gates; implementer reports alone are not proof"). P-45 (build rc.35 + silent local upgrade) is gated on this all-PASS **and** RB-6/P-43 VERDICT SHIP. NO install on a red verdict or missing proof.