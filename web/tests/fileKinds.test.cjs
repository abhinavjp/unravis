const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The file-kind catalog of the "Import files" panel (gaps G1, G2): names, the guess from a file name, time-zone rules, the list rows, and the copy.

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

const kinds = loadTypeScript('src/lib/fileKinds.ts');
const copy = loadTypeScript('src/lib/copy/fileKindsCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const fakeFile = (name, size = 100, relative = '') => ({ name, size, webkitRelativePath: relative });

test('the catalog uses the server importer names, and app-log is the one that has no importer name', () => {
  assert.deepEqual([...kinds.FILE_KIND_IDS], ['iis-w3c', 'iis-httperr', 'iis-freb', 'sql-xe', 'sql-errorlog', 'sql-querystore', 'win-events', 'app-log']);
  for (const id of kinds.FILE_KIND_IDS) {
    assert.equal(kinds.isCatalogKind(id), true);
    assert.equal(kinds.importerFor(id), id === 'app-log' ? '' : id);
    assert.ok(copy.fileKindLabel(id).length > 0, id);
    assert.ok(copy.readsText(id).length > 0, id);
  }
  assert.equal(kinds.isCatalogKind('hangfire-csv'), false);
  assert.equal(kinds.importerFor('nonsense'), '');
  assert.equal(kinds.importerFor(''), '');
  assert.equal(copy.fileKindLabel('constructor'), '');
  assert.equal(copy.readsText('__proto__'), '');
});

const TABLE = [
  ['u_ex261020.log', 'iis-w3c'],
  ['U_EX261020.LOG', 'iis-w3c'],
  ['u_ex261020_siteB.log', 'iis-w3c'],
  ['u_extend1.log', 'iis-w3c'],
  ['W3SVC1_2026-10-20.log', 'iis-w3c'],
  ['iis-site.txt', 'iis-w3c'],
  ['httperr1.log', 'iis-httperr'],
  ['HTTPERR12.LOG', 'iis-httperr'],
  ['httperr.log', 'iis-httperr'],
  ['fr000123.xml', 'iis-freb'],
  ['FR12.XML', 'iis-freb'],
  ['freb-trace.xml', 'iis-freb'],
  ['orders_xe_2026-10-20.xel.csv', 'sql-xe'],
  ['system_health_0_1.xel', 'sql-xe'],
  ['Extended Events 2026.csv', 'sql-xe'],
  ['sql_errorlog_2026-10-20.txt', 'sql-errorlog'],
  ['ERRORLOG', 'sql-errorlog'],
  ['ERRORLOG.1', 'sql-errorlog'],
  ['querystore.csv', 'sql-querystore'],
  ['Query_Store export.csv', 'sql-querystore'],
  ['Application.evtx', 'win-events'],
  ['eventlog-app.csv', 'win-events'],
  ['C:\\inetpub\\logs\\LogFiles\\W3SVC1\\u_ex261020.log', 'iis-w3c'],
  ['logs/HTTPERR/httperr1.log', 'iis-httperr'],
  ['  u_ex261020.log  ', 'iis-w3c'],
  // not recognised
  ['notes.txt', ''],
  ['readme', ''],
  ['', ''],
  ['.log', ''],
  ['orders.csv', ''],
  ['x.xml', ''],
  ['axe.csv', ''],
  // compressed files are never guessed
  ['logs-october.zip', ''],
  ['u_ex261020.log.gz', ''],
  ['LOGS.ZIP', ''],
  ['httperr1.7z', '']
];
for (const [name, want] of TABLE) {
  test('detectKind(' + JSON.stringify(name) + ') is ' + JSON.stringify(want), () => {
    assert.equal(kinds.detectKind(name), want);
  });
}

test('detectKind survives odd input without throwing', () => {
  for (const odd of [undefined, null, 5, {}, '\u0000', 'a'.repeat(100000), '../../etc/passwd', 'u_ex1.log\u202egnp']) {
    assert.doesNotThrow(() => kinds.detectKind(odd));
  }
});

test('compressed files are named by their ending, in any case and any folder', () => {
  for (const name of ['a.zip', 'A.ZIP', 'x/y/z.tar', 'logs.tgz', 'a.log.gz', 'b.7z', 'c.rar', 'd.bz2', 'e.xz', 'f.cab']) assert.equal(kinds.isCompressed(name), true, name);
  for (const name of ['a.log', 'zip.log', 'gz', 'notes.zipx.txt', '']) assert.equal(kinds.isCompressed(name), false, name);
});

test('the time-zone rule: UTC for W3C, HTTPERR, FREB, XE and Query Store; local for the error log and event log; app logs vary', () => {
  for (const id of ['iis-w3c', 'iis-httperr', 'iis-freb', 'sql-xe', 'sql-querystore']) { assert.equal(kinds.timeZoneRule(id), 'utc', id); assert.equal(kinds.needsTimeZone(id), false, id); }
  for (const id of ['sql-errorlog', 'win-events']) { assert.equal(kinds.timeZoneRule(id), 'local', id); assert.equal(kinds.needsTimeZone(id), true, id); }
  assert.equal(kinds.timeZoneRule('app-log'), 'varies');
  assert.equal(kinds.needsTimeZone('app-log'), true);
  assert.equal(kinds.timeZoneRule('nonsense'), null);
  assert.equal(kinds.needsTimeZone('nonsense'), false);
  assert.match(copy.zoneText('iis-w3c'), /UTC/);
  assert.match(copy.zoneText('iis-httperr'), /UTC/);
  assert.match(copy.zoneText('sql-errorlog'), /local/i);
});

test('stageFiles suggests a kind, refuses compressed and empty files, and leaves unknown ones without a choice', () => {
  const list = kinds.stageFiles([
    fakeFile('u_ex261020.log', 4000), fakeFile('httperr1.log', 900), fakeFile('mystery.dat', 50), fakeFile('logs.zip', 9000), fakeFile('u_ex261021.log', 0)
  ]);
  assert.deepEqual(list.map((x) => x.suggested), ['iis-w3c', 'iis-httperr', '', '', '']);
  assert.deepEqual(list.map((x) => x.choice), ['iis-w3c', 'iis-httperr', '', '', '']);
  assert.deepEqual(list.map((x) => x.refusal), ['', '', '', 'compressed', 'empty']);
  assert.deepEqual(list.map((x) => x.id), [1, 2, 3, 4, 5]);
  assert.deepEqual(kinds.usableFiles(list).map((x) => x.name), ['u_ex261020.log', 'httperr1.log']);
  assert.deepEqual(kinds.needingZone(list).map((x) => x.name), []);
  assert.deepEqual(kinds.needingHost(list).map((x) => x.name), ['httperr1.log']);
  assert.deepEqual(kinds.kindsInUse(list), ['iis-w3c', 'iis-httperr']);
});

test('a folder pick keeps the folder in the shown name but guesses from the file name', () => {
  const [row] = kinds.stageFiles([fakeFile('u_ex261020.log', 10, 'W3SVC1/u_ex261020.log')]);
  assert.equal(row.name, 'W3SVC1/u_ex261020.log');
  assert.equal(row.suggested, 'iis-w3c');
});

test('the copy passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(copy.fileKindsCopyEntries(), deck.terms), []);
});

test('the copy builds its sentences', () => {
  assert.equal(copy.addFilesText(0), copy.FILES_TEXT.addNone);
  assert.equal(copy.addFilesText(1), 'Add 1 file');
  assert.equal(copy.addFilesText(3), 'Add 3 files');
  assert.match(copy.zoneWarnText(2), /^2 files write server local time/);
  assert.match(copy.zoneWarnText(1), /^1 file writes server local time/);
  assert.equal(copy.looksLikeText('iis-w3c'), 'Looks like: IIS web log (W3C).');
  assert.match(copy.supportedText(), /^Supported: IIS web log \(W3C\) · HTTP\.sys error log \(HTTPERR\)/);
  assert.match(copy.supportedText(), /compressed/i);
});

test('the IIS guide names what has to be on and has no empty part', () => {
  assert.equal(copy.IIS_GUIDE.heading, 'Before you add IIS logs: what has to be on');
  assert.deepEqual(copy.IIS_GUIDE.items.map((x) => x.id), ['w3c', 'httperr', 'freb', 'reading']);
  const text = JSON.stringify(copy.IIS_GUIDE.items);
  for (const needle of ['time-taken', 'sc-win32-status', 'HTTPERR', 'fr######.xml', 'W3SVC']) assert.ok(text.includes(needle), needle);
});

test('the components exist and keep their hooks', () => {
  const guide = read('src/components/wizard/IisGuide.svelte');
  assert.match(guide, /<details/);
  const panel = read('src/components/wizard/ImportFilesPanel.svelte');
  assert.match(panel, /type="file"/);
  assert.match(panel, /webkitdirectory/);
  assert.match(panel, /ondrop/);
});

test('which kinds need a computer picked: HTTPERR and Query Store need a host, the error log and event log need a host with a time zone', () => {
  assert.deepEqual(kinds.FILE_KIND_IDS.map((id) => [id, kinds.hostNeed(id)]), [
    ['iis-w3c', 'optional'], ['iis-httperr', 'host'], ['iis-freb', 'optional'], ['sql-xe', 'optional'],
    ['sql-errorlog', 'host-tz'], ['sql-querystore', 'host'], ['win-events', 'host-tz'], ['app-log', 'host']
  ]);
  for (const id of ['iis-httperr', 'sql-querystore', 'sql-errorlog', 'win-events', 'app-log']) assert.equal(kinds.needsHost(id), true, id);
  for (const id of ['iis-w3c', 'iis-freb', 'sql-xe', 'nonsense']) assert.equal(kinds.needsHost(id), false, id);
  // The server refuses log4net-blocks and log4net-text (and the other app-log importers) without a computer, so the page must not call it optional.
  assert.equal(kinds.offersHost('app-log'), false);
  assert.equal(kinds.offersHost('sql-xe'), true);
  // A real IIS log names a server that may not be in the list: the pick is offered, and used for that name.
  assert.equal(kinds.offersHost('iis-w3c'), true);
  assert.equal(kinds.offersHost('iis-freb'), true);
  assert.equal(kinds.offersHost('iis-httperr'), false);
  assert.equal(kinds.hostNeed('nonsense'), 'none');
});

test('a staged row starts with no computer, and needingHost drops a row once a computer is set', () => {
  const list = kinds.stageFiles([fakeFile('httperr1.log', 900), fakeFile('u_ex261020.log', 10)]);
  assert.deepEqual(list.map((x) => x.host), ['', '']);
  assert.deepEqual(kinds.needingHost(list).map((x) => x.name), ['httperr1.log']);
  list[0].host = 'WEB01';
  assert.deepEqual(kinds.needingHost(list), []);
});

test('the offered pick names the file\'s own computer for IIS, and keeps the plain ask for sql-xe', () => {
  const zone = loadTypeScript('src/lib/copy/zoneChoiceCopy.ts');
  const named = zone.optionalHelpText('iis-w3c');
  assert.equal(named, zone.ZONE_CHOICE_TEXT.optionalHelpNamed, 'an IIS log reads the computer from the file');
  assert.match(named, /read from the file/);
  assert.match(named, /only if/);
  assert.equal(zone.optionalHelpText('sql-xe'), zone.ZONE_CHOICE_TEXT.optionalHelp, 'a sql-xe export may name no computer, so its plain ask stays');
  assert.notEqual(named, zone.optionalHelpText('sql-xe'));
  for (const id of ['iis-w3c', 'iis-freb', 'sql-xe']) assert.equal(kinds.offersHost(id), true, id);
  for (const id of ['iis-httperr', 'sql-querystore', 'sql-errorlog', 'win-events', 'app-log']) assert.equal(kinds.offersHost(id), false, id);
  // Both help lines reach the copy lint through the zone-choice entry list.
  const entries = zone.zoneChoiceCopyEntries();
  const ids = new Set(entries.map((e) => e.id));
  assert.ok(ids.has('zone.sample.optional.named') && ids.has('zone.sample.optional.xe'));
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});
