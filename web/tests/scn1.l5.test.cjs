'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript, step, suspect, result, change } = require('./guided.fixture.cjs');

const read = (relative) => fs.readFileSync(path.join(__dirname, '..', relative), 'utf8');
const guided = (name) => read('src/lib/components/guided/' + name);
const store = () => read('src/lib/guided/guided.svelte.ts');
const SHELL = loadTypeScript('src/lib/copy/guidedShellCopy.ts').SHELL_TEXT;

// Every string of the shell copy, and every function of it called with a word and a few numbers.
function shellLines() {
  const out = [];
  for (const [key, value] of Object.entries(SHELL)) {
    if (typeof value === 'string') out.push([key, value]);
    else if (typeof value === 'function') for (const arg of ['zzz', 0, 1, 3, 300]) out.push([`${key}(${arg})`, String(value(arg))]);
  }
  return out;
}

test('L5 shell copy: plain sentences, no emoji, no dashes, no causal words', () => {
  const lines = shellLines();
  assert.ok(lines.length > 0);
  for (const [key, text] of lines) {
    assert.ok(text.trim() === text && text.length > 0, key);
    assert.ok(!/\p{Extended_Pictographic}/u.test(text), `${key}: emoji`);
    assert.ok(!/[‒-―−]/.test(text) && !/\s-\s/.test(text) && !text.includes('--'), `${key}: dash`);
    assert.ok(!/\b(because|caused|causes|due to|culprit|root cause)\b/i.test(text), `${key}: causal word`);
  }
});

test('B-02: a step change moves the keyboard to the step title', () => {
  assert.match(store(), /titleFocus = \$state\(0\);/);
  assert.match(store(), /goTo\(step: number\): void \{ this\.step = clampStep\(step\); this\.titleFocus\+\+; \}/);
  assert.match(guided('StepShell.svelte'), /<h3 id=\{'step-title-' \+ step\} tabindex="-1">/);
  const g = guided('GuidedAnalysis.svelte');
  assert.match(g, /let seenFocus = untrack\(\(\) => store\.titleFocus\);/);
  assert.match(g, /document\.getElementById\('step-title-' \+ store\.step\)\?\.focus\(\)/);
  assert.match(g, /store\.mode !== 'guided' \|\| panel !== 'main'/);
});

test('B-31: the heading names the mode on screen', () => {
  assert.equal(SHELL.manualHeading, 'Manual tools');
  assert.equal(SHELL.manualHeading, loadTypeScript('src/lib/copy/guidedCopy.ts').GUIDED_TEXT.toolsHeading);
  const g = guided('GuidedAnalysis.svelte');
  assert.match(g, /<h2 tabindex="-1" bind:this=\{heading\}>\{store\.mode === 'guided' \? T\.heading : S\.manualHeading\}<\/h2>/);
  assert.match(g, /import \{ SHELL_TEXT as S \} from '\.\.\/\.\.\/copy\/guidedShellCopy';/);
});

const tools = loadTypeScript('src/lib/guided/tools.ts');

test('B-11: a tool with its source but no run is Waiting; Slowest calls and Nearby activity are Ready', () => {
  const audit = { have: new Set(['audit']), result: null };
  for (const id of ['response-time', 'slowest-apis', 'calls-in-flight', 'changes', 'after-fix', 'suspect-summary']) {
    const s = tools.toolState(tools.toolById(id), audit);
    assert.equal(s.word, 'Waiting', id);
    assert.deepEqual(s.missing, [], id);
    assert.equal(s.why, '', id);
  }
  for (const id of ['slowest-calls', 'nearby-activity']) assert.equal(tools.toolState(tools.toolById(id), audit).word, 'Ready', id);
  assert.equal(tools.toolState(tools.toolById('time-split'), audit).word, 'Partly ready');
  assert.equal(tools.toolState(tools.toolById('time-split'), { have: new Set(['audit', 'iis']), result: null }).word, 'Waiting');
  assert.equal(tools.toolState(tools.toolById('sql-waits'), audit).word, 'Cannot test yet');
  // With a run the old rules apply again.
  const run = { have: new Set(['audit']), result: result() };
  assert.equal(tools.toolState(tools.toolById('slowest-apis'), run).word, 'Ready');
  assert.equal(tools.toolState(tools.toolById('response-time'), run).word, 'Ready');
});

test('B-08: a tool sticker carries Out of date only when it has a run to be out of date', () => {
  const st = (word) => ({ word, missing: [], why: '' });
  assert.deepEqual(tools.toolSticker(st('Ready'), true), { word: 'Ready', tone: 'ready', outOfDate: true });
  assert.deepEqual(tools.toolSticker(st('Partly ready'), true), { word: 'Partly ready', tone: 'partly', outOfDate: true });
  assert.deepEqual(tools.toolSticker(st('Failed'), true), { word: 'Failed', tone: 'failed', outOfDate: true });
  assert.deepEqual(tools.toolSticker(st('Waiting'), true), { word: 'Waiting', tone: 'waiting', outOfDate: false });
  assert.deepEqual(tools.toolSticker(st('Cannot test yet'), true), { word: 'Cannot test yet', tone: 'cannot', outOfDate: false });
  assert.equal(tools.toolSticker(st('Ready'), false).outOfDate, false);
});

test('B-08 and B-11: ManualTools passes the stale flag to both stickers and shows the Waiting hint', () => {
  const m = guided('ManualTools.svelte');
  assert.match(m, /const stale = \$derived\(store\.outOfDate\(sourceIds\)\);/);
  assert.match(m, /<Sticker word=\{sk\.word\} tone=\{sk\.tone\} outOfDate=\{sk\.outOfDate\} \/>/);
  assert.match(m, /<Sticker word=\{openSticker\.word\} tone=\{openSticker\.tone\} outOfDate=\{openSticker\.outOfDate\} \/>/);
  assert.match(m, /\{#if st\.word === 'Waiting'\}<p class="src">\{S\.toolWaitingHint\}<\/p>\{\/if\}/);
  assert.ok(!/const TONE/.test(m), 'the local tone table is replaced by toolSticker');
});

test('B-09: the fingerprint of a case changes with its files and source tags, and ignores order and rejected files', () => {
  const imp = (id, status = 'ok', importer = 'iis-w3c') => ({ id, importer, status });
  const tag = (id, kind = 'mongo-database') => ({ id, kind });
  const base = tools.dataStamp([imp(1), imp(2)], [tag(1)]);
  assert.equal(tools.dataStamp([imp(2), imp(1)], [tag(1)]), base);
  assert.notEqual(tools.dataStamp([imp(1), imp(2), imp(3)], [tag(1)]), base, 'a new file');
  assert.notEqual(tools.dataStamp([imp(1), imp(2)], [tag(1), tag(2, 'sql')]), base, 'a new source tag');
  assert.notEqual(tools.dataStamp([imp(1)], [tag(1)]), base, 'a removed file');
  assert.equal(tools.dataStamp([imp(1), imp(2), imp(3, 'failed')], [tag(1)]), base, 'a rejected file is not kept data');
  assert.equal(tools.dataStamp([], []), '');
});

test('B-09: an unknown fingerprint is never a change', () => {
  assert.equal(tools.stampChanged(null, 'i1'), false);
  assert.equal(tools.stampChanged('i1', null), false);
  assert.equal(tools.stampChanged('i1', 'i1'), false);
  assert.equal(tools.stampChanged('i1', 'i1,i2'), true);
  assert.equal(tools.stampChanged('', 'i1'), true);
});

test('B-09: the store records the fingerprint at run time and compares it in outOfDate', () => {
  const s = store();
  assert.match(s, /stamp = \$state<string \| null>\(null\);/);
  assert.match(s, /answeredStamp = \$state<string \| null>\(null\);/);
  assert.match(s, /return this\.keyFor\(sourceIds\) !== this\.answeredKey \|\| stampChanged\(this\.answeredStamp, this\.stamp\);/);
  assert.match(s, /const stamp = await this\.refreshStamp\(\);/);
  assert.match(s, /this\.answeredStamp = stamp;/);
  assert.match(s, /dispose\(\): void \{ this\.cancel\(\); this\.#plan\+\+; this\.#stampRun\+\+; this\.#sourcesRun\+\+; \}/);
  assert.match(s, /casePath\(caseId, '\/sources'\)/);
  const g = guided('GuidedAnalysis.svelte');
  assert.match(g, /window\.addEventListener\('focus', onFocus\);/);
  assert.match(g, /window\.removeEventListener\('focus', onFocus\);/);
  assert.match(g, /void hasIis;/);
});

test('B-10: the store is kept per case in memory, keyed by case id, with a cap', () => {
  const s = store();
  assert.match(s, /export function storeFor\(caseId: string\): GuidedStore \{/);
  assert.match(s, /held && held\.caseId === caseId/);
  assert.match(s, /const MAX_KEPT_STORES = 5;/);
  assert.match(s, /kept\.get\(oldest\)\?\.dispose\(\);/);
  assert.match(s, /export function releaseStore\(caseId: string\): void \{/);
  assert.match(s, /noteDraft = \$state\(''\);/);
  // The mode is the one value that is persisted (readMode and writeMode); every other line of the store stays off disk.
  const rest = s.split(/\r?\n/).filter((line) => !/MODE_KEY/.test(line)).join('\n');
  assert.ok(!/localStorage|sessionStorage|indexedDB/.test(rest), 'kept state stays in memory');
  const g = guided('GuidedAnalysis.svelte');
  assert.match(g, /const store = storeFor\(untrack\(\(\) => caseId\)\);/);
  assert.ok(!/new GuidedStore\(/.test(g), 'the screen no longer makes its own store');
  assert.match(g, /return \(\) => \{ window\.removeEventListener\('focus', onFocus\); store\.dispose\(\); \};/);
});

test('B-10: the unsaved note lives on the store, not in the screen', () => {
  const m = guided('ManualTools.svelte');
  assert.ok(!/let note = \$state/.test(m));
  assert.match(m, /store\.addItem\('learned', store\.noteDraft\)/);
  assert.match(m, /bind:value=\{store\.noteDraft\}/);
  assert.match(m, /disabled=\{store\.noteDraft\.trim\(\) === ''( \|\| noteOver)?\}/);
});

test('B-34: only the sources of the open case are read', () => {
  const src = (id, database, selectionKey) => ({ id, database, selectionKey });
  const connected = [src('a', 'AuditOne'), src('b', 'AuditTwo'), src('c', 'Other', 'k3')];
  assert.deepEqual(tools.caseSourceIds(connected, ['AuditOne'], []), ['a']);
  assert.deepEqual(tools.caseSourceIds(connected, ['AuditOne', 'AuditTwo'], []), ['a', 'b']);
  assert.deepEqual(tools.caseSourceIds(connected, [], []), [], 'a case with no source reads nothing, whatever is connected');
  assert.deepEqual(tools.caseSourceIds([], ['AuditOne'], []), []);
  assert.deepEqual(tools.caseSourceIds(connected, ['Missing'], [{ name: 'AuditOne' }]), [], 'tags win over the workspace');
});

test('B-34: an older case with no tag falls back to the databases of its workspace', () => {
  const connected = [{ id: 'a', database: 'AuditOne', selectionKey: 'k1' }, { id: 'b', database: 'AuditTwo', selectionKey: 'k2' }, { id: 'c', database: 'Other' }];
  assert.deepEqual(tools.caseSourceIds(connected, [], [{ name: 'AuditTwo' }]), ['b'], 'by name when the workspace has no key');
  assert.deepEqual(tools.caseSourceIds(connected, [], [{ name: 'renamed', selectionKey: 'k1' }]), ['a'], 'by key when both have one');
  assert.deepEqual(tools.caseSourceIds(connected, [], [{ name: 'Other', selectionKey: 'k9' }]), ['c'], 'by name when the source has no key');
});

test('B-34: the page sends the case sources, waits while reading and keeps the demo list', () => {
  const g = guided('GuidedAnalysis.svelte');
  assert.match(g, /demo \? app\.sources\.map\(\(s\) => s\.id\) : store\.sourcesState === 'ready' \? caseSourceIds\(app\.sources, store\.auditLabels, store\.workspaceDbs\) : \[\]/);
  assert.match(g, /\{:else if sourcesWait\}\s*<p class="note" role="status">\{S\.sourcesLoading\}<\/p>/);
  assert.match(g, /\{:else if sourcesFailed\}\s*<p class="inline-error" role="alert">\{S\.sourcesFailed\}<\/p>/);
  assert.match(g, /void store\.loadCaseSources\(\);/);
  const s = store();
  assert.match(s, /casePath\(caseId, '\/mongo-sources'\)/);
  assert.match(s, /sourcesState = \$state<'loading' \| 'ready' \| 'failed'>\('loading'\);/);
  assert.ok(!/password|uri|username/i.test(s.slice(s.indexOf('async function listAuditLabels'), s.indexOf('export class GuidedStore'))), 'only names are read');
});

test('B-34: the new sentences are plain', () => {
  assert.equal(SHELL.sourcesLoading, 'Reading the sources this case holds.');
  assert.equal(SHELL.sourcesFailed, 'The sources of this case could not be read. Reload the page and try again.');
});

const sus = (id, colour, rank) => suspect(id, colour, { rank, nextCheck: 'check ' + id });
const conf = (word) => ({ word, why: 'x', sourcesUsed: [], sourcesMissing: [], notTested: [], overturn: '' });

test('B-25: suggested checks are the red and orange ones, not the grey', () => {
  const grey = Array.from({ length: 38 }, (_, i) => sus('G' + (i + 1), 'grey', 10 + i));
  const r = result({ suspects: [sus('L1', 'red', 1), sus('D1', 'orange', 2), sus('H1', 'green', 3), ...grey] });
  const got = tools.originChecks(r, []);
  assert.deepEqual(got.map((c) => c.suspect), ['L1', 'D1']);
  assert.equal(got[0].text, 'L1: check L1', 'the cheapest check is the first ranked one');
  const onList = [{ id: 1, list: 'todo', text: 'L1: check L1', order: 1 }];
  assert.deepEqual(tools.originChecks(r, onList).map((c) => c.suspect), ['D1'], 'a check already on the list is not added twice');
  assert.deepEqual(tools.originChecks(result({ suspects: [sus('G1', 'grey', 1)] }), []), []);
});

test('B-04: the Likely-without-tier line shows only for None confidence with an orange suspect', () => {
  const orange = [sus('D1', 'orange', 1)];
  assert.equal(tools.likelyWithoutTier(result({ suspects: orange, confidence: conf('None') })), true);
  assert.equal(tools.likelyWithoutTier(result({ suspects: orange, confidence: conf('Medium') })), false);
  assert.equal(tools.likelyWithoutTier(result({ suspects: [sus('L1', 'red', 1)], confidence: conf('None') })), false);
  assert.equal(tools.likelyWithoutTier(result({ suspects: [sus('M1', 'grey', 1)], confidence: conf('None') })), false);
  assert.equal(tools.likelyWithoutTier(null), false);
});

test('B-04, B-24, B-25: Step 10 and the checklist use the new lines and the right helper', () => {
  assert.match(SHELL.likelyNoTier, /^A suspect can be listed as Likely while no tier is dated\./);
  assert.equal(SHELL.showRuledOut, 'Show the suspects set aside');
  assert.equal(SHELL.showFewerSuspects, 'Show fewer suspects');
  assert.equal(SHELL.suggestedAdded(1), 'Added 1 check.');
  assert.equal(SHELL.suggestedAdded(3), 'Added 3 checks.');
  const s10 = guided('Step10Origin.svelte');
  assert.match(s10, /\{showSetAside \? S\.showFewerSuspects : S\.showRuledOut\}/);
  assert.ok(!/T\.showAll|T\.showLess/.test(s10), 'the set-aside button no longer says every API');
  assert.match(s10, /\{#if likelyNoTier\}<p class="note">\{S\.likelyNoTier\}<\/p>\{\/if\}/);
  assert.match(guided('Checklist.svelte'), /originChecks\(store\.result, store\.items\)/);
  assert.match(guided('Checklist.svelte'), /if \(added > 0\) note = S\.suggestedAdded\(added\);/);
  assert.match(store(), /for \(const check of originChecks\(result, this\.items\)\)/);
});

test('B-13: limits are counted in characters and said, never cut in silence', () => {
  assert.equal(tools.MAX_ITEM_TEXT, 300);
  assert.equal(tools.itemTooLong('x'.repeat(300)), false);
  assert.equal(tools.itemTooLong('x'.repeat(301)), true);
  assert.equal(tools.itemTooLong('  ' + 'x'.repeat(300) + '  '), false, 'the ends are trimmed first');
  assert.equal(tools.itemTooLong('\u{1F600}'.repeat(300)), false, 'wide characters count once');
  assert.equal(tools.viewNameTooLong('n'.repeat(60)), false);
  assert.equal(tools.viewNameTooLong('n'.repeat(61)), true);
  assert.equal(tools.viewNameTooLong('a\u0001   b'), false);
  assert.equal(tools.cleanViewName('  a \u0001 b  '), 'a b');
  assert.equal(tools.cleanViewName('\u{1F600}'.repeat(70)), '\u{1F600}'.repeat(60));
  assert.equal(tools.cleanViewName('   '), '');
});

test('B-13, B-14: the fields no longer cut with maxlength and the store refuses over the limit', () => {
  const list = guided('Checklist.svelte');
  const views = guided('SavedViews.svelte');
  const manual = guided('ManualTools.svelte');
  for (const [name, source] of [['Checklist', list], ['SavedViews', views], ['ManualTools', manual]]) {
    assert.ok(!/maxlength=\{?"?(300|MAX_VIEW_NAME)/.test(source.replace(/maxlength="100"/g, '')), `${name}: no silent maxlength on a counted field`);
  }
  assert.match(list, /<input type="text" bind:value=\{text\} required aria-invalid=\{over\} \/>/);
  assert.match(list, /disabled=\{text\.trim\(\) === '' \|\| over\}/);
  assert.match(manual, /disabled=\{store\.noteDraft\.trim\(\) === '' \|\| noteOver\}/);
  assert.match(manual, /maxlength="100" bind:value=\{match\}/, 'the match box keeps its own short limit');
  assert.match(views, /<input type="text" bind:value=\{name\} aria-invalid=\{over\} \/>/);
  assert.ok(!/<input[^>]*bind:value=\{name\}[^>]*required/.test(views), 'B-14: no browser bubble on the view name');
  assert.match(views, /disabled=\{over\}/);
  const s = store();
  assert.match(s, /if \(itemTooLong\(clean\)\) \{ this\.planError = GUIDED_TEXT\.itemTooLong; return false; \}/);
  assert.ok(!/clean\.slice\(0, 300\)/.test(s));
  assert.match(s, /if \(viewNameTooLong\(name\)\) \{ this\.viewError = SHELL_TEXT\.viewNameTooLong; return false; \}/);
  assert.match(s, /cause\.reason === 'plan-invalid'\s*\? SHELL_TEXT\.itemNotPlain/);
  assert.equal(SHELL.viewNameTooLong, 'A view name holds up to 60 characters. Shorten it to save.');
  assert.equal(SHELL.itemNotPlain, 'An item holds plain text only. Remove any control characters and try again.');
});

test('B-12: a text that matches no API says so, a high minimum keeps its own sentence', () => {
  assert.equal(SHELL.noApiMatch('orders'), 'No API matches "orders".');
  const m = guided('ManualTools.svelte');
  assert.match(m, /const noMatch = \$derived\(match\.trim\(\) !== '' && slowestApiRows\(store\.result, 0, match\)\.length === 0\);/);
  assert.match(m, /\{noMatch \? S\.noApiMatch\(match\.trim\(\)\) : T\.noApiRows\(Number\(minCalls\)\)\}/);
  const r = result();
  const rows = tools.slowestApiRows(r, 0, '');
  assert.ok(rows.length > 0, 'the fixture has at least one API');
  assert.equal(tools.slowestApiRows(r, 0, 'no-such-route-zzz').length, 0);
  assert.equal(tools.slowestApiRows(r, 1e9, '').length, 0, 'a minimum that is too high is a different empty state');
});

test('B-16: a closed case says why notes, checks and data cannot be added', () => {
  assert.equal(SHELL.closedNoAdd, 'This case is closed, so notes and checks cannot be added here. You can still read everything.');
  assert.equal(SHELL.closedNoData, 'This case is closed, so no data can be added to it.');
  const m = guided('ManualTools.svelte');
  assert.match(m, /\{:else\}\s*<p class="notice" role="status">\{S\.closedNoAdd\}<\/p>\s*\{\/if\}/);
  assert.match(m, /<form class="note-form"/, 'the note form is still there for an open case');
  const s10 = guided('Step10Origin.svelte');
  assert.match(s10, /\{#if readOnly && store\.mode === 'guided' && \(groups\.ranked\.length > 0 \|\| groups\.notTested\.length > 0\)\}<p class="notice" role="status">\{S\.closedNoAdd\}<\/p>\{\/if\}/);
  const bar = guided('RunBar.svelte');
  assert.match(bar, /\{#if readOnly\}<p class="note" role="status">\{S\.closedNoData\}<\/p>\{\/if\}/);
  assert.match(bar, /disabled=\{readOnly\} onclick=\{onAddData\}/, 'the button stays off, only explained');
});

test('B-33: the bad-entry message waits for a touch, and a second press is ignored', () => {
  const src = guided('NearbyActivity.svelte');
  assert.match(src, /let touched = \$state\(false\);/);
  assert.match(src, /\{#if touched && !win\}<p class="note" role="status">\{T\.nearbyBad\}<\/p>\{\/if\}/);
  assert.ok(!/\{#if !win\}/.test(src), 'the message no longer shows on open');
  assert.equal((src.match(/oninput=\{\(\) => \(touched = true\)\}/g) ?? []).length, 3, 'moment, before and after each mark the entry as touched');
  assert.match(src, /before = b; after = a; touched = true;/);
  assert.match(src, /\r?\n  let busy = false;\r?\n/, 'a plain flag, set in the same step as the press');
  assert.match(src, /async function run\(\) \{\r?\n    if \(busy\) return;/);
  assert.match(src, /busy = true;\r?\n    const mine = new AbortController\(\);/);
  assert.match(src, /\} finally \{\r?\n      busy = false;\r?\n    \}/);
  assert.ok(!/controller\?\.abort\(\);\r?\n    const mine/.test(src), 'the first request is no longer cancelled by a second press');
  assert.match(src, /disabled=\{!win \|\| phase === 'loading'\}/, 'the button is still off for a bad entry and while loading');
  assert.equal(SHELL.nearbyBad, undefined, 'the existing words are reused, not copied');
});
