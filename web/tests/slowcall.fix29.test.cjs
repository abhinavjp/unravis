const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// FIX-29 / UI-02: keyboard moves on the overview strip preview at once and commit once, after the keys stop.
// Behaviour tests run the real pure module (src/lib/drill/keyCommit.ts) with node's fake timers.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { createKeyCommit, stripKeyTarget, KEY_COMMIT_DELAY_MS, PAGE_STEP_FRACTION } = loadTypeScript('src/lib/drill/keyCommit.ts');
const { KEY_STEP_FRACTION } = loadTypeScript('src/lib/drill/callMap.ts');

test('5 rapid keys give one commit, with the last value, after the delay', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const commits = [];
  const kc = createKeyCommit((v) => commits.push(v));
  for (let i = 1; i <= 5; i++) {
    kc.push(i);
    t.mock.timers.tick(KEY_COMMIT_DELAY_MS - 50); // every key lands inside the window of the one before
  }
  assert.deepEqual(commits, [], 'nothing committed while keys keep coming');
  assert.equal(kc.pending(), true);
  t.mock.timers.tick(50);
  assert.deepEqual(commits, [5], 'one commit, the last value');
  assert.equal(kc.pending(), false);
  t.mock.timers.tick(5000);
  assert.deepEqual(commits, [5], 'and no second one');
});

test('the delay is about 250 ms and a quiet gap longer than it commits each burst separately', (t) => {
  assert.equal(KEY_COMMIT_DELAY_MS, 250);
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const commits = [];
  const kc = createKeyCommit((v) => commits.push(v));
  kc.push('a');
  t.mock.timers.tick(249);
  assert.deepEqual(commits, []);
  t.mock.timers.tick(1);
  assert.deepEqual(commits, ['a']);
  kc.push('b');
  kc.push('c');
  t.mock.timers.tick(250);
  assert.deepEqual(commits, ['a', 'c']);
});

test('flush commits the pending value at once (focus leaves the strip); cancel drops it (drag, click, close)', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const commits = [];
  const kc = createKeyCommit((v) => commits.push(v));
  kc.push(1);
  kc.push(2);
  kc.flush();
  assert.deepEqual(commits, [2]);
  t.mock.timers.tick(1000);
  assert.deepEqual(commits, [2], 'the flushed timer does not fire again');
  kc.flush();
  assert.deepEqual(commits, [2], 'flush with nothing pending does nothing');
  kc.push(3);
  kc.cancel();
  t.mock.timers.tick(1000);
  assert.deepEqual(commits, [2], 'cancel drops the pending commit');
  assert.equal(kc.pending(), false);
});

test('a commit that throws does not leave the value pending', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let calls = 0;
  const kc = createKeyCommit(() => { calls++; throw new Error('boom'); });
  kc.push(1);
  assert.throws(() => t.mock.timers.tick(250));
  assert.equal(kc.pending(), false);
  t.mock.timers.tick(1000);
  assert.equal(calls, 1);
});

test('keys: arrows step 5%, Page Up / Page Down step further, Home goes to the call, End to the end of the strip', () => {
  const overview = { start: 0, end: 1000 };
  const callMid = 400;
  const at = (key, centreMs) => stripKeyTarget(key, centreMs, overview, callMid);
  assert.ok(PAGE_STEP_FRACTION > KEY_STEP_FRACTION, 'a page is a bigger step than an arrow');
  assert.equal(at('ArrowRight', 500), 500 + 1000 * KEY_STEP_FRACTION);
  assert.equal(at('ArrowLeft', 500), 500 - 1000 * KEY_STEP_FRACTION);
  assert.equal(at('PageUp', 500), 500 + 1000 * PAGE_STEP_FRACTION, 'Page Up goes later, like a slider value going up');
  assert.equal(at('PageDown', 500), 500 - 1000 * PAGE_STEP_FRACTION);
  assert.equal(at('Home', 900), callMid);
  assert.equal(at('End', 100), 1000);
  assert.equal(at('PageUp', 950), 1000, 'clamped at the end of the strip');
  assert.equal(at('PageDown', 50), 0, 'clamped at the start of the strip');
  for (const other of ['a', 'Enter', 'Escape', 'ArrowUp', 'Tab', ' ']) assert.equal(at(other, 500), null, `${other} is not a strip key`);
});

// ---- the component wires it ---------------------------------------------------------------------------------------------------
const src = fs.readFileSync(path.join(root, 'src/components/CallMinimap.svelte'), 'utf8').replace(/\r\n/g, '\n');
const body = (name) => {
  const start = src.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `function ${name} exists`);
  const next = src.indexOf('\n  function ', start + 1);
  return src.slice(start, next >= 0 ? next : src.indexOf('</script>', start));
};

test('onBoxKey previews at once and commits only through the debounce; Escape is never swallowed', () => {
  const keys = body('onBoxKey');
  assert.match(keys, /stripKeyTarget\(/);
  assert.match(keys, /onPreview\(/, 'the box moves at once');
  assert.match(keys, /keyCommit\.push\(/, 'the commit goes through the debounce');
  assert.doesNotMatch(keys, /onCommit\(/, 'no direct commit on a key');
  assert.match(keys, /if \(next === null\) return;/);
  assert.ok(keys.indexOf('if (next === null) return;') < keys.indexOf('preventDefault'), 'only strip keys are prevented, so Esc still reaches the window handler');
});

test('a pointer drag or a strip click drops a pending key commit; leaving the box and closing are handled', () => {
  assert.match(body('onBoxDown'), /keyCommit\.cancel\(\)/);
  assert.match(body('onStripClick'), /keyCommit\.cancel\(\)/);
  assert.match(src, /onblur=\{[^}]*keyCommit\.flush\(\)/);
  assert.match(src, /onDestroy\(\(\) => keyCommit\.cancel\(\)\)/);
  assert.match(src, /createKeyCommit<number>\(\(centreMs\) => onCommit\(centreMs\)\)/);
});
