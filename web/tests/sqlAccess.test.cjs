const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// The Blackbox access check in the SQL connection form: words, what counts as ready, when a script is shown.
const a = loadTypeScript('src/lib/sqlAccess.ts');
const { ACCESS_TEXT } = loadTypeScript('src/lib/copy/sqlAccessCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');

const rep = (extra = {}) => ({ reached: true, checks: [{ id: 'connect', state: 'passed', label: 'x', detail: 'y' }], ready: true, verdict: 'ready', writableDatabases: 0, ...extra });

test('ready needs the server to say ready and no failed line', () => {
  assert.equal(a.isReady(rep()), true);
  assert.equal(a.isReady(rep({ ready: false })), false);
  assert.equal(a.isReady(rep({ verdict: 'needs-grant' })), false);
  assert.equal(a.isReady(rep({ checks: [{ id: 'read', state: 'failed', label: 'x', detail: 'y' }] })), false);
});

test('a script is shown only for a verdict that needs one, and never invented', () => {
  assert.equal(a.scriptToShow(rep({ verdict: 'needs-grant', ready: false, script: 'GRANT X;' })), 'GRANT X;');
  assert.equal(a.scriptToShow(rep({ verdict: 'too-many-rights', ready: false, script: 'CREATE LOGIN y;' })), 'CREATE LOGIN y;');
  assert.equal(a.scriptToShow(rep({ script: 'GRANT X;' })), '', 'a ready login shows no script even if the server sent one');
  assert.equal(a.scriptToShow(rep({ verdict: 'unreadable', ready: false, script: 'GRANT X;' })), '');
  assert.equal(a.scriptToShow(rep({ verdict: 'needs-grant', ready: false })), '');
});

test('an unknown verdict or state never reads as ready or passed, and a lookalike never reads the prototype', () => {
  assert.equal(a.verdictLine(rep({ verdict: 'weird' })), ACCESS_TEXT.verdict.unreadable);
  assert.equal(a.verdictLine(rep({ verdict: 'constructor' })), ACCESS_TEXT.verdict.unreadable);
  assert.equal(a.stateWord('weird'), ACCESS_TEXT.state.skipped);
  assert.equal(a.stateWord('__proto__'), ACCESS_TEXT.state.skipped);
  assert.equal(a.stateWord('passed'), 'Passed');
});

test('the words pass the copy checks and name no cause', () => {
  const texts = [];
  const walk = (o, id) => { for (const [k, v] of Object.entries(o)) { if (typeof v === 'string') texts.push({ id: id + '.' + k, text: v }); else if (typeof v === 'object') walk(v, id + '.' + k); } };
  walk(ACCESS_TEXT, 'access');
  assert.deepEqual(lintCopy(texts).filter((f) => f.rule === 'causal' || f.rule === 'wording'), []);
});

test('the form calls the access route with the same request as the test and shows the result component', () => {
  const form = read('src/components/sources/SqlConnectionForm.svelte');
  assert.match(form, /api\.checkSqlAccess\(sqlTestInput\(form\)\)/);
  assert.match(form, /<SqlAccessResult report=\{access\} \/>/);
  const api = read('src/lib/api.ts');
  assert.match(api, /checkSqlAccess:[^\n]*'\/sql-connections\/access'/);
});

test('the result component renders text, never HTML, and the script sits in a read-only box', () => {
  const src = read('src/components/sources/SqlAccessResult.svelte');
  assert.ok(!/\{@html/.test(src));
  assert.match(src, /<textarea[^>]*readonly/);
});
