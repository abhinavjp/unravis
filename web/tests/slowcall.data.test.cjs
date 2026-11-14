const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the helper that reads imported SQL Server and HTTP.sys events for the call's window.

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

// A session token from the link, so the shared sender has one to attach. Set before the modules load.
globalThis.location = { hash: '#t=unit-test-token', pathname: '/', search: '' };
globalThis.history = { state: null, replaceState() {} };

const SOURCE_FILE = 'src/lib/drill/callData.ts';
const { EVENT_SOURCES, EVENT_LIMIT, eventsPath, fetchCallEvents } = loadTypeScript(SOURCE_FILE);
const CASE_ID = 'abcdefghijklmnopqrstuvwxyz'.slice(0, 26).replace(/[01389]/g, 'a');
const FROM = '2026-11-12T10:00:00Z';
const TO = '2026-11-12T10:05:00Z';

test('constants name the three event sources and the 500 cap', () => {
  assert.deepEqual([...EVENT_SOURCES], ['sql-xe', 'sql-errorlog', 'iis-httperr']);
  assert.equal(EVENT_LIMIT, 500);
});

test('eventsPath asks for the window, the three sources and the cap, and never the sensitive extras', () => {
  const p = eventsPath(CASE_ID, FROM, TO);
  assert.ok(p.includes('/events?'), p);
  assert.ok(p.includes('from=' + encodeURIComponent(FROM)), p);
  assert.ok(p.includes('to=' + encodeURIComponent(TO)), p);
  assert.ok(p.includes('source=sql-xe%2Csql-errorlog%2Ciis-httperr'), p);
  assert.ok(p.includes('limit=500'), p);
  assert.ok(!p.includes('includeSensitive'), p);
  assert.ok(p.startsWith('/cases/' + CASE_ID + '/events?'), p);
});

test('eventsPath refuses a value that is not a case id', () => {
  assert.throws(() => eventsPath('abc', FROM, TO));
});

test('the source file does not copy the record text properties', () => {
  const source = fs.readFileSync(path.join(root, SOURCE_FILE), 'utf8');
  assert.equal(/\.(line|attrs)\b/.test(source), false);
  assert.equal(/\br\.(attrs|value|unit|line)\b/.test(source), false);
});

function stubFetch(body) {
  const calls = [];
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init });
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
  };
  return calls;
}

test('fetchCallEvents keeps only the screen fields and reports truncation', async () => {
  const calls = stubFetch({
    events: [{
      evidenceId: 'e1', source: 'sql-xe', kind: 'statement', host: 'h1', start: '2026-11-12T10:01:00Z', end: '2026-11-12T10:01:02Z',
      durationMs: 2000, value: 5, unit: 'ms', severity: 'info', line: 42, tzRule: 'utc', attrs: { text: 'secret record text' }
    }],
    limit: 500, truncated: true
  });
  const out = await fetchCallEvents(CASE_ID, FROM, TO);
  assert.equal(calls.length, 1);
  assert.ok(calls[0].url.includes('/events?'));
  assert.equal(calls[0].init.method, 'GET');
  assert.equal(out.truncated, true);
  assert.equal(out.events.length, 1);
  assert.deepEqual(Object.keys(out.events[0]).sort(), ['durationMs', 'end', 'evidenceId', 'host', 'kind', 'severity', 'source', 'start']);
  assert.equal(JSON.stringify(out).includes('secret record text'), false);
  assert.equal(out.events[0].durationMs, 2000);
});

test('a missing duration stays missing, never 0', async () => {
  stubFetch({ events: [{ evidenceId: 'e2', source: 'iis-httperr', kind: 'refused', start: '2026-11-12T10:02:00Z', durationMs: null }], truncated: false });
  const out = await fetchCallEvents(CASE_ID, FROM, TO);
  assert.equal(out.events[0].durationMs, null);
  assert.equal(out.truncated, false);
});

test('a reply without an events list gives an empty answer', async () => {
  stubFetch({ nothing: true });
  assert.deepEqual(await fetchCallEvents(CASE_ID, FROM, TO), { events: [], truncated: false });
  stubFetch({ events: 'no' });
  assert.deepEqual(await fetchCallEvents(CASE_ID, FROM, TO), { events: [], truncated: false });
});
