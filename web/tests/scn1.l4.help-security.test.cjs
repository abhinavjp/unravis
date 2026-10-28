const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, read } = require('./scn1.l4.helpers.cjs');

const { parseMarkdown } = loadTypeScript('src/lib/help/markdown.ts');
const { resolveHelpLink } = loadTypeScript('src/lib/help/links.ts');
const text = (s) => ({ t: 'text', text: s });
const known = ['install', 'cases'];
const none = { kind: 'none' };

test('D-C-28: raw HTML in the Markdown stays plain text', () => {
  assert.deepEqual(parseMarkdown('<img src=x onerror=alert(1)>'), [{ t: 'p', children: [text('<img src=x onerror=alert(1)>')] }]);
  assert.deepEqual(parseMarkdown('<script>alert(1)</script>'), [{ t: 'p', children: [text('<script>alert(1)</script>')] }]);
  assert.deepEqual(parseMarkdown('**<b>x</b>**'), [{ t: 'p', children: [{ t: 'strong', children: [text('<b>x</b>')] }] }]);
});

test('D-C-28: entities stay as typed and a link title is dropped', () => {
  assert.deepEqual(parseMarkdown('&lt;b&gt; &amp; &quot;'), [{ t: 'p', children: [text('&lt;b&gt; &amp; &quot;')] }]);
  assert.deepEqual(parseMarkdown('[a](install.md "My title")'), [{ t: 'p', children: [{ t: 'link', href: 'install.md', children: [text('a')] }] }]);
});

test('D-C-28: the parser keeps a javascript link as data, and the resolver turns it into none', () => {
  assert.deepEqual(parseMarkdown('[a](javascript:alert(1))'), [{ t: 'p', children: [{ t: 'link', href: 'javascript:alert(1)', children: [text('a')] }] }]);
  assert.deepEqual(resolveHelpLink('javascript:alert(1)', known), none);
});

test('D-C-28: a table is not parsed, its pipes stay as text', () => {
  assert.deepEqual(parseMarkdown('| a | b |\n| c | d |'), [{ t: 'p', children: [text('| a | b | | c | d |')] }]);
});

test('D-C-28: only a known page, an anchor or plain https becomes a link', () => {
  assert.deepEqual(resolveHelpLink('install.md', known), { kind: 'page', slug: 'install', anchor: '' });
  assert.deepEqual(resolveHelpLink('install.md#', known), { kind: 'page', slug: 'install', anchor: '' });
  for (const href of ['a b.md', 'unknown.md', 'INSTALL.md', 'install.MD', 'install.md#a b', 'JAVASCRIPT:alert(1)', '\tjavascript:x', String.raw`\\server\share`]) {
    assert.deepEqual(resolveHelpLink(href, known), none, JSON.stringify(href));
  }
});

test('D-C-28: the scheme check ignores case, so HTTPS is still external', () => {
  assert.deepEqual(resolveHelpLink('HTTPS://example.com', known), { kind: 'external', href: 'HTTPS://example.com' });
});

test('D-C-28: the guide builds no HTML from text and its only anchor is the external link', () => {
  const guide = read('src/components/HelpGuide.svelte');
  assert.doesNotMatch(guide, /\{@html/);
  assert.equal((guide.match(/<a\s[^>]*href=/g) ?? []).length, 1, 'one anchor element with an href');
  assert.match(guide, /\{:else if target\.kind === 'external'\}<a href=\{target\.href\} target="_blank" rel="noopener noreferrer">/);
  for (const file of ['src/lib/help/markdown.ts', 'src/lib/help/links.ts', 'src/lib/help/pages.ts']) {
    assert.doesNotMatch(read(file), /innerHTML|@html|insertAdjacentHTML|document\.write/, file);
  }
});
