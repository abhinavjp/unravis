const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

// L9: the setup window's copy and spoken parts (D-C-12, D-C-26). Same loading pattern as setup.test.cjs.
const root = path.resolve(__dirname, '..');
const read = (relativePath) => fs.readFileSync(path.join(root, relativePath), 'utf8');
function loadTypeScript(relativePath) {
  const output = ts.transpileModule(read(relativePath), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  new Function('module', 'exports', output)(module, module.exports);
  return module.exports;
}
const logic = loadTypeScript('setup/logic.ts');
const app = read('setup/App.svelte');

test('Repair has its own nutshell sentence; install and upgrade keep theirs (D-C-12)', () => {
  assert.equal(logic.welcomeNutshell('install'), 'Unravis reads audit logs and server health. It never changes your servers.');
  assert.equal(logic.welcomeNutshell('upgrade'), 'Your cases, saved connections and recordings stay as they are. Only the program files change.');
  const repair = logic.welcomeNutshell('repair');
  assert.equal(repair, 'Repair puts back program files that are missing or damaged. Your settings, cases, saved connections and recordings stay as they are.');
  assert.notEqual(repair, logic.welcomeNutshell('upgrade'));
  for (const op of ['install', 'upgrade', 'repair']) {
    assert.doesNotMatch(logic.welcomeNutshell(op), /[-\u2013\u2014]/, op + ' copy has a dash');
  }
});

test('the Welcome screen takes its nutshell from welcomeNutshell and keeps the label', () => {
  assert.match(app, /<div class="nutshell"><b>In a nutshell<\/b>\s*\{welcomeNutshell\(op\)\}\s*<\/div>/);
  assert.doesNotMatch(app, /Only the program files change\./, 'the sentence now lives only in logic.ts');
  assert.match(app, /welcomeHeading, welcomeNutshell,/);
});

test('step changes and results are announced (D-C-26)', () => {
  assert.equal(logic.stepAnnouncement('install', 'options', 2, 5, 'Options', null), 'Step 2 of 5: Options');
  assert.equal(logic.stepAnnouncement('uninstall', 'uninstall', 1, 4, 'Remove', null), 'Step 1 of 4: Remove');
  assert.equal(logic.stepAnnouncement('install', 'done', 5, 5, 'Done', { outcome: 'done', op: 'install' }), 'Unravis is installed.');
  assert.equal(
    logic.stepAnnouncement('uninstall', 'done', 4, 4, 'Done', { outcome: 'done-with-leftovers', op: 'uninstall', left: ['File C:\\x'] }),
    'Unravis is removed. Some items are left behind.'
  );
  assert.equal(logic.stepAnnouncement('install', 'failed', 4, 5, 'Install', { outcome: 'rolled-back', op: 'install' }), 'Install did not finish. Nothing was left behind.');
  // An end screen without its result yet falls back to the step line.
  assert.equal(logic.stepAnnouncement('install', 'done', 5, 5, 'Done', null), 'Step 5 of 5: Done');
});

test('the window has one hidden status region that is always present, and the progress bar speaks its value', () => {
  const region = /<p class="sr" role="status" aria-live="polite" aria-atomic="true">\{announcement\}<\/p>/;
  assert.match(app, region);
  assert.ok(app.search(region) < app.indexOf('{#if fatal}'), 'the region sits before the screens, outside any {#if}');
  assert.match(app, /const announcement = \$derived\(/);
  assert.match(app, /aria-valuetext=\{progressPercent\(rows\) \+ ' percent'\}/);
});

test('hints are linked to their controls with aria-describedby, and every linked id exists (D-C-26)', () => {
  for (const id of ['key-hint', 'forever-cases', 'forever-recordings', 'cases-place', 'recordings-place']) {
    assert.ok(app.includes('id="' + id + '"'), 'no element has id ' + id);
    assert.ok(app.includes("'" + id + "'") || app.includes('aria-describedby="' + id + '"'), 'nothing points at ' + id);
  }
  for (const match of app.matchAll(/aria-describedby="([^"]+)"/g)) {
    assert.ok(app.includes('id="' + match[1] + '"'), 'aria-describedby points at a missing id: ' + match[1]);
  }
  assert.match(app, /bind:checked=\{keySaved\} aria-describedby="key-hint"/);
  assert.match(app, /bind:value=\{uninst\.confirmCases\} aria-describedby="forever-cases"/);
  assert.match(app, /bind:value=\{uninst\.confirmRecordings\} aria-describedby="forever-recordings"/);
  // The approval screen's warning is not linked to anything and has no id.
  assert.equal(app.split('<p class="danger">{FOREVER}</p>').length - 1, 1);
});
