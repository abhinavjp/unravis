const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Source-side gap 3: the size line of an audit database, and the inline edits (labels, computer time zone) in the Sources list.

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
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const size = loadTypeScript('src/lib/sourceSize.ts');
const model = loadTypeScript('src/lib/sourcesList.ts');
const copy = loadTypeScript('src/lib/copy/sourcesListCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const NOW = Date.parse('2026-11-04T12:00:00Z');

test('hubfixE size: counts read in plain short form', () => {
  assert.equal(size.docsText(-1), '');
  assert.equal(size.docsText(Number.NaN), '');
  assert.equal(size.docsText(0), '0 docs');
  assert.equal(size.docsText(1), '1 doc');
  assert.equal(size.docsText(950), '950 docs');
  assert.equal(size.docsText(1400), '1.4 k docs');
  assert.equal(size.docsText(218_000), '218 k docs');
  assert.equal(size.docsText(1_400_000), '1.4 M docs');
});

test('hubfixE size: age reads as just now, minutes, hours or days; a future time is just now', () => {
  assert.equal(size.ageText('not a time', NOW), '');
  assert.equal(size.ageText('2026-11-04T11:59:30Z', NOW), 'newest just now');
  assert.equal(size.ageText('2026-11-04T12:30:00Z', NOW), 'newest just now');
  assert.equal(size.ageText('2026-11-04T11:15:00Z', NOW), 'newest 45 min ago');
  assert.equal(size.ageText('2026-11-04T09:00:00Z', NOW), 'newest 3 h ago');
  assert.equal(size.ageText('2026-11-01T12:00:00Z', NOW), 'newest 3 days ago');
});

test('hubfixE size: the line joins count and age and leaves out what is unknown', () => {
  assert.equal(size.sizeLine(null, NOW), '');
  assert.equal(size.sizeLine({ docs: 218_000, newestAt: '2026-11-04T09:00:00Z' }, NOW), '218 k docs · newest 3 h ago');
  assert.equal(size.sizeLine({ docs: -1, newestAt: '2026-11-04T09:00:00Z' }, NOW), 'newest 3 h ago');
  assert.equal(size.sizeLine({ docs: 5, newestAt: '' }, NOW), '5 docs');
  assert.equal(size.sizeLine({ docs: -1, newestAt: '' }, NOW), '');
});

test('hubfixE size: sources of one database are found by name and their stats add up', () => {
  const sources = [{ id: 'a', database: 'demo' }, { id: 'b', database: 'demo' }, { id: 'c', database: 'other' }];
  assert.deepEqual(size.auditSourcesOfDatabase(sources, 'demo'), ['a', 'b']);
  assert.deepEqual(size.auditSourcesOfDatabase(sources, 'none'), []);
  assert.equal(size.mergeStats([]), null);
  assert.deepEqual(size.mergeStats([{ docs: 10, newestAt: '2026-11-01T00:00:00Z' }, { docs: 5, newestAt: '2026-11-03T00:00:00Z' }]), { docs: 15, newestAt: '2026-11-03T00:00:00Z' });
  assert.equal(size.mergeStats([{ docs: 10, newestAt: '' }, { docs: -1, newestAt: '' }]).docs, -1);
});

test('hubfixE size: one failed collection read makes the database total unknown, never the good count alone', () => {
  const failed = { docs: -1, newestAt: '' }; // the shape readSizes maps a failed api.sourceStats to
  const merged = size.mergeStats([{ docs: 218_000, newestAt: '2026-11-04T09:00:00Z' }, failed]);
  assert.equal(merged.docs, -1, 'an unknown part makes the sum unknown');
  const line = size.sizeLine(merged, NOW);
  assert.ok(!line.includes('218'), 'the good collection count is not shown as the database total: ' + line);
  assert.equal(line, 'newest 3 h ago', 'only what is known is said');
  assert.equal(size.sizeLine(size.mergeStats([{ docs: 10, newestAt: '' }, failed]), NOW), '', 'fully unknown says nothing');
});

test('hubfixE list: a row carries what an edit needs, and a file with no tag gets one made from the file', () => {
  const rows = model.buildSourceList(
    [{ id: 7, importId: 0, kind: 'mongo-database', label: 'demo', env: 'Prod', portal: '', api: '', classification: 'audit' }],
    [{ id: 3, importer: 'iis-w3c', fileName: 'u_ex2.log', hostLabel: 'WEB01', tzRule: 'host-local', status: 'ok', rows: 5 }],
    [{ label: 'WEB01', tz: '' }]
  );
  const file = rows.find((row) => row.isFile);
  const db = rows.find((row) => !row.isFile);
  assert.deepEqual([file.tagId, file.importId, file.tagKind, file.database], [0, 3, 'iis-w3c', '']);
  assert.equal(file.tz.why, 'no-zone');
  assert.deepEqual([db.tagId, db.tagKind, db.tagLabel, db.database], [7, 'mongo-database', 'demo', 'demo']);
});

test('hubfixE api: the tag edit is a PUT to the one tag and the size line reads the stats route', () => {
  const tags = read('src/lib/caseTagsApi.ts');
  assert.match(tags, /casePath\(caseId, '\/sources\/' \+ tagId\), \{ method: 'PUT'/);
  const api = read('src/lib/api.ts');
  assert.match(api, /'\/sources\/' \+ encodeURIComponent\(sourceId\) \+ '\/stats'/);
});

test('hubfixE component: the list shows the size line and hands each row an editor; read only hides the editor', () => {
  const list = read('src/components/sources/SourcesList.svelte');
  assert.match(list, /data-testid="srclist-size"/);
  assert.match(list, /<SourceEdit \{caseId\} \{row\} \{envs\} \{readOnly\} \{onTag\} \/>/);
  assert.match(list, /api\.sourceStats\(id, signal\)\.catch\(\(\) => \(\{ docs: -1, newestAt: '' \}\)\)/, 'a failed collection read counts as unknown');
  assert.ok(!list.includes('.filter((part) => part !== null)'), 'a failed read is not dropped from the merge');
  const edit = read('src/components/sources/SourceEdit.svelte');
  assert.match(edit, /\{#if !readOnly\}/);
  assert.match(edit, /row\.tagId > 0\s*\? await putSourceTag\(caseId, row\.tagId, labels\)\s*: await postSourceTag\(/);
  assert.match(edit, /row\.tz\.why === 'host-gone' \? await saveHost\(row\.host, zone, false\) : await saveHostZone\(row\.host, zone\)/);
  assert.match(edit, /zoneNameProblem\(zone\)/);
});

test('hubfixE list: the size lines sit in a Map, so a database named "constructor" reads no inherited property', () => {
  const list = read('src/components/sources/SourcesList.svelte');
  assert.match(list, /\$state\.raw<ReadonlyMap<string, string>>\(new Map\(\)\)/);
  assert.match(list, /sizes = new Map\(sizes\)\.set\(database, line\)/);
  assert.match(list, /sizes\.get\(row\.database\)/);
  assert.ok(!list.includes('sizes[row.database]'), 'no plain-object indexing of sizes');
});

test('hubfixE edit: opening the label editor focuses the first field, closing it returns focus to the Edit button', () => {
  const edit = read('src/components/sources/SourceEdit.svelte');
  assert.match(edit, /import \{ tick \} from 'svelte'/);
  assert.match(edit, /bind:this=\{envInput\}/, 'the environment input is bound');
  assert.match(edit, /bind:this=\{openButton\}/, 'the Edit button is bound');
  assert.match(edit, /async function open\(\) \{[^}]*await tick\(\); envInput\?\.focus\(\);/, 'open focuses the environment input after render');
  assert.match(edit, /async function close\(\) \{[^}]*await tick\(\); openButton\?\.focus\(\);/, 'cancel returns focus to the Edit button after render');
  assert.match(edit, /noteBad = false;\s*await tick\(\);\s*openButton\?\.focus\(\);/, 'a successful save returns focus after render');
});

test('hubfixE edit: the environment field matches the server limit of 60 characters; portal and api keep 80', () => {
  const edit = read('src/components/sources/SourceEdit.svelte');
  const field = (name) => new RegExp('<input id="\\{uid\\}-' + name + '"[^>]*>').exec(edit);
  assert.match(field('env')[0], /maxlength="60"/);
  assert.match(field('portal')[0], /maxlength="80"/);
  assert.match(field('api')[0], /maxlength="80"/);
});

test('hubfixE copy: the new words are registered and pass the copy lint', () => {
  const entries = copy.sourcesListCopyEntries();
  for (const key of Object.keys(copy.SOURCES_LIST_TEXT)) assert.ok(entries.some((e) => e.id === 'srclist.' + key), 'not registered: ' + key);
  assert.ok(entries.some((e) => e.id === 'srclist.zone.shared'));
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  assert.equal(copy.zoneSharedText('WEB01'), 'This is the time zone of WEB01, so it applies to every file from it.');
});

// ---- gap 4: a hand-picked list takes the new file's source; the cap refusal is said in words ----
const picks = loadTypeScript('src/lib/tracing/sources.ts');
const chip = (id, kind = 'file') => ({ id, label: id, kind });

test('hubfixE picks: the ids of the file sources are formed as the server forms them', () => {
  const ids = picks.fileSourceIds(
    [{ id: 4, kind: 'iis-logs', importId: 9 }, { id: 5, kind: 'mongo-database', importId: 0 }, { id: 6, kind: 'sql-server', importId: 0 }],
    [{ id: 9, status: 'ok' }, { id: 10, status: 'ok' }, { id: 11, status: 'failed' }, { id: 3, status: '' }]
  );
  assert.deepEqual(ids, ['cs_4', 'cs_6', 'imp_3', 'imp_10']);
});

test('hubfixE picks: a hand-picked list switches on a new source; all and none stay as they are', () => {
  const some = { mode: 'some', ids: ['a'] };
  assert.deepEqual(picks.switchOnNew(some, ['imp_7', 'a']), { selection: { mode: 'some', ids: ['a', 'imp_7'] }, refused: false });
  assert.equal(picks.switchOnNew(some, []).selection, some);
  assert.equal(picks.switchOnNew(some, ['a']).selection, some);
  assert.deepEqual(picks.switchOnNew({ mode: 'all' }, ['imp_7']), { selection: { mode: 'all' }, refused: false });
  assert.deepEqual(picks.switchOnNew({ mode: 'none' }, ['imp_7']), { selection: { mode: 'none' }, refused: false });
});

test('hubfixE picks: past 50 nothing is added and the refusal is reported', () => {
  const ids = Array.from({ length: 50 }, (_, i) => 'x' + i);
  const full = { mode: 'some', ids };
  assert.deepEqual(picks.switchOnNew(full, ['imp_7']), { selection: full, refused: true });
  const chips = [...ids.map((id) => chip(id)), chip('y'), chip('z')];
  assert.deepEqual(picks.toggleOutcome(full, chips, 'y'), { selection: full, refused: true });
  assert.equal(picks.toggleSource(full, chips, 'y'), full);
  assert.deepEqual(picks.toggleOutcome(full, chips, 'nope'), { selection: full, refused: false });
  assert.equal(picks.toggleOutcome(full, chips, 'x3').refused, false);
});

test('hubfixE picks: the timeline reads the case after the drawer closes and says the cap in the region', () => {
  const paper = read('src/components/TracingPaper.svelte');
  assert.match(paper, /void readAfterAdd\(\)/);
  assert.match(paper, /switchOnNew\(selection, fileSourceIds\(tags, imports\)\.filter\(\(id\) => !known\.has\(id\)\)\)/);
  assert.match(paper, /data-testid="tp-source-cap"/);
  assert.match(paper, /out\.refused\) capNote = SOURCES_TEXT\.capNote/);
  assert.match(picks.SOURCES_TEXT.capNote, /50 sources/);
});
