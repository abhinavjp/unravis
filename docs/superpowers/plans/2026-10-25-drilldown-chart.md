# Drill-down chart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user pick 7 days, drag a window on a time chart, drag again inside it, and keep drilling down to 1 to 5 minutes. The bars stay readable at every level. When the narrow window is the one that matters, one action reruns the full analysis on it.

**Architecture:**
- The zoom stack in `web/src/lib/zoom.ts` remains the chart lens, and stays the default. Two new pure modules sit beside it:
  - `zoomBrush.ts`: the overview brush geometry
  - `promote.ts`: turns the lens window into a real query window
- `zoomView.svelte.ts` and `appState.svelte.ts` hold thin wiring over those modules.
- `chartZoom.ts` gets a live readout on the drag band.
- `ZoomStrip.svelte` becomes an always-visible strip with:
  - the brush
  - "Investigate this window"
  - "Back to …"

**Tech Stack:** Svelte 5 runes, TypeScript, ECharts, `node:test`. Tests load TypeScript with `ts.transpileModule` and render components with `svelte/server`.

**Spec:** There is no separate spec document. This plan implements the drill-down request of 2026-10-25, and the Decisions section below records what was settled. Design rules come from `.dev/design-polish-brief.md`.

## Global Constraints

- Never read or reference `sql/`, `.mcp.json`, `.claude/`.
- No client data in the plan, tests or fixtures. Fixtures are synthetic: use 2001 dates in UTC, for example `2001-10-22T00:00:00Z`.
- Web tests: `cd web && node --test tests/*.test.cjs` (778 pass before this work). `npm run check` = svelte-check 0 errors.
- Source files use CRLF. This includes new `.ts`, `.svelte` and `.test.cjs` files. Keep the BOM at the start of `web/tests/zoomStrip.test.cjs`.
- Never commit `web/package-lock.json`. Stage files by name, never with `git add -A` or `git add .`.
- Do not touch `web/dist` (`build.ps1` rebuilds it later).
- Design rules (from `.dev/design-polish-brief.md`):
  - no eyebrows
  - hairline rules
  - sentence case
  - colours only through the tokens in `web/src/app.css`
  - no new animation or transition
  - respect `prefers-reduced-motion`
  - a solid accent fill only on primary buttons. Every new control here is quiet or secondary.
- All new user-facing text goes in `web/src/lib/copy/zoomCopy.ts`, is added to `allZoomStrings()`, and passes the copy lint. Banned words include: cause, because, due to, waiting on, wait for, blocked by, led to, result in, result from, responsible for, proven, proof.
- Keep these existing behaviours unchanged:
  - slot snapping
  - the 1-minute floor (`MIN_VIEW_MS`)
  - keyboard parity for every pointer action
  - existing ARIA roles and labels
  - existing copy strings, except the two Task 6 removes
  - the lens as the default (zooming never changes `scope.query` on its own)

## Decisions

1. **Finer automatic interval (problem 3).**
   - The automatic interval becomes the smallest step on the interval ladder, at least 10 s, that keeps both the view and its reference at 120 points or fewer.
   - 180 is added to the ladder, so neighbouring steps are at most 2x apart. A view of 20 minutes or longer therefore gets 60 to 120 bars.
   - The `PREFERRED` per-level table goes away.
   - A manual interval keeps its current rule: it is kept if it fits, and raised if it would go past 120 points.
   - Below 20 minutes the 10 s floor applies: a 5-minute view has 30 bars and a 1-minute view has 6. This is accepted. The backend's floor is 10 s.
2. **Investigate this window (problem 2).**
   - What it does:
     - `query.start` and `query.end` take the lens window, rounded outward to whole seconds and clipped to the current query window.
     - The reference becomes `[start − width, start)`.
     - `bucketSeconds` follows the app's rule (`bucketForWindows`). A manual interval is kept only when it was manual before, still fits (`intervalFits`), and is smaller than the new width.
     - The full analysis then reruns.
   - Where it is available:
     - Only in the strip, and only when zoomed in.
     - Not while a run is loading, and not while the displayed run is stale (the reason is shown).
   - The trail back:
     - Each promotion pushes a trail entry. The trail holds at most 10 entries and lives in memory only.
     - No browser-history entry is added.
     - "Back to 7 days" restores the previous window and reruns. It also reopens the lens on the window that was investigated.
     - "Back to …" hides once the query window no longer matches the last promotion, for example after a manual date edit.
     - `resetInvestigation` clears the trail.
3. **Live readout (problem 4).**
   - While dragging on a chart, the band shows the snapped range and its duration, for example `Oct 24, 14:00 – 14:05 · 5 minutes`.
   - It updates only when the snapped slot pair changes.
   - A range under 1 minute shows `zoomCopy.tooShort`, in dimmer text.
4. **Overview brush (problem 5).**
   - The overview svg becomes a brush over the root window. It has:
     - two handles, each `role="slider"`
     - a draggable body
     - click-to-centre
   - It commits on pointerup, so one gesture makes one read.
   - A commit replaces the top of the stack (`setTop`). At the root it pushes a new frame instead.
   - Esc or `pointercancel` reverts the preview.
   - Keyboard follows the WAI-ARIA multi-thumb slider pattern:
     - Arrow keys move an edge by one bucket step.
     - PageUp and PageDown shift the window by its own width.
     - Home and End go to the root's edges.
     - Keyboard commits are debounced by 300 ms, and also commit on blur.
5. **Discoverability (problem 1).**
   - The `<details>` collapse goes. The strip is always visible:
     - row 1: "Back to …", the breadcrumbs, the six buttons, and "Investigate this window" (secondary)
     - row 2: the brush
     - row 3: level, range, interval, and the hint "Drag across a chart to zoom in."
     - then the existing notes
   - The charts get a crosshair cursor over the plot area.
6. **Touch.**
   - Charts and the brush keep `touch-action: pan-y`. A horizontal drag zooms; a vertical swipe scrolls the page.
   - Under `(pointer: coarse)` the brush handles get a 24px hit area.
   - Tap-to-zoom-a-slot stays as it is (`zoomCopy.tapToZoom`).
7. **Coarse resource samples in a narrow window.**
   - `ResourcePanel` logic does not change.
   - In a narrow window it shows the samples that exist plus the existing "not observed" gaps. It does not interpolate.
   - It shows a note only when the window holds no samples, and that note already exists.
8. **After Investigate.**
   - The new analysis becomes the new root of the zoom stack.
   - Where, Inspector, Export and Patterns follow `scope.query`.
   - Patterns reports are keyed by sources, start and end. Up to 3 are kept, so "Back to …" finds the earlier report again.
   - The Resource panel's stall markers stay empty until Patterns runs for the new window. This is a known limit, recorded under Risks.
   - The Ledger does not depend on the window.
   - The session snapshot saves the promoted window on its own, as it does for any query change.

## Review Focus

1. A double click or double Enter on "Investigate this window" must give exactly one run and one trail entry. The test is in Task 4: `planInvestigation refuses while a run is loading`, plus the source assertion that the guard runs before `pushTrail`.
2. Lens edges carry milliseconds after `scale` or `shift`. Promotion must give whole seconds inside the old window, and at least 1 minute wide. The test is in Task 4: `promotedScope rounds outward to whole seconds and stays inside the current window`.
3. A manual interval larger than the new window, or one that no longer fits, must fall back to the app rule. Otherwise the rerun would draw 1 bar or fail validation. The test is in Task 4: `promotedScope drops a manual interval that no longer fits`.
4. When the brush is dragged past either end, or its handles are crossed, it must clamp to the root, keep start before end, and keep at least 1 minute. The test is in Task 3: `brushDrag clamps, orders and keeps the 1-minute floor`.
5. A `pointercancel` or Esc in the middle of a brush drag must commit nothing and leave the stack as it was. The test is in Task 6: `the strip reverts the preview on pointercancel and Escape` (source assertion). The pure part is in Task 3: `brushDrag with mode 'none' returns the start view`.

---

## Task order and file overlap

| Wave | Task | Files |
|---|---|---|
| A | 1 Finer automatic interval | `zoom.ts` (bucket section), `tests/zoom.test.cjs`, `tests/zoomStrip.test.cjs` (interval strings only) |
| A | 3 Brush geometry | new `zoomBrush.ts`, new `tests/zoomBrush.test.cjs` |
| B | 2 Foundations: copy, readouts, setTop, brush model, store additions | `zoom.ts`, `zoomCopy.ts`, `zoomView.svelte.ts`, `tests/zoom.test.cjs`, new `tests/zoomView.test.cjs` |
| C | 4 Promotion | new `promote.ts`, new `tests/promote.test.cjs`, `appState.svelte.ts` |
| C | 5 Chart readout and crosshair | `chartZoom.ts`, new `tests/chartReadout.test.cjs` |
| D | 6 Strip redesign and App wiring | `ZoomStrip.svelte`, `App.svelte`, `zoomView.svelte.ts`, `zoomCopy.ts`, `tests/zoomStrip.test.cjs` |
| E | 7 Clumsy-user review and full verification | new `tests/drilldown.clumsy.test.cjs` |

Overlaps:
- `zoom.ts` and `tests/zoom.test.cjs`: Task 1 must merge before Task 2 starts.
- `zoomView.svelte.ts` and `zoomCopy.ts`: Task 2 must merge before Task 6.
- `tests/zoomStrip.test.cjs`: Task 1 must merge before Task 6.
- Tasks 1 and 3 can run in parallel. So can Tasks 4 and 5.

All paths below are relative to `web/`.

---

### Task 1: Finer automatic interval

**Files:**
- Modify: `src/lib/zoom.ts`: the `LADDER`, `PREFERRED` and `bucketForView` section
- Test: `tests/zoom.test.cjs`: the "interval keeps every view at 120 points" test, and the 25-hour day assertion near line 145
- Test: `tests/zoomStrip.test.cjs`: interval strings only

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - The signature and return type of `bucketForView(view, { manualSeconds? }): BucketChoice | null` do not change.
  - `LADDER` gains `180`.
  - `PREFERRED` is removed.

- [ ] **Step 1: Write the failing tests**

In `tests/zoom.test.cjs`, replace the `pick` assertions (lines 282 to 286):

```js
const pick = (width) => z.bucketForView({ start: week.start, end: week.start + width }).seconds;
const cases = [[MIN, 10], [5 * MIN, 10], [17 * MIN, 10], [HOUR, 30], [90 * MIN, 60], [3 * HOUR, 120], [5 * HOUR, 180],
  [23 * HOUR, 900], [DAY, 900], [36 * HOUR, 1800], [2 * DAY, 1800], [3 * DAY, 3600], [7 * DAY, 7200]];
for (const [width, seconds] of cases) assert.equal(pick(width), seconds, width / MIN + ' min');
```

Add the test `the automatic interval draws 60 to 120 bars for any view of 20 minutes or more`:
- for every whole-minute width from 20 min to 7 days (step 7 min, plus the end points 20 min and 7 days)
- at offsets `[0, 7 * MIN, 31 * MIN]` from `2001-10-22T00:00:00Z`
- assert `60 <= z.slotsOf(view, choice.seconds).length <= 120`, with a message naming the width and offset.

At line 145, change the 25-hour day assertions to `bucket.seconds === 900` and `slotsOf(child, 900).length === 100`.

In `tests/zoomStrip.test.cjs`:
- the hour model's interval line becomes `'Interval: 30 seconds (automatic).'`
- the day model, if asserted, becomes `'Interval: 15 minutes (automatic).'`
- the top model stays `'Interval: 2 hours (automatic).'`

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd web && node --test tests/zoom.test.cjs tests/zoomStrip.test.cjs`
Expected: FAIL on `pick(HOUR)` (60 ≠ 30) and the new 60-to-120 test.

- [ ] **Step 3: Implement in `src/lib/zoom.ts`**

- New `LADDER`: `[10,15,30,60,120,180,300,600,900,1800,3600,7200,10800,21600,43200,86400]`.
- The automatic branch of `bucketForView` returns `fitSeconds(view, MIN_BUCKET_SECONDS, true)`, which already checks the view and its reference. The `PREFERRED` lookup goes.
- The manual branch does not change.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd web && node --test tests/*.test.cjs`
Expected: all pass. If any other test assumed hourly bars for a day view, update its expected interval and note it in the commit body.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/zoom.ts web/tests/zoom.test.cjs web/tests/zoomStrip.test.cjs
git commit -m "Zoom: pick the finest interval that keeps 60 to 120 bars"
```

---

### Task 2: Foundations (copy, readouts, setTop, brush model, store additions)

**Files:**
- Modify: `src/lib/copy/zoomCopy.ts`
- Modify: `src/lib/zoom.ts`: new exports, plus the `stripModel` additions
- Modify: `src/lib/zoomView.svelte.ts`
- Test: `tests/zoom.test.cjs`
- Create: `tests/zoomView.test.cjs`. Compile the store with `svelte/compiler` `compileModule`, using the zoomStrip test's loader pattern. Stub `api.analyze`.

**Interfaces:**
- Consumes: Task 1's `bucketForView`.
- Produces, in `zoom.ts`:
  - `setTop(stack: ZoomStack, win: ZoomWindow): ZoomStack`
    - At the root it pushes. Otherwise it replaces the top frame.
    - It returns the same stack for an invalid window, for a window under `MIN_VIEW_MS`, for a window outside the root, or when nothing changed.
  - `rangeReadout(win: ZoomWindow, zone: string): string`
    - Returns `zoomText.readout(windowLabel(win, zone), durationLabel(width))`.
  - `dragReadout(slots: Slot[], a: number, b: number, bucketSeconds: number, view: ZoomWindow, zone: string): { text: string; ok: boolean }`
    - It snaps with `snapRange`.
    - Under 1 minute it returns `{ text: zoomCopy.tooShort, ok: false }`.
    - When the snapped range equals the view it returns `{ text: zoomCopy.readoutWhole, ok: false }`.
    - Otherwise it returns `{ text: rangeReadout(...), ok: true }`.
  - `StripModel` gains:
    - `brush: { rootStart: number; rootEnd: number; viewStart: number; viewEnd: number; stepMs: number; startText: string; endText: string }`
    - `hint: string`
- Produces, in `zoomView`:
  - `get view(): ZoomWindow | null`, which is null at the root
  - `setWindow(win: ZoomWindow): void`, which applies `setTop`, then `apply()`
  - `readout(fromIndex: number, toIndex: number): { text: string; ok: boolean }`
  - `expect(root: ZoomWindow, lens: ZoomWindow): void`
    - The next `follow(analysis)` whose query window equals `root` pushes `lens`.
    - Any `follow` clears the pending lens.
- Produces, in `zoomCopy`:
  - `hint`: 'Drag across a chart to zoom in.'
  - `investigate`: 'Investigate this window'
  - `investigateTitle`: 'Rerun the full analysis on this window, compared with the same length of time just before it.'
  - `investigateTop`: 'Zoom in first, then investigate the narrower window.'
  - `investigateBusy`: 'A run is in progress.'
  - `investigateStale`: 'Run analysis to apply your changes first.'
  - `brushStart`: 'Start of the zoomed window'
  - `brushEnd`: 'End of the zoomed window'
  - `readoutWhole`: 'This is the window on screen.'
  - In `zoomText`:
    - `readout(range, duration)` → `` `${range} · ${duration}` ``
    - `backTo(label)` → `` `Back to ${label}` ``
    - `investigated(range)` → `` `Now investigating ${range}.` ``
  - All of these are added to `allZoomStrings()`.

- [ ] **Step 1: Write the failing tests**

In `tests/zoom.test.cjs`:
- `setTop replaces the top frame, pushes at the root, and keeps the floor`:
  - At the root: `frames.length` goes 0 → 1.
  - One level down: the length stays 1 and the top equals the new window.
  - A 30-second window returns the same stack.
  - A window past `root.end` returns the same stack.
- `dragReadout snaps and describes the range`:
  - Use hour-level slots at 60 s, indices 3 to 7.
  - The text contains `'5 minutes'` and `ok === true`.
  - With `a === b` on a 10 s bucket, the text is `zoomCopy.tooShort` and `ok === false`.
  - Over the whole range, the text is `zoomCopy.readoutWhole`.
- `stripModel exposes the brush and the hint`:
  - On the day model: `brush.rootStart === week.start`, `brush.viewEnd - brush.viewStart === DAY`, and `brush.stepMs === 900_000` (Task 1's day interval).
  - `hint === zoomCopy.hint`.
- The existing copy-lint test over `allZoomStrings()` must still pass with the new strings.

In `tests/zoomView.test.cjs`:
- `setWindow at a lower level replaces the top and makes one read`: the stub records 1 analyze call, and `zoomView.view` equals the window.
- `expect reopens the lens on the next matching follow only`:
  - Calling `follow` with a different root leaves `view === null`.
  - Calling `expect` again and then `follow` with the matching root gives `view` equal to the lens.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd web && node --test tests/zoom.test.cjs tests/zoomView.test.cjs`
Expected: FAIL with `setTop is not a function`, or similar.

- [ ] **Step 3: Implement**

- `setTop` reuses the private `replaceTop` and the guards `push` already has.
- `brush.stepMs` is the current bucket's seconds × 1000.
- `startText` and `endText` use the existing time formatting `windowLabel` uses.
- In the store, `readout` resolves the indices against `slots` and calls `dragReadout`.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd web && node --test tests/*.test.cjs && npm run check`
Expected: all pass, and svelte-check reports 0 errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/zoom.ts web/src/lib/copy/zoomCopy.ts web/src/lib/zoomView.svelte.ts web/tests/zoom.test.cjs web/tests/zoomView.test.cjs
git commit -m "Zoom: add setTop, range readouts, the brush model and the new strip copy"
```

---

### Task 3: Brush geometry (pure)

**Files:**
- Create: `src/lib/zoomBrush.ts`. It may import types and `MIN_VIEW_MS` from `./zoom`, and nothing else.
- Create: `tests/zoomBrush.test.cjs`

**Interfaces:**
- Consumes: the `ZoomWindow` type and `MIN_VIEW_MS` from `zoom.ts`.
- Produces:
  - `type BrushMode = 'start' | 'end' | 'move' | 'new' | 'none'`
  - `brushHit(xFrac: number, sel: { left: number; right: number }, handleFrac: number): BrushMode`
    - Within `handleFrac` of an edge it returns that edge. Inside the selection it returns `move`. Elsewhere it returns `new`.
    - A selection narrower than `2 × handleFrac` favours the edges.
  - `brushDrag(mode: BrushMode, root: ZoomWindow, startView: ZoomWindow, fromMs: number, toMs: number, stepMs: number): ZoomWindow`
    - The result is always inside the root, ordered, and at least `MIN_VIEW_MS` wide.
    - Edges snap to `stepMs` measured from `root.start`.
    - `move` keeps the width.
    - `new` spans from `fromMs` to `toMs`, ordered.
    - `none` returns `startView`.
  - `brushCentre(root: ZoomWindow, view: ZoomWindow, atMs: number, stepMs: number): ZoomWindow`
    - Keeps the width, centres on `atMs`, and clamps to the root.
  - `brushKey(handle: 'start' | 'end', key: string, root: ZoomWindow, view: ZoomWindow, stepMs: number): ZoomWindow | null`
    - Arrow keys move ±1 step. PageUp and PageDown shift by the window's width. Home and End go to the root's edges.
    - Any other key returns null.
  - `brushStepMs(bucketSeconds: number): number`: seconds × 1000, at least 10 000.

- [ ] **Step 1: Write the failing tests** (root = `2001-10-22T00:00Z` + 7 days, step = 15 min)

- `brushHit picks the edge, the body or a new range`: at 0.30, 0.50 and 0.70 against `{left: .3, right: .7}` with handle 0.01, it returns `start`, `move` and `end`. At 0.9 it returns `new`.
- `brushDrag clamps, orders and keeps the 1-minute floor` (Review Focus 4):
  - Dragging `start` past `root.end` gives `end - start >= MIN_VIEW_MS`, `end <= root.end` and `start < end`.
  - Dragging `move` past `root.start` gives `start === root.start` with the width unchanged.
  - A `new` drag from right to left gives an ordered window.
  - A 20-second `new` drag gives exactly `MIN_VIEW_MS`.
- `brushDrag snaps edges to the step from the root start`: `(start - root.start) % step === 0`.
- `brushDrag with mode 'none' returns the start view` (Review Focus 5, the pure part): the result is deep-equal to `startView`.
- `brushCentre keeps the width and clamps at the ends`: centring at `root.end` gives `end === root.end`.
- `brushKey moves one edge or the whole window`:
  - ArrowRight on `end` gives +1 step.
  - PageDown on either handle shifts by the width, clamped.
  - Home on `start` gives `root.start`.
  - `'a'` gives null.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd web && node --test tests/zoomBrush.test.cjs`
Expected: FAIL, because the module is missing.

- [ ] **Step 3: Implement `src/lib/zoomBrush.ts`** as specified in the Interfaces above.

Order of operations in `brushDrag`:
1. Apply the delta.
2. Snap.
3. Order the edges.
4. Enforce the floor by growing away from the clamped side.
5. Clamp to the root.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd web && node --test tests/*.test.cjs`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/zoomBrush.ts web/tests/zoomBrush.test.cjs
git commit -m "Zoom: add pure brush geometry for the overview strip"
```

---

### Task 4: Promotion ("Investigate this window")

**Files:**
- Create: `src/lib/promote.ts`. It imports from `./zoom`, `./time` (`bucketForWindows`), `./autorun` (`intervalFits`) and `./copy/zoomCopy`.
- Create: `tests/promote.test.cjs`
- Modify: `src/lib/appState.svelte.ts`. Add these next to `setWindowFromHour` (around line 969):
  - the `windowTrail` state
  - `investigateWindow`
  - `backToPreviousWindow`
  - `results.windowBack`
  - clearing the trail in `resetInvestigation` (line 618)

**Interfaces:**
- Consumes:
  - `zoomView.expect(root, lens)` from Task 2
  - `durationLabel` and `isManualBucket` from `zoom.ts`
  - `zoomText.backTo` and `investigate*` copy from Task 2
- Produces, in `promote.ts`:
  - `interface WindowFields { start: string; end: string; baselineStart: string; baselineEnd: string; bucketSeconds: number }`. The field formats match `Query`.
  - `interface TrailEntry { from: WindowFields; to: { start: string; end: string }; lens: ZoomWindow }`
  - `TRAIL_LIMIT = 10`
  - `promotedScope(current: WindowFields, view: ZoomWindow): WindowFields | null`
  - `investigateState(input: { isRoot: boolean; runLoading: boolean; stale: boolean }): { enabled: boolean; reason: string }`
    - It checks in this order: `isRoot` gives `investigateTop`, then `runLoading` gives `investigateBusy`, then `stale` gives `investigateStale`.
    - Otherwise it returns `{ enabled: true, reason: '' }`.
  - `planInvestigation(input: { current: WindowFields; view: ZoomWindow | null; runLoading: boolean; stale: boolean; trail: TrailEntry[] }): { patch: WindowFields; trail: TrailEntry[] } | { refused: string }`
  - `pushTrail(trail, entry): TrailEntry[]`
    - New array. It drops the oldest entry past `TRAIL_LIMIT`.
  - `popTrail(trail): { entry: TrailEntry | null; trail: TrailEntry[] }`
  - `backTarget(trail: TrailEntry[], current: { start: string; end: string }): TrailEntry | null`
    - Returns the last entry only when its `to` equals `current`.
  - `backLabel(entry: TrailEntry): string`
    - Returns `zoomText.backTo(durationLabel(width of entry.from))`, for example `'Back to 7 days'`.
- Produces, in `appState`:
  - `investigateWindow(): void`. It reads `zoomView.view` and returns early on a refusal.
  - `backToPreviousWindow(): void`
  - `results.windowBack: TrailEntry | null`

- [ ] **Step 1: Write the failing tests** in `tests/promote.test.cjs` (current = `2001-10-22T00:00:00Z` to `2001-10-29T00:00:00Z`, 7200 s automatic)

- `promotedScope sets the window, the reference before it and the app interval`: for the view 14:00 to 15:00 on Oct 24:
  - `start`/`end` match that view
  - `baselineStart` is 13:00 and `baselineEnd` is 14:00
  - `bucketSeconds === bucketForWindows(...)`, which is 30
- `promotedScope rounds outward to whole seconds and stays inside the current window` (Review Focus 2):
  - A view of `{start: t + 400.6 s, end: t + 4000.2 s}` gives whole-second edges with `start <= view.start` and `end >= view.end`.
  - A view hanging 5 s past `current.end` is clipped to `current.end`.
- `promotedScope refuses a window under a minute, an unreadable one, or the current one`:
  - 30 s gives null.
  - `{start: NaN}` gives null.
  - A view equal to the current window gives null.
- `promotedScope drops a manual interval that no longer fits` (Review Focus 3):
  - A manual 3600 on a 30-minute view gives the app rule (10).
  - A manual 60 on a 1-hour view is kept (60).
  - The automatic 7200 on a 1-hour view gives 30, not 7200.
- `investigateState gives the first reason in order`:
  - The order is root, then busy, then stale.
  - Root and busy together give `investigateTop`.
  - None of them gives `enabled: true`.
- `planInvestigation refuses while a run is loading` (Review Focus 1):
  - With `runLoading: true` it returns `{ refused: zoomCopy.investigateBusy }` and the trail is unchanged.
  - Two calls in a row, where the second sees `runLoading: true`, give a trail of length 1.
- `the trail caps at 10, pops in order, and backTarget hides after a manual edit`:
  - After 12 pushes, the length is 10 and the oldest entries are gone.
  - `backTarget` with a `current` that differs from `last.to` gives null.
  - `backLabel` gives `'Back to 7 days'`.
- Source assertions on `src/lib/appState.svelte.ts`, in the house regex style:
  - `investigateWindow` calls `planInvestigation(` before `Object.assign(query`.
  - It then calls `controlsResetKey++` and `runAnalysis()`.
  - It does not call `onScopeChange()`.
  - `runAnalysis` sets `loading = true` before its first `await`.
  - `resetInvestigation` clears `windowTrail`.
  - `backToPreviousWindow` calls `zoomView.expect(`.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd web && node --test tests/promote.test.cjs`
Expected: FAIL, because the module is missing.

- [ ] **Step 3: Implement `promote.ts`, then wire up `appState`**

`promote.ts`:
- Convert the times with the same ISO helpers `hourWindowQuery` in `rollup.ts` uses. The output format matches what QueryControls writes.

`investigateWindow`:
1. Build `current` from `analysis.query`, not from the edited `query`, so the lens edges and the root agree.
2. Call `planInvestigation`.
3. On a refusal, set `zoomView`'s notice to the reason and return.
4. Otherwise: `windowTrail = plan.trail`, then `Object.assign(query, plan.patch)`, then `controlsResetKey++`, then `void runAnalysis()`.

`backToPreviousWindow`:
1. `popTrail`.
2. `Object.assign(query, entry.from)`, then `controlsResetKey++`.
3. `zoomView.expect(rootOf(entry.from), entry.lens)`.
4. `void runAnalysis()`.

`results.windowBack`: `backTarget(windowTrail, query)`.

Errors: a failed run uses the existing error banner and stale banner. "Back to …" still works afterwards, because `query` matches `to`.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd web && node --test tests/*.test.cjs && npm run check`
Expected: all pass, 0 errors. The existing appState source tests (drawer, importJob, import, clumsy, uxnav, time, pure) still pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/promote.ts web/tests/promote.test.cjs web/src/lib/appState.svelte.ts
git commit -m "Zoom: promote the zoomed window to the query window with a way back"
```

---

### Task 5: Chart drag readout and crosshair

**Files:**
- Modify: `src/lib/chartZoom.ts`
- Create: `tests/chartReadout.test.cjs`. It makes source assertions, following `tests/drillfix.test.cjs`.

**Interfaces:**
- Consumes: `zoomView.readout(fromIndex, toIndex)` from Task 2.
- Produces: no new exports. The `use:zoomable` signature does not change.

- [ ] **Step 1: Write the failing tests** (source assertions on `chartZoom.ts`)

- `the drag band carries a readout`:
  - The source creates a child element with class `readout`.
  - It sets its text from `zoomView.readout(`.
  - It styles the element only with `var(--surface)`, `var(--line)`, `var(--text)` and `var(--text-2)`, plus `font-variant-numeric: tabular-nums` and `12px`.
- `the readout updates only when the slot pair changes`: the source keeps the last index pair and compares it before writing.
- `no transition or animation`: the source has no `transition` or `animation`.
- `crosshair over the plot`: the source sets `cursor = 'crosshair'` inside the `inPlot` branch and restores it outside.
- Unchanged:
  - `DRAG_PIXELS = 6`
  - `touchAction = 'pan-y'`
  - `queueMicrotask` in the blur handler (drillfix D6 still passes)

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd web && node --test tests/chartReadout.test.cjs`
Expected: FAIL.

- [ ] **Step 3: Implement**

- The readout sits above the band and is clamped horizontally inside the container (`Math.min` and `Math.max` against `offsetWidth`).
- When `ok === false`, lower the band's opacity to .08 and use `--text-2` for the text.
- Remove the readout when the band is removed.
- The band's existing `aria-hidden` behaviour stays. Screen-reader users get the range from the strip's live region on commit.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd web && node --test tests/*.test.cjs`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/chartZoom.ts web/tests/chartReadout.test.cjs
git commit -m "Zoom: show the snapped range and duration while dragging on a chart"
```

---

### Task 6: Strip redesign, brush UI and App wiring

**Files:**
- Modify: `src/components/ZoomStrip.svelte`
- Modify: `src/App.svelte`, line 79
- Modify: `src/lib/zoomView.svelte.ts`: remove `open` and its setter
- Modify: `src/lib/copy/zoomCopy.ts`: remove `expand` and `zoomText.collapsed`
- Test: `tests/zoomStrip.test.cjs`. Keep the BOM. Extend the import loader map with `../lib/zoomBrush`.

**Interfaces:**
- Consumes:
  - `StripModel.brush` and `hint` from Task 2
  - `zoomBrush.*` from Task 3
  - `investigateState`, `backLabel`, `investigateWindow`, `backToPreviousWindow` and `results.windowBack` from Task 4
- Produces: new `ZoomStrip` props, which replace `open`:
  - `investigate: { enabled: boolean; reason: string }`
  - `backLabel: string | null`
  - `onInvestigate: () => void`
  - `onWindowBack: () => void`
  - `onBrush: (win: ZoomWindow) => void`. App passes `win => zoomView.setWindow(win)`.

- [ ] **Step 1: Write the failing tests** in `tests/zoomStrip.test.cjs`

- Replace `starts as one line "Zoom: 7 days"` and `closed until used` with `the strip is always open and shows the hint`:
  - The HTML has no `<details>` or `<summary>`.
  - It contains `zoomCopy.hint`.
  - It contains the six button labels.
- `the brush is two sliders in a labelled group`:
  - There are two `role="slider"` elements.
  - They carry `aria-label` set to `zoomCopy.brushStart` and `zoomCopy.brushEnd`.
  - `aria-valuemin` is `brush.rootStart` and `aria-valuemax` is `brush.rootEnd`.
  - `aria-valuenow` is the view's edges.
  - `aria-valuetext` is `startText` and `endText`.
  - The group has `role="group"` with `aria-label` set to `overview.alt`.
- `the brush still draws with no data`: on the empty model, the sliders are present and `zoomCopy.overviewEmpty` shows.
- `Investigate is a secondary button with a spoken reason when unavailable`:
  - With `{enabled: false, reason: zoomCopy.investigateBusy}`, the button has `aria-disabled="true"` and no `disabled` attribute.
  - Its `aria-describedby` points to an element holding the reason.
  - The class is `secondary-button`, never `primary-button`.
- `Back to … shows only when given`: `backLabel: 'Back to 7 days'` renders the button, and `null` renders none.
- `the strip reverts the preview on pointercancel and Escape` (Review Focus 5): source assertions.
  - A `pointercancel` handler and an `Escape` branch both reset the preview and do not call `onBrush`.
  - The Escape branch calls `preventDefault` and `stopPropagation`.
- `the brush commits once per gesture`: `onBrush` appears only in the pointerup handler, in the debounced key commit (300 ms) and in the blur handler.
- `coarse pointers get wider handles`: the style has `@media (pointer: coarse)`, and the handle hit area is at least 24px.
- `no transition`: the style has no `transition`. The `summary::after` rotation is deleted along with `<details>`.
- Update the imports-only test to allow `['../lib/copy/zoomCopy', '../lib/zoom', '../lib/zoomBrush']`.
- Add a source assertion on `App.svelte`:
  - `ZoomStrip` receives `onInvestigate={investigateWindow}` and `onWindowBack={backToPreviousWindow}`.
  - It does not receive `bind:open`.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd web && node --test tests/zoomStrip.test.cjs`
Expected: FAIL.

- [ ] **Step 3: Implement**

Rows of the strip, in order:
- Row 1:
  - "Back to …", a quiet button
  - the crumbs
  - the six quiet buttons
  - "Investigate this window", a `secondary-button` with `title={zoomCopy.investigateTitle}`. Pressing it while `aria-disabled` puts the reason in the live region and does nothing else.
- Row 2: the brush.
  - The existing bars svg is kept as the track, decorative under the group.
  - On top sit the selection body and two handle elements, positioned by percentage from `brush`.
  - A local `$state` preview window is used while dragging. The zoom-line shows `rangeReadout` of the preview.
  - Pointer capture is set on pointerdown.
  - A click without movement (under 4px) calls `brushCentre`.
- Row 3: `levelName`, `rangeLine`, `intervalLine`, and `hint` in `--text-2`.
- Then the existing message, notice, as-of, scope, reference and howTo notes, and the `sr-only` live region.

Styling:
- The selection uses `var(--accent)` at low opacity, with a hairline `var(--accent)` border.
- The handles are hairline bars.
- No solid fill.

In App, compute:
- `investigate = investigateState({ isRoot: zoomModel.isRoot, runLoading: loading, stale: results.displayedRunIsStale })`
- `backLabel = results.windowBack ? backLabel(results.windowBack) : null`

Remove `open` from `zoomView`, and `expand` and `zoomText.collapsed` from the copy. Leave `zoomModel.summary` in place only if another test or the sr-only line reads it; otherwise remove it and its test lines.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `cd web && node --test tests/*.test.cjs && npm run check`
Expected: all pass, svelte-check reports 0 errors, and nothing references `zoomView.open`, `zoomCopy.expand` or `zoomText.collapsed`. Confirm the last point with `rg "zoomView.open|zoomCopy.expand|collapsed\(" web/src web/tests`.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/ZoomStrip.svelte web/src/App.svelte web/src/lib/zoomView.svelte.ts web/src/lib/copy/zoomCopy.ts web/tests/zoomStrip.test.cjs
git commit -m "Zoom: always-visible strip with an overview brush and Investigate this window"
```

---

### Task 7: Clumsy-user review and full verification

This follows the user's standing rule: after each phase, one review that tries to break the app, pinned with tests.

**Files:**
- Create: `tests/drilldown.clumsy.test.cjs`

**Interfaces:**
- Consumes: everything above. Pure modules only, plus source assertions.

- [ ] **Step 1: Write the tests**

- `drill from 7 days to one minute in five drags never passes 120 bars or goes under the floor`:
  - Repeatedly call `zoomToRange` on a fifth of the view.
  - At each level, check `bucketForView` and the slot count, and that the width is at least `MIN_VIEW_MS`.
- `brush keys hammered at the edges stay inside the root`:
  - Apply 200 random keys from the set {Arrow*, Page*, Home, End}, using a fixed seed.
  - Every result is a valid window inside the root and at least 1 minute wide.
- `investigate at the root, while loading, while stale, or on a 30-second window is refused with a reason`: run the cases through `planInvestigation` and `promotedScope`.
- `promote then back then promote again keeps the trail consistent`: use pure trail operations, and check that `backTarget` matches each step.
- `a promoted window keeps every edge whole-second ISO and passes scopeProblem`: assert `scopeProblem(patchAsQuery, true) === ''`.
- `every new string passes the copy lint`: run all of `allZoomStrings()` through `copylint`.

- [ ] **Step 2: Run the full suite and type check**

Run: `cd web && node --test tests/*.test.cjs` and `npm run check`
Expected: all pass (778 plus the new tests), and svelte-check reports 0 errors.

- [ ] **Step 3: Manual browser pass** (not committed)

Use `npm run dev` and synthetic data. Check:
- Drag on a chart and see the readout.
- Drag again inside it.
- Use the brush with mouse and keyboard.
- Use touch emulation at 375px: a vertical swipe scrolls, and a horizontal drag zooms.
- Use Investigate, then "Back to 7 days".
- Use reduced motion.
- Check light and dark themes.

Record any defect as a new test before fixing it.

- [ ] **Step 4: Commit**

```bash
git add web/tests/drilldown.clumsy.test.cjs
git commit -m "Zoom: clumsy-user tests for the drill-down and promotion"
```

---

## Risks

- **Quick-range chip lights for promoted windows.** `depth.ts` `activeRangeId` matches on span length only. A promoted 1-hour window lights "Last hour" even when it is not the last hour. `setWindowFromHour` has the same issue today. Fixing it is out of scope; it is listed as an open question.
- **Stall markers after promotion.** Resource panel stall spans come from the Patterns report for the query window. They stay empty until Patterns runs for the promoted window.
- **Finer bars cost more reads.** A day view at 15-minute bars draws 96 bars instead of 24. The backend already serves 120 points, and the zoom cache (24 entries) still applies.
- **`dateInputsValid` after promotion.** `controlsResetKey++` re-keys QueryControls. Confirm in the Task 7 browser pass that the date inputs read the new values and that `scopeProblem` does not report stale input state.
- **Existing tests coupled to the old intervals or `<details>`.** Tasks 1 and 6 own those updates. Any other failing assertion must be updated with a note in the commit body, never deleted silently.

## Open questions

1. Should a promoted window keep the user's manual interval when it fits, or always use the app rule? The plan keeps it when it is manual, fits, and is smaller than the window.
2. Should the quick-range chip ignore windows set by promotion or by the hour grid? This plan does not fix it.
3. Should promotion add a browser-history step, so the browser's Back button acts as "Back to …"? The plan uses in-memory only.
4. Is it acceptable that stall markers stay empty after promotion until Patterns is rerun? The alternative is to auto-run Patterns on promotion.
