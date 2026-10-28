const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Scenario 1 defect tests for lane L8 (Blackbox). Each test pins one defect so it cannot come back.

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');

const bb = loadTypeScript('src/lib/blackbox.ts');
const { BB_TEXT: T } = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const keyLib = loadTypeScript('src/lib/blackboxKey.ts');
const { blackboxCopyEntries } = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

// D-C-01: a keeper-held answer is told apart, copyKeyOnce does not write to the clipboard for it, and the box shows one calm line.
test('D-C-01: isKeeperHeldError matches only a refusal whose reason is keeper-held', () => {
  const held = new bb.BlackboxError({ status: 403, error: 'The key keeper holds this read key and never gives it out.', reason: 'keeper-held' });
  assert.equal(keyLib.isKeeperHeldError(held), true);
  assert.equal(keyLib.isKeeperHeldError(new bb.BlackboxError({ status: 404, error: 'x', reason: 'no-key' })), false);
  assert.equal(keyLib.isKeeperHeldError(new bb.BlackboxError({ status: 403, error: 'x', reason: 'not-this-pc' })), false);
  assert.equal(keyLib.isKeeperHeldError(new Error('keeper-held')), false);
  assert.equal(keyLib.isKeeperHeldError(null), false);
  assert.equal(keyLib.isKeeperHeldError(undefined), false);
  assert.equal(keyLib.isKeeperHeldError('keeper-held'), false);
});

test('D-C-01: copyKeyOnce answers keeper-held and never touches the clipboard for it', async () => {
  let wrote = 0;
  const held = new bb.BlackboxError({ status: 403, error: 'held', reason: 'keeper-held' });
  const got = await keyLib.copyKeyOnce(() => Promise.reject(held), async () => { wrote++; });
  assert.equal(got, 'keeper-held');
  assert.equal(wrote, 0);
  // The other answers are unchanged.
  assert.equal(await keyLib.copyKeyOnce(() => Promise.reject(new Error('refused')), async () => { wrote++; }), 'no-key');
  assert.equal(await keyLib.copyKeyOnce(() => Promise.resolve(''), async () => { wrote++; }), 'no-key');
  assert.equal(await keyLib.copyKeyOnce(() => Promise.resolve('ubk-x'), async () => { throw new Error('denied'); }), 'no-clipboard');
  assert.equal(await keyLib.copyKeyOnce(() => Promise.resolve('ubk-x'), async () => { wrote++; }), 'copied');
  assert.equal(wrote, 1);
});

test('D-C-01: the Read key box handles keeper-held with one calm line and keeps its guards', () => {
  const src = read('src/components/BlackboxReadKey.svelte');
  assert.match(src, /import \{ copyKeyOnce, isKeeperHeldError, KEY_SHOW_MS, makeRevealGuard \} from '\.\.\/lib\/blackboxKey';/);
  assert.match(src, /let held = \$state\(false\);/);
  assert.match(src, /catch \(error\) \{\s*if \(!guard\.valid\(mine\)\) return;\s*letGo\(\);\s*if \(isKeeperHeldError\(error\)\) held = true;\s*else problem = /);
  assert.match(src, /else if \(done === 'keeper-held'\) held = true;/);
  assert.equal((src.match(/disabled=\{!!busy \|\| held\}/g) ?? []).length, 2);
  assert.match(src, /\{#if held\}<p class="bb-note" role="status">\{T\.keyHeld\}<\/p>\{\/if\}/);
  assert.doesNotMatch(src, /localStorage|sessionStorage|indexedDB|app\.|console\./);
  assert.match(T.keyHeld, /key keeper/);
  assert.doesNotMatch(T.keyHeld, /[–—]| - |error|failed/i);
});

test('D-B-43 pin: the page says the plain install command, with no --name', () => {
  assert.match(T.packSteps[3], /^Run: unravis-blackbox\.exe install\./);
  assert.doesNotMatch(T.packSteps[3], /--name/);
});

test('D-B-45: the other-server status view shows the short read-only line', () => {
  assert.equal(T.statusReadOnly, 'Unravis only reads here. Nothing was changed on that server.');
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /\{#if !item\.thisPc\}<p class="bb-note">\{T\.statusReadOnly\}<\/p>\{\/if\}/);
  // It sits in the top block of the status view, before the facts list.
  assert.ok(panel.indexOf('T.statusReadOnly') < panel.indexOf('<dl class="bb-facts">'));
});

test('D-C-22: Back in the status view returns focus to the opened row', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /data-open-for=\{item\.id\}/);
  assert.match(panel, /const leavingStatus = view\.kind === 'status' \? view\.id : '';/);
  assert.match(panel, /el\.dataset\.openFor === leavingStatus/);
  assert.match(panel, /const target = row \?\? body\?\.querySelector<HTMLElement>\('h3\[tabindex="-1"\]'\)/);
  assert.match(panel, /if \(fromList && next\.kind === 'status'\) void refreshOpened\(next\.id\)/);
});

test('D-B-51: the gaps fact says how long the longest gap was and what the rule is', () => {
  assert.equal(T.gapsText(1, 0, 90), '1 gap of 90 seconds. This is a warning only. Missing minutes are never counted as zero');
  assert.equal(T.gapsText(2, 0, 100), '2 gaps, the longest is 100 seconds. This is a warning only. Missing minutes are never counted as zero');
  assert.equal(T.gapsText(1, 0, 300), '1 gap of 5 minutes. The minutes inside it are Cannot test yet');
  assert.equal(T.gapsText(3, 1, 1800), '3 gaps, the longest is 30 minutes. The minutes inside it are Cannot test yet. 1 recording file was not read to the end');
  // No length: the old text, unchanged.
  assert.equal(T.gapsText(0, 0, 0), 'none');
  assert.equal(T.gapsText(2, 0), '2 gaps');
  assert.equal(T.gapsText(1, 3, 0), '1 gap. 3 recording files were not read to the end');
  // 120 seconds is still short; 121 is long.
  assert.match(T.gapsText(1, 0, 120), /warning only/);
  assert.match(T.gapsText(1, 0, 121), /Cannot test yet/);
});

test('D-B-51: the panel and the facts type carry the length, and the lint reads the new sentences', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /T\.gapsText\(item\.facts\.gaps, item\.facts\.unread \?\? 0, item\.facts\.longestGapSeconds \?\? 0\)/);
  assert.match(read('src/lib/blackbox.ts'), /longestGapSeconds\?: number;/);
  const entries = blackboxCopyEntries();
  for (const id of ['short', 'shorts', 'long', 'longs']) {
    assert.ok(entries.some((entry) => entry.id === 'blackbox.gapsText.' + id), 'the lint reads gapsText.' + id);
  }
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('D-B-46: Use in this case is off on a closed case, with the reason', () => {
  const use = read('src/components/BlackboxCaseUse.svelte');
  assert.match(use, /import \{ believedState, getCaseView \} from '\.\.\/lib\/cases';/);
  assert.match(use, /caseClosed = view \? believedState\(view\) === 'closed' : false;/);
  assert.match(use, /const closed = \$derived\(!!caseId && caseClosed\);/);
  assert.match(use, /\{#if closed\}<p class="bb-note">\{T\.caseUseClosed\}<\/p>\{\/if\}/);
  assert.match(use, /disabled=\{!!busy \|\| !caseId \|\| !readable \|\| closed \|\| use\?\.state === 'other'\}/);
  assert.match(use, /disabled=\{!!busy \|\| closed\} onclick=\{remove\}/);
  assert.equal(T.caseUseClosed, 'This case is closed. Its Blackbox use cannot be changed. Reopen the case to change it.');
  assert.doesNotMatch(use, /item\.location|\.key\b|hasKey|revealKey/);
});

test('D-B-47: a refused host read clears the old answer and the windows panel stays open', () => {
  const card = read('src/components/BlackboxHostCard.svelte');
  assert.match(card, /if \(!windowsOk\(\)\) \{ answer = null; problem = T\.hostWindowProblem; return; \}/);
  assert.match(card, /<details class="bb-windows" open>/);
  assert.doesNotMatch(card, /open=\{!answer\}/);
});

test('D-B-49: wizardEscape says what Esc does on each part', () => {
  assert.equal(bb.wizardEscape(1, false, false), 'leave');
  assert.equal(bb.wizardEscape(2, false, false), 'back');
  assert.equal(bb.wizardEscape(3, false, false), 'back');
  assert.equal(bb.wizardEscape(4, false, false), 'back');
  assert.equal(bb.wizardEscape(4, true, false), 'swallow');
  assert.equal(bb.wizardEscape(1, true, false), 'swallow');
  assert.equal(bb.wizardEscape(4, false, true), 'none');
  assert.equal(bb.wizardEscape(4, true, true), 'none');
});

test('D-B-49: the wizard takes Esc in the capture phase, only while focus is inside it, and the drawer is untouched', () => {
  const wizard = read('src/components/BlackboxWizard.svelte');
  assert.match(wizard, /<svelte:window onkeydowncapture=\{escape\} \/>/);
  assert.match(wizard, /<div class="bb-wizard" bind:this=\{rootEl\}>/);
  assert.match(wizard, /!rootEl\?\.contains\(document\.activeElement\)/);
  assert.match(wizard, /event\.preventDefault\(\);\s*event\.stopPropagation\(\);/);
  assert.match(wizard, /if \(part === 4\) backToSettings\(\); else void go\(part - 1\);/);
  assert.match(wizard, /wizardEscape\(part, busy, phase\.kind === 'installed' \|\| phase\.kind === 'pack'\)/);
  assert.doesNotMatch(wizard, /localStorage|sessionStorage|indexedDB|console\.(log|info|debug)/);
  // The drawer keeps its own handler; nothing in this lane edits it.
  assert.match(read('src/components/Drawer.svelte'), /stopEscape/);
});

test('D-B-49: the heading shows the real part, for example "Part 2 of 4: Where"', () => {
  assert.equal(T.partHeading(2, T.partNames[1]), 'Part 2 of 4: Where');
  assert.equal(T.partHeading(4, T.partNames[3]), 'Part 4 of 4: Review');
  const wizard = read('src/components/BlackboxWizard.svelte');
  assert.match(wizard, /<span class="bb-part-now">\{T\.partHeading\(part, T\.partNames\[part - 1\]\)\}<\/span>/);
  assert.match(wizard, /aria-label=\{T\.partOf\(part\)\}/);
  assert.ok(blackboxCopyEntries().some((entry) => entry.id === 'blackbox.partHeading'), 'the lint reads partHeading');
});

test('D-B-50: listensOnAll is true for every address and false otherwise, and 0.0.0.0 stays valid', () => {
  for (const yes of ['0.0.0.0', ' 0.0.0.0 ', '::']) assert.equal(bb.listensOnAll(yes), true, yes);
  for (const no of ['127.0.0.1', 'web-a', '', '0.0.0.1', '10.0.0.5', '[::]', '::1']) assert.equal(bb.listensOnAll(no), false, no);
  // Warn only, never refuse.
  assert.equal(bb.validBind('0.0.0.0'), true);
  const settings = { ...bb.defaultSettings(), name: 'web-a', bind: '0.0.0.0' };
  assert.notEqual(bb.settingsProblem(settings), 'bind');
  assert.equal(bb.listensOnAll(bb.defaultSettings().bind), false);
});

test('D-B-50 and D-C-20: the warning, the Review sentence and the opt-in firewall note', () => {
  assert.equal(T.bindWarn, 'This lets other computers on the network reach the status port. The read key is the only protection.');
  assert.match(T.reviewBindWarn, /The read key is the only protection\./);
  assert.match(T.bindHelp, /Unravis does not add a firewall rule\. Adding one is your own choice\./);
  assert.doesNotMatch(T.bindHelp, /and add a firewall rule/);
  const fields = read('src/components/BlackboxSettings.svelte');
  assert.match(fields, /\{#if warnAll\}<p class="bb-note bb-warn" id="\{idPrefix\}-bind-warn">\{T\.bindWarn\}<\/p>\{\/if\}/);
  assert.match(fields, /aria-describedby="\{idPrefix\}-bind-help\{warnAll \? ' ' \+ idPrefix \+ '-bind-warn' : ''\}\{problem === 'bind' \? ' ' \+ idPrefix \+ '-bind-error' : ''\}"/);
  const wizard = read('src/components/BlackboxWizard.svelte');
  assert.match(wizard, /\{#if listensOnAll\(settings\.bind\)\}<p class="bb-note bb-warn">\{T\.reviewBindWarn\}<\/p>\{\/if\}/);
  // The Review sentence sits after the facts list and before the read-only note.
  assert.ok(wizard.indexOf('T.reviewBindWarn') > wizard.indexOf('T.reviewBind}') && wizard.indexOf('T.reviewBindWarn') < wizard.indexOf('{T.readOnly}', wizard.indexOf('T.reviewBindWarn')));
});

test('D-B-50: fieldProblemNow keeps a message only while that field is still wrong', () => {
  const good = { ...bb.defaultSettings(), name: 'web-a' };
  assert.equal(bb.fieldProblemNow(null, good, 'C:\\logs'), null);
  const bad = { ...good, keepDays: 0 };
  assert.equal(bb.fieldProblemNow('keep', bad, 'C:\\logs'), 'keep');
  assert.equal(bb.fieldProblemNow('keep', { ...bad, keepDays: 7 }, 'C:\\logs'), null);
  // The log box text is read as one folder per line, as the wizard does before it checks.
  assert.equal(bb.fieldProblemNow('logs', good, 'logs'), 'logs');
  assert.equal(bb.fieldProblemNow('logs', good, 'C:\\a\n\nD:\\b\n'), null);
});

test('D-B-50: each settings field has an id, an error line and aria-describedby that names it', () => {
  const fields = read('src/components/BlackboxSettings.svelte');
  assert.match(fields, /problem = null \}: \{ settings: Settings; logPathsText: string; idPrefix\?: string; problem\?: SettingsProblem \| null \}/);
  assert.match(fields, /\{#snippet fieldError\(key: SettingsProblem\)\}/);
  assert.match(fields, /<p class="inline-error" id="\{idPrefix\}-\{key\}-error" role="alert">\{T\.settingProblems\[key\]\}<\/p>/);
  for (const key of ['interval', 'keep', 'size', 'free', 'folder', 'port', 'bind', 'logs']) {
    assert.ok(fields.includes('id="{idPrefix}-f-' + key + '"'), 'field id ' + key);
    assert.ok(fields.includes("{@render fieldError('" + key + "')}"), 'error line ' + key);
  }
  assert.equal((fields.match(/aria-invalid=\{invalid\('/g) ?? []).length, 8);
  assert.match(fields, /aria-describedby=\{described\('keep', 'keep-help'\)\}/);
  assert.match(fields, /aria-describedby=\{problem === 'logs' \? idPrefix \+ '-logs-error' : undefined\}/);
  // The help notes keep their ids.
  for (const help of ['keep', 'size', 'free', 'folder', 'port', 'bind']) assert.ok(fields.includes('id="{idPrefix}-' + help + '-help"'), 'help id ' + help);
});

test('D-B-50: the wizard and Apply put a field problem beside the field and focus it; name and refusals stay at the bottom', () => {
  const wizard = read('src/components/BlackboxWizard.svelte');
  assert.match(wizard, /const shownField = \$derived\(fieldProblemNow\(fieldProblem, settings, logPathsText\)\);/);
  assert.match(wizard, /if \(first === 'name'\) \{ problem = T\.settingProblems\.name; return; \}/);
  assert.match(wizard, /fieldProblem = first;\s*await tick\(\);\s*rootEl\?\.querySelector<HTMLElement>\('#bb-f-' \+ first\)\?\.focus\(\);/);
  assert.match(wizard, /<SettingsFields bind:settings bind:logPathsText problem=\{shownField\} \/>/);
  assert.match(wizard, /problem = refusalText\(error, BB_REFUSAL\);/);
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /const applyFieldShown = \$derived\(fieldProblemNow\(applyField, applySettings, applyLogs\)\);/);
  assert.match(panel, /applyField = first;\s*await tick\(\);\s*body\?\.querySelector<HTMLElement>\('#bb-apply-f-' \+ first\)\?\.focus\(\);/);
  assert.match(panel, /idPrefix="bb-apply" problem=\{applyFieldShown\} \/>/);
  assert.match(panel, /applyProblem = refusalText\(error, BB_REFUSAL\);/);
});

test('D-C-24: in the light theme the OK tone and the three sticker fills pass 4.5:1, and both light blocks agree', () => {
  const css = read('src/brand/tokens.css');
  const starts = [css.indexOf(':root:not([data-theme="dark"]) {'), css.indexOf(':root[data-theme="light"] {')];
  assert.ok(starts.every((n) => n > 0), 'both light blocks exist');
  const blocks = starts.map((start) => css.slice(start, css.indexOf('}', start)));
  const get = (block, name) => (new RegExp('\\s' + name + ':\\s*(#[0-9a-fA-F]{6})').exec(block) ?? [])[1];
  // Both light blocks carry the same dark green; the dark block keeps its own.
  for (const block of blocks) assert.equal(get(block, '--tone-ok'), '#176b43');
  assert.equal((css.match(/--tone-ok:/g) ?? []).length, 3, 'one per theme block');
  assert.match(css, /:root \{[\s\S]*?--tone-ok: #3fb27f;/);

  const channel = (c) => { c /= 255; return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
  const rgb = (hex) => { const n = parseInt(hex.slice(1), 16); return [n >> 16, (n >> 8) & 255, n & 255]; };
  const luminance = (hex) => { const [r, g, b] = rgb(hex); return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b); };
  const ratio = (a, b) => { const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };
  const mixHex = (ink, ground, share) => '#' + rgb(ink).map((v, i) => Math.round(v * share + rgb(ground)[i] * (1 - share)).toString(16).padStart(2, '0')).join('');

  const sticker = read('src/components/BlackboxSticker.svelte');
  for (const block of blocks) {
    const surface = get(block, '--surface');
    for (const [klass, token] of [['good', '--tone-ok'], ['warn', '--warn'], ['bad', '--bad']]) {
      // The sticker mixes 10% of its ink into the surface (read from the source, so a change there is tested too).
      const mix = new RegExp('\\.bb-sticker\\.' + klass + ' \\{[^}]*color-mix\\(in srgb, var\\(' + token + '\\) (\\d+)%, var\\(--surface\\)\\)').exec(sticker);
      assert.ok(mix, klass + ' mixes its ink into the surface');
      assert.equal(mix[1], '10', klass + ' uses 10%');
      const ink = get(block, token);
      const fill = mixHex(ink, surface, Number(mix[1]) / 100);
      const value = ratio(ink, fill);
      assert.ok(value >= 4.5, klass + ' word on its own fill is ' + value.toFixed(2));
    }
    // The OK tone is also used as plain text on the page, the card and the raised card.
    for (const ground of ['--bg', '--surface', '--surface-2']) {
      const value = ratio(get(block, '--tone-ok'), get(block, ground));
      assert.ok(value >= 4.5, '--tone-ok on ' + ground + ' is ' + value.toFixed(2));
    }
  }
  assert.doesNotMatch(sticker, /14%/);
});
