# Unravis Release 1: build-out plan

Read `context.md` first, then `decisions.md`. Installer and release details are in `installer-and-releases.md`.

Note: the forge-plan skill could not be invoked by a model in this session. This plan was written by hand to the same headings. The owner can run `/skill-engineer:forge-plan` on it.

## How to read a packet

Every phase has a `risk:` label and six headings in this order: Outcome, Write scope, Must not change, Changes, Proof, Depends on.

- Write scope lists the only files and folders the phase may edit.
- Every phase ends with a "Docs and screenshots" block inside Changes and a docs line inside Proof.
- Every phase ends with a clumsy-user review: one person (or agent) tries to break what shipped. Each finding becomes a test, a fix in the same phase, or a written "won't fix" with a reason.
- Depends on lists only real edges: the phase cannot start, or cannot be proven, without the other.

## Release 1 scope

### In

- Brand tokens in one file, version stamping, screenshot harness, CI on a Windows runner (P1).
- Full case lifecycle from spec v7: Reopen, per-case lock, Archive, Verify, Free, Restore in place, Restore as new, optional per-case key, hardened Erase (P2 to P4).
- Investigation engine Phase A (audit only) plus SQL, Mongo and Blackbox lanes feeding Steps 7 to 9 (P5, P7, P9).
- Guided analysis Steps 0 to 10, Manual tools, checklist, after-the-fix compare, export without payloads (P6).
- Blackbox recorder, its page and wizard in Unravis, this-PC scan, folder / UNC / URL sources (P8, P9).
- Shared machine-wide data folder and a key-keeper service so everyone on the PC shares cases and saved connections while no one can read a saved password (P14).
- One-file installer with upgrade, repair, uninstall, silent mode and rollback (P11).
- GitHub Releases pipeline, manual update check, clean-machine smoke (P12).
- User docs with screenshots that match the shipped screens (every phase, final pass in P13).

### Out

- Permission-check figures (N17 dropped).
- Network scan for Blackboxes, guest-book or any discovery beyond this PC.
- Anything that writes to a production server from Unravis. Scripts only.
- Automatic download or install of updates.

### Deferred (after Release 1)

- An MSI wrapper for group-policy deployment (only if asked for).
- Mongo FTDC (diagnostic.data) decoding.
- Thread-pool and GC counters (H3 stays grey).
- Blackbox SQL probe (was P10; owner ruling Q1, A43).
- Running Unravis itself as a Windows service (Q2 answered: no).
- Product Phase C extras beyond after-the-fix and export (deeper drill-down).

## Phase list

| Phase | Title | Risk | Depends on |
|---|---|---|---|
| P1 | Foundations: shared tokens, version, demo capture, screenshot harness, CI | low | none |
| P2 | Case lifecycle core: Reopen, per-case lock, request id, retry list, marks and recovery | medium | P1 |
| P3 | Archive, Verify, Free, Restore in place, case history | high | P2 |
| P4 | Optional per-case key and Erase hardening | high | P2 |
| P5 | Investigation engine Phase A (`internal/investigate`) | high | P1 |
| P6 | Guided analysis Steps 0 to 10, Manual tools, checklist, after-the-fix, export | medium | P5 |
| P7 | SQL and Mongo lanes and sources | medium | P5 |
| P8 | Blackbox recorder binary (`cmd/unravis-blackbox`) | high | P1 |
| P9 | Blackbox in Unravis: page, wizard, pack, scan, source, Step 9 | medium | P8, P5 |
| P10 | Blackbox SQL probe. DEFERRED out of Release 1 (A43); the section below is kept for a later release | medium | P9, P7 |
| P11 | One-file installer (`cmd/unravis-setup`) | high | P1, P8, P14 |
| P12 | GitHub Releases pipeline, manual update check, clean-machine smoke | medium | P11 |
| P13 | Release 1 hardening: final docs, full screenshot refresh, end-to-end review | medium | P2 to P9, P11, P12, P14 |
| P14 | Shared data folder and key-keeper service (`cmd/unravis-keeper`) | high | P1, P2 |

## Parallel lanes

After P1 merges, four lanes can run at the same time.

| Lane | Order | Note |
|---|---|---|
| A: cases | P2, then P3 and P4, then P14 | P3 and P4 both touch `internal/casestore/sqlite`. They may be built in parallel but merge one at a time, P3 first. P14 moves the data folder, so it merges after P3 and P4. |
| B: investigation | P5, then P6 and P7 | P6 and P7 can run in parallel after P5. P7 adds lanes; P6 renders them when present. P7 uses `internal/sqlconn` as it is; P14 swaps what is behind it. |
| C: Blackbox | P8, then P9 (also needs P5) | P8 can start the day P1 merges. Lane ends at P9 (P10 deferred). |
| D: ship | P11 (needs P8 for the Blackbox zip and P14 for the keeper), then P12 | P11's UI shell and preflight can start before P8 and P14 finish, using stubs. |

P13 starts when everything else has merged.

Shared files that cause merge conflicts, so merges in them are serialised: `cmd/audit-explorer/main.go`, `internal/server/server.go` (route table), `web/src/App.svelte`, `web/src/brand/tokens.css`, `tools/screens/screens.json`, `build.ps1`.

---

## P1. Foundations

risk: low

### Outcome

After P1:

- the brand tokens live in one file that the app and the future installer both use, and a test stops colours leaking out of it;
- every build knows its version (`unravis.exe --version`, `/api/version`, start-up log);
- demo mode can run with a fixed clock and capture-only cases, so screens are the same every time;
- one command captures every listed screen in both themes into `buildout/screenshots/`;
- a GitHub Actions workflow builds and tests on Windows for every push and pull request;
- `go.mod` is tidy.

No user-visible behaviour changes except `--version` and the version shown in the start-up line and in the About area.

### Write scope

- `web/src/brand/tokens.css` (new), `web/src/brand/fallback.ts` (new, only if charts need it)
- `buildout/brand/unravis-brand.md` (line 4 only)
- `web/src/app.css` (token blocks and `@font-face` removed, replaced by one `@import`)
- `web/src/lib/charts.ts`, `web/src/lib/components/CaseHome.svelte`, `web/src/lib/components/HealthPanel.svelte` (colour fallbacks only)
- `web/tests/tokens.test.cjs` (new)
- `internal/version/version.go`, `internal/version/version_test.go` (new)
- `internal/server/` (one new route `/api/version` and its test)
- `cmd/audit-explorer/main.go`, `cmd/audit-explorer/cases.go` (flag, start-up line, demo clock, demo cases)
- `internal/audit/` (only if the demo constructor needs a clock parameter; no logic change)
- `build.ps1`
- `go.mod`, `go.sum`
- `tools/screens/` (new)
- `.github/workflows/ci.yml` (new)
- `buildout/docs/` (new, draft pages)
- `buildout/screenshots/P1/` (new)
- `.gitignore` (add `tools/screens/node_modules/`, `tools/screens/test-results/`, `tools/screens/playwright-report/`)

### Must not change

- Any colour value, font, spacing or radius as rendered. The token move must be pixel-identical.
- The default behaviour of plain `--demo` (no disk writes, case routes still 503).
- Live mode behaviour, routes, data formats, case files, sealed files.
- The launch link format and the session file format.
- The compatibility copy `dist\api-audit-explorer.exe` (still produced until 2026-12-31).
- `web/dist` stays embedded the same way.

### Changes

**1. Shared brand tokens**

1. Create `web/src/brand/tokens.css`. Move into it, unchanged and in the same order:
   - the five `@font-face` blocks (IBM Plex Sans 400/500/600, Mono 400/500); fix the `url(./fonts/...)` paths to `url(../fonts/...)`;
   - the `:root` dark block;
   - `@media (prefers-color-scheme: light) { :root:not([data-theme="dark"]) { ... } }`;
   - `:root[data-theme="light"] { ... }`;
   - and any `:root[data-theme="dark"]` block if present.
2. `web/src/app.css` starts with `@import "./brand/tokens.css";`. Nothing else in it changes.
3. Colour fallbacks:
   - `charts.ts` lines 23 to 25: the light fallback palette becomes a read of the CSS variables with no hex fallback, or a fallback list exported from one place (`web/src/brand/fallback.ts`) that the token test checks against `tokens.css`. Pick the second if charts render before CSS loads in tests.
   - `CaseHome.svelte` 257 to 259 and `HealthPanel.svelte` 264 to 271: drop the hex fallback inside `var(--x, #hex)`; the token always exists.
4. `web/tests/tokens.test.cjs`:
   - scans `web/src/**/*.{css,svelte,ts}` except `web/src/brand/`;
   - fails on `#rgb`, `#rrggbb`, `#rrggbbaa`, `rgb(`, `rgba(`, `hsl(` literals;
   - allowlist, each with a reason in the test: `Inspector.svelte` `#000` in a mask; `web/src/brand/fallback.ts` if used;
   - second check: every `var(--name)` used in `web/src` is defined in `tokens.css` or locally in the same file.
5. Update `buildout/brand/unravis-brand.md` line 4 to name `web/src/brand/tokens.css` as the source of truth.

**2. Version**

1. `internal/version/version.go`: `var Version = "dev"`, `var Commit = ""`, `var Date = ""`, `func String() string` returning `Unravis vX.Y.Z (commit abc1234, built 2026-10-27)` or `Unravis dev`.
2. `version_test.go`: default string, filled string, no empty brackets.
3. `build.ps1`: new `-Version` parameter. Default: `git describe --tags --always --dirty`, falling back to `dev` when git is missing. Passes `-ldflags "-X api-audit-explorer/internal/version.Version=$v -X ...Commit=$c -X ...Date=$d"`. Same flags for the compatibility copy (it is a copy, so nothing extra).
4. `main.go`:
   - `--version` prints `version.String()` and exits 0 before anything else runs (no port, no browser, no files);
   - the start-up line becomes `Unravis vX.Y.Z listening at <url> (demo=<bool>)`. Keep the rest of the line so log parsers still match `listening at`.
5. `/api/version` (behind the same session check as other `/api/` routes): `{"version":"...","commit":"...","date":"..."}`. Test in `internal/server`.
6. Show the version in the existing About or footer area if one exists; if none exists, add one quiet line to the Glossary or help panel footer. No new screen.

**3. Demo for capture**

1. `UNRAVIS_DEMO_NOW` (RFC 3339, for example `2026-10-20T09:00:00Z`): honoured only with `--demo`; passed to `audit.NewDemoWithPatterns`. A bad value stops start-up with a plain message: `UNRAVIS_DEMO_NOW must look like 2026-10-20T09:00:00Z.` Ignored, with a log line, without `--demo`.
2. `UNRAVIS_DEMO_CASES=1`: honoured only with `--demo` and only when `AUDIT_CONFIG_DIR` is set. Opens the normal case store under that folder and seeds a fixed set of cases (Open, Closed, one with a failed import in the retry list, one Untrusted). Later phases add Archived, Freed, Erased and per-case-key seeds as they build those states. Without `AUDIT_CONFIG_DIR` it refuses to start: `UNRAVIS_DEMO_CASES needs AUDIT_CONFIG_DIR so demo cases never touch your real cases.`
3. The seed is deterministic: fixed ids, titles, times derived from `UNRAVIS_DEMO_NOW`.
4. Plain `--demo` is unchanged. A test proves it: with neither variable set, the case routes still answer 503 and nothing is written.

**4. Screenshot harness (`tools/screens/`)**

1. `package.json` with pinned `@playwright/test` (exact version, no caret) and a lockfile. Only Chromium is installed (`npx playwright install chromium`).
2. `playwright.config.ts`:
   - projects `dark` and `light` (`colorScheme`), plus `dark-narrow` and `light-narrow` at 390x844 for screens marked `narrow`;
   - viewport 1440x900, `deviceScaleFactor: 1`, `timezoneId: "UTC"`, `locale: "en-GB"`;
   - animations disabled (`reducedMotion: "reduce"`), fonts awaited (`document.fonts.ready`);
   - `expect.toHaveScreenshot` with `maxDiffPixelRatio: 0.001`;
   - `snapshotPathTemplate: "../../buildout/screenshots/{arg}-{projectName}{ext}"`.
3. `screens.json`: one entry per screen: `id` (for example `p1-home`), `phase`, `doc` (the doc page that shows it), `narrow` (bool), `steps` (a short list: `goto`, `click`, `fill`, `waitFor`, `theme`). Start with about 8 screens that exist today: home, case list, case home, create wizard step 1, health panel, connections drawer, glossary, export panel.
4. `capture.spec.ts`: reads `screens.json`, runs the steps, calls `toHaveScreenshot("<phase>/<id>")`.
5. `run-screens.ps1`:
   - fails early with a plain message if `dist\unravis.exe` is missing (`Build first: .\build.ps1`);
   - makes a temp folder, sets `AUDIT_CONFIG_DIR` to it, sets `UNRAVIS_DEMO_NOW=2026-10-20T09:00:00Z` and `UNRAVIS_DEMO_CASES=1`;
   - starts `dist\unravis.exe --demo --no-browser --port 0`, reads its log until it sees `listening at` and the launch link, then passes the link to Playwright in `UNRAVIS_LAUNCH_URL`;
   - modes: `-Update` (write new baselines), default (compare), `-Only <id>`;
   - always stops the process and deletes the temp folder, even on failure.
6. Baselines are made on the CI runner (the reference renderer). Local runs on a developer PC use `-Update` only to look, never to commit, unless the PC is the agreed reference (see Risks).

**5. Docs**

1. Create draft pages in `buildout/docs/`: `index.md`, `getting-started.md`, `cases.md`, `sources.md`, `blackbox.md`, `guided-analysis.md`, `manual-tools.md`, `install.md`, `upgrade-and-uninstall.md`, `troubleshooting.md`, `security.md`, `screenshots.md` (how to capture). Pages for features not built yet hold a heading and "Coming in Pn".
2. `tools/screens/docs-check.mjs`: (a) every image link in `buildout/docs/*.md` points to a file that exists; (b) every `id` in `screens.json` appears in the `doc` page it names; (c) no page links to a screenshot of another phase that no longer exists.

**6. CI (`.github/workflows/ci.yml`)**

1. Triggers: push to master, pull requests.
2. Runner: `windows-2022` (pinned, not `windows-latest`, so the reference renderer does not move silently).
3. Steps: checkout; `actions/setup-go` with `go-version-file: go.mod`; `actions/setup-node` with Node 22 and npm cache; in `web/`: `npm ci`, `npm run check`, `npm test`, `npm run build`; `go vet ./...`; `go test ./...`; `go mod tidy` then `git diff --exit-code go.mod go.sum`; build `dist\unravis.exe` through `build.ps1 -Version ci-<sha>`; in `tools/screens/`: `npm ci`, `npx playwright install chromium`, `run-screens.ps1`; `node docs-check.mjs`.
4. On screenshot failure, upload the diff images as a workflow artifact.
5. All third-party actions pinned to a full commit SHA.

**7. go.mod**

1. Run `go mod tidy`. `github.com/microsoft/go-mssqldb` moves to the direct block. Commit the result.

**8. Docs and screenshots**

- Doc pages changed: all draft pages created; `getting-started.md` filled for today's start-up and `--version`; `screenshots.md` filled.
- Screens captured: `p1-home`, `p1-case-list`, `p1-case-home`, `p1-create-1`, `p1-health`, `p1-connections`, `p1-glossary`, `p1-export`, each in dark and light; `p1-home` and `p1-case-home` also narrow.

**9. Clumsy-user review**

Try: run the harness without building; run it with port 0 refused by a busy test listener; set `UNRAVIS_DEMO_NOW=yesterday`; set `UNRAVIS_DEMO_CASES=1` without `AUDIT_CONFIG_DIR`; force `data-theme="light"` on a dark system and the reverse; run `unravis.exe --version --demo`; run two harnesses at once; kill the harness mid-run and check the temp folder and process are gone.

### Proof

1. `.\build.ps1` passes; `dist\unravis.exe --version` prints the stamped version; with no tag it prints a `git describe` value.
2. Before the token move, capture baselines on the current master. After the move, `run-screens.ps1` reports zero changed pixels on all P1 screens.
3. `npm test` in `web/` includes `tokens.test.cjs` and passes; adding `color: #123456` to any component makes it fail.
4. `go test ./internal/version ./internal/server ./cmd/audit-explorer` passes, including: plain `--demo` writes nothing; `UNRAVIS_DEMO_CASES` without `AUDIT_CONFIG_DIR` refuses to start; bad `UNRAVIS_DEMO_NOW` refuses to start.
5. `go mod tidy` leaves no diff.
6. The CI workflow is green on a pull request, and the screenshot diff artifact appears when a deliberate change is pushed to a test branch.
7. Docs: `docs-check.mjs` passes; every P1 screen id is linked from its doc page; the captured images match the current build.
8. Clumsy-user findings are each a test or a written "won't fix".

### Depends on

None.

---

## P2. Case lifecycle core

risk: medium

### Outcome

Closed cases can be reopened. Every case action runs under one lock per case and checks the state the page believes, so two tabs cannot fight. Create is safe against double press, reload and restart. The "needs another try" list is stored in the case with a retry per item. A marks-and-recovery framework exists that P3 and P4 use for Archiving, Freeing and Erasing.

### Write scope

- `internal/casestore/` and `internal/casestore/sqlite/`
- `internal/server/` (case routes)
- `cmd/audit-explorer/cases.go` (recovery at start-up, demo seeds for new states)
- `web/src/lib/components/CaseHome.svelte`, `web/src/components/wizard/`, `web/src/lib/components/CreateWizard*`, case list components, `web/src/lib/api*` (case calls)
- `web/tests/` (new tests)
- `tools/screens/screens.json`, `buildout/screenshots/P2/`, `buildout/docs/cases.md`

### Must not change

- The case file format version, unless a migration with a test is added in the same phase.
- Import parsing and analysis outputs.
- Sealed files and their DPAPI scope.
- The 423 (key locked) and 409 (untrusted) answers keep their meaning; the new 409 for "changed in another tab" uses a distinct message id.

### Changes

1. Per-case lock in the store; every mutating call takes it.
2. Belief check: each mutating request carries `expectState`; mismatch answers 409 with message "This case changed in another tab", except the listed exceptions in spec v7.
3. Reopen: Closed to Open; applies the saved Mongo link and filters; offers the sampler, never starts it. Manifest `Closed` stamp cleared, history entry written.
4. Create request id: kept for one wizard run; repeated requests return the same case; a restart mid-create re-runs the remaining steps.
5. "Needs another try" list stored in the case, one retry per item, included in case files. Distinguish "not added to analysis" from "import failed" (C19).
6. Marks and recovery framework: a mark table (Archiving, Freeing, Erasing) with recovery hooks run at start-up, at unlock and per call. P2 ships the framework and tests with a fake mark; P3 and P4 register real ones.
7. Import is one transaction; a restart mid-import leaves nothing partial (C17).
8. Demo seeds: add a Closed case ready to Reopen and a case with two retry items.
9. Docs and screenshots: `cases.md` sections Create, Close and Reopen, Retry list, Two tabs. Screens: `p2-close` (118), `p2-closed-readonly` (119), `p2-reopen` (120), `p2-retry-list`, `p2-changed-elsewhere`, both themes.
10. Clumsy-user review: double-press Create; reload mid-create; kill the app mid-import; Close in tab A and import in tab B; Reopen twice fast.

### Proof

1. Go tests for C17, C18, C19 and the belief table in spec v7 pass.
2. Failure-injection points that belong to create and import (from INJ-1 to INJ-10) pass.
3. Web tests cover the 409 message and Reopen button states.
4. Docs: `cases.md` shows the P2 screens and `docs-check.mjs` passes; images match the build.
5. Clumsy-user findings closed.

### Depends on

P1 (demo cases for capture, CI, harness).

---

## P3. Archive, Verify, Free, Restore

risk: high

### Outcome

A case can be archived to a `.unravis-case` file without closing it first, verified by a real test restore, freed after one confirmation, and restored in place or as a new case. Every crash point recovers to the state the case was in. The case keeps a history of files made and restores.

### Write scope

- `internal/casestore/sqlite/` (archive, verify, free, restore, history)
- `internal/server/` (archive, verify, free, restore, history routes and jobs)
- `cmd/audit-explorer/cases.go` (recovery registration, demo seeds)
- `web/src/` archive, archived list, restore and free components; `HealthPanel.svelte` (orphan files)
- `web/tests/`, `tools/screens/screens.json`, `buildout/screenshots/P3/`, `buildout/docs/cases.md`, `buildout/docs/troubleshooting.md`

### Must not change

- Close and Reopen behaviour from P2.
- The 8 GiB case database limit.
- Unravis never deletes a finished `.unravis-case` file.
- Existing Export and Restore-as-new keep working for files made before this phase.

### Changes

1. Path rules from A17, with the slug test vectors from the spec.
2. Archive job: `.part`, read back, rename, mark Archived under the case lock; percent and Cancel; shares the Export and Restore lock. Free-space check 1.2 x.
3. Verify: restore into a temporary store, compare checksum and counts; M13 when the key changed.
4. Free in 3 stages (134 to 137): verify, confirm naming file and folder with the master key warning, then remove the working copy with the Freeing mark as commit (M19).
5. Restore in place (only while kept; result is Closed with a real closed time) and Restore as new (kept or freed, repeatable).
6. Case history and per-file "Key ending xxxx". Orphan and left-over complete files listed in the health panel.
7. Archived list: paged 50 at a time, own toggle and total; Missing cases shown.
8. Demo seeds: Archived kept, Archived freed, Missing.
9. Docs and screenshots: `cases.md` sections Archive, Verify, Free, Restore, History; `troubleshooting.md` orphan files. Screens 127 to 137 as `p3-archive`, `p3-archive-running`, `p3-archived-list`, `p3-restore-*`, `p3-free-1` to `p3-free-3`, `p3-archived-home` (122), both themes.
10. Clumsy-user review: archive to a UNC path, a `..` path, a 241-character path, a full disk, a folder inside the data folder; Cancel at 99%; kill during rename; free then try restore in place; copy the file elsewhere and restore as new twice.

### Proof

1. All archive scenarios (18) and the archive-related failure-injection points pass in Go tests.
2. Acceptance criteria for archive, verify, free and restore in Appendix C pass, run by hand once and recorded.
3. A kill at each injection point followed by a restart leaves the case in its original state (test).
4. Docs: P3 screens match the build; `docs-check.mjs` passes.
5. Clumsy-user findings closed.

### Depends on

P2 (lock, belief check, marks and recovery framework).

---

## P4. Optional per-case key and Erase hardening

risk: high

### Outcome

A case can have its own key (off by default) set in Create or Keys. A master key reset leaves such cases alone. Erase stops imports, closes streams, waits up to 30 s for readers, and uses an Erasing mark as its commit with automatic recovery.

### Write scope

- `internal/casestore/sqlite/` (key records, erase)
- key and master-key packages in `internal/` (whichever hold Argon2id and the check value today)
- `internal/server/` (key and erase routes)
- `web/src/` MasterKey components, Create wizard key step, Keys page, Erase dialog
- `web/tests/`, `tools/screens/screens.json`, `buildout/screenshots/P4/`, `buildout/docs/cases.md`, `buildout/docs/security.md`

### Must not change

- Cases without their own key keep working with the master key or "Not now".
- Demo never reads or asks for a key.
- Archive file format from P3, except to add the per-case key check value.

### Changes

1. Per-case key: Argon2id derivation, HMAC-SHA256 check value inside the sealed record, "Key ending xxxx" shown.
2. Create step and Keys page (S4, S17; screens 125, 126). Locked case answers 423 with the case-key message.
3. Master key reset warns first, counting archives that exist only as files, and skips cases with their own key.
4. Erase: short code typed; stop import; close streams; wait up to 30 s; Erasing mark; recovery. Hidden on an Archiving row; server refuses it. Freed case erase text names the case file.
5. Demo seeds: a case with its own key (locked) and an Erased tombstone.
6. Docs and screenshots: `cases.md` Keys and Erase; `security.md` key model in plain words. Screens `p4-case-key-create`, `p4-case-key-unlock`, `p4-erase-1`, `p4-erase-2` (138, 139), `p4-erased-home` (124), `p4-untrusted-home` (123).
7. Clumsy-user review: wrong key five times; reset master key with a mix of cases; erase while an import runs; erase while another tab streams events; kill during the 30 s wait.

### Proof

1. Go tests for key derivation, check value, reset behaviour, and erase injection points pass.
2. Acceptance criteria for keys and erase in Appendix C pass and are recorded.
3. Docs: P4 screens match the build; `docs-check.mjs` passes.
4. Clumsy-user findings closed.

### Depends on

P2 (lock, marks and recovery framework). Merges after P3 when both are ready (same package).

---

## P5. Investigation engine Phase A

risk: high

### Outcome

A new package `internal/investigate` turns a case's audit data into facts, signals and an evaluation for Steps 0 to 10, using only audit data (product Phase A). Each step has a readiness word. Each suspect has a colour and a cheapest next check. Thresholds are named and versioned. Golden tests lock the outputs.

### Write scope

- `internal/investigate/` (new)
- `internal/server/` (one read route per step, or one `/api/cases/{id}/investigation`)
- `internal/audit/` (read-only helpers only if missing)
- `testdata/investigate/` (golden inputs and outputs)
- `buildout/docs/guided-analysis.md` (method section only; screens come in P6)

### Must not change

- Existing `/api/analyze` and `/api/breakdown` outputs (the old views keep working until P6 replaces them).
- Case data or files; the engine only reads.

### Changes

1. Facts: per-API counts, P50/P95/P99, onset t0 (A24), reference window, D, A, C, P, O split (A21), concurrency from audit documents, Little's Law with lambda 40/s (A22), group sizes 20 and 100.
2. Signals: change rule (A23), shares of the extra time (Step 3), load versus pool (Step 4; pool limit entered by the user), timeline of changes (Step 5; manual release entry).
3. Readiness gate per step: Ready, Partly ready, Cannot test yet, Failed. Missing source gives Cannot test yet (A6).
4. Evaluation: hypothesis groups Q, L, S, D, M, H, C; suspect colours; grey for H2, M2, H3 in Release 1; "no clear origin" path (screen 96) and how sure we are.
5. Thresholds in one versioned table (`thresholds.go`, `Version = "2026-10-27"`), returned with every result.
6. Golden tests: at least the demo pattern data, a "no incident" day, a "SQL pool exhausted" day, a "missing IIS logs" day, and a gap-in-data day.
7. Docs and screenshots: `guided-analysis.md` method in plain words with the "In a nutshell" boxes per step. No new screens in this phase (engine only); P6 captures them.
8. Clumsy-user review (engine level): empty case, one request only, all requests at the same millisecond, clocks 2 s apart, reference window after incident window.

### Proof

1. Golden tests pass; changing any threshold without bumping `Version` fails a test.
2. Every step returns a readiness word and never "pass" for missing data (table test).
3. Docs: `guided-analysis.md` method section reviewed against the walkthrough; `docs-check.mjs` passes.
4. Clumsy-user findings closed.

### Depends on

P1 (CI and demo clock for stable golden data).

---

## P6. Guided analysis UI, Manual tools, checklist, after-the-fix, export

risk: medium

### Outcome

The case home leads through Steps 0 to 10 on a left step rail, renders the engine output, and offers Manual tools (filters, saved views, tool missing its source), the 4-list checklist, an after-the-fix comparison of days, and HTML/CSV export without payloads.

### Write scope

- `web/src/` (step rail, step pages, manual tools, checklist, after-the-fix, export)
- `internal/server/` (saved views, checklist state, export changes)
- `internal/casestore/sqlite/` (saved views and checklist tables only)
- `web/tests/`, `tools/screens/screens.json`, `buildout/screenshots/P6/`, `buildout/docs/guided-analysis.md`, `buildout/docs/manual-tools.md`

### Must not change

- Engine outputs from P5 (UI only renders them).
- Export never includes payloads.

### Changes

1. Step rail 0 to 10 with readiness stickers; step pages per wireframe screens 75 to 96.
2. Step 10 origin and victim table, suspects with colours and cheapest check.
3. Nearby activity (97), checklist with 4 lists (98), after-the-fix (99 to 105).
4. Manual tools (106 to 111) and mode switch.
5. Export HTML/CSV with no payloads (116, 117).
6. Docs and screenshots: `guided-analysis.md` one section per step; `manual-tools.md`. Screens `p6-step-0` to `p6-step-10`, `p6-no-clear-origin`, `p6-checklist`, `p6-after-fix-*`, `p6-tools-*`, `p6-export-*`; narrow versions of `p6-step-10` and `p6-checklist`.
7. Clumsy-user review: jump straight to Step 10; resize to 320 px; switch mode mid-step; save 100 views; export an empty case.

### Proof

1. Web tests for each step's states (Ready, Partly ready, Cannot test yet, Failed).
2. An automated check that no export contains a payload field.
3. Docs: P6 screens match the build; `docs-check.mjs` passes.
4. Clumsy-user findings closed.

### Depends on

P5.

---

## P7. SQL and Mongo lanes and sources

risk: medium

### Outcome

SQL and Mongo sources feed Steps 7 and 8. Four SQL checks run read-only. Grant scripts are shown for the owner to run. The connections page manages up to 20 saved connections. "Add another day" and source combinations work.

### Write scope

- `internal/dbhealth/`, `internal/sqlconn/`, Mongo source packages in `internal/`
- `internal/investigate/` (SQL and Mongo lanes only)
- `internal/server/` (source routes)
- `web/src/` (connections page, source steps, grant script panels, add data, add another day)
- `web/tests/`, `tools/screens/screens.json`, `buildout/screenshots/P7/`, `buildout/docs/sources.md`

### Must not change

- Sealed passwords never returned to the UI (A2).
- Unravis never runs a grant or profiler set-up itself (A1).
- P5 audit-only results when no SQL or Mongo source exists.

### Changes

1. Four SQL checks, read-only, with their grants per version (A30). Missing grant gives Cannot test yet with the script to fix it.
2. Mongo lane with clusterMonitor + read (A29); profiler guidance as a "Switched-on" guide and script.
3. Connections page (max 20), connection test words Works / Failed / Not tested.
4. Add data (112), add another day (113), source combinations (114), nothing read (115).
5. Docs and screenshots: `sources.md` SQL, Mongo, grants, profiler guide. Screens `p7-connections`, `p7-sql-form`, `p7-grant-script-2019`, `p7-grant-script-2022`, `p7-step-7`, `p7-step-8`, `p7-add-day`, `p7-nothing-read`.
6. Clumsy-user review: wrong password; 21st connection; SQL login with no grants; Mongo down mid-read; two days that overlap.

### Proof

1. Go tests with a fake SQL and Mongo responder for each check and each missing-grant case.
2. Docs: P7 screens match the build; `docs-check.mjs` passes.
3. Clumsy-user findings closed.

### Depends on

P5 (lanes plug into the engine).

---

## P8. Blackbox recorder binary

risk: high

### Outcome

`unravis-blackbox.exe` runs as the Windows service `UnravisBlackbox`, samples every 15 s, writes a manifest plus hourly NDJSON files, enforces housekeeping, serves status on port 7788 behind a read key, and installs or removes itself with full undo on failure.

### Write scope

- `cmd/unravis-blackbox/` (new)
- `internal/sampler/` (reuse; only additions that keep the in-app sampler unchanged)
- `internal/blackbox/` (new: config, manifest, store, housekeeping, status server, service control)
- `build.ps1` (build the second binary and the zip)
- `buildout/docs/blackbox.md`

### Must not change

- The in-app sampler behaviour and its route.
- The main `unravis.exe` (no new dependency on the service).

### Changes

1. `blackbox.json` config: name, folder (default `D:\UnravisBlackbox`), keep 7 days, 2 GB, 10% free disk, port 7788, probe block (off; filled in P10).
2. Sampler loop every 15 s; Running late after more than 3 missed intervals.
3. Store: manifest plus hourly NDJSON, append-only, fsync per sample batch.
4. Housekeeping always on: oldest files removed first to meet days, size and free-disk limits; Disk low below 10% free.
5. Status server on 7788: read key required (made at install, printed once); endpoints for status, manifest, file list and file download; clock in each answer for the 1 s check.
6. Service: `install`, `start`, `stop`, `apply` (restart), `remove`, `remove --delete-recordings`, `snapshot`. Install checks the five failure reasons (A39) and undoes itself on failure.
7. Pack: `unravis-blackbox-vX.Y.Z-windows-x64.zip` with the exe, `blackbox.json`, README, about 6 MB.
8. Docs and screenshots: `blackbox.md` sections What it records, Install, Remove, Housekeeping, Status page. Screen `p8-status-json` is a captured terminal text block (no browser screen in this phase).
9. Clumsy-user review: install twice; port 7788 busy; folder on a full disk; stop the service mid-write; wrong read key; clock moved by 5 s; antivirus quarantine simulated by deleting the exe.

### Proof

1. Go tests for housekeeping, manifest, gap detection and status answers.
2. A manual run on a Windows Server VM: install, run 10 minutes, stop, remove, remove with recordings; each failure reason produces its message and leaves nothing behind.
3. Zip size under 8 MB.
4. Docs: `blackbox.md` matches the commands and outputs; `docs-check.mjs` passes.
5. Clumsy-user findings closed.

### Depends on

P1 (version stamping, CI).

---

## P9. Blackbox in Unravis

risk: medium

### Outcome

Unravis has a Blackbox page: place a recorder through the 4-part wizard, build the zip, scan this PC, add a Blackbox by folder, UNC share or URL:port, see its state, and use it as a source in Step 9 with the gap rule applied.

### Write scope

- `internal/blackbox/` (client side: reader, pack builder)
- `internal/investigate/` (host lane and gap rule only)
- `internal/sqlconn/` or a new sealed store for read keys (never in case files)
- `internal/server/` (Blackbox routes)
- `web/src/` (Blackbox page, wizard, source step, Step 9)
- `web/tests/`, `tools/screens/screens.json`, `buildout/screenshots/P9/`, `buildout/docs/blackbox.md`

### Must not change

- The recorder binary's file and status formats from P8 (read only).
- Read keys never written to case files or exports.

### Changes

1. Wizard Name, Where, Settings, Review; pack builder writes the zip.
2. This-PC actions (Install, Start, Apply settings, Stop, Remove, Delete the recordings too, Snapshot) through an elevated helper; other servers get script steps only.
3. Add by folder, UNC or URL:port; read key sealed with DPAPI.
4. States from A36 shown as stickers.
5. Gap rule (A35) in the host lane; Step 9 renders it.
6. Docs and screenshots: `blackbox.md` sections Place, Add, States, Use in a case. Screens `p9-blackbox-list`, `p9-wizard-1` to `p9-wizard-4`, `p9-state-*` (one per state), `p9-step-9`, `p9-gap-warning`.
7. Clumsy-user review: add the same Blackbox twice; UNC share that drops mid-read; URL with wrong key; a 3-minute gap; remove from this PC while a case reads it.

### Proof

1. Go tests for the reader against recorded fixtures, including each state and both gap cases.
2. A test proves no read key appears in any case file or export.
3. Docs: P9 screens match the build; `docs-check.mjs` passes.
4. Clumsy-user findings closed.

### Depends on

P8 (recorder formats), P5 (engine lanes).

---

## P10. Blackbox SQL probe

**DEFERRED. Not part of Release 1 (owner ruling Q1, A43). Do not start. Kept as the starting design for a later release; the P8 `blackbox.json` probe block stays present and off.**

risk: medium

### Outcome

A Blackbox can run an optional SQL Server probe every 15 s, off by default, with the limits in A40. Its results feed Step 7 as a second SQL source.

### Write scope

- `internal/blackbox/` (probe), `cmd/unravis-blackbox/`
- `internal/investigate/` (SQL lane: accept probe data)
- `web/src/` (wizard Settings: probe tick box and login fields as decided)
- `tools/screens/screens.json`, `buildout/screenshots/P10/`, `buildout/docs/blackbox.md`, `buildout/docs/security.md`

### Must not change

- Probe off by default.
- The probe never creates, alters or writes anything on the SQL Server.

### Changes

1. Probe queries reuse the P7 SQL checks; 5 s timeout, `SET LOCK_TIMEOUT 2000`, `SET DEADLOCK_PRIORITY LOW`.
2. Login storage per the answer to Q1.
3. Docs and screenshots: `blackbox.md` SQL probe; `security.md` where the probe login lives. Screens `p10-wizard-probe-off`, `p10-wizard-probe-on`, `p10-step-7-with-probe`.
4. Clumsy-user review: probe on with a login that has no grants; SQL Server down; blocking held for 10 s on a test server.

### Proof

1. Go tests with a fake SQL responder: timeout, lock timeout, no grants.
2. A blocking test on a local SQL Server proves the probe gives up within limits and holds no locks.
3. Docs: P10 screens match the build; `docs-check.mjs` passes.
4. Clumsy-user findings closed.

### Depends on

P9, P7 and a decision on where the probe's login is kept (the old Q1, now deferred with the phase).

---

## P11. One-file installer

risk: high

### Outcome

`unravis-setup-vX.Y.Z-windows-x64.exe` installs, upgrades, repairs and uninstalls Unravis and, optionally, the Blackbox on this PC, with a branded UI, a silent mode driven by a config file, a log, and rollback on failure. Full design in `installer-and-releases.md`.

### Write scope

- `cmd/unravis-setup/` (new)
- `internal/setup/` (new: preflight, plan, actions, rollback journal, pipe, registry)
- `web/setup/` (new: installer UI, imports `web/src/brand/tokens.css`)
- `web/src/brand/components/` (new: sticker, buttons, step rail shared by app and installer), `web/tests/tokens.test.cjs` (extend to `web/setup/`)
- `build.ps1` (build setup after the two binaries)
- `cmd/audit-explorer/main.go` (only: honour an install-time config such as port, if the owner keeps that option)
- `tools/screens/` (installer screens), `buildout/screenshots/P11/`, `buildout/docs/install.md`, `buildout/docs/upgrade-and-uninstall.md`

### Must not change

- Case data, sealed files and their DPAPI scope during upgrade or repair.
- The app's own UI and tokens (the installer only imports them).

### Changes

See `installer-and-releases.md` sections 2 to 7. Summary:

1. Preflight, explanation before UAC, location, browser check, Blackbox option, port choice, firewall rule (opt-in), start-at-sign-in shortcut, first-run launch link.
2. Upgrade in place, repair, uninstall that asks before deleting data, silent mode, log, rollback journal.
3. Docs and screenshots: `install.md`, `upgrade-and-uninstall.md`. Screens `p11-welcome`, `p11-preflight-fail`, `p11-options`, `p11-uac-explain`, `p11-progress`, `p11-done`, `p11-failed-rolled-back`, `p11-upgrade`, `p11-repair`, `p11-uninstall`, both themes, captured from the installer UI in a capture mode that fakes the worker.
4. Clumsy-user review: double-click setup twice; cancel at UAC; cancel mid-copy; unplug network; run on a PC with Unravis open; install to a folder with spaces and non-ASCII; uninstall while Unravis runs; silent mode with a broken config.

### Proof

1. Go tests for preflight rules, the rollback journal (each action undone), and the silent config parser.
2. Clean VM run (Windows 11 and Server 2022): install, upgrade from the previous build keeping a case and a saved connection, repair, uninstall keeping data, uninstall deleting data.
3. Rollback test: inject a failure at each action; the PC returns to its prior state (files, services, registry, firewall).
4. Docs: P11 screens match the build; `docs-check.mjs` passes.
5. Clumsy-user findings closed.

### Depends on

P1 (tokens, version), P8 (Blackbox binary to package), P14 (keeper binary and the shared data folder to install). The installer adds two steps for P14: create `C:\ProgramData\Unravis` with its permissions, and install, upgrade or remove the `UnravisKeeper` service (admin approval, explained before the Windows prompt, undone on rollback). Uninstall asks before deleting the shared data and never deletes it silently.

---

## P12. GitHub Releases pipeline

risk: medium

### Outcome

Pushing a tag `vX.Y.Z` builds, tests, checks, optionally signs, and drafts a GitHub Release with the installer, the portable zip, the Blackbox zip and `SHA256SUMS.txt`. A clean-machine smoke test runs the installer silently before the draft can be published. Unravis has a manual "Check for updates" button.

### Write scope

- `.github/workflows/release.yml` (new), `.github/workflows/ci.yml` (shared steps)
- `.github/release-notes-template.md` (new)
- `tools/release/` (new: checksum, notes, smoke scripts)
- `internal/server/` and `web/src/` (update check button only)
- `tools/screens/screens.json`, `buildout/screenshots/P12/`, `buildout/docs/upgrade-and-uninstall.md`, `buildout/docs/security.md` (verifying downloads)

### Must not change

- CI behaviour for pull requests.
- No background network calls from Unravis.

### Changes

See `installer-and-releases.md` sections 8 to 11.

1. Release workflow on tag, build, test, checksum, optional sign, draft, smoke, publish by hand.
2. Update check button.
3. Docs and screenshots: verifying a download, updating. Screens `p12-update-none`, `p12-update-available`, `p12-update-offline`.
4. Clumsy-user review: tag without `v`; tag on a commit that fails tests; re-run a release; delete an asset and re-run; check updates offline; GitHub rate limit.

### Proof

1. A test tag `v0.0.1-rc.1` on a fork or the real repo produces a draft with four assets and a matching `SHA256SUMS.txt`.
2. The smoke job passes on `windows-2022` and `windows-2025`.
3. Docs: P12 screens match the build; `docs-check.mjs` passes.
4. Clumsy-user findings closed.

### Depends on

P11.

---

## P13. Release 1 hardening

risk: medium

### Outcome

Docs move to their final location, every screenshot is recaptured from the release candidate, one end-to-end clumsy-user review covers the whole flow (install, create, import, investigate, archive, free, restore, upgrade, uninstall), and `v1.0.0` is published.

### Write scope

- Final docs location (per Q6), `buildout/docs/` (removed or left as a pointer)
- `tools/screens/` (paths), all `buildout/screenshots/` (moved or refreshed)
- Bug fixes found in the review, each limited to the files the fix needs

### Must not change

- Features. Only fixes.

### Changes

1. Move docs and images; update `docs-check.mjs` paths.
2. Full recapture with `-Update` on the reference runner; review every changed image.
3. End-to-end review on a clean VM, both themes, a narrow window and a keyboard-only pass.
4. Upgrade test from the last pre-release to the release candidate.
5. Docs and screenshots: every page; every screen.

### Proof

1. All CI and smoke jobs green on the release candidate tag.
2. Docs: every screen id is in the final docs; `docs-check.mjs` passes against the final location; images match the release candidate.
3. Every end-to-end finding is fixed or recorded as "won't fix" with the owner's agreement.

### Depends on

P2 to P9, P11, P12 and P14 (P10 is deferred).

---

## P14. Shared data folder and key-keeper service

risk: high

### Outcome

Everyone who signs in to the PC sees the same cases, saved connections and settings, from one folder, `C:\ProgramData\Unravis`. A saved password is sealed by a small helper service (`UnravisKeeper`) and never reaches the UI, a log, a case file or an API reply. This delivers owner rulings A44 and A45.

### Write scope

- `cmd/unravis-keeper/` (new), `internal/keeper/` (new: pipe server, sealing, request audit log, rate limit)
- `internal/sqlconn/` (new implementation behind the existing interface: asks the keeper)
- `internal/` data-folder resolution, `cmd/audit-explorer/main.go` (data folder only), `internal/casestore/` (path only)
- `web/src/` (states for "key keeper not running", one-time migration prompt)
- `tools/screens/screens.json`, `buildout/screenshots/P14/`, `buildout/docs/security.md`

### Must not change

- A2: a sealed password is never returned to the UI, a log or a case file.
- The `.unravis-case` file format and per-case key rules (P4).
- The saved-connection API shape the UI already uses.

### Changes

PROPOSAL, details to confirm in the first step:

1. Spike first, one day: list the operations the saved connections must do (test, the P7 read-only checks, Mongo reads). If they are a narrow set, the keeper runs them for Unravis and returns rows only, so Unravis never holds a password. If not, stop and ask the owner before choosing a weaker design (keeper hands the password to the Unravis process over the pipe).
2. Data folder `C:\ProgramData\Unravis`: users can read and write cases and settings; the keeper's own folder is readable only by its service account, SYSTEM and Administrators.
3. Service `UnravisKeeper` under a virtual account (`NT SERVICE\UnravisKeeper`). Named pipe open to local interactive users only, never remote. Every request is logged (who, which saved connection, when) and rate limited.
4. One-time migration: a per-user `sqlconnections.dat` is offered for import into the keeper; the old file is removed only after the owner confirms the import worked. Saved Mongo links with a password are sealed the same way.
5. If the keeper is stopped, the UI says plainly that saved connections are unavailable and how to start it. Cases still open.
6. Docs and screenshots: `security.md` states what is protected and the honest limit (a local administrator can stop the service or take over the machine; no local scheme fully stops that). Screens `p14-connection-saved`, `p14-keeper-stopped`, `p14-migrate`.
7. Hostile-user review: another signed-in user tries to read the files, call the pipe from a remote session, replay a request, stop the service, and copy a saved connection.

### Proof

1. Go tests for sealing, the pipe (local only), the audit log, rate limiting and migration.
2. A two-user VM run: user B uses user A's saved connection and cases, and cannot read any file or API reply that contains the password.
3. A search test: the test password does not appear in any API response, log, case file or Unravis process memory dump.
4. Upgrade test: a saved connection survives upgrade, repair and keeper restart.
5. Docs: P14 screens match the build; `docs-check.mjs` passes.
6. Hostile-user findings closed or recorded as accepted limits in `security.md`.

### Depends on

P1, P2. It swaps the implementation behind `internal/sqlconn`, so P7 does not wait for it. P11 installs the service.

---

## Questions (all answered 2026-10-27)

The owner answered all ten. The tables stay as the record of the options; the line under each heading is the ruling (see decisions.md A43 to A53).

### Q1. Where does the Blackbox SQL probe store its login?

**Answered: no probe in Release 1 (option d). P10 deferred.**

| Option | Pros | Cons |
|---|---|---|
| a. Windows login: the service runs as a dedicated domain or virtual account that has a SQL login | No password stored anywhere; standard for SQL Server; easy to revoke | Needs a domain account or `NT SERVICE\UnravisBlackbox` granted on SQL; owner must run a script on SQL Server; harder on workgroup servers |
| b. SQL login sealed on the server by the installer (DPAPI machine scope, file ACL to the service account) | Works without a domain; set up in the wizard | A secret on disk; any admin on that server can unseal it; rotation is manual |
| c. SQL login sealed in Unravis and pushed to the Blackbox at each read | Secret never at rest on the server | Probe stops when Unravis is away, which defeats a recorder |
| d. No probe in Release 1 | No new secret; simplest | Loses SQL samples during the incident window |

Planner pick: a, with d as the fallback if the owner cannot grant a Windows login. P10 waits on this.

### Q2. Should Unravis itself run as a Windows service?

**Answered: no service for Unravis, but all data shared by everyone on the PC, and no one can ever see a password or bypass the protection. Delivered by a small key-keeper service and a machine-wide data folder (P14, A44, A45). This is none of a, b or c as written.**

| Option | Pros | Cons |
|---|---|---|
| a. Per-user app, start at sign-in (planner pick) | Works with user-scope DPAPI, session file and browser opening as built | Not running when nobody is signed in; each user has their own data |
| b. Service under a dedicated account plus a launcher that gets a launch code over a named pipe | Always on; one data set | DPAPI sealed files must move to the service account; big change to session and launch; more attack surface |
| c. DPAPI machine scope and a service | Simple unseal | Any local admin or process can unseal; weakens A2 |

### Q3. Installer technology

**Answered: a, Go single file (A46).**

| Option | Pros | Cons |
|---|---|---|
| a. Go self-extracting with embedded web UI (planner pick) | One language; reuses tokens and Svelte; full control of brand, rollback and upgrade; no extra build tools | We own rollback and Add/Remove Programs entries; no MSI for group policy |
| b. WiX MSI | Transactions, repair, group policy deployment | .NET build tooling; branded UI is hard; custom actions needed for services and firewall anyway |
| c. Inno Setup | Mature, small, scripting; good upgrade handling | Pascal script; UI cannot use the brand tokens; second language in the build |
| d. NSIS | Tiny, flexible | Old scripting language; manual rollback; UI hard to brand |

MSIX is ruled out: signing is mandatory, and services plus firewall rules are limited.

### Q4. Installer UI shell

**Answered: a, app window with browser fallback (A47).**

| Option | Pros | Cons |
|---|---|---|
| a. WebView2 window, fall back to browser (planner pick) | Looks like a real installer; WebView2 is present on Windows 11 and most Server 2022 | Fallback path to maintain; WebView2 may be missing on Server 2019 |
| b. Always the browser (`--app` mode when possible) | One path; no WebView2 | Looks less like an installer; a stray tab can be closed |
| c. Native Win32 UI in Go | No browser need | Cannot reuse the tokens or Svelte; drift risk |

### Q5. Code signing

**Answered: c, SignPath Foundation (A48). Needs a public repo and a per-release approval. Until then releases ship unsigned (option a) with checksums.**

| Option | Pros | Cons |
|---|---|---|
| a. Unsigned, with checksums (Release 1 default) | Free; nothing to manage | SmartScreen "Windows protected your PC"; UAC shows "Unknown publisher"; some antivirus flag it |
| b. Azure Trusted Signing | Low monthly cost; keys held in the cloud; works in GitHub Actions | Eligibility rules (business or individual checks) to verify; Azure account needed |
| c. SignPath Foundation | Free for open source; GitHub integration | Repo must be public and meet their rules; approval step per release |
| d. OV certificate on a hardware token or cloud HSM | Name shown on UAC | Yearly cost; reputation still builds slowly; token handling in CI |

EV certificates no longer give instant SmartScreen reputation, so they add cost without that benefit.

### Q6. Final docs location

**Answered: a, repo plus in-app help (A49).**

| Option | Pros | Cons |
|---|---|---|
| a. `docs/user/` Markdown in the repo plus the same pages embedded as in-app help (planner pick) | Works offline; versioned with code; one source | In-app help needs a small Markdown renderer |
| b. Repo Markdown only | Simplest | Users on servers without internet cannot read GitHub |
| c. GitHub Pages site | Searchable, nice | Needs internet; another build |
| d. GitHub wiki | Easy edits | Not versioned with releases; screenshots drift |

### Q7. Case code length (spec gap)

**Answered: a, long id plus 8-character short code (A50). The wireframe's single 8-character code is updated to match before P2 UI work.**

| Option | Pros | Cons |
|---|---|---|
| a. Keep the spec: 26-character id plus 8-character short code shown to users (planner pick) | Matches the spec and tests; short code is what people type | Two ids to explain |
| b. 8-character code only, as in the wireframe | Simple | Higher chance of clashes; changes the spec |

### Q8. Browser on servers

**Answered: a, warn and link to Edge, carry on (A51).**

| Option | Pros | Cons |
|---|---|---|
| a. Preflight warns and links to Edge; install continues (planner pick) | Never blocks; clear | User may finish without a usable browser |
| b. Preflight blocks until a browser exists | No dead end | Annoying on servers where Unravis is reached another way |
| c. Bundle WebView2 runtime | Always works | Adds about 150 MB or a network download |

### Q9. Update check network policy

**Answered: a, manual button only (A52).**

| Option | Pros | Cons |
|---|---|---|
| a. Manual button only (planner pick) | No surprise network calls | Users may stay on old versions |
| b. Check at start-up once a week, can be turned off | Users learn of fixes | A network call by default on production-adjacent PCs |
| c. No update check | Nothing to explain | Upgrades only by reading GitHub |

### Q10. Firewall rule for port 7788

**Answered: a, opt-in and scoped to typed addresses (A53).**

| Option | Pros | Cons |
|---|---|---|
| a. Opt-in rule scoped to typed remote addresses (planner pick) | Least exposure | One more step in the wizard |
| b. Always add a rule for the domain profile | Just works on domains | Opens the port to the whole domain |
| c. Never add a rule; folder or UNC only | No open port | URL:port access does not work across machines |

## Risks

| Risk | Effect | Mitigation |
|---|---|---|
| Moving saved passwords from per-user DPAPI to the keeper (P14) | Saved connections lost or readable | One-time confirmed migration; old file removed only after the owner confirms; upgrade, repair and keeper-restart tests keep a saved connection |
| Keeper service is a new attack surface and a single point of failure | Passwords exposed, or saved connections unavailable | Local-only pipe, request log, rate limit, plain "keeper not running" state; hostile-user review; honest limit (local admin) written in `security.md` |
| Keeper proxy turns out too narrow for all connection uses | P14 redesign | Spike first; stop and ask before weakening the design |
| SmartScreen and antivirus on unsigned builds | Users scared off, Blackbox and keeper quarantined | Checksums, docs, SignPath (Q5), submit builds to Microsoft for analysis |
| SignPath needs a public repo and approval | Signing never starts, or private code made public | Check repo visibility before P12; ship unsigned with checksums until approved |
| Screenshot flakiness across renderers | False failures, ignored diffs | Pinned runner image and Playwright, fixed clock, fonts awaited, CI is the only baseline maker |
| Rollback gaps in a home-made installer | Half-installed PC | Journal of every action with an undo; injection test per action |
| Scope size (13 phases, P10 deferred, P14 added) | Release slips | Lanes run in parallel; deferred list kept honest |
| Blackbox disk use on production servers | Disk fills | Housekeeping always on, Disk low state, 10% free floor |
| Blackbox port exposure | Status reachable by others | Read key, opt-in firewall rule, loopback by default if no rule |
| Spec and wireframe disagree (case codes, Blackbox scope) | Rework | Q7 is answered (long id plus 8-character code); update the wireframe before P2 starts UI work |
