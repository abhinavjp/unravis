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

const { resolveHelpLink, titleFromSource, PAGE_ORDER } = loadTypeScript('src/lib/help/links.ts');
const known = ['index', 'cases', 'getting-started'];
const none = { kind: 'none' };

test('a .md link to a known page resolves to that page', () => {
  assert.deepEqual(resolveHelpLink('cases.md', known), { kind: 'page', slug: 'cases', anchor: '' });
  assert.deepEqual(resolveHelpLink('getting-started.md', known), { kind: 'page', slug: 'getting-started', anchor: '' });
});

test('a .md link keeps its anchor, with or without ./', () => {
  assert.deepEqual(resolveHelpLink('cases.md#open-a-case', known), { kind: 'page', slug: 'cases', anchor: 'open-a-case' });
  assert.deepEqual(resolveHelpLink('./cases.md', known), { kind: 'page', slug: 'cases', anchor: '' });
  assert.deepEqual(resolveHelpLink('./cases.md#a', known), { kind: 'page', slug: 'cases', anchor: 'a' });
});

test('a .md link to an unknown slug or odd path gives none', () => {
  assert.deepEqual(resolveHelpLink('nope.md', known), none);
  assert.deepEqual(resolveHelpLink('nope.md#a', known), none);
  assert.deepEqual(resolveHelpLink('../cases.md', known), none);
  assert.deepEqual(resolveHelpLink('screenshots/cases.md', known), none);
  assert.deepEqual(resolveHelpLink('cases.md?x=1', known), none);
});

test('#a is an anchor on the same page', () => {
  assert.deepEqual(resolveHelpLink('#what-next', known), { kind: 'anchor', anchor: 'what-next' });
  assert.deepEqual(resolveHelpLink('#', known), none);
});

test('only https is external', () => {
  assert.deepEqual(resolveHelpLink('https://example.com/a?b=1#c', known), { kind: 'external', href: 'https://example.com/a?b=1#c' });
  assert.deepEqual(resolveHelpLink('http://x', known), none);
  assert.deepEqual(resolveHelpLink('https://', known), none);
});

test('dangerous or unsupported links give none', () => {
  const bad = [
    'javascript:alert(1)', 'JavaScript:alert(1)', 'data:text/html,hi', 'mailto:a@b.c', '',
    '//evil.example', '//evil.example/x.md', 'a\\b.md', 'https:\\\\evil.example', 'vbscript:x',
    'file:///c:/x', ' https://x.example', 'cases.md ',
  ];
  for (const href of bad) assert.deepEqual(resolveHelpLink(href, known), none, JSON.stringify(href));
  assert.deepEqual(resolveHelpLink(undefined, known), none);
});

test('titleFromSource takes the first # line, else the slug', () => {
  assert.equal(titleFromSource('intro\n\n# The title\n\n# Second\n', 'x'), 'The title');
  assert.equal(titleFromSource('# Cases  \r\ntext', 'cases'), 'Cases');
  assert.equal(titleFromSource('## Only sub\ntext', 'getting-started'), 'Getting-started');
  assert.equal(titleFromSource('', 'index'), 'Index');
  assert.equal(titleFromSource('#nospace\n', 'index'), 'Index');
});

test('PAGE_ORDER has no duplicates and every name has a docs file', () => {
  assert.equal(new Set(PAGE_ORDER).size, PAGE_ORDER.length);
  for (const name of PAGE_ORDER) {
    assert.ok(fs.existsSync(path.join(root, '..', 'docs', 'user', name + '.md')), name);
  }
});

test('every docs/user page except screenshots.md is in PAGE_ORDER', () => {
  const files = fs.readdirSync(path.join(root, '..', 'docs', 'user')).filter((f) => f.endsWith('.md') && f !== 'screenshots.md');
  for (const f of files) assert.ok(PAGE_ORDER.includes(f.replace(/\.md$/, '')), f);
  assert.ok(!PAGE_ORDER.includes('screenshots'));
});
