const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The tracing window picker rules (WP-22a): defaults, refusals, overlap warning and the overlap fix.

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
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const windows = loadTypeScript('src/lib/tracing/windows.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const fields = (incidentStart, incidentEnd, referenceStart, referenceEnd) => ({ incidentStart, incidentEnd, referenceStart, referenceEnd });

test('the defaults are the last hour for the incident and the hour before for the comparison', () => {
  const f = windows.defaultWindows(Date.UTC(2026, 0, 15, 10, 30, 45, 999));
  assert.deepEqual(f, fields('2026-01-15T09:30', '2026-01-15T10:30', '2026-01-15T08:30', '2026-01-15T09:30'));
});

test('exactly 30 days is allowed and the body holds both windows as ISO strings', () => {
  const check = windows.checkWindows(fields('2026-01-01T00:00', '2026-01-31T00:00', '2025-12-01T00:00', '2025-12-02T00:00'));
  assert.deepEqual(check.errors, []);
  assert.deepEqual(check.body, {
    incident: { start: '2026-01-01T00:00:00.000Z', end: '2026-01-31T00:00:00.000Z' },
    reference: { start: '2025-12-01T00:00:00.000Z', end: '2025-12-02T00:00:00.000Z' }
  });
});

test('30 days plus one minute is refused with the exact sentence and no body', () => {
  const check = windows.checkWindows(fields('2026-01-01T00:00', '2026-01-31T00:01', '2025-12-01T00:00', '2025-12-02T00:00'));
  assert.deepEqual(check.errors, ['The longest window is 30 days. Shorten the incident window.']);
  assert.equal(check.body, null);
});

test('an empty value, a day that does not exist and reversed times are refused without throwing', () => {
  assert.doesNotThrow(() => windows.checkWindows(fields('', '', '', '')));
  const empty = windows.checkWindows(fields('', '2026-01-15T11:00', '2026-01-15T08:00', '2026-01-15T09:00'));
  assert.deepEqual(empty.errors, ['The incident window needs a start and an end, in UTC.']);
  assert.equal(empty.body, null);
  const badDay = windows.checkWindows(fields('2026-02-31T10:00', '2026-03-01T10:00', '2026-01-15T08:00', '2026-01-15T09:00'));
  assert.deepEqual(badDay.errors, ['The incident window needs a start and an end, in UTC.']);
  const reversed = windows.checkWindows(fields('2026-01-15T11:00', '2026-01-15T10:00', '2026-01-15T09:00', '2026-01-15T08:00'));
  assert.deepEqual(reversed.errors, [
    'The incident window end must be later than the start.',
    'The comparison period end must be later than the start.'
  ]);
});

test('equal start and end is refused with the same plain sentence, in the pickers own words', () => {
  const equal = windows.checkWindows(fields('2026-01-15T10:00', '2026-01-15T10:00', '2026-01-15T08:00', '2026-01-15T09:00'));
  assert.deepEqual(equal.errors, ['The incident window end must be later than the start.']);
  assert.doesNotMatch(equal.errors.join(' '), /reference/i);
});

test('check names the inputs each refusal is about, so the screen can mark them invalid', () => {
  const ok = windows.checkWindows(fields('2026-01-15T10:00', '2026-01-15T11:00', '2026-01-15T08:00', '2026-01-15T09:00'));
  assert.deepEqual(ok.invalid, []);
  const missing = windows.checkWindows(fields('2026-01-15T10:00', '2026-01-15T11:00', '', '2026-01-15T09:00'));
  assert.deepEqual(missing.invalid, ['referenceStart']);
  assert.deepEqual(missing.errors, ['The comparison period needs a start and an end, in UTC.']);
  const reversed = windows.checkWindows(fields('2026-01-15T11:00', '2026-01-15T10:00', '2026-01-15T08:00', '2026-01-15T09:00'));
  assert.deepEqual(reversed.invalid, ['incidentStart', 'incidentEnd']);
  const long = windows.checkWindows(fields('2026-01-01T00:00', '2026-01-31T00:01', '2026-01-15T08:00', '2026-01-15T09:00'));
  assert.deepEqual(long.invalid, ['incidentStart', 'incidentEnd']);
});

test('windowNow uses the demo reference time in demo mode and the real clock otherwise', () => {
  const real = Date.UTC(2026, 10, 3, 22, 0);
  assert.equal(windows.windowNow({ demo: true, now: '2026-10-20T09:00:00Z' }, real), Date.UTC(2026, 9, 20, 9, 0));
  assert.equal(windows.windowNow({ demo: false, now: '2026-10-20T09:00:00Z' }, real), real);
  assert.equal(windows.windowNow({ demo: true, now: 'not a time' }, real), real);
  assert.equal(windows.windowNow(null, real), real);
  assert.equal(windows.windowNow(undefined, real), real);
});

test('windowIsEmpty is true only when a read lane has no events and no lane has any', () => {
  const lane = (state, counts) => ({ state, buckets: counts.map((count) => ({ count })) });
  assert.equal(windows.windowIsEmpty([lane('events', [0, 0]), lane('events', [])]), true);
  assert.equal(windows.windowIsEmpty([lane('events', [0]), lane('events', [3])]), false);
  assert.equal(windows.windowIsEmpty([lane('no-data', [0]), lane('none-seen', [])]), false, 'lanes that explain themselves need no extra hint');
  assert.equal(windows.windowIsEmpty([lane('incomplete', [0]), lane('no-data', [])]), true);
  assert.equal(windows.windowIsEmpty([]), false);
});

test('overlapping windows warn but are not an error, and touching windows do not overlap', () => {
  const overlap = windows.checkWindows(fields('2026-01-15T10:00', '2026-01-15T11:00', '2026-01-15T10:30', '2026-01-15T11:30'));
  assert.equal(overlap.overlap, true);
  assert.deepEqual(overlap.errors, []);
  assert.notEqual(overlap.body, null);
  const touching = windows.checkWindows(fields('2026-01-15T10:00', '2026-01-15T11:00', '2026-01-15T09:00', '2026-01-15T10:00'));
  assert.equal(touching.overlap, false);
});

test('fixOverlap keeps the incident and moves the comparison to an equal length ending at the incident start', () => {
  const fixed = windows.fixOverlap(fields('2026-01-15T10:00', '2026-01-15T11:00', '2026-01-15T10:30', '2026-01-15T12:00'));
  assert.equal(fixed.incidentStart, '2026-01-15T10:00');
  assert.equal(fixed.incidentEnd, '2026-01-15T11:00');
  assert.equal(fixed.referenceStart, '2026-01-15T09:00');
  assert.equal(fixed.referenceEnd, '2026-01-15T10:00');
  const check = windows.checkWindows(fixed);
  assert.equal(check.overlap, false);
  assert.deepEqual(check.errors, []);
});

test('the window picker copy passes the screen-text rules', () => {
  assert.deepEqual(lintScreenStrings(windows.windowCopyEntries(), deck.terms), []);
});

test('the window module never imports from the guided tour', () => {
  assert.doesNotMatch(read('src/lib/tracing/windows.ts'), /from '.*guided/);
});

const paperSource = fs.readFileSync(path.join(root, 'src/components/TracingPaper.svelte'), 'utf8');

test('TracingPaper fills the windows region from windows.ts and sends only the checked body', () => {
  assert.match(paperSource, /from '\.\.\/lib\/tracing\/windows'/);
  assert.match(paperSource, /defaultWindows\(windowNow\(app\.info, Date\.now\(\)\)\)/);
  assert.match(paperSource, /checkWindows\(fields\)/);
  assert.match(paperSource, /fetchTracing\(caseId, body, nextRequestSeq\(\), mine\.signal\)/);
  assert.equal((paperSource.match(/type="datetime-local"/g) || []).length, 4);
  assert.match(paperSource, /role="alert"/);
  assert.match(paperSource, /WINDOW_TEXT\.overlapFix/);
  assert.match(paperSource, /fixOverlap\(fields\)/);
  assert.equal((paperSource.match(/aria-invalid=\{invalidIf\(/g) || []).length, 4, 'every picker input can be marked invalid');
  assert.equal((paperSource.match(/aria-describedby=\{describedBy\}/g) || []).length, 4, 'every picker input points at the error list');
  assert.match(paperSource, /id="tp-problems"/);
  assert.match(paperSource, /WINDOW_TEXT\.keptPrevious/);
  assert.match(paperSource, /WINDOW_TEXT\.endNote/);
  assert.doesNotMatch(paperSource, /initialWindows|floorToHour/);
  assert.doesNotMatch(paperSource, /\{@html|guided/);
});

test('TracingPaper starts exactly one request on open and one per Start tracing', () => {
  assert.equal((paperSource.match(/fetchTracing\(/g) || []).length, 1);
  assert.match(paperSource, /onMount\(\(\) => \{ void run\(\); \}\)/);
});
