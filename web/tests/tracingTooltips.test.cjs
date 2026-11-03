const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The chart tooltip text: the lane head hints and the plain-word lines for one chart bar.

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}

const { explain, DICTIONARY } = loadTypeScript('src/lib/copy/dictionary.ts');
const { LANE_DICT_ID, TOOLTIP_TEXT, formatCount, laneHint, barTipLines, isTouchPointer, slotAtFraction, tooltipCopyEntries } = loadTypeScript('src/lib/tracing/tooltip.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const bar = (extra = {}) => ({
  key: 'audit',
  startText: '10:00',
  endText: '10:01',
  zoneName: 'UTC',
  count: 1200,
  errors: 3,
  origin: 'live',
  asOfText: '10:05',
  note: '',
  ...extra
});

test('each lane head hint is the dictionary text behind its id, and an unknown key has none', () => {
  assert.deepEqual(LANE_DICT_ID, { audit: 'lane.api-audit', sql: 'lane.sql', iis: 'lane.iis' });
  assert.equal(laneHint('audit'), explain('lane.api-audit'));
  assert.equal(laneHint('sql'), explain('lane.sql'));
  assert.equal(laneHint('iis'), explain('lane.iis'));
  for (const key of Object.keys(LANE_DICT_ID)) assert.ok(laneHint(key).length > 0);
  assert.equal(laneHint('mongo'), '');
  assert.equal(laneHint(''), '');
});

test('a live audit bar with errors lists the span, the count, the errors, their meaning and the read', () => {
  assert.deepEqual(barTipLines(bar()), [
    '10:00 to 10:01 (UTC)',
    '1,200 requests',
    '3 server errors (5xx)',
    explain('status.5xx'),
    'Read just now (as of 10:05)'
  ]);
});

test('one error is worded in the singular', () => {
  const lines = barTipLines(bar({ errors: 1 }));
  assert.ok(lines.includes('1 server error (5xx)'));
  assert.ok(!lines.includes('1 server errors (5xx)'));
});

test('an IIS bar with 5xx answers lists the count and what 5xx means, as the audit lane does (UI-R2-01)', () => {
  const lines = barTipLines(bar({ key: 'iis', count: 17, errors: 3, origin: 'case' }));
  assert.deepEqual(lines, [
    '10:00 to 10:01 (UTC)',
    '17 requests',
    '3 server errors (5xx)',
    explain('status.5xx'),
    explain('iis.point'),
    'From saved logs (as of 10:05)'
  ]);
  assert.ok(barTipLines(bar({ key: 'iis', count: 2, errors: 1 })).includes('1 server error (5xx)'));
  assert.ok(!barTipLines(bar({ key: 'iis', count: 2, errors: 0 })).some((line) => line.includes('5xx')));
  assert.ok(!barTipLines(bar({ key: 'sql', count: 2, errors: 1 })).some((line) => line.includes('5xx')), 'SQL has no 5xx count');
});

test('an IIS bar explains its points; a sql bar counts calls, and one call is singular', () => {
  const iis = barTipLines(bar({ key: 'iis', count: 2, errors: 0 }));
  assert.ok(iis.includes(explain('iis.point')));
  assert.deepEqual(barTipLines(bar({ key: 'sql', count: 1, errors: 0 }))[1], '1 call');
  assert.ok(barTipLines(bar({ key: 'sql', count: 4, errors: 0 })).includes('4 calls'));
});

test('an empty bucket says so and shows neither the 5xx line nor the IIS point line', () => {
  const lines = barTipLines(bar({ count: 0 }));
  assert.deepEqual(lines, ['10:00 to 10:01 (UTC)', 'No events in this bucket', 'Read just now (as of 10:05)']);
  const iis = barTipLines(bar({ key: 'iis', count: 0 }));
  assert.ok(!iis.includes(explain('iis.point')));
  assert.ok(!iis.some((line) => line.includes('5xx')));
});

test('a bar from a saved case says where its events came from, with or without an as-of time', () => {
  const withAsOf = barTipLines(bar({ origin: 'case', key: 'sql', count: 1, errors: 0 }));
  assert.ok(withAsOf.includes('From saved logs (as of 10:05)'));
  const withoutAsOf = barTipLines(bar({ origin: 'case', key: 'sql', count: 1, errors: 0, asOfText: '' }));
  assert.ok(withoutAsOf.includes('From saved logs'));
  assert.ok(!withoutAsOf.includes('From saved logs ('));
});

test('a note is the last line', () => {
  const note = 'The audit read ran out of time; nothing was shortened.';
  const lines = barTipLines(bar({ note }));
  assert.equal(lines[lines.length - 1], note);
});

test('a line whose text is empty is dropped rather than shown as a blank row', () => {
  const entry = DICTIONARY['iis.point'];
  const saved = { ...entry };
  entry.plain = '';
  entry.term = '';
  try {
    const lines = barTipLines(bar({ key: 'iis', count: 2, errors: 0 }));
    assert.ok(!lines.includes(''));
  } finally {
    Object.assign(entry, saved);
  }
  assert.equal(explain('iis.point'), saved.plain);
});

test('dictionary text is used exactly as written, with no HTML escaping of its own', () => {
  const entry = DICTIONARY['status.5xx'];
  const saved = entry.plain;
  entry.plain = '<b>&';
  try {
    assert.ok(barTipLines(bar()).includes('<b>&'));
  } finally {
    entry.plain = saved;
  }
  assert.equal(explain('status.5xx'), saved);
});

test('a fraction maps to a bucket slot, clamped, and bad input picks no slot', () => {
  const n = 10;
  assert.equal(slotAtFraction(0, n), 0);
  assert.equal(slotAtFraction(0.999, n), n - 1);
  assert.equal(slotAtFraction(1, n), n - 1);
  assert.equal(slotAtFraction(-1, n), 0);
  assert.equal(slotAtFraction(Number.NaN, n), -1);
  assert.equal(slotAtFraction(0.5, 0), -1);
  assert.equal(slotAtFraction(0.5, -3), -1);
});

test('the fixed strings in the tooltip module pass the screen-text rules', () => {
  assert.deepEqual(lintScreenStrings(tooltipCopyEntries(), deck.terms), []);
});

const chartSource = fs.readFileSync(path.join(root, 'src/components/TracingChart.svelte'), 'utf8');

test('the chart wires lane hints and the bucket tooltip for hover and keyboard', () => {
  assert.match(chartSource, /id="tc-tip"[^>]*role="tooltip"|role="tooltip"[^>]*id="tc-tip"/);
  assert.match(chartSource, /aria-describedby="tc-tip"/);
  assert.match(chartSource, /aria-describedby="tc-hint-\{lane\.key\}"/);
  assert.match(chartSource, /id="tc-hint-\{lane\.key\}"/);
  assert.match(chartSource, /class="lane-label" role="group"/);
  assert.doesNotMatch(chartSource, /class="lane-label"[^>]*tabindex/, 'a lane head has no action, so it is not a tab stop (UI-R2-07)');
  assert.match(chartSource, /onpointermove=\{onOverlayMove\}/);
  assert.match(chartSource, /onpointerleave=\{onOverlayLeave\}/);
  assert.match(chartSource, /event\.key === 'Escape'/);
  assert.match(chartSource, /:focus-within \.hint/);
});

test('the chart tooltip is plain text only and stays out of the old chart code', () => {
  assert.doesNotMatch(chartSource, /\{@html/);
  assert.doesNotMatch(chartSource, /\btitle=/);
  assert.doesNotMatch(chartSource, /echarts|callTrace|CallTrace|guided/i);
});

test('counts use thousands separators, the same as the axis (UI-R1-10)', () => {
  assert.equal(formatCount(60000), '60,000');
  assert.equal(formatCount(999), '999');
  assert.equal(formatCount(0), '0');
  assert.ok(barTipLines(bar({ count: 60000, errors: 1250 })).includes('1,250 server errors (5xx)'));
});

test('the time zone is named once in the range line (UI-R1-11)', () => {
  assert.equal(barTipLines(bar({ startText: '19 Oct 10:20 UTC', endText: '19 Oct 10:25 UTC' }))[0], '19 Oct 10:20 to 19 Oct 10:25 UTC');
  assert.equal(barTipLines(bar({ startText: '19 Oct 10:20', endText: '19 Oct 10:25' }))[0], '19 Oct 10:20 to 19 Oct 10:25 (UTC)');
  assert.equal(barTipLines(bar({ zoneName: '' }))[0], '10:00 to 10:01');
});

test('a zone shared by both times is named once, also in Local (UI-R3-04)', () => {
  const local = barTipLines(bar({ startText: '19 Oct 14:20 GMT+4', endText: '19 Oct 14:25 GMT+4', zoneName: 'Asia/Dubai' }))[0];
  assert.equal(local, '19 Oct 14:20 to 19 Oct 14:25 GMT+4');
  assert.equal((local.match(/GMT/g) || []).length, 1);
  assert.ok(!local.includes('Asia/Dubai'));
  // Times with no letters in the last word keep the old rule: the zone name is added once.
  assert.equal(barTipLines(bar({ startText: '10:20', endText: '10:25' }))[0], '10:20 to 10:25 (UTC)');
});

test('a finger or a pen is a touch pointer, a mouse never is (UI-R3-06)', () => {
  assert.equal(isTouchPointer('touch', false), true);
  assert.equal(isTouchPointer('pen', false), true);
  assert.equal(isTouchPointer('mouse', false), false);
  assert.equal(isTouchPointer('mouse', true), false);
  assert.equal(isTouchPointer('', true), true);
  assert.equal(isTouchPointer(undefined, true), true);
  assert.equal(isTouchPointer('', false), false);
  assert.equal(isTouchPointer(undefined, false), false);
});

test('the tap-again line is plain copy and is in the lint list (UI-R3-06)', () => {
  assert.match(TOOLTIP_TEXT.tapAgain, /^Tap this bar again/);
  assert.ok(tooltipCopyEntries().some((entry) => entry.text === TOOLTIP_TEXT.tapAgain));
});
