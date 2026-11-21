const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const read = (r) => fs.readFileSync(path.join(root, r), 'utf8');
const load = (r) => { const m = { exports: {} }; new Function('module', 'exports', 'require', ts.transpileModule(read(r), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(m, m.exports, require); return m.exports; };
const { addedLine } = load('src/lib/sourceWizard.ts');
const text = { one: (l) => 'Added ' + l + '.', many: (n) => 'Added ' + n + ' sources.', recorder: 'Saved now.' };
const item = (key, kind = 'redis', label = key) => ({ key, kind, label });

// UX-9 / BL-162: every kind of add gets the same confirmation line.
test('one added source names it, several count them, a removal says nothing', () => {
  assert.equal(addedLine([], [item('a', 'redis', 'Redis 10.0.0.1')], text), 'Added Redis 10.0.0.1.');
  assert.equal(addedLine(['a'], [item('a'), item('b'), item('c')], text), 'Added 2 sources.');
  assert.equal(addedLine(['a', 'b'], [item('a')], text), '');
  assert.equal(addedLine(['a'], [item('a')], text), '');
});

test('a Blackbox add also says the recorder is saved at once', () => {
  assert.equal(addedLine([], [item('r', 'blackbox', 'This PC')], text), 'Added This PC. Saved now.');
});

test('the wizard step shows the line through one live region for every kind', () => {
  const step = read('src/components/sourcewiz/SourceStep.svelte');
  assert.match(step, /addedLine\(seenKeys, now, WIZ\.added\)/);
  assert.match(step, /role="status" data-added-note>\{addedNote\}/);
  assert.match(read('src/lib/copy/sourceWizardCopy.ts'), /Nothing is read until the last button\./);
});

// UX-10: the lane "?" buttons came out 21 px on a 14 px root; the floor is 24 px (WCAG 2.5.8).
test('the Timeline lane help buttons are never below 24 px', () => {
  const chart = read('src/components/TracingChart.svelte');
  assert.match(chart, /\.hint-btn \{ flex: none; width: max\(1\.5rem, 24px\); height: max\(1\.5rem, 24px\);/);
  assert.match(read('src/lib/copy/fileKindsCopy.ts'), /'1 file is ready to add\.'/);
});
