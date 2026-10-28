const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// SQL Server and MongoDB screens (P7): failure sentences, the nothing-read rule, and the shape of the components. These are the web side of
// the clumsy-user cases: wrong password, 21st connection, no grants, MongoDB down mid-read, two days that overlap.

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
const comp = (name) => read('src/components/sources/' + name + '.svelte');

const lanes = loadTypeScript('src/lib/lanes.ts');
const copy = loadTypeScript('src/lib/copy/sourcesCopy.ts');
const { ApiError } = loadTypeScript('src/lib/api.ts');
const { CaseApiError } = loadTypeScript('src/lib/cases.ts');

const check = (over = {}) => ({ id: 'c1', title: 'Query Store', state: 'Ready', summary: '', feeds: [], fix: null, ...over });
const laneOf = (checks) => ({ state: 'Cannot test yet', era: '2022', checks, answers: {}, onset: '', guide: null });

test('nothingRead: only when every check is Cannot test yet and none has a fix', () => {
  assert.equal(lanes.nothingRead(null), false);
  assert.equal(lanes.nothingRead(laneOf([])), false, 'no checks is not the same as nothing read');
  assert.equal(lanes.nothingRead(laneOf([check({ state: 'Cannot test yet' }), check({ id: 'c2', state: 'Cannot test yet' })])), true);
  assert.equal(lanes.nothingRead(laneOf([check({ state: 'Cannot test yet' }), check({ id: 'c2', state: 'Ready' })])), false);
  assert.equal(
    lanes.nothingRead(laneOf([check({ state: 'Cannot test yet', fix: { permission: 'VIEW SERVER STATE', script: 'GRANT VIEW SERVER STATE TO [x];' } })])),
    false, 'a missing grant has its own screen with the script to fix it'
  );
  assert.equal(lanes.nothingRead(laneOf([check({ state: 'Failed' })])), false);
});

test('runProblem: each status gets its own sentence, and nothing else is passed on', () => {
  const T = copy.RUN_TEXT;
  assert.equal(lanes.runProblem(new CaseApiError('x', 429)).text, T.limited);
  assert.equal(lanes.runProblem(new CaseApiError('x', 423)).text, T.locked);
  assert.equal(lanes.runProblem(new CaseApiError('x', 503)).text, T.demo);
  assert.equal(lanes.runProblem(new CaseApiError('boom: Login failed for user sa', 500)).text, T.failed, 'a raw server error never reaches the screen');
  assert.equal(lanes.runProblem(new Error('plain')).text, T.failed);
  assert.equal(lanes.runProblem('nope').text, T.failed);
  assert.equal(lanes.runProblem(new CaseApiError('Another read is running.', 409)).text, T.busy);
  const overlap = 'A day for this database already overlaps those times. Nothing was read.';
  assert.equal(lanes.runProblem(new CaseApiError(overlap, 409)).text, overlap, 'overlap (409) says so');
  const full = 'You already saved 20 connections. Forget one first.';
  assert.equal(lanes.runProblem(new ApiError(full, undefined, undefined, 409)).text, full, '21st connection (409) says so');
  assert.equal(lanes.runProblem(new CaseApiError('Request validation failed.', 400)).text, T.failed);
  assert.equal(lanes.runProblem(new CaseApiError('The database name is missing.', 400)).text, 'The database name is missing.');
  const gone = lanes.runProblem(new CaseApiError('gone', 404, 'case-not-found'));
  assert.equal(gone.gone, true);
  assert.equal(lanes.runProblem(new CaseApiError('x', 500)).gone, false);
});

test('every source screen shows the In a nutshell label and the shared sticker words', () => {
  assert.equal(copy.NUTSHELL, 'In a nutshell');
  for (const name of ['ConnectionsPage', 'SourceStep', 'AddDataPanel', 'AddAnotherDay', 'SourceCombinations']) {
    assert.match(comp(name), /NUTSHELL/, name + ' carries the label');
  }
  const sticker = comp('StateSticker');
  for (const word of ['Ready', 'Partly ready', 'Cannot test yet', 'Failed', 'Works', 'Not tested']) assert.ok(sticker.includes(word) || read('src/components/sources/sources.css').includes(word), word);
  const css = read('src/components/sources/sources.css');
  assert.match(css, /var\(--hatch\)/, 'pending is hatched');
  assert.match(css, /src-sticker/);
});

test('one primary button per screen, and the others are outlined', () => {
  const primary = (name) => (comp(name).match(/class="btn-primary"/g) ?? []).length;
  for (const name of ['ConnectionsPage', 'SqlConnectionForm', 'LaneRunner', 'GrantScriptPanel', 'ProfilerGuide', 'LaneResultView', 'NothingRead', 'AddDataPanel', 'SourceCombinations', 'SourcesHub']) {
    assert.ok(primary(name) <= 1, name + ' has at most one primary button');
  }
  // On case home these buttons are outlined (the home already has its main button); a shell may promote one with the primary prop.
  for (const name of ['ConnectionsPage', 'SqlConnectionForm', 'LaneRunner']) {
    assert.equal(primary(name), 0, name + ' is outlined by default');
    assert.match(comp(name), /class=\{primary \? 'btn-primary' : 'btn'\}/, name + ' can be promoted');
  }
  assert.equal(primary('AddDataPanel'), 0, 'Add data only offers choices');
});

test('saved passwords are never shown, bound back or put in a URL', () => {
  const page = comp('ConnectionsPage');
  assert.doesNotMatch(page, /item\.password|bind:value=\{[^}]*password/, 'the connections page never reads or binds a password');
  assert.match(page, /CONN_TEXT\.pwSaved/);
  const form = comp('SqlConnectionForm');
  assert.match(form, /type="password"/);
  assert.match(form, /autocomplete="(off|new-password)"/);
  const runner = comp('LaneRunner');
  assert.match(runner, /password = ''/, 'the typed password is dropped after the run');
  for (const name of ['ConnectionsPage', 'SqlConnectionForm', 'LaneRunner', 'SourceStep', 'GrantScriptPanel']) {
    assert.doesNotMatch(comp(name), /localStorage|sessionStorage|console\./, name + ' keeps nothing and logs nothing');
  }
});

test('the 21st connection: the add button is disabled at 20 and says why', () => {
  const page = comp('ConnectionsPage');
  assert.match(page, /disabled=\{full \|\| readOnly\}/);
  assert.match(page, /CONN_TEXT\.full/);
  assert.match(page, /CONNECTION_LIMIT/);
  assert.equal(lanes.canSaveAnother(19), true);
  assert.equal(lanes.canSaveAnother(20), false);
  assert.match(copy.CONN_TEXT.full, /20/);
});

test('Forget asks once before it removes anything', () => {
  const page = comp('ConnectionsPage');
  assert.match(page, /CONN_TEXT\.forgetAsk/);
  assert.match(page, /asking = row\.key/);
  assert.match(page, /CONN_TEXT\.forgetYes/);
  assert.match(page, /CONN_TEXT\.forgetNo/);
});

test('wrong password and a server down mid-read show a sentence, never a stack or a password', () => {
  const form = comp('SqlConnectionForm');
  assert.match(form, /Failed/, 'a failed test is a Failed sticker');
  const runner = comp('LaneRunner');
  assert.match(runner, /runProblem\(error\)/);
  assert.match(runner, /role="alert"/);
  assert.match(runner, /finally \{ busy = false; \}/, 'the button comes back after a failure');
  // A failed read that reached the server but could not be saved says so instead of showing a green result.
  assert.match(runner, /runNotice\(result\)/);
  assert.match(copy.RUN_TEXT.notSaved, /could not be kept/);
});

test('a login with no grants shows the script to fix it, and never runs it', () => {
  const view = comp('LaneResultView');
  assert.match(view, /check\.fix/);
  assert.match(view, /GRANT_TEXT\.notRun/);
  assert.match(view, /GRANT_TEXT\.copy/);
  assert.doesNotMatch(view, /fetch\(|api\./, 'the result view only shows what the server returned');
  const grants = comp('GrantScriptPanel');
  assert.match(grants, /GRANT_TEXT\.notRun/);
  assert.match(grants, /versionNote/);
  assert.match(copy.GRANT_TEXT.notRun, /never runs|does not run|never run/i);
});

test('two days that overlap are refused here first, with nothing read', () => {
  const runner = comp('LaneRunner');
  const refuse = runner.indexOf('overlapProblem(');
  const call = runner.indexOf('runSqlLane(');
  assert.ok(refuse > 0 && call > refuse, 'the overlap check comes before any read');
  assert.match(runner, /RUN_TEXT\.overlap/);
  assert.match(copy.RUN_TEXT.overlap, /Nothing was read/);
});

test('Step 8 and Add another day: MongoDB is Not by day with the reason', () => {
  const day = comp('AddAnotherDay');
  assert.match(day, /DAY_TEXT\.notByDay/);
  assert.match(day, /DAY_TEXT\.profilerWhy/);
  assert.match(day, /DAY_TEXT\.nothingHeld/);
  assert.match(day, /shiftDays\(latest\.incident, 1\)/, 'the range starts as the same hours the next day');
  assert.match(day, /lockDatabase/);
  const step = comp('SourceStep');
  assert.match(step, /ProfilerGuide/);
  assert.match(step, /data-no-source/);
  assert.match(step, /Cannot test yet/);
});

test('the case page mounts the hub additively', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /SourcesHub/);
  assert.match(home, /<SourcesHub \{caseId\} \{readOnly\} onGone=\{gone\} onAddFiles=\{openCaseImport\} \/>/);
  const hub = comp('SourcesHub');
  for (const name of ['ConnectionsPage', 'SqlStepPanel', 'MongoStepPanel', 'AddDataPanel', 'AddAnotherDay', 'SourceCombinations']) assert.match(hub, new RegExp(name));
  assert.match(hub, /role="tablist"/);
});

test('the demo does not keep connections, and the connections page says so instead of failing', () => {
  const page = comp('ConnectionsPage');
  assert.match(page, /data-demo/);
  assert.match(page, /CONN_TEXT\.demo/);
  assert.ok(copy.CONN_TEXT.demo.length > 0);
});

test('the empty steps still show the grant script so the owner can run it before connecting', () => {
  const step = comp('SourceStep');
  assert.match(step, /<GrantScriptPanel \{kind\} \/>/);
  assert.match(comp('GrantScriptPanel'), /aria-pressed/);
});
