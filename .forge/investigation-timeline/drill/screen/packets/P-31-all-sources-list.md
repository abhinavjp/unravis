# P-31 "All sources in time order" list

model: sonnet, effort: low. Reason: a plain table over a ready-made model.
risk: low. Review batch: RB-5. Wave: W1. Requirements: R-10, R-13.

## Outcome
`CallAllSources.svelte` lists the slow items and event markers of every shown source in one time-ordered table, kept separate from Nearby calls.

## Write scope
- `web/src/components/CallAllSources.svelte` (new)
- `web/tests/slowcall.allsources.test.cjs` (new)

## Must not change
- Nothing else.

## Changes
1. Props: `{ items: SourceItem[]; zone: string; selectedKey: string | null; onSelect: (key: string) => void }` (`SourceItem` from `web/src/lib/drill/callReadout.ts`).
2. `<h3 data-testid="scall-all-heading">{DRILL_TEXT.allHeading(items.length)}</h3>`; no items → `<p data-testid="scall-all-empty">{DRILL_TEXT.allEmpty}</p>`.
3. Table `data-testid="scall-all"`, header cells `colTime`, `colSource`, `colWhat`, `colTook`. Row: time (`formatTime`, zone, seconds); source name with a colour dot (`laneApi` / `laneIis` / `laneDb`, dot uses `var(--lane-audit|--lane-iis|--lane-sql)` and is `aria-hidden`); `what`; `formatMs(tookMs)` or, for a marker, an em-free dash is NOT used: print `DRILL_TEXT.notRecorded` for a null duration of a bar and leave the cell empty for a marker.
4. The call's row has `aria-current="true"` and `thisCallMark`. The selected row has `class="selected"`. Click or Enter → `onSelect(key)`.
5. Under the table: `<p class="muted">{DRILL_TEXT.notShownTime}</p>` (matched by time only).
6. No inner scroll box.
7. Text test: testids present; the four header keys used; `notShownTime` used; `aria-current`; no `overflow: auto|scroll`.

## Proof
- [ ] `cd web && node --test tests/slowcall.allsources.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] Not browser-verified here.

## Depends on
P-02, P-10.
