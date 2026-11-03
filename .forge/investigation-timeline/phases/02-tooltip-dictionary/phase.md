# Phase 02: tooltip-dictionary

risk: low

## Goal
One reviewed dictionary holds the plain-language text behind every timeline tooltip, and the first two places use it: the hero and failure-trend chart tooltips and the call-trace lane heads (TR-22). The copy lint covers the dictionary, so no tooltip can name a cause or use an undefined technical word.

## Packets

Waves and dependencies live only in plan.md §3 and in each packet header (`wave:`) and `## Depends on`; check-plan.ps1 fails if a phase.md table carries them.

| Packet | Title |
|---|---|
| WP-08 | Tooltip dictionary |
| WP-09 | Tooltips use the dictionary |

## Edges
WP-08 -> WP-09. WP-08 writes only new files; WP-09 edits `charts.ts` and `CallTrace.svelte`, which no other packet of this phase touches. Later phases (04 tracing UI) import `explain` from `web/src/lib/copy/dictionary.ts` and may add dictionary entries only through a packet that lists that file in its Write scope.

## Review focus
- Dictionary text: every string is short, plain, causal-word free and passes `lintTerms` and `lintScreenStrings`; the six fixed wordings in WP-08 step 2 are exact.
- `explain` never throws and never returns a prototype property (`constructor`, `__proto__`).
- `dictionary.ts` has no imports, so `distChunks.test.cjs` (INV-6) stays green.
- `CallTrace.svelte` gained only the `title` attribute and the import (INV-7).
- Existing tooltip text is unchanged except the added P95 line; `pure.test.cjs` and `callTrace.test.cjs` are not edited.

## Phase proof
```
cd web && node --test tests/dictionary.test.cjs tests/tooltipWiring.test.cjs
cd web && node --test tests/distChunks.test.cjs tests/copylint.test.cjs
cd web && npm test
cd web && npm run check
rg -n "explain\(" web/src/lib/charts.ts web/src/components/CallTrace.svelte
rg "\.unravis\.db" .
```
All pass; the `rg` for `.unravis.db` returns nothing. No Go package is touched, so no `go test` is needed for this phase.

## Additions to plan section 7
- Browser (step 2): open the seeded demo case. Hover the hero chart on a bucket that has timed requests and read the tooltip: the last line explains P95 in plain words and the earlier lines are unchanged. Hover a failure-trend bucket with missing timing and see "Missing timing (not a failure)". Open Nearby activity, open a call trace, hover each lane head (SQL, Mongo, Redis, IIS, Other) and see its dictionary text; a lane with no entry shows no title. Check 1280 px and 375 px, light and dark; on 375 px a tooltip must not run off the screen. Compare the text with the wireframe tooltip notes. Console and network show no errors.
- Security (step 3): tooltips build HTML strings, so check the escaped path: `tooltipWiring.test.cjs` proves dictionary text goes through `escapeHTML`. Nothing here reads event attributes, cookies, IPs or referers.
- Performance (step 4): no new route, no chart data change; record the bundle size delta (the dictionary is a few hundred bytes of text and is part of an existing chunk, not a new one).
- Clumsy-user review (step 5): ask for `explain` with a missing, empty, numeric and prototype-name id (covered), and hover a lane head whose key is new to the dictionary.

## Commit file list
- `web/src/lib/copy/dictionary.ts`
- `web/tests/dictionary.test.cjs`
- `web/src/lib/charts.ts`
- `web/src/components/CallTrace.svelte`
- `web/tests/tooltipWiring.test.cjs`
- `.forge/investigation-timeline/phases/02-tooltip-dictionary/progress.md`
