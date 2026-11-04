const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Job B fixes: a cancelled file read stays cancelled (C7), a locked drawer row locks its computer choice (C8), removing a row keeps
// the keyboard focus (C9), Mongo checkbox ids use the row's place (C6), the Mongo tick records outlive the form (C4), and an Add tags
// the case as it was when Add was pressed (P3). The store runs with a stand-in for the $state rune; the components are checked as text.

const root = path.resolve(__dirname, '..');
const cache = new Map();
// One shared module cache, so the store and this test see the same `wizard` object.
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

// The body of one component function, from its header to its closing brace at two-space indent.
const body = (text, header) => {
  const at = text.indexOf(header);
  assert.ok(at >= 0, 'missing: ' + header);
  return text.slice(at, text.indexOf('\n  }', at));
};

const model = loadTypeScript('src/lib/wizardSources.ts');
const store = loadTypeScript('src/lib/wizardSources.svelte.ts');
const draft = loadTypeScript('src/lib/wizard.svelte.ts');
const held = loadTypeScript('src/lib/wizardConnections.ts');
const { wizard } = draft;
const { sourcesView } = store;

const panel = read('src/components/wizard/ImportFilesPanel.svelte');
const zone = read('src/components/wizard/ZoneChoice.svelte');
const mongo = read('src/components/wizard/MongoClassify.svelte');
const addPage = read('src/components/sources/AddAuditDatabase.svelte');

// ---- C7: Clear and Add cancel a read that is still running

// Hub fix D (F37): the list and its reads moved from the panel into the staging store (wizardFiles.svelte.ts), so they outlive the panel.
const staging = read('src/lib/wizardFiles.svelte.ts');

test('C7: clear() and add() cancel a read in flight and take over the state', () => {
  for (const header of ['function clear()', 'async function add()']) {
    assert.match(body(panel, header), /staging\.clear\(\)/, header + ' ends the reads and empties the list through the store');
  }
  assert.match(staging, /const cancel = \(\) => \{ epoch \+= 1; pending = 0; state\.reading = false; \};/, 'the store marks the cancellation, owns the count and the reading flag');
  assert.match(staging, /clear\(\) \{ cancel\(\); state\.list = \[\]; state\.fromFolder = false; \}/, 'clear cancels and empties');
});

test('C7: a stale read returns before it touches the list, the note or the reading flag', () => {
  const take = body(panel, 'async function take(');
  assert.ok(take.indexOf('if (!staging.current(mine)) return') >= 0, 'take checks the epoch after its awaits');
  assert.ok(take.indexOf('if (!staging.current(mine)) return') < take.indexOf('staging.append('), 'the stale return comes before the append');
  const drop = body(panel, 'async function onDrop(');
  assert.ok(drop.indexOf('if (!staging.current(mine)) return') >= 0, 'the drop checks the epoch after the walk');
  assert.match(drop, /take\(dropped\.files, dropped\.folder, parts, mine\)/, 'the drop hands its own read to take, so one logical read counts once');
  assert.match(take, /owner \?\? staging\.beginRead\(\)/);
  assert.match(take, /if \(owner === null\) staging\.endRead\(mine\)/, 'a handed-over read is not counted twice');
});

test('C7: the reading flag comes from the count of reads in flight, not from whichever read ends first', () => {
  assert.match(staging, /pending \+= 1/);
  assert.match(staging, /pending -= 1/);
  assert.match(staging, /state\.reading = pending > 0/);
  for (const header of ['async function take(', 'async function expand(', 'async function onDrop(']) {
    assert.doesNotMatch(body(panel, header), /reading = false/, header + ' does not clear the flag unconditionally');
  }
});

// ---- C8: a locked drawer row locks its computer choice

test('C8: ZoneChoice has a disabled prop, off by default, and it locks every control', () => {
  assert.match(zone, /disabled\?: boolean/);
  assert.match(zone, /disabled = false/);
  // hubfixC F45: the controls are counted exactly, so a control added later without the lock is noticed (the title promised "every control").
  assert.match(zone, /<select [^>]*value=\{host\} disabled=\{disabled \|\| saving\}/, 'the computer select is locked by the row and while a computer is being saved');
  const buttons = zone.match(/<button\b[^\r\n]*/g) ?? [];
  const locked = buttons.filter((tag) => /disabled=\{saving \|\| disabled\}/.test(tag));
  assert.equal(locked.length, 3, 'the save-zone, Save and Cancel buttons are the locked buttons');
  assert.equal(buttons.filter((tag) => !/disabled=\{saving \|\| disabled\}/.test(tag) && !/loadHosts\(true\)/.test(tag)).length, 0, 'no other button is left unlocked (the retry of the list is read only)');
  const inputs = zone.match(/<input\b[^\r\n]*/g) ?? [];
  assert.equal(inputs.length, 3, 'the zone-only, name and zone inputs are all the inputs');
  assert.ok(inputs.every((tag) => /disabled=\{disabled\}/.test(tag)), 'every input is locked');
});

test('hubfixC F7: a computer being saved cannot be un-picked meanwhile, and a stale save does not pick it', () => {
  const save = body(zone, 'async function save(');
  assert.match(save, /const pickedBefore = host;/, 'the pick at the start of the save is remembered');
  assert.ok(save.indexOf('const pickedBefore = host') < save.indexOf('await saveHost('), 'it is taken before the wait');
  assert.match(save, /if \(host === pickedBefore\) onChange\(result\.label\);/, 'the new computer is picked only if the pick is unchanged');
});

test('C8: the drawer passes the row lock to its ZoneChoice', () => {
  assert.match(panel, /<ZoneChoice kind=\{item\.choice\} host=\{item\.host\} name=\{item\.name\} onChange=\{\(label\) => setHost\(item\.id, label\)\} disabled=\{choiceLocked\(item\)\} \/>/);
  assert.match(panel, /const choiceLocked = \(item: StagedFile\): boolean => locked\(item\) \|\| finished\(item\);/, 'a kept file is locked too');
});

// ---- C9: removing a row keeps the keyboard focus

test('C9: removeRow waits for the DOM and moves focus to the button that took the row\'s place', () => {
  const fn = body(panel, 'async function removeRow(');
  assert.ok(fn.indexOf('const at = list.findIndex') >= 0 && fn.indexOf('const at = list.findIndex') < fn.indexOf('staging.setList(list.filter'), 'the place is taken before the filter');
  assert.match(fn, /await tick\(\)/);
  assert.match(fn, /querySelectorAll<HTMLButtonElement>\('\[data-remove-row\]'\)/, 'the Remove buttons are found in DOM order');
  assert.match(fn, /find\(\(button\) => !button\.disabled\)/, 'a Remove button of a locked row (disabled, cannot take the focus) is skipped');
  assert.match(fn, /\(target \?\? chooseButton\)\?\.focus\(\)/, 'with no row left the choose-files button gets the focus');
  assert.match(panel, /data-remove-row disabled=\{locked\(item\)\}/, 'the Remove button is marked');
  assert.match(panel, /<div class="files"[^>]* bind:this=\{rootEl\}>/, 'the panel root is bound');
  assert.match(panel, /bind:this=\{chooseButton\}/, 'the choose-files button is bound');
});

// ---- C6: a Mongo checkbox id uses the row's place, not its name

test('C6: the database row id uses the loop index', () => {
  assert.ok(!mongo.includes('.replace(/[^A-Za-z0-9_-]/g'), 'the name-based id construction is gone');
  assert.match(mongo, /\{#each connection\.databases as database, i \(database\)\}/);
  assert.match(mongo, /rowId = uid \+ '-' \+ connection\.id \+ '-' \+ i/);
});

test('C6: the new id scheme keeps non-ASCII database names apart', () => {
  const names = ['租户', '审计'];
  const ids = names.map((database, i) => 'u1' + '-' + 'c1' + '-' + i);
  assert.equal(new Set(ids).size, names.length, 'one id per row');
  // The old scheme collapsed both names to the same id:
  assert.equal(names[0].replace(/[^A-Za-z0-9_-]/g, '_'), names[1].replace(/[^A-Za-z0-9_-]/g, '_'));
});

// ---- C4: the Mongo tick records live in the step state, so a remount keeps them

test('C4: the step state starts with empty set-aside, also-app and suggested records', () => {
  draft.resetWizard();
  assert.deepEqual(sourcesView.setAside, {});
  assert.deepEqual(sourcesView.alsoApp, {});
  assert.deepEqual(sourcesView.suggested, {});
});

test('C4: a database the person set aside is not offered the audit tick by name again', () => {
  draft.resetWizard();
  assert.deepEqual(model.defaultAuditDatabases(['BrainAudit'], () => false), ['BrainAudit'], 'untouched, the name suggests the tick');
  sourcesView.setAside['c1/BrainAudit'] = true;
  // The same default the component's tickAuditByName uses.
  const offered = model.defaultAuditDatabases(['BrainAudit', 'orders'], (d) => model.databaseClass(wizard.sources, 'c1', d) !== '' || !!sourcesView.setAside['c1/' + d]);
  assert.deepEqual(offered, [], 'the untick sticks across a remount and a new test');
});

test('C4: removing a connection forgets its own ticks and keeps the others', () => {
  draft.resetWizard();
  for (const record of [sourcesView.setAside, sourcesView.alsoApp, sourcesView.suggested]) {
    record['c1/a'] = true;
    record['c2/b'] = true;
  }
  store.removeConnection('c1');
  assert.deepEqual(sourcesView.setAside, { 'c2/b': true });
  assert.deepEqual(sourcesView.alsoApp, { 'c2/b': true });
  assert.deepEqual(sourcesView.suggested, { 'c2/b': true });
});

test('C4: testing a connection again forgets the ticks of databases that are gone', async () => {
  draft.resetWizard();
  const input = { uri: 'mongodb://admin:hunter22@db1:27017', username: 'admin', password: 'hunter22', authSource: '', tlsMode: 'uri', caCertificate: '' };
  const first = await store.testMongo(input, '', { testConnection: async () => ({ databases: ['a', 'b'] }) });
  assert.equal(first.ok, true);
  sourcesView.setAside[first.id + '/a'] = true;
  sourcesView.setAside[first.id + '/b'] = true;
  sourcesView.alsoApp[first.id + '/b'] = true;
  sourcesView.suggested[first.id + '/b'] = true;
  const again = await store.testMongo(held.heldConnection(first.id), first.id, { testConnection: async () => ({ databases: ['a'] }) });
  assert.equal(again.ok, true);
  assert.equal(again.id, first.id);
  assert.deepEqual(sourcesView.setAside, { [first.id + '/a']: true }, 'the kept database keeps its mark');
  assert.deepEqual(sourcesView.alsoApp, {});
  assert.deepEqual(sourcesView.suggested, {});
});

test('C4: resetting the sources step clears the tick records', () => {
  draft.resetWizard();
  sourcesView.setAside['c1/a'] = true;
  sourcesView.alsoApp['c1/a'] = true;
  sourcesView.suggested['c1/a'] = true;
  store.resetSources();
  assert.deepEqual(sourcesView.setAside, {});
  assert.deepEqual(sourcesView.alsoApp, {});
  assert.deepEqual(sourcesView.suggested, {});
});

test('C4: the Mongo form keeps no local tick records of its own', () => {
  assert.doesNotMatch(mongo, /let setAside = \$state/);
  assert.doesNotMatch(mongo, /let alsoApp = \$state/);
  assert.doesNotMatch(mongo, /let suggested = \$state/);
  assert.match(mongo, /sourcesView\.setAside\[/);
  assert.match(mongo, /sourcesView\.alsoApp\[/);
  assert.match(mongo, /sourcesView\.suggested\[/);
});

// ---- P3: the tag goes to the case as it was when Add was pressed

test('P3: tag() takes the case id as a parameter and uses it', () => {
  assert.match(addPage, /async function tag\(cls: 'audit' \| 'other', h: ConnectionInput \| null, id: string\)/);
  assert.match(addPage, /postSourceTag\(id, /, 'the parameter is sent, not the prop');
  assert.doesNotMatch(body(addPage, 'async function tag('), /postSourceTag\(caseId/);
});

test('P3: add() reads the case id once, before the first await', () => {
  const fn = body(addPage, 'async function add()');
  assert.ok(fn.indexOf('const id = caseId;') >= 0, 'the case id is captured');
  assert.ok(fn.indexOf('const id = caseId;') < fn.indexOf('await'), 'the capture comes before the first await');
  assert.match(fn, /await tag\(cls, h, id\)/);
});
