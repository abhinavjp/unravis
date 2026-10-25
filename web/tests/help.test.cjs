const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
// `runes: true` gives a .svelte.ts store a plain stand-in for $state, so its logic runs under node.
function loadTypeScript(relativePath, runes = false) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, runes ? (value) => value : undefined);
  return module.exports;
}
const read = (relativePath) => fs.readFileSync(path.join(root, relativePath), 'utf8');

const { help } = loadTypeScript('src/lib/copy/helpCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

// The field ids come from the group table of the More filters plan, plus the six range rows (rf-{key}).
const GROUPS = ['when', 'what', 'details', 'ranges', 'chart'];
const WHEN_FIELDS = ['current-range', 'reference-range', 'current-start', 'current-end', 'reference-start', 'reference-end'];
const WHAT_FIELDS = ['outcome', 'rf-completion', 'controller', 'action', 'rf-operation', 'kind-unknown'];
const DETAIL_FIELDS = ['rf-user-ids', 'rf-ui-versions', 'rf-user-ips', 'rf-status-codes', 'rf-agent', 'rf-url', 'rf-audit-id'];
const RANGE_ROWS = ['observedMs', 'apiTimeMs', 'auditCreateTimeMs', 'requestKb', 'responseKb', 'incompleteAgeMs'].map((key) => 'rf-' + key);
const CHART_FIELDS = ['metric', 'bucket', 'latency-target'];
const SCREENS = ['drill-navigator', 'drill-investigate', 'drill-back', 'pulse', 'where', 'inspector', 'resource', 'patterns', 'ledger', 'export', 'import', 'discover'];
const REQUIRED = [...GROUPS, ...WHEN_FIELDS, ...WHAT_FIELDS, ...DETAIL_FIELDS, ...RANGE_ROWS, ...CHART_FIELDS, ...SCREENS];
const sentences = (text) => text.split(/[.!?]+(?=\s|$)/).filter((part) => /[A-Za-z0-9]/.test(part)).length;

test('every required help key exists with a plain line and a simple explanation', () => {
  for (const key of REQUIRED) {
    assert.ok(Object.hasOwn(help, key), 'missing help key ' + key);
    assert.equal(typeof help[key].plain, 'string', key);
    assert.equal(typeof help[key].simple, 'string', key);
    assert.ok(help[key].plain.trim().length > 0, key + ' plain');
    assert.ok(help[key].simple.trim().length > 0, key + ' simple');
  }
});

test('the range keys match the real RANGE_FIELDS keys', () => {
  const source = read('src/lib/investigation.ts');
  const block = source.slice(source.indexOf('export const RANGE_FIELDS'), source.indexOf('const NUMBER_TEXT'));
  const keys = [...block.matchAll(/key: '(\w+)'/g)].map((m) => 'rf-' + m[1]);
  assert.deepEqual(keys, RANGE_ROWS);
});

test('plain is one sentence and simple is at most two', () => {
  for (const [key, entry] of Object.entries(help)) {
    assert.equal(sentences(entry.plain), 1, key + ' plain: ' + entry.plain);
    assert.match(entry.plain.trim(), /[.!?]$/, key + ' plain ends a sentence');
    const n = sentences(entry.simple);
    assert.ok(n >= 1 && n <= 2, key + ' simple has ' + n + ' sentences');
    assert.notEqual(entry.plain, entry.simple, key + ' simple adds something');
  }
});

test('the table holds only the required keys, so no id is orphaned', () => {
  assert.deepEqual(Object.keys(help).sort(), [...REQUIRED].sort());
});

test('every help string passes the copy lint, including the banned words', () => {
  const entries = Object.entries(help).flatMap(([key, entry]) => [{ id: key + '.plain', text: entry.plain }, { id: key + '.simple', text: entry.simple }]);
  assert.deepEqual(lintScreenStrings(entries, deck.terms).map((f) => f.id + ': ' + f.message), []);
  // The lint only fires on what it knows, so the named words are also checked directly.
  const banned = /\b(?:cause|caused|causes|because|due to|waiting on|blocked by|led to|result in|proof|prove|proven)\b/i;
  for (const entry of entries) assert.doesNotMatch(entry.text, banned, entry.id);
});

test('help text keeps the glossary meanings', () => {
  const text = (key) => help[key].plain + ' ' + help[key].simple;
  // Observed time runs from the request start to its completion; API time is the action itself.
  assert.match(text('rf-observedMs'), /start/i);
  assert.match(text('rf-observedMs'), /complet/i);
  assert.match(text('rf-apiTimeMs'), /action/i);
  // Completion: an incomplete request may simply have been recorded late, and is not called a failure.
  assert.match(text('rf-completion'), /recorded late/i);
  assert.doesNotMatch(text('rf-completion'), /\b(?:failed|stuck)\b/i);
  // An unknown polling type is an HTTP request with no polling type recorded, not Synchronous or Asynchronous.
  assert.match(text('kind-unknown'), /polling type/i);
  assert.match(text('kind-unknown'), /Synchronous or Asynchronous/);
  // 4xx and 5xx follow the glossary hint.
  assert.match(text('outcome'), /4xx/);
  assert.match(text('outcome'), /5xx/);
  // No new glossary terms: nothing in the help layer adds an entry to the deck.
  assert.doesNotMatch(read('src/lib/copy/helpCopy.ts'), /\bdeck\b|terms:\s*\[/);
});

test('HelpNote renders the plain line and a labelled disclosure button', () => {
  const src = read('src/components/HelpNote.svelte');
  assert.match(src, /let \{ id \}: \{ id: string \} = \$props\(\)/);
  assert.match(src, /import \{ help \} from '\.\.\/lib\/copy\/helpCopy'/);
  assert.match(src, /import \{ eli10 \} from '\.\.\/lib\/eli10\.svelte'/);
  assert.match(src, /\{entry\.plain\}/);
  assert.match(src, /\{entry\.simple\}/);
  assert.match(src, /<button type="button"[^>]*aria-expanded=\{shown\}/);
  assert.match(src, /aria-controls=\{blockId\}/);
  assert.match(src, /id=\{blockId\}/);
  assert.match(src, />In simple words</);
  assert.match(src, /eli10\.on \|\| open/, 'the shared flag opens every block');
  assert.match(src, /hidden=\{!shown\}/, 'the block stays mounted so aria-controls always resolves');
  assert.match(src, />\?<\/button>/);
  assert.doesNotMatch(src, /\{@html|innerHTML/);
});

test('HelpNote styling uses tokens only and adds no animation', () => {
  const src = read('src/components/HelpNote.svelte');
  const style = src.slice(src.indexOf('<style>'));
  assert.ok(style.length > 10);
  assert.doesNotMatch(style, /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/);
  assert.doesNotMatch(style, /animation|transition|@keyframes/);
  assert.match(style, /var\(--text-2\)/);
  assert.match(style, /:focus-visible/);
});

test('every HelpNote id used in a component exists in the table', () => {
  const dir = path.join(root, 'src');
  const files = [];
  (function walk(d) { for (const e of fs.readdirSync(d, { withFileTypes: true })) { const p = path.join(d, e.name); if (e.isDirectory()) walk(p); else if (p.endsWith('.svelte')) files.push(p); } })(dir);
  let uses = 0;
  for (const file of files) {
    const src = fs.readFileSync(file, 'utf8');
    for (const m of src.matchAll(/<HelpNote\s+id="([^"]+)"/g)) { uses++; assert.ok(Object.hasOwn(help, m[1]), path.basename(file) + ' uses unknown help id ' + m[1]); }
    assert.doesNotMatch(src.replace(/<HelpNote\s+id="[^"]+"/g, ''), /<HelpNote\s+id=\{/, path.basename(file) + ' uses a dynamic help id the test cannot check');
  }
  assert.ok(uses >= SCREEN_KEYS.length);
});

test('Eli10Toggle is a labelled checkbox bound to the shared flag and is mounted in the header', () => {
  const src = read('src/components/Eli10Toggle.svelte');
  assert.match(src, /<input type="checkbox"[^>]*checked=\{eli10\.on\}/);
  assert.match(src, /onchange=\{[^}]*eli10\.set\(/);
  assert.match(src, /Explain simply/);
  assert.match(src, /<label/);
  assert.doesNotMatch(src.slice(src.indexOf('<style>')), /#[0-9a-fA-F]{3,8}\b|rgba?\(|animation|transition/);
  assert.match(read('src/components/AppHeader.svelte'), /import Eli10Toggle from '\.\/Eli10Toggle\.svelte'/);
  assert.match(read('src/components/AppHeader.svelte'), /<Eli10Toggle\b/);
});

// A fake storage on globalThis, restored after each test.
function withStorage(storage, run) {
  const had = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, get: storage });
  try { return run(); } finally { if (had) Object.defineProperty(globalThis, 'localStorage', had); else delete globalThis.localStorage; }
}
const throwing = () => { throw new Error('storage is blocked'); };
const memoryStorage = () => { const map = new Map(); return { getItem: (k) => map.has(k) ? map.get(k) : null, setItem: (k, v) => map.set(k, String(v)), removeItem: (k) => map.delete(k), map }; };
const loadStore = () => loadTypeScript('src/lib/eli10.svelte.ts', true).eli10;

test('the eli10 flag starts off, and works in memory when localStorage throws', () => {
  withStorage(throwing, () => {
    const eli10 = loadStore();
    assert.equal(eli10.on, false);
    assert.doesNotThrow(() => eli10.set(true));
    assert.equal(eli10.on, true, 'kept in memory');
    eli10.toggle();
    assert.equal(eli10.on, false);
    eli10.toggle();
    assert.equal(eli10.on, true);
  });
});

test('the eli10 flag is saved and read back when storage works', () => {
  const storage = memoryStorage();
  withStorage(() => storage, () => {
    const first = loadStore();
    assert.equal(first.on, false);
    first.set(true);
    assert.equal(first.on, true);
    assert.ok([...storage.map.values()].includes('1'));
    assert.equal(loadStore().on, true, 'a fresh load reads the saved flag');
    first.set(false);
    assert.equal(loadStore().on, false);
  });
});

test('a storage that throws only on write still reads the saved flag', () => {
  const storage = memoryStorage();
  storage.setItem('unravis.eli10', '1');
  const broken = { getItem: storage.getItem, setItem: throwing, removeItem: throwing };
  withStorage(() => broken, () => {
    const eli10 = loadStore();
    assert.equal(eli10.on, true);
    assert.doesNotThrow(() => eli10.set(false));
    assert.equal(eli10.on, false);
  });
});

test('the eli10 store reads storage only in try/catch and is a rune store', () => {
  const src = read('src/lib/eli10.svelte.ts');
  assert.match(src, /\$state\(/);
  assert.equal((src.match(/localStorage\./g) ?? []).length, (src.match(/try \{[^}]*localStorage\./g) ?? []).length, 'every localStorage use sits inside a try');
  assert.doesNotMatch(src, /fetch\(|document\.cookie|sessionStorage/);
});

test('new files keep CRLF line endings', () => {
  for (const file of ['src/lib/copy/helpCopy.ts', 'src/lib/eli10.svelte.ts', 'src/components/HelpNote.svelte', 'src/components/Eli10Toggle.svelte', 'tests/help.test.cjs']) {
    const text = read(file);
    assert.ok(!/(?<!\r)\n/.test(text), file + ' has a bare LF');
  }
});

// The nine screens and panels that carry a note, one component each, directly under the title.
const SCREEN_KEYS = ['pulse', 'where', 'inspector', 'resource', 'patterns', 'ledger', 'export', 'import', 'discover'];
const sveltePaths = () => {
  const files = [];
  (function walk(d) { for (const e of fs.readdirSync(d, { withFileTypes: true })) { const p = path.join(d, e.name); if (e.isDirectory()) walk(p); else if (p.endsWith('.svelte')) files.push(p); } })(path.join(root, 'src'));
  return files;
};

test('each screen help key is used by exactly one component, and only once in it', () => {
  const users = {};
  for (const file of sveltePaths()) {
    const src = fs.readFileSync(file, 'utf8');
    for (const m of src.matchAll(/<HelpNote\s+id="([^"]+)"/g)) (users[m[1]] ??= []).push(path.basename(file));
  }
  const expected = { pulse: 'Pulse', where: 'WhereView', inspector: 'Inspector', resource: 'ResourcePanel', patterns: 'PatternsPanel', ledger: 'LedgerPanel', export: 'ExportPanel', discover: 'DiscoverPanel', import: 'ImportPanel' };
  for (const key of SCREEN_KEYS) {
    assert.deepEqual(users[key], [expected[key] + '.svelte'], key + ' is used once, in ' + expected[key]);
  }
});

test('every component that mounts a HelpNote imports it', () => {
  for (const file of sveltePaths()) {
    const src = fs.readFileSync(file, 'utf8');
    if (/<HelpNote\b/.test(src)) assert.match(src, /import HelpNote from '[./]+(?:components\/)?HelpNote\.svelte'/, path.basename(file) + ' imports HelpNote');
  }
});

test('the header folds Explain simply into the More menu as a menu item checkbox', () => {
  const header = read('src/components/AppHeader.svelte');
  assert.match(header, /<Eli10Toggle menu \/>/, 'the folded menu holds the switch');
  assert.match(header, /\[role="menuitemcheckbox"\]/, 'arrow keys reach it');
  assert.match(read('src/components/Eli10Toggle.svelte'), /role=\{menu \? 'menuitemcheckbox' : undefined\}/);
});

test('Enter toggles the Explain simply menu item, and arrow keys still move', () => {
  const header = read('src/components/AppHeader.svelte');
  const handler = header.slice(header.indexOf('function onMenuKey'), header.indexOf('$effect'));
  assert.match(handler, /event\.key === 'Enter'[^\n]*type === 'checkbox'[^\n]*click\(\)/, 'Enter clicks the focused checkbox');
  assert.ok(handler.indexOf("'Enter'") < handler.indexOf('nextMenuIndex('), 'Enter is handled before the arrow keys');
  assert.match(handler, /nextMenuIndex\(/);
});

test('the header keeps the reference time from 1200px and shortens the switch label below 1320px', () => {
  const header = read('src/components/AppHeader.svelte');
  assert.match(header, /@media \(min-width: 1200px\) \{ \.reference \{ display: inline; \}/);
  const toggle = read('src/components/Eli10Toggle.svelte');
  assert.match(toggle, /max-width: 1319px/);
  assert.match(toggle, /<span class="short" aria-hidden="true">/);
  assert.match(toggle, /<span class="full">Explain simply<\/span>/, 'the full name stays in the accessible name');
  assert.doesNotMatch(toggle.slice(toggle.indexOf('<style>')), /display: none[^}]*\.full|\.full[^}]*display: none/, 'the full label is clipped, not removed');
});

// ---- Wiring: every key in the help table is reached by a screen ----
// Keys kept on purpose with no component, each with a reason. None today.
const INTENTIONALLY_UNUSED = {};
const usedHelpIds = () => {
  const used = new Set();
  const block = read('src/lib/investigation.ts').match(/export const RANGE_FIELDS[\s\S]*?const NUMBER_TEXT/)[0];
  const rangeKeys = [...block.matchAll(/key: '(\w+)'/g)].map((m) => m[1]);
  for (const file of sveltePaths()) {
    const src = fs.readFileSync(file, 'utf8');
    for (const m of src.matchAll(/<(?:HelpNote|FieldHelp)\s+id="([^"]+)"/g)) used.add(m[1]);
    // A range row passes its id as 'rf-' + field.key inside the RANGE_FIELDS loop, so every range key is used there.
    if (/<FieldHelp\s+id=\{'rf-' \+ field\.key\}/.test(src)) for (const key of rangeKeys) used.add('rf-' + key);
    // A date field takes its help key as the help prop.
    for (const m of src.matchAll(/<DateTimeField\b[^>]*\shelp="([^"]+)"/g)) used.add(m[1]);
  }
  return used;
};

test('every key in the help table is used by a HelpNote or a FieldHelp, or is listed as intentionally unused', () => {
  const used = usedHelpIds();
  for (const key of Object.keys(help)) {
    if (Object.hasOwn(INTENTIONALLY_UNUSED, key)) { assert.ok(INTENTIONALLY_UNUSED[key].length > 10, key + ' needs a reason'); continue; }
    assert.ok(used.has(key), 'help key ' + key + ' is not used by any component');
  }
  for (const key of Object.keys(INTENTIONALLY_UNUSED)) assert.ok(!used.has(key), key + ' is used, so remove it from the allow-list');
  for (const id of used) assert.ok(Object.hasOwn(help, id), 'unknown help id ' + id);
});

test('each filter group renders its own HelpNote once, through the help snippet of FilterGroup', () => {
  for (const [file, key] of [['WhenGroup', 'when'], ['WhatGroup', 'what'], ['DetailsGroup', 'details'], ['RangesGroup', 'ranges'], ['ChartGroup', 'chart']]) {
    const src = read('src/components/filters/' + file + '.svelte');
    assert.equal(src.split('<HelpNote id="' + key + '" />').length - 1, 1, file);
    assert.match(src, /\{#snippet help\(\)\}<HelpNote id="\w+" \/>\{\/snippet\}/, file);
    assert.match(src, /import HelpNote from '\.\.\/HelpNote\.svelte'/, file);
  }
  const group = read('src/components/filters/FilterGroup.svelte');
  assert.match(group, /help\?: Snippet/);
  assert.match(group, /\{#if help\}<div class="fg-help">\{@render help\(\)\}<\/div>\{\/if\}/);
});

test('the field keys are wired to the controls they describe', () => {
  const when = read('src/components/filters/WhenGroup.svelte');
  for (const id of ['current-range', 'reference-range']) assert.match(when, new RegExp('<FieldHelp id="' + id + '" />'));
  for (const id of ['current-start', 'current-end', 'reference-start', 'reference-end']) assert.match(when, new RegExp('<DateTimeField id="' + id + '" label="(?:From|To)" help="' + id + '"'));
  const what = read('src/components/filters/WhatGroup.svelte');
  for (const id of ['outcome', 'rf-completion', 'controller', 'action', 'rf-operation', 'kind-unknown']) assert.match(what, new RegExp('<FieldHelp id="' + id + '" />'));
  const details = read('src/components/filters/DetailsGroup.svelte');
  for (const id of DETAIL_FIELDS) assert.match(details, new RegExp('<label for="' + id + '">[^\\n]*</label><FieldHelp id="' + id + '" />'));
  const chart = read('src/components/filters/ChartGroup.svelte');
  for (const id of CHART_FIELDS) assert.match(chart, new RegExp('<FieldHelp id="' + id + '" />'));
  const ranges = read('src/components/filters/RangesGroup.svelte');
  assert.match(ranges, /part="toggle" bind:open=\{helpOpen\[field\.key\]\}/);
  assert.match(ranges, /part="block" open=\{helpOpen\[field\.key\]\}/);
});

test('the drill-down controls carry their notes: investigate with the chip, back with the Back button', () => {
  const src = read('src/components/ZoomStrip.svelte');
  assert.match(src, /\{#if !model\.isRoot\}<div class="zoom-help"><HelpNote id="drill-investigate" \/><\/div>\{\/if\}/, 'shown only while the chip is shown');
  assert.match(src, /\{#if model\.canBack\}<div class="zoom-help"><HelpNote id="drill-back" \/><\/div>\{\/if\}/);
  assert.match(src, /<HelpNote id="drill-navigator" \/>/);
  // The chip keeps its own disabled logic.
  assert.match(src, /aria-disabled=\{!investigate\.enabled\} aria-describedby=\{investigate\.enabled \? undefined : reasonId\}/);
});

test('FieldHelp is a labelled disclosure that adds no tab stop beyond its button and uses tokens only', () => {
  const src = read('src/components/FieldHelp.svelte');
  assert.match(src, /let \{ id, part = 'both', open = \$bindable\(false\) \}/);
  assert.match(src, /import \{ help \} from '\.\.\/lib\/copy\/helpCopy'/);
  assert.match(src, /<button type="button"[^>]*aria-expanded=\{shown\}[^>]*aria-controls=\{blockId\}/);
  assert.match(src, /disabled=\{eli10\.on\}/);
  assert.match(src, /id=\{blockId\}[^>]*hidden=\{!shown\}/, 'the block stays mounted and hidden');
  assert.doesNotMatch(src, /tabindex|\{@html|innerHTML/);
  const style = src.slice(src.indexOf('<style>'));
  assert.doesNotMatch(style, /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(|animation|transition/);
  assert.match(style, /:focus-visible/);
});

test('the group rail keeps one roving tab stop: help sits inside the panes, never on the tab buttons', () => {
  const src = read('src/components/QueryControls.svelte');
  const tabs = src.slice(src.indexOf('role="tablist"'), src.indexOf('role="tabpanel"'));
  assert.ok(tabs.length > 20, 'the tab list was found');
  assert.doesNotMatch(tabs, /HelpNote|FieldHelp/);
  assert.match(src, /role="tabpanel" id="rail-pane-when"[^>]*hidden=\{current !== 'when'\}/, 'a hidden pane is hidden, so its notes take no focus');
});

test('FieldHelp keeps CRLF line endings', () => {
  assert.ok(!/(?<!\r)\n/.test(read('src/components/FieldHelp.svelte')));
});
