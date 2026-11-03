# Investigation Timeline (spec name: "tracing") — build plan, MVP-1

mode: detailed
branch: `feat/investigation-timeline` (cut from `master` at `94ea98e`; master carries ~269 unrelated CRLF-modified files, see Commit policy)
status: rounds 1-3 reviewed, round-3 minors fixed; Phase 07 background-run (35 packets) authored from the owner answers, Phase 07 round-1, round-2 and round-3 findings fixed (round-3 fixes applied: profile links by location, worker writes no log, shared after-worker function for window and silent mode), check-plan OK (79 packets)

## 1. Outcome, scope, exclusions

Outcome: from a case, the user presses **Open the Investigation Timeline**, picks a window of up to 30 days (UTC), and sees one aligned time axis with an audit lane, a SQL lane and an IIS lane (with HTTPERR markers), a toolbar, tooltips from a plain-word dictionary, and a bucket click that hands off to the existing analysis. IIS client IP, referer and allowlisted cookie digests are stored but never leave the machine in an export unless the user turns the scrub off.

In scope: spec MVP-1 = WP-01..20, 22, 23, 25, 45, 58, 59, 60..64, 65a, 65b, 65 (44 packet files after splits and the added WP-17c) plus Phase 07 background-run, WP-70a..77 (35 packet files: a windowless `unravis.exe` with tray icon, log file and single instance, start at sign-in, Desktop and Quit shortcuts, quit on upgrade). Total 79 packet files, 12 waves, 7 phases.
Out of scope (later plans, not packetised here): WP-21, WP-24 (Later); MVP-2 WP-26..38 (saved pulls, schema v5, rollups, coverage); MVP-3 WP-39..44 (host metrics, Mongo profiler, MatchAgents); Bench WP-46..57. Deferring them is a scope decision, not a verdict that they are unneeded.

Source basis (read-only, scratch copy of branch `docs/tracing-paper-spec-round6`):
- `.dev/spec-src/2026-10-30-tracing-paper-impl-spec.md` (the "spec"; §8 requirements TR-xx, §9.2 packet table rows at lines 298-367, Appendix P at 4183-4597)
- `.dev/spec-src/2026-10-31-tracing-paper-round6-owner-decisions.md` (R6-02 UTC floor-aligned axis, R6-03 409 window-overlap approved 2026-10-31)
- wireframe: `prototype/tracing-paper-PROTOTYPE.html`, `prototype/screenshots/08-tracing.png`, `08a-tracing-lane-charts.png`, `09-analysis-from-tracing.png`, `13-narrow-tracing.png`; naming per `prototype/FLOW-COVERAGE.md` row 64j (user-facing name "Investigation Timeline"; code and routes say "tracing")

Conflict rule: if spec and code disagree, the packet author records it in the packet's `Changes` and the implementer follows the packet; a real contradiction is a blocker (workflow contract). Where the spec flags an item "(unverified)", the packet author opened the file and wrote the verified fact into the packet.

## 2. Requirement traceability (TR ids are the spec's §8 ids; do not renumber)

| TR | Phase | Packets |
|---|---|---|
| TR-01 Case-home entry | 04, 05 | WP-16b, WP-64 |
| TR-02 window ≤30d UTC | 03 | WP-15 |
| TR-03 read-only start | 03 | WP-14 |
| TR-04 three lanes + consistency | 03, 04 | WP-17a, WP-17b, WP-18b |
| TR-05 shared UTC axis, local toggle | 04 | WP-23 |
| TR-06 bucket ladder, aligned axis ≤120 | 03 | WP-15 |
| TR-07 missing-lane reasons | 03 | WP-13, WP-18b |
| TR-08 overlap warning | 04 | WP-22 |
| TR-09 bucket click hand-off | 05 | WP-61 |
| TR-10 50k cap, one `$match`+`$group` audit aggregate | 03, 04 | WP-15, WP-17a |
| TR-11 TZ validated | 03 | WP-14 |
| TR-12 TracingPaper is the surface, CallTrace tray untouched | 04 | WP-11, WP-16b |
| TR-13 no grants/imports from tracing API | 03 | WP-14 |
| TR-15 `ProjectEventAttrs` on every outward view | 01 | WP-07a |
| TR-16 >24h hourly footnote | 04 | WP-23 |
| TR-18 E2E three lanes on seeded demo | 03, 06 | WP-25, WP-58 |
| TR-19 store IIS IP + referer | 01 | WP-02a, WP-02b, WP-03, WP-05 |
| TR-20 cookie HMAC digest | 01 | WP-01a, WP-01b, WP-02a, WP-02b |
| TR-21 export scrub ON, previewId 409 | 01 | WP-06a, WP-06b, WP-07b, WP-65 |
| TR-22 tooltip dictionary | 02, 04 | WP-08, WP-09, WP-17c |
| TR-23 canvas toolbar | 04 | WP-10, WP-11, WP-23 |
| TR-30 HTTPERR markers | 01, 03 | WP-04, WP-19 |
| TR-31 IIS point at timestamp | 03 | WP-20 (gated by WP-45) |
| TR-32 lane payload carries HTTP status | 03 | WP-13 |
| Later / MVP-2 / MVP-3 (TR-14, 17, 24-29, 33, 34) | not in this plan | — |

Invariants every packet must preserve: INV-1 aligned UTC axis and half-open windows (`[start,end)`); INV-2 every lane carries `origin, asOf, complete, truncated, reason, consistency`, `mixedConsistency` when any lane is `live-non-snapshot`; INV-3 never store raw cookies, server IP, query strings or user agents; INV-4 never create `.unravis.db`; INV-5 no hard CI assertions of `<15 ms`/`<100 ms`; INV-6 new web chunks must not import each other in a cycle (`web/tests/distChunks.test.cjs`); INV-7 do not extend `CallTrace.svelte` except WP-09's `explain()` labels; INV-8 guided code stays in the app (unlisted only).

## 3. Phases, packet graph, frontier

Phases (each independently reviewable; risk per phase.md):

| Phase | Slug | Packets | Risk |
|---|---|---|---|
| 01 | iis-privacy-export | WP-45, 01a, 01b, 02a, 02b, 03, 04, 05, 59, 65a, 65b, 06a, 06b, 07a, 07b, 65 | high (privacy boundary, schema meta, export) |
| 02 | tooltip-dictionary | WP-08, 09 | low |
| 03 | tracing-api-lanes | WP-12, 13, 14, 15, 18a, 18b, 19, 20, 25 | high (new public route, query budget) |
| 04 | tracing-ui | WP-16a, 16b, 17a, 17b, 17c, 22, 10, 11, 23 | medium |
| 05 | guided-unlisting | WP-60, 61, 62a, 62b, 62c, 63, 64 | medium |
| 06 | mvp1-gate | WP-58 plus the final acceptance run | low |
| 07 | background-run | WP-70a, 70b, 70c, 70d, 70e, 70f, 71a, 71b, 71c, 71d, 72a, 72b, 72c, 72d, 73a, 73b, 73c, 73d, 73e, 74a, 74b, 74c, 74d, 74e, 74f, 74g, 74h, 74i, 74j, 75a, 75b, 75c, 76a, 76b, 77 | high (windowless app, tray, open/quit signals, quit route, autostart shortcut, profile links written by the user process not the elevated worker, terminate on upgrade) |

Packet dependency edges. This table is the plan's authoritative graph. Phase 07 (WP-70a..77) is not in the spec: its rows come from `research/background-run.md` adjusted for the owner answers, with only real edges (the `cmd/audit-explorer/main.go` chain 70c, 70d, 73a, 73b, 73c, 73e, the `internal/setup/plan.go` chain 74f, 74h, 75b, the `internal/setup/uiserver.go` chain 74g, 74j and the `internal/setup/os_windows.go` chain 75a, 74e, 75c are real file-order edges, not style). It starts from the spec §9.2 Depends-on column and extends it: split packets (01a/b, 02a/b, 16a/b, 17a/b, 18a/b, 62a/b/c), the added WP-17c, and edges found in authoring and round-1 review (on WP-04, 05, 10, 11, 17a, 18b, 19, 20, 22, 23, 61, 65b), so it is not a copy of the spec rows. Waves are global topological levels (a packet's wave is one more than its deepest dependency); WP-58's `all other packets` puts it after every other wave. A packet file's header `wave:` and `## Depends on` must equal this table, and phase.md files carry no waves or dependencies (check-plan.ps1 enforces all three):

| Packet | Depends on | Wave | Phase |
|---|---|---|---|
| WP-01a | none | 1 | 01 |
| WP-08 | none | 1 | 02 |
| WP-12 | none | 1 | 03 |
| WP-14 | none | 1 | 03 |
| WP-18a | none | 1 | 03 |
| WP-45 | none | 1 | 01 |
| WP-60 | none | 1 | 05 |
| WP-62a | none | 1 | 05 |
| WP-65a | none | 1 | 01 |
| WP-01b | 01a | 2 | 01 |
| WP-02a | 01a | 2 | 01 |
| WP-06a | 65a | 2 | 01 |
| WP-09 | 08 | 2 | 02 |
| WP-13 | 12 | 2 | 03 |
| WP-15 | 14 | 2 | 03 |
| WP-16a | 14 | 2 | 04 |
| WP-59 | 45 | 2 | 01 |
| WP-02b | 02a, 45 | 3 | 01 |
| WP-06b | 06a | 3 | 01 |
| WP-07a | 06a, 01b | 3 | 01 |
| WP-16b | 14, 16a | 3 | 04 |
| WP-18b | 15, 13, 18a | 3 | 03 |
| WP-65b | 65a, 01b | 3 | 01 |
| WP-03 | 02b | 4 | 01 |
| WP-07b | 07a | 4 | 01 |
| WP-17a | 15, 18b | 4 | 04 |
| WP-17b | 15, 16b | 4 | 04 |
| WP-25 | 18b | 4 | 03 |
| WP-04 | 45, 02b, 03 | 5 | 01 |
| WP-10 | 07a, 07b, 16a | 5 | 04 |
| WP-17c | 17b, 09 | 5 | 04 |
| WP-65 | 65a, 65b, 06a, 07a, 07b | 5 | 01 |
| WP-05 | 03, 04 | 6 | 01 |
| WP-19 | 18b, 04 | 6 | 03 |
| WP-22 | 16b, 17b | 5 | 04 |
| WP-23 | 17b, 17c | 6 | 04 |
| WP-11 | 10, 22, 23, 06b | 7 | 04 |
| WP-20 | 45, 02b, 03, 05 | 7 | 03 |
| WP-61 | 23, 60, 11 | 8 | 05 |
| WP-62b | 61, 62a | 9 | 05 |
| WP-62c | 61, 62a | 9 | 05 |
| WP-63 | 62b, 62c | 10 | 05 |
| WP-64 | 61, 63 | 11 | 05 |
| WP-70a | none | 1 | 07 |
| WP-70b | none | 1 | 07 |
| WP-71a | none | 1 | 07 |
| WP-72a | none | 1 | 07 |
| WP-72d | none | 1 | 07 |
| WP-74a | none | 1 | 07 |
| WP-74d | none | 1 | 07 |
| WP-75a | none | 1 | 07 |
| WP-70c | 70a, 70b | 2 | 07 |
| WP-71b | 71a | 2 | 07 |
| WP-71c | 71a | 2 | 07 |
| WP-72b | 72a | 2 | 07 |
| WP-72c | 72a | 2 | 07 |
| WP-74b | 74a | 2 | 07 |
| WP-74e | 74d, 75a | 2 | 07 |
| WP-74i | 74a, 74d | 2 | 07 |
| WP-70d | 70c | 3 | 07 |
| WP-71d | 71b, 71c | 3 | 07 |
| WP-74c | 74b | 3 | 07 |
| WP-74f | 74a, 74d, 74i | 3 | 07 |
| WP-75c | 72a, 72c, 75a, 74e | 3 | 07 |
| WP-70e | 70d | 4 | 07 |
| WP-73a | 70d, 72a, 72c | 4 | 07 |
| WP-73d | 71a, 74b, 74c | 4 | 07 |
| WP-74g | 74f | 4 | 07 |
| WP-74h | 74f, 74i | 4 | 07 |
| WP-74j | 74f, 74g, 74h | 5 | 07 |
| WP-75b | 75a, 74h | 5 | 07 |
| WP-70f | 70d, 70e | 5 | 07 |
| WP-73b | 70c, 72a, 72b, 73a | 5 | 07 |
| WP-73c | 70c, 72c, 72d, 73b | 6 | 07 |
| WP-73e | 71d, 73c, 73d | 7 | 07 |
| WP-76a | 73e, 70e, 74g, 74j, 75b | 8 | 07 |
| WP-76b | 73e, 75b, 74g, 74j | 8 | 07 |
| WP-77 | 76a, 76b, 70f, 75c | 9 | 07 |
| WP-58 | all other packets | 12 | 06 |

Executable frontier at start (phases run in order, see §5): phase 01 wave 1 = WP-01a, 45, 65a, with phase 02 wave 1 = WP-08 in parallel (disjoint files). Wave-1 packets of later phases (WP-12, 14, 18a, 60, 62a, and Phase 07's WP-70a, 70b, 71a, 72a, 72d, 74a, 74d, 75a) wait for their own phase.

Cross-phase edges: 03 needs 01 (WP-02b, 03, 05, 45 → WP-20; WP-04 → WP-19); 04 needs 01 (WP-07a, 07b → WP-10; WP-06b → WP-11), 02 (WP-09 → WP-17c) and 03 (WP-14 → 16a/16b, WP-15 → 17a/17b, WP-18b → 17a); 05 needs 04 (WP-23, WP-11 → WP-61); 07 has no edge to phases 01 to 05 (it touches Go app start-up, the tray, the installer and docs, none of the timeline files) and its waves stay below 12; 06 needs all, including 07 (WP-58 step 10a reads the Phase 07 acceptance record `docs/background-run-acceptance.md` written by WP-77). Ruling (round 1): WP-19 does not depend on WP-17a; both edit different regions of tracing.go (17a the audit lane, 19 the IIS marker step), so phase order 03 then 04 holds.

## 4. Integration points

- 01 → 03: events carry `client-ip`, `referer`, `cookie:*` attrs (WP-02b/03); WP-20 reads them. Contract: attrs keys exactly as in the spec §4 / Appendix P1.
- 01 → 04: `ProjectEventAttrs` (WP-07a/07b) and the export `ScrubSensitive` setting feed the toolbar scrub pill (WP-10, mounted by WP-11 after WP-06b).
- 02 → 04: `explain()` dictionary (WP-08/09) supplies tooltip text to the tracing chart (WP-17c).
- 03 → 04: `POST /api/cases/{id}/tracing` response shape (WP-14/15/17/18) is the only contract the UI reads.
- 04 → 05: WP-23 (chart + toolbar) is what WP-61 proves a bucket click on.

## 5. Commit policy and branch

```yaml
commit_granularity: phase
history_style: separate
```
- Work happens on `feat/investigation-timeline`. The tree already has ~269 modified files that are not part of this plan ("not mine"); every commit stages only the phase's own files by explicit path, never `git add -A`/`.`.
- Execution order: phases run one after another in the order 01 (with 02 in parallel: 02 is web-only and its files are disjoint from 01's), 03, 04, 05, 07, 06. Phase 07 runs after 05 and before the 06 gate so WP-58 can record it; the phase number 07 only names the folder. A phase starts only after the previous phase is committed. Waves inside a phase run in parallel where write scopes are disjoint (§6). The §3 wave numbers are global levels; inside the current phase run its packets in increasing wave order.
- Phase 07 shares no file with phases 01 to 05 (new packages, `cmd/audit-explorer/main.go`, `internal/setup`, `build.ps1`, docs), so its commit carries only its own hunks; its list is in its `phase.md`.
- One local commit per phase on the feature branch, made after that phase's gate (§7) by explicit paths (the phase.md commit list). Because a later phase only starts after the earlier one is committed, a file written by two phases (for example `internal/casestore/sqlite/case.go`: WP-65b in 01, WP-18a in 03; `httperr.go`: WP-04 in 01, WP-19 in 03; `CaseTools.svelte`: WP-06b in 01, WP-11 in 04; `CaseHome.svelte`, `TracingPaper.svelte`, `TracingChart.svelte`: 04 then 05) carries only that phase's hunks in each commit, and no later phase has hunks in an earlier phase's commit. For 01 and 02 running together, each commits only its own listed paths; a web test failure in the 01 gate that comes from a phase 02 file belongs to phase 02.
- Line-ending check before each commit: run `git diff --stat -- <listed paths>` and `git diff --ignore-cr-at-eol --stat -- <listed paths>`. The repo has many CRLF-noise files (for example `internal/server/cases.go`, `case_views.go`, `export.go`, `routes.go`, `int_test.go` already show as modified with line-ending changes only). If a listed file shows whole-file churn that is not this phase's change (the two stats differ widely for it), stop and ask the owner before committing (normalise first, or accept the churn).
- The workflow contract says to stop before each commit. The owner's standing instruction of 2026-11-02 was "Don't stop till all phases complete", so a phase-end commit **on the feature branch only** goes ahead after the phase gate is clean, with the stop recorded in the phase's `progress.md`. Push, merge to master, tag and release always need a fresh ask. If the owner objects, the fallback is one stop per phase.
- `.forge/investigation-timeline/` is committed with phase 01 (later phases add their `progress.md`). The spec copy under `.dev/spec-src/` is **not** committed: `.dev/` is git-ignored (`.gitignore` `/.dev/`), the copy stays on this machine for later sessions, and its source is branch `docs/tracing-paper-spec-round6`. Committing it would need `git add -f`; do not.

## 6. Rules for packets, parallel work and tokens

Packet rules (copied into every packet author's brief):
1. One WP per implementer turn; edit only `Write scope`; a dependency-adjacent edit is allowed only if a listed file fails to compile or an existing invariant breaks, and it must be reported in `progress.md` `deviation:`.
2. Public contract, schema or route changes beyond the packet are escalations, not edits.
3. Never create `.unravis.db`; never read `sql/`; never put secrets in files.
4. Every packet names exact file paths, function/type names with signatures, test names with assertions, and a runnable proof command with the expected result. Where the spec says "(unverified)", the author opens the file and writes the fact in.
5. Every packet states `risk:` and `size:` (S/M) and exactly the dependency ids from the §3 table.
6. A packet that touches the browser UI also names the Playwright/Vitest test, not a manual check.

Parallel safety: two packets in the same wave may run in parallel only if their `Write scope` file sets are disjoint. `check-plan.ps1` (see §9) prints overlaps; overlapping packets run sequentially in the order of the lowest packet id.

Token economy: implementers get the packet file path and the global rules only (no spec). Reviewers get the packet, the diff file and the proof output. Workhorse model by default (Sonnet; Haiku for S-size mechanical packets). Opus is used only for this plan's graph review, the per-phase security review and the final review. Runner Hub takes over when usage nears the 5-hour window: see §8.

## 7. Phase acceptance protocol (runs at the end of every phase, before its commit)

Per phase, in this order; failures create fix packets inside that phase (max two fix loops, then stop and report):

1. **Checks**: the phase's proof commands; `go build ./... && go vet ./...` on touched packages; `go test` on touched packages; web `npm test` when `web/` changed; no `.unravis.db` file exists and nothing creates one: `git ls-files -- "*.unravis.db"` prints nothing, `Get-ChildItem -LiteralPath D:\Projects\api-audit-explorer -Recurse -Force -Filter *.unravis.db | Where-Object { $_.FullName -notmatch '\\(node_modules|\.forge)\\' }` prints nothing, and the phase diff adds no code path that creates or opens a file of that name (`git diff -- '*.go' '*.ts' '*.svelte' | Select-String 'unravis\.db'` prints nothing). A text search over the repo is not used: the plan files name the literal on purpose.
2. **Browser UI/UX** (in-app browser): start the backend in demo mode on a throw-away config folder, e.g. `$env:AUDIT_CONFIG_DIR='D:\Projects\api-audit-explorer\.dev\run\demo-cfg'` (empty folder), `$env:UNRAVIS_DEMO_CASES='1'`, `go run ./cmd/audit-explorer --demo --port 18765`, then the `unravis-web` configuration of `.claude/launch.json` (Vite on 5173, proxies `/api` to `127.0.0.1:18765`; never print its token). Seeded demo case for phases 01 and 02: **"Checkout slowdown, 17 October"**, id `demoopenaaaaaaaaaaaaaaaaaa` (`demoCaseOpenID` in `cmd/audit-explorer/demo_cases.go`), open it from the start-screen case list (or `#/case/demoopenaaaaaaaaaaaaaaaaaa`). From phase 03 on, UI and route checks use the timeline demo case WP-25 adds, **"Orders API stall, 19 October"**, id `demotracingaaaaaaaaaaaaaaa` (`demoCaseTracingID`), incident window 2026-10-19 09:00-10:00 UTC. Walk every flow the phase changed with that case, at 1280 px and 375 px, light and dark, keyboard only once; read console and network for errors. Compare with the wireframe: layout, lane order, labels, copy, empty/error states. Differences are listed as `matches`, `fixed`, or `accepted deviation (owner decision needed)`.
3. **Security**: privacy canaries (no raw cookie value, server IP, query string or user agent anywhere in events, exports, logs, API responses); scrub default ON on every export surface; new routes are case-scoped and read-only; input validation (window, tz, ids) refuses bad input with a named error; no secret in logs.
4. **Performance**: bucket count ≤120 asserted; audit lane is one `$match`+`$group` per source (no raw paging); SQL/IIS lanes read local aggregates; no unbounded loop or response; web chunk-cycle test passes; record the bundle size delta of new chunks. No `<15 ms`/`<100 ms` hard assertions (INV-5).
5. **Clumsy-user review**: one agent tries to break the phase's feature (empty case, 30 d + 1 s window, reversed window, huge window, missing source, double click, reload mid-flow, back button) and writes tests for what breaks.
6. **Diff review against the Goal and this plan**; high-risk phases get a second reviewer (Opus). Stop before the commit; commit per §5.

Phase-specific additions are in each `phase.md`.

## 8. Execution, usage and Runner Hub

- Build order: phases in sequence, 01 (with 02 in parallel), then 03, 04, 05, 07, 06 (§5). Inside the current phase, its packets run in increasing §3 wave order; packets of one wave run in parallel subagents when file scopes are disjoint. Then the phase acceptance protocol (§7), then the phase commit. No packet of a later phase starts before the current phase is committed.
- Before each wave check usage (`get_usage`). At or above ~75 percent of the 5-hour window: stop starting new waves in this session and hand remaining S/M mechanical packets to Runner Hub one packet per handoff, in wave order, and only packets of the current phase (never a later phase's packet ahead of time) (`~/.claude/runner-hub.md`: re-probe lanes, cloud lanes first, then local). The hub never gets: browser or running-app work, local secrets, `sql/`, real databases, or the phase acceptance steps 2-5 (those stay local). Every hub patch is reviewed locally against the packet before it is applied.
- Each phase keeps `progress.md` in its folder using the shared checklist format; resume at the first non-`[x]` box.

## 9. Plan self-checks

`.forge/investigation-timeline/check-plan.ps1` (deterministic, no model, not part of the product): verifies every packet file has the six headings in order, a `risk:` and `size:` line, a header `wave:` and a `Depends on` equal to the §3 table (WP-58 says exactly `all other packets` and sits in the last wave), dependencies in earlier waves, and that every `phase.md` table lists exactly its folder's packets with no wave or dependency column; it lists same-wave write-scope overlaps and WARNs (does not fail) on edges from a lower-numbered phase to a higher one and on wave numbers in `phase.md` prose. The reviewer loop runs it first; it must print `OK` (overlaps are resolved by serialising, see §6).

## 10. Final acceptance (closes the plan)

1. Spec §10 MVP-1 gate: IIS stores IP/referer/cookie-hash; export scrub default ON proven by `TestBuildCaseNoIPsOrUserIDs` plus canary tests on every export surface; trace shows audit+SQL+IIS lanes on the seeded demo; toolbar toggles; tooltips via the dictionary.
2. `go test ./...` and web `npm test` pass; the `.unravis.db` file check of §7 step 1 (`git ls-files` and `Get-ChildItem` outside `node_modules`/`.forge` print nothing; no code path creates the file) passes over the whole branch diff; `./run-e2e.ps1` main suite passes (release gate `-Release` is a later release step, not this plan).
3. Browser walk of the whole Investigation Timeline flow against the wireframe screenshots 08, 08a, 09, 13 with a written differences table.
4. Guided is unlisted but intact (WP-62a/b/c, 63, 64). OPEN OWNER DECISION (escalation from phase-05 authoring): after WP-64 removes the Guided button, GuidedAnalysis has no entry point (`#/case/guided/N` has no consumer; Manual tools is reachable only through Guided), so the archive suite can only pass on a pre-WP-64 build. Until the owner rules on a minimal hidden entry, WP-58 records the archive run as informational, not a gate.
5. Phase 07 background-run: `docs/background-run-acceptance.md` (WP-77) ends with `ACCEPTANCE: AUTOMATED PASS, MANUAL PENDING` (or names the failing automated row); WP-58 step 10a reads it read-only. Automated rows must not FAIL. Manual rows (sign-in start, logoff, Explorer restart, single left click, second launch, port taken, MCP spawn, upgrade while running, uninstall and repair, Defender and SmartScreen, second account, no console window) are recorded as PENDING and do not fail the gate; the owner runs them on a real desktop or a clean VM.

## 11. Conditional controls

- Security (high risk phases 01, 03): per-phase security pass in §7 step 3 plus Opus second review; canary fixtures in WP-05/WP-07/WP-65.
- Rollback: phases 01-06 are separate commits on the feature branch, made in phase order, each holding only its own phase's hunks (§5). Roll back from the newest phase backwards (`git revert` of the latest phase commit first); reverting an earlier phase alone while later phases stay is not supported, because later phases build on it (shared files such as `case.go`, `httperr.go`, `CaseTools.svelte`). Schema change in phase 01 is `meta` keys only (no table change); WP-65a adds the `case.revision` storage — its packet states the migration and downgrade behaviour.
- Idempotency/observability: none beyond the spec (tracing is read-only; no background jobs).
- Manual/live proof: WP-45 needs real IIS/SQL samples; if no sample path is available the packet records `UNMEASURED` with reason and WP-04/20/59 keep their conservative behaviour defined in the packet.
