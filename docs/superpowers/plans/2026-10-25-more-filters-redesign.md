# More filters panel redesign: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to carry out this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the "More filters" panel as one flat, ruled form in five groups, with removable chips and a filter count at the top. Behaviour, validation, copy meaning, ARIA and every existing test must keep working.

**Architecture:** The panel stays an inline full-width row inside `details.more-filters` in `SubBar.svelte`. The panel half of `QueryControls.svelte` splits into five group components under `web/src/components/filters/`, each wrapped in a shared `FilterGroup.svelte` and each owning its own drafts and effects. `QueryControls` keeps the bar (quick ranges and the request-type segments) and composes the groups. Group counts come from a new pure helper in `lib/depth.ts`, which is derived from `filterChips()`, so the button badge, the panel header and the group titles can never disagree.

**Tech Stack:** Svelte 5 runes (`$state`, `$derived`, `$effect`, `$bindable`, snippets, `bind:open`), TypeScript, plain CSS tokens in `web/src/app.css`, node:test `.cjs` source and pure-function tests.

**Spec:** The task brief from the session lead (problems: nested disclosures, duplicate Request type, help sentence under every field, scattered chart and scope fields, count only on the button). Also `.dev/design-polish-brief.md` and the impeccable references `craft-floor.md` and `operate.md`.

## Global Constraints

- Never read or reference `sql/`, `.mcp.json`, `.claude/`.
- Never commit `web/package-lock.json`; don't touch `web/dist`; do not run `build.ps1`.
- Source files use CRLF. Keep CRLF in every file you edit or create under `web/`.
- Verify with `cd web && node --test tests/*.test.cjs` (778 pass today; each task adds its own tests, and none may fail) and `cd web && npm run check` (svelte-check, 0 errors, 0 new warnings).
- Colours come only from tokens defined in the `:root` blocks of `web/src/app.css`. No hex, rgb or hsl values in component `<style>` blocks or in new app.css rules. Do not edit token values.
- Operate mode:
  - ruled rows, not boxes; one container level; no nested cards
  - labels in sentence case, 12px, `--text-2`, weight 500
  - controls use `--radius-sm` (4px) and a `--control-line` border
  - a selected state is a `--surface-2` fill plus an `--accent` line
  - a solid accent fill only on "Run analysis"
  - no eyebrows; no side borders wider than 1px; no new animation
  - `font-variant-numeric: tabular-nums` on every number (counts, inputs, UTC times)
- Copy keeps its meaning. Existing label text, error text and Hint `termId`s stay word for word unless this plan quotes a replacement. Add no glossary terms: the deck tests police `notMeaning`.
- Never add the strings "Total request" or "Run comparison" (pure.test.cjs bans them).
- Keep every id, `aria-*` attribute, `role="alert"` and `data-testid` listed under "Selectors tests rely on". Each moved control keeps its id.

## Review Focus

1. **Chip removal inside the open panel.** After removing a chip, focus lands on the next chip's remove button (or the previous one, else the "More filters" summary), never on `<body>`, and Esc still closes the panel. Pinned in Task 5, Step 1 (`FilterChips moves focus after a removal instead of dropping it`).
2. **The polling-type-unknown checkbox.** Checking it sets `kind` to `http_unknown` and shows the "Request type" chip. Unchecking it sets `kind` to `http`, so the bar's HTTP segment lights up again. Removing the chip sets `kind` to `all`, as it does today. Pinned in Task 1 (`unknownKindToggle maps the checkbox to a kind`) and Task 3 (`WhatGroup drives the unknown-kind checkbox from query.kind and never autoruns`).
3. **An invalid panel date, then the already-active quick range.** Today the bar's early return reads its own empty `invalidWindows`, so clicking the active range cannot repair broken panel dates. After the split, the bar returns early only when `activeRange === id && dateInputsValid`. Pinned in Task 5 (`the active quick range still repairs invalid panel dates`).
4. **A panel taller than the window at 1440×800 and 1280×720.** On wide screens `.app-shell` is `100vh` with `overflow: hidden`, so a flat panel would be clipped with no way to scroll. Above 720px `.more-body` scrolls inside itself and the Run analysis row stays visible. Pinned in Task 5 (`the open panel scrolls inside itself above 720px and never scrolls sideways`).
5. **Esc from every new focus stop** (checkbox, chip buttons, range inputs, UTC echo): it closes the panel and focuses the summary. Esc on a `<select>` keeps the native behaviour, and Esc inside a Hint popover closes only the popover. The `details.more-filters[open]` lookup in `appState.onWindowKey` must survive. Pinned in Task 5 (`Esc still closes the panel from inside it`).

---

## Decisions (read before any task)

**Placement: inline full-width row, not a popover or drawer.**
- `appState.onWindowKey` (appState.svelte.ts:950–965) already closes `details.more-filters[open]` and returns focus to its summary. A native `<details>` keeps that behaviour and the summary's `aria-expanded` for free.
- The right edge already belongs to the Inspector and the drawers (`--drawer-w`). A second right-hand surface would fight them.
- A popover would clip about 30 controls at 390px and needs a focus trap. Operate mode says not to make the first answer a modal.
- The open panel already takes `flex-basis: 100%; order: 5` (app.css:237), so the Run analysis row stays above it and stays reachable.

**Groups, in DOM and visual order:**

| Group (h2) | id | Contents |
|---|---|---|
| When | `when` | Investigate / Reference windows (h3 `#current-range-label`, `#reference-range-label`), From/To `DateTimeField`s, one UTC echo line per window, time-zone line, `timeInputError` alert |
| What | `what` | `#outcome` Status, `#rf-completion` Completion, `#controller`, `#action`, `#rf-operation`, new `#kind-unknown` checkbox |
| Request details | `details` | `#rf-user-ids`, `#rf-ui-versions`, `#rf-user-ips`, `#rf-status-codes`, `#rf-agent`, `#rf-url`, `#rf-audit-id` |
| Ranges | `ranges` | the six `RANGE_FIELDS` rows as a compact four-column table, plus `rangeNote` |
| Chart | `chart` | `#metric` Time measured, `#bucket` Chart interval, `#latency-target` Response-time target |

How this refines the lead's proposal:
- **"Request details" instead of "Who and where".** Status codes belong with the record fields, and keeping all four list drafts in one file keeps the `setList` cap, which clumsy.test.cjs pins, in one place.
- **"Ranges" instead of "Measures".** "Time measured" lives in Chart, and "Measures" would collide with it. "Ranges" is the panel's existing word.

**Removed nesting.** `details.advanced-filters`, `details.record-filters` and `details.exact-times` go away. The exact UTC times become one always-visible line per window: `UTC <time datetime={start}>{start}</time> – <time datetime={end}>{end}</time>`. It shows the same raw ISO values the old `<dl>` showed, and is hidden while that window has an invalid field.

**Request type dedupe.** `<select id="kind">` is deleted. The five segments in the bar stay the only general control. The one value only the panel could set (`http_unknown`) becomes the checkbox `#kind-unknown`, labelled "Only HTTP requests whose polling type is unknown". Like every other panel edit, it does not call `onScopeChange`.

**Help text diet.**
- Short qualifiers move into the label as `<span class="label-aside">`:
  - "exact match" for UI versions and IPs
  - "not case sensitive" for browser and URL
- Long notes are shortened to the exact copy given below, or dropped where a Hint already defines the term.
- The Ranges notes split in two: a definition, kept in the row's `title` and in visually hidden described-by text, and an effect, shown in the table's Note column.

**Chips and count at the top of the panel** (in SubBar, outside the `{#key}`):
- A count line `filtersInUseText(moreCount)`.
- The stale hint "Changes here apply when you press Run analysis." It is shown only while `results.displayedRunIsStale && !results.loading`, and it has no role, because App's `frozen-run-note` already announces.
- The same `<FilterChips>` as the outer row, wired to the same `removeChip` and `clearChips`.

While the panel is open the outer chip row is not rendered (`{#if !moreOpen}`), so no chip ever appears twice.

The per-value record chips, `record-count`, "Clear all record filters" and their CSS are removed from QueryControls.

**Shared state between the two QueryControls instances** stays as it is today:
- both are bound to `scope.query`, `scope.targetMs` and `scope.dateInputsValid`
- both remount on `scope.controlsResetKey`
- only the bar instance calls `onScopeChange`

The panel groups receive `bind:query` (and `bind:dateInputsValid` / `bind:targetMs` where noted) from the panel instance. They never receive `onScopeChange`.

**Responsive grid** (`.filter-panel` holds the five groups):
- **≤720px (390):**
  - one column; each group has a 1px `--line` top rule and padding `12px 0`
  - field grids, When windows and From/To are all one column
  - range rows become `"name name" "min max" "note note"`
  - the table header row is hidden
- **721–1099px (768):**
  - groups stay one column; `.field-grid` uses two columns
  - the When windows sit side by side (two columns); From/To stack inside each window
  - range rows use the full four columns
- **≥1100px (1440):** `grid-template-columns: repeat(2, minmax(0, 1fr)); column-gap: var(--space-8)`, with areas `"when when" "what details" "ranges chart"`. Inside When, each window puts From | To side by side.
- **Above 720px only:** `.more-filters[open] > .more-body` gets `max-height: min(72vh, calc(100dvh - var(--header-h) - 120px)); overflow-y: auto; overscroll-behavior: contain`.
- **Everywhere:**
  - every grid track is `minmax(0, …)` and every input is `min-width: 0; width: 100%`
  - no fixed widths over 150px
  - delete the old 920px `.query-ranges` rule

## Selectors tests rely on (do not rename or drop)

| Where | What | Pinned by |
|---|---|---|
| SubBar.svelte (APP_FILES) | `<span class="run-hint" id="run-hint" role="status">{runHint}</span>` exactly; no `{#if runHint}`; `class="primary-button" aria-describedby="run-hint"`; no "Total request" or "Run comparison"; `scopeRun.schedule()` lives elsewhere in APP_FILES (untouched) | pure 491, 1606, 1621 |
| QueryControls.svelte | `quickRanges` | pure 970 |
| Panel sources (QueryControls today; QueryControls plus `filters/*.svelte` after Task 5) | `syncOperationDraft(`; ≥4 × `syncListDraft(`; `$effect(` | pure 776 |
| Same | `splitList(text).slice(0, MAX_FILTER_VALUES)`; `.slice(0, MAX_USER_IDS)`; `data-testid="over-user-ids"`, `"over-ui-versions"`, `"over-user-ips"`, `"over-status-codes"` | clumsy 97 |
| Same | `RANGE_FIELDS`, `rangeNote`, `syncRangeDraft(`; no hex colour in `<style>` | pure 1082 (rewritten in Task 5) |
| app.css | exactly 3 × `--control-line: #hex`; `input, select {… border … var(--control-line)}`; `::placeholder { opacity: 1 }`; no `.crumbs`, `.topbar-meta` or `max-width: 14ch` | pure 1606 |
| depth.ts | `filterChips` id order and values, `applyChipPatch`, `moreFiltersCount === chips.length`, and no chip for the five segment kinds | pure 1352, 1381 |
| appState.svelte.ts | `details.menu[open], details.more-filters[open]` in `onWindowKey`; `removeChip` and `clearChips` | new pin in Task 5 |
| Ids kept for labels and ARIA (no test today) | `current-start`/`-end`, `reference-start`/`-end` (+ `-date`/`-time`), `current-range-label`, `reference-range-label`, `outcome`, `metric`, `bucket`, `latency-target`, `controller`, `action`, `rf-*` fields, `rf-operation-options`, `rf-{key}-min`/`-max`/`-label` | new pins in Tasks 2–4 |

## File map and agent split

| Task | Creates | Modifies | Runs after |
|---|---|---|---|
| 1 Foundation | `web/src/components/filters/FilterGroup.svelte`, `web/tests/filterpanel.test.cjs` | `web/src/lib/depth.ts` (append), `web/src/app.css` (append a block after line 248: `/* More filters panel */` plus `.visually-hidden`) | none |
| 2 When | `filters/WhenGroup.svelte`, `web/tests/filters.when.test.cjs` | none (`DateTimeField.svelte` read only) | 1 |
| 3 What, details, chart | `filters/WhatGroup.svelte`, `filters/DetailsGroup.svelte`, `filters/ChartGroup.svelte`, `web/tests/filters.fields.test.cjs` | none | 1 |
| 4 Ranges | `filters/RangesGroup.svelte`, `web/tests/filters.ranges.test.cjs` | none | 1 |
| 5 Integration | none | `QueryControls.svelte`, `SubBar.svelte`, `FilterChips.svelte`, `app.css` (delete dead rules, add responsive rules), `pure.test.cjs` (tests at 491, 776, 1082), `clumsy.test.cjs` (lines 107–110), `filterpanel.test.cjs` (append) | 2, 3, 4 |
| 6 Browser check | none | none | 5 |

How the split works:
- **Tasks 2, 3 and 4 can run in parallel.** Each creates new files only, and each group's layout lives in that component's scoped `<style>` (tokens only). No two of them touch `app.css`, `QueryControls.svelte` or the same test file.
- **Overlaps, all sequential:**
  - `app.css` is edited by Tasks 1 and 5.
  - `filterpanel.test.cjs` is created in Task 1 and appended to in Task 5.
- **The suite stays green between tasks.** Until Task 5 the new groups exist but are unused (svelte-check still type-checks them), and QueryControls keeps its old code, so every pinned regex still matches.
- **Why split QueryControls:**
  - it is about 300 lines, with four draft families and two effects
  - each group owns one family, so a reviewer can reject one group without touching its neighbours
  - the bar half stays in QueryControls because quick ranges and segments call `onScopeChange` and the panel never does
  - DateTimeField is not split; it already is its own component

---

### Task 1: Foundation (pure helpers, FilterGroup, panel CSS)

**Files:**
- Modify: `web/src/lib/depth.ts` (append after line 113)
- Create: `web/src/components/filters/FilterGroup.svelte`
- Modify: `web/src/app.css` (insert after line 248)
- Create: `web/tests/filterpanel.test.cjs` (copy the `loadTypeScript` and `read` helpers from clumsy.test.cjs:11–24)

**Interfaces (produces):**
- `export type PanelGroup = 'what' | 'details' | 'ranges' | 'chart';`
- `export function panelGroupCounts(chips: readonly FilterChip[]): Record<PanelGroup, number>`. It maps by `chip.id`:
  - `kind`, `outcome`, `controller`, `action`, `operation`, `completion` → `what`
  - `userIds`, `uiVersions`, `userIps`, `statusCodes`, `userAgentContains`, `urlContains`, `auditId` → `details`
  - every `RangeKey` → `ranges`
  - `metric` → `chart`
  - an unknown id throws, so a new chip cannot silently vanish from the counts
- `export function filtersInUseText(count: number): string` returns `'No filters in use'` for 0, `'1 filter in use'` for 1, and `` `${n} filters in use` `` above that.
- `export const unknownKindToggle = (checked: boolean): Query['kind'] => …`, giving `'http_unknown'` when checked and `'http'` otherwise.
- `FilterGroup.svelte` props: `{ id: string; title: string; count?: number; lede?: Snippet; children: Snippet }`. It renders:
  - `<section class="filter-group" data-group={id} aria-labelledby={'fg-' + id + '-title'}>`
  - `<h2 class="fg-title" id={'fg-' + id + '-title'}>{title}{#if count}<span class="fg-count">{count}<span class="visually-hidden"> in use</span></span>{/if}</h2>`
  - `{#if lede}<p class="fg-lede">{@render lede()}</p>{/if}` and then `{@render children()}`
- CSS classes produced: `.filter-panel`, `.filter-group`, `.fg-title`, `.fg-count`, `.fg-lede`, `.label-aside`, `.panel-head`, `.panel-count`, `.panel-stale`, `.visually-hidden`.

- [ ] **Step 1: Write the failing tests** in `web/tests/filterpanel.test.cjs`:

```js
const depth = loadTypeScript('src/lib/depth.ts');
const time = loadTypeScript('src/lib/time.ts');
test('panelGroupCounts sorts every More filters chip into exactly one group', () => {
  const query = time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']);
  assert.deepEqual(depth.panelGroupCounts(depth.filterChips(query)), { what: 0, details: 0, ranges: 0, chart: 0 });
  Object.assign(query, { kind: 'http_unknown', outcome: 'server_error', metric: 'api', controller: 'Orders', action: ' Get ',
    completion: 'pending', userIds: [7, 9], statusCodes: ['500'], urlContains: '/api/x', observedMs: { min: 100 }, requestKb: { min: 1, max: 5 } });
  const counts = depth.panelGroupCounts(depth.filterChips(query));
  assert.deepEqual(counts, { what: 5, details: 3, ranges: 2, chart: 1 });
  assert.equal(Object.values(counts).reduce((a, b) => a + b, 0), depth.moreFiltersCount(query));
  Object.assign(query, { operation: 'Orders.Get', auditId: 'a'.repeat(24), incompleteAgeMs: { min: 1000 }, observedMs: undefined });
  const more = depth.panelGroupCounts(depth.filterChips(query));
  assert.equal(more.what, 6); assert.equal(more.details, 4); assert.equal(more.ranges, 2);
  assert.throws(() => depth.panelGroupCounts([{ id: 'mystery', group: 'record', label: '', value: '', patch: {} }]));
});
test('filtersInUseText counts in words', () => {
  assert.equal(depth.filtersInUseText(0), 'No filters in use');
  assert.equal(depth.filtersInUseText(1), '1 filter in use');
  assert.equal(depth.filtersInUseText(11), '11 filters in use');
});
test('unknownKindToggle maps the checkbox to a kind', () => {
  assert.equal(depth.unknownKindToggle(true), 'http_unknown');
  assert.equal(depth.unknownKindToggle(false), 'http');
  const query = time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']);
  query.kind = depth.unknownKindToggle(true);
  assert.deepEqual(depth.filterChips(query).map(c => c.id), ['kind']);
  query.kind = depth.unknownKindToggle(false);
  assert.deepEqual(depth.filterChips(query), []);
});
test('FilterGroup is a labelled ruled section with a counted title and no box', () => {
  const src = read('src/components/filters/FilterGroup.svelte');
  assert.match(src, /<section class="filter-group"[^>]*aria-labelledby=\{'fg-' \+ id \+ '-title'\}/);
  assert.match(src, /<h2 class="fg-title" id=\{'fg-' \+ id \+ '-title'\}>/);
  assert.match(src, /<span class="visually-hidden"> in use<\/span>/);
  const css = read('src/app.css');
  const block = css.slice(css.indexOf('/* More filters panel */'));
  assert.match(block, /\.filter-group\s*\{[^}]*border-top:\s*1px solid var\(--line\)/);
  assert.doesNotMatch(block.slice(0, block.indexOf('/* ----------')), /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/);
  assert.match(css, /\.visually-hidden\s*\{[^}]*clip:/);
  assert.match(block, /\.fg-count\s*\{[^}]*tabular-nums/);
});
```

- [ ] **Step 2: Run the tests and confirm they fail.** Run `cd web && node --test tests/filterpanel.test.cjs`. Expected: FAIL, because `panelGroupCounts` is not a function and FilterGroup.svelte is missing.
- [ ] **Step 3: Implement the three helpers in `depth.ts`.** `panelGroupCounts` uses one `Record<string, PanelGroup>` built from the lists above plus `Object.keys(rangeLabels)`.
- [ ] **Step 4: Create `FilterGroup.svelte`** with the markup in Interfaces and no `<style>`; its rules live in app.css.
- [ ] **Step 5: Append the CSS block** to app.css, starting with the comment `/* More filters panel */`. Values:
  - `.filter-panel { display: grid; gap: 0; min-width: 0; }`
  - `.filter-group { min-width: 0; padding: 12px 0; border-top: 1px solid var(--line); }`, plus `.filter-group:first-child { border-top: 0; padding-top: 4px; }`
  - `.fg-title { display: flex; align-items: baseline; gap: 8px; margin: 0 0 8px; font-size: 13px; font-weight: 600; letter-spacing: -0.01em; }`
  - `.fg-count { color: var(--text-2); font-size: 12px; font-weight: 500; font-variant-numeric: tabular-nums; }`
  - `.fg-lede { margin: -4px 0 8px; color: var(--text-2); font-size: 12px; line-height: 1.45; }`
  - `.label-aside { margin-left: 6px; color: var(--text-2); font-weight: 400; }`
  - `.panel-head { display: grid; gap: 6px; padding-bottom: 10px; }`
  - `.panel-count { margin: 0; font-size: 12px; font-weight: 500; font-variant-numeric: tabular-nums; }`
  - `.panel-stale { margin: 0; color: var(--warn); font-size: 12px; }`
  - `.panel-head .chip-row { padding: 0; background: transparent; border: 0; }`
  - `.visually-hidden { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }`
- [ ] **Step 6: Run all tests and the type check.** Run `cd web && node --test tests/*.test.cjs && npm run check`. Expected: 782 pass, 0 errors.
- [ ] **Step 7: Commit:** `git add web/src/lib/depth.ts web/src/components/filters/FilterGroup.svelte web/src/app.css web/tests/filterpanel.test.cjs && git commit -m "More filters: group counts, FilterGroup and panel rules"`

---

### Task 2: WhenGroup

**Files:**
- Create: `web/src/components/filters/WhenGroup.svelte`
- Test: `web/tests/filters.when.test.cjs`
- Read only: `web/src/components/QueryControls.svelte` (the date state, `setWindow`, both date effects, and the `.query-ranges` markup); `DateTimeField.svelte`

**Interfaces:**
- Consumes: `FilterGroup` (Task 1); `DateTimeField` as it is today; `fromLocalParts`, `bucketForWindows`, `isManualBucket` and `intervalNote` from their current modules (same imports as QueryControls).
- Produces: `WhenGroup.svelte` with props `{ query: Query ($bindable), dateInputsValid: boolean ($bindable) }`. It owns `invalidWindows`, `timeInputError`, `setWindow(field, date, time)` and both effects, which move verbatim:
  - `dateInputsValid = invalidWindows.length === 0`, which loses its `part !== 'bar'` guard because only the panel renders this component
  - the clear-on-valid effect
- Markup:
  - `<FilterGroup id="when" title="When">`, whose lede snippet is `Times use your browser time zone: <strong>{browserZone}</strong>.`
  - `.when-windows` holds two `section.when-window`. Each has `aria-labelledby` pointing at its h3 and contains:
    - h3 `#current-range-label` "Investigate", or h3 `#reference-range-label` "Reference" followed by the `reference-period` Hint
    - one short `.when-sub` line, keeping the existing copy "Requests in the period you want to inspect." / "An earlier period to compare with."
    - `.window-bounds` with two DateTimeFields, keeping the ids `current-start`, `current-end`, `reference-start`, `reference-end` and the labels From / To
    - `<p class="utc-echo">`
  - Then `{#if timeInputError}<p class="inline-error" role="alert">{timeInputError}</p>{/if}` as the group's last child, keeping both existing error strings.
- Scoped CSS:
  - `.when-windows` is `grid-template-columns: minmax(0,1fr)` by default and `repeat(2, minmax(0,1fr))` at `min-width: 721px`
  - `.window-bounds` is one column, and `repeat(2, minmax(0,1fr))` at `min-width: 1100px`
  - `.utc-echo` is 12px, `--text-2`, tabular-nums, `overflow-wrap: anywhere`

- [ ] **Step 1: Write the failing test** `WhenGroup owns the date windows, keeps ids, and shows UTC without a disclosure`:

```js
const src = read('src/components/filters/WhenGroup.svelte');
for (const id of ['current-start', 'current-end', 'reference-start', 'reference-end']) assert.match(src, new RegExp(`id="${id}"`));
assert.match(src, /id="current-range-label"/); assert.match(src, /id="reference-range-label"/);
assert.match(src, /<Hint termId="reference-period"/);
assert.match(src, /dateInputsValid = invalidWindows\.length === 0/);
assert.doesNotMatch(src, /part !== 'bar'/);
assert.doesNotMatch(src, /<details/);
assert.match(src, /class="utc-echo"[\s\S]*?<time datetime=\{query\.start\}>/);
assert.match(src, /<p class="inline-error" role="alert">\{timeInputError\}<\/p>/);
assert.match(src, /Enter both a date and time for every range\./);
assert.match(src, /Complete every date and time with a valid local value before running the comparison\./);
assert.doesNotMatch(src.slice(src.indexOf('<style>')), /#[0-9a-fA-F]{3,8}\b|rgba?\(/);
```

Add the test `the UTC echo hides while its window is invalid`: assert the source matches `/\{#if !invalidWindows\.some\(/` before each `class="utc-echo"`, with two occurrences.

- [ ] **Step 2: Run the test and confirm it fails.** Run `cd web && node --test tests/filters.when.test.cjs`. Expected: FAIL with ENOENT.
- [ ] **Step 3: Build WhenGroup.svelte.** Move the logic over by copying it; QueryControls stays untouched until Task 5. Use the field keys already stored in `invalidWindows` to decide which window is invalid.
- [ ] **Step 4: Run the full check.** Run `cd web && node --test tests/*.test.cjs && npm run check`. Expected: all pass, 0 errors.
- [ ] **Step 5: Commit:** `git add web/src/components/filters/WhenGroup.svelte web/tests/filters.when.test.cjs && git commit -m "More filters: When group"`

---

### Task 3: WhatGroup, DetailsGroup, ChartGroup

**Files:**
- Create: `web/src/components/filters/WhatGroup.svelte`, `DetailsGroup.svelte`, `ChartGroup.svelte`
- Test: `web/tests/filters.fields.test.cjs`
- Read only: QueryControls.svelte, covering:
  - the `#outcome`, `#metric`, `#bucket`, `#latency-target`, `#controller` and `#action` blocks
  - every `rf-*` list field, the audit ID, completion and operation blocks
  - `setList`, `setUserIds`, the draft state, and the draft-sync effect

**Interfaces:**
- Consumes: `FilterGroup` and `unknownKindToggle` (Task 1); `Hint`; `metricLabels`; `syncListDraft`, `syncOperationDraft`, `splitList`, `itemsOverLimit`, `isValidOperation`, `isValidUserId`, `MAX_FILTER_VALUES` and `MAX_USER_IDS` from `lib/investigation`; `intervalNote`.
- **`WhatGroup.svelte`**, props `{ query ($bindable), groupNames: string[], count: number }`.
  - Owns the operation draft and its `$effect` with `syncOperationDraft(`.
  - Fields, in order inside a `.field-grid`:
    - `#outcome` (label and options unchanged, Hint `status-class`)
    - `#rf-completion` (Hint `incomplete-request`, options unchanged)
    - `#controller`, `#action`
    - `#rf-operation`, spanning both columns
  - The operation field keeps its datalist `rf-operation-options`, `aria-invalid`, and the alert "Not applied. Write the operation as Controller.Action, with a name on both sides of the dot." Its note shortens to "Suggestions come from the loaded results."
  - Then the checkbox row `<label class="check-row"><input type="checkbox" id="kind-unknown" checked={query.kind === 'http_unknown'} onchange={e => query.kind = unknownKindToggle(e.currentTarget.checked)} /> Only HTTP requests whose polling type is unknown</label>`.
- **`DetailsGroup.svelte`**, props `{ query ($bindable), count: number }`.
  - Owns the four list drafts, the user-id draft, the audit-ID draft, one `$effect` with four `syncListDraft(` calls, `setList` (keeping the literal `splitList(text).slice(0, MAX_FILTER_VALUES)`) and `setUserIds` (keeping `.slice(0, MAX_USER_IDS)`).
  - Lede snippet: "Narrow to records that match all of these. Separate several values with commas; leave a box empty to ignore it."
  - Fields in a `.field-grid`, in this order:
    - `#rf-user-ids`, keeping `aria-invalid`, the alert "Ignored, not whole numbers from 1 to 2147483647: …" and `data-testid="over-user-ids"`
    - `#rf-ui-versions`, with label aside "exact match" and `over-ui-versions`
    - `#rf-user-ips`, with label aside "exact match" and `over-user-ips`
    - `#rf-status-codes`, with `over-status-codes`
    - `#rf-agent`, with label aside "not case sensitive"
    - `#rf-url`, with label aside "not case sensitive"
    - `#rf-audit-id`, keeping `aria-invalid` and the alert "An audit ID is exactly 24 hexadecimal characters."
  - Each existing label keeps its words; the aside is added inside the `<label>`. The "Exact match." and "Not case sensitive." notes are deleted.
- **`ChartGroup.svelte`**, props `{ query ($bindable), targetMs ($bindable), count: number }`.
  - `#metric`: Hint `response-time`, options built from `metricLabels`.
  - `#bucket`: Hint `time-slice`; `min="10" max="86400"`; `oninput={() => intervalNote.clear()}`; note "10–86,400 seconds; no more than 120 points."; then the `intervalFitText` `<p class="field-note interval-note" role="status">`.
  - `#latency-target`: Hint `p95`; `placeholder="No target"`; note "Adds a chart line compared with P95."
- Every error `<p role="alert">` sits directly after its input, inside the same `.control-group`. A field with an error has `aria-describedby` that includes the error's id: `{id}-error`.
- Scoped CSS:
  - `.field-grid > .wide { grid-column: 1 / -1; }`
  - `.check-row { display: flex; gap: 8px; align-items: center; min-height: 30px; font-size: 13px; }`
  - `.check-row input { accent-color: var(--accent); }`
  - tokens only

- [ ] **Step 1: Write the failing tests** in `filters.fields.test.cjs`:
  - **`WhatGroup drives the unknown-kind checkbox from query.kind and never autoruns`**
    - the source matches `/id="kind-unknown"[^>]*checked=\{query\.kind === 'http_unknown'\}/` and `/unknownKindToggle\(e\.currentTarget\.checked\)/`
    - it does not match `/onScopeChange|id="kind"/`
    - it matches `syncOperationDraft(` and `$effect(`
  - **`DetailsGroup keeps the list caps and the over-limit notices`**
    - the source matches `/splitList\(text\)\.slice\(0, MAX_FILTER_VALUES\)/` and `/\.slice\(0, MAX_USER_IDS\)/`
    - it has 4 or more `syncListDraft(`
    - it has all four `data-testid="over-…"` attributes
    - it has 2 × `>exact match<` and 2 × `>not case sensitive<`
  - **`validation messages stay next to their own field`**
    - for each of `rf-user-ids`, `rf-audit-id` (DetailsGroup) and `rf-operation` (WhatGroup), take the source slice from `id="<id>"` to the next `<label` or `class="control-group"`
    - assert the slice matches `/aria-invalid=/`, `/role="alert"/` and `` new RegExp(`aria-describedby=\\{[^}]*${id}-error`) ``
  - **`ChartGroup keeps the chart controls and their limits`**
    - ids `metric`, `bucket` and `latency-target` exist
    - it matches `/min="10" max="86400"/`, `/intervalNote\.clear\(\)/`, `/10–86,400 seconds; no more than 120 points\./`, `/Adds a chart line compared with P95\./` and `/metricLabels/`
    - it does not match `/Total request/`
  - **For all three files:**
    - no `<details`
    - no hex, rgb or hsl colour in `<style>`
    - every `<Hint termId="…">` uses one of `status-class`, `incomplete-request`, `response-time`, `time-slice`, `p95`
- [ ] **Step 2: Run the tests and confirm they fail.** Run `cd web && node --test tests/filters.fields.test.cjs`. Expected: FAIL with ENOENT.
- [ ] **Step 3: Build the three components.** Copy the logic from QueryControls; do not edit QueryControls.
- [ ] **Step 4: Run the full check.** Run `cd web && node --test tests/*.test.cjs && npm run check`. Expected: all pass, 0 errors.
- [ ] **Step 5: Commit:** `git add web/src/components/filters/{WhatGroup,DetailsGroup,ChartGroup}.svelte web/tests/filters.fields.test.cjs && git commit -m "More filters: What, Request details and Chart groups"`

---

### Task 4: RangesGroup (compact range table)

**Files:**
- Create: `web/src/components/filters/RangesGroup.svelte`
- Test: `web/tests/filters.ranges.test.cjs`
- Read only: QueryControls.svelte, covering `rangeMeta`, `rangeText`, `syncRangeDraft`, `rangeNote`, and the `.range-filters` markup

**Interfaces:**
- Consumes: `FilterGroup` (Task 1); `RANGE_FIELDS`, `buildRange`, `syncRangeDraft` and `rangeConflict` from `lib/investigation`.
- Produces: `RangesGroup.svelte`, props `{ query ($bindable), count: number }`.
  - Lede: "Both ends are optional and included. A request that does not record the value never matches a range." That is followed by `{#if rangeNote}<p class="inline-error" role="alert">{rangeNote}</p>{/if}`.
  - `rangeMeta: Record<RangeKey, { label: string; describe: string; effect: string }>`. The labels are unchanged: "Observed time", "API time", "Audit write time", "Request size", "Response size", "Time without finishing". The exact `effect` copy is:
    - observedMs: `Complete requests only, so it sets Completion to Complete. Its minimum also raises the slowest-requests floor.`
    - apiTimeMs: the same as observedMs, ending `… slowest-requests floor when the time measured is API time.`
    - auditCreateTimeMs: `Complete requests only, so it sets Completion to Complete.`
    - requestKb, responseKb: `Requests that do not record a size never match a bounded range.`
    - incompleteAgeMs: `Incomplete and pending requests only, so it sets Completion to Incomplete. It cannot be combined with a time range.`
  - The `describe` value is the first sentence of today's text for that key, verbatim: "From the request start to its completion.", "Time spent in the API itself.", "Time spent creating the audit record.", "Size of the request body.", "Size of the response body.", "How long a request has gone without finishing, measured when the search runs."
- Markup:
  - `<div class="ranges-table">`, then `<div class="ranges-head" aria-hidden="true"><span>Measure</span><span>Min</span><span>Max</span><span>Note</span></div>`
  - then, for each `RANGE_FIELDS` entry, `<div class="range-row" role="group" aria-labelledby="rf-{key}-label" title={describe}>`, which contains:
    - `<span class="range-name" id="rf-{key}-label">{label} ({unit})</span>`
    - `<span class="visually-hidden" id="rf-{key}-desc">{describe}</span>`
    - two inputs `rf-{key}-min` and `rf-{key}-max`. They keep their aria-labels (`{label} minimum ({unit})` / `maximum`), `aria-invalid` and Min/Max placeholders, and add `aria-describedby="rf-{key}-desc rf-{key}-note"`, plus ` rf-{key}-error` while there is a problem.
    - `<span class="range-note" id="rf-{key}-note">{effect}</span>`
    - `{#if problem}<p class="inline-error range-error" id="rf-{key}-error" role="alert">Not applied. {problem}.</p>{/if}`
- Scoped CSS:
  - `.range-row` is `display: grid; grid-template-columns: minmax(9rem, 1.1fr) minmax(0, .6fr) minmax(0, .6fr) minmax(0, 2fr); gap: 4px 10px; align-items: center; padding: 6px 0; border-top: 1px solid var(--line-soft)`
  - `.ranges-head` uses the same columns, 12px, `--text-2`, weight 500
  - `.range-error { grid-column: 1 / -1; margin: 0; }`
  - `.range-note` is 12px, `--text-2`
  - inputs are `font-variant-numeric: tabular-nums`
  - at `max-width: 720px`:
    - `.ranges-head { display: none; }`
    - `.range-row` gets `grid-template-columns: repeat(2, minmax(0,1fr))` with areas `"name name" "min max" "note note" "err err"`
    - the inputs, note and error get the matching `grid-area`s

- [ ] **Step 1: Write the failing tests** in `filters.ranges.test.cjs`:
  - **`RangesGroup is the only home of the range filters`**
    - matches `RANGE_FIELDS`, `rangeNote` and `syncRangeDraft(`
    - matches `/role="group" aria-labelledby=\{'rf-' \+ /` (or the template-string form), plus `/'rf-' \+ key \+ '-min'|rf-\$\{key\}-min/`
    - has no `<details`
    - has no hex, rgb or hsl colour in `<style>`
  - **`range errors sit in their own row and are announced`**
    - matches `/class="inline-error range-error"[^>]*role="alert">Not applied\. \{/`
    - the style matches `/\.range-error\s*\{[^}]*grid-column:\s*1 \/ -1/`
    - matches `/aria-describedby=/`
  - **`each range keeps its label and effect copy`**
    - assert each of the six `effect` strings above appears verbatim
    - assert the label `Time without finishing` exists
  - **`the range table drops its header and stacks at phone width`**
    - the style matches `/@media \(max-width: 720px\)[\s\S]*\.ranges-head\s*\{\s*display:\s*none/`
    - `.ranges-head` has `aria-hidden="true"`
- [ ] **Step 2: Run the tests and confirm they fail.** Run `cd web && node --test tests/filters.ranges.test.cjs`. Expected: FAIL with ENOENT.
- [ ] **Step 3: Build RangesGroup.svelte.** Copy `rangeText`, the effect and the problem logic verbatim from QueryControls, and split the old `implies` text into `describe` and `effect` as listed above.
- [ ] **Step 4: Run the full check.** Run `cd web && node --test tests/*.test.cjs && npm run check`. Expected: all pass, 0 errors.
- [ ] **Step 5: Commit:** `git add web/src/components/filters/RangesGroup.svelte web/tests/filters.ranges.test.cjs && git commit -m "More filters: Ranges table"`

---

### Task 5: Integration (QueryControls, SubBar, chips, CSS cleanup, test retargets)

**Files:**
- Modify `web/src/components/QueryControls.svelte`:
  - delete the whole panel markup and the panel-only state, effects and helpers
  - delete the `record-*` CSS, the per-value `chips` and `describeRange`
  - change the `applyQuickRange` early return
- Modify `web/src/components/SubBar.svelte`, lines 14–27.
- Modify `web/src/components/FilterChips.svelte`, adding focus handoff.
- Modify `web/src/app.css`:
  - delete `.query-ranges`, `.range-group`, `.range-title p`, `.query-footer`, `.browser-time-note` and `.advanced-content` (lines 437–440, 447–448, 457)
  - delete `.record-filters`/`.record-chips`/`.record-chip`/`.record-clear` and `.range-filters`/`.range-heading`/`.range-label`/`.range-pair`/`.reference-range` (lines 458–466)
  - remove `.exact-times` and `.advanced-filters` from the shared selectors on lines 449–456, keeping `.auth-options`
  - delete the 920px `.query-ranges` rule (line 768) and `.range-pair` from line 791
  - keep `.field-grid`, `.field-label` and `.control-group`, because LedgerPanel uses the first two
  - add the responsive `.filter-panel` and `.more-body` rules from "Decisions"
- Modify `web/tests/pure.test.cjs` (tests at 491, 776, 1082), `web/tests/clumsy.test.cjs` (107–110) and `web/tests/filterpanel.test.cjs` (append).

**Interfaces:**
- Consumes: everything from Tasks 1–4.
- **QueryControls panel part** (`part !== 'bar'`):
  - `const counts = $derived(panelGroupCounts(filterChips(query)));`
  - it then renders `<div class="filter-panel">`, which contains:
    - `<WhenGroup bind:query bind:dateInputsValid />`
    - `<WhatGroup bind:query {groupNames} count={counts.what} />`
    - `<DetailsGroup bind:query count={counts.details} />`
    - `<RangesGroup bind:query count={counts.ranges} />`
    - `<ChartGroup bind:query bind:targetMs count={counts.chart} />`
  - The bar part is unchanged except for `applyQuickRange`, which now starts `if (activeRange === id && dateInputsValid) return;`.
- **SubBar:**
  - `let moreOpen = $state(false); let summaryEl = $state<HTMLElement>();`
  - `<details class="more-filters" bind:open={moreOpen}>`, with `<summary bind:this={summaryEl}>` and the same content as today
  - `.more-body` starts with `<div class="panel-head"><p class="panel-count">{filtersInUseText(moreCount)}</p>`, then `{#if results.displayedRunIsStale && !loading}<p class="panel-stale">Changes here apply when you press Run analysis.</p>{/if}`, then `<FilterChips chips={scope.filterChipList} onRemove={removeChip} onClear={clearChips} onEmpty={() => summaryEl?.focus()} /></div>`
  - after that comes the existing `{#key}` panel QueryControls
  - the outer `<FilterChips …>` becomes `{#if !moreOpen}<FilterChips … onEmpty={() => summaryEl?.focus()} />{/if}`
  - the run-actions line stays byte-identical
- **FilterChips** gains the optional prop `onEmpty?: () => void` and a `bind:this` row element.
  - `remove(chip, index)` calls `onRemove(chip)`, then `await tick()`, then focuses `row.querySelectorAll('.filter-chip button')[Math.min(index, n - 1)]` if there is one, else calls `onEmpty?.()`.
  - `clear()` calls `onClear()`, then `await tick()`, then `onEmpty?.()`.
  - Labels, aria-labels, the `chip-row` role and "Clear all" stay unchanged.

- [ ] **Step 1: Write the failing tests** (append to `filterpanel.test.cjs`):
  - **`FilterChips moves focus after a removal instead of dropping it`**
    - the source matches `/await tick\(\)/`, `/onEmpty\?\.\(\)/` and `/querySelectorAll\('\.filter-chip button'\)/`
    - it still matches `aria-label={'Remove ' + chip.label + ' filter: ' + chip.value}` and `>Clear all<`
  - **`SubBar shows the count, the stale hint and the chips at the top of the open panel`**
    - the SubBar slice from `<div class="more-body">` to `{#key` matches `/filtersInUseText\(moreCount\)/`, `/results\.displayedRunIsStale && !loading/`, `/Changes here apply when you press Run analysis\./` and `/<FilterChips /`
    - SubBar matches `/<details class="more-filters" bind:open=\{moreOpen\}>/` and `/\{#if !moreOpen\}<FilterChips/`
    - `count-badge` is still present
  - **`Esc still closes the panel from inside it`**
    - appState matches `/details\.menu\[open\], details\.more-filters\[open\]/`
    - appState matches `/removeChip\(chip: FilterChip\) \{ applyChipPatch\(query, chip\); controlsResetKey\+\+; onScopeChange\(\); \}/`, which confirms behaviour is unchanged
    - none of the `filters/*.svelte` files contain `onkeydown`, so they do not swallow Esc
  - **`the active quick range still repairs invalid panel dates`**
    - QueryControls matches `/if \(activeRange === id && dateInputsValid\) return;/`
    - it does not match `/invalidWindows/`
  - **`the open panel scrolls inside itself above 720px and never scrolls sideways`**
    - app.css matches `/@media \(min-width: 721px\)[^@]*\.more-filters\[open\] > \.more-body\s*\{[^}]*max-height:[^}]*overflow-y:\s*auto/`
    - app.css matches `/@media \(min-width: 1100px\)[^@]*\.filter-panel\s*\{[^}]*grid-template-columns:\s*repeat\(2, minmax\(0, 1fr\)\)/`
    - app.css does not match `/\.query-ranges|\.advanced-filters|\.exact-times|\.record-chips/`
    - app.css still matches `/\.auth-options summary/`
  - **`the duplicate request-type select is gone`**
    - QueryControls and every filters file contain no `id="kind"` and no `http_unknown">HTTP (polling type unknown)`
    - QueryControls still contains the segmented kinds
- [ ] **Step 2: Retarget the existing pins**, keeping the same assertions. Each test reads a concatenated source.
  - Add the helper `const panelSources = () => ['src/components/QueryControls.svelte', ...fs.readdirSync(path.join(root, 'src/components/filters')).map(f => 'src/components/filters/' + f)].map(read).join('\n');` near the top of each file. pure.test.cjs uses `fs.readFileSync(path.join(root, f), 'utf8')`.
  - **pure 776:** `source = panelSources()`.
  - **pure 491:** add `...fs.readdirSync(path.join(root, 'src/components/filters')).map(f => 'src/components/filters/' + f)` to its file list.
  - **pure 1082:** rename it to `'the range filters live only in RangesGroup'`:
    - RangesGroup matches `RANGE_FIELDS`, `rangeNote` and `syncRangeDraft(`
    - QueryControls and the other four filters files do not match `/RANGE_FIELDS|rf-\$\{key\}|'rf-' \+ key/`
    - no `filters/*.svelte` or QueryControls `<style>` contains a hex colour
  - **clumsy 107:** `controls = panelSources()`.
- [ ] **Step 3: Run the tests and confirm they fail.** Run `cd web && node --test tests/*.test.cjs`. Expected: only the new Task 5 tests fail; the retargeted ones still pass, because the copies exist in both places.
- [ ] **Step 4: Implement the QueryControls, SubBar, FilterChips and app.css changes** described in Files and Interfaces.
- [ ] **Step 5: Run the full check.** Run `cd web && node --test tests/*.test.cjs && npm run check`. Expected: every test passes (778 plus the new ones), 0 errors, and no `ownership_invalid_mutation` warnings in check output.
- [ ] **Step 6: Commit:** `git add web/src/components/QueryControls.svelte web/src/components/SubBar.svelte web/src/components/FilterChips.svelte web/src/app.css web/tests/pure.test.cjs web/tests/clumsy.test.cjs web/tests/filterpanel.test.cjs && git commit -m "More filters: flat grouped panel with chips and count at the top"` (check `git status` first; `web/package-lock.json` must not be staged).

---

### Task 6: Browser verification (no code)

Run the dev server through `.claude/launch.json`, or `cd web && npm run dev`. Do every row in both themes (header toggle) at 1440×900 and at 390×844. Also do one pass at 768×1024 and one at 1280×720.

- [ ] **Layout**
  - `document.documentElement.scrollWidth <= innerWidth` with the panel open
  - groups appear in the order When, What, Request details, Ranges, Chart
  - 1440: the 2-column grid with When spanning the full width
  - 768: groups in one column and windows side by side
  - 390: everything in one column, and the range rows stacked as name / min-max / note
- [ ] **Look**
  - hairline rules between groups; no boxed group, no nested card
  - labels 12px `--text-2`, weight 500
  - inputs 4px radius
  - the only solid accent fill is "Run analysis"
  - the checkbox, the UTC times and every count use tabular numerals
  - in light theme, every text and line readable (AA)
- [ ] **Typing**
  - type into each field once:
    - the four date/time pairs
    - Status, Completion, Controller, Action, Operation, the checkbox
    - the seven Request details fields
    - all 12 range inputs
    - Time measured, Chart interval, Response-time target
  - the matching chip, the group count, the panel count and the button badge all update together
  - App's frozen-run note and the panel's stale hint appear, and no run starts
- [ ] **Validation, triggering each error once:**
  - clear a time, which gives the time error alert in When; Run analysis is disabled
  - user IDs `0, abc`
  - audit ID `xyz`
  - operation `Orders`
  - range min greater than max
  - "Time without finishing" together with Observed time, which shows `rangeNote`
  - more than 20 IPs, which shows the over-limit notice
  - chart interval `5`, which updates the run hint
  - each one: the error sits directly under its own field, the field has `aria-invalid="true"`, and a screen reader announces it (role=alert)
- [ ] **Chips**
  - remove a middle chip in the panel: focus moves to the next chip
  - remove the last one: focus goes to the summary
  - "Clear all": focus goes to the summary, and every panel filter is cleared
  - with the panel closed, the outer chip row returns; with it open, the outer row is not shown
- [ ] **Keyboard**
  - Tab order follows the visual order
  - Esc from the checkbox, a chip button, a range input and a text field closes the panel and focuses "More filters"
  - Esc on an open `<select>` only closes the select
  - Esc in a Hint popover closes only the popover
- [ ] **Review Focus 3 (invalid date repair)**
  - break a date in When, then click the already-active quick range
  - the dates repair and Run analysis becomes enabled again
- [ ] **Review Focus 4 (tall panel)**
  - at 1280×720 with the panel open, scroll inside it to Chart
  - the Run analysis row stays visible, and the page behind does not scroll

---

## Self-review

1. **Spec coverage.** Each requirement from the brief maps to a task:
   - nested disclosures removed: Tasks 2–5
   - Request type deduped: Tasks 1, 3, 5
   - help text cut: Tasks 3 and 4
   - chart and scope fields gathered: Task 3
   - count visible in the panel and in each group: Tasks 1 and 5
   - chips and Clear all at the top: Task 5
   - placement justified: Decisions
   - keyboard and Esc: Task 5 and Review Focus 5
   - 390, 768 and 1440 behaviour: Decisions, Tasks 2, 4, 5, 6
   - shared state: Decisions
   - errors next to their fields: Tasks 2–4
   - dirty hint: Task 5
   - themes: Global Constraints and Task 6
   - test-relied selectors: their own table
   - agent split and overlaps: the file map
2. **Step scan.** Every code step names its file, its props or signature, and the exact copy. Every test step lists its assertions. There are no TBDs.
3. **Type consistency.**
   - `PanelGroup` keys (`what`, `details`, `ranges`, `chart`) match the `counts.*` props in Task 5 and the FilterGroup ids.
   - `filtersInUseText`, `unknownKindToggle` and `panelGroupCounts` are named the same everywhere.
   - FilterChips `onEmpty` is used only in SubBar.
4. **Review Focus.** Each of the five lines has a named test in its owning task (1, 3, 5) plus a browser check in Task 6.
5. **Proportion.** The plan sets markup contracts, copy and assertions. Logic moves verbatim from QueryControls rather than being rewritten here.

## Open questions for the lead

- **Per-value removal is gone.** Removing the old record chips loses removal of a single list value, and "Clear all" now clears every panel filter, not only the record filters. Is that acceptable?
- **Remount on chip removal still drops drafts.** Chip removal still bumps `controlsResetKey`, so a half-typed draft in another field is lost. Should `removeChip`/`clearChips` stop remounting? The draft-sync effects make that safe, but it is an appState behaviour change.
- **The `applyQuickRange` change is a small bug fix**, forced by the split. Confirm.
- **Group names.** "Request details" and "Ranges" replace the proposed "Who and where" and "Measures". Confirm, or rename.
- **Unchecking `#kind-unknown` goes to `http`**, while removing the chip goes to `all` (existing patch). Keep that difference?
