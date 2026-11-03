// Clumsy-user pass on the case export panel (CaseTools.svelte): a stale preview must say so, a preview and a timeline must not
// cancel each other, and a save that finishes after the panel is gone must not download anything.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const loaded = new Map();
function load(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const output = ts.transpileModule(fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
  const nodeRequire = createRequire(filename);
  const req = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return load(path.relative(root, plain));
      if (fs.existsSync(store)) return load(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(req, module, module.exports, (value) => value);
  return module.exports;
}

const cases = load('src/lib/cases.ts');
const copy = load('src/lib/copy/importCopy.ts');
const { lintScreenStrings } = load('src/lib/copy/copylint.ts');
const source = fs.readFileSync(path.join(root, 'src/lib/components/CaseTools.svelte'), 'utf8').replace(/\r\n/g, '\n');
const CASE_ID = 'a'.repeat(26);

async function withServer(responses, fn) {
  const names = ['sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const items = new Map([['unravis.session', 'link-session-token-0123456789abcdef']]);
  const values = {
    sessionStorage: { getItem: (k) => (items.has(k) ? items.get(k) : null), setItem: (k, v) => { items.set(k, String(v)); }, removeItem: (k) => { items.delete(k); } },
    fetch: async () => {
      const next = responses.shift() ?? { status: 200, body: {} };
      return { ok: next.status >= 200 && next.status < 300, status: next.status, json: async () => next.body, headers: { get: () => null } };
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try { return await fn(); } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('clumsy export: a stale preview (409 on confirm) gets its own words, not the generic refusal', async () => {
  const err = await withServer([{ status: 409, body: { error: 'the preview changed; run the preview again' } }], async () => {
    try { await cases.confirmCaseExport('p-1', CASE_ID); } catch (e) { return e; }
    return null;
  });
  assert.ok(err instanceof cases.CaseApiError && err.status === 409);
  assert.equal(copy.exportErrorText(err), copy.IMPORT_TEXT.exportChanged);
  assert.match(copy.IMPORT_TEXT.exportChanged, /preview/i);
  assert.notEqual(copy.exportErrorText(err), copy.errorText(err), 'the generic text hid the reason');
});

test('clumsy export: other refusals keep their usual text', () => {
  const make = (status, reason) => new cases.CaseApiError('m', status, reason);
  assert.equal(copy.exportErrorText(make(500, '')), copy.errorText(make(500, '')));
  assert.equal(copy.exportErrorText(make(0, 'unreachable')), copy.errorText(make(0, 'unreachable')));
  assert.equal(copy.exportErrorText(make(404, 'not-found')), copy.errorText(make(404, 'not-found')));
  const dup = new cases.CaseApiError('m', 409, 'duplicate');
  assert.equal(copy.exportErrorText(dup), copy.errorText(dup), 'a duplicate 409 is not a stale preview');
});

test('clumsy export: the stale-preview sentence passes the copy lint', () => {
  const problems = lintScreenStrings([{ id: 'export.changed', text: copy.IMPORT_TEXT.exportChanged }]);
  assert.deepEqual(problems, []);
});

test('clumsy export: the panel shows the stale-preview sentence on save and on preview failures', () => {
  assert.match(source, /failure\(error, exportErrorText\)/);
  assert.equal((source.match(/failure\(error, exportErrorText\)/g) ?? []).length, 2);
});

test('clumsy export: a timeline and an export preview do not share one abort controller', () => {
  const timeline = source.slice(source.indexOf('async function buildTimeline'), source.indexOf('async function buildPreview'));
  assert.match(timeline, /timelineController/);
  assert.doesNotMatch(timeline.replace(/timelineController/g, ''), /\bcontroller\b/, 'buildTimeline must not touch the export controller');
  assert.match(source, /timelineController\?\.abort\(\)/, 'unmount aborts the timeline too');
});

test('clumsy export: save is abortable and downloads nothing once aborted', () => {
  const save = source.slice(source.indexOf('async function save'), source.indexOf('</script>'));
  assert.match(save, /confirmCaseExport\(shown\.previewId, caseId, mine\.signal\)/);
  const guard = save.indexOf('if (mine.signal.aborted) return;');
  assert.ok(guard > 0, 'an abort check after the awaits');
  assert.ok(guard < save.indexOf('download(file.content'), 'the check comes before the first download');
  assert.match(save, /AbortError/);
});
