const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// BL-495: the "Nearby activity" and "Inspect" buttons of the All operations request list, and "Show only" of the Request volume table. They are real
// quiet buttons of one size, and each names the request it belongs to, with the visible words first (WCAG 2.5.3 label in name).

const actions = loadTypeScript('src/lib/requestActions.ts');
const read = (file) => fs.readFileSync(path.resolve(__dirname, '..', file), 'utf8');
const at = '2026-10-20T08:59:52.123Z';
const row = (over = {}) => ({ id: 'r-124', controller: 'Payroll', action: 'Run', initiatedAt: at, ...over });

test('the Nearby activity name starts with the visible words, then the API and the start time to the millisecond', () => {
  assert.equal(actions.nearbyActivityName(row(), 'UTC'), 'Nearby activity for Payroll.Run at 08:59:52.123, record r-124');
});

test('the Inspect name starts with the visible word; while loading it starts with the visible "Loading…"', () => {
  assert.equal(actions.inspectName(row(), 'UTC'), 'Inspect record for Payroll.Run at 08:59:52.123, record r-124');
  assert.equal(actions.inspectName(row(), 'UTC', true), 'Loading… record for Payroll.Run at 08:59:52.123, record r-124');
  for (const visible of ['Inspect', 'Loading…']) assert.ok(actions.inspectName(row(), 'UTC', visible === 'Loading…').startsWith(visible));
});

test('the time is read in the zone of the table, with no date and no zone name', () => {
  assert.equal(actions.nearbyActivityName(row(), 'Asia/Dubai'), 'Nearby activity for Payroll.Run at 12:59:52.123, record r-124');
  assert.equal(actions.nearbyActivityName(row({ initiatedAt: '2026-10-25T00:30:00.007Z' }), 'Europe/London'), 'Nearby activity for Payroll.Run at 01:30:00.007, record r-124');
});

test('two records of the same API, source and millisecond differ only by id: the names differ, and the visible words stay first', () => {
  const a = row({ id: 'r-1' }), b = row({ id: 'r-2' });
  assert.notEqual(actions.nearbyActivityName(a, 'UTC', 'Demo West'), actions.nearbyActivityName(b, 'UTC', 'Demo West'));
  assert.notEqual(actions.inspectName(a, 'UTC', false, 'Demo West'), actions.inspectName(b, 'UTC', false, 'Demo West'));
  assert.ok(actions.nearbyActivityName(a, 'UTC', 'Demo West').startsWith('Nearby activity'));
  assert.ok(actions.inspectName(a, 'UTC', false, 'Demo West').startsWith('Inspect'));
});

test('the same clock time on two days, and the repeated hour of a clock change, read differently through the id', () => {
  const day1 = row({ id: 'r-day1', initiatedAt: '2026-10-19T08:59:52.123Z' }), day2 = row({ id: 'r-day2', initiatedAt: '2026-10-20T08:59:52.123Z' });
  assert.notEqual(actions.nearbyActivityName(day1, 'UTC'), actions.nearbyActivityName(day2, 'UTC'));
  // London goes back on 25 Oct 2026 at 02:00 BST (01:00 UTC): 00:30 UTC and 01:30 UTC both read 01:30 on the clock
  const first = row({ id: 'r-a', initiatedAt: '2026-10-25T00:30:00.000Z' }), second = row({ id: 'r-b', initiatedAt: '2026-10-25T01:30:00.000Z' });
  assert.equal(actions.requestLabel(first, 'Europe/London').split(', record')[0], actions.requestLabel(second, 'Europe/London').split(', record')[0], 'the clock time alone is the same');
  assert.notEqual(actions.inspectName(first, 'Europe/London'), actions.inspectName(second, 'Europe/London'));
});

test('two rows of the same API at different milliseconds have different names', () => {
  const a = actions.nearbyActivityName(row(), 'UTC'), b = actions.nearbyActivityName(row({ initiatedAt: '2026-10-20T08:59:52.124Z' }), 'UTC');
  assert.notEqual(a, b);
});

test('the source is part of the name, so two sources with the same API at the same moment read differently', () => {
  assert.equal(actions.nearbyActivityName(row(), 'UTC', 'Demo West'), 'Nearby activity for Payroll.Run at 08:59:52.123, Demo West, record r-124');
  assert.equal(actions.inspectName(row(), 'UTC', false, 'Demo East'), 'Inspect record for Payroll.Run at 08:59:52.123, Demo East, record r-124');
  assert.equal(actions.inspectName(row(), 'UTC', true, 'Demo East'), 'Loading… record for Payroll.Run at 08:59:52.123, Demo East, record r-124');
  assert.notEqual(actions.nearbyActivityName(row(), 'UTC', 'Demo West'), actions.nearbyActivityName(row(), 'UTC', 'Demo East'));
});

test('a row without a start time or a name still gets a readable name', () => {
  assert.equal(actions.nearbyActivityName(row({ initiatedAt: null }), 'UTC'), 'Nearby activity for Payroll.Run at unknown time, record r-124');
  assert.equal(actions.nearbyActivityName(row({ initiatedAt: 'not a time' }), 'UTC'), 'Nearby activity for Payroll.Run at unknown time, record r-124');
  assert.equal(actions.inspectName(row({ controller: '', action: '' }), 'UTC'), 'Inspect record for unnamed API at 08:59:52.123, record r-124');
  assert.equal(actions.inspectName(row({ controller: 'Payroll', action: '' }), 'UTC'), 'Inspect record for Payroll at 08:59:52.123, record r-124');
});

test('the three row actions share one class, and the table gives both its buttons the same line and the same box', () => {
  const table = read('src/components/RequestTable.svelte');
  const buttons = [...table.matchAll(/<button type="button" class="quiet-button row-action"[^>]*>/g)].map((m) => m[0]);
  assert.equal(buttons.length, 2, 'Nearby activity and Inspect are both type=button quiet row-action buttons');
  assert.match(buttons[0], /aria-label=\{nearbyActivityName\(row, timeZone\.zone, sourceLabels\.get\(row\.sourceId\) \?\? row\.sourceId\)\}/);
  assert.match(buttons[1], /aria-label=\{inspectName\(row, timeZone\.zone, recordId === row\.id, sourceLabels\.get\(row\.sourceId\) \?\? row\.sourceId\)\}/);
  assert.doesNotMatch(table, /secondary-button payload-button/);
  assert.match(table, /\.row-actions :global\(button\) \{ flex: 1 1 100%;/);
  const css = read('src/app.css');
  // not faint text: a visible line around it; a press area of 24 px or more from the ::after; it never wraps
  assert.match(css, /\.row-action \{[^}]*border-color: var\(--control-line\);[^}]*white-space: nowrap;/);
  assert.match(css, /\.row-action::after \{ content: ''; position: absolute; left: min\(0px, calc\(\(100% - 24px\) \/ 2\)\)/);
  assert.match(read('src/components/RequestVolume.svelte'), /<button type="button" class="quiet-button row-action" aria-label=\{'Show only '/);
});
