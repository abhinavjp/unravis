const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Hub fix D, the logic side: the wizard's staged list outlives the files form and the add-source forms (F37), a card's kind is the person's own
// choice, the restored kinds (F35) map to no importer, the cards keep the wireframe's words, and the intent note behind the drawer's Add data cards.

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

const kinds = loadTypeScript('src/lib/fileKinds.ts');
const copy = loadTypeScript('src/lib/copy/fileKindsCopy.ts');
const files = loadTypeScript('src/lib/wizardFiles.svelte.ts');
const intent = loadTypeScript('src/lib/startIntent.ts');
const sourcesCopy = loadTypeScript('src/lib/copy/sourcesCopy.ts');

const fake = (name, size = 10) => ({ name, size, lastModified: 1 });

/* ------------------------------------------------------------------ F37: the staged list survives */

test('F37: the wizard store keeps rows, chosen kind and computer until it is cleared', () => {
  files.wizardStaging.clear();
  files.wizardStaging.append([fake('u_ex261020.log')], false);
  const row = files.wizardStaging.state.list[0];
  files.wizardStaging.setList([{ ...row, choice: 'iis-httperr', changed: true, host: 'WEB01' }]);
  // Switching add-source forms never touches the store: it is only read again.
  const again = files.wizardStaging.state.list;
  assert.equal(again.length, 1);
  assert.equal(again[0].choice, 'iis-httperr');
  assert.equal(again[0].host, 'WEB01');
  files.wizardStaging.clear();
  assert.equal(files.wizardStaging.state.list.length, 0);
});

test('F37: a reset while a read is in flight cancels it (the F14 guard follows the store)', () => {
  const staging = files.createStaging();
  const mine = staging.beginRead();
  assert.equal(staging.state.reading, true);
  assert.equal(staging.current(mine), true);
  staging.clear();
  assert.equal(staging.current(mine), false, 'the old read is stale after a clear');
  assert.equal(staging.state.reading, false);
  assert.equal(staging.endRead(mine), false, 'a stale end does not touch the count');
  const next = staging.beginRead();
  assert.equal(staging.current(next), true);
  assert.equal(staging.endRead(next), true);
  assert.equal(staging.state.reading, false);
});

test('F37: two stores are independent (the drawer has its own list, the wizard keeps its own)', () => {
  const a = files.createStaging();
  const b = files.createStaging();
  a.append([fake('one.log')], false);
  assert.equal(a.state.list.length, 1);
  assert.equal(b.state.list.length, 0);
});

test('F37: Remove and Clear still work on the lifted list', () => {
  const staging = files.createStaging();
  staging.append([fake('a.log'), fake('b.log'), fake('c.log')], false);
  staging.setList(staging.state.list.filter((row) => row.name !== 'b.log'));
  assert.deepEqual(staging.state.list.map((row) => row.name), ['a.log', 'c.log']);
  staging.clear();
  assert.equal(staging.state.list.length, 0);
});

/* ------------------------------------------------------------------ C: a card's kind is the person's choice */

test('C: a card sets the kind and the origin of the rows it adds', () => {
  const staging = files.createStaging();
  staging.append([fake('anything.log')], false, 'querystore', 'sql-querystore');
  const row = staging.state.list[0];
  assert.equal(row.choice, 'sql-querystore');
  assert.equal(row.origin, 'querystore');
  assert.equal(row.changed, true, 'the name did not suggest it, so the server is told');
});

test('C: a card kind that the name already suggests is not sent as a change', () => {
  const staging = files.createStaging();
  staging.append([fake('u_ex261020.log')], false, 'iis', 'iis-w3c');
  const row = staging.state.list[0];
  assert.equal(row.choice, 'iis-w3c');
  assert.equal(row.changed, row.suggested !== 'iis-w3c');
});

test('C: rows added with no card have no origin', () => {
  const staging = files.createStaging();
  staging.append([fake('x.log')], false);
  assert.ok(!staging.state.list[0].origin);
});

test('C: the four cards keep the wireframe words', () => {
  const byId = Object.fromEntries(copy.FILE_CARDS.map((card) => [card.id, card]));
  assert.deepEqual(copy.FILE_CARDS.map((card) => card.button), ['Add IIS logs', 'Add app logs', 'Add export', 'Add audit export']);
  assert.equal(byId.iis.title, 'IIS logs (W3C)');
  assert.match(byId.iis.text, /^Gives time-taken per request\. Used for O in Step 3\./);
  assert.equal(byId.iis.chip, 'needs time-taken field');
  assert.equal(byId.app.text, 'Errors and warnings near onset. Used by Step 5 only.');
  assert.equal(byId.querystore.text, 'For SQL you cannot connect to. A CSV from the sys.query_store views.');
  assert.match(byId.audit.text, /^For a Mongo you cannot connect to\./);
  assert.equal(copy.CARDS_TEXT.footer, 'No step needs application logs or a Query Store export to start. IIS logs and audit exports feed the main steps (2 to 4).');
});

test('C: every card input has its own name', () => {
  const names = copy.FILE_CARDS.map((card) => copy.cardInputLabel(card));
  assert.equal(new Set(names).size, names.length);
  assert.ok(names.every((name) => /^Choose .+ files$/.test(name)));
});

test('C: counts and group headings read plainly', () => {
  assert.equal(copy.cardCountText(0), 'No files chosen yet');
  assert.equal(copy.cardCountText(1), '1 file chosen');
  assert.equal(copy.cardCountText(3), '3 files chosen');
  assert.equal(copy.cardGroupHeading('Query Store export', 2), 'Query Store export: 2 files');
});

/* ------------------------------------------------------------------ F35: the restored kinds */

test('F35: the three restored kinds exist, are labelled and send no importer', () => {
  assert.deepEqual([...kinds.EXTRA_KIND_IDS], ['hangfire-csv', 'redis-slowlog-csv', 'file']);
  assert.equal(copy.fileKindLabel('hangfire-csv'), 'Hangfire jobs (CSV export)');
  assert.equal(copy.fileKindLabel('redis-slowlog-csv'), 'Redis slow log (CSV export)');
  assert.equal(copy.fileKindLabel('file'), 'Another file or export');
  for (const id of kinds.EXTRA_KIND_IDS) {
    assert.equal(kinds.importerFor(id), '', id + ' is read by the server auto-detect');
    assert.equal(kinds.isPanelKind(id), true);
    assert.equal(kinds.isExtraKind(id), true);
  }
  assert.equal(kinds.isPanelKind(''), false, 'the blank choice (Do not use) is not a kind');
  assert.equal(kinds.FILE_KIND_IDS.length, 8, 'the eight real kinds are unchanged');
});

test('F35: a restored kind needs no computer and has no time-zone rule', () => {
  for (const id of kinds.EXTRA_KIND_IDS) {
    assert.equal(kinds.hostNeed(id), 'none');
    assert.equal(kinds.timeZoneRule(id), null);
  }
});

test('F35: the help text does not promise what no importer reads', () => {
  assert.match(copy.EXTRA_KIND_READS['hangfire-csv'], /set aside/);
  assert.doesNotMatch(Object.values(copy.EXTRA_KIND_READS).join(' '), /because/i);
});

test('F35: a restored kind is usable and can be put back to Do not use', () => {
  const staging = files.createStaging();
  staging.append([fake('jobs.csv')], false, '', 'hangfire-csv');
  const row = staging.state.list[0];
  assert.equal(kinds.usableFiles([row]).length, 1);
  assert.equal(kinds.usableFiles([{ ...row, choice: '' }]).length, 0);
});

/* ------------------------------------------------------------------ D: the drawer's entry */

test('D: a plain open leaves no files note, a files request leaves one and it is taken once', () => {
  assert.equal(intent.takeFilesViewIntent(), false);
  intent.requestFilesView();
  assert.equal(intent.takeFilesViewIntent(), true);
  assert.equal(intent.takeFilesViewIntent(), false);
});

test('D: the drawer lead is the wireframe sentence', () => {
  assert.equal(sourcesCopy.ADD_TEXT.drawerLead, 'Add a source after the case was created. Same cards as at creation. New data is imported separately and does not touch what is there.');
  assert.equal(sourcesCopy.ADD_TEXT.drawerTitle('k7m2qx4a'), 'Add data, case k7m2qx4a');
  assert.equal(sourcesCopy.ADD_TEXT.busy, 'An import is already running. Stop it first.');
});

test('D: the staged line and the leave text count files', () => {
  assert.equal(sourcesCopy.ADD_TEXT.staged(1), '1 file staged, not imported');
  assert.equal(sourcesCopy.ADD_TEXT.staged(6), '6 files staged, not imported');
  assert.equal(sourcesCopy.ADD_TEXT.leaveText(6), '6 files and unsaved fields will be lost.');
});

test('D: the five cards keep the wireframe words', () => {
  const t = sourcesCopy.ADD_TEXT;
  assert.deepEqual([t.sqlTitle, t.mongoTitle, t.blackboxTitle, t.filesTitle, t.dayTitle], ['SQL Server', 'MongoDB', 'Blackbox', 'Files', 'Another day']);
  assert.deepEqual([t.addSql, t.addMongo, t.addBlackbox, t.addFiles, t.addDay], ['Add SQL source', 'Add Mongo source', 'Add a Blackbox', 'Add files', 'Add another day']);
});

test('D: the saved-connection line has its plural back and passes the copy lint', () => {
  assert.equal(sourcesCopy.ADD_TEXT.connReady(0), 'No saved connection yet');
  assert.equal(sourcesCopy.ADD_TEXT.connReady(1), 'Saved connection ready');
  assert.equal(sourcesCopy.ADD_TEXT.connReady(3), '3 saved connections ready');
  assert.equal(sourcesCopy.ADD_TEXT.held(2), '2 found here', 'held is unchanged');
  const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
  const { deck } = loadTypeScript('src/lib/copy/deck.ts');
  assert.deepEqual(lintScreenStrings([{ id: 'add.connReady', text: sourcesCopy.ADD_TEXT.connReady(3) }], deck.terms), []);
});
