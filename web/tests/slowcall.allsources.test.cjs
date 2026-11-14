const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the "All sources in time order" list, checked as source text.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { DRILL_TEXT } = loadTypeScript('src/lib/drill/drillCopy.ts');
const src = fs.readFileSync(path.join(root, 'src/components/CallAllSources.svelte'), 'utf8');
const markup = src.replace(/<style[\s\S]*?<\/style>/, '');
const style = (src.match(/<style[\s\S]*?<\/style>/) || [''])[0];

test('test ids are present', () => {
  for (const id of ['scall-all-heading', 'scall-all-empty', 'scall-all']) {
    assert.ok(markup.includes(`data-testid="${id}"`), id);
  }
});

test('heading, empty text and the four header keys come from the copy file', () => {
  assert.match(markup, /DRILL_TEXT\.allHeading\(items\.length\)/);
  assert.match(markup, /DRILL_TEXT\.allEmpty/);
  for (const key of ['colTime', 'colSource', 'colWhat', 'colTook']) {
    assert.match(markup, new RegExp(`DRILL_TEXT\\.${key}\\b`), key);
    assert.equal(typeof DRILL_TEXT[key], 'string', key);
  }
  assert.equal(typeof DRILL_TEXT.allHeading, 'function');
});

test('source names, dots and the time-only note', () => {
  for (const key of ['laneApi', 'laneIis', 'laneDb']) assert.match(src, new RegExp(`DRILL_TEXT\\.${key}\\b`), key);
  for (const lane of ['--lane-audit', '--lane-iis', '--lane-sql']) assert.ok(src.includes(`var(${lane})`), lane);
  assert.match(markup, /class="dot" aria-hidden="true"/);
  assert.match(markup, /<p class="muted">\{DRILL_TEXT\.notShownTime\}<\/p>/);
});

test('missing numbers say not recorded; markers leave the cell empty', () => {
  assert.match(src, /DRILL_TEXT\.notRecorded/);
  assert.match(src, /if \(item\.marker\) return ''/);
  assert.match(src, /formatMs\(item\.tookMs\)/);
});

test('this call and selected rows are marked; click and Enter select', () => {
  assert.match(markup, /aria-current=\{item\.thisCall \? 'true' : undefined\}/);
  assert.match(markup, /DRILL_TEXT\.thisCallMark/);
  assert.match(markup, /class:selected=\{selectedKey === item\.key\}/);
  assert.match(markup, /onclick=\{\(\) => onSelect\(item\.key\)\}/);
  assert.match(src, /event\.key === 'Enter'/);
});

test('the list has no inner scroll box', () => {
  assert.doesNotMatch(style, /overflow(-[xy])?\s*:\s*(auto|scroll)/);
  assert.doesNotMatch(markup, /overflow(-[xy])?\s*:\s*(auto|scroll)/);
});
