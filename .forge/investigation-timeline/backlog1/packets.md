# Backlog 1: Import coverage gaps, packets

## Answer first

- **A (chip mark): client only.** The server already sends `noFile: true` on
  `resp.Sources` (`tracingRef.NoFile`, set by `markNoFile`). The client drops it in
  `parseSourceRef`. Packets: parse it (P1), carry it on the chip (P2), and render
  " · not imported" plus a title sentence (P3).
- **B (cover sentence): needs Go.** `calltrace.BuildLanes` already returns
  `out-of-range` when every file's span misses the window. The span itself is never
  sent. Add **`tracingLane.ImportedFrom` / `tracingLane.ImportedTo`**
  (`*time.Time`, `json:"importedFrom,omitempty"` / `json:"importedTo,omitempty"`)
  in `internal/server/tracing.go`. Fill them from `rec.Quality.CoverageFirst/Last`
  through a new `laneSources.span` map (P4). The client parses them (P1), builds
  the sentence (P5) and shows it in `lane-status` in place of bars (P6). e2e: P7.
- All kept test ids stay. New e2e ids: **TSRC-11**, **TSRC-12**. New unit test
  files, one per packet, so that no two packets touch the same file.

## Rulings (controller, final; do not reopen)
1. Keep the events/scanned-0 guard in P5. 2. Unknown span: option B, send nothing. 3. Year: do not add; keep `at` as is. 4. "not imported" chip stays toggleable. 5. Held SQL lane: out of scope.
Implementer rules: edit ONLY the files your packet lists; if an anchor does not match, find the nearest equivalent and say so in your report; if truly stuck, ask the advisor (SendMessage to agent `ADVISOR_ID`) one short question. Do not commit. Do not read `.env` or `sql/`. Report: status, files changed, test command output tail (5 lines), concerns. Max 10 lines.

## Dependency graph

```
P1 model.ts ──┬──> P2 sources.ts ──> P3 TraceSources.svelte ──┐
              └──> P5 lanes.ts ────> P6 TracingChart.svelte ──┼──> P7 e2e (TSRC-11/12)
P4 tracing.go (GO, independent) ──────────────────────────────┘
```
Waves: {P1, P4} -> {P2, P5} -> {P3, P6} -> P7.

Unit test helper: each new `.cjs` file copies the `loadTypeScript` /
`require` preamble from the top of `web/tests/timelineSources.test.cjs` verbatim.
Web commands are run from `web/`: `npm test` and `npm run check`.

---

## P1 (haiku): parse noFile and the imported span
Files: `web/src/lib/tracing/model.ts`, new `web/tests/backlog1.model.test.cjs`

Anchor 1:
```ts
export interface TracingSourceRef { sourceId: string; label: string }
```
Replace with:
```ts
export interface TracingSourceRef { sourceId: string; label: string; noFile?: true }
```
Anchor 2 (in `parseSourceRef`):
```ts
  if (!isObject(value) || !isText(value.sourceId) || !isText(value.label)) return null;
  return { sourceId: value.sourceId, label: value.label };
```
Replace the return line with:
```ts
  const ref: TracingSourceRef = { sourceId: value.sourceId, label: value.label };
  if (value.noFile === true) ref.noFile = true;
  return ref;
```
Add two optional fields to `interface TracingLane`, next to `savedNotConnected?`:
```ts
  importedFrom?: string;
  importedTo?: string;
```
Anchor 3 (end of `parseLane`):
```ts
  const saved = parseNames(value.savedNotConnected);
  if (saved.length > 0) lane.savedNotConnected = saved;
  return lane;
```
Insert before `return lane;`:
```ts
  if (isText(value.importedFrom) && isText(value.importedTo)
    && isParseableDate(value.importedFrom) && isParseableDate(value.importedTo)) {
    lane.importedFrom = value.importedFrom;
    lane.importedTo = value.importedTo;
  }
```
Test (`backlog1.model.test.cjs`). Build a valid response the same way the
existing parse tests in `timelineSources.test.cjs` do, then:
```js
test('a source with no import keeps noFile; others stay as before', () => {
  const r = parse(withSources([{ sourceId: 'a1', label: 'Orders' }, { sourceId: 'b2', label: 'u_ex.log', noFile: true }]));
  assert.deepEqual(r.sources, [{ sourceId: 'a1', label: 'Orders' }, { sourceId: 'b2', label: 'u_ex.log', noFile: true }]);
});
test('a lane keeps the imported span only when both ends are dates', () => {
  const ok = parse(withLane({ importedFrom: '2001-01-01T00:00:00Z', importedTo: '2001-01-01T01:00:00Z' }));
  assert.equal(laneOf(ok).importedFrom, '2001-01-01T00:00:00Z');
  const bad = parse(withLane({ importedFrom: 'soon', importedTo: '2001-01-01T01:00:00Z' }));
  assert.equal(laneOf(bad).importedFrom, undefined);
});
```
(`withSources` / `withLane` / `laneOf` are local helpers over the fixture that the
existing parse tests use.) Commands: `npm test`, `npm run check`.

## P2 (haiku): the chip carries noFile, plus copy
Files: `web/src/lib/tracing/sources.ts`, new `web/tests/backlog1.sources.test.cjs`. Depends on P1.

- Add `noFile?: true` to `interface SourceChip {id,label,kind}`.
- Anchor (in `chipsOf`):
```ts
    out.push({ id: ref.sourceId, label: ref.label || ref.sourceId, kind });
```
  Replace with:
```ts
    out.push({ id: ref.sourceId, label: ref.label || ref.sourceId, kind, ...(ref.noFile ? { noFile: true as const } : {}) });
```
- Anchor (in `loadSources`):
```ts
    chips.push({ id: chip.id, label: chip.label, kind: chip.kind as SourceKind });
```
  Replace with:
```ts
    chips.push({ id: chip.id, label: chip.label, kind: chip.kind as SourceKind, ...(chip.noFile === true ? { noFile: true as const } : {}) });
```
- Anchor (end of `SOURCES_TEXT`):
```ts
  newNotOn: 'The new file’s source was not switched on. 50 sources are already on.'
} as const;
```
  Replace with:
```ts
  newNotOn: 'The new file’s source was not switched on. 50 sources are already on.',
  notImported: 'not imported',
  notImportedTitle: 'This file is listed but was never imported, so the timeline holds nothing from it. Import it to see its events.'
} as const;
```
  (`sourcesCopyEntries` lints both strings automatically. Neither contains a
  banned word.)

Test:
```js
test('chipsOf marks a source that was never imported', () => {
  const chips = chipsOf([{ sourceId: 'x', label: 'u_ex.log', noFile: true }, { sourceId: 'y', label: 'a.log' }]);
  assert.equal(chips.find(c => c.id === 'x').noFile, true);
  assert.equal('noFile' in chips.find(c => c.id === 'y'), false);
});
test('the not-imported copy passes the copy lint', () => {
  for (const e of sourcesCopyEntries()) assert.deepEqual(lintCopy(e.text), [], e.key);
});
```
Match the `chipsOf` call to its real arity. If it takes extra arguments, copy them
from the existing chipsOf test. Commands: `npm test`, `npm run check`.

## P3 (haiku): render the chip mark
Files: `web/src/components/TraceSources.svelte`, new `web/tests/backlog1.chip.test.cjs`. Depends on P2.

Anchor (line 36):
```svelte
<button type="button" class="chip" data-source-id={chip.id} aria-pressed={isChipOn(selection, chip.id)} {disabled} onclick={() => onToggle(chip.id)}>{chipText(chip)}</button>
```
Replace with:
```svelte
<button type="button" class="chip" data-source-id={chip.id} aria-pressed={isChipOn(selection, chip.id)} title={chip.noFile ? SOURCES_TEXT.notImportedTitle : undefined} {disabled} onclick={() => onToggle(chip.id)}>{chipText(chip)}{#if chip.noFile}<span class="ts-chip-flag" data-testid="chip-not-imported"> · {SOURCES_TEXT.notImported}</span>{/if}</button>
```
If `SOURCES_TEXT` is not imported yet, add it to the existing import from
`../lib/tracing/sources`. CSS, next to `.ts-saved-state`:
```css
  .ts-chip-flag { font-style: italic; }
```
The chip stays toggleable (see open question 4).

Test (a source-text test, the same style as the existing chip regex tests):
```js
const src = fs.readFileSync(path.join(__dirname, '../src/components/TraceSources.svelte'), 'utf8');
test('chip keeps its toggle and text, and shows a text mark with a title', () => {
  assert.match(src, /aria-pressed=\{isChipOn\(selection, chip\.id\)\}/);
  assert.match(src, /\{chipText\(chip\)\}/);
  assert.match(src, /title=\{chip\.noFile \? SOURCES_TEXT\.notImportedTitle : undefined\}/);
  assert.match(src, /\{#if chip\.noFile\}<span class="ts-chip-flag" data-testid="chip-not-imported">/);
});
```
Commands: `npm test`, `npm run check`.

## P4 (sonnet) **GO**: send the imported span on a lane
Files: `internal/server/tracing.go`, new `internal/server/tracing_importedspan_test.go`

1. Anchor (end of `tracingLane`):
```go
	SourcesRead       []tracingRef `json:"sourcesRead,omitempty"`
	SavedNotConnected []string     `json:"savedNotConnected,omitempty"`
}
```
   Insert before the `}`:
```go
	ImportedFrom      *time.Time   `json:"importedFrom,omitempty"` // earliest time in the lane's imported files
	ImportedTo        *time.Time   `json:"importedTo,omitempty"`   // latest time in the lane's imported files
```
2. New type, placed near `laneSources`:
```go
// importedSpan is the earliest and latest time held by a lane's imported files.
// known turns false as soon as one file has no recorded span, so a partial range
// is never shown as the whole.
type importedSpan struct {
	from, to time.Time
	known    bool
	seen     bool
}

func (s *importedSpan) add(first, last *time.Time) {
	if first == nil || last == nil {
		s.known, s.seen = false, true
		return
	}
	f, l := first.UTC(), last.UTC()
	if !s.seen {
		s.from, s.to, s.known, s.seen = f, l, true, true
		return
	}
	if !s.known {
		return
	}
	if f.Before(s.from) {
		s.from = f
	}
	if l.After(s.to) {
		s.to = l
	}
}
```
3. Anchor (in `laneSources`):
```go
	skipped     map[string]int // lane key -> selected imports that were quarantined or failed
```
   Add after it:
```go
	span        map[string]*importedSpan // lane key -> time range of its imported files
```
4. In `resolveLaneSources`, anchor:
```go
	out.skipped = map[string]int{}
```
   Add after it: `out.span = map[string]*importedSpan{}`. Then anchor (in the loop):
```go
		out.infos = append(out.infos, info)
		out.selectedIDs[rec.ID] = true
```
   Add after it:
```go
		sp := out.span[lane]
		if sp == nil {
			sp = &importedSpan{}
			out.span[lane] = sp
		}
		sp.add(rec.Quality.CoverageFirst, rec.Quality.CoverageLast)
```
   (`lane` is the `calltrace.LaneOf(rec.Importer)` value already in the loop. If
   its type is not `string`, key the map with `string(lane)`.)
5. In `tracingSQLIISLanes`, anchor:
```go
		if lane.State == calltrace.StateNoData {
			lane.ReasonCode, lane.Reason = emptyLaneReason(key, src)
		}
```
   Insert before it:
```go
		if sp := src.span[key]; sp != nil && sp.known {
			from, to := sp.from, sp.to
			lane.ImportedFrom, lane.ImportedTo = &from, &to
		}
```
   (Use the local names that function already uses for the lane key and the
   `laneSources` value.)

Test (`tracing_importedspan_test.go`). Copy `seedTracingImport` from
`tracing_test.go` into a local helper `seedSpannedImport(t, store, caseID, importer,
file, evs, first, last *time.Time)`. It must be identical except that the run's
report is `importer.Report{Importer: importerName, Quality: evidence.SourceQuality{CoverageFirst: first, CoverageLast: last}}`.
```go
// The owner imported an IIS day that ends before the incident. The lane must say
// which times the files hold, so the empty lane is not read as "nothing happened".
func TestTracingLaneSaysWhatImportedFilesCover(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		first := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		last := time.Date(2001, 1, 1, 1, 0, 0, 0, time.UTC)
		evs := tracingEventsAt(first.Add(time.Minute), 2) // events inside the span, outside the window
		imp := seedSpannedImport(t, e.store, id, "iis-w3c", "u_ex010101.log", evs, &first, &last)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "u_ex010101.log", ImportID: imp.ID})

		res := e.json(http.MethodPost, tracingPath(id), tracingWindowBody)
		var out tracingResponse
		decodeBody(t, res, &out)
		l := tracingLaneOf(t, out, "iis")
		if l.State != calltrace.StateOutOfRange {
			t.Fatalf("iis state = %q, want out-of-range", l.State)
		}
		if l.ImportedFrom == nil || !l.ImportedFrom.Equal(first) || l.ImportedTo == nil || !l.ImportedTo.Equal(last) {
			t.Errorf("imported span = %v..%v, want %v..%v", l.ImportedFrom, l.ImportedTo, first, last)
		}
	})
}

// A file with no recorded span sends no range at all.
func TestTracingLaneSendsNoSpanWhenAFileHasNone(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		imp := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", tracingEvents(2))
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		var out tracingResponse
		decodeBody(t, res, &out)
		if l := tracingLaneOf(t, out, "iis"); l.ImportedFrom != nil || l.ImportedTo != nil {
			t.Errorf("span sent without a recorded range: %v..%v", l.ImportedFrom, l.ImportedTo)
		}
	})
}
```
`tracingEventsAt(start, n)` is a local copy of `tracingEvents(n)` with the start
time as a parameter. If `tracingWindowBody` is a func, call it. Also add a small
unit test of `importedSpan.add`: two spans widen, and a nil end makes known false.
Command (from the repo root, bash):
```
export PATH="$PWD/.dev/toolchains/go/bin:$PATH"
go test ./internal/server/ -run 'TestTracingLane(SaysWhatImportedFilesCover|SendsNoSpanWhenAFileHasNone)|TestImportedSpan' && go test ./internal/server/
```
The second run is the full package. The contract test in
`tracing_audit_test.go:384` only checks substrings, and `omitempty` leaves its
output unchanged.

## P5 (sonnet): the cover sentence
Files: `web/src/lib/tracing/lanes.ts`, new `web/tests/backlog1.lanes.test.cjs`. Depends on P1.

Add, next to `READ_TEXT`:
```ts
export const COVER_TEXT = {
  outside: (from: string, to: string): string => `Imported files cover ${from} to ${to}; nothing falls inside this window.`
} as const;

/** The sentence for a lane whose imported files all sit outside the window, or '' when it does not apply. */
export function laneImportedLine(lane: TracingLane, window: TracingWindow, at: (v: string) => string): string {
  if (lane.disabled || !lane.importedFrom || !lane.importedTo) return '';
  const from = Date.parse(lane.importedFrom);
  const to = Date.parse(lane.importedTo);
  const start = Date.parse(window.start);
  const end = Date.parse(window.end);
  const outside = to < start || from >= end;
  const emptyEvents = lane.state === 'events' && (lane.scanned ?? 0) === 0
    && !(lane.buckets ?? []).some((b) => (b.count ?? 0) > 0);
  if (lane.state !== 'out-of-range' && !(emptyEvents && outside)) return '';
  return COVER_TEXT.outside(at(lane.importedFrom), at(lane.importedTo));
}
```
Check the real field names (`disabled`, `scanned`, `buckets[].count`,
`window.start/end`) against `TracingLane` / `TracingWindow` in `model.ts`, and
use those names. In `laneCopyEntries` (line 141), add a sample:
```ts
    { key: 'cover.outside', text: COVER_TEXT.outside('1 Jan 00:00', '1 Jan 01:00') },
```
(Match the entry shape that the existing sentence samples use.)

Test:
```js
const at = (v) => v.slice(11, 16);
const win = { start: '2001-02-03T04:00:00Z', end: '2001-02-03T05:00:00Z' };
const span = { importedFrom: '2001-01-01T00:00:00Z', importedTo: '2001-01-01T01:00:00Z' };
test('out-of-range lane with a span says what the files cover', () => {
  assert.equal(laneImportedLine({ key: 'iis', state: 'out-of-range', ...span }, win, at),
    'Imported files cover 00:00 to 01:00; nothing falls inside this window.');
});
test('an events lane that scanned 0 and sits outside gets the same sentence', () => {
  assert.match(laneImportedLine({ key: 'iis', state: 'events', scanned: 0, buckets: [], ...span }, win, at), /nothing falls inside this window/);
});
test('no span, a disabled lane, or a lane with events inside gets no sentence', () => {
  assert.equal(laneImportedLine({ key: 'iis', state: 'out-of-range' }, win, at), '');
  assert.equal(laneImportedLine({ key: 'iis', state: 'out-of-range', disabled: 'x', ...span }, win, at), '');
  assert.equal(laneImportedLine({ key: 'iis', state: 'events', scanned: 5, ...span }, win, at), '');
});
test('the cover sentence passes the copy lint', () => {
  for (const e of laneCopyEntries()) assert.deepEqual(lintCopy(e.text), [], e.key);
});
```
If `laneCopyEntries` takes arguments, pass them the same way the existing lint
test does. Commands: `npm test`, `npm run check`.

## P6 (haiku): wire the sentence into the chart
Files: `web/src/components/TracingChart.svelte`, new `web/tests/backlog1.chart.test.cjs`. Depends on P5.

- Add `laneImportedLine` to the import from lanes on line 9 (it sits next to
  `laneStatusLineFor`).
- Anchor (lines 77-80):
```ts
  const statusLine = (lane: TracingLane): string => laneStatusLineFor(lane, unselected, SOURCES_TEXT.laneUnselected);
  function drawsBars(lane: TracingLane): boolean {
    return !!grid && laneDrawsBars(lane, laneBars.get(lane.key)?.bars.length ?? 0);
  }
```
  Replace with:
```ts
  const importedLine = (lane: TracingLane): string => laneImportedLine(lane, response.window, at);
  const statusLine = (lane: TracingLane): string => importedLine(lane) || laneStatusLineFor(lane, unselected, SOURCES_TEXT.laneUnselected);
  function drawsBars(lane: TracingLane): boolean {
    return !!grid && !importedLine(lane) && laneDrawsBars(lane, laneBars.get(lane.key)?.bars.length ?? 0);
  }
```
  `at` (line 51) takes `string | number`, so it fits. `lane-status` (line 616)
  and `data-state` (line 549) stay as they are.

Test (source-text):
```js
const src = fs.readFileSync(path.join(__dirname, '../src/components/TracingChart.svelte'), 'utf8');
test('the cover sentence comes before the status line and stops the bars', () => {
  assert.match(src, /importedLine\(lane\) \|\| laneStatusLineFor\(lane, unselected, SOURCES_TEXT\.laneUnselected\)/);
  assert.match(src, /!!grid && !importedLine\(lane\) && laneDrawsBars\(/);
  assert.match(src, /data-testid="lane-status"/);
});
```
Commands: `npm test`, `npm run check`.

## P7 (sonnet) **E2E**: TSRC-11 and TSRC-12
Files: `tools/e2e/specs/timeline-sources.spec.ts` only. Depends on P1-P6 (and P4
for the real server shape).

Add both ids to the header comment list. Then add:
```ts
test('TSRC-11 a source that was never imported says so on its chip', async ({ page }) => {
  await answerWith(page, (body) => {
    body.sources[0].noFile = true;
  });
  // open the timeline exactly as TSRC-01 does
  const chip = page.getByTestId('trace-sources').locator(`[data-source-id="${'<id of sources[0]>'}"]`);
  await expect(chip.getByTestId('chip-not-imported')).toHaveText(/not imported/);
  await expect(chip).toHaveAttribute('title', /never imported/);
});

test('TSRC-12 a lane whose files sit outside the window says what they cover', async ({ page }) => {
  await answerWith(page, (body) => {
    const iis = body.lanes.find((l) => l.key === 'iis');
    Object.assign(iis, { state: 'out-of-range', scanned: 0, buckets: [], events: [],
      importedFrom: '2001-01-01T00:00:00Z', importedTo: '2001-01-01T01:00:00Z' });
  });
  // open the timeline exactly as TSRC-01 does
  const panel = page.locator('.panel[data-lane="iis"]');
  await expect(panel).toHaveAttribute('data-state', 'out-of-range');
  await expect(panel.getByTestId('lane-status')).toContainText('nothing falls inside this window');
  await expect(panel.getByTestId('lane-status')).toContainText('Imported files cover');
});
```
Read the chip id from the fixture in the same way TSRC-01 does. Do not hard-code
it. Command: run the repo's e2e runner for this spec only, as the other TSRC
tests are run (e.g. `npx playwright test tools/e2e/specs/timeline-sources.spec.ts -g "TSRC-1[12]"`),
then the whole spec once.

---

## Open questions (real ones)

1. **The "events, scanned 0" case does not happen today.** `BuildLanes` already
   returns `out-of-range` when every file's span misses the window. P5 still
   guards the events case, as the brief asked. Keep that guard, or drop it to
   keep the code small?
2. **Unknown span hides the sentence.** If one file in the lane has no recorded
   span, P4 sends no range, and BuildLanes treats the lane as `none-seen` ("Files
   cover this time and nothing was seen"), which may be untrue. Option A: send
   the range of the files that do have one and word it "Some imported files
   cover...". Option B: leave it as it is. Owner's choice.
3. **Year in X and Y.** `at` uses the window's year flag. A span in another year
   (e.g. a 2025 file in a 2026 window) would show no year. Show the year whenever
   the span's year differs from the window's?
4. **Toggle on a "not imported" chip.** P3 keeps it toggleable (switching it on
   selects nothing). Disable it instead?
5. **Held SQL lane.** `tracingHeldSQLLane` / `layerSQLLane` build a fresh lane, so
   the span from imported SQL files is lost when the held day replaces a NoData
   lane. Should it be copied across? It only matters for the layered case.

No blockers.
