const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, read } = require('./scn1.l4.helpers.cjs');
const upd = loadTypeScript('src/lib/updateCheck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('D-C-07: a bad update value clears the stored preview', () => {
  assert.equal(upd.updatePreviewAfter('#update=bogus', 'available'), '');
  assert.equal(upd.updatePreviewAfter('#update=', 'latest'), '');
  assert.equal(upd.updatePreviewAfter('#update=Latest', 'latest'), '');
  assert.equal(upd.updatePreviewAfter('#x=1&update=zzz', 'available'), '');
});

test('D-C-07: an address without update= keeps the stored preview', () => {
  assert.equal(upd.updatePreviewAfter('#t=abc', 'available'), 'available');
  assert.equal(upd.updatePreviewAfter('', 'offline'), 'offline');
  assert.equal(upd.updatePreviewAfter('#t=abc', ''), '');
});

test('D-C-07: a good value replaces the stored one', () => {
  assert.equal(upd.updatePreviewAfter('#update=latest', 'offline'), 'latest');
  assert.equal(upd.updatePreviewAfter('#t=abc&update=offline', 'latest'), 'offline');
  assert.equal(upd.updatePreviewAfter('#update=latest&t=1', ''), 'latest');
});

test('D-C-07: storage is cleared too', () => {
  const src = read('src/lib/updateCheck.ts');
  assert.match(src, /if \(next\) sessionStorage\.setItem\(UPDATE_PREVIEW_KEY, next\);\s*else sessionStorage\.removeItem\(UPDATE_PREVIEW_KEY\);/);
  assert.match(src, /const next = updatePreviewAfter\(location\.hash, updatePreview\);/);
  assert.match(src, /window\.addEventListener\('popstate', noticeUpdatePreview\);/);
  assert.match(src, /window\.addEventListener\('hashchange', noticeUpdatePreview\);/);
});

test('D-C-09: every update sentence has the exact wording and passes the copy lint', () => {
  const sentences = upd.updateCheckSentences();
  assert.deepEqual(sentences, ['You are on the latest version (v1.2.0).', 'Version 1.3.0 is available. You have v1.2.0.', 'This is a development build, so there is nothing to compare.', 'Could not check for updates. Check your connection and try again later.']);
  assert.deepEqual(lintCopy(sentences.map((text, index) => ({ id: 'update-' + index, text })), deck.terms), []);
});

test('D-C-09: the Help panel labels exist and pass the copy lint', () => {
  const glossary = read('src/components/Glossary.svelte');
  const labels = ['Check for updates', 'Checking...', 'Open the release page'];
  for (const label of labels) assert.ok(glossary.includes(label), 'Glossary.svelte shows: ' + label);
  assert.deepEqual(lintCopy(labels.map((text, index) => ({ id: 'update-label-' + index, text })), deck.terms), []);
});
