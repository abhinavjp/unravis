const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// Slow call screen: the nearby calls list component, checked as source text.

const root = path.resolve(__dirname, '..');
function loadCopy() {
  const ts = require('typescript');
  const text = fs.readFileSync(path.join(root, 'src/lib/drill/drillCopy.ts'), 'utf8');
  const output = ts.transpileModule(text, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  new Function('require', 'module', 'exports', output)((request) => { throw new Error('unexpected import ' + request); }, module, module.exports);
  return module.exports;
}
const source = fs.readFileSync(path.join(root, 'src/components/CallNearby.svelte'), 'utf8');

test('the test ids are present', () => {
  for (const id of ['scall-nearby-hint', 'scall-nearby-asking', 'scall-nearby"', 'scall-light-none']) {
    assert.ok(source.includes(`data-testid="${id.replace(/"$/, '')}"`), `has ${id}`);
  }
  assert.match(source, /data-testid=\{`scall-chip-\$\{c\}`\}/);
  for (const chip of ['everyone', 'user', 'api']) assert.ok(source.includes(`'${chip}'`), `chip ${chip}`);
});

test('chips are a labelled group with pressed state and availability', () => {
  assert.match(source, /role="group" aria-label=\{DRILL_TEXT\.chipsLabel\}/);
  assert.match(source, /aria-pressed=\{chip === c\}/);
  assert.match(source, /disabled=\{!chipAvailable\(c, call\)\}/);
  assert.ok(source.includes('chipAvailable('));
});

test('light totals are one wide cell or three cells, never 0 for a missing value', () => {
  assert.ok(source.includes('lightCells('));
  assert.ok(source.includes('colspan="3"'));
  assert.match(source, /light === null/);
  assert.match(source, /value === null \|\| value === undefined \? DRILL_TEXT\.notRecorded/);
  assert.ok(source.includes('DRILL_TEXT.notRecorded'));
  assert.ok(!/\?\?\s*0\b/.test(source), 'no ?? 0 fallback');
  assert.ok(!/\|\|\s*0\b/.test(source), 'no || 0 fallback');
});

test('this call row, gap label and row selection', () => {
  assert.ok(source.includes('gapLabel('));
  assert.ok(source.includes('thisCall: DRILL_TEXT.gapThis'));
  assert.ok(source.includes('during: DRILL_TEXT.gapDuring'));
  assert.ok(source.includes('class:thiscall'));
  assert.match(source, /aria-current=\{own \? 'true' : undefined\}/);
  assert.ok(source.includes('DRILL_TEXT.thisCallMark'));
  // BL-495: the mouse keeps the whole row, the keyboard gets a real button in the API cell that keeps its press to itself
  assert.match(source, /<tr class:thiscall=\{own\} onclick=\{\(\) => onSelect\(row\)\}>/);
  assert.match(source, /<button type="button" class="row-btn" data-testid="scall-nearby-open"/);
  assert.match(source, /aria-label=\{DRILL_TEXT\.openNearbyCall\(/);
  assert.match(source, /function pick\(event: MouseEvent, row: NearbyRow\)[\s\S]*?event\.stopPropagation\(\);[\s\S]*?onSelect\(row\);/);
  assert.doesNotMatch(source, /tabindex|onkeydown|event\.key/);
});

test('BL-495: the All sources button names the visible words first, then source, time to the millisecond and duration', () => {
  const { DRILL_TEXT } = loadCopy();
  assert.equal(DRILL_TEXT.openSourceItem('Payroll / Run', 'API calls', '08:00:00.123 UTC', '60.0 s'), 'Payroll / Run, API calls, 08:00:00.123 UTC, took 60.0 s');
  assert.equal(DRILL_TEXT.openSourceItem('Marker', 'SQL', '08:00:01 UTC', ''), 'Marker, SQL, 08:00:01 UTC');
});

test('BL-495: on a phone the column name is a CSS label with an empty alternative, so it is not read again in the cell name', () => {
  for (const name of ['CallNearby', 'CallAllSources']) {
    const css = fs.readFileSync(path.join(root, 'src/components/' + name + '.svelte'), 'utf8');
    assert.match(css, /content: attr\(data-label\);\s*\/\*[^]*?\*\/\s*content: attr\(data-label\) \/ '';/, name);
  }
});

test('BL-495: the row button names the visible API first and the start time after it', () => {
  const { DRILL_TEXT } = loadCopy();
  const name = DRILL_TEXT.openNearbyCall('Orders.Get', '10:14:07.123');
  assert.ok(name.startsWith('Orders.Get'), 'the visible text leads the name (label in name)');
  assert.ok(name.includes('10:14:07.123'));
});

test('states use the existing copy keys', () => {
  for (const key of ['askingWider', 'askingUser', 'askingApi', 'askFailed', 'truncated', 'noNearby', 'nearbyHint', 'chipEveryone', 'chipUser', 'chipApi',
    'colTime', 'colFromCall', 'colUser', 'colApi', 'colTook', 'colDatabase', 'colRedis', 'colWebCalls']) {
    assert.ok(source.includes(`DRILL_TEXT.${key}`), `uses ${key}`);
  }
  assert.match(source, /role="status" data-testid="scall-nearby-asking"/);
});

test('no guided import and no inner scroll box', () => {
  assert.ok(!/guided/.test(source), 'no guided import');
  assert.ok(!/overflow(-[xy])?\s*:\s*(auto|scroll)/.test(source), 'no overflow auto or scroll');
  assert.ok(!/max-height|[^-]height\s*:\s*\d/.test(source), 'no fixed height');
});

test('a disabled user or endpoint chip says so in words, not "Only not recorded"', () => {
  assert.ok(source.includes('DRILL_TEXT.chipUserNone'), 'uses chipUserNone');
  assert.ok(source.includes('DRILL_TEXT.chipApiNone'), 'uses chipApiNone');
  assert.ok(!source.includes('chipApi(DRILL_TEXT.notRecorded)'), 'no Only + not recorded label');
});
