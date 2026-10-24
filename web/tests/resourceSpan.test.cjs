const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const s = loadTypeScript('src/lib/resourceSpan.ts');
const api = loadTypeScript('src/lib/api.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

// Synthetic values only: 2001 dates, web-a.example.test hosts.
const T0 = Date.parse('2001-02-03T04:00:00Z');
const HOUR = 3600000, YEAR = 365 * 24 * HOUR;
const HOST = 'web-a.example.test';
const NOW_VIEW = { fromMs: Date.parse('2026-10-25T10:00:00Z'), toMs: Date.parse('2026-10-25T10:15:00Z') };
const empty = (extra = {}) => ({ hosts: [{ host: HOST, summary: [], coverage: [{ metric: 'host.cpu', fraction: 0 }], processes: [] }], rules: [], markers: [], ...extra });
const withData = () => ({ hosts: [{ host: HOST, summary: ['CPU averaged 12%.'], coverage: [{ metric: 'host.cpu', fraction: 1 }], processes: [] }], rules: [], markers: [] });
const SPAN = { fromMs: T0, toMs: T0 + 2 * HOUR };
const ask = (over = {}) => s.spanNotice({ resource: empty(), settled: true, span: SPAN, view: NOW_VIEW, zone: 'UTC', ...over });

test('no data in the window and a span: the notice names the span and carries the window to show', () => {
  const notice = ask();
  assert.equal(notice.text, 'These files cover 3 Feb 2001 04:00:00 UTC to 3 Feb 2001 06:00:00 UTC.');
  assert.deepEqual(notice.window, { fromMs: SPAN.fromMs, toMs: SPAN.toMs });
});

test('data in the window: no notice, whatever the span', () => {
  assert.equal(ask({ resource: withData() }), null);
  // Any one measured thing counts: a rule row, a marker, a summary line or a coverage fraction above zero.
  assert.equal(ask({ resource: empty({ rules: [{ id: 'RC3' }] }) }), null);
  assert.equal(ask({ resource: empty({ markers: [{ kind: 'start' }] }) }), null);
  assert.equal(s.resourceHasData(empty()), false);
  assert.equal(s.resourceHasData({ ...empty(), hosts: [{ host: HOST, summary: [], coverage: [{ metric: 'host.cpu', fraction: 0.02 }], processes: [] }] }), true);
  assert.equal(s.resourceHasData({ hosts: [], rules: [], markers: [] }), false);
});

test('no span: no notice. Neither while the answer is still being read, unread, or failed', () => {
  assert.equal(ask({ span: null }), null);
  assert.equal(ask({ settled: false }), null);
  assert.equal(ask({ resource: null }), null);
  assert.equal(ask({ view: null }), null);
});

test('a window that already holds the whole period gets no button that would change nothing', () => {
  assert.equal(ask({ view: { fromMs: T0 - HOUR, toMs: T0 + 3 * HOUR } }), null);
  assert.equal(ask({ view: { fromMs: SPAN.fromMs, toMs: SPAN.toMs } }), null);
  assert.notEqual(ask({ view: { fromMs: T0 - HOUR, toMs: T0 + HOUR } }), null, 'a window holding only part of it still offers it');
});

test('caseSpan takes the earliest first and the latest last among kept imports', () => {
  const rec = (first, last, status = 'ok') => ({ status, quality: { coverageFirst: first, coverageLast: last } });
  assert.deepEqual(s.caseSpan([rec('2001-02-03T05:00:00Z', '2001-02-03T06:00:00Z'), rec('2001-02-03T04:00:00Z', '2001-02-03T05:30:00Z')]),
    { fromMs: T0, toMs: T0 + 2 * HOUR });
  assert.equal(s.caseSpan([]), null);
  assert.equal(s.caseSpan(null), null);
  assert.equal(s.caseSpan([{ status: 'ok', quality: {} }, { status: 'ok' }, { status: 'ok', quality: null }]), null, 'no times, no span');
  assert.equal(s.caseSpan([rec('not a time', '2001-02-03T05:00:00Z')]), null);
  assert.equal(s.caseSpan([rec('2001-02-03T06:00:00Z', '2001-02-03T05:00:00Z')]), null, 'last before first is not a span');
  assert.equal(s.caseSpan([rec('2001-02-03T04:00:00Z', '2001-02-03T05:00:00Z', 'quarantined'), rec('2001-02-03T04:00:00Z', '2001-02-03T05:00:00Z', 'duplicate')]), null, 'refused and duplicate imports add nothing');
  assert.deepEqual(s.caseSpan([{ quality: { coverageFirst: '2001-02-03T04:00:00Z', coverageLast: '2001-02-03T05:00:00Z' } }]), { fromMs: T0, toMs: T0 + HOUR }, 'a record with no status counts as kept');
});

test('a zero-width span reads as one time and the window around it is a minute wide', () => {
  const one = { fromMs: T0, toMs: T0 };
  const notice = ask({ span: one });
  assert.equal(notice.text, 'These files cover 3 Feb 2001 04:00:00 UTC.');
  assert.equal(notice.window.toMs - notice.window.fromMs, s.MIN_PERIOD_MS);
  assert.ok(notice.window.fromMs < T0 && notice.window.toMs > T0, 'the time sits inside the window');
  const narrow = s.periodWindow({ fromMs: T0, toMs: T0 + 5000 });
  assert.equal(narrow.toMs - narrow.fromMs, s.MIN_PERIOD_MS);
  assert.deepEqual(s.periodWindow({ fromMs: T0, toMs: T0 + 5 * 60000 }), { fromMs: T0, toMs: T0 + 5 * 60000 });
});

test('a span in 1970 or 2200 is shown, and the window stays under the 200 year limit the server accepts', () => {
  const old = { fromMs: 0, toMs: 3600000 };
  assert.equal(ask({ span: old }).text, 'These files cover 1 Jan 1970 00:00:00 UTC to 1 Jan 1970 01:00:00 UTC.');
  const late = { fromMs: Date.parse('2200-06-01T00:00:00Z'), toMs: Date.parse('2200-06-01T01:00:00Z') };
  assert.equal(ask({ span: late }).text, 'These files cover 1 Jun 2200 00:00:00 UTC to 1 Jun 2200 01:00:00 UTC.');
  assert.deepEqual(ask({ span: late }).window, late);
  const wide = { fromMs: 0, toMs: Date.parse('2200-01-01T00:00:00Z') };
  const window = s.periodWindow(wide);
  assert.equal(window.fromMs, 0);
  assert.ok(window.toMs - window.fromMs <= 200 * YEAR, 'under the server limit');
  assert.ok(window.toMs - window.fromMs >= 198 * YEAR, 'still almost all of it');
  assert.ok(Number.isFinite(Date.parse(new Date(window.toMs).toISOString())), 'both ends are valid RFC 3339 times');
  // The server limit is 200 * 365 days.
  assert.ok(s.MAX_PERIOD_MS < 200 * 365 * 24 * HOUR);
});

test('the notice follows the UTC and Local choice', () => {
  const utc = ask({ zone: 'UTC' }).text;
  const dubai = ask({ zone: 'Asia/Dubai' }).text;
  assert.match(utc, /04:00:00 UTC to .*06:00:00 UTC\.$/);
  assert.match(dubai, /3 Feb 2001 08:00:00 GMT\+4 to 3 Feb 2001 10:00:00 GMT\+4\.$/);
  assert.notEqual(utc, dubai);
});

test('the notice copy is plain: reading grade, glossary terms and no causal words', () => {
  const texts = [ask().text, ask({ span: { fromMs: T0, toMs: T0 } }).text, s.spanCopy.button, s.spanCopy.buttonLabel, s.spanCopy.shown, s.spanCopy.back];
  assert.deepEqual(lintScreenStrings(texts.map((text, i) => ({ id: 'span.' + i, text })), deck.terms), []);
  const causal = /\b(?:leak|leaks|starv\w*|saturat\w*|caus\w*|because|due to|waiting on|blocked by|bottleneck|root cause|culprit|responsible|result of)\b/i;
  for (const text of texts) assert.doesNotMatch(text, causal, text);
});

test('the panel shows the notice only through spanNotice and sets its own window, not the audit window', () => {
  const panel = read('src/components/ResourcePanel.svelte');
  assert.match(panel, /spanNotice\(/);
  assert.match(panel, /\{#if notice\}/);
  assert.match(panel, /spanCopy\.button/);
  assert.match(panel, /const shown = \$derived\([^\n]*notice != null[^\n]*period != null/, 'an empty answer lists no host when the files named none, so the notice keeps the panel on screen');
  assert.doesNotMatch(panel, /app\.query\s*=|setQuery|applyQuery/, 'the audit window is left alone');
  const tools = read('src/lib/components/CaseTools.svelte');
  assert.match(tools, /<ResourcePanel [^>]*\{span\}/);
  assert.match(read('src/lib/components/ImportPanel.svelte'), /caseSpan\(imports\)/);
});

test('the server bare validation 400 becomes a plain sentence; every other message is left as sent', () => {
  assert.equal(api.plainApiMessage(400, 'Request validation failed.'), api.VALIDATION_MESSAGE);
  assert.equal(api.plainApiMessage(400, ' Request validation failed. '), api.VALIDATION_MESSAGE);
  assert.equal(api.plainApiMessage(500, 'Request validation failed.'), 'Request validation failed.');
  assert.equal(api.plainApiMessage(400, 'The time range is too wide.'), 'The time range is too wide.');
  assert.doesNotMatch(api.VALIDATION_MESSAGE, /validation/i);
  assert.deepEqual(lintScreenStrings([{ id: 'api.validation', text: api.VALIDATION_MESSAGE }], deck.terms), []);
  const source = read('src/lib/api.ts');
  assert.equal((source.match(/plainApiMessage\(response\.status, message\)/g) ?? []).length, 2, 'both the JSON routes and the export route map it');
});
