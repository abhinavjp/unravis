# WP-09 Tooltips use the dictionary
risk: low    size: M    wave: 2    model: sonnet

## Outcome
The hero chart tooltip explains P95 in plain words, the failure-trend tooltip takes its "Missing timing" wording from the dictionary, and each call-trace lane head shows its dictionary text as a hover title (TR-22). Existing tooltip text does not change except for the one added P95 line.

## Write scope
- `web/src/lib/charts.ts` (edit the `heroOption` and `failureTrendOption` formatters; one import)
- `web/src/components/CallTrace.svelte` (edit the lane head line; one import)
- `web/tests/tooltipWiring.test.cjs` (new)

## Must not change
- Anything in `web/src/lib/copy/` (WP-08 owns the dictionary), and the other `charts.ts` options, series and axes.
- `CallTrace.svelte` stays untouched apart from the one `title` attribute and the import (INV-7: only `explain()` labels are added there).
- Existing tooltip strings checked by `pure.test.cjs`, `chart.test.cjs` and `callTrace.test.cjs` stay green and unedited.
- All tooltip text built from the dictionary goes through `escapeHTML` (`charts.ts:38`).

## Changes
1. `charts.ts`: add `import { explain } from './copy/dictionary';` after the `lightFallback` import (line 10). The dictionary has no imports, so INV-6 holds.
2. `heroOption` tooltip formatter (about lines 158-165): when `withLatency` is true, append `'<br/>' + escapeHTML(explain('latency.p95'))` as the last line of the tooltip, after the "Slowest request" part. With `withLatency` false the output is unchanged.
3. `failureTrendOption` tooltip formatter (line 120): replace the literal `'<br/>Missing timing (not a failure): '` with `'<br/>' + escapeHTML(explain('incomplete')) + ': '`. The output text is identical, so `pure.test.cjs` still passes; the wording now lives in one place.
4. `CallTrace.svelte`: add `import { explain } from '../lib/copy/dictionary';` beside the other `../lib/copy/` import (line 10). On the lane head (line 121, `<div class="ct-lane-head">`) add `title={explain('lane.' + lane.key) || undefined}`. No other element, class or text changes. An unknown lane key gives no title.
5. Tests in `web/tests/tooltipWiring.test.cjs` (copy `loadTypeScript` from `chart.test.cjs` and add the module cache described in the escaped-text test; `palette()` is safe in node):
   - `tooltip wiring: the hero tooltip ends with the P95 explanation`: `heroOption([{ at: 0, count: 5, latency: 200, max: 400 }], 'UTC', true).tooltip.formatter([{ dataIndex: 0 }])` ends with the `latency.p95` plain text and still contains `Requests: 5` and `P95:`.
   - `tooltip wiring: the hero tooltip without latency has no explanation`: the same call with `false` contains no `About 95 of every 100`.
   - `tooltip wiring: tooltip text is escaped`: `loadTypeScript` in `chart.test.cjs` has no module cache, so a separate `loadTypeScript('src/lib/copy/dictionary.ts')` would be a different instance from the one `charts.ts` loads, and mutating it would change nothing. In `tooltipWiring.test.cjs` give the helper a cache: declare `const cache = new Map();` and, at the top of `loadTypeScript`, `if (cache.has(filename)) return cache.get(filename);`; before returning, `cache.set(filename, module.exports)` (set it right after `module` is created so a cycle cannot loop). Then `const dict = loadTypeScript('src/lib/copy/dictionary.ts')` and `const { heroOption } = loadTypeScript('src/lib/charts.ts')` share one dictionary, because the nested `require('./copy/dictionary')` in `charts.ts` resolves to the same path and hits the cache. In the test set `dict.DICTIONARY['latency.p95'].plain = '<b>&'` (restore the original value in a `finally`) and assert the hero formatter output contains `&lt;b&gt;&amp;` and no raw `<b>`. `DICTIONARY` is a plain mutable `Record<string, DictEntry>` (WP-08 step 1), so the assignment works; `explain` reads it on every call.
   - `tooltip wiring: charts.ts reads the missing-timing wording from the dictionary`: read `src/lib/charts.ts` as text; it matches `/explain\('incomplete'\)/` and `/explain\('latency\.p95'\)/` and no longer contains the literal `Missing timing (not a failure)`.
   - `tooltip wiring: the call trace lane head carries the dictionary title`: read `src/components/CallTrace.svelte`; it matches `/title=\{explain\('lane\.' \+ lane\.key\)/`, and the pattern `explain\(` matches exactly once in the file (the import line reads `{ explain }` and does not match).

## Proof
- `cd web && node --test tests/tooltipWiring.test.cjs` -> 5 tests pass.
- `cd web && npm test` -> pass (includes `pure.test.cjs`, `callTrace.test.cjs`, `distChunks.test.cjs`).
- `cd web && npm run check` -> 0 errors.
- `rg -n "explain\(" web/src/lib/charts.ts web/src/components/CallTrace.svelte` -> two hits in `charts.ts` (hero, failure trend) and one in `CallTrace.svelte`.

## Depends on
WP-08
