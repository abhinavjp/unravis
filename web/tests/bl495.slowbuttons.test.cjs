const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// BL-495 part n: the "Inspect window" and "Nearby activity" buttons of the Slowest tab (TrafficSlowness). Quiet buttons of one class with the other row
// buttons (.row-action), real buttons (type=button), each named by what it acts on with the visible words first (WCAG 2.5.3 label in name).

const actions = loadTypeScript('src/lib/requestActions.ts');
const read = (file) => fs.readFileSync(path.resolve(__dirname, '..', file), 'utf8');
const WINDOW = 5 * 60 * 1000;
const t0 = Date.parse('2026-10-20T08:00:00.000Z');
const win = (over = {}) => ({ start: t0, end: t0 + WINDOW, partial: false, ...over });
const request = (over = {}) => ({ id: 'r-124', controller: 'Payroll', action: 'Run', initiatedAt: '2026-10-20T08:59:52.123Z', ...over });

test('the Inspect window name starts with the visible words, then the window in the zone of the table with the zone named once', () => {
  assert.equal(actions.windowInspectName(win(), 'UTC'), 'Inspect window for 20 Oct 08:00:00 to 08:05:00 UTC');
  assert.match(actions.windowInspectName(win(), 'Asia/Dubai'), /^Inspect window for 20 Oct 12:00:00 to 12:05:00 (GST|GMT\+4)$/); // the zone's short name depends on the ICU data
  assert.ok(actions.windowInspectName(win(), 'UTC').startsWith(actions.REQUEST_ACTION_TEXT.inspectWindow));
  assert.equal(actions.REQUEST_ACTION_TEXT.inspectWindow, 'Inspect window');
});

test('two windows that differ only by their start (or only by their end) have different names', () => {
  const a = win(), b = win({ start: t0 + WINDOW, end: t0 + 2 * WINDOW });
  assert.notEqual(actions.windowInspectName(a, 'UTC'), actions.windowInspectName(b, 'UTC'));
  const short = win({ end: t0 + WINDOW - 1000 });
  assert.notEqual(actions.windowInspectName(a, 'UTC'), actions.windowInspectName(short, 'UTC'));
});

test('a window that crosses midnight names both days, and a partial window says so, as its row does', () => {
  const night = win({ start: Date.parse('2026-10-20T23:55:00Z'), end: Date.parse('2026-10-21T00:00:00Z') + WINDOW });
  assert.match(actions.windowInspectName(night, 'UTC'), /^Inspect window for 20 Oct 23:55:00 to 21 Oct 00:05:00 UTC$/);
  assert.equal(actions.windowInspectName(win({ partial: true }), 'UTC'), 'Inspect window for 20 Oct 08:00:00 to 08:05:00 UTC (partial)');
  assert.notEqual(actions.windowInspectName(win({ partial: true }), 'UTC'), actions.windowInspectName(win(), 'UTC'));
});

test('the hour a clock change repeats: the two windows with the same clock time read differently through the zone name', () => {
  // London goes back on 25 Oct 2026 at 02:00 BST (01:00 UTC): 00:30 UTC and 01:30 UTC both read 01:30 on the clock
  const first = win({ start: Date.parse('2026-10-25T00:30:00Z'), end: Date.parse('2026-10-25T00:35:00Z') });
  const second = win({ start: Date.parse('2026-10-25T01:30:00Z'), end: Date.parse('2026-10-25T01:35:00Z') });
  const a = actions.windowInspectName(first, 'Europe/London'), b = actions.windowInspectName(second, 'Europe/London');
  assert.match(a, /^Inspect window for 25 Oct 01:30:00 to 01:35:00 (BST|GMT\+1)$/);
  assert.match(b, /^Inspect window for 25 Oct 01:30:00 to 01:35:00 (GMT|GMT\+0|UTC)$/);
  assert.notEqual(a, b);
});

test('the Nearby activity button of the slowest list is named like the one of the request list: visible words first, API, time, source, record id', () => {
  assert.equal(actions.nearbyActivityName(request(), 'UTC', 'Demo West / demo.audit'), 'Nearby activity for Payroll.Run at 08:59:52.123, Demo West / demo.audit, record r-124');
});

test('two slow requests that differ in one field only (id, source, start time or API) have different Nearby activity names', () => {
  const base = actions.nearbyActivityName(request(), 'UTC', 'Demo West');
  for (const [label, name] of [
    ['id', actions.nearbyActivityName(request({ id: 'r-125' }), 'UTC', 'Demo West')],
    ['source', actions.nearbyActivityName(request(), 'UTC', 'Demo East')],
    ['start', actions.nearbyActivityName(request({ initiatedAt: '2026-10-20T08:59:52.124Z' }), 'UTC', 'Demo West')],
    ['API', actions.nearbyActivityName(request({ action: 'Preview' }), 'UTC', 'Demo West')]
  ]) {
    assert.notEqual(name, base, 'differing only by ' + label);
    assert.ok(name.startsWith('Nearby activity'), 'visible words first when differing by ' + label);
  }
});

test('the two buttons of the Slowest tab are type=button quiet row-action buttons named by their row, and the text on them comes from the same words', () => {
  const src = read('src/components/TrafficSlowness.svelte');
  const buttons = [...src.matchAll(/<button type="button" class="quiet-button row-action"[^>]*>[^<]*<\/button>/g)].map((m) => m[0]);
  assert.equal(buttons.length, 2, 'Inspect window and Nearby activity');
  const [inspect, nearby] = buttons;
  assert.match(inspect, /aria-label=\{windowInspectName\(row,zone\)\}/);
  assert.match(inspect, />\{REQUEST_ACTION_TEXT\.inspectWindow\}<\/button>$/);
  assert.match(nearby, /aria-label=\{nearbyActivityName\(row,zone,sourceLabels\.get\(row\.sourceId\)\?\?row\.sourceId\)\}/);
  assert.match(nearby, />\{REQUEST_ACTION_TEXT\.nearby\}<\/button>$/);
  assert.doesNotMatch(src, /<button class="quiet-button"[^>]*>(Inspect window|Nearby activity)</, 'no bare quiet button of these two is left');
  assert.match(src, /import \{ nearbyActivityName, REQUEST_ACTION_TEXT, windowInspectName \} from '\.\.\/lib\/requestActions';/);
  // the shared class is the one of PR 192: a visible edge, no wrapping, and the 24 px press area
  assert.match(read('src/app.css'), /\.row-action \{[^}]*border-color: var\(--control-line\);[^}]*white-space: nowrap;/);
});
