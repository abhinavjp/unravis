const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the Inspector panel and the demo-only "Pretend the database is" switch, checked as source text.

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
const src = fs.readFileSync(path.join(root, 'src/components/CallInspector.svelte'), 'utf8');
const markup = src.replace(/<style[\s\S]*?<\/style>/, '').replace(/<script[\s\S]*?<\/script>/, '');
const script = (src.match(/<script[\s\S]*?<\/script>/) || [''])[0];
const style = (src.match(/<style[\s\S]*?<\/style>/) || [''])[0];

test('the panel is an aside with the right test id and label', () => {
  assert.match(markup, /<aside class="scall-inspector" data-testid="scall-inspector" aria-label=\{DRILL_TEXT\.secSelected\}>/);
});

test('the five sections each have an h3 from their copy key', () => {
  for (const key of ['secSelected', 'secWhile', 'secSources', 'secNotShown', 'secHow']) {
    assert.ok(DRILL_TEXT[key], key + ' exists in the copy');
    assert.match(markup, new RegExp('<h3>\\{DRILL_TEXT\\.' + key + '\\}</h3>'), key);
  }
  assert.equal((markup.match(/<section/g) || []).length, 5);
});

test('the sections use their copy keys', () => {
  for (const key of ['selectedNone', 'readApi', 'readIis', 'readDb', 'readDbNone', 'coverageNote', 'notShownTime', 'notShownStart', 'notShownWholeDb', 'notShownIis', 'legendWidth', 'legendShape', 'legendHatched', 'legendCall']) {
    assert.ok(DRILL_TEXT[key] !== undefined, key + ' exists in the copy');
    assert.match(src, new RegExp('DRILL_TEXT\\.' + key + '\\b'), key);
  }
  assert.match(src, /DRILL_HELP\.atOnce/);
  assert.match(markup, /data-testid="scall-sources"/);
  assert.match(src, /formatMs\(/);
  assert.match(src, /formatTime\(selected\.startMs, \{ zone \}\)/);
});

test('the database sentence comes from dbState and carries its key', () => {
  assert.match(markup, /<p data-testid="scall-db-state" data-state=\{db\.key\}>\{db\.message\}<\/p>/);
  assert.match(script, /import type \{[^}]*DbStateKey[^}]*DbStateView[^}]*\} from '\.\.\/lib\/drill\/dbState'/);
});

test('a missing number says "not recorded", never 0', () => {
  assert.match(src, /DRILL_TEXT\.notRecorded/);
  assert.match(markup, /#if !source\.recorded\} · \{DRILL_TEXT\.notRecorded\}/);
  assert.doesNotMatch(src, /\?\? 0\b/);
});

test('the pretend block sits inside {#if demo} and nowhere else', () => {
  assert.equal((markup.match(/\{#if demo\}/g) || []).length, 1);
  const start = markup.indexOf('{#if demo}');
  const asideEnd = markup.indexOf('</aside>');
  assert.ok(start > 0 && asideEnd > start);
  const block = markup.slice(start, asideEnd);
  assert.match(block, /data-testid="scall-pretend"/);
  assert.match(block, /DRILL_TEXT\.pretendLabel/);
  assert.match(block, /DRILL_TEXT\.pretendNote/);
  assert.match(block, /\{\/if\}\s*$/);
  const outside = markup.slice(0, start) + markup.slice(asideEnd);
  assert.doesNotMatch(outside, /scall-pretend|pretendLabel|pretendNote|onPretend/);
});

test('ten pretend buttons: real data plus nine states, pressed state and callback', () => {
  const ids = ['real', 'qs', 'qsfile', 'api', 'off', 'ro', 'late', 'denied', 'events', 'notread'];
  for (const id of ids) assert.match(src, new RegExp("id: '" + id + "'"), id);
  assert.match(src, /id: 'real', state: null/);
  assert.match(markup, /data-testid="scall-pretend-\{option\.id\}"/);
  assert.match(markup, /aria-pressed=\{pretend === option\.state\}/);
  assert.match(markup, /onclick=\{\(\) => onPretend\(option\.state\)\}/);
  const keys = ['pretendReal', 'pretendQs', 'pretendFile', 'pretendApi', 'pretendOff', 'pretendRo', 'pretendLate', 'pretendDenied', 'pretendEvents', 'pretendNotRead'];
  for (const key of keys) {
    assert.ok(DRILL_TEXT[key], key + ' exists in the copy');
    assert.match(src, new RegExp('DRILL_TEXT\\.' + key + '\\b'), key);
  }
});

test('no sentence of its own and no inner scroll box', () => {
  assert.doesNotMatch(src, /Query Store/);
  assert.doesNotMatch(src, /'[^']*\b(because|due to|caused|waiting on|blocked by|led to|results in|responsible for|leak|starve|saturate|proven)\b[^']*'/i);
  assert.doesNotMatch(style, /overflow(-y)?\s*:\s*(auto|scroll)/);
  assert.doesNotMatch(style, /max-height|[^-]height\s*:/);
});
