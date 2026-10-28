const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
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

const guide = read('src/components/HelpGuide.svelte');
const glossary = read('src/components/Glossary.svelte');
const { HELP_GUIDE_TEXT } = loadTypeScript('src/lib/copy/helpCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('the guide never builds HTML from text and shows no pictures', () => {
  assert.doesNotMatch(guide, /\{@html/);
  assert.doesNotMatch(guide, /<img\b/);
  assert.doesNotMatch(guide, /innerHTML|insertAdjacentHTML/);
});

test('every link goes through the link rules and opens only as a page, anchor, https link or plain text', () => {
  assert.match(guide, /resolveHelpLink\(node\.href, helpSlugs\)/);
  assert.match(guide, /target\.kind === 'page'/);
  assert.match(guide, /target\.kind === 'anchor'/);
  assert.match(guide, /target\.kind === 'external'/);
  assert.match(guide, /target="_blank" rel="noopener noreferrer"/);
  assert.equal((guide.match(/<a href=/g) ?? []).length, 1, 'the only anchor element is the external link');
});

test('the guide renders headings shifted down two levels with page-scoped ids, a real table in a scrolling region, and code blocks', () => {
  assert.match(guide, /Math\.min\(6, block\.level \+ 2\)/);
  assert.match(guide, /id=\{'help-' \+ slug \+ '-' \+ block\.id\}/);
  assert.match(guide, /tabindex="-1"/);
  assert.match(guide, /<table>/);
  assert.match(guide, /class="guide-table" role="region" aria-label=\{page\?\.title \?\? ''\} tabindex="0"/);
  assert.match(guide, /\.guide-table \{[^}]*overflow-x: auto/);
  assert.match(guide, /<pre><code>\{block\.text\}<\/code><\/pre>/);
  assert.match(guide, /pre \{[^}]*overflow-x: auto/);
  assert.match(guide, /<nav aria-label=\{HELP_GUIDE_TEXT\.guideContents\}>/);
  assert.match(guide, /aria-current=\{entry\.slug === slug \? 'page' : undefined\}/);
  assert.match(guide, /\{#snippet blocksView/);
  assert.match(guide, /\{@render blocksView\(item\.sub\)\}/);
  assert.match(guide, /HELP_GUIDE_TEXT\.guideMissing/);
});

test('an image shows only its description as a muted line', () => {
  assert.match(guide, /class="guide-image">\{HELP_GUIDE_TEXT\.guideImage\}\{block\.children\[0\]\.alt\}/);
  assert.match(guide, /\.guide-image \{[^}]*color: var\(--text-2\)/);
});

test('the Help drawer opens and closes the guide, and keeps one Esc handler', () => {
  assert.match(glossary, /import HelpGuide from '\.\/HelpGuide\.svelte'/);
  assert.match(glossary, /data-testid="guide-open"/);
  assert.match(glossary, /data-testid="guide-back"/);
  assert.match(glossary, /let guide = \$state<\{ slug: string; anchor: string \} \| null>\(null\)/);
  assert.match(glossary, /guide = \{ slug: 'index', anchor: '' \}/);
  assert.match(glossary, /<button type="button" class="update-button guide-open"/);
  assert.doesNotMatch(glossary, /primary/);
  assert.match(glossary, /guideOpen\?\.focus\(\)/);
  assert.doesNotMatch(glossary, /onkeydown|Escape/, 'Esc stays with the Drawer');
  assert.match(glossary, /<Drawer class="glossary-drawer" title=\{HELP_TEXT\.title\} titleId="glossary-title" closeLabel="Close help and glossary"/);
});

test('the guide replaces the help parts and opening a glossary term never opens it', () => {
  const body = glossary.slice(glossary.indexOf('{#if guide}'), glossary.indexOf('</Drawer>'));
  assert.ok(body.indexOf('<HelpGuide') < body.indexOf('{:else}'), 'the guide sits in the first branch');
  for (const part of ['help-start', 'glossary-heading', 'glossary-search', 'build-line', 'update-check-button']) {
    assert.ok(body.indexOf(part) > body.indexOf('{:else}'), part + ' is hidden while the guide is open');
  }
  assert.equal((glossary.match(/guide = \{/g) ?? []).length, 2, 'only the button and the guide links set a page');
  const effect = glossary.slice(glossary.indexOf('$effect('), glossary.indexOf('</script>'));
  assert.doesNotMatch(effect, /guide/);
});

test('the guide copy has its five keys and passes the copy lint', () => {
  assert.deepEqual(Object.keys(HELP_GUIDE_TEXT).sort(), ['guideBack', 'guideButton', 'guideContents', 'guideImage', 'guideMissing']);
  assert.equal(HELP_GUIDE_TEXT.guideButton, 'Open the user guide');
  assert.equal(HELP_GUIDE_TEXT.guideBack, 'Back to help');
  assert.equal(HELP_GUIDE_TEXT.guideContents, 'Pages');
  assert.equal(HELP_GUIDE_TEXT.guideImage, 'Picture: ');
  assert.equal(HELP_GUIDE_TEXT.guideMissing, 'This page is not available.');
  const entries = Object.entries(HELP_GUIDE_TEXT).map(([id, text]) => ({ id: 'guide-' + id, text: text.trim() }));
  assert.deepEqual(lintCopy(entries, deck.terms), []);
});

test('the new and changed files keep CRLF line endings', () => {
  for (const file of ['src/components/HelpGuide.svelte', 'src/components/Glossary.svelte', 'src/lib/copy/helpCopy.ts']) {
    const text = fs.readFileSync(path.join(root, file), 'utf8');
    assert.ok(!/(?<!\r)\n/.test(text), file + ' has a bare LF');
  }
});
