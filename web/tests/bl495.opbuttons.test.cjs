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
const row = (over = {}) => ({ controller: 'Payroll', action: 'Run', initiatedAt: at, ...over });

test('the Nearby activity name starts with the visible words, then the API and the start time to the millisecond', () => {
  assert.equal(actions.nearbyActivityName(row(), 'UTC'), 'Nearby activity for Payroll.Run at 08:59:52.123');
});

test('the Inspect name starts with the visible word; while loading it starts with the visible "Loading…"', () => {
  assert.equal(actions.inspectName(row(), 'UTC'), 'Inspect record for Payroll.Run at 08:59:52.123');
  assert.equal(actions.inspectName(row(), 'UTC', true), 'Loading… record for Payroll.Run at 08:59:52.123');
  for (const visible of ['Inspect', 'Loading…']) assert.ok(actions.inspectName(row(), 'UTC', visible === 'Loading…').startsWith(visible));
});

test('the time is read in the zone of the table, with no date and no zone name', () => {
  assert.equal(actions.nearbyActivityName(row(), 'Asia/Dubai'), 'Nearby activity for Payroll.Run at 12:59:52.123');
  assert.equal(actions.nearbyActivityName(row({ initiatedAt: '2026-10-25T00:30:00.007Z' }), 'Europe/London'), 'Nearby activity for Payroll.Run at 01:30:00.007');
});

test('two rows of the same API at different milliseconds have different names', () => {
  const a = actions.nearbyActivityName(row(), 'UTC'), b = actions.nearbyActivityName(row({ initiatedAt: '2026-10-20T08:59:52.124Z' }), 'UTC');
  assert.notEqual(a, b);
});

test('the source is part of the name, so two sources with the same API at the same moment read differently', () => {
  assert.equal(actions.nearbyActivityName(row(), 'UTC', 'Demo West'), 'Nearby activity for Payroll.Run at 08:59:52.123, Demo West');
  assert.equal(actions.inspectName(row(), 'UTC', false, 'Demo East'), 'Inspect record for Payroll.Run at 08:59:52.123, Demo East');
  assert.equal(actions.inspectName(row(), 'UTC', true, 'Demo East'), 'Loading… record for Payroll.Run at 08:59:52.123, Demo East');
  assert.notEqual(actions.nearbyActivityName(row(), 'UTC', 'Demo West'), actions.nearbyActivityName(row(), 'UTC', 'Demo East'));
});

test('a row without a start time or a name still gets a readable name', () => {
  assert.equal(actions.nearbyActivityName(row({ initiatedAt: null }), 'UTC'), 'Nearby activity for Payroll.Run at unknown time');
  assert.equal(actions.nearbyActivityName(row({ initiatedAt: 'not a time' }), 'UTC'), 'Nearby activity for Payroll.Run at unknown time');
  assert.equal(actions.inspectName(row({ controller: '', action: '' }), 'UTC'), 'Inspect record for unnamed API at 08:59:52.123');
  assert.equal(actions.inspectName(row({ controller: 'Payroll', action: '' }), 'UTC'), 'Inspect record for Payroll at 08:59:52.123');
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
