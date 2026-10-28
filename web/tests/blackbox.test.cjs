const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Blackbox in Unravis: the page's rules, its words, the A35 gap summary, and how the panel is wired in.

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

const bb = loadTypeScript('src/lib/blackbox.ts');
const { BB_TEXT, BB_REFUSAL, blackboxCopyEntries } = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('a name is letters, digits and dashes, starts with a letter or digit, and is at most 63 long', () => {
  assert.equal(bb.nameProblem('web-a'), null);
  assert.equal(bb.nameProblem('A1'), null);
  assert.equal(bb.nameProblem(''), 'empty');
  assert.equal(bb.nameProblem('   '), 'empty');
  assert.equal(bb.nameProblem('-web'), 'chars');
  assert.equal(bb.nameProblem('web a'), 'chars');
  assert.equal(bb.nameProblem('web_a'), 'chars');
  assert.equal(bb.nameProblem('a'.repeat(63)), null);
  assert.equal(bb.nameProblem('a'.repeat(64)), 'long');
});

test('the settings check names the first thing wrong and passes the defaults', () => {
  const good = bb.defaultSettings('web-a');
  assert.equal(bb.settingsProblem(good), null);
  assert.equal(bb.settingsProblem({ ...good, name: '' }), 'name');
  assert.equal(bb.settingsProblem({ ...good, folder: 'UnravisBlackbox' }), 'folder');
  assert.equal(bb.settingsProblem({ ...good, folder: 'D:\\' }), 'folder');
  assert.equal(bb.settingsProblem({ ...good, intervalSeconds: 7 }), 'interval');
  assert.equal(bb.settingsProblem({ ...good, keepDays: 0 }), 'keep');
  assert.equal(bb.settingsProblem({ ...good, keepDays: 2.5 }), 'keep');
  assert.equal(bb.settingsProblem({ ...good, maxGB: 0 }), 'size');
  assert.equal(bb.settingsProblem({ ...good, maxGB: Number.NaN }), 'size');
  assert.equal(bb.settingsProblem({ ...good, minFreePercent: 51 }), 'free');
  assert.equal(bb.settingsProblem({ ...good, port: 70000 }), 'port');
  assert.equal(bb.settingsProblem({ ...good, bind: ' ' }), 'bind');
  assert.equal(bb.settingsProblem({ ...good, logPaths: ['logs'] }), 'logs');
  assert.equal(bb.defaultSettings().bind, '127.0.0.1', 'the pack stays on the loopback address by default (A53)');
});

test('a place is a folder, a share or an address, and only an address asks for a key', () => {
  assert.equal(bb.guessKind('D:\\UnravisBlackbox'), 'folder');
  assert.equal(bb.guessKind('d:/x'), 'folder');
  assert.equal(bb.guessKind('\\\\web-b\\blackbox'), 'share');
  assert.equal(bb.guessKind('//web-b/blackbox'), 'share');
  assert.equal(bb.guessKind('web-b:7788'), 'url');
  assert.equal(bb.guessKind('http://web-b:7788'), 'url');
  assert.equal(bb.guessKind(''), null);
  assert.equal(bb.guessKind('just words'), null);
  assert.equal(bb.needsKey('web-b:7788'), true);
  assert.equal(bb.needsKey('\\\\web-b\\blackbox'), false);
  assert.equal(bb.needsKey('D:\\UnravisBlackbox'), false);
});

test('deleting the recordings needs the exact name typed; keeping them needs nothing', () => {
  assert.equal(bb.removeConfirmed(false, '', 'web-a'), true);
  assert.equal(bb.removeConfirmed(true, '', 'web-a'), false);
  assert.equal(bb.removeConfirmed(true, 'web', 'web-a'), false);
  assert.equal(bb.removeConfirmed(true, 'Web-A', 'web-a'), false);
  assert.equal(bb.removeConfirmed(true, 'web-a', 'web-a'), true);
  assert.equal(bb.removeConfirmed(true, '', ''), false);
});

test('a short gap only warns and a long gap is counted apart; no gap is no warning', () => {
  assert.equal(bb.gapSummary(undefined), null);
  assert.equal(bb.gapSummary([]), null);
  const short = { window: 'incident', start: 'a', end: 'b', minutes: 2, kind: 'short' };
  const long = { window: 'incident', start: 'c', end: 'd', minutes: 3, kind: 'long' };
  const only = bb.gapSummary([short]);
  assert.equal(only.shortMinutes, 2);
  assert.equal(only.longMinutes, 0);
  assert.equal(only.long.length, 0);
  const both = bb.gapSummary([short, long, { ...long, minutes: 4 }]);
  assert.equal(both.shortMinutes, 2);
  assert.equal(both.longMinutes, 7);
  assert.equal(both.long.length, 2);
});

test('the words for a refused call prefer the server, then the table, and never show a stack', () => {
  const refuse = (extra) => new bb.BlackboxError({ status: 422, error: 'raw error', reason: 'x', ...extra });
  assert.equal(bb.refusalText(refuse({ what: 'Wrong key.', next: 'Type it again.' }), BB_REFUSAL), 'Wrong key. Type it again.');
  assert.equal(bb.refusalText(refuse({ what: 'Wrong key.' }), BB_REFUSAL), 'Wrong key.');
  assert.equal(bb.refusalText(refuse({ reason: 'rate' }), BB_REFUSAL), BB_REFUSAL.rate);
  assert.equal(bb.refusalText(refuse({ reason: 'unknown', next: 'Try later.' }), BB_REFUSAL), 'raw error Try later.');
  assert.equal(bb.refusalText(new Error('boom at line 3'), BB_REFUSAL), BB_REFUSAL.offline);
});

test('a sticker tone that is not known falls back to idle, and times and sizes never show NaN', () => {
  assert.equal(bb.stickerTone('good'), 'good');
  assert.equal(bb.stickerTone('warn'), 'warn');
  assert.equal(bb.stickerTone('bad'), 'bad');
  assert.equal(bb.stickerTone('purple'), 'idle');
  assert.equal(bb.shownTime(''), '-');
  assert.equal(bb.shownTime('not a date'), '-');
  assert.equal(bb.shownTime('2026-10-20T09:00:00Z'), '2026-10-20 09:00 UTC');
  assert.equal(bb.shownBytes(-1), '-');
  assert.equal(bb.shownBytes(Number.NaN), '-');
  assert.equal(bb.shownBytes(512), '512 B');
  assert.equal(bb.shownBytes(3 * 1024 * 1024), '3.0 MB');
});

test('every state has a sticker word and a next step, and the states are the nine of A36', () => {
  assert.deepEqual([...bb.ALL_STATES].sort(), ['clock-differs', 'disk-low', 'incomplete', 'not-found', 'not-placed', 'running', 'running-late', 'stopped', 'unreachable']);
  for (const id of bb.ALL_STATES) assert.ok(BB_TEXT.stateNext[id], 'a next step for ' + id);
});

test('the Blackbox copy passes the screen-string lint', () => {
  const entries = blackboxCopyEntries();
  assert.ok(entries.length > 80, 'the lint reads the whole copy');
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('the read key goes up in the add and key calls only, and is never kept or shown', () => {
  const lib = read('src/lib/blackbox.ts');
  const calls = lib.split('\n').filter((line) => /\bkey\b/.test(line) && /call<|body|JSON/.test(line));
  assert.ok(calls.some((line) => line.includes('setKey')), 'a new key is sent by setKey');
  for (const file of ['src/lib/blackbox.ts', 'src/components/BlackboxAdd.svelte', 'src/components/BlackboxPanel.svelte', 'src/components/BlackboxHostCard.svelte', 'src/components/BlackboxWizard.svelte', 'src/components/BlackboxRemove.svelte']) {
    const text = read(file);
    assert.doesNotMatch(text, /localStorage|sessionStorage|indexedDB|console\.(log|info|debug)/, file + ' keeps nothing in the browser');
  }
  assert.doesNotMatch(read('src/components/BlackboxAdd.svelte'), />\s*\{key\}\s*</, 'the typed key is never printed');
  assert.match(read('src/components/BlackboxAdd.svelte'), /type=\{showKey \? 'text' : 'password'\}/, 'the key field hides the key unless asked');
  assert.match(read('src/components/BlackboxAdd.svelte'), /key = '';\s*onAdded/, 'the key is cleared once the add worked');
  assert.match(read('src/components/BlackboxPanel.svelte'), /newKey = '';/, 'a replaced key is cleared once sent');
  // No answer type in the page carries a key field.
  const types = lib.slice(0, lib.indexOf('/* ---------------------------------------------------------------- calls */'));
  const answers = types.split('\n').filter((line) => /\bkey\??\s*:/.test(line));
  assert.deepEqual(answers.map((line) => line.trim()).filter((line) => !line.startsWith('export interface AddBody')), [], 'only the add body has a key');
});

test('the panel is a header utility on every screen and is opened as a side panel', () => {
  assert.match(read('src/lib/appState.svelte.ts'), /'import' \| 'blackbox' \| null/);
  assert.match(read('src/lib/appState.svelte.ts'), /openSidePanel\(which: [^)]*'blackbox'\)/);
  const header = read('src/components/AppHeader.svelte');
  assert.match(header, /id: 'blackbox', label: 'Blackbox', open: \(\) => openSidePanel\('blackbox'\)/);
  assert.match(header, /\.\.\.\(working \? navDrawers : \[\]\), blackbox, glossary/, 'the menu reaches it before a case is open');
  assert.match(read('src/App.svelte'), /panels\.sidePanel === 'blackbox'\}<BlackboxPanel onClose=\{closeSidePanel\}/);
});

test('the panel shows no network scan and says the Blackbox only reads', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.doesNotMatch(panel, /scan the network|discover/i);
  assert.ok(BB_TEXT.readOnly.length > 20);
});

test('a not-placed entry offers to place a Blackbox or forget the place, and the remove dialog has a words-only branch', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /not-placed/);
  assert.ok(BB_TEXT.removeNotPlaced && BB_TEXT.removeNotPlaced.length > 20);
  assert.match(read('src/components/BlackboxRemove.svelte'), /gone/);
});

/* ---------------------------------------------------------- use in a case (F1) */

const bbCase = loadTypeScript('src/lib/blackboxCase.ts');

function fakeCaseApi(start = []) {
  const tags = start.map((t, i) => ({ id: i + 1, ...t }));
  const calls = [];
  return {
    tags, calls,
    async listTags() { calls.push('list'); return tags.map((t) => ({ ...t })); },
    async postTag(caseId, tag) { calls.push(['post', tag]); const t = { id: tags.length + 10, kind: tag.kind, label: tag.label }; tags.push(t); return t; },
    async removeTag(caseId, id) { calls.push(['remove', id]); const i = tags.findIndex((t) => t.id === id); if (i >= 0) tags.splice(i, 1); }
  };
}

test('the tag a case keeps for a Blackbox is the name and nothing else', () => {
  assert.deepEqual({ ...bbCase.blackboxTag(' web-a ') }, { importId: 0, kind: 'blackbox', label: 'web-a', env: '', portal: '', api: '', classification: '' });
});

test('use in this case adds the name tag once, and a second press adds nothing', async () => {
  const api = fakeCaseApi();
  const first = await bbCase.useInCase(api, 'c', 'web-a');
  assert.equal(first.state, 'this');
  const again = await bbCase.useInCase(api, 'c', 'WEB-A');
  assert.equal(again.state, 'this');
  assert.equal(api.calls.filter((c) => c[0] === 'post').length, 1);
  assert.equal(api.tags.length, 1);
  assert.equal(api.tags[0].label, 'web-a');
});

test('a case reads one Blackbox, so a second one is refused until the first is taken out', async () => {
  const api = fakeCaseApi([{ kind: 'blackbox', label: 'web-b' }, { kind: 'iis', label: 'web-b' }]);
  const r = await bbCase.useInCase(api, 'c', 'web-a');
  assert.deepEqual({ ...r }, { state: 'other', name: 'web-b' });
  assert.equal(api.calls.filter((c) => c[0] === 'post').length, 0);
  await bbCase.stopUsingInCase(api, 'c', 'web-b');
  assert.equal((await bbCase.useInCase(api, 'c', 'web-a')).state, 'this');
});

test('stop using takes out only the Blackbox tag of that name', async () => {
  const api = fakeCaseApi([{ kind: 'iis', label: 'web-a' }, { kind: 'blackbox', label: 'web-a' }]);
  assert.equal((await bbCase.stopUsingInCase(api, 'c', 'web-a')).state, 'none');
  assert.deepEqual(api.tags.map((t) => t.kind), ['iis']);
});

test('the status screen offers use in this case and the control posts a name tag, never a key or an address', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /BlackboxCaseUse\.svelte/);
  assert.match(panel, /<CaseUse \{item\} \/>/);
  const control = read('src/components/BlackboxCaseUse.svelte');
  assert.match(control, /useInCase\(caseUseApi, caseId, item\.name\)/);
  assert.doesNotMatch(control, /item\.location|\.key\b|hasKey/);
  const api = read('src/lib/blackboxCaseApi.ts');
  assert.match(api, /postSourceTag/);
  assert.equal(BB_TEXT.caseUseButton, 'Use in this case');
});
