const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Scenario 1, lane L3: sources and connections.
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

const copy = loadTypeScript('src/lib/copy/sourcesCopy.ts');
const wizardCopy = loadTypeScript('src/lib/copy/wizardSourcesCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const memory = loadTypeScript('src/components/sources/connectionMemory.ts');

test('L3-01 the connections-page SQL form names its real buttons and refuses a long server name', () => {
  const form = read('src/components/sources/SqlConnectionForm.svelte');
  assert.match(form, /<p class="lead">\{CONN_TEXT\.sqlFormIntro\}<\/p>/);
  assert.doesNotMatch(form, /SOURCES_TEXT\.sqlIntro/);
  assert.match(form, /line = SOURCES_TEXT\.sqlTestNeedPassword/);
  assert.doesNotMatch(form, /CONN_TEXT\.testNeedsPassword/);
  assert.doesNotMatch(form, /line = CONN_TEXT\.saved/);
  assert.match(form, /form\.server\.trim\(\)\.length > 200[\s\S]*?SOURCES_TEXT\.sqlServerTooLong/);
  assert.match(form, /maxlength="400"/);
  assert.doesNotMatch(form, /maxlength="260"/);
  assert.ok(copy.CONN_TEXT.sqlFormIntro.includes('Press Test '));
  assert.ok(copy.CONN_TEXT.sqlFormIntro.includes('Save connection'));
  assert.ok(!copy.CONN_TEXT.sqlFormIntro.includes('list databases'));
  assert.ok(wizardCopy.SOURCES_TEXT.sqlIntro.includes('Test and list databases'));
  assert.ok(wizardCopy.SOURCES_TEXT.sqlServerTooLong.includes('200'));
});

test('L3 copy passes the screen lint', () => {
  assert.deepEqual(lintScreenStrings([...copy.sourcesCopyEntries(), ...wizardCopy.wizardSourcesCopyEntries()], deck.terms), []);
});

test('L3-03 the connections page keeps results, always offers Refresh and says when a kind is empty', () => {
  const page = read('src/components/sources/ConnectionsPage.svelte');
  assert.match(page, /tests = \$state\.raw<Record<string, TestResult>>\(recallTests\(\)\)/);
  assert.match(page, /tests = rememberTest\(key,/);
  assert.match(page, /tests = dropTest\(row\.key\)/);
  assert.ok((page.match(/announceConnectionsChanged\(\)/g) ?? []).length >= 2);
  assert.match(page, /\n\s*<button type="button" class="btn" onclick=\{\(\) => void load\(\)\}>\{CONN_TEXT\.refresh\}<\/button>\r?\n\s*\{#if askedMongo\}[^\n]*CONN_TEXT\.refreshAfterMongo/);
  assert.match(page, /\{#if askedMongo\}[^\n]*CONN_TEXT\.refresh/);
  assert.match(page, /data-no-sql/);
  assert.match(page, /data-no-mongo/);
  assert.match(page, /CONN_TEXT\.noSql/);
  assert.match(page, /CONN_TEXT\.noMongo/);
  assert.match(page, /\{#if row\.kind === 'sql'\}<button[^\n]*CONN_TEXT\.edit/);
  assert.match(page, /function saved\(item: SavedSqlConnection\)[\s\S]*?notice = CONN_TEXT\.saved/);
  assert.match(page, /data-notice/);
});

test('L3-03 a Mongo test in the demo makes no call and stays Not tested', () => {
  const page = read('src/components/sources/ConnectionsPage.svelte');
  assert.match(page, /if \(app\.info\?\.demo === true\) \{ notice = CONN_TEXT\.testDemo; return; \}/);
  const at = page.indexOf('app.info?.demo === true');
  assert.ok(at > 0 && page.indexOf('api.testConnection(') > at);
  assert.match(page, /import \{ app \} from '\.\.\/\.\.\/lib\/appState\.svelte'/);
});

test('L3-03 test results are remembered in memory and the change event fires', () => {
  memory.resetTests();
  assert.deepEqual(memory.recallTests(), {});
  const a = memory.rememberTest('sql:1', { word: 'Works', at: 't', problem: '' });
  assert.equal(a['sql:1'].word, 'Works');
  assert.equal(memory.recallTests()['sql:1'].word, 'Works');
  memory.rememberTest('mongo:2', { word: 'Failed', at: 't', problem: 'x' });
  memory.dropTest('sql:1');
  assert.deepEqual(Object.keys(memory.recallTests()), ['mongo:2']);
  memory.resetTests();
  assert.deepEqual(memory.recallTests(), {});
  assert.doesNotThrow(() => memory.announceConnectionsChanged());
  try {
    globalThis.window = new EventTarget();
    let n = 0;
    const stop = memory.onConnectionsChanged(() => { n += 1; });
    memory.announceConnectionsChanged();
    assert.equal(n, 1);
    stop();
    memory.announceConnectionsChanged();
    assert.equal(n, 1);
  } finally { delete globalThis.window; }
  assert.doesNotMatch(read('src/components/sources/connectionMemory.ts'), /password|localStorage|sessionStorage|console\./i);
});

test('L3-03 new connection copy', () => {
  const c = copy.CONN_TEXT;
  for (const key of ['refreshAfterMongo', 'noSql', 'noMongo', 'testDemo']) assert.ok(typeof c[key] === 'string' && c[key] !== '');
  assert.ok(c.refreshAfterMongo.includes('Refresh'));
  assert.ok(c.noSql.includes('+ SQL Server'));
  assert.ok(c.noMongo.includes('+ MongoDB'));
  assert.doesNotMatch(c.testDemo, /demo/i);
});

test('L3-04 Add another day shows only the last result and says the reference stays', () => {
  const day = read('src/components/sources/AddAnotherDay.svelte');
  assert.doesNotMatch(day, /let added = \$state/);
  assert.doesNotMatch(day, /\{#if added\}/);
  assert.match(day, /let said = \$state<\{ good: boolean; text: string \} \| null>\(null\)/);
  assert.match(day, /said = result\.saved \? \{ good: true, text: DAY_TEXT\.added \} : runNotice\(result\)/);
  assert.match(day, /<LaneRunner [^\n]*\n[^\n]*ownNotice onStart=\{\(\) => \{ said = null; \}\} onRan=\{ran\}/);
  assert.doesNotMatch(day, /<LaneRunner [^>]*runLabel/);
  assert.match(day, /shiftDays\(latest\.incident, 1\)/);
  assert.match(day, /reference=\{latest\.reference\}/);
  assert.match(day, /\{#if readOnly\}/);
  assert.match(day, /<p class="hint" data-reference-stays>\{DAY_TEXT\.referenceStays\}<\/p>/);
  assert.ok(day.indexOf('DAY_TEXT.prefill') < day.indexOf('DAY_TEXT.referenceStays'));
  assert.match(day, /\{#if said\}[^\n]*role=\{said\.good \? 'status' : 'alert'\}/);
  const runner = read('src/components/sources/LaneRunner.svelte');
  assert.match(runner, /onStart\?: \(\) => void/);
  assert.match(runner, /done = ''; problem = '';\s*\n\s*onStart\?\.\(\);/);
  assert.match(runner, /if \(!ownNotice\)/);
  const step = read('src/components/sources/SourceStep.svelte');
  assert.equal((step.match(/ownNotice/g) ?? []).length, 2);
});

test('L3-04 Add another day copy', () => {
  assert.ok(copy.DAY_TEXT.referenceStays.includes('does not move'));
  assert.ok(copy.DAY_TEXT.referenceStays.includes('same normal period'));
  assert.ok(copy.DAY_TEXT.prefill.startsWith('The incident times'));
  assert.equal(copy.DAY_TEXT.added, 'Day added to the case.');
  const lanes = loadTypeScript('src/lib/lanes.ts');
  assert.equal(lanes.runNotice({ saved: true, saveError: '', day: {} }).good, true);
  assert.equal(lanes.runNotice({ saved: false, saveError: 'x', day: {} }).good, false);
});

test('L3-05 the census panel notes an unticked source and still allows the run', () => {
  const panel = read('src/components/CoveragePanel.svelte');
  assert.match(panel, /import \{ CENSUS_TEXT \} from '\.\.\/lib\/copy\/sourcesCopy'/);
  assert.match(panel, /const unticked = \$derived\(!scope\.query\.sourceIds\.includes\(sourceId\)\)/);
  assert.match(panel, /\{#if unticked\}<p class="coverage-intro" data-unticked>\{CENSUS_TEXT\.notTicked\}<\/p>\{\/if\}/);
  assert.ok(panel.indexOf('data-unticked') > 0 && panel.indexOf('data-unticked') < panel.indexOf('coverage-run'));
  assert.match(panel, /disabled=\{!range \|\| phase\.kind === 'running'\}/);
  assert.doesNotMatch(panel, /disabled=\{[^}]*unticked/);
  assert.equal((panel.match(/api\.coverage\(/g) ?? []).length, 1);
  const mount = panel.match(/onMount\(\(\) => \{[\s\S]*?\n  \}\);/);
  assert.ok(mount);
  assert.doesNotMatch(mount[0], /unticked|CENSUS_TEXT/);
  assert.ok(copy.CENSUS_TEXT.notTicked.includes('not ticked'));
  assert.ok(copy.CENSUS_TEXT.notTicked.includes('only reads'));
  assert.ok(copy.sourcesCopyEntries().some((e) => e.id === 'sources.census.notTicked'));
});
