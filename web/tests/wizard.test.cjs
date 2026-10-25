const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
// `runes: true` gives a .svelte.ts store a plain stand-in for $state, so its logic runs under node (the idiom in help.test.cjs).
function loadTypeScript(relativePath, runes = false) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain), runes);
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store), runes);
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, runes ? (value) => value : undefined);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const flow = loadTypeScript('src/lib/wizardFlow.ts');
const copy = loadTypeScript('src/lib/copy/wizardCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const store = loadTypeScript('src/lib/wizard.svelte.ts', true);

const GOOD = { title: 'Payroll slowdown week 42' };
const BAD = { title: '' };

// ---- the name rules

test('a case name needs 2 to 80 characters', () => {
  assert.equal(flow.TITLE_MIN, 2);
  assert.equal(flow.TITLE_MAX, 80);
  assert.equal(flow.titleProblem(''), 'empty');
  assert.equal(flow.titleProblem('   \n  '), 'empty');
  assert.equal(flow.titleProblem('a'), 'too-short');
  assert.equal(flow.titleProblem(' a '), 'too-short');
  assert.equal(flow.titleProblem('ab'), null);
  assert.equal(flow.titleProblem('x'.repeat(80)), null);
  assert.equal(flow.titleProblem('x'.repeat(81)), 'too-long');
});

test('the length counts characters as a person does: an emoji is one, spaces collapse', () => {
  assert.equal(flow.titleProblem('😀'.repeat(80)), null);
  assert.equal(flow.titleProblem('😀'.repeat(81)), 'too-long');
  assert.equal(flow.titleLength('a   b'), 3);
  assert.equal(flow.titleLength('  ab  '), 2);
  assert.equal(flow.cleanTitle('  Week \n 42   issue '), 'Week 42 issue');
});

test('control and invisible formatting characters are refused', () => {
  assert.equal(flow.titleProblem('ab\u0000cd'), 'bad-characters');
  assert.equal(flow.titleProblem('ab' + String.fromCharCode(0x200B) + 'cd'), 'bad-characters');
  assert.equal(flow.titleProblem('ab' + String.fromCharCode(0x202E) + 'cd'), 'bad-characters');
  assert.equal(flow.titleProblem('ab\u0007cd'), 'bad-characters');
  assert.equal(flow.titleProblem('Client A (prod) – week 42'), null);
});

test('every name problem has a plain sentence', () => {
  for (const problem of ['empty', 'too-short', 'too-long', 'bad-characters']) assert.ok(copy.TITLE_PROBLEM_TEXT[problem], problem);
});

// ---- the step rules

test('there are three steps in order: name, sources, review', () => {
  assert.deepEqual([...flow.STEPS], ['name', 'sources', 'review']);
  assert.equal(flow.stepIndex('review'), 2);
});

test('the first step is always open, the others need a valid name', () => {
  assert.equal(flow.canEnter('name', BAD), true);
  assert.equal(flow.canEnter('sources', BAD), false);
  assert.equal(flow.canEnter('review', BAD), false);
  assert.equal(flow.canEnter('sources', GOOD), true);
  assert.equal(flow.canEnter('review', GOOD), true);
});

test('going back is always allowed, going forward needs a valid name, and a refused move keeps the step', () => {
  assert.deepEqual(flow.moveToStep('sources', 'name', BAD), { ok: true, step: 'name' });
  assert.deepEqual(flow.moveToStep('review', 'sources', BAD), { ok: true, step: 'sources' });
  const refused = flow.moveToStep('name', 'sources', BAD);
  assert.equal(refused.ok, false);
  assert.equal(refused.step, 'name');
  assert.ok(refused.reason);
  assert.deepEqual(flow.moveToStep('name', 'sources', GOOD), { ok: true, step: 'sources' });
  assert.deepEqual(flow.moveToStep('name', 'review', GOOD), { ok: true, step: 'review' }, 'the sources step may be skipped');
  assert.equal(flow.moveToStep('name', 'nowhere', GOOD).ok, false);
});

test('next and back stop at the ends', () => {
  assert.equal(flow.nextStepOf('name'), 'sources');
  assert.equal(flow.nextStepOf('review'), 'review');
  assert.equal(flow.backStepOf('review'), 'sources');
  assert.equal(flow.backStepOf('name'), 'name');
});

test('a draft picked up again returns to the step it was on, or to the name when that is no longer allowed', () => {
  assert.equal(flow.resumeStep('sources', GOOD), 'sources');
  assert.equal(flow.resumeStep('review', BAD), 'name');
  assert.equal(flow.resumeStep('bogus', GOOD), 'name');
  assert.equal(flow.resumeStep('name', BAD), 'name');
});

test('the progress strip numbers the steps and says which can be chosen', () => {
  const fresh = flow.stepViews('name', BAD);
  assert.deepEqual(fresh.map((v) => [v.number, v.state, v.enabled]), [[1, 'current', true], [2, 'locked', false], [3, 'locked', false]]);
  const named = flow.stepViews('name', GOOD);
  assert.deepEqual(named.map((v) => v.state), ['current', 'open', 'open']);
  const later = flow.stepViews('review', GOOD);
  assert.deepEqual(later.map((v) => v.state), ['done', 'done', 'current']);
  assert.equal(later.filter((v) => v.state === 'current').length, 1);
});

test('a draft with nothing in it is not dirty; any entry makes it dirty', () => {
  assert.equal(flow.isDirty({ title: '', envText: '', sources: [] }), false);
  assert.equal(flow.isDirty({ title: '  ', envText: ' , ,', sources: [] }), false);
  assert.equal(flow.isDirty({ title: 'a', envText: '', sources: [] }), true);
  assert.equal(flow.isDirty({ title: '', envText: 'Prod', sources: [] }), true);
  assert.equal(flow.isDirty({ title: '', envText: '', sources: [{ kind: 'x' }] }), true);
});

// ---- the draft

test('the draft starts blank on the first step', () => {
  store.resetWizard();
  assert.deepEqual({ ...store.wizard }, { step: 'name', title: '', envText: '', sources: [] });
  assert.equal(store.wizardIsDirty(), false);
});

test('Next is refused with a bad name and the step stays', () => {
  store.resetWizard();
  assert.equal(store.nextStep(), false);
  assert.equal(store.wizard.step, 'name');
  store.wizard.title = 'a';
  assert.equal(store.goToStep('review'), false);
  assert.equal(store.wizard.step, 'name');
});

test('Back and Next keep every entry', () => {
  store.resetWizard();
  store.wizard.title = '  Payroll   week 42 ';
  store.wizard.envText = 'Client A production, Client A staging';
  assert.equal(store.nextStep(), true);
  assert.equal(store.wizard.step, 'sources');
  store.setSources([{ kind: 'iis', ref: 'site-1', label: 'Site 1', env: 'Client A production', portal: '', api: '', classification: '' }]);
  assert.equal(store.nextStep(), true);
  assert.equal(store.wizard.step, 'review');
  assert.equal(store.nextStep(), false, 'no step after the last');
  assert.equal(store.backStep(), true);
  assert.equal(store.backStep(), true);
  assert.equal(store.wizard.step, 'name');
  assert.equal(store.backStep(), false, 'no step before the first');
  assert.equal(store.wizard.title, '  Payroll   week 42 ');
  assert.equal(store.wizard.envText, 'Client A production, Client A staging');
  assert.equal(store.wizard.sources.length, 1);
});

test('the name can be cleared after going back, and then forward is refused again', () => {
  store.resetWizard();
  store.wizard.title = 'Week 42';
  store.nextStep();
  store.backStep();
  store.wizard.title = '';
  assert.equal(store.nextStep(), false);
  assert.equal(store.wizard.step, 'name');
});

test('the snapshot is a clean copy: trimmed name, parsed environments, copied sources', () => {
  store.resetWizard();
  store.wizard.title = '  Week \n 42 ';
  store.wizard.envText = 'A prod, a PROD, B';
  store.setSources([{ kind: 'mongo', ref: 'db1', label: 'DB 1', env: 'A prod', portal: 'Portal 1', api: 'Api 1', classification: 'audit' }]);
  const snap = store.draftSnapshot();
  assert.equal(snap.title, 'Week 42');
  assert.deepEqual(snap.envs, ['A prod', 'B']);
  assert.equal(snap.sources.length, 1);
  snap.sources[0].env = 'changed';
  snap.title = 'changed';
  assert.equal(store.wizard.sources[0].env, 'A prod', 'a change to the snapshot does not reach the draft');
  assert.equal(store.draftSnapshot().title, 'Week 42');
});

test('the environments may be blank', () => {
  store.resetWizard();
  store.wizard.title = 'Week 42';
  assert.deepEqual(store.draftSnapshot().envs, []);
  assert.equal(store.nextStep(), true);
});

test('setSources copies the list, so later edits to the caller list do not leak in', () => {
  store.resetWizard();
  const list = [{ kind: 'iis', ref: 'r', label: 'L', env: '', portal: '', api: '', classification: '' }];
  store.setSources(list);
  list[0].env = 'leak';
  list.push({ kind: 'x', ref: 'y', label: 'z', env: '', portal: '', api: '', classification: '' });
  assert.equal(store.wizard.sources.length, 1);
  assert.equal(store.wizard.sources[0].env, '');
});

test('resumeWizard moves a draft with a bad name back to the first step', () => {
  store.resetWizard();
  store.wizard.title = 'Week 42';
  store.nextStep();
  store.wizard.title = '';
  store.resumeWizard();
  assert.equal(store.wizard.step, 'name');
});

test('resetWizard throws everything away', () => {
  store.wizard.title = 'Week 42';
  store.wizard.envText = 'A';
  store.nextStep();
  store.setSources([{ kind: 'iis', ref: 'r', label: 'L', env: '', portal: '', api: '', classification: '' }]);
  assert.equal(store.wizardIsDirty(), true);
  store.resetWizard();
  assert.deepEqual({ ...store.wizard }, { step: 'name', title: '', envText: '', sources: [] });
  assert.equal(store.wizardIsDirty(), false);
});

// ---- copy

test('the wizard copy passes the plain-words lint', () => {
  store.resetWizard();
  assert.deepEqual(lintScreenStrings(copy.wizardLintEntries(), deck.terms).map((f) => f.id + ': ' + f.message), []);
});

test('the copy says nothing is saved until the last step, and that environments are optional', () => {
  assert.match(copy.WIZARD_TEXT.stepsNote, /Nothing is saved until the last step/);
  assert.match(copy.WIZARD_TEXT.envsLabel, /optional/i);
  assert.match(copy.WIZARD_TEXT.envsHint, /blank/i);
  assert.match(copy.WIZARD_TEXT.envsTag, /environment, then a portal, then an API/);
});

test('the leave question says the work is thrown away and offers a way to stay', () => {
  assert.match(copy.WIZARD_TEXT.leaveAskText, /thrown away/);
  assert.equal(copy.WIZARD_TEXT.leaveNo, 'Keep working');
  assert.equal(copy.WIZARD_TEXT.leaveYes, 'Leave and discard');
});

test('built sentences', () => {
  assert.equal(copy.stepOfText(2, 3, 'Sources'), 'Step 2 of 3: Sources');
  assert.equal(copy.chosenText(0), '0 sources chosen');
  assert.equal(copy.chosenText(1), '1 source chosen');
  assert.equal(copy.chosenText(4), '4 sources chosen');
  assert.equal(copy.envCountText(1), '1 environment');
  assert.equal(copy.envCountText(3), '3 environments');
  assert.equal(copy.titleCountText(12, 80), '12 of 80');
  assert.equal(copy.nameList(['A', 'B', 'C']), 'A, B, C');
  assert.equal(copy.nameList(['A', 'B', 'C', 'D', 'E']), 'A, B, C and 2 more');
});

test('the step labels tell a screen reader the state', () => {
  assert.match(copy.stepButtonLabel(1, 'Name', 'current'), /current step/);
  assert.match(copy.stepButtonLabel(2, 'Sources', 'locked'), /not available until the case has a name/);
  assert.match(copy.stepButtonLabel(1, 'Name', 'done'), /done/);
  assert.equal(copy.stepButtonLabel(2, 'Sources', 'open'), 'Step 2, Sources');
});

test('notes about changed environments are empty when nothing changed, and name each change otherwise', () => {
  assert.deepEqual(copy.envNotes({ duplicates: [], overflow: [], shortened: [], changed: [] }), []);
  const notes = copy.envNotes({ duplicates: ['A'], overflow: ['B'], shortened: ['C'], changed: ['D'] });
  assert.equal(notes.length, 4);
  assert.match(notes[1], /32/);
  assert.match(notes[2], /60/);
});

// ---- the component and its wiring

test('the shell has the three slots and the props the next tasks use', () => {
  const src = read('src/components/CreateWizard.svelte');
  assert.match(src, /sources\?: Snippet/);
  assert.match(src, /review\?: Snippet/);
  assert.match(src, /finish\?: Snippet/);
  assert.match(src, /busy\?: boolean/);
  assert.match(src, /onLeave: \(\) => void/);
  assert.match(src, /\{@render sources\(\)\}/);
  assert.match(src, /\{@render review\(\)\}/);
  assert.match(src, /\{@render finish\(\)\}/);
});

test('the progress strip is a named list of buttons that marks the current step', () => {
  const src = read('src/components/CreateWizard.svelte');
  assert.match(src, /<nav class="steps" aria-label=\{WIZARD_TEXT\.stepsLabel\}>/);
  assert.match(src, /aria-current=\{view\.state === 'current' \? 'step' : undefined\}/);
  assert.match(src, /aria-label=\{stepButtonLabel\(/);
  assert.match(src, /<h3 class="step-heading" tabindex="-1"/, 'focus moves to the step heading');
});

test('the name field is labelled, described, and flags a problem to a screen reader', () => {
  const src = read('src/components/CreateWizard.svelte');
  assert.match(src, /<label for=\{titleFieldId\}>/);
  assert.match(src, /aria-describedby=\{titleHintId \+/);
  assert.match(src, /aria-invalid=\{showProblem \? true : undefined\}/);
  assert.match(src, /role="alert"/);
  assert.match(src, /maxlength=\{ENV_FIELD_MAX\}/);
});

test('leaving asks first when there is something to lose, moves focus to the safe button, and closes on Escape', () => {
  const src = read('src/components/CreateWizard.svelte');
  assert.match(src, /if \(!dirty\) \{ doLeave\(\); return; \}/);
  assert.match(src, /keepButton\?\.focus\(\)/);
  assert.match(src, /event\.key === 'Escape'/);
  assert.match(src, /beforeunload/);
});

test('the shell never talks to the server or the browser store', () => {
  for (const file of ['src/components/CreateWizard.svelte', 'src/lib/wizard.svelte.ts', 'src/lib/wizardFlow.ts', 'src/lib/wizardEnvs.ts', 'src/lib/copy/wizardCopy.ts']) {
    const src = read(file);
    assert.doesNotMatch(src, /\bfetch\(|XMLHttpRequest|localStorage|sessionStorage|indexedDB|from '\.\.?\/(?:lib\/)?api'/, file);
  }
});

test('a draft source has no field for a password, key or connection string', () => {
  const src = read('src/lib/wizardFlow.ts');
  const shape = src.slice(src.indexOf('export interface DraftSource'), src.indexOf('export const stepIndex'));
  assert.doesNotMatch(shape.replace(/\/\*[\s\S]*?\*\//g, ''), /password|passphrase|connectionString|secret|token/i);
});

test('the create route reaches the shell through the Landing screen', () => {
  assert.match(read('src/components/Landing.svelte'), /mode === 'create'\}\s*<CreateWizard onLeave=\{goHome\} busy=\{[^}]+\}>\s*\{#snippet sources\(\)\}<SourceGallery \/>\{\/snippet\}\s*\{#snippet review\(\)\}[\s\S]*?\{#snippet finish\(\)\}[\s\S]*?<\/CreateWizard>/);
  assert.match(read('src/App.svelte'), /app\.screen === 'create'[\s\S]{0,300}<Landing mode="create" \/>/);
});

test('the help notes for the wizard exist and are used by the component', () => {
  const help = read('src/lib/copy/helpCopy.ts');
  assert.match(help, /\bwizard: \{/);
  assert.match(help, /'wizard-envs': \{/);
  const src = read('src/components/CreateWizard.svelte');
  assert.match(src, /<HelpNote id="wizard" \/>/);
  assert.match(src, /<HelpNote id="wizard-envs" \/>/);
});
