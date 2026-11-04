const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Packet P11: the Sources list (tags and time-zone state per source) and the time-zone notice on the Investigation Timeline.

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

const model = loadTypeScript('src/lib/sourcesList.ts');
const copy = loadTypeScript('src/lib/copy/sourcesListCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const imp = (id, importer, over = {}) => ({ id, importer, fileName: importer + '-' + id + '.log', hostLabel: '', tzRule: 'utc-field', status: 'ok', rows: 10, ...over });
const tag = (id, kind, over = {}) => ({ id, importId: 0, kind, label: kind + id, env: '', portal: '', api: '', classification: '', ...over });
const hosts = [{ label: 'WEB01', tz: 'Asia/Dubai' }, { label: 'DB01', tz: '' }];

test('tz state: the three self-describing rules are known', () => {
  assert.deepEqual(model.tzOfImport(imp(1, 'iis-w3c'), hosts), { state: 'known', why: 'utc', zone: 'UTC', host: '' });
  assert.equal(model.tzOfImport(imp(1, 'sql-xe', { tzRule: 'offset-in-field' }), hosts).why, 'offset');
  assert.equal(model.tzOfImport(imp(1, 'win-events', { tzRule: 'header-bias' }), hosts).state, 'known');
});

test('tz state: UTC taken for a type that writes local time is assumed, not known', () => {
  for (const kind of ['sql-errorlog', 'win-events', 'app-log']) assert.equal(model.tzOfImport(imp(1, kind), hosts).state, 'assumed-utc', kind);
  assert.equal(model.tzOfImport(imp(1, 'iis-w3c'), hosts).state, 'known');
});

test('tz state: a missing or unknown rule needs a check, as the server leaves the file out', () => {
  assert.equal(model.tzOfImport(imp(1, 'sql-xe', { tzRule: '' }), hosts).state, 'needs-check');
  assert.equal(model.tzOfImport(imp(1, 'sql-xe', { tzRule: 'guess' }), hosts).why, 'no-rule');
});

test('tz state: host-local needs a computer that is in the list and has a zone', () => {
  const local = (over) => model.tzOfImport(imp(1, 'sql-errorlog', { tzRule: 'host-local', ...over }), hosts);
  assert.equal(local({}).why, 'no-host');
  assert.equal(local({ hostLabel: 'WEB01' }).state, 'known');
  assert.equal(local({ hostLabel: 'WEB01' }).zone, 'Asia/Dubai');
  assert.equal(local({ hostLabel: 'GONE' }).why, 'host-gone');
  assert.equal(local({ hostLabel: 'DB01' }).why, 'no-zone');
  // The list not read yet: a computer already named is trusted.
  assert.equal(model.tzOfImport(imp(1, 'sql-errorlog', { tzRule: 'host-local', hostLabel: 'GONE' }), null).state, 'known');
});

test('tz state: a MongoDB database is UTC, other tags carry no state', () => {
  assert.equal(model.tzOfTag({ kind: 'mongo-database' }).state, 'known');
  assert.equal(model.tzOfTag({ kind: 'blackbox' }), null);
});

test('stickers name the lane a source feeds', () => {
  assert.equal(model.stickerOfTag({ kind: 'mongo-database', classification: 'audit' }), 'API audit');
  assert.equal(model.stickerOfTag({ kind: 'mongo-database', classification: 'other' }), 'App data');
  assert.equal(model.stickerOfTag({ kind: 'sql-server', classification: '' }), 'SQL');
  assert.equal(model.stickerOfImporter('iis-httperr'), 'IIS');
  assert.equal(model.stickerOfImporter('win-events'), 'Windows events');
  assert.equal(model.stickerOfImporter('app-log'), 'App data');
  assert.equal(model.stickerOfTag({ kind: 'blackbox', classification: '' }), 'Blackbox');
  assert.equal(model.stickerOfTag({ kind: 'redis', classification: '' }), '');
});

test('list: a tag that names an import folds into that file and gives it its tags', () => {
  const rows = model.buildSourceList([tag(1, 'iis-w3c', { importId: 7, env: 'Production', portal: 'Admin', api: 'Orders' })], [imp(7, 'iis-w3c')], hosts);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].key, 'f7');
  assert.deepEqual([rows[0].env, rows[0].portal, rows[0].api], ['Production', 'Admin', 'Orders']);
  assert.equal(rows[0].isFile, true);
});

test('list: a loose tag folds into the one unclaimed file of its type, and only then', () => {
  const one = model.buildSourceList([tag(1, 'iis-w3c', { env: 'UAT' })], [imp(7, 'iis-w3c')], hosts);
  assert.equal(one.length, 1);
  assert.equal(one[0].env, 'UAT');
  const two = model.buildSourceList([tag(1, 'iis-w3c', { env: 'UAT' })], [imp(7, 'iis-w3c'), imp(8, 'iis-w3c')], hosts);
  assert.equal(two.length, 3, 'two candidate files: the tag keeps its own row and nothing is guessed');
  assert.ok(two.every((row) => row.key === 't1' || row.env === ''));
});

test('list: files not kept are left out, tags with no file keep a row, rows are ordered by lane', () => {
  const rows = model.buildSourceList(
    [tag(1, 'mongo-database', { classification: 'audit', env: 'Production' }), tag(2, 'blackbox')],
    [imp(1, 'win-events'), imp(2, 'iis-w3c', { status: 'failed' }), imp(3, 'iis-w3c')], hosts);
  assert.deepEqual(rows.map((row) => row.sticker), ['API audit', 'IIS', 'Windows events', 'Blackbox']);
  assert.equal(rows.some((row) => row.key === 'f2'), false);
});

test('environment filter: names in order of appearance, one at a time, empty clears', () => {
  const rows = model.buildSourceList(
    [tag(1, 'mongo-database', { classification: 'audit', env: 'Production' }), tag(2, 'mongo-database', { classification: 'audit', env: 'UAT' }), tag(3, 'mongo-database', { classification: 'other', env: 'Production' })], [], hosts);
  assert.deepEqual(model.envNames(rows), ['Production', 'UAT']);
  assert.equal(model.filterByEnv(rows, 'UAT').length, 1);
  assert.equal(model.filterByEnv(rows, 'Production').length, 2);
  assert.equal(model.filterByEnv(rows, '').length, 3, 'no filter shows every row');
  assert.equal(model.filterByEnv(rows, 'Nowhere').length, 0);
});

test('counts add up per state and ignore stateless rows', () => {
  const rows = model.buildSourceList([tag(1, 'blackbox')], [imp(1, 'iis-w3c'), imp(2, 'sql-errorlog'), imp(3, 'sql-xe', { tzRule: '' })], hosts);
  assert.deepEqual(model.tzCounts(rows), { known: 1, assumed: 1, needs: 1 });
  assert.equal(copy.countsText({ known: 1, assumed: 1, needs: 1 }), '1 known, 1 assumed UTC, 1 to check');
  assert.equal(copy.countsText({ known: 0, assumed: 0, needs: 0 }), '');
});

test('copy: every sentence is registered and passes the copy lint', () => {
  const entries = copy.sourcesListCopyEntries();
  for (const key of Object.keys(copy.SOURCES_LIST_TEXT)) assert.ok(entries.some((e) => e.id === 'srclist.' + key), 'not registered: ' + key);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  assert.equal(copy.TZ_NOTICE_TEXT.lead(['SQL', 'IIS']), 'A file for the SQL and IIS lanes has no usable time zone, so those lanes draw nothing.');
  assert.match(copy.TZ_NOTICE_TEXT.lead(['SQL']), /SQL lane has/);
});

test('wiring: the hub lists the Sources tab and opens it on request; the list is read only', () => {
  const hub = read('src/components/sources/SourcesHub.svelte');
  assert.match(hub, /<SourcesList \{caseId\} \{readOnly\} \{onAddFiles\} \/>/);
  assert.match(hub, /sourcesNav\.wantList/);
  assert.match(hub, /let tab = \$state<Tab>\('connections'\)/, 'the default tab is unchanged');
  const list = read('src/components/sources/SourcesList.svelte');
  assert.doesNotMatch(list, /fetch\(|method: 'P(UT|OST)'|DELETE/, 'the list only reads');
  assert.match(list, /aria-pressed=\{envFilter === ''\}/);
  assert.match(list, /envFilter === name \? '' : name/, 'pressing the pressed environment clears the filter');
});

test('wiring: the timeline shows a notice for a lane left out for its time zone and links to the Sources tab', () => {
  const tracing = read('src/components/TracingPaper.svelte');
  assert.match(tracing, /TZ_NOTICE_TEXT/);
  assert.match(tracing, /requestSourcesList/);
  assert.match(tracing, /clock-unchecked|tz-rule/);
});
