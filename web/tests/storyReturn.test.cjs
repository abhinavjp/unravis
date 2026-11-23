// BL-162 / BL-164 leftover: Story so far also reads again when the person comes back to the page, because a change made somewhere the page
// cannot see (another browser tab, the API) has no signal in this tab. Pure rules (`storyReturn.ts`) are driven directly; the page's own wiring
// is pinned against the Svelte source.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'src/lib/storyReturn.ts'), 'utf8');
const module_ = { exports: {} };
new Function('module', 'exports', ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(module_, module_.exports);
const { RETURN_GAP_MS, readOnReturn, sameRead, watchReturn } = module_.exports;
const page = fs.readFileSync(path.join(root, 'src/lib/components/CaseHome.svelte'), 'utf8');

const base = { ready: true, hidden: false, busy: false, changedElsewhere: false, lastReadAt: 1000, now: 1000 + RETURN_GAP_MS };

test('coming back after the gap reads the case again', () => {
  assert.equal(readOnReturn(base), true);
});

test('no request storm: a return inside the gap, or while a read is on its way, reads nothing', () => {
  assert.equal(readOnReturn({ ...base, now: base.lastReadAt + RETURN_GAP_MS - 1 }), false);
  assert.equal(readOnReturn({ ...base, busy: true }), false);
});

test('nothing is read for a page that is hidden, not showing a case, or waiting for a reload after another tab moved the case', () => {
  assert.equal(readOnReturn({ ...base, hidden: true }), false);
  assert.equal(readOnReturn({ ...base, ready: false }), false);
  assert.equal(readOnReturn({ ...base, changedElsewhere: true }), false);
});

test('watchReturn listens to focus and visibilitychange, and stops', () => {
  const log = [];
  const target = (name) => ({ addEventListener: (type, fn) => log.push(`${name}+${type}`) && fn, removeEventListener: (type) => log.push(`${name}-${type}`) });
  const stop = watchReturn(() => {}, target('win'), target('doc'));
  assert.deepEqual(log, ['win+focus', 'doc+visibilitychange']);
  stop();
  assert.deepEqual(log.slice(2), ['win-focus', 'doc-visibilitychange']);
  assert.equal(typeof watchReturn(() => {}, undefined, undefined), 'function');
});

test('an answer that says what the page shows is the same read; a new file is not', () => {
  const files = [{ id: 1, fileName: 'a.log' }];
  assert.equal(sameRead(files, [{ id: 1, fileName: 'a.log' }]), true);
  assert.equal(sameRead(files, [{ id: 1, fileName: 'a.log' }, { id: 2, fileName: 'b.log' }]), false);
  assert.equal(sameRead(null, null), true);
  assert.equal(sameRead(null, files), false);
});

test('Case home reads on return, keeps what it shows when the read fails, and reads each part once', () => {
  assert.match(page, /watchReturn\(backOnPage\)/);
  assert.match(page, /function backOnPage\(\)[\s\S]*readOnReturn\(\{[\s\S]*isHidden\(\)[\s\S]*changedElsewhere: changed[\s\S]*void rereadFiles\(true\)/);
  // The case, its files and its health checks are read together; the theories only on return.
  const reread = page.slice(page.indexOf('async function rereadFiles'), page.indexOf('function backOnPage'));
  assert.match(reread, /getCaseView\(id\)/);
  assert.match(reread, /listImports\(id\)/);
  assert.match(reread, /fetchHealthRuns\(id\)\.catch\(\(\) => null\)/);
  assert.match(reread, /everything && reads \? refreshLedger\(\)/);
  // The answers are put in only when they all came and say something new; a failure goes to the catch and puts in nothing.
  assert.ok(reread.indexOf('const fresh = await getCaseView') < reread.indexOf('info = fresh'));
  assert.match(reread, /if \(!sameRead\(info, fresh\)\) info = fresh/);
  assert.match(reread, /catch \(error\) \{ lockedUnder\(error\)/);
  // The last read starts the gap; the listeners are taken off when the page goes.
  assert.match(reread, /lastReadAt = Date\.now\(\)/);
  assert.match(page, /stopWatching\(\); controller\?\.abort\(\)/);
});
