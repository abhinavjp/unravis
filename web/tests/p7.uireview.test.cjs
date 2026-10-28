const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// P7 UI review fixes: one regression test per finding (M1 to M4, minors 1 to 15). The components are Svelte, so where a rule lives in a component
// the test reads its source; where it can be a pure function, the function is exercised.

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
const comp = (name) => read('src/components/sources/' + name + '.svelte');

const lanes = loadTypeScript('src/lib/lanes.ts');
const copy = loadTypeScript('src/lib/copy/sourcesCopy.ts');
const { CaseApiError } = loadTypeScript('src/lib/cases.ts');

const day = (over = {}) => ({ id: 1, at: '2026-10-20T09:00:00Z', kind: 'sql', label: 'SQL Server', database: 'Audit', state: 'Ready', incident: { start: '2026-10-19T10:00:00Z', end: '2026-10-19T11:00:00Z' }, reference: { start: '', end: '' }, lane: null, ...over });

// ---- M1 ------------------------------------------------------------------------------------------------------------------------------

test('M1: a read that was not kept says so on the page, and the notice outlives the runner', () => {
  const kept = lanes.runNotice({ saved: true, saveError: '', day: day() });
  assert.equal(kept.good, true);
  assert.equal(kept.text, copy.RUN_TEXT.done);
  const lost = lanes.runNotice({ saved: false, saveError: 'The case is closed, so the result was not saved.', day: day({ id: 0 }) });
  assert.equal(lost.good, false);
  assert.ok(lost.text.startsWith(copy.RUN_TEXT.notSaved) && lost.text.includes('closed'));
  assert.ok(lanes.runNotice({ saved: false, saveError: '', day: day({ id: 0 }) }).text.includes(copy.RUN_TEXT.failed), 'no reason from the server still gives a sentence');
  const step = comp('SourceStep');
  // The notice is drawn by the step itself, outside every branch that mounts or removes the runner.
  const noticeAt = step.indexOf('data-notice');
  assert.ok(noticeAt > 0 && noticeAt < step.indexOf('{#if loadFailed}'), 'the notice sits above the branches');
  assert.match(step, /notice = runNotice\(result\)/);
  assert.match(step, /unsaved = result\.saved \? null : result\.day/, 'a day that was read but not kept is still shown, from memory');
  assert.equal((step.match(/ownNotice/g) ?? []).length, 2, 'both runners of the step leave the message to the step');
  const runner = comp('LaneRunner');
  assert.match(runner, /if \(!ownNotice\)/);
});

// ---- minor 13 ------------------------------------------------------------------------------------------------------------------------

test('minor 13: with nothing chosen the newest day opens, not the first one in the list', () => {
  assert.equal(lanes.newestDay([]), null);
  const list = [day({ id: 3 }), day({ id: 9 }), day({ id: 5 })];
  assert.equal(lanes.newestDay(list).id, 9);
  assert.equal(lanes.newestDay([...list].reverse()).id, 9, 'the order of the list does not matter');
  assert.match(comp('SourceStep'), /newestDay\(mine\)/);
  assert.doesNotMatch(comp('SourceStep'), /mine\[0\]/);
});

// ---- M2 ------------------------------------------------------------------------------------------------------------------------------

test('M2: a closed case offers no read, add, test, edit or forget, and says why', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /<SourcesHub \{caseId\} \{readOnly\}/, 'the case page hands its read-only state to the hub');
  const hub = comp('SourcesHub');
  for (const mount of ['ConnectionsPage', 'SqlStepPanel', 'MongoStepPanel', 'AddDataPanel', 'AddAnotherDay']) {
    assert.match(hub, new RegExp('<' + mount + ' [^>]*\\{readOnly\\}'), mount + ' gets readOnly from the hub');
  }
  for (const name of ['SqlStepPanel', 'MongoStepPanel']) assert.match(comp(name), /readOnly = false/, name + ' stays usable in the guided shell, which never passes it');
  const page = comp('ConnectionsPage');
  assert.match(page, /disabled=\{testing !== '' \|\| readOnly\}/);
  assert.equal((page.match(/disabled=\{readOnly\}/g) ?? []).length, 2, 'edit and forget are off');
  assert.match(page, /disabled=\{full \|\| readOnly\}/);
  const add = comp('AddDataPanel');
  assert.match(add, /disabled=\{readOnly\} onclick=\{\(\) => choose\('sql'\)\}/);
  assert.match(add, /disabled=\{readOnly\} onclick=\{\(\) => choose\('mongo'\)\}/);
  assert.match(add, /disabled=\{readOnly \|\| byDay === 0\}/);
  const step = comp('SourceStep');
  assert.equal((step.match(/data-read-only/g) ?? []).length, 2, 'both places the step would read say why they do not');
  assert.match(step, /onAddDay=\{readableByDay\(kind\) && !readOnly/);
  assert.match(comp('AddAnotherDay'), /\{#if readOnly\}/);
  assert.match(copy.HUB_TEXT.readOnlyWhy, /closed/);
});

test('M2: the case-closed refusal reads as a plain sentence, not as another read running', () => {
  const refused = new CaseApiError('The case is closed, so nothing was read. Reopen the case to read from a server.', 409, 'case-closed');
  assert.equal(lanes.runProblem(refused).text, copy.RUN_TEXT.closed);
  assert.match(copy.RUN_TEXT.closed, /closed/);
  assert.notEqual(lanes.runProblem(new CaseApiError('busy', 409, '')).text, copy.RUN_TEXT.closed, 'a plain 409 is still busy');
});

// ---- M3 ------------------------------------------------------------------------------------------------------------------------------

test('M3: Add data never clicks into nothing: Files opens the import page, Blackbox is off with a reason until its page exists', () => {
  const hub = comp('SourcesHub');
  assert.doesNotMatch(hub, /else tab = 'add';/, 'choosing files or blackbox no longer lands back on the same tab');
  assert.match(hub, /what === 'files'\) onAddFiles\?\.\(\)/);
  assert.match(hub, /what === 'blackbox'\) onAddBlackbox\?\.\(\)/);
  assert.match(hub, /files=\{!!onAddFiles\} blackbox=\{!!onAddBlackbox\}/, 'a card is on only when its page was handed in');
  const add = comp('AddDataPanel');
  assert.match(add, /disabled=\{readOnly \|\| !blackbox\} onclick=\{\(\) => choose\('blackbox'\)\}/);
  assert.match(add, /disabled=\{readOnly \|\| !files\} onclick=\{\(\) => choose\('files'\)\}/);
  assert.match(add, /\{#if !blackbox\}<p class="hint" data-unavailable="blackbox">\{ADD_TEXT\.blackboxLater\}/);
  assert.match(copy.ADD_TEXT.blackboxLater, /not part of this version/);
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /onAddFiles=\{openCaseImport\}/, 'Files opens the import drawer that exists on this branch');
  assert.doesNotMatch(home, /onAddBlackbox/, 'the Blackbox page is wired by the phase that adds it');
});

// ---- M4 ------------------------------------------------------------------------------------------------------------------------------

test('M4: + MongoDB opens the window where a connection is saved, and is off when nothing can open it', () => {
  const hub = comp('SourcesHub');
  assert.match(hub, /<ConnectionsPage \{readOnly\} onMongoAdd=\{onAddMongo\} \/>/, 'it no longer jumps to Step 8, which has nothing to pick');
  assert.doesNotMatch(hub, /onMongoAdd=\{\(\) => \{ tab = 'mongo'/);
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /onAddMongo=\{\(\) => void openConnections\('new'\)\}/);
  assert.match(home, /openConnections,/);
  const page = comp('ConnectionsPage');
  assert.match(page, /disabled=\{readOnly \|\| !onMongoAdd\}/);
  assert.match(page, /CONN_TEXT\.mongoNowhere/);
  assert.match(page, /\{#if askedMongo\}[^\n]*CONN_TEXT\.refresh/, 'after the window opens, the list can be refreshed to show the new connection');
  assert.match(copy.CONN_TEXT.mongoHere, /Refresh/);
  assert.doesNotMatch(copy.CONN_TEXT.mongoHere, /drawer/i, 'the old sentence named a drawer the user was never taken to');
});

// ---- minors 1 to 4: the grant script panel -------------------------------------------------------------------------------------------

test('minor 1: a missing permission\'s script is shown once, with its check, not again in the grant panel', () => {
  const panel = comp('GrantScriptPanel');
  assert.doesNotMatch(panel, /fixScript|permission=|{permission}/, 'the panel has no per-check script of its own');
  assert.doesNotMatch(comp('SourceStep'), /fixScript|firstFix|permission=/);
  const view = comp('LaneResultView');
  assert.match(view, /data-fix=\{check\.id\}/, 'each fix is listed once, with its check');
  assert.equal((view.match(/<pre class="script"/g) ?? []).length, 1);
});

test('minor 2: the grant script follows the login and the version (the real route takes both)', () => {
  const src = read('src/lib/lanes.ts');
  assert.match(src, /query\(\{ kind: 'sql', version, login, database \}\)/);
  assert.match(src, /query\(\{ kind: 'mongo', login, database \}\)/);
  const panel = comp('GrantScriptPanel');
  assert.match(panel, /const v = chosen; const l = who\.trim\(\); const d = db\.trim\(\)/, 'a change of version, login or database runs the fetch again');
  assert.match(panel, /fetchSqlGrantScripts\(v, l, d, mine\.signal\)/);
  assert.match(panel, /bind:value=\{who\}/);
  assert.match(panel, /bind:value=\{db\}/);
});

test('minor 3: every Copy says Copied beside itself', () => {
  const view = comp('LaneResultView');
  assert.match(view, /copied === check\.id \? GRANT_TEXT\.copied/, 'each fix has its own note');
  const panel = comp('GrantScriptPanel');
  assert.match(panel, /copied === 'yes' \? GRANT_TEXT\.copied/);
  assert.equal((panel.match(/GRANT_TEXT\.copy\}/g) ?? []).length, 1, 'the one Copy button of the panel has its note');
});

test('minor 4: the grant script does not blank while it reloads, and a pick survives the panel leaving', () => {
  const panel = comp('GrantScriptPanel');
  assert.match(panel, /\{#if script !== ''\}/, 'the script on screen stays while the next one loads');
  assert.doesNotMatch(panel, /script !== '' && !loading/);
  assert.match(panel, /\{#if loading && script === ''\}/, 'Writing the script only shows when there is nothing to show yet');
  assert.match(panel, /aria-busy=\{loading\}/);
  assert.match(panel, /disabled=\{loading\} onclick=\{\(\) => void copy\(script\)\}/, 'a half-loaded script cannot be copied');
  lanes.rememberGrant('sql', { version: '2019' });
  lanes.rememberGrant('sql', { login: 'unravis_ro' });
  assert.deepEqual({ ...lanes.recallGrant('sql') }, { version: '2019', login: 'unravis_ro' });
  assert.deepEqual({ ...lanes.recallGrant('mongo') }, {}, 'the two kinds keep their own picks');
  assert.match(panel, /const kept = recallGrant\(kind\)/);
  assert.match(panel, /rememberGrant\(kind, \{ version: '2019' \}\)/);
});
