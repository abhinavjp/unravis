const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// "Collect SQL data" on the Blackbox page: which connections can run unattended, how the profile is written, and what the screen never does.
const m = loadTypeScript('src/lib/blackboxSql.ts');
const { SQLC_TEXT } = loadTypeScript('src/lib/copy/blackboxSqlCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');
const conn = (extra) => ({ id: 'a', label: 'x', server: 's', host: 's', port: 1433, auth: 'sql', username: 'u', database: '', encrypt: 'mandatory', trustServerCertificate: false, hasPassword: true, savedAt: '', ...extra });

test('only a SQL login with a kept password can run unattended', () => {
  const got = m.loginChoices([conn({ id: '1' }), conn({ id: '2', hasPassword: false }), conn({ id: '3', auth: 'windows' })]);
  assert.deepEqual(got.map((c) => c.id), ['1']);
});

test('an unknown or missing profile reads as standard', () => {
  assert.equal(m.profileOf(undefined), 'standard');
  assert.equal(m.profileOf({ sql: { profile: 'weird' } }), 'standard');
  assert.equal(m.profileOf({ sql: { profile: 'deep' } }), 'deep');
});

test('choosing a profile keeps the other settings and standard leaves no block', () => {
  const base = { name: 'n', port: 1, sql: { profile: 'deep' } };
  assert.equal(m.withProfile(base, 'standard').sql, undefined);
  assert.equal(m.withProfile(base, 'standard').name, 'n');
  assert.deepEqual(m.withProfile({ name: 'n' }, 'everything').sql, { profile: 'everything' });
  assert.deepEqual(m.withProfile({ sql: { extras: ['waits'] } }, 'standard').sql, { profile: 'standard', extras: ['waits'] });
});

test('the words pass the copy checks', () => {
  const texts = [];
  const walk = (o, id) => { for (const [k, v] of Object.entries(o)) { if (typeof v === 'string') texts.push({ id: id + '.' + k, text: v }); else if (typeof v === 'function') texts.push({ id: id + '.' + k, text: String(v({ installed: true, started: true })) + ' ' + String(v(1000)) }); else if (typeof v === 'object') walk(v, id + '.' + k); } };
  walk(SQLC_TEXT, 'sqlc');
  assert.deepEqual(lintCopy(texts).filter((f) => f.rule === 'causal' || f.rule === 'wording'), []);
});

test('the screen never asks for a password and never shows the script as HTML', () => {
  const src = read('src/components/BlackboxSqlCollect.svelte');
  assert.ok(!/type="password"/.test(src));
  assert.ok(!/\{@html/.test(src));
  assert.match(src, /<textarea[^>]*readonly/);
  assert.match(src, /blackboxApi\.sqlSetup\(\{ mode, savedId: mode === 'login' \? savedId : undefined, enable \}\)/);
});

test('the Blackbox page shows it only for the recorder on this PC', () => {
  const src = read('src/components/BlackboxPanel.svelte');
  assert.match(src, /\{#if item\.thisPc && thisPc\.installed\}\{#key item\.id\}<SqlCollect \{thisPc\} \/>/);
  const api = read('src/lib/blackbox.ts');
  assert.match(api, /sqlSetup:[^\n]*'\/this-pc\/sql\/setup'/);
  assert.match(api, /sqlClearLogin:[^\n]*'\/this-pc\/sql\/login\/clear'/);
});

test('query text exists only for the deep profile with the flag, and any other choice drops it', () => {
  const deepText = { sql: { profile: 'deep', queryText: true, queryTextMaxChars: 2000 } };
  assert.equal(m.keepsQueryText(deepText), true);
  assert.equal(m.keepsQueryText({ sql: { profile: 'everything', queryText: true } }), false);
  assert.equal(m.keepsQueryText({ sql: { profile: 'deep' } }), false);
  assert.equal(m.queryTextSizeOf(deepText), 2000);
  assert.equal(m.queryTextSizeOf({ sql: { queryTextMaxChars: 7 } }), 1000, 'an unknown size reads as the default');
  assert.deepEqual(m.withProfile({ name: 'n' }, 'deep', { on: true, size: 500 }).sql, { profile: 'deep', queryText: true, queryTextMaxChars: 500 });
  assert.deepEqual(m.withProfile({ name: 'n' }, 'deep').sql, { profile: 'deep' }, 'text is off unless asked for');
  assert.equal(m.withProfile(deepText, 'standard').sql, undefined, 'leaving deep drops the flag and the size');
  assert.deepEqual(m.withProfile(deepText, 'everything').sql, { profile: 'everything' });
  assert.deepEqual(m.withProfile({ sql: { profile: 'deep', extras: ['waits'], queryText: true } }, 'everything', { on: true, size: 500 }).sql, { profile: 'everything', extras: ['waits'] }, 'text never rides along outside deep');
});

test('the status line comes from the service manager only', () => {
  assert.equal(m.sqlStatusLine({ installed: false }), '');
  assert.equal(m.sqlStatusLine({ installed: true }), SQLC_TEXT.statusNotInstalled);
  assert.equal(m.sqlStatusLine({ installed: true, sqlInstalled: true }), SQLC_TEXT.statusStopped);
  assert.equal(m.sqlStatusLine({ installed: true, sqlInstalled: true, sqlRunning: true }), SQLC_TEXT.statusRunning);
});

test('the query text switch carries a warning and shows only for deep', () => {
  const src = read('src/components/BlackboxSqlCollect.svelte');
  assert.match(src, /\{#if profile === 'deep'\}\s*<label class="opt"><input type="checkbox" bind:checked=\{textOn\}/);
  assert.match(src, /aria-describedby="\{uid\}-text-warn"/);
  assert.match(SQLC_TEXT.queryTextWarn, /password/);
});
