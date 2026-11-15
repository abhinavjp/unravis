const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The timeline page's parity with the picker's quick ranges: the "Quick range" chip row model (incidentQuickChips, withQuickRange,
// quickRangeIsActive), the new words in windows.ts, and the wiring of TracingPaper.svelte (read as text, like the picker wiring tests).

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
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relativePath) => fs.readFileSync(path.join(root, relativePath), 'utf8');
const m = loadTypeScript('src/lib/tracing/pickerModel.ts');
const windows = loadTypeScript('src/lib/tracing/windows.ts');

const NOW = Date.UTC(2026, 9, 20, 9, 0, 40); // 2026-10-20 09:00:40 UTC
const span = (start, end) => Date.parse(end + ':00Z') - Date.parse(start + ':00Z');

test('the page chips carry the popover ranges without the "(default)" note', () => {
  const chips = m.incidentQuickChips(NOW);
  assert.deepEqual(chips.map((q) => q.label), ['Last 15 min', 'Last hour', 'Last 4 hours', 'Last 24 hours', 'Last 7 days', 'Last 30 days']);
  const popover = m.incidentQuickRanges(NOW);
  assert.deepEqual(chips.map((q) => q.id), popover.map((q) => q.id));
  assert.deepEqual(chips.map((q) => q.range), popover.map((q) => q.range), 'the same ranges as the popover');
  assert.ok(popover.some((q) => q.label.includes('(default)')), 'only the page row drops the note');
  assert.ok(chips.every((q) => !q.label.includes('(default)')));
});

test('withQuickRange sets the incident and the equal period right before it', () => {
  const base = { incidentStart: '2026-10-20T08:00', incidentEnd: '2026-10-20T09:00', referenceStart: '2026-10-20T07:00', referenceEnd: '2026-10-20T08:00' };
  const quick = m.incidentQuickChips(NOW).find((q) => q.id === '4h');
  const next = m.withQuickRange(base, quick);
  assert.equal(next.incidentStart, '2026-10-20T05:00');
  assert.equal(next.incidentEnd, '2026-10-20T09:00');
  assert.equal(next.referenceStart, '2026-10-20T01:00');
  assert.equal(next.referenceEnd, '2026-10-20T05:00');
  assert.equal(next.referenceEnd, next.incidentStart, 'the comparison period ends where the incident starts');
  assert.equal(span(next.referenceStart, next.referenceEnd), span(next.incidentStart, next.incidentEnd), 'and is as long');
});

test('withQuickRange returns the same object for a null range and keeps extra properties', () => {
  const base = { incidentStart: '2026-10-20T08:00', incidentEnd: '2026-10-20T09:00', referenceStart: '2026-10-20T07:00', referenceEnd: '2026-10-20T08:00' };
  const empty = { id: 'none', label: 'No range', range: null };
  assert.equal(m.withQuickRange(base, empty), base, 'nothing to apply: the very same object comes back');
  const quick = m.incidentQuickChips(NOW).find((q) => q.id === '1h');
  const kept = m.withQuickRange({ ...base, note: 'kept' }, quick);
  assert.equal(kept.note, 'kept', 'properties the model does not know survive');
  assert.equal(kept.incidentStart, '2026-10-20T08:00');
});

test('quickRangeIsActive is true only for an exact match of all four fields', () => {
  const base = { incidentStart: '2026-10-20T08:00', incidentEnd: '2026-10-20T09:00', referenceStart: '2026-10-20T07:00', referenceEnd: '2026-10-20T08:00' };
  const quick = m.incidentQuickChips(NOW).find((q) => q.id === '1h');
  assert.equal(m.quickRangeIsActive(base, quick), true, 'the fresh defaults are the last hour');
  const applied = m.withQuickRange({ incidentStart: '', incidentEnd: '', referenceStart: '', referenceEnd: '' }, m.incidentQuickChips(NOW).find((q) => q.id === '4h'));
  assert.equal(m.quickRangeIsActive(applied, m.incidentQuickChips(NOW).find((q) => q.id === '4h')), true);
  for (const key of ['incidentStart', 'incidentEnd', 'referenceStart', 'referenceEnd']) {
    assert.equal(m.quickRangeIsActive({ ...base, [key]: '2026-01-01T00:00' }, quick), false, key + ' differs');
  }
  assert.equal(m.quickRangeIsActive(base, { id: 'none', label: 'No range', range: null }), false, 'a null range is never active');
});

test('the new window words are present and not empty', () => {
  const t = windows.WINDOW_TEXT;
  for (const key of ['quickRow', 'incidentHeading', 'incidentHelp', 'referenceHeading', 'referenceHeadingNote', 'referenceHelp', 'addEvidence', 'stale']) {
    assert.equal(typeof t[key], 'string', key);
    assert.ok(t[key].length > 0, key + ' is not empty');
  }
  assert.equal(t.quickRow, 'Quick range');
  assert.equal(t.incidentHeading, 'Investigation window');
  assert.equal(t.startTracing, 'Start tracing');
  assert.ok(!('refresh' in t), 'there is one button to read the timeline, not two');
  assert.equal(t.addEvidence, 'Add evidence');
});

test('wiring: the screen draws the chip row, the two buttons, and keeps the hidden legends', () => {
  const paper = read('src/components/TracingPaper.svelte');
  assert.ok(paper.includes('data-testid="tp-quick-row"'), 'the Quick range row carries its test id');
  assert.ok(paper.includes('WINDOW_TEXT.startTracing'), 'the Start tracing button uses the shared words');
  assert.ok(paper.includes('WINDOW_TEXT.addEvidence'), 'the Add evidence button uses the shared words');
  assert.ok(paper.includes('openCaseImport'), 'Add evidence opens the import drawer');
  assert.ok(paper.includes('<legend class="visually-hidden">{WINDOW_TEXT.incidentLabel}</legend>'), 'the incident legend is hidden but still names the group');
  assert.ok(paper.includes('<legend class="visually-hidden">{WINDOW_TEXT.referenceLabel}</legend>'), 'the comparison legend is hidden but still names the group');
});

test('wiring: a source connected while the screen is open is read at once, with the last windows', () => {
  const paper = read('src/components/TracingPaper.svelte');
  assert.ok(paper.includes('app.sources.map((source) => source.id)'), 'the screen watches the ids of the open audit sources');
  assert.ok(/void run\(lastBody \?\? undefined\);/.test(paper), 'a new source id re-reads with the windows of the last read');
  assert.ok(!paper.includes('canRefresh'), 'the second read button is gone');
});
