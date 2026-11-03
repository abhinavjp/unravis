# WP-08 Tooltip dictionary
risk: low    size: S    wave: 1    model: haiku

## Outcome
One module holds the plain-language text for every tooltip and short label the timeline work needs, behind `explain(id)`, and the copy lint passes on all of it (TR-22). Nothing on screen changes in this packet; WP-09 wires the first two uses.

## Write scope
- `web/src/lib/copy/dictionary.ts` (new)
- `web/tests/dictionary.test.cjs` (new)

## Must not change
- `web/src/lib/copy/deck.ts`, `copylint.ts`, `types.ts` and `wordings.ts`. The dictionary is a separate module; it does not add glossary terms.
- No import of `charts.ts`, any `.svelte` file or `../api`: the module has no dependencies (INV-6, no chunk cycles).
- Copy rules: no causal word (cause, because, due to, led to, resulted in, responsible for, waiting on, wait for, blocked by), and no word from the lint's jargon list except the `P50` and `P95` names that `deck.ts` defines.

## Changes
1. `dictionary.ts` exports `interface DictEntry { term: string; plain: string }`, `const DICTIONARY: Record<string, DictEntry>` and `function explain(id: string): string`. `explain` returns `plain` for a known id, the `term` when `plain` is empty, and `''` for anything else, including `undefined`, `null`, numbers, `'constructor'`, `'__proto__'` and `'toString'` (look the id up with `Object.hasOwn`, never by plain indexing). It never throws.
2. `DICTIONARY` holds exactly these 23 entries (id, term, plain). The plain text is final and already passes the lint; do not reword it.
   - `status.4xx`, "Client errors (4xx)", "Requests the server turned down, such as a missing page or a refused sign-in."
   - `status.5xx`, "Server errors (5xx)", "Requests the server could not finish."
   - `incomplete`, "Missing timing", "Missing timing (not a failure)"
   - `latency.p50`, "Typical time (P50)", "Half of the requests finished faster than this."
   - `latency.p95`, "Slower-end time (P95)", "About 95 of every 100 requests finished within this time."
   - `latency.p95Ratio`, "Slower-end spread", "How much longer the slower end took than the typical request."
   - `lane.api-audit`, "API audit", "Requests recorded in the API audit log."
   - `lane.sql`, "SQL", "Queries recorded by the SQL source."
   - `lane.iis`, "IIS", "Requests recorded in the web server log."
   - `lane.mongo`, "Mongo", "Operations recorded by the Mongo source."
   - `lane.redis`, "Redis", "Commands recorded by the Redis source."
   - `lane.other`, "Other sources", "Events from sources without a lane of their own."
   - `event.duration`, "Duration", "How long the event took, as its source recorded it."
   - `event.time-taken`, "Time taken", "Observed elapsed time, never API processing time."
   - `iis.point`, "IIS point", "Drawn at its timestamp until the source semantics are confirmed."
   - `gap.time-end`, "Time field", "The W3C time field is assumed to be the request end."
   - `gap.resolution`, "Time resolution", "Log time has one-second resolution."
   - `attr.client-ip`, "Client IP", "The address the request came from."
   - `attr.referer`, "Referer", "The page that linked to the request."
   - `attr.cookie`, "Cookie", "A digest, not the value."
   - `join.unique`, "One match", "Exactly one record fits."
   - `join.ambiguous`, "Several matches", "More than one record fits, so none is picked."
   - `join.none`, "No match", "No record fits."
3. Note: the lane ids match `LaneKey` in `web/src/lib/callTrace.ts` (`sql`, `mongo`, `redis`, `iis`, `other`); `lane.api-audit` is for later packets.
4. `dictionary.test.cjs` copies the `loadTypeScript` helper from `web/tests/copylint.test.cjs` and loads `src/lib/copy/dictionary.ts` and `copylint.ts`, and takes the glossary with `const { deck } = loadTypeScript('src/lib/copy/deck.ts')` (the export is `export const deck`, as `callTrace.test.cjs` line 38 does); iterate `deck.terms`. Tests:
   - `dictionary: every seeded id has a term and a plain text`: all 23 ids above exist, each `term` and `plain` is non-empty and under 160 characters.
   - `dictionary: the fixed wording is exact`: the plain text of `incomplete`, `event.time-taken`, `iis.point`, `gap.time-end`, `gap.resolution` and `attr.cookie` equals the strings in step 2.
   - `dictionary: explain returns plain text and never throws`: a known id gives its plain text; `'missing'`, `''`, `undefined`, `null`, `5`, `'constructor'`, `'__proto__'` and `'toString'` each give `''`; the module source contains the fallback to `term` (assert with a regex on the file text).
   - `dictionary: the copy lint finds nothing`: map each entry to a `Term` (`id`, `term`, and `hint`, `definition`, `notMeaning` all set to `plain`) and assert `lintTerms([...deck.terms, ...mapped])` has no finding whose id starts with a dictionary id, and `lintScreenStrings(entries, deck.terms)` is `[]`.
   - `dictionary: the module has no imports`: read the file as text and assert it has no line starting with `import `.

## Proof
- `cd web && node --test tests/dictionary.test.cjs` -> 5 tests pass.
- `cd web && npm test` -> pass.
- `cd web && npm run check` -> 0 errors.

## Depends on
none
