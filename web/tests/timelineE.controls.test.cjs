const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Investigation Timeline gaps 1 to 3: the one controls card, the UTC readout, the 12-hour time boxes, and the "Each bar" line.

const root = path.resolve(__dirname, '..');
const loaded = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const picker = loadTypeScript('src/lib/tracing/pickerModel.ts');
const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const windows = loadTypeScript('src/lib/tracing/windows.ts');

test('24-hour time parts (owner call 2026-11-21, BL-181): every minute of the day survives the round trip through the two boxes', () => {
  for (let h = 0; h < 24; h++) {
    for (const m of [0, 1, 29, 30, 59]) {
      const time = String(h).padStart(2, '0') + ':' + String(m).padStart(2, '0');
      const parts = picker.timeParts(time);
      assert.deepEqual(parts, { hour: h, minute: m }, time);
      assert.equal(picker.timeFromParts(parts.hour, parts.minute), time);
    }
  }
  assert.equal(picker.timeParts(''), null);
  assert.equal(picker.timeParts('24:00'), null);
  assert.deepEqual([...picker.HOURS_24], Array.from({ length: 24 }, (_, i) => i));
  assert.equal(picker.MINUTES_60.length, 60);
  assert.equal(picker.HOURS_12, undefined, 'no 12-hour box is left');
});

test('typed times: 24-hour text is still accepted, 12-hour text is read, nonsense is refused', () => {
  const cases = { '22:30': '22:30', '9:30': '09:30', '10:30 PM': '22:30', '10:30pm': '22:30', '12:15 a.m.': '00:15', '12:00 PM': '12:00', '00:00': '00:00', '23:59': '23:59' };
  for (const [typed, kept] of Object.entries(cases)) assert.equal(picker.parseTime(typed), kept, typed);
  for (const bad of ['24:00', '13:00 PM', '0:30 AM', '10:75', 'ten', '']) assert.equal(picker.parseTime(bad), null, bad);
});

test('the time box is two labelled 24-hour selects, hour 00 to 23 and minute, and the pickers use it', () => {
  const box = read('src/lib/components/TimeBox.svelte');
  assert.ok(box.includes('role="group"'));
  for (const label of ['hourWord', 'minuteWord']) assert.ok(box.includes(label), label);
  assert.ok(!/AM|PM|periodWord/.test(box), 'no AM or PM box');
  assert.equal((box.match(/<select/g) || []).length, 2);
  assert.match(box, /HOURS_24 as h/);
  assert.match(box, /\{two\(h\)\}/, 'hours show as 00 to 23');
  for (const file of ['src/lib/components/WindowPicker.svelte', 'src/lib/components/StampField.svelte']) assert.ok(read(file).includes('<TimeBox'), file);
});

test('both boxes offer "--", an empty time shows it, and picking it clears the time', () => {
  const box = read('src/lib/components/TimeBox.svelte');
  assert.equal((box.match(/<option value="">\{PICKER_TEXT\.unset\}<\/option>/g) || []).length, 2, 'both boxes can be unset');
  assert.match(box, /change\.hour === '' \|\| change\.minute === ''/, 'choosing -- clears the whole time');
});

test('one controls card holds the quick range, both windows, the UTC readout, the sources row and the notices', () => {
  const paper = read('src/components/TracingPaper.svelte');
  const open = paper.indexOf('<section class="card trctl"');
  const close = paper.indexOf('</section>', open);
  assert.ok(open > 0 && close > open);
  const card = paper.slice(open, close);
  for (const needle of ['data-region="windows"', 'tp-quick-row', 'data-testid="tp-metaline"', 'utc-echo-incident', 'utc-echo-reference', 'data-region="sources"', '<TraceSources', 'WINDOW_TEXT.addEvidence', 'WINDOW_TEXT.startTracing', 'trctl-notices', 'tp-problems']) {
    assert.ok(card.includes(needle), 'the card must hold ' + needle);
  }
  assert.ok(card.indexOf('tp-metaline') < card.indexOf('data-region="sources"'), 'the readout sits under the windows, above the sources row');
  assert.ok(!paper.slice(close).includes('<TraceSources'), 'the sources row is not outside the card');
  const sources = read('src/components/TraceSources.svelte');
  assert.ok(!/\.ts-card|class="card/.test(sources), 'the sources row has no card of its own');
});

test('the UTC readout wording is fixed and comes from the window text', () => {
  const w = windows.WINDOW_TEXT;
  assert.equal(w.metaCompared, 'compared with');
  assert.equal(w.metaUnset, 'not set');
  assert.ok(w.metaZoneTail.includes('stores UTC'));
  const paper = read('src/components/TracingPaper.svelte');
  assert.ok(paper.includes('`UTC ${start}:00Z – ${end}:00Z`'));
  assert.deepEqual(lintScreenStrings(['controlsLabel', 'metaCompared', 'metaUnset', 'metaZoneLead', 'metaZoneTail'].map((key) => ({ id: 'window.' + key, text: w[key] })), deck.terms), []);
});

test('the chart says how much time one bar covers, from the grid it draws', () => {
  const start = Date.parse('2001-03-04T10:00:00Z');
  const window = (hours, bucketSeconds) => ({ start: new Date(start).toISOString(), end: new Date(start + hours * 3600e3).toISOString(), axisStart: new Date(start).toISOString(), axisEnd: new Date(start + hours * 3600e3).toISOString(), bucketSeconds });
  const one = lanes.barSizeParts(lanes.slotGrid(window(1, 60)), 60);
  assert.equal(one.lead, 'Each bar = 1 minute');
  assert.ok(one.rest.includes('60 bars on the axis'));
  const wide = lanes.barSizeParts(lanes.slotGrid(window(48, 1800)), 1800);
  assert.equal(wide.lead, 'Each bar = 30 minutes');
  assert.ok(wide.rest.includes('longer periods group more time into each bar'));
  const chart = read('src/components/TracingChart.svelte');
  assert.ok(chart.includes('data-testid="bar-size"'));
});
