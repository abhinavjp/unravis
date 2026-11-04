const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Hub fixes, batch C, the screen side: Try again and the choices of a kept file (F1, F20), the notice that must be confirmed again after the list
// changes (F2), the page locks while a batch runs (F10), the ticks of a picked database (F13), a closed drawer stops reading zips (F14), focus
// after Clear, Remove and a run (F23, F24, F25), the dropped-folder notices (F26), the environment filter (F28) and the lost-session text (F31).
// Pure parts run as they are; the Svelte files are checked by their source, as the other component tests do.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
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

/** The text of one function or block: from its header to the line that closes it at the same indentation. */
function body(text, header) {
  const start = text.indexOf(header);
  assert.ok(start >= 0, 'missing: ' + header);
  const lineStart = text.lastIndexOf('\n', start) + 1;
  const indent = /^\s*/.exec(text.slice(lineStart))[0];
  const end = text.indexOf('\n' + indent + '}', start);
  assert.ok(end > start, 'unclosed: ' + header);
  return text.slice(start, end + 1 + indent.length + 1);
}

const batch = read('src/lib/components/ImportBatch.svelte');
const importPanel = read('src/lib/components/ImportPanel.svelte');
const panel = read('src/components/wizard/ImportFilesPanel.svelte');
const audit = read('src/components/sources/AddAuditDatabase.svelte');
const sourcesList = read('src/components/sources/SourcesList.svelte');

// ---- F26: a dropped folder says what was cut

const drop = loadTypeScript('src/lib/dropEntries.ts');
const fileEntry = (name) => ({ isFile: true, isDirectory: false, name, file: (ok) => ok({ name, size: 1 }) });
const dirEntry = (name, children) => ({
  isFile: false, isDirectory: true, name,
  createReader() { let done = false; return { readEntries(ok) { const part = done ? [] : children; done = true; ok(part); } }; }
});
const item = (entry) => ({ kind: 'file', webkitGetAsEntry: () => entry });

test('F26: more files than the cap set tooMany (and truncated), not tooDeep', async () => {
  const many = Array.from({ length: drop.MAX_DROPPED_FILES + 5 }, (_, i) => fileEntry('f' + i + '.log'));
  const result = await drop.filesFromDrop([item(dirEntry('big', many))], []);
  assert.equal(result.files.length, drop.MAX_DROPPED_FILES);
  assert.equal(result.truncated, true);
  assert.equal(result.tooMany, true);
  assert.equal(result.tooDeep, false);
});

test('F26: folders nested deeper than the cap set tooDeep (and truncated), not tooMany', async () => {
  let inner = dirEntry('d' + (drop.MAX_DROP_DEPTH + 3), [fileEntry('deep.log')]);
  for (let level = drop.MAX_DROP_DEPTH + 2; level >= 0; level--) inner = dirEntry('d' + level, [fileEntry('l' + level + '.log'), inner]);
  const result = await drop.filesFromDrop([item(inner)], []);
  assert.equal(result.truncated, true);
  assert.equal(result.tooDeep, true);
  assert.equal(result.tooMany, false);
  assert.ok(!result.files.some((file) => file.name === 'deep.log'), 'the folder past the cap was not opened');
});

test('F26: a small folder sets neither flag, and the panel tells the two cuts apart', async () => {
  const result = await drop.filesFromDrop([item(dirEntry('d', [fileEntry('a.log')]))], []);
  assert.deepEqual([result.truncated, result.tooMany, result.tooDeep], [false, false, false]);
  assert.match(panel, /if \(dropped\.tooMany\) parts\.push\(FILES_TEXT\.dropTruncated\);/);
  assert.match(panel, /if \(dropped\.tooDeep\) parts\.push\(FILES_TEXT\.dropTooDeep\);/);
  const copy = loadTypeScript('src/lib/copy/fileKindsCopy.ts');
  assert.notEqual(copy.FILES_TEXT.dropTooDeep, copy.FILES_TEXT.dropTruncated);
  assert.match(copy.FILES_TEXT.dropTooDeep, /12 levels/);
});

// ---- F31: a lost session reads as a sentence

test('F31: a lost session with no server message gets the session sentence, and the server message wins when there is one', () => {
  const copy = loadTypeScript('src/lib/copy/importCopy.ts');
  const lost = copy.errorText({ status: 401, reason: 'no_session', column: '', importId: 0, message: '' });
  assert.equal(lost, copy.REASON_TEXT.no_session);
  assert.match(lost, /session/i);
  assert.equal(copy.errorText({ status: 401, reason: '', column: '', importId: 0, message: '' }), copy.REASON_TEXT.no_session);
  assert.equal(copy.errorText({ status: 401, reason: 'no_session', column: '', importId: 0, message: 'Open Unravis again.' }), 'Open Unravis again.');
  // The batch row of a rejected file uses the same sentence.
  assert.equal(copy.batchReasonText({ verdict: 'rejected', reason: 'no_session', rowReason: '', rows: 0, rejected: 0 }), copy.REASON_TEXT.no_session);
});

// ---- F1, F20: Try again and the choices of a kept file

test('F1: Try again is offered only for a file that is meant to go, and re-checks it when pressed', () => {
  assert.match(panel, /const canRetry = \(item: StagedFile\): boolean => item\.choice !== '' && !refused\(item\);/);
  assert.match(panel, /result\.outcome\.verdict === 'rejected' && canRetry\(item\)/);
  assert.match(panel, /onclick=\{\(\) => \{ if \(canRetry\(item\)\) onRetry\?\.\(item\); \}\}/);
});

test('F1: the batch never sends a row set to "Don’t use this file", also not by Try again', () => {
  const run = body(batch, 'async function run(');
  assert.match(run, /const rows = usableFiles\(picked\);/);
  assert.ok(run.indexOf('usableFiles(picked)') < run.indexOf('rows.length === 0'), 'the check uses the filtered rows');
  assert.match(run, /const items = rows\.map\(itemOf\);/);
  const kinds = loadTypeScript('src/lib/fileKinds.ts');
  const left = { id: 1, choice: '', refusal: '', name: 'x.txt' };
  const kept = { id: 2, choice: 'iis-w3c', refusal: '', name: 'u.log' };
  assert.deepEqual(kinds.usableFiles([left, kept]).map((row) => row.id), [2]);
});

test('F20: the kind and the computer of a kept file are locked, those of a rejected file are not', () => {
  assert.match(panel, /const finished = \(item: StagedFile\): boolean => \{[\s\S]*?result\?\.state === 'done' && result\.outcome\.verdict !== 'rejected'/);
  assert.match(panel, /const choiceLocked = \(item: StagedFile\): boolean => locked\(item\) \|\| finished\(item\);/);
  assert.match(panel, /<select value=\{item\.choice\} disabled=\{item\.refusal !== '' \|\| choiceLocked\(item\)\}/);
  assert.match(panel, /<ZoneChoice [^\r\n]*disabled=\{choiceLocked\(item\)\} \/>/);
});

// ---- F2: the notice is confirmed again after the list changes

test('F2: adding or clearing files takes the confirmation back', () => {
  assert.match(batch, /onListChange=\{\(\) => \{ acknowledged = false; \}\}/);
  assert.match(panel, /onListChange\?: \(\) => void;/);
  assert.match(body(panel, 'function clear()'), /onListChange\?\.\(\);/);
  // Adding files (the `take` path) and Clear are the two ways a different set of files gets chosen; removing a row only shrinks the set.
  assert.equal((panel.match(/onListChange\?\.\(\)/g) ?? []).length, 2, 'add and clear call it');
});

// ---- F10: the page locks its single-file controls while a batch runs

test('F10: the batch reports that it is busy and the page locks the single-file controls meanwhile', () => {
  assert.match(batch, /busy = \$bindable\(false\)/);
  assert.match(batch, /onDestroy\(\(\) => \{ leave\.abort\(\); busy = false; staged = 0; \}\);/, 'leaving the screen ends the busy state');
  assert.match(importPanel, /let batchBusy = \$state\(false\);/);
  assert.match(importPanel, /<ImportBatch [^\r\n]*bind:busy=\{batchBusy\}/);
  assert.match(importPanel, /busy: phase !== 'idle' \|\| batchBusy,/, 'the single-file commit is blocked');
  assert.match(importPanel, /id="import-file" type="file"[^\r\n]*disabled=\{phase === 'importing' \|\| batchBusy\}/);
  assert.match(importPanel, /id="import-host"[^\r\n]*disabled=\{phase !== 'idle' \|\| batchBusy\}/);
  assert.match(importPanel, /id="import-type"[^\r\n]*disabled=\{phase !== 'idle' \|\| batchBusy\}/);
  assert.match(importPanel, /bind:checked=\{acknowledged\} disabled=\{phase !== 'idle' \|\| batchBusy\}/);
});

// ---- F13: ticks the person set are kept when they pick another database and come back

test('F13: the audit/app ticks of each picked database are remembered and given back, and a new host forgets them', () => {
  assert.match(audit, /let decided = \$state<Record<string, \{ audit: boolean; app: boolean \}>>\(\{\}\);/);
  assert.match(audit, /const before = name !== '' \? decided\[name\] : undefined;/);
  assert.match(audit, /if \(database !== ''\) decided = \{ \.\.\.decided, \[database\]: \{ audit: auditTick, app: appTick \} \};/);
  assert.match(body(audit, 'function resetFrom('), /decided = \{\};/);
});

// ---- F14: a closed drawer stops reading zips

test('F14: leaving the panel cancels reads in flight, and a zip is not read once the epoch moved', () => {
  // The drawer's own list ends with the drawer; the wizard's list outlives the panel (F37), so only the drawer cancels.
  assert.match(panel, /onDestroy\(\(\) => \{ if \(drawer\) staging\.cancel\(\); \}\);/);
  const expand = body(panel, 'async function expand(');
  assert.ok((expand.match(/!staging\.current\(mine\)/g) ?? []).length >= 1, 'expand checks the epoch');
  assert.ok(expand.indexOf('!staging.current(mine)') < expand.indexOf('await readZip('), 'before it reads a zip');
});

// ---- F23, F24, F25: where the focus goes

test('F23: Clear moves the focus to the choose-files button', () => {
  assert.match(body(panel, 'async function focusChoose()'), /await tick\(\);[\s\S]*chooseButton\?\.focus\(\)/);
  assert.match(body(panel, 'function clear()'), /void focusChoose\(\);/);
});

test('F24: the computer select gets the focus back after Save, Cancel and Save zone; the batch summary after a run', () => {
  const zone = read('src/components/wizard/ZoneChoice.svelte');
  assert.match(zone, /bind:this=\{selectEl\}/);
  assert.ok((zone.match(/void focusSelect\(\);/g) ?? []).length === 3, 'save, save zone only and cancel');
  assert.match(batch, /bind:this=\{summaryEl\}/);
  assert.match(batch, /tabindex="-1"/);
  const settle = body(batch, 'async function settleFocus()');
  assert.match(settle, /document\.activeElement/);
  assert.match(settle, /summaryEl\?\.focus\(\)/);
  assert.match(batch, /if \(!leave\.signal\.aborted\) \{ onFinished\?\.\(\); void settleFocus\(\); \}/);
});

test('F25: Remove skips the Remove button of a locked row', () => {
  const fn = body(panel, 'async function removeRow(');
  assert.match(fn, /querySelectorAll<HTMLButtonElement>\('\[data-remove-row\]'\)/);
  assert.match(fn, /!button\.disabled/);
});

// ---- F28: an environment filter whose environment is gone is dropped

test('F28: the Sources list drops an environment filter that no longer exists', () => {
  assert.match(sourcesList, /\$effect\(\(\) => \{ if \(phase === 'ready' && envFilter !== '' && !envs\.includes\(envFilter\)\) envFilter = ''; \}\);/);
});
