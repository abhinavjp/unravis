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

const { parseMarkdown, slugify } = loadTypeScript('src/lib/help/markdown.ts');
const text = (s) => ({ t: 'text', text: s });

test('headings get levels and github style slugs', () => {
  const blocks = parseMarkdown('# Getting started\n\n## What\'s new? (v2)\n\n#### Deep one');
  assert.deepEqual(blocks.map((b) => [b.t, b.level, b.id]), [
    ['heading', 1, 'getting-started'],
    ['heading', 2, 'whats-new-v2'],
    ['heading', 4, 'deep-one'],
  ]);
  assert.equal(slugify('A  B-c_d'), 'a--b-cd');
});

test('duplicate heading ids get numbered suffixes within one parse', () => {
  const ids = parseMarkdown('# Notes\n\n## Notes\n\n## Notes').map((b) => b.id);
  assert.deepEqual(ids, ['notes', 'notes-1', 'notes-2']);
  assert.deepEqual(parseMarkdown('# Notes').map((b) => b.id), ['notes']);
});

test('heading slug uses the plain text of inline markup', () => {
  const [h] = parseMarkdown('## The `code` **bold** [link](x.md)');
  assert.equal(h.id, 'the-code-bold-link');
});

test('a paragraph joins soft line breaks with a space', () => {
  assert.deepEqual(parseMarkdown('one\ntwo\n  three'), [{ t: 'p', children: [text('one two three')] }]);
});

test('blank lines split paragraphs', () => {
  assert.equal(parseMarkdown('one\n\ntwo').length, 2);
});

test('an unordered list has items and no sub blocks', () => {
  const [list] = parseMarkdown('- a\n- b\n* c');
  assert.equal(list.t, 'list');
  assert.equal(list.ordered, false);
  assert.deepEqual(list.items, [
    { children: [text('a')], sub: [] },
    { children: [text('b')], sub: [] },
    { children: [text('c')], sub: [] },
  ]);
});

test('a nested list under an ordered item becomes sub blocks', () => {
  const [list] = parseMarkdown('1. First\n   - inner a\n   - inner b\n2. Second\n   continues here');
  assert.equal(list.ordered, true);
  assert.equal(list.items.length, 2);
  assert.deepEqual(list.items[0].children, [text('First')]);
  assert.equal(list.items[0].sub.length, 1);
  assert.equal(list.items[0].sub[0].t, 'list');
  assert.equal(list.items[0].sub[0].ordered, false);
  assert.deepEqual(list.items[0].sub[0].items.map((i) => i.children), [[text('inner a')], [text('inner b')]]);
  assert.deepEqual(list.items[1].children, [text('Second continues here')]);
  assert.deepEqual(list.items[1].sub, []);
});

test('a list item continuation and two space nesting work together', () => {
  const [list] = parseMarkdown('- Top line\n  wrapped\n  - child\n    - grandchild');
  assert.equal(list.items.length, 1);
  assert.deepEqual(list.items[0].children, [text('Top line wrapped')]);
  const child = list.items[0].sub[0];
  assert.deepEqual(child.items[0].children, [text('child')]);
  assert.deepEqual(child.items[0].sub[0].items[0].children, [text('grandchild')]);
});

test('a blank line between items of the same list keeps one list', () => {
  const blocks = parseMarkdown('1. one\n\n2. two\n\n3. three');
  assert.equal(blocks.length, 1);
  assert.equal(blocks[0].items.length, 3);
});

test('a table with an escaped pipe keeps the pipe in the cell', () => {
  const [table] = parseMarkdown('| Name | Value |\n| :--- | ---: |\n| a \\| b | `x` |\n| c | **d** |');
  assert.equal(table.t, 'table');
  assert.deepEqual(table.head, [[text('Name')], [text('Value')]]);
  assert.deepEqual(table.rows[0], [[text('a | b')], [{ t: 'code', text: 'x' }]]);
  assert.deepEqual(table.rows[1][1], [{ t: 'strong', children: [text('d')] }]);
  assert.equal(table.rows.length, 2);
});

test('a table ends at a blank line and a short row is padded', () => {
  const blocks = parseMarkdown('| A | B |\n|---|---|\n| only |\n\nafter');
  assert.equal(blocks[0].rows[0].length, 2);
  assert.deepEqual(blocks[0].rows[0][1], []);
  assert.equal(blocks[1].t, 'p');
});

test('fenced code with html and markers inside stays text', () => {
  const [code] = parseMarkdown('```json\n{ "a": "<b>x</b>", "n": **x** }\n\n# not a heading\n```');
  assert.deepEqual(code, { t: 'code', lang: 'json', text: '{ "a": "<b>x</b>", "n": **x** }\n\n# not a heading' });
});

test('a fence without a language and an unclosed fence both work', () => {
  assert.deepEqual(parseMarkdown('```\na\n```')[0], { t: 'code', lang: '', text: 'a' });
  assert.deepEqual(parseMarkdown('```\na\nb')[0], { t: 'code', lang: '', text: 'a\nb' });
});

test('inline code keeps its contents verbatim', () => {
  const [p] = parseMarkdown('use `a **b** _c_ <i>` now');
  assert.deepEqual(p.children, [text('use '), { t: 'code', text: 'a **b** _c_ <i>' }, text(' now')]);
});

test('snake_case_name and a*b stay text', () => {
  assert.deepEqual(parseMarkdown('snake_case_name and a*b*c')[0].children, [text('snake_case_name and a*b*c')]);
});

test('strong and em nest and mix', () => {
  const [p] = parseMarkdown('**bold _and em_** and *star em* and _under em_.');
  assert.deepEqual(p.children, [
    { t: 'strong', children: [text('bold '), { t: 'em', children: [text('and em')] }] },
    text(' and '),
    { t: 'em', children: [text('star em')] },
    text(' and '),
    { t: 'em', children: [text('under em')] },
    text('.'),
  ]);
});

test('an unclosed ** stays text', () => {
  assert.deepEqual(parseMarkdown('this **never closes')[0].children, [text('this **never closes')]);
  assert.deepEqual(parseMarkdown('a ` b [c](d')[0].children, [text('a ` b [c](d')]);
});

test('an image becomes an image node', () => {
  const [p] = parseMarkdown('![Place a Blackbox, part 1, dark](screenshots/P9/a.png)');
  assert.deepEqual(p.children, [{ t: 'image', alt: 'Place a Blackbox, part 1, dark', src: 'screenshots/P9/a.png' }]);
});

test('a link with an anchor keeps its href and inline children', () => {
  const [p] = parseMarkdown('See [the **setup** page](install.md#step-2) or [here](#top).');
  assert.deepEqual(p.children, [
    text('See '),
    { t: 'link', href: 'install.md#step-2', children: [text('the '), { t: 'strong', children: [text('setup')] }, text(' page')] },
    text(' or '),
    { t: 'link', href: '#top', children: [text('here')] },
    text('.'),
  ]);
});

test('a block quote parses its inner blocks', () => {
  const [quote] = parseMarkdown('> **In a nutshell:** short.\n> NOT RUN: not yet\n>\n> - item');
  assert.equal(quote.t, 'quote');
  assert.equal(quote.blocks.length, 2);
  assert.equal(quote.blocks[0].t, 'p');
  assert.deepEqual(quote.blocks[0].children[0], { t: 'strong', children: [text('In a nutshell:')] });
  assert.equal(quote.blocks[1].t, 'list');
});

test('angle brackets are plain text', () => {
  assert.deepEqual(parseMarkdown('a <script>x</script> b')[0].children, [text('a <script>x</script> b')]);
});

test('CRLF input parses like LF input', () => {
  const lf = '# Title\n\n- a\n- b\n\n| A | B |\n|---|---|\n| 1 | 2 |\n';
  assert.deepEqual(parseMarkdown(lf.replace(/\n/g, '\r\n')), parseMarkdown(lf));
});

test('empty and whitespace-only input give no blocks', () => {
  assert.deepEqual(parseMarkdown(''), []);
  assert.deepEqual(parseMarkdown('   \n\n \t\n'), []);
  assert.deepEqual(parseMarkdown(undefined), []);
});

test('every user doc parses without throwing and has a heading', () => {
  const dir = path.resolve(root, '..', 'docs', 'user');
  const files = fs.readdirSync(dir).filter((f) => f.endsWith('.md'));
  assert.ok(files.length >= 12);
  for (const file of files) {
    const blocks = parseMarkdown(fs.readFileSync(path.join(dir, file), 'utf8'));
    assert.ok(blocks.some((b) => b.t === 'heading'), file + ' has a heading');
    const ids = blocks.filter((b) => b.t === 'heading').map((b) => b.id);
    assert.equal(new Set(ids).size, ids.length, file + ' heading ids are unique');
  }
});
