# Slow call screen: plan (forge-plan, detailed mode)

Planned 2026-11-13. Branch `feat/investigation-timeline` (shared dirty tree, no worktrees). Planner: Fable. Nothing in this folder is built yet.

Source of truth: `prototype/slow-call-popup-WIREFRAME-v3.html`, layout **W1 Workbench**. Owner decisions: `../decisions.md`. Backlog: `docs/BACKLOG.md` BL-005..BL-019.

Layout deviation from forge-plan: this work item uses `packets/` (one file per packet), `graph.md`, `reviews.md`, `orchestrate.md` instead of the `phases/` tree. Reason: the owner asked for very small packets with a dependency graph and batch reviews. In each packet the six headings are in forge order (Outcome, Write scope, Must not change, Changes, Proof, Depends on); "Depends on" is one line after the Proof checklist.

## 1. Goal

Replace the Slow call pop-up with a full-page "Slow call" screen under the nav bar. It shows the API, web server (IIS) and database lanes on one time axis around one slow API call, with one window control that drives the chart, the minimap, both lists and the details. It works on the real data path and on the demo path.

## 2. Scope (requirement IDs)

| ID | Requirement | Backlog | Packets |
|---|---|---|---|
| R-01 | Full-page screen with its own history entry; Back and Esc climb one level to the timeline with the same window | BL-019 | P-03 P-04 P-23 P-25 P-33 |
| R-02 | One window control: ±5/10/15/30/60, Custom 1..120, Earlier/Later, Back to this call, drag on minimap | BL-019 | P-05 P-24 P-26 P-28 |
| R-03 | Three lanes always visible together, no inner scroll, at 1440x800 and 1100x760; timeline pinned | BL-005 BL-009 | P-27 P-32 P-42 P-46 |
| R-04 | Lane height = running at once (busy time / step length), gaps drawn as gaps, never zero | BL-010 | P-06 P-14 P-15 P-19 P-20 P-27 |
| R-05 | Database lane source label and coverage | BL-011 | P-07 P-20 P-21 P-29 |
| R-06 | Query Store states: off, read-only, nothing before a time, permission denied | BL-012 | P-07 P-16 P-29 |
| R-07 | Query Store export file as a database lane source with real volumes | BL-013 | P-21 |
| R-08 | Light per-call totals (database, Redis, web-call ms) in list rows | BL-006 | P-01 P-34 P-35 P-36 |
| R-09 | "Seen by the API" database fallback lane | BL-014 | P-37 P-38 P-39 |
| R-10 | Event markers (system_health, ERRORLOG, HTTP.sys) from imported files | BL-015 | P-10 P-12 P-27 P-31 |
| R-11 | Everyone / Only user / Only API chips, answered by the server | BL-007 | P-08 P-13 P-17 P-18 P-30 |
| R-12 | Nearby calls list tied to the window; server asked when the window is wider than ±15 min | BL-008 | P-08 P-24 P-30 |
| R-13 | All sources in time order list (Nearby activity) kept separate from Nearby calls | BL-009 | P-10 P-31 |
| R-14 | Minimap redesigned for beginners | BL-005 | P-09 P-28 |
| R-15 | Beginner copy, all in `drillCopy.ts`, linted | BL-017 | P-02 |
| R-16 | Open in Analysis hand-off keeps working | BL-019 | P-25 P-33 |
| R-17 | Demo path: "Pretend the database is" switch, demo SQL day | CLAUDE.md | P-22 P-29 |
| R-18 | Older Query Store history when OFF: research only | BL-016 | P-40 |
| R-19 | Proof: unit, e2e, security review, gates, rc.35 local install, browser acceptance, clumsy-user pass, backlog ticks | BL-018 | P-41..P-48 |

## 3. Out of scope

- BL-100..BL-103 (new collector or agent). No live `system_health` reader (none exists; would be new collection).
- No new live SQL read started from this screen. The screen reads what the timeline already saved (`GET /api/cases/{id}/lanes`, `POST /api/cases/{id}/tracing`).
- A visible `#/call` hash address (see D-2). Layouts W2 and W3.
- Deleting `DrillPopup.svelte` (P-49, later, needs owner go). BL-020..BL-025: `packets/later/` only, not in any wave.
- Tag, release, push, merge, commit.

## 4. Work-item mode

Detailed mode (owner confirmed). Why: 49 packets, three Go packages with query changes, navigation state in `appState.svelte.ts`, more than 40 text-pinned tests around `TracingPaper.svelte`, and a live-data rule.

Commit policy (frozen): `commit_granularity: none-by-agents`, `history_style: owner-decides`. Every agent stops before commit. Never push. The orchestrator reports the changed-file list per wave; the owner commits.

## 5. Planner rulings (verified against code; change only with owner go)

- **D-1 Mount point.** The timeline is a sub-view of Screen `'case'` (`CaseHome.svelte` line 409-412 mounts `TracingPaper`). The new screen is mounted inside `TracingPaper.svelte` where `DrillPopup` is today (line 532-536), and the timeline section gets `hidden` while it shows. `TracingPaper` stays mounted, so Back returns with the same window and no re-read. `CaseHome.svelte` is not edited (many tests pin its lines).
- **D-2 Address.** History mark `CASE_CALL_MARK = 'case/tracing/call'` plus session memory, not a visible hash. Reason: the view-sync effect (`appState.svelte.ts` 441-449) rewrites the hash from `currentView`, which is null on the case screen, and `initialView != null` routes a reload to `'running'` (line 601). This differs from the BL-019 wording "an address in `lib/wayfinding.ts`"; `createViewHistory.markScreen/stepBack` from that file are still what moves history.
- **D-3 `parseScreenMark` shape stays** `{screen, tracing}` (`caseTimelineHistory.test.cjs` 40-43 uses deepEqual). The call mark parses as `{screen:'case', tracing:true}`; a new `isCallMark` tells the two apart.
- **D-4 Data.** No new endpoint for lanes. `POST /api/cases/{id}/tracing` gains `busyMs` per bucket (and `blockSeconds` per lane). Running at once = `busyMs / (bucketSeconds * 1000)`. The audit rollup already computes the sum (`RollupWireBin.Sum`, `internal/audit/rollup_wire.go:27`); `latencyAcc.add` ignores it today.
- **D-5 Bucket width is not always one minute.** `tracing_axis.go`: 60 s only while the hour-aligned axis has ≤ 120 buckets; a ±60 min window usually gets 300 s. The UI reads `window.bucketSeconds`; copy says "time step", never a fixed minute.
- **D-6 Two tracing reads.** One for the overview strip (call ±60 min, read once) and one for the visible window (read when the window settles, never during a drag). ±60 reuses the overview read.
- **D-7 Busy time is counted in the step where the call started** (Little's law approximation). The screen says so in "What this does not show".
- **D-8 Query Store state** becomes a structured `queryStore` object on the saved lane day (`dbhealth.LaneResult`), filled from the two queries that already run (`qLaneQSOptions`, `qLaneQSFirst`). Old saved days lack it; the UI then says the database was not read for this time.
- **D-9 "Read-only since <time>" and "switched on at <time>"** are not knowable from the existing queries. Real copy says "read-only" and "nothing before <first data time>". The demo switch may show the wireframe wording.
- **D-10 Copy** lives in `DRILL_TEXT` / `DRILL_HELP`; P-02 is the only writer. `DRILL_HELP` grows from 18 to 26 keys and the test count is updated.
- **D-11 Old pop-up kept on disk** (`DrillPopup.svelte`, `drill.popup.test.cjs`, `drillOpen`, `setDrillOpen`) so switching back is one packet. Only its mount is removed.
- **D-12 Overview span is ±60 min around the call**, not "the hour" of the wireframe demo. Custom windows above ±60 hide the strip.

## 6. Technical dependencies

- Go toolchain: from repo root, Bash: `export PATH="$PWD/.dev/toolchains/go/bin:$PATH"`. Import graph: `store -> audit`, `keeper -> store, audit`, `server -> keeper, store, audit`. One Go writer per package at a time.
- Web: Svelte 5.57, Vite 8, TypeScript 5.9. Tests `node --test tests/*.test.cjs` (2779 pass before this work). New `.cjs` tests copy the `loadTypeScript` preamble from `web/tests/drill.copy.test.cjs` lines 1-24; `.svelte` files are tested as text.
- Endpoints used (all exist): `POST /api/cases/{id}/tracing`, `GET /api/cases/{id}/lanes`, `GET /api/cases/{id}/events`, `POST /api/activity`, `POST /api/slowest`, `POST /api/record`, `GET /api/sources/{id}/fields`. All decode strictly (unknown field = 400).
- Files: see each packet. Shared hot files with a single writer: `drillCopy.ts` (P-02), `appState.svelte.ts` (P-23), `TracingPaper.svelte` (P-33), `SlowCallScreen.svelte` (P-25 then P-32), `internal/server/tracing.go` (P-14, P-19, P-20, P-21, P-39 in that order).
- e2e: `tools/e2e` (Playwright, one demo exe per spec, 1440x900, UTC). Run: `cd tools/e2e && npm run e2e -- -Only <IDS>`.
- Build: `powershell -File build.ps1 -Version 0.1.0-rc.35`; installer `dist/unravis-setup.exe --silent --config setup.json` (keys in `docs/user/install.md`).
- Gates: G-go `go build ./... && go vet ./... && go test ./internal/audit/ ./internal/store/ ./internal/server/ ./internal/keeper/ ./internal/casestore/ ./internal/dbhealth/` (server tests about 2.5 min, run in background). G-web `cd web && npx svelte-check --threshold error && npm test`.

## 7. Skills and tools per stage

| Stage | Skill or tool |
|---|---|
| Model packets (P-03..P-12), Go packets | `superpowers:test-driven-development` (test first, see it fail, then code) |
| UI components (P-25..P-32) | `impeccable` for spacing, hierarchy and copy check against the wireframe; `prototype` is not needed (wireframe v3 is the prototype) |
| Every packet before reporting | `superpowers:verification-before-completion` (run each Proof command, paste the output line) |
| Review batches | `skill-engineer:merge-sentinel` or `code-review` on the batch diff; `security-review` for P-43 |
| P-46, P-47 | Browser pane tools (`mcp__Claude_Browser__*`): resize to 1440x800 and 1100x760, screenshots, console errors |

## 8. Ground rules for every implementer (the prompt template repeats them)

1. Edit only the files in your packet's Write scope. No commit, stash, revert, push, tag.
2. Never read `.env` or anything under `sql/`. Never log record text, tokens or passwords. No fixture holds a credential.
3. No write to the customer database. No code path may run `ALTER DATABASE ... SET QUERY_STORE`. MongoDB 3.4 operators only.
4. Edit by anchor text; do not reformat or reorder untouched code. Many tests pin source text.
5. `null` means "not recorded". Never turn a missing number into 0.
6. Tick every Proof item with the command output line as evidence. An item you could not run is reported as UNMEASURED with the reason, never ticked.
7. If an anchor in the packet is not found, stop and report; do not improvise outside the Write scope.

## 9. Risks

| Risk | Level | Guard |
|---|---|---|
| `appState.svelte.ts` navigation (Esc, Back, reload) regresses the timeline | high | P-23 text tests + e2e SCALL-02/03; Opus review RB-4; rollback = revert the P-23 hunks |
| `TracingPaper.svelte` edit breaks one of about 20 text-pinned tests | high | P-33 touches 4 anchors only; full `npm test` in its Proof |
| Mongo activity filter lets a hostile value through or skips the `$or` overlap branch | high | P-17 applies the filter in its own `$match` after the overlap stage; P-43 Opus security review |
| Real perf field names do not exist in the audit records | medium | P-01 finds out first; P-34..P-39 stop cleanly and the lane says "Not recorded" |
| `casestore` timing sum not cheap to add | medium | P-15 reads the code first; fallback written in the packet |
| IIS log time is the request END (`w3cTimeIsEnd`, marked UNVERIFIED in `importer/iis/w3c.go:14-27`) | medium | bars drawn end-anchored; said in the inspector; flagged in acceptance |
| Lane-fit rule broken by real content (long labels, many bar rows) | medium | bar rows capped at 3 per lane; e2e measures at both sizes |
| Reading level lint rejects long copy | low | P-02 may split a sentence, never change meaning; reports final text |
| Weekly usage | medium | wave pacing and review caps in `orchestrate.md` |

## 10. Assumptions for the owner to confirm (work proceeds on these)

- A-1 Layout W1 Workbench. Switching stays cheap: every region is its own component; only P-32 holds the layout.
- A-2 Nearby calls and All sources stay two tabs. Merging later = one list component reading both models.
- A-3 Overview strip covers ±60 min around the call (D-12), and its columns are 1 or 5 minutes wide (D-5).
- A-4 No visible hash address in v1 (D-2).
- A-5 The screen does not start a new database read; it points back to the timeline for that.

## 11. UNVERIFIED (each has a "find it first" step in its packet)

- Real field names for SQL/Redis/HTTP per-call totals (P-01).
- Unit of `RollupWireBin.Sum` (assumed ms of `_auditMetricDuration`) and whether it covers only timed calls (P-14).
- How `casestore.CountBuckets` computes timings and whether a sum is cheap (P-15).
- Whether saved `LaneResult` JSON is persisted in a form that tolerates a new field (P-16).
- Interval length of a held Query Store series when only one point exists (P-20).
- How `.dev/run/scn/unravis-demo.exe` is built for e2e (P-41).
- Silent upgrade of an existing install: exact flags and `setup.json` keys (P-45).
- Whether Query Store history stays readable when OFF (P-40; a live answer needs a database in that state).
- `w3cTimeIsEnd` (see risks).

## 12. Phases, waves, integration points

Phases: A models and copy (P-01..P-12), B backend (P-13..P-22), C screen (P-23..P-33), D light totals, blocked on the probe (P-34..P-39), E proof (P-40..P-48). Waves and the critical path: `graph.md`.

Integration points (cross-phase proof):
- I-1 wire contract `busyMs` / `blockSeconds`: Go struct tags (P-14, P-19, P-20, P-21) = TS types (P-11) = model input (P-06). Proof: P-44 runs a demo tracing call and the P-06 test fixture uses the same field names.
- I-2 activity filter: `userId`, `operation` in `audit.ActivityInput` (P-13) = `types.ts` (P-11) = controller call (P-24). Proof: server test in P-18 posts the exact JSON the UI sends.
- I-3 history mark: `CASE_CALL_MARK` (P-03) used by P-23 and by e2e SCALL-02.
- I-4 `queryStore` object: Go tags (P-16) = `dbState.ts` input (P-07).
- I-5 `light` row object: Go tags (P-35) = `types.ts` (P-11) = `nearbyCalls.ts` (P-08).

## 13. Review plan

Seven batches, see `reviews.md`. Cap 5 rounds per batch, one fixer pass per round, re-run only unclear reviewers.

## 14. Final acceptance checklist

P-46 fills this in; P-48 ticks the backlog from it. An item is "live-verified" only when seen on the installed rc.35 with a live source.

Verified on live data (target):
- [ ] Diamond on the timeline opens the Slow call screen for a real call; header facts match the record.
- [ ] Back, Esc and browser Back return to the timeline with the same window, no re-read.
- [ ] Reload on the screen returns to the same call.
- [ ] API lane shows running at once from real audit calls; a gap is hatched.
- [ ] IIS lane shows running at once from imported IIS logs (needs a case with IIS files).
- [ ] Database lane shows a saved Query Store day with its source label, in blocks.
- [ ] Query Store state sentence matches the real database state.
- [ ] Nearby calls: window drives the list; chips ask the server; wider window asks the server.
- [ ] Light totals columns show real numbers, or the screen says "not recorded" and P-01's report says why.
- [ ] Three lanes fully visible at 1440x800 and 1100x760 with no inner scroll; timeline stays pinned.
- [ ] Open in Analysis opens Run Analysis on the call's slot.

Demo only (cannot be live-verified without a matching database or files):
- [ ] The eight database states through "Pretend the database is".
- [ ] Query Store file source, unless a real export file is imported.
- [ ] Event markers, unless real XEL / ERRORLOG / HTTPERR files are imported.
- [ ] "Seen by the API" lane, unless P-01 finds the field.
