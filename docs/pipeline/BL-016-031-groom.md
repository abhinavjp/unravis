# GROOM handoff: BL-016 (Query Store OFF history) and BL-031 (reproducible web build, dist check)

Groomed 2026-11-21 against master @452f3b9. Research outputs read in full:
`/mnt/project-files/research/outputs/backlog/BL-016-query-store-off-history.md` and `.../BL-031-reproducible-web-build.md`.
Two items, two sections each. They share one link: every web change in BL-016 must also commit a rebuilt dist, which BL-031 will start to enforce (see section 9).

Tags: SOURCE = the research cites a source. INFERRED = the research reasoned it. CHECKED = I checked it in this repo or by running it here.

---

# PART A: BL-016 Query Store OFF history

## A1. Verdict

Build it, as a small read-only change. Today the SQL lane stops after the options row when Query Store is OFF, so it never even tries to read the retained history. The research gives good reason to think OFF keeps persisted data and the views stay readable, but no live OFF result exists anywhere (not in the research, not in P-40). So build it so that it proves itself: when OFF, ask whether any history exists; if rows come back, show them with honest "off" wording; if nothing comes back, show exactly today's message. That is safe to ship before the live proof, and the live proof then decides whether to keep the claim in the owner's notes. Skip: plan-cache substitute, shifting the window to the last stored bucket, replica or Azure special code, query text, any new permission. Nothing is already done for OFF. The READ_ONLY path already works the same way and is the template.

## A2. What the research found that matters

- SOURCE: `QUERY_STORE = OFF` stops capture and keeps persisted data. `CLEAR` is the delete. (Microsoft ALTER DATABASE docs; the "kept after OFF" line comes from a 2016 Ignite deck, so OLD.)
- SOURCE: Database Health Monitor's maintainer (Stedman, 2026-10-11) says his tool reads retained history while the store is OFF, from SQL Server 2016 on. This is the only current "it works" report. No transcript, no per-build result.
- INFERRED: No Microsoft page promises that views return rows while OFF (the docs spell it out only for READ_ONLY). So treat it as "try and see", per database.
- SOURCE: `OFF (FORCED)` aborts running Query Store tasks and skips the final flush. INFERRED: already-saved data survives, the newest unflushed tail may be missing. So the last bucket before OFF can be short or absent.
- SOURCE: Same grants as today for the four views plus options: `VIEW DATABASE STATE` (2016-2019), `VIEW DATABASE PERFORMANCE STATE` (2022+). No wider grant. Query text view needs a server-level grant, so keep text out.
- SOURCE: Azure SQL Database single DB and pools reject setting OFF, so OFF there is rare. Secondary-replica capture is preview. INFERRED: do not add replica code.
- INFERRED: `sys.database_query_store_options` has no "switched off at" time. Never claim one. Show first and last retained bucket as observed times.
- INFERRED: Empty result, permission denied, timeout, ERROR and "no options row" are five different outcomes. Keep them apart in wording.
- INFERRED: Plan-cache DMV totals have no time axis. Do not use them for the lane. (Agrees with P-40.)
- SOURCE (weak): Jam SQL Studio moves its window back to the end of retained history. INFERRED: Unravis must not do that. Keep the asked windows and show only the buckets that exist.

## A3. Current state in code (CHECKED)

- `internal/dbhealth/lane_sql.go`, `checkQueryStore` (line 685). Line 706: `if !opt.found || opt.state == "OFF" || opt.state == "ERROR" || opt.state == ""` returns "Cannot test yet" with the switch-on script and stops. First-data, stats and intervals queries never run. The research is right about this early return.
- Queries already there and reusable as they are: `qLaneQSOptions`, `qLaneQSFirst` (MIN start_time only), `qLaneQSStats`, `qLaneQSIntervals`. They filter by `i.start_time` inside each slice, in 72-hour slices, 60 s limit each, top 3000 rows. They only read. (The research's test script uses an overlap predicate; the lane uses start-time alignment. Keep the lane's.)
- `queryStoreOf` (line 666) maps OFF to state `off`, READ_ONLY to `read-only`, READ_WRITE to `on`. ERROR and an unknown value fall to `unknown` while the sentence says "switched off". P-40 already flagged this mismatch. The research's "conflates OFF, ERROR and missing options" is correct.
- `evalLaneQueryStore` (line 466) has the wording "it was off then, or its history was cleaned up" for an empty incident window. It was written for ON. It needs an OFF branch.
- `internal/dbhealth/lane.go`: `LaneQueryStore{State, FirstData, IntervalMinutes}`. No last-bucket time. JSON is optional-field safe (test `TestLaneQueryStoreJSONIsOptional`).
- `internal/server/tracing_sqllive.go`: if saved days have a series, the Timeline draws it. `heldSQLQueryStoreLane` (line 115) is only the no-series fallback and says "Query Store is switched off ... saved no history" for `off`.
- Front end: `web/src/lib/drill/dbState.ts` `pickKey` line 88: `if (qs?.state === 'off') return input.hasEvents ? 'events' : 'off';` comes BEFORE the check that the lane has a drawn shape. So even if the server saves an OFF series, the screen would still show "Not recorded: Query Store is off". The front end needs a change too. Wording lives in `web/src/lib/drill/drillCopy.ts` (`dbMsgOff`, `dbShortOff`). `web/src/lib/lanes.ts` holds the `queryStore` type.
- Tests exist with a fake query runner (`internal/dbhealth/lane_sql_test.go`: `TestSQLLaneQueryStoreOffGivesFix`, `...StateEachWay`, `...CarriesFirstDataAndBlockLength` asserts OFF has no FirstData). These three will need updating on purpose.
- Missing: anything that reads history when OFF; a last-bucket time; an ERROR state value; an OFF-with-history message and screen key; any live OFF test.
- P-40 (`.forge/investigation-timeline/drill/screen/probe/qs-off-history.md`) is accurate; its line numbers are stale (moved since). It asks the owner for a known-OFF database. Owner rule says find another way first (see A7).

## A4. Scope: slices

1. **S1 Server read when OFF** (M, ~250 lines incl. tests). Goal: when state is OFF, read first and last retained bucket; if none, return today's message and script unchanged; if some, read stats and intervals only inside [first, last] range slices, save the series, answer rules below, OFF-specific wording. Split ERROR from the "off" sentence (ERROR and no-row keep "cannot tell", no read). Files: `internal/dbhealth/lane_sql.go`, `lane.go` (add optional `LastData`), `lane_sql_test.go`.
   - Answer rule (my proposal, INFERRED): in OFF, give a "Yes" answer when found, never a "No", unless the last bucket end is at or after the incident end. Reason: OFF may have begun mid-incident, and a missing plan change is not evidence of none. Same idea as the existing `truncated` rule.
   - Cheap guard: if MIN is NULL, stop after one query. If history is older than the window, skip empty slices. This keeps cost for a long window on an empty OFF store at one extra query.
2. **S2 Timeline and screen** (M, ~250 lines + regenerated dist). Goal: show retained history on an OFF database as blocks, with wording "Query Store is off. Showing kept history from X to Y." and the empty case "kept no history for this window". Files: `web/src/lib/drill/dbState.ts` (new key, e.g. `offkept`, picked when state off AND lane has a drawn shape from a held SQL series), `drillCopy.ts` (new strings, copy lint rules apply), `lanes.ts` (type `lastData`), `internal/server/tracing_sqllive.go` (pass coverage and keep the "stale" suffix), tests `web/tests/slowcall.dbstate.test.cjs` and server tests. Rebuild `web/dist` (BL-031).
3. **S3 ERROR wording** (S, ~80 lines). Add state `error` (or keep `unknown`) with its own safe sentence, so ERROR never says "switched off". Can be folded into S1 if the diff stays under 400. Files: `lane.go`, `lane_sql.go`, `dbState.ts`, `drillCopy.ts`.
4. **S4 Live proof harness** (S, ~150 lines, test-only). An opt-in Go test (skipped unless an env var names a SQL Server) that builds a throwaway database, makes history, sets OFF and OFF (FORCED), then runs `RunSQLLane` and asserts rows come back with a low-privilege reader. Files: new `internal/dbhealth/lane_sql_live_test.go`, no production code. See A7 for where it can run.

## A5. Risks and unknowns

- Security: none new. Same views, same grants, read only, fixed query texts. Do not read `sys.query_store_query_text`. Do not write to Query Store. Check no new query contains values from outside the package.
- Windows-only: none in code. The open question is engine behaviour on the owner's real Windows builds (SQL 2016/2017/2019/2022/2025). Unknown per build.
- Data loss / rollback: none; read only. Rollback is reverting the PR. Old saved reports with `off` and no series still load (optional fields).
- Footprint: OFF with history adds up to the same number of read queries as an ON database today (2 per 72 h slice, 60 s limit each, 3000 rows). OFF with no history adds 1 query. Run on the owner's SQL Server, not inside Blackbox. No new network, no new memory shape.
- Wrong claim risk: showing old buckets as if they were the incident. Mitigation: windows stay as asked; only existing buckets drawn; wording names first and last bucket times, never "switched off at".
- Forced OFF may drop the unflushed tail; a partial last bucket is possible. Wording must say "kept history", not "complete".
- Docs disagree (SQL 2017 forced-OFF floor, Azure MI default, 2025 secondary default). None of them blocks this design.
- Unproven: that the views return rows while OFF on a given build. The design handles "no" safely; "yes" needs the live proof.

## A6. Acceptance per slice

S1
- Given OFF and a fake runner returning a first/last bucket and rows, the check state is ready, the series is saved, `QueryStore.State` is `off`, `FirstData` and `LastData` are set.
- Given OFF and no first bucket, the check is exactly today's "Cannot test yet" with the switch-on script and no stats query ran (assert the query log).
- Given OFF with history that ends before the incident end, D3/D14/D15 never get `No`; a plan regression found in the earlier buckets can still give `Yes`.
- Given ERROR or no options row, no history query runs and the sentence does not say "switched off".
- Permission error on the history query (after a good options row) gives a failed check that keeps `State = off`, not "denied", and says which read failed.
- No new query text writes anything or contains the query-text view (a test greps the query constants).
- A report saved before this change loads and renders unchanged.

S2
- State off + drawn series: the lane draws blocks and the message names first and last bucket in the user's zone; it does not say "saved no history".
- State off + no series: today's message, unchanged.
- State off + events only: still `events` (existing rule) when there is no series.
- Copy lint test passes for the new strings. Stale-day suffix still shows when the day was saved before the window ended.
- Regenerated dist is committed and the BL-031 check (when it exists) is green.

S3
- ERROR shows its own sentence and never offers the "switch on" script as the only message.

S4
- With the env var unset the test is skipped and `go test ./...` stays green on Linux and Windows.
- With it set: OFF with persisted history returns rows through `RunSQLLane`; a database that never had Query Store returns the old message; a reader with only the documented grant works.

## A7. LIVE-PATH check per slice

(a) What a worker can verify here on Linux
- S1/S2/S3: real code with the repo's fake query runner and the web node tests (`node --test`, runs here; CHECKED that the web toolchain installs and builds here). This proves the logic and wording, NOT the engine.
- Engine proof on Linux: `docker` CLI is installed here, but no daemon is running and starting `dockerd` was blocked by the sandbox in my session. `mcr.microsoft.com` is reachable through the proxy (HTTP 200). If a later worker is allowed to run Docker, a SQL Server 2022 Developer Linux container plus S4 gives real OFF behaviour on a real engine (same SQL engine code, Linux build). That is real engine data, not dummy data, but it is Linux, so it is only partial evidence for the owner's Windows builds. Not done in this groom; unknown whether the sandbox will allow it.
- No public feed exists for this item.

(b) What needs nachi's Windows PC (DESKTOP-QQDVSD1) or the owner's SQL
- The real data path: a lane read on a database that is OFF and has pre-OFF history, with the real low-privilege login, on the real build. Two safe ways with no change to the owner's databases: (1) any database the owner already knows is OFF; (2) restore a backup of a database that had Query Store on onto the PC's own SQL instance (a throwaway copy), then set OFF on the copy. Option 2 changes only the copy, not production. Ask the owner only for the choice of option and for any SQL instance on the PC; do not ask them to turn Query Store off anywhere that matters.
- Record per P-40: check state, `queryStore.state`, `firstData`, `lastData`, row count, SQL version/edition, plain OFF vs FORCED if both are available.

(c) Impossible to verify
- All five SQL versions plus Azure SQL and Managed Instance, secondary replicas, restore/attach cases: needs servers nobody has here. Say so in the PR. Ship with "verified on build X only".

## A8. New collector or agent?

No. It is a change to the existing on-demand lane read. No Blackbox, no new service, no new agent. BACKLOG places BL-016 under "Planned now", not "Backlog (deferred)". No owner go needed beyond the normal one. Quote: "BL-016 Possible small improvement: read older Query Store history when the state is OFF (unverified that it stays readable)."

## A9. Dependencies and collisions

- Open PRs #70 (`internal/setup/runner.go`), #71 (`internal/blackbox/selfupdate.go`, `internal/server/blackbox.go`, `web/src/components/BlackboxPanel.svelte`, `web/src/lib/blackbox.ts`, `web/src/lib/copy/blackboxCopy.ts`), #72 (`cmd/unravis-releases/**`, `internal/releasedash/**`, `internal/update/fetch.go`) were listed with the GitHub REST API (CHECKED). None touches `internal/dbhealth`, `internal/server/tracing_sqllive.go`, `dbState.ts` or `drillCopy.ts`. No collision with internal/update, `internal/server/updater*.go` or `internal/blackbox/selfupdate.go` from any BL-016 slice.
- `web/src/lib/copy/` files: #71 edits `blackboxCopy.ts`; BL-016 edits `web/src/lib/drill/drillCopy.ts`. Different files. Both PRs change web source, so both need a rebuilt `web/dist`: merge order will force one to rebase and rebuild dist (generated files conflict). See B9.
- Overlaps BL-012 (lane wording for states) conceptually. BL-012 is not in code collision.

## A10. Order and hand-off

1. SPEC next: S1 + S3 together if the diff stays under ~400 lines, then S2, then S4.
2. Hand SPEC: the three lane tests that must change on purpose; the answer rule (never `No` unless last bucket covers the incident end); the exact new wording for OFF-with-history and OFF-empty; the `LastData` JSON field; the state-key name for the screen; the query guard order (first/last, then slices inside range).
3. Do the live proof (A7 b) after S1/S2 merge; do not tick BL-016 until it runs on a real OFF database. If the real read returns nothing on the owner's build, keep the code (it falls back) but write "does not stay readable on build X" in BACKLOG and close.
4. Drop: plan-cache fallback, window shifting, replica/Azure branches, query-text read, any attempt to say when it was switched off.

---

# PART B: BL-031 committed web build and a dist check in CI

## B1. Verdict

Keep the built UI committed and add a blocking CI check. This is already the repo's stated design (comment in `web/vite.setup.config.ts`: files are committed "so a clean checkout builds the Go program without npm"), so the owner decision can be recorded as "keep", with a one-line confirm in the PR. I ran the real build here (Linux, Node 22.23.5, npm 10.9.9, fresh `npm ci`, two separate clones at different paths): the three outputs were byte-identical to each other AND to what is committed on master, 91 of 91 files. So the build is byte-repeatable and the committed dist is not stale today. The one real failure mode is Windows line endings in three source `index.html` files: with `core.autocrlf=true` (the Windows default, and what GitHub Windows runners use) the three built `index.html` files differ. The fix is a few scoped lines in `.gitattributes`, proven here. Build: the attributes, then the check. Skip for now: Vite bump, Node/runner pin work beyond one exact Node line, diffoscope, a Linux builder.

## B2. What the research found that matters

- INFERRED: Keep dist committed, add a source-to-bytes check. Matches the repo's own comment (CHECKED).
- SOURCE (own experiment, Windows): repeat builds identical, different path identical, CRLF checkout changes only the generated `index.html`, LF source HTML restores equality. CHECKED here on Linux: same result. Two clean builds in different folders identical; Linux build equals the committed bytes; `autocrlf=true` changes exactly three `index.html` files (main, setup, lite).
- INFERRED: The scoped `.gitattributes` fix (`text eol=lf` on the source HTML, `-text` on the generated trees). CHECKED: with those lines, a CRLF-config checkout rebuilt clean (no diff, no untracked files).
- INFERRED: Compare raw blob ids, not just `git diff`, because Git text filters can hide CRLF-only changes, and list untracked files to catch extras. PowerShell recipe given. SOURCE says the research ran it against a fixture. I have no PowerShell here (CHECKED: no `pwsh`), so I could not run it.
- SOURCE: Vite 8 uses Rolldown/Oxc. Known hash issues are in Vite #23975 (experimental import map, off by default, fixed in 8.3.6) and Rolldown #9339 (fixed May 2026). INFERRED: neither affects Unravis. Vite 8.3.8 exists on npm and depends on Rolldown `~1.2.18` (CHECKED); lockfile has 8.3.5.
- SOURCE: No Vite/Rolldown use of `SOURCE_DATE_EPOCH` found; INFERRED: not needed, since output carried no timestamps and a changed epoch/TZ changed nothing (research's own run).
- INFERRED: Pin Node exactly (`22.23.5`, npm 10.9.9) instead of moving `22`. CHECKED: this box has exactly 22.23.5 / 10.9.9 and builds identical to master, so that pair is proven.
- SOURCE: PocketBase commits its Svelte dist; Prometheus builds in CI. Both work. INFERRED: committed fits a solo owner on Go-only builds.
- SOURCE: GitHub moves `D:` on Windows images from NTFS to ReFS from 2026-11-30. INFERRED risk to path/line behaviour; the check will show it if it matters. Unmeasured.

## B3. Current state in code (CHECKED)

- Committed: `web/dist` (75 files), `cmd/unravis-setup/ui` (8), `cmd/unravis-lite/ui` (8) = 91 tracked files. Embedded by `assets.go` (`//go:embed web/dist`), `cmd/unravis-setup/main.go` and `cmd/unravis-lite/main.go` (`//go:embed all:ui`).
- The research is WRONG on one point: it says it found no Lite entry in the npm build. `web/package.json` `build` runs three builds: main, `vite.setup.config.ts`, `vite.lite.config.ts`. Lite writes to `cmd/unravis-lite/ui`. The check must cover three roots, not two. (Research looked at an older commit `e16435ea`, which is not in this checkout.)
- The research says HEAD source drifted from dist. On master 452f3b9 that is not true: a fresh build equals the committed bytes, and the last commit touching `web/src` and the last touching `web/dist` are the same (bb59344). So no baseline regeneration is needed now.
- `.gitattributes` only has `binary` lines and a long comment: "Do not add a global text/eol rule". Scoped rules for six paths are not a global rule, but the new lines must reuse that comment's reasoning (blobs are already LF, so nothing is renormalised).
- `.github/workflows/ci.yml` and `release.yml`: both on `windows-2022`, `setup-node@v4.4.0` with `node-version: 22` (moving), `npm ci`, `npm run check`, `npm test`, `npm run build`, then Go. Nothing compares the build output to git. `release.yml` rebuilds at the tag, so the exe always embeds fresh output; `build.ps1 -SkipWeb` relies on that.
- `web/tests/distChunks.test.cjs` reads dist from disk, runs before the build in both workflows, so today it tests the committed bytes. Passes here on a fresh build (2 of 2).
- `web/dist/index.html` etc. are LF in git (`git ls-files --eol`).
- Missing: the gate, the scoped attributes, an exact Node pin, a note that web PRs must commit rebuilt output.
- No `.nvmrc`; no `pwsh` on this Linux box.

## B4. Scope: slices

1. **S1 Line-ending attributes** (S, ~12 lines). Goal: a Windows or Linux checkout builds the same three `index.html` bytes. Files: `.gitattributes` (add `/web/index.html`, `/web/setup/index.html`, `/web/lite/index.html` `text eol=lf`; `/web/dist/**`, `/cmd/unravis-setup/ui/**`, `/cmd/unravis-lite/ui/**` `-text`). The research listed only two HTML files and two trees; use the three-and-three list.
2. **S2 The check** (M, ~120 lines). Goal: fail CI when the fresh build differs from the commit in bytes, in file list, or has untracked/ignored extras. Put the logic in one script, e.g. `tools/release/Test-WebDist.ps1` (the existing powershell-syntax job already parses every script under `tools/`), called by a step right after "Web - build" in `ci.yml` and `release.yml`. Roots: the three trees. Also add a short self-test (like `Test-Release.ps1`) that makes a fixture repo and proves it passes, and fails on a changed byte, a CRLF-only change, an extra file, a missing file. Same step also pins `node-version: '22.23.5'` and echoes `node`/`npm` versions (proven pair). Message on failure: say "run `npm run build` in `web` and commit the three folders".
3. **S3 Doc line** (S, ~10 lines). One line in the workflow comment and the PR template/guide that a PR changing `web/` or `web/setup`/`web/lite` must commit the rebuilt folders. SPEC decides where; do not edit CLAUDE.md or BACKLOG here.
4. **S4 (optional, separate PR) Vite 8.3.8 bump + regenerated dist** (L by generated size, small by hand-written lines; generated churn is expected: hashed names change). Do it only after S2 is merged so the gate proves it. The 8.3.7 fix is for an intentionally exposed dev server; Unravis dev server binds 127.0.0.1, so urgency is low. Not needed for BL-031 itself.

## B5. Risks and unknowns

- Windows runner vs Linux: I proved Linux builds equal the committed bytes (committed bytes were built on the owner's Windows PC or CI). That is real cross-OS evidence, better than the research had. But I did not run on a GitHub Windows runner. The only proof of the gate is its own first CI run. If `windows-2022` gives a diff, the check prints it; do not enforce on release until it has been green a few runs (use `continue-on-error` for the first PR only if the first run shows a difference, then remove it in the next PR).
- Blocking check also blocks `release.yml`. A stale dist at a tag stops a release. That is intended, but SPEC should say it plainly.
- The gate will fail any PR that edits web source without committing the rebuilt folders. Open PR #71 edits `BlackboxPanel.svelte` and does not list `web/dist` in its files (CHECKED via REST API), so it will need a rebuild once the gate lands. PR conflicts on generated hashed files are expected.
- Security: gate script must run with the existing read-only `contents: read`; it only reads the working tree and git. No secrets. A dependency or lockfile change can still change output legitimately; reviewers must read lockfile changes.
- This check proves source-to-dist equality, not that the signed exe is reproducible. Do not claim that.
- Footprint: no new CPU for users. CI adds seconds (hash of ~91 files).
- `npm ci` here installed the Linux Rolldown binding from the lockfile; research's concern about missing optional bindings on npm 10.9.x did not show up (CHECKED: clean install and build worked).
- Unknown: ReFS change on 2026-11-30 effect; GitHub runner image drift. The gate catches it when it happens.
- Rollback: delete the step, keep the attributes.
- If the owner changes the decision to "build in CI, do not commit", that is a separate larger change (fresh checkout would fail `go:embed`). Not recommended.

## B6. Acceptance per slice

S1
- `git ls-files --eol web/index.html web/setup/index.html web/lite/index.html` shows `attr/text eol=lf`.
- With `core.autocrlf=true`, a fresh checkout then `npm run build` leaves `git status` clean for the three trees (I proved this by hand here; the PR must show the same by a CI run on Windows).
- No tracked file changes content (no renormalisation commit).

S2
- Self-test: passes on a clean fixture; fails on (a) one changed byte, (b) CRLF-only change hidden by text filters, (c) extra untracked file, (d) extra ignored file, (e) missing file, (f) missing entry `index.html` in any of the three roots.
- On master after S1, the real CI run is green on `windows-2022`.
- A deliberate throwaway commit that edits one `web/src` file without rebuilding makes CI fail with the three-folder message. (Do it on a scratch branch and close it.)
- `release.yml` has the same step and passes on a dry run or on the next draft release.
- `npm --version` is 10.9.9 in the log.

S3
- The doc line exists where web contributors will see it.

S4
- Gate green after the bump; `distChunks.test.cjs` passes; app loads (see live-path).

## B7. LIVE-PATH check per slice

(a) Linux, here
- Done already in this groom: two fresh `npm ci` + `npm run build` in separate clones (different paths): identical, 91 files. Equal to committed. `autocrlf=true` changes exactly three `index.html`. Scoped attributes fix it. `distChunks.test.cjs` 2/2.
- A builder can repeat all of that. S2's script logic can be written so its core compare is also runnable with plain `git` and `sha256sum` for a Linux check, but the script itself is PowerShell and cannot run here (no `pwsh`). Option: write the compare in a tiny Node script under `web/` instead, so it runs on Linux and Windows CI and in `npm test`. SPEC should pick this; it makes the logic verifiable here. (My recommendation: Node script, called from a one-line workflow step.)

(b) Windows / GitHub runner
- The first CI run of S2 on `windows-2022` is the real proof (public repo, run result readable through the REST API). Also on the owner's PC: `npm run build` then `git status` clean, with the owner's own git config (`core.autocrlf`). A worker can open the PR and read the run result; no elevation needed.

(c) Impossible to verify
- Effect of the 2026-11-30 ReFS change and future image updates: not until they happen. Byte equality of the final signed exe or installer: out of scope.

## B8. New collector or agent?

No. CI and build config only. BL-031 sits in "PR #2 review follow-ups ... none built" and ends with "(needs owner)". The needed owner decision is "is dist meant to be committed". The repo already says yes in `web/vite.setup.config.ts` and `web/vite.lite.config.ts` comments. Quote of the BACKLOG line: "Needs an owner decision first: is dist meant to be committed, and is the web build byte-for-byte repeatable on the CI runner? Only then add a `git diff --exit-code web/dist` step after \"Web - build\"." Both questions now have an evidence answer (yes by design; yes repeatable, proven here). Treat the owner confirm as one line in the PR, not a blocker.

## B9. Dependencies and file-collision warnings

- Touches: `.gitattributes`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, a new script under `tools/` or `web/`, optionally `web/package.json` and `web/package-lock.json` (S4 only). None of #70, #71, #72 or the OTA thread's paths (`internal/update/**`, `internal/server/updater*.go`, `internal/blackbox/selfupdate.go`) are touched. CHECKED: #70 = `internal/setup/runner.go` + tests; #71 = blackbox selfupdate/server/panel files; #72 = `cmd/unravis-releases/**`, `internal/releasedash/**`, `internal/update/fetch.go`.
- Interaction, not file collision: #71 changes `web/` source and does not list rebuilt dist files. After S2 lands, #71 (and BL-016 S2) must rebuild and commit the three folders. Merge S1+S2 early, or tell #71's author. If #72 adds `cmd/unravis-releases/ui` it is hand-written (no Vite build), so it is not in the gate. Add it to the roots only if it ever gets a build step.
- `web/package-lock.json` is touched only by optional S4. BL-030 (Playwright in CI) also edits `ci.yml`; sequence it after S2 to avoid a workflow conflict.
- Do NOT regenerate the whole dist for any reason other than a source change; it is in sync now.

## B10. Order and hand-off

1. S1 (attributes) then S2 (check + exact Node pin) as two PRs, or one if under ~200 lines. Then S3. S4 only if the owner wants it.
2. Hand SPEC: the three roots (correct the research's two); the exact attribute lines; the decision to implement the compare in Node or PowerShell (recommend Node, so it is testable on Linux); the failure message; the plan to read the first Windows CI run before making the release step blocking; the note for #71.
3. Drop: diffoscope in CI, a Linux canonical builder, a second independent build job per PR, `SOURCE_DATE_EPOCH` work, a Rolldown override, removing hashes. Reasons: the single-build compare already proved sufficient; no timestamps found in output; the research itself says do not chase latest.

---

# Combined order

1. BL-031 S1 and S2 first (small, unblocks safe web changes, and the gate will protect every web PR after it).
2. BL-016 S1 + S3 (server and tests), then S2 (web, with rebuilt dist, proving the new gate on a real web change).
3. BL-016 S4 and the real-OFF live proof; tick only after that.
4. BL-031 S4 (Vite bump) last or never.

Scratch builds used for this groom are under `/tmp/claude-0/-home-claude/7f919106-f2a4-563b-a8c1-856d3767d40c/scratchpad/b1` and `.../b2` (copies, not the checkout). Nothing in `/home/claude/unravis` was changed except this file.
