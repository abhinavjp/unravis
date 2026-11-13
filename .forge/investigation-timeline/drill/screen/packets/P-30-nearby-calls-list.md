# P-30 Nearby calls list

model: sonnet, effort: medium. Reason: table with chips, an "asking the server" state and null-safe columns.
risk: low. Review batch: RB-5. Wave: W1. Requirements: R-11, R-12, R-08.

## Outcome
`CallNearby.svelte` lists the exact API calls in the window, with the Everyone / Only this user / Only this API chips and the three light totals columns that say "not recorded" when the audit record has no totals.

## Write scope
- `web/src/components/CallNearby.svelte` (new)
- `web/tests/slowcall.nearbyui.test.cjs` (new)

## Must not change
- `web/src/lib/guided/*` (no import). The chip copy keys that already exist (`chipsLabel`, `chipEveryone`, `chipUser`, `chipApi`, `askingUser`, `askingApi`, `truncated`, `askFailed`, `noNearby`): reuse them, call the function ones with the arguments `DrillPopup.svelte` passes (read it for the call shape only).

## Changes
1. Props: `{ rows: NearbyRow[]; call: CallRef; chip: NearbyChip; start: number; end: number; asking: boolean; failed: boolean; truncated: boolean; zone: string; onChip: (c: NearbyChip) => void; onSelect: (row: NearbyRow) => void }`. `rows` are already filtered by the parent with `filterRows`.
2. Above the table: `<p data-testid="scall-nearby-hint">{DRILL_TEXT.nearbyHint(from, to, rows.length)}</p>`; chips as a `role="group"` labelled `chipsLabel`, three buttons `data-testid="scall-chip-{everyone|user|api}"`, `aria-pressed`, a chip is `disabled` when `!chipAvailable(chip, call)`.
3. States: `asking` → `<p role="status" data-testid="scall-nearby-asking">` with `askingWider` (chip everyone) or `askingUser` / `askingApi`; `failed` → `askFailed`; `truncated` → `truncated` text under the table; no rows and not asking → `noNearby`.
4. Table `data-testid="scall-nearby"`, header cells `colTime`, `colFromCall`, `colUser`, `colApi`, `colTook`, `colDatabase`, `colRedis`, `colWebCalls`. One row per call: time (`formatTime`, zone, seconds), `gapLabel(...)` with `{ thisCall: gapThis, during: gapDuring }`, user id or `notRecorded`, `Controller.Action`, `formatMs(durationMs)` or `notRecorded`.
5. Light totals: `const light = lightCells(row)`. `light === null` → ONE cell `<td colspan="3" class="muted" data-testid="scall-light-none">{DRILL_TEXT.notRecorded}</td>`. Otherwise three cells, each `formatMs(v)` or `notRecorded` for a null value. Never print 0 for null.
6. The call's own row has `class="thiscall"`, `aria-current="true"` and the text `thisCallMark`. A row is a button-like row: click or Enter → `onSelect(row)`.
7. No inner scroll box and no fixed height: the list grows and the page scrolls (the timeline above it is pinned by P-32).
8. Text test: testids present; `colspan="3"`; `lightCells(`; `chipAvailable(`; `gapLabel(`; `notRecorded` used; no `guided` import; no `overflow: auto|scroll`.

## Proof
- [ ] `cd web && node --test tests/slowcall.nearbyui.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] Not browser-verified here; live path in P-46.

## Depends on
P-02, P-08.
