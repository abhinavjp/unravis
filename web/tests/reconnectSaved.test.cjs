const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Saved databases reconnect by themselves: the client reads which ones could not be reopened, and the words it shows for them.

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

const model = loadTypeScript('src/lib/tracing/model.ts');
const sources = loadTypeScript('src/lib/tracing/sources.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const window = { start: '2026-10-01T00:00:00Z', end: '2026-10-12T00:00:00Z', axisStart: '2026-10-01T00:00:00Z', axisEnd: '2026-10-12T00:00:00Z', bucketSeconds: 3600 };
const answer = (extra) => ({ window, lanes: [], sources: [], auditSources: [], savedNotConnected: [], ...extra });

test('reconnectFailed is read, trimmed to usable entries and capped', () => {
  const many = Array.from({ length: 12 }, (_, i) => ({ name: 'DB' + i, reason: 'the database could not be reached' }));
  const r = model.parseTracing(answer({ reconnectFailed: [{ name: 'Payroll', reason: 'no password is saved' }, { name: '', reason: 'x' }, { name: 'Y' }, 7, ...many] }));
  assert.deepEqual(r.reconnectFailed.slice(0, 2), [
    { name: 'Payroll', reason: 'no password is saved', action: 'connect' },
    { name: 'DB0', reason: 'the database could not be reached', action: 'connect' }
  ]);
  assert.equal(r.reconnectFailed.length, 8);
});

test('the action the server names is kept, and an unknown one becomes Connect', () => {
  const r = model.parseTracing(answer({ reconnectFailed: [
    { name: 'A', reason: 'x', action: 'start-keeper' },
    { name: 'B', reason: 'x', action: 'remove-source' },
    { name: 'C', reason: 'x', action: 'retry' },
    { name: 'D', reason: 'x', action: 'launch-rockets' },
    { name: 'E', reason: 'x', action: 7 }
  ] }));
  assert.deepEqual(r.reconnectFailed.map((f) => f.action), ['start-keeper', 'remove-source', 'retry', 'connect', 'connect']);
});

test('an answer without failures has no reconnectFailed key', () => {
  assert.equal('reconnectFailed' in model.parseTracing(answer({})), false);
  assert.equal('reconnectFailed' in model.parseTracing(answer({ reconnectFailed: 'nope' })), false);
  assert.equal('reconnectFailed' in model.parseTracing(answer({ reconnectFailed: [] })), false);
});

test('the failure sentence names the database, the reason and what to do, with one full stop', () => {
  assert.equal(sources.reconnectFailedText('Payroll', 'the password was not accepted'), 'Could not reconnect Payroll: the password was not accepted. Press Connect and enter the password.');
  assert.equal(sources.reconnectFailedText('Payroll', 'the password was not accepted. '), 'Could not reconnect Payroll: the password was not accepted. Press Connect and enter the password.');
});

test('the next step follows the action, never the reason text', () => {
  const same = 'the Unravis Key Keeper service is not running';
  assert.equal(sources.reconnectFailedText('Payroll', same, 'start-keeper'), 'Could not reconnect Payroll: the Unravis Key Keeper service is not running. Start the Unravis Key Keeper service, then press Start tracing again.');
  assert.equal(sources.reconnectFailedText('Payroll', same, 'connect'), 'Could not reconnect Payroll: ' + same + '. Press Connect and enter the password.');
  assert.match(sources.reconnectFailedText('Payroll', 'the most sources are already open', 'remove-source'), /Remove a source, then press Start tracing again\.$/);
  assert.match(sources.reconnectFailedText('Payroll', 'there was not enough time to try', 'retry'), /Press Start tracing again\.$/);
});

test('the reconnecting line shows only while a saved name has no open audit source', () => {
  const chip = (label) => ({ id: 'src_' + label, label, kind: 'audit' });
  assert.deepEqual(sources.savedStillClosed([], [], []), []);
  assert.deepEqual(sources.savedStillClosed(['Payroll', 'Orders'], [chip('payroll')], []), ['Orders']);
  assert.deepEqual(sources.savedStillClosed(['Payroll'], [], [{ sourceId: 'x', label: ' PAYROLL ' }]), []);
  assert.deepEqual(sources.savedStillClosed(['Payroll'], [{ id: 'f', label: 'payroll', kind: 'file' }], []), ['Payroll']);
});

test('the new copy is in the lint list, passes it, and the Connect tooltip explains the automatic reconnect', () => {
  const entries = sources.sourcesCopyEntries();
  for (const id of ['tracingSources.reconnecting', 'tracingSources.reconnectingTitle', 'tracingSources.reconnectFailed', 'tracingSources.reconnectKeeper', 'tracingSources.reconnectRemove', 'tracingSources.reconnectRetry', 'tracingSources.connectTitle']) {
    assert.ok(entries.some((e) => e.id === id), id);
  }
  assert.equal(sources.SOURCES_TEXT.reconnecting, 'Reconnecting saved databases…');
  assert.match(sources.SOURCES_TEXT.connectTitle, /password is saved/);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('the screen shows the reconnecting line while reading and one note per failure', () => {
  const paper = fs.readFileSync(path.join(root, 'src/components/TracingPaper.svelte'), 'utf8');
  assert.match(paper, /SOURCES_TEXT\.reconnecting\b/);
  assert.match(paper, /reconnectFailedText\(failed\.name, failed\.reason, failed\.action\)/);
  assert.match(paper, /\{#if closedSaved\.length > 0\}/);
  assert.doesNotMatch(paper, /\{#if universe\.saved\.length > 0\}<p[^>]*reconnecting/);
});
