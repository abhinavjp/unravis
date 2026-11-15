const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// FIX-35 / TEST-04 (c): holding an arrow key on the overview strip commits once, after the keys stop.
// The REAL `onBoxKey` is cut out of CallMinimap.svelte (a component cannot be mounted under node), stripped of types and run
// with the real keyCommit and callMap modules, node's fake timers, and spies for onPreview / onCommit. Each key press moves the
// box (the spy moves the centre the way the parent does), so a held key walks the box step by step like in the browser.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const output = ts.transpileModule(fs.readFileSync(filename, 'utf8'), {
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

const { createKeyCommit, stripKeyTarget, KEY_COMMIT_DELAY_MS } = loadTypeScript('src/lib/drill/keyCommit.ts');
const { centreFromFraction, fractionOf, KEY_STEP_FRACTION } = loadTypeScript('src/lib/drill/callMap.ts');

const svelte = fs.readFileSync(path.join(root, 'src/components/CallMinimap.svelte'), 'utf8').replace(/\r\n/g, '\n');
function onBoxKeySource() {
  const start = svelte.indexOf('  function onBoxKey(');
  assert.ok(start >= 0, 'onBoxKey is in CallMinimap.svelte');
  const end = svelte.indexOf('\n  }\n', start);
  assert.ok(end > start, 'onBoxKey body found');
  return svelte.slice(start, end + 4);
}

const OVERVIEW = { start: 0, end: 120 * 60000 }; // a two hour strip
const CALL = { startMs: 60 * 60000, endMs: 61 * 60000 };

// Builds a strip with the given onBoxKey source. Returns the handler and what it did.
function strip(fnSource) {
  const js = ts.transpileModule(fnSource, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const log = { previews: [], commits: [], prevented: 0 };
  let centre = fractionOf(OVERVIEW, CALL.startMs);
  const onPreview = (ms) => { log.previews.push(ms); centre = fractionOf(OVERVIEW, ms); };
  const onCommit = (ms) => { log.commits.push(ms); };
  const keyCommit = createKeyCommit((ms) => onCommit(ms));
  // onBoxKey reads the box centre as a free variable (a Svelte derived value): point it at a getter that follows the preview.
  const factory = new Function(
    'stripKeyTarget', 'centreFromFraction', 'overview', 'call', 'state', 'onPreview', 'onCommit', 'keyCommit',
    `${js.replace(/\bcentre\b/g, 'state.centre')}
     return onBoxKey;`
  );
  const state = { get centre() { return centre; } };
  const onBoxKey = factory(stripKeyTarget, centreFromFraction, OVERVIEW, CALL, state, onPreview, onCommit, keyCommit);
  const press = (key) => {
    const e = { key, preventDefault() { log.prevented += 1; } };
    onBoxKey(e);
  };
  return { press, log, keyCommit };
}

test('holding ArrowRight (30 repeats, 33 ms apart) previews every step but commits once, 250 ms after the last key', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { press, log, keyCommit } = strip(onBoxKeySource());
  for (let i = 0; i < 30; i++) {
    press('ArrowRight');
    t.mock.timers.tick(33);
  }
  assert.equal(log.previews.length, 30, 'every key moves the box at once');
  assert.equal(log.prevented, 30, 'every strip key is prevented (the page does not scroll)');
  assert.deepEqual(log.commits, [], 'no read starts while the key is held');
  assert.equal(keyCommit.pending(), true);
  t.mock.timers.tick(KEY_COMMIT_DELAY_MS - 33 - 1);
  assert.deepEqual(log.commits, [], 'still waiting just before the delay ends');
  t.mock.timers.tick(1);
  assert.equal(log.commits.length, 1, 'exactly one commit');
  assert.equal(log.commits[0], log.previews[29], 'the commit is where the box ended');
  assert.ok(log.previews.every((ms, i) => i === 0 || ms >= log.previews[i - 1]), 'the box only moved forward');
  t.mock.timers.tick(10000);
  assert.equal(log.commits.length, 1, 'and no second commit');
});

test('the held key stops at the end of the strip: the box does not run past it and still commits once', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { press, log } = strip(onBoxKeySource());
  const steps = Math.ceil(1 / KEY_STEP_FRACTION) + 10; // far more presses than the strip is long
  for (let i = 0; i < steps; i++) { press('ArrowRight'); t.mock.timers.tick(20); }
  t.mock.timers.tick(KEY_COMMIT_DELAY_MS);
  assert.ok(log.previews.every((ms) => ms <= OVERVIEW.end), 'never past the strip end');
  assert.equal(log.commits.length, 1);
  assert.equal(log.commits[0], OVERVIEW.end);
});

test('two bursts with a pause longer than the delay commit twice, each with its own last position', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { press, log } = strip(onBoxKeySource());
  for (let i = 0; i < 3; i++) { press('ArrowRight'); t.mock.timers.tick(30); }
  t.mock.timers.tick(KEY_COMMIT_DELAY_MS);
  for (let i = 0; i < 4; i++) { press('ArrowLeft'); t.mock.timers.tick(30); }
  t.mock.timers.tick(KEY_COMMIT_DELAY_MS);
  assert.equal(log.commits.length, 2);
  assert.equal(log.commits[0], log.previews[2]);
  assert.equal(log.commits[1], log.previews[6]);
});

test('a key that is not a strip key (Escape, Tab, a letter) neither previews, prevents nor commits', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { press, log, keyCommit } = strip(onBoxKeySource());
  for (const key of ['Escape', 'Tab', 'a', 'Enter', ' ']) press(key);
  t.mock.timers.tick(1000);
  assert.equal(log.previews.length, 0);
  assert.equal(log.prevented, 0, 'Escape in particular still reaches the window handler');
  assert.deepEqual(log.commits, []);
  assert.equal(keyCommit.pending(), false);
});

test('a drag or a click (keyCommit.cancel) drops the held key commit', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { press, log, keyCommit } = strip(onBoxKeySource());
  press('ArrowRight');
  press('ArrowRight');
  keyCommit.cancel();
  t.mock.timers.tick(1000);
  assert.deepEqual(log.commits, []);
});

// ---- Mutation check: prove the debounce test can fail. ----
test('mutation: committing straight from the key (no debounce) gives one commit per key, so the tests above would fail', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const real = onBoxKeySource();
  assert.ok(real.includes('keyCommit.push(next);'), 'the debounced commit is still in onBoxKey (update this test if it was reshaped)');
  const { press, log } = strip(real.replace('keyCommit.push(next);', 'onCommit(next);'));
  for (let i = 0; i < 30; i++) { press('ArrowRight'); t.mock.timers.tick(33); }
  t.mock.timers.tick(1000);
  assert.equal(log.commits.length, 30, 'without the debounce every held key repeat would start the full read set');
});
