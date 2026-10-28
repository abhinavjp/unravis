const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// P7 UI review fixes: one regression test per finding (M1 to M4, minors 1 to 15). The components are Svelte, so where a rule lives in a component
// the test reads its source; where it can be a pure function, the function is exercised.

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
const comp = (name) => read('src/components/sources/' + name + '.svelte');

const lanes = loadTypeScript('src/lib/lanes.ts');
const copy = loadTypeScript('src/lib/copy/sourcesCopy.ts');
const { CaseApiError } = loadTypeScript('src/lib/cases.ts');

const day = (over = {}) => ({ id: 1, at: '2026-10-20T09:00:00Z', kind: 'sql', label: 'SQL Server', database: 'Audit', state: 'Ready', incident: { start: '2026-10-19T10:00:00Z', end: '2026-10-19T11:00:00Z' }, reference: { start: '', end: '' }, lane: null, ...over });

// ---- M1 ------------------------------------------------------------------------------------------------------------------------------

test('M1: a read that was not kept says so on the page, and the notice outlives the runner', () => {
  const kept = lanes.runNotice({ saved: true, saveError: '', day: day() });
  assert.equal(kept.good, true);
  assert.equal(kept.text, copy.RUN_TEXT.done);
  const lost = lanes.runNotice({ saved: false, saveError: 'The case is closed, so the result was not saved.', day: day({ id: 0 }) });
  assert.equal(lost.good, false);
  assert.ok(lost.text.startsWith(copy.RUN_TEXT.notSaved) && lost.text.includes('closed'));
  assert.ok(lanes.runNotice({ saved: false, saveError: '', day: day({ id: 0 }) }).text.includes(copy.RUN_TEXT.failed), 'no reason from the server still gives a sentence');
  const step = comp('SourceStep');
  // The notice is drawn by the step itself, outside every branch that mounts or removes the runner.
  const noticeAt = step.indexOf('data-notice');
  assert.ok(noticeAt > 0 && noticeAt < step.indexOf('{#if loadFailed}'), 'the notice sits above the branches');
  assert.match(step, /notice = runNotice\(result\)/);
  assert.match(step, /unsaved = result\.saved \? null : result\.day/, 'a day that was read but not kept is still shown, from memory');
  assert.equal((step.match(/ownNotice/g) ?? []).length, 2, 'both runners of the step leave the message to the step');
  const runner = comp('LaneRunner');
  assert.match(runner, /if \(!ownNotice\)/);
});

// ---- minor 13 ------------------------------------------------------------------------------------------------------------------------

test('minor 13: with nothing chosen the newest day opens, not the first one in the list', () => {
  assert.equal(lanes.newestDay([]), null);
  const list = [day({ id: 3 }), day({ id: 9 }), day({ id: 5 })];
  assert.equal(lanes.newestDay(list).id, 9);
  assert.equal(lanes.newestDay([...list].reverse()).id, 9, 'the order of the list does not matter');
  assert.match(comp('SourceStep'), /newestDay\(mine\)/);
  assert.doesNotMatch(comp('SourceStep'), /mine\[0\]/);
});
