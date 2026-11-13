const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the three-lane chart component, checked as source text.

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
const source = fs.readFileSync(path.join(root, 'src/components/CallLanes.svelte'), 'utf8');

test('the three lanes are drawn with their own tokens', () => {
  for (const lane of ['audit', 'iis', 'sql']) {
    assert.ok(source.includes(`data-lane={name}`) || source.includes(`data-lane="${lane}"`), 'data-lane attribute');
    assert.ok(source.includes(`var(--lane-${lane})`), `uses --lane-${lane}`);
    assert.ok(new RegExp(`${lane}:\\s*'var\\(--lane-${lane}\\)'`).test(source), `colour map has ${lane}`);
  }
  assert.match(source, /name: 'audit' as LaneName/);
  assert.match(source, /name: 'iis' as LaneName/);
  assert.match(source, /name: 'sql' as LaneName/);
  assert.match(source, /data-testid="scall-lane-\{name\}"/);
  assert.match(source, /data-testid="scall-lanes"/);
  assert.match(source, /aria-busy=\{loading\}/);
});

test('every part of the chart is present', () => {
  for (const needle of [
    'class="gap"', 'class="thiscall"', 'class="bar"', 'class="shape"', 'scall-readout', 'scall-legend', 'scall-db-source',
    'preserveAspectRatio="none"', '<pattern', 'role="button"', 'tabindex="0"', 'aria-live="polite"', 'data-key'
  ]) {
    assert.ok(source.includes(needle), `has ${needle}`);
  }
  assert.match(source, /Math\.max\(.*, 2 \* pxX\)/, 'the call column is at least 2 px wide');
  assert.match(source, /patternUnits="userSpaceOnUse"/);
  assert.equal((source.match(/<pattern /g) ?? []).length, 1, 'one shared pattern');
  assert.match(source, /instanceCounter/, 'pattern id is unique per instance');
});

test('hover, pin and keyboard are wired', () => {
  assert.match(source, /import \{[^}]*readoutAt[^}]*\} from '\.\.\/lib\/drill\/callReadout'/);
  assert.match(source, /onpointermove=\{onMove\}/);
  assert.match(source, /onpointerleave=\{onLeave\}/);
  assert.match(source, /onclick=\{onClick\}/);
  assert.match(source, /onReadout\(readoutFor\(hoverMs\), false\)/);
  assert.match(source, /onReadout\(readoutFor\(pinnedMs\), true\)/);
  assert.match(source, /'ArrowLeft'/);
  assert.match(source, /'ArrowRight'/);
  assert.match(source, /e\.key === 'Enter' \|\| e\.key === ' '/);
  assert.match(source, /onSelect\(key\)/);
});

test('the database lane never draws a flat zero line when it has no shape', () => {
  assert.match(source, /db\.shape === 'none'/);
  assert.match(source, /db\.shape === 'markers'/);
  assert.match(source, /<p class="scall-note">\{db\.short\}<\/p>/);
  assert.match(source, /hatchAll\(name\)/);
});

test('fixed heights: no scrolling inside the lanes', () => {
  assert.doesNotMatch(source, /overflow(-y)?:\s*(auto|scroll)/);
  assert.doesNotMatch(source, /max-height/);
  assert.match(source, /\.scall-lane \{[^}]*height: var\(--scall-lane-h, 96px\);[^}]*overflow: hidden;/);
  assert.match(source, /\.scall-lanes \{[^}]*overflow: visible;/);
});

test('a missing number reads "not recorded", and the old chart is not used', () => {
  assert.match(source, /DRILL_TEXT\.notRecorded/);
  assert.doesNotMatch(source, /TracingChart/);
  assert.doesNotMatch(source, /\?\? 0\b[^;]*DRILL_TEXT\.read/);
});

test('every copy key the component uses exists', () => {
  const used = new Set([...source.matchAll(/DRILL_TEXT\.(\w+)/g)].map((m) => m[1]));
  assert.ok(used.size >= 20, `uses ${used.size} keys`);
  for (const key of used) assert.ok(key in DRILL_TEXT, `DRILL_TEXT.${key} exists`);
  for (const key of [
    'laneApi', 'laneIis', 'laneDb', 'laneSource', 'laneSlow', 'laneNothingSlow', 'laneShape', 'laneMoreBars',
    'readTitle', 'readApi', 'readP95', 'readIis', 'readRefused', 'readDb', 'readDbNone', 'readHover', 'readPinned',
    'legendWidth', 'legendShape', 'legendHatched', 'legendCall'
  ]) assert.ok(used.has(key), `component uses ${key}`);
});

test('the component holds no sentence of its own', () => {
  const markup = source.replace(/<script[\s\S]*?<\/script>/g, '').replace(/<style[\s\S]*?<\/style>/g, '');
  let flat = markup;
  for (let i = 0; i < 4; i += 1) flat = flat.replace(/\{[^{}]*\}/g, ' ');
  const text = flat.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
  assert.equal(text.replace(/[·]/g, '').trim(), '', `leftover text: ${text}`);
});
