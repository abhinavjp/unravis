# Round 1 plan-level review (plan.md, six phase.md, graph-wide)

check-plan.ps1: `packets in plan: 42; packet files: 42; errors: 0; same-wave overlaps: 0` -> OK.

## Findings

[MAJOR] phases/01,03,04,05,06 phase.md packet tables — stale against plan.md §3 and packet headers. Examples: 05/phase.md:14-19 has WP-61 w5 deps "WP-23, WP-60", 62a w5, 62b/c w6, 63 w7, 64 w8 vs plan.md:105-110 (61 w7 deps 23,60,11; 62a w1; 62b/c w8; 63 w9; 64 w10); 05/phase.md:21 still asks the caller to update plan.md rows. 01/phase.md has WP-04 w2 deps 45 (plan.md:80 w4 deps 45,02,03) and an applied "plan change needed WP-65b" note; 03/phase.md has WP-20 w3 deps 45,02 (plan.md:96 w6 deps 45,02,03,05) and WP-19 w4 deps 18b (plan.md:100 w5 deps 18b,17a); 04/phase.md waves/deps of 16b,17a,17b,22,10,23,11 differ; 06 has WP-58 w9 (plan w11). An executor resuming from phase.md picks the wrong frontier. — Regenerate every phase.md table from §3, delete the "plan.md must be updated" notes, and extend check-plan.ps1 to compare phase.md tables with §3.

[MAJOR] plan.md §5 + §6 (commit per phase, explicit paths) vs global waves — files written by packets of two phases get committed whole by whichever phase commits first, carrying the other phase's ungated hunks: `internal/casestore/sqlite/case.go` (WP-18a p03 w1, WP-65b p01 w3; it is in both the 01 and 03 commit lists), `httperr.go` (WP-04 p01, WP-19 p03), `w3c.go` and `iis_test.go` (p01 + WP-20 p03), `internal/server/tracing.go` (p03 + WP-17a p04), `TracingPaper.svelte`/`TracingChart.svelte` (p04 + WP-61 p05), `CaseHome.svelte` (WP-16b p04, WP-64 p05), `CaseTools.svelte` (WP-06b p01, WP-11 p04). Phase gates also run `go test` on packages holding other phases' in-flight work. "Revert by phase" (§11) is then false. — Fix one of: (a) state a phase commit order (01,02,03,04,05) and require that a phase commits only after every packet of every lower-numbered phase is merged, with `git add -p` for the listed shared files; or (b) switch `commit_granularity` to wave and drop the per-phase revert claim.

[MAJOR] WP-11 (Depends on: WP-10 only) — missing edges. It needs the `visible` prop that WP-23 adds (WP-11:29 "if TracingChart.svelte has no visible prop, STOP"), edits `TracingPaper.svelte` regions created/edited by WP-16b and WP-22, and edits `CaseTools.svelte` after WP-06b and reads the preview `scrubSensitive` from WP-06a. Order only works today by wave coincidence. Wave text is stale too: WP-11:29 "WP-23 (wave 4 ...)" and WP-23:15,19 "WP-11 (wave 5)". — Depends on: WP-10, WP-23, WP-22, WP-06b (update §3 row 104); fix the wave text in WP-11 and WP-23.

[MAJOR] WP-61 step 2 — contract mismatch with WP-17b/WP-23: it says "echarts `click` event mapped to the bucket by `dataIndex`" and "fields that WP-18 defined in `web/src/lib/tracing/types.ts`", but WP-17b:35,41 defines a plain-Svelte chart with `onSelect(slotIndex)` (click and Enter, "the Run-analysis hand-off itself is WP-61"), WP-17b:49 and WP-23:38 tests forbid `echarts`, and client types live in `web/src/lib/tracing/model.ts` (WP-16a); no packet creates `types.ts`. A blind implementer either adds echarts (breaks WP-17b/23 tests) or stops. — Rewrite step 2: in `TracingPaper.svelte` wire the existing `onSelect(slotIndex)` to `handOffBucket` using the response bucket's start/end from `model.ts` types; drop the `onBucketClick` prop or define it on top of `onSelect`; name WP-16b/17b/23 instead of WP-16/17/18.

[MAJOR] plan.md §7 step 1 and §10.2 — `rg "\.unravis\.db" .` can never return nothing: `.forge/` is not git-ignored (`.dev/` is, `.gitignore:8`) and plan.md:51,140 plus every packet's INV-4 line contain the literal. Phase 05 already uses a different form (05/phase.md:31). — Replace with a file check, e.g. `Get-ChildItem -LiteralPath D:\Projects\api-audit-explorer -Recurse -Force -Filter *.unravis.db` -> no output (or `git grep -n --untracked -e "\.unravis\.db" -- . ":(exclude).forge"` everywhere).

[MINOR] WP-10 (Depends on: WP-07a, WP-07b) — consumes `TracingLaneKey` from WP-16a `model.ts` (WP-10:14) with no edge; the 07a/07b edges have no consumed symbol (WP-10:20 takes scrub as a prop "so it does not depend on that type"). Plan §3:115 and §4:120 repeat "WP-07 -> WP-10". — Depends on: WP-16a (keep 07a/07b only if marked "ordering only" in §3).

[MINOR] Guided-entry open decision — handled safely (no deletion, WP-63 guard, WP-58 archive informational, §10.4 flags it). Gap: 05/phase.md:30 requires the archive run "after WP-62c and BEFORE WP-64", but the phase proof runs at phase end, after WP-64 (w10). — Add an explicit progress step/proof in WP-63 (w9): run `run-e2e.ps1 -Archive` and record the 80-pass baseline before WP-64 starts; WP-62b outcome "Archived tests still pass" should say "until WP-64".

[MINOR] plan.md §3 heading/§3 text — claims edges "copied from spec §9.2, authoritative" but added edges exist on 04, 05, 17a, 18b, 19, 20, 22, 10, 61, 65b. Stale packet text: WP-04:15 "No symbol from WP-02 is used (same wave)" (now w4 after 02/03); WP-65a:21 "WP-65b, which edits case.go in wave 2" (w3). — Reword the heading ("derived from §9.2, with added edges: ..."), fix those lines.

[MINOR] plan.md §5:133 — `.dev/spec-src/` is git-ignored (`.gitignore:8 /.dev/`); "committed with phase 01" needs `git add -f` of a 466 KB file. — Say `git add -f` explicitly or drop the spec copy from the commit.

[MINOR] plan.md §5 — `internal/server/cases.go`, `case_views.go`, `export.go`, `routes.go`, `int_test.go` are already modified in the tree, line-ending only (`git diff --ignore-cr-at-eol` empty). Committing them carries whole-file EOL churn. — Note it in §5 (accept, or normalise first).

[MINOR] plan.md §7 step 2 — "seeded demo case" is not named; it exists only via WP-25 (phase 03, w4) and `--demo`/`UNRAVIS_DEMO_CASES=1` (03/phase.md:33). — Name the launch.json configuration and the demo flags; say which demo case phases 01/02 use before WP-25 lands.

## Areas with no finding

- TR traceability vs spec §8/§9.2: none (every MVP-1 TR maps to at least one packet).
- Same-wave write overlaps: none. Cross-wave same-file writers are wave-ordered and, except WP-11 (above), connected by edges.
- Runner Hub rules (§8): none — no browser/app work, secrets, `sql/`, real DBs or acceptance steps 2-5 to the hub; hub patches reviewed locally.
- Push/merge policy: none — no push; master/tag/release need a fresh ask.
- Owner decisions R6-02/R6-03: not reviewed (approved).
