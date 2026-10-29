const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

function loadTypeScript(relative) {
  const filename = path.join(__dirname, '..', relative);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    fileName: filename,
  }).outputText;
  const loaded = { exports: {} };
  new Function('exports', 'require', 'module', output)(loaded.exports, require, loaded);
  return loaded.exports;
}

const { createClientLog } = loadTypeScript('src/lib/clientLog.ts');

/** A sender with a manual clock: scheduled flushes run only when tick() is called. */
function harness({ token = 'session-token', send = () => true } = {}) {
  const scheduled = [];
  const sent = [];
  const log = createClientLog({
    token: () => token,
    send: (body, tok) => { sent.push({ body, tok }); return send(body, tok); },
    schedule: (fn) => { scheduled.push(fn); return scheduled.length; },
    cancel: () => {},
  });
  return {
    log, sent,
    tick() { const fns = scheduled.splice(0); for (const fn of fns) fn(); },
    entriesAt(i) { return JSON.parse(sent[i].body).entries; },
  };
}

test('entries are batched and flushed by the timer, with the session token', () => {
  const h = harness();
  h.log.report({ kind: 'nav', screen: 'cases' });
  h.log.report({ kind: 'action', screen: 'guided', action: 'run' });
  assert.equal(h.sent.length, 0, 'nothing is sent before the flush');
  h.tick();
  assert.equal(h.sent.length, 1);
  assert.equal(h.sent[0].tok, 'session-token');
  assert.deepEqual(h.entriesAt(0), [
    { kind: 'nav', screen: 'cases' },
    { kind: 'action', screen: 'guided', action: 'run' },
  ]);
  h.tick();
  assert.equal(h.sent.length, 1, 'an empty queue is not sent again');
});

test('a full batch flushes immediately, without waiting for the timer', () => {
  const h = harness();
  for (let i = 0; i < 10; i++) h.log.report({ kind: 'nav', screen: 's' + i });
  assert.equal(h.sent.length, 1);
  assert.equal(h.entriesAt(0).length, 10);
});

test('a throwing transport is silent and the batch is dropped, not retried', () => {
  const h = harness({ send: () => { throw new Error('offline'); } });
  h.log.report({ kind: 'error', screen: 'app', code: 'window_error', message: 'boom' });
  assert.doesNotThrow(() => h.log.flush());
  assert.equal(h.log.pending(), 0);
});

test('without a session the entries are dropped and nothing is sent', () => {
  const h = harness({ token: '' });
  h.log.report({ kind: 'nav', screen: 'cases' });
  h.log.report({ kind: 'nav', screen: 'start' });
  h.tick();
  assert.equal(h.sent.length, 0);
  assert.equal(h.log.pending(), 0, 'dropped entries do not pile up waiting for a session');
});

test('drop throws queued entries away without sending them', () => {
  const h = harness();
  h.log.report({ kind: 'nav', screen: 'cases' });
  h.log.drop();
  h.tick();
  assert.equal(h.sent.length, 0);
  assert.equal(h.log.pending(), 0);
});

test('messages are capped at 300 characters client-side too', () => {
  const h = harness();
  h.log.report({ kind: 'error', screen: 'app', message: 'x'.repeat(500) });
  h.log.flush();
  assert.equal(h.entriesAt(0)[0].message.length, 300);
});
