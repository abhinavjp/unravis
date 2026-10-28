const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const { normaliseCode, resolveCode, CODE_NOT_FOUND, CODE_PATTERN, codeIsEmpty } = loadTypeScript('src/lib/caseCode.ts');
const { ENTER_TEXT, RESTORE_FAILED, enterLintEntries } = loadTypeScript('src/lib/copy/enterCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('the pattern is 26 characters of a-z and 2-7', () => {
  assert.equal(CODE_PATTERN.source, '^[a-z2-7]{26}$');
});

test('a valid code comes back as itself', () => {
  const code = 'abcdefghij' + 'klmnopqrst' + 'uvwxy2';
  assert.equal(code.length, 26);
  assert.equal(normaliseCode(code), code);
  assert.equal(normaliseCode('a7b2c3d4e5f6g7h2j3k4m5n6p7'), 'a7b2c3d4e5f6g7h2j3k4m5n6p7');
});

test('upper case, spaces, dashes and padding are normalised', () => {
  const code = 'abcdefghij' + 'klmnopqrst' + 'uvwxy2';
  assert.equal(normaliseCode(code.toUpperCase()), code);
  assert.equal(normaliseCode('  ' + code + '\n'), code);
  assert.equal(normaliseCode(code.slice(0, 8) + '-' + code.slice(8, 16) + '-' + code.slice(16)), code);
  assert.equal(normaliseCode(code.slice(0, 5) + ' ' + code.slice(5, 13) + '\t' + code.slice(13)), code);
  assert.equal(normaliseCode(code.slice(0, 13) + '–' + code.slice(13)), code, 'an en dash');
  assert.equal(normaliseCode('​' + code + '﻿'), code, 'invisible characters from a paste');
  assert.equal(normaliseCode(code.slice(0, 13) + ' ' + code.slice(13)), code, 'a non-breaking space');
});

test('25 and 27 characters, wrong characters and empty input give null', () => {
  const code = 'abcdefghij' + 'klmnopqrst' + 'uvwxy2';
  assert.equal(normaliseCode(code.slice(0, 25)), null, '25');
  assert.equal(normaliseCode(code + 'a'), null, '27');
  assert.equal(normaliseCode(code.slice(0, 25) + '1'), null, 'a 1');
  assert.equal(normaliseCode(code.slice(0, 25) + '0'), null, 'a 0');
  assert.equal(normaliseCode(code.slice(0, 25) + '8'), null, 'an 8');
  assert.equal(normaliseCode(code.slice(0, 25) + '9'), null, 'a 9');
  assert.equal(normaliseCode(code.slice(0, 25) + '_'), null, 'an underscore');
  assert.equal(normaliseCode(code.slice(0, 25) + '!'), null, 'punctuation');
  assert.equal(normaliseCode(code.slice(0, 24) + 'éa'), null, 'an accented letter');
  assert.equal(normaliseCode(''), null, 'empty');
  assert.equal(normaliseCode('   \n '), null, 'blank');
  assert.equal(normaliseCode('-- --'), null, 'only separators');
  assert.equal(normaliseCode('a'.repeat(5000)), null, 'far too long');
  assert.equal(normaliseCode(undefined), null);
  assert.equal(normaliseCode(null), null);
  assert.equal(normaliseCode(12345), null);
});

test('the short 8-character code is not a case code', () => {
  assert.equal(normaliseCode('abcdefgh'), null);
});

test('a code that is not base32 is refused locally and the server is not asked', async () => {
  let asked = 0;
  const check = async () => { asked++; };
  for (const typed of ['', 'abc', 'x'.repeat(30), 'ABCDEFGHIJKLMNOPQRSTUVWXY1']) {
    const outcome = await resolveCode(typed, check);
    assert.deepEqual(outcome, { ok: false, message: CODE_NOT_FOUND }, JSON.stringify(typed));
  }
  assert.equal(asked, 0);
});

test('a code the server knows opens that case, asked once with the cleaned code', async () => {
  const code = 'abcdefghij' + 'klmnopqrst' + 'uvwxy2';
  const seen = [];
  const outcome = await resolveCode('  ' + code.toUpperCase().slice(0, 10) + '-' + code.slice(10) + ' ', async (id) => { seen.push(id); });
  assert.deepEqual(outcome, { ok: true, id: code });
  assert.deepEqual(seen, [code]);
});

test('an unknown, an erased, a locked and an unreachable case all give the same message as a malformed code', async () => {
  const code = 'abcdefghij' + 'klmnopqrst' + 'uvwxy2';
  const refusals = [
    { status: 404, reason: 'case-not-found' }, // unknown or erased
    { status: 400, reason: 'bad-case-id' },
    { status: 423, reason: 'locked' },
    { status: 0, reason: 'unreachable' },
    { status: 500, reason: '' },
    new Error('anything at all')
  ];
  const malformed = await resolveCode('nope', async () => {});
  assert.equal(malformed.message, CODE_NOT_FOUND);
  for (const refusal of refusals) {
    const outcome = await resolveCode(code, async () => { throw refusal; });
    assert.deepEqual(outcome, { ok: false, message: CODE_NOT_FOUND }, JSON.stringify(refusal));
    assert.equal(outcome.message, malformed.message, 'the same string for every failure');
  }
});

test('an abort is not turned into a message', async () => {
  const code = 'abcdefghij' + 'klmnopqrst' + 'uvwxy2';
  await assert.rejects(resolveCode(code, async () => { throw new DOMException('Aborted', 'AbortError'); }), { name: 'AbortError' });
});

test('the not-found message is one constant, said in plain words, and the card uses it on every code path', () => {
  assert.equal(CODE_NOT_FOUND, 'That code does not match a case on this computer.');
  const caseCode = read('src/lib/caseCode.ts');
  assert.equal((caseCode.match(/message: CODE_NOT_FOUND/g) ?? []).length, 2, 'both failure paths use the constant');
  assert.doesNotMatch(caseCode, /message: '/, 'no other message text in the code logic');
  const card = read('src/components/EnterCase.svelte');
  assert.doesNotMatch(card, /does not match a case/, 'the sentence lives only in the copy file');
  assert.match(card, /codeProblem = result\.message/);
});

test('restoring says it always makes a new case and never changes an existing one', () => {
  assert.match(ENTER_TEXT.restoreIntro, /always makes a new case/);
  assert.match(ENTER_TEXT.restoreIntro, /never changes a case you already have/);
  assert.match(ENTER_TEXT.restoreButton, /new case/i);
});

test('the copy passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(enterLintEntries(), deck.terms).map((f) => f.id + ': ' + f.message), []);
});

test('the card wires the code field, the toggle and the restore form with the usual accessibility attributes', () => {
  const card = read('src/components/EnterCase.svelte');
  assert.match(card, /<label for="\{uid\}-code">/);
  assert.match(card, /<label for="\{uid\}-path">/);
  assert.match(card, /<label for="\{uid\}-key">/);
  assert.match(card, /aria-expanded=\{open\}/);
  assert.match(card, /aria-controls="\{uid\}-restore"/);
  assert.match(card, /role="alert"/);
  assert.match(card, /<form class="part" onsubmit=\{openByCode\}>/, 'Enter submits the code');
  assert.match(card, /<form class="part" onsubmit=\{restore\}>/, 'Enter submits the restore');
  assert.match(card, /<HelpNote id="enter" \/>/);
  assert.match(card, /type="password"/);
  assert.match(card, /autocomplete="off"/);
});

test('the master key stays in a local variable: cleared before the request, never stored, logged or shown', () => {
  const card = read('src/components/EnterCase.svelte');
  assert.doesNotMatch(card, /localStorage|sessionStorage|console\.|document\.cookie|location\.|history\.|\$inspect|JSON\.stringify/);
  assert.doesNotMatch(card, />\s*\{(?:pass|attempt)\}\s*</, 'the key is never printed');
  const script = card.slice(0, card.indexOf('</script>'));
  const read1 = script.indexOf('const attempt = pass;');
  const clear = script.indexOf("pass = '';", read1);
  const send = script.indexOf('await restoreCaseFile(', read1);
  assert.ok(read1 > 0 && clear > read1 && send > clear, 'read, clear, then send');
  assert.match(script, /restoreCaseFile\(checked\.path, attempt, controller\.signal\)/, 'the local copy is what is sent');
  // The failure text comes from the model, never from the error itself.
  assert.doesNotMatch(script, /error\.message|String\(error\)/);
});

test('the start screen gets the card through the enter slot, and the old "not ready yet" text is only the fallback', () => {
  const landing = read('src/components/Landing.svelte');
  assert.match(landing, /import EnterCase from '\.\/EnterCase\.svelte'/);
  assert.match(landing, /\{#snippet enter\(\)\}<EnterCase onOpen=\{openCase\} onSetup=\{openKeySetup\} \/>\{\/snippet\}/);
  assert.ok(RESTORE_FAILED.length > 0);
});

test('an empty code field disables the button and shows no sentence; a typed wrong code still gets the one sentence (D-A-04)', async () => {
  for (const blank of ['', ' ', '  \t', '---', ' - - ', '​']) assert.equal(codeIsEmpty(blank), true, JSON.stringify(blank));
  for (const typed of ['a', 'abc', 'z'.repeat(26)]) assert.equal(codeIsEmpty(typed), false, typed);
  const outcome = await resolveCode('abc', async () => { throw new Error('never asked'); });
  assert.deepEqual(outcome, { ok: false, message: CODE_NOT_FOUND });
  const card = read('src/components/EnterCase.svelte');
  assert.match(card, /disabled=\{codeBusy \|\| codeIsEmpty\(code\)\}/);
  assert.match(card, /if \(codeBusy\) return;\s*if \(codeIsEmpty\(code\)\) return;/);
  assert.doesNotMatch(read('src/lib/caseCode.ts'), /archived/i, 'the code box never says a case is archived');
});
