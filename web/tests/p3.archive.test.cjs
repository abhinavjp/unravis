// Phase P3 (archive, check, free, restore): the page side. The server side is tested in Go (internal/server, internal/casestore).
// These tests pin what the page sends, what it shows and what a clumsy person cannot do.
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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const model = load('src/lib/archiveModel.ts');
const copy = load('src/lib/copy/archiveCopy.ts');
const api = load('src/lib/archiveApi.ts');
const cases = load('src/lib/cases.ts');
const life = load('src/lib/caseLifecycle.ts');
const story = load('src/lib/story.ts');
const start = load('src/lib/startScreen.ts');
const { lintScreenStrings } = load('src/lib/copy/copylint.ts');
const { deck } = load('src/lib/copy/deck.ts');

const TOKEN = 'link-session-token-0123456789abcdef';
const CASE_ID = 'abcdefgh' + 'a'.repeat(18);

async function withServer(responses, fn) {
  const names = ['sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const calls = [];
  const items = new Map([['unravis.session', TOKEN]]);
  const values = {
    sessionStorage: { getItem: (k) => (items.has(k) ? items.get(k) : null), setItem: (k, v) => { items.set(k, String(v)); }, removeItem: (k) => { items.delete(k); } },
    fetch: async (url, init = {}) => {
      calls.push({ url, method: init.method || 'GET', headers: new Headers(init.headers), body: init.body });
      const next = responses.shift() ?? { status: 200, body: {} };
      if (next.network) throw new TypeError('Failed to fetch');
      return { ok: next.status >= 200 && next.status < 300, status: next.status, json: async () => next.body, headers: { get: () => null } };
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try { return await fn({ calls, items }); } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

const FILE = { name: 'q3-' + CASE_ID + '.unravis-case', folder: 'D:\\Unravis archive', bytes: 1200, keyKind: 'master', keyEnding: '7f3a', made: '2026-10-12T17:44:55Z', counts: { imports: 9, sources: 4 } };

/* ------------------------------------------------------------------ model */

test('a job is read only when it has a known state; a bad answer is not a job', () => {
  assert.equal(model.parseFileJob(null, 'archive'), null);
  assert.equal(model.parseFileJob({ state: 'exploding' }, 'archive'), null);
  const job = model.parseFileJob({ state: 'running', phase: 'write', percent: 250, verified: 'yes' }, 'archive');
  assert.equal(job.percent, 100);
  assert.equal(job.verified, false, 'only a real true counts as verified');
  assert.equal(model.jobRunning(job), true);
  assert.equal(model.jobRunning({ state: 'idle' }), false);
  assert.equal(model.parseFileJob({ state: 'running', percent: -5 }, 'verify').percent, 0);
});

test('the free code is the first 8 characters of the case id, case and spaces do not matter, nothing else counts', () => {
  assert.equal(model.freeConfirmed(CASE_ID.slice(0, 8), CASE_ID), true);
  assert.equal(model.freeConfirmed('  ' + CASE_ID.slice(0, 8).toUpperCase() + ' ', CASE_ID), true);
  assert.equal(model.freeConfirmed(CASE_ID.slice(0, 7), CASE_ID), false);
  assert.equal(model.freeConfirmed(CASE_ID, CASE_ID), false);
  assert.equal(model.freeConfirmed('', CASE_ID), false);
  assert.equal(model.freeConfirmed('abcdefgh', 'not-an-id'), false, 'a bad id never confirms');
});

test('a typed passphrase needs 12 characters as a person counts them', () => {
  assert.equal(model.passphraseOk('x'.repeat(11)), false);
  assert.equal(model.passphraseOk('x'.repeat(12)), true);
  assert.equal(model.passphraseOk('\u{1F600}'.repeat(12)), true);
  assert.equal(model.passphraseOk('\u{1F600}'.repeat(11)), false);
  assert.equal(model.needsTypedPassphrase('none'), true);
  assert.equal(model.needsTypedPassphrase('unlocked'), false);
  assert.equal(model.needsTypedPassphrase('locked'), false);
});

test('archived actions: restore in place only while the working copy is kept, erase always', () => {
  const kept = model.archivedActions({ state: 'archived', archive: { freed: false, current: FILE } });
  assert.deepEqual({ ...kept }, { restoreInPlace: true, restoreAsNew: true, free: true, erase: true });
  const freed = model.archivedActions({ state: 'archived', archive: { freed: true, current: FILE } });
  assert.deepEqual({ ...freed }, { restoreInPlace: false, restoreAsNew: true, free: false, erase: true });
  const missing = model.archivedActions({ state: 'archived', missing: true, archive: { freed: false, current: FILE } });
  assert.deepEqual({ ...missing }, { restoreInPlace: false, restoreAsNew: true, free: false, erase: true });
  const noFile = model.archivedActions({ state: 'archived', missing: true, archive: { freed: false, current: null } });
  assert.equal(noFile.restoreAsNew, false);
  assert.equal(noFile.erase, true);
  const open = model.archivedActions({ state: 'open' });
  assert.deepEqual({ ...open }, { restoreInPlace: false, restoreAsNew: false, free: false, erase: true });
});

test('chips: a freed case is never missing, a missing one is never freed', () => {
  assert.deepEqual([...model.archivedChips({ archive: { freed: false, current: FILE } })], ['archived', 'kept', 'key']);
  assert.deepEqual([...model.archivedChips({ archive: { freed: true, current: FILE } })], ['archived', 'freed', 'key']);
  assert.deepEqual([...model.archivedChips({ missing: true, archive: { freed: false, current: null } })], ['archived', 'missing']);
});

test('orphan files: a case sees its own and the ones with no case', () => {
  const report = model.parseOrphans({ files: [{ name: 'a.unravis-case', caseId: CASE_ID }, { name: 'b.unravis-case', caseId: 'z'.repeat(26) }, { name: 'c.unravis-case' }, { nope: 1 }], verifyCleanupPending: true });
  assert.equal(report.files.length, 3);
  assert.deepEqual(model.orphansFor(report, CASE_ID).map((f) => f.name), ['a.unravis-case', 'c.unravis-case']);
  assert.equal(report.verifyCleanupPending, true);
  assert.deepEqual({ ...model.parseOrphans(undefined) }, { files: [], verifyCleanupPending: false });
});

test('a case row carries the archive facts and a percent that stays between 0 and 100', () => {
  const info = cases.toCase({ id: CASE_ID, title: 't', created: '2026-10-01T00:00:00Z', state: 'archiving', bytes: 1, imports: 2, percent: 400, filesMade: 2, missing: true, archive: { active: true, at: '2026-10-02T00:00:00Z', freed: false, current: FILE, files: [FILE], restores: [] } });
  assert.equal(info.percent, 100);
  assert.equal(info.filesMade, 2);
  assert.equal(info.missing, true);
  assert.equal(info.archive.current.keyEnding, '7f3a');
  const plain = cases.toCase({ id: CASE_ID, title: 't', created: '', state: 'open', bytes: 0, imports: 0, filesMade: 0 });
  assert.equal(plain.filesMade, undefined, 'no files made shows nothing');
});

/* ------------------------------------------------------------------ rules */

test('Archive is offered on an open or closed case, never on an untrusted, missing, archiving or archived one', () => {
  assert.equal(life.archiveButton({ state: 'open', closed: null }), 'enabled');
  assert.equal(life.archiveButton({ state: 'closed', closed: '2026-10-01T00:00:00Z' }), 'enabled');
  assert.equal(life.archiveButton({ state: 'open', untrusted: true }), 'hidden');
  assert.equal(life.archiveButton({ state: 'open', missing: true }), 'hidden');
  assert.equal(life.archiveButton({ state: 'archiving' }), 'hidden');
  assert.equal(life.archiveButton({ state: 'archived' }), 'hidden');
});

/* ------------------------------------------------------------------ calls */

test('archive sends the believed state, the folder and no passphrase unless one was typed', async () => {
  await withServer([{ status: 202, body: { state: 'running', phase: 'write', percent: 0 } }, { status: 202, body: { state: 'running' } }], async ({ calls }) => {
    const job = await api.startArchive(CASE_ID, 'open', 'D:\\Unravis archive');
    assert.equal(job.state, 'running');
    await api.startArchive(CASE_ID, 'closed', 'D:\\a', 'a long typed passphrase');
    assert.match(calls[0].url, new RegExp('/cases/' + CASE_ID + '/archive$'));
    assert.equal(calls[0].method, 'POST');
    assert.equal(calls[0].headers.get('X-Expect-State'), 'open');
    assert.deepEqual(JSON.parse(calls[0].body), { path: 'D:\\Unravis archive' });
    assert.deepEqual(JSON.parse(calls[1].body), { path: 'D:\\a', passphrase: 'a long typed passphrase' });
    assert.equal(calls[1].headers.get('X-Expect-State'), 'closed');
    for (const call of calls) assert.ok(!/passphrase/.test(call.url), 'a passphrase is never in a URL');
  });
});

test('free sends the typed code trimmed and lower case; an unreadable job answer is an error, not a job', async () => {
  await withServer([{ status: 200, body: {} }, { status: 200, body: { nonsense: true } }], async ({ calls }) => {
    await api.freeCase(CASE_ID, 'archived', '  ABCDEFGH ');
    assert.deepEqual(JSON.parse(calls[0].body), { confirm: 'abcdefgh' });
    assert.equal(calls[0].headers.get('X-Expect-State'), 'archived');
    await assert.rejects(() => api.getVerify(CASE_ID), (error) => error.reason === 'unreadable');
  });
});

test('restore as new sends a passphrase only when given, restore in place sends none', async () => {
  const body = { id: 'n'.repeat(26), title: 'New', created: '2026-10-20T00:00:00Z', state: 'closed', bytes: 1, imports: 1 };
  await withServer([{ status: 201, body }, { status: 201, body }, { status: 200, body: {} }], async ({ calls }) => {
    const made = await api.restoreAsNew(CASE_ID, 'archived');
    assert.equal(made.id, 'n'.repeat(26));
    await api.restoreAsNew(CASE_ID, 'archived', 'typed passphrase 1');
    await api.restoreInPlace(CASE_ID, 'archived');
    assert.deepEqual(JSON.parse(calls[0].body), {});
    assert.deepEqual(JSON.parse(calls[1].body), { passphrase: 'typed passphrase 1' });
    assert.equal(calls[2].body, '{}');
    assert.match(calls[2].url, /restore-in-place$/);
  });
});

test('the archived list asks for its own page; the working list carries the total', async () => {
  const row = { id: CASE_ID, title: 'A', created: '', state: 'archived', bytes: 1, imports: 1 };
  await withServer([{ status: 200, body: { cases: [row], archivedTotal: 120, offset: 0, pageSize: 50 } }, { status: 200, body: { cases: [row, { id: 'bad' }], archivedTotal: 120, offset: 50, pageSize: 50 } }], async ({ calls }) => {
    const working = await api.listCasesPage();
    assert.equal(working.archivedTotal, 120);
    assert.equal(calls[0].url.endsWith('/cases'), true);
    const page = await api.listArchivedCases(50.9);
    assert.match(calls[1].url, /\/cases\?archived=1&offset=50$/);
    assert.equal(page.cases.length, 1, 'a row with a bad id is dropped');
    assert.equal(page.pageSize, 50);
  });
  await withServer([{ status: 200, body: { cases: [], archivedTotal: -3, offset: 'x' } }], async () => {
    const page = await api.listCasesPage();
    assert.equal(page.archivedTotal, 0);
    assert.equal(page.offset, 0);
    assert.equal(page.pageSize, 50);
  });
});

test('a negative offset is sent as zero', async () => {
  await withServer([{ status: 200, body: { cases: [] } }], async ({ calls }) => {
    await api.listArchivedCases(-20);
    assert.match(calls[0].url, /offset=0$/);
  });
});

/* ------------------------------------------------------------------ words */

test('every fixed archive sentence passes the copy lint', () => {
  const findings = lintScreenStrings(copy.archiveLintEntries(), deck.terms).map((f) => f.id + ': ' + f.message);
  assert.deepEqual(findings, []);
});

test('a reason the page does not know gets a generic sentence, never the server words', () => {
  assert.equal(copy.archiveReasonText('some-new-thing', 500), copy.ARCHIVE_TEXT.generic);
  assert.equal(copy.archiveReasonText('some-new-thing', 0), copy.ARCHIVE_TEXT.unreachable);
  assert.equal(copy.archiveReasonText('x', 423), copy.ARCHIVE_TEXT.locked);
  assert.equal(copy.archiveReasonText('case-freeing'), copy.ARCHIVE_TEXT.m19);
  assert.equal(copy.archiveReasonText('busy'), copy.ARCHIVE_TEXT.a18);
  assert.equal(copy.jobFailureText('archive', 'check-failed'), copy.ARCHIVE_TEXT.m10);
  assert.equal(copy.jobFailureText('verify', 'check-failed'), copy.ARCHIVE_TEXT.m11);
  assert.equal(copy.jobFailureText('archive', 'stopped'), copy.ARCHIVE_TEXT.m14);
  assert.equal(copy.jobFailureText('verify', 'stopped'), copy.ARCHIVE_TEXT.m17);
});

test('the archived notice names the file, the folder and the key ending, and no key', () => {
  const text = copy.archivedNotice('a.unravis-case', 'D:\\Unravis archive', '7f3a');
  assert.match(text, /a\.unravis-case/);
  assert.match(text, /Key ending 7f3a/);
  assert.equal(copy.keyEndingText(''), '');
  assert.ok(!/Key ending/.test(copy.archivedNotice('a.unravis-case', '', '')));
});

/* ------------------------------------------------------------------ story and list */

test('the case story tells archived, restored and freed in fixed words, with the key ending of the file', () => {
  const info = cases.toCase({ id: CASE_ID, title: 'T', created: '2026-10-01T00:00:00Z', state: 'closed', bytes: 1, imports: 1, archive: { active: false, at: '', freed: false, current: FILE, files: [FILE], restores: [] } });
  const events = story.buildStory({
    info, imports: [], health: [], theories: [],
    history: [
      { at: '2026-10-02T00:00:00Z', kind: 'archived', detail: FILE.name },
      { at: '2026-10-03T00:00:00Z', kind: 'freed' },
      { at: '2026-10-04T00:00:00Z', kind: 'restored' },
      { at: '2026-10-05T00:00:00Z', kind: 'restored-from-case', detail: 'abcdefgh' },
      { at: '2026-10-06T00:00:00Z', kind: 'mystery' }
    ]
  });
  const titles = events.map((e) => e.title);
  assert.ok(titles.includes('Case archived'));
  assert.ok(titles.includes('Space freed'));
  assert.ok(titles.includes('Case restored in place'));
  assert.equal(events.some((e) => e.id.includes('mystery')), false, 'an unknown kind is not shown');
  const archived = events.find((e) => e.title === 'Case archived');
  assert.match(archived.detail, /Key ending 7f3a/);
  assert.ok(!archived.detail.includes(FILE.folder), 'the folder is not in the story');
});

test('the start list shows an archiving row with a percent and no Open, and a missing row with a note', () => {
  const rows = start.startView('unlocked', [
    { id: 'a'.repeat(26), title: 'A', created: '', imports: 1, state: 'archiving', percent: 42 },
    { id: 'b'.repeat(26), title: 'B', created: '', imports: 1, state: 'archived', missing: true },
    { id: 'c'.repeat(26), title: 'C', created: '', imports: 1, state: 'open' }
  ]).rows;
  assert.equal(rows[0].archiving, true);
  assert.equal(rows[0].archivingLabel, 'Archiving 42%');
  assert.equal(rows[1].missing, true);
  assert.equal(rows[1].missingLabel, 'Working copy missing');
  assert.equal(rows[2].archiving, false);
  assert.equal(rows[2].missing, false);
});

/* ------------------------------------------------------------------ the screens: what a clumsy person cannot do */

test('the free drawer cannot skip the check and cannot be sent with a wrong code', () => {
  const src = read('src/lib/components/FreeDrawer.svelte');
  assert.match(src, /disabled=\{freeing \|\| !codeOk\}/, 'the free button is off until the code is right');
  assert.match(src, /if \(freeing \|\| !codeOk \|\| freed\) return;/, 'a second press or a wrong code does nothing');
  assert.match(src, /error\.reason === 'check-first'/, 'a check that no longer counts sends the person back to stage 1');
  assert.match(src, /stage = \$derived\(freed \? 3 : verified \? 2 : 1\)/);
  assert.match(src, /passphrase = '';/, 'the passphrase is cleared');
});

test('the archive drawer clears the passphrase and stops polling when it closes', () => {
  const src = read('src/lib/components/ArchiveDrawer.svelte');
  assert.match(src, /passphrase = '';/);
  assert.match(src, /clearTimeout\(timer\)/);
  assert.match(src, /alive = false/);
  assert.ok(!/localStorage|sessionStorage/.test(src), 'nothing typed is stored');
});

test('no archive screen puts a key, a passphrase or a raw server error on the page', () => {
  for (const name of ['ArchiveDrawer', 'FreeDrawer', 'RestoreDrawer', 'ArchivedList', 'OrphanFiles']) {
    const src = read('src/lib/components/' + name + '.svelte');
    assert.ok(!/\{error\.message\}|\{job\.error\}|\{e\.message\}/.test(src), name + ' shows no raw error text');
    assert.ok(!/console\.(log|info|debug)/.test(src), name + ' logs nothing');
  }
});

test('the archived list pages by what it has already shown and keeps Erase behind a typed code', () => {
  const src = read('src/lib/components/ArchivedList.svelte');
  assert.match(src, /rows\.length/);
  assert.match(src, /eraseConfirmed/);
  assert.match(src, /archivedShownText/);
});

test('the start screen offers the archived toggle only when there are archived cases', () => {
  const src = read('src/components/StartScreen.svelte');
  assert.match(src, /archivedTotal > 0/);
  assert.match(src, /archivedToggleText\(archivedTotal\)/);
  assert.match(src, /<ArchivedList/);
});

test('the archive drawer clears its error line when the folder or the passphrase is edited (D-A-10)', () => {
  const src = read('src/lib/components/ArchiveDrawer.svelte');
  assert.match(src, /<input id="archive-folder" type="text" bind:value=\{folder\} oninput=\{\(\) => \{ problem = ''; \}\}/);
  assert.match(src, /<input id="archive-pass" type="password" bind:value=\{passphrase\} oninput=\{\(\) => \{ problem = ''; \}\}/);
  assert.match(src, /async function submit\(event: SubmitEvent\) \{[\s\S]*?starting = true;\s*problem = '';/);
});
