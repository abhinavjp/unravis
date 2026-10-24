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

const z = loadTypeScript('src/lib/zoom.ts');
const { zoomCopy } = loadTypeScript('src/lib/copy/zoomCopy.ts');
const { compile } = require('svelte/compiler');

const componentPath = path.join(root, 'src/components/ZoomStrip.svelte');
const source = fs.readFileSync(componentPath, 'utf8');

// Render the real component on the server side of Svelte, in memory, so the text and the buttons it draws can be checked.
function loadStrip() {
  const compiled = compile(source, { filename: 'ZoomStrip.svelte', generate: 'server' });
  const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(componentPath);
  const load = (request) => request === '../lib/zoom' ? z : request === '../lib/copy/zoomCopy' ? { zoomCopy } : nodeRequire(request);
  new Function('require', 'module', 'exports', js)(load, module, module.exports);
  return module.exports.default;
}
const { render } = require('svelte/server');
const Strip = loadStrip();
const noop = () => {};
const html = (model, extra = {}) => render(Strip, { props: { model, onBack: noop, onEarlier: noop, onLater: noop, onZoomIn: noop, onZoomOut: noop, onReset: noop, onJump: noop, ...extra } }).body;

const DAY = 86_400_000;
const week = { start: Date.parse('2001-10-22T00:00:00Z'), end: Date.parse('2001-10-29T00:00:00Z') };
const zone = 'UTC';
const baseInput = { zone, loading: false, error: '', hasData: true, asOf: Date.parse('2001-10-24T14:03:22Z'), overviewCounts: [4, 0, 9, 2, 1, 0, 3] };
const topStack = z.createStack(week);
const topModel = z.stripModel({ ...baseInput, stack: topStack, bucket: z.bucketForView(week) });
const dayStack = z.zoomIntoSlot(topStack, z.slotsOf(week, 86_400)[2], zone);
const dayModel = z.stripModel({ ...baseInput, stack: dayStack, bucket: z.bucketForView(z.currentOf(dayStack)) });
const hourStack = z.zoomIntoSlot(dayStack, z.slotsOf(z.currentOf(dayStack), 3600)[14], zone);
const hourModel = z.stripModel({ ...baseInput, stack: hourStack, bucket: z.bucketForView(z.currentOf(hourStack)) });

const buttonOf = (markup, label) => {
  const found = [...markup.matchAll(/<button\b([^>]*)>([^<]*)<\/button>/g)].find((match) => match[2] === label);
  assert.ok(found, 'no button named ' + label);
  return found[1];
};
const isDisabled = (markup, label) => /\bdisabled\b/.test(buttonOf(markup, label));

test('ZoomStrip.svelte compiles without warnings and keeps its text out of the markup', () => {
  const result = compile(source, { filename: 'ZoomStrip.svelte', generate: 'client' });
  assert.deepEqual(result.warnings.map((x) => x.code), []);
  const markup = source.slice(source.indexOf('</script>'), source.indexOf('<style>'));
  const literal = markup.replace(/\{[^}]*\}/g, '').replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
  assert.equal(literal, '', 'static text in markup: ' + literal);
});

test('the strip starts as the one line "Zoom: 7 days"', () => {
  const markup = html(topModel);
  assert.match(markup, /<summary[^>]*>\s*<span class="summary-text">Zoom: 7 days<\/span>\s*<\/summary>/);
  assert.doesNotMatch(markup, /<details[^>]*\bopen\b/, 'closed until used');
  assert.match(markup, /aria-label="Zoom"/);
});

test('at the whole period only Zoom in is on, and the trail has one current step', () => {
  const markup = html(topModel, { open: true });
  assert.match(markup, /<details[^>]*\bopen\b/);
  assert.equal(isDisabled(markup, zoomCopy.zoomIn), false);
  for (const label of [zoomCopy.back, zoomCopy.earlier, zoomCopy.later, zoomCopy.zoomOut, zoomCopy.reset]) assert.equal(isDisabled(markup, label), true, label + ' should be off');
  assert.equal([...markup.matchAll(/aria-current="step"/g)].length, 1);
  assert.match(markup, /<span class="crumb-current[^"]*" aria-current="step">7 days<\/span>/);
  assert.equal([...markup.matchAll(/class="crumb-link[ "]/g)].length, 0);
  assert.match(markup, /Whole period/);
  assert.match(markup, /Interval: 2 hours \(automatic\)\./);
});

test('when zoomed, the trail links back up and the movement buttons turn on', () => {
  const markup = html(hourModel, { open: true });
  assert.equal([...markup.matchAll(/class="crumb-link[ "]/g)].length, 2, 'two earlier steps are links');
  assert.match(markup, /<span class="crumb-current[^"]*" aria-current="step">24 Oct 14:00<\/span>/);
  const labels = [...markup.matchAll(/class="crumb-link[^"]*"[^>]*>([^<]*)<\/button>/g)].map((match) => match[1]);
  assert.deepEqual(labels, ['7 days', '24 Oct']);
  for (const label of [zoomCopy.back, zoomCopy.earlier, zoomCopy.later, zoomCopy.zoomIn, zoomCopy.zoomOut, zoomCopy.reset]) assert.equal(isDisabled(markup, label), false, label + ' should be on');
  assert.match(markup, /Zoom: 24 Oct 14:00/);
  assert.match(markup, /Hour/);
  assert.match(markup, /Interval: 1 minute \(automatic\)\./);
});

test('the day level reads as Day and keeps its own breadcrumb', () => {
  const markup = html(dayModel, { open: true });
  assert.equal(dayModel.level, 'day');
  assert.match(markup, /<strong class="level[^"]*">Day<\/strong>/);
  assert.equal([...markup.matchAll(/class="crumb-link[ "]/g)].length, 1);
});

test('at the smallest window Zoom in is off but Zoom out is on', () => {
  let stack = hourStack;
  stack = z.zoomIntoSlot(stack, z.slotsOf(z.currentOf(stack), 60)[7], zone);
  const model = z.stripModel({ ...baseInput, stack, bucket: z.bucketForView(z.currentOf(stack)) });
  assert.equal(model.level, 'minute');
  const markup = html(model, { open: true });
  assert.equal(isDisabled(markup, zoomCopy.zoomIn), true);
  assert.equal(isDisabled(markup, zoomCopy.zoomOut), false);
  assert.equal(isDisabled(markup, zoomCopy.back), false);
});

test('a window with no data, a read in progress and a failed read each say so in words', () => {
  const none = html(z.stripModel({ ...baseInput, stack: dayStack, bucket: z.bucketForView(z.currentOf(dayStack)), hasData: false }), { open: true });
  assert.ok(none.includes(zoomCopy.noData));
  const busy = html(z.stripModel({ ...baseInput, stack: dayStack, bucket: z.bucketForView(z.currentOf(dayStack)), loading: true }), { open: true });
  assert.ok(busy.includes(zoomCopy.loading));
  assert.doesNotMatch(busy, /As of /, 'no "as of" for a read that is still running');
  const failed = html(z.stripModel({ ...baseInput, stack: dayStack, bucket: z.bucketForView(z.currentOf(dayStack)), error: 'The server did not answer.' }), { open: true });
  assert.match(failed, /data-tone="error"/);
  assert.match(failed, /could not be read\. The server did not answer\./);
  const fine = html(dayModel, { open: true });
  assert.doesNotMatch(fine, /class="zoom-message[ "]/);
  assert.match(fine, /As of 14:03:22 UTC\./);
});

test('the overview strip draws one bar per slot, marks the zoomed part, or says the period is empty', () => {
  const markup = html(dayModel, { open: true });
  assert.equal([...markup.matchAll(/<rect class="bar[ "]/g)].length, baseInputBars());
  assert.match(markup, /<rect class="mark[ "]/);
  assert.match(markup, /role="img"/);
  const empty = html(z.stripModel({ ...baseInput, overviewCounts: [], stack: dayStack, bucket: z.bucketForView(z.currentOf(dayStack)) }), { open: true });
  assert.ok(empty.includes(zoomCopy.overviewEmpty));
  assert.doesNotMatch(empty, /<rect class="bar[ "]/);
});
function baseInputBars() { return baseInput.overviewCounts.length; }

test('the live region carries the range and any announcement', () => {
  const markup = html(hourModel, { announcement: 'Part 3 of 60: 14:02 to 14:03 UTC.' });
  assert.match(markup, /role="status"[^>]*>[^<]*Showing 24 Oct 14:00 to 15:00 UTC\.[^<]*Part 3 of 60/);
});

test('the strip file imports nothing but its types, the copy and Svelte', () => {
  const imports = [...source.matchAll(/^\s*import .* from '([^']+)'/gm)].map((match) => match[1]);
  assert.deepEqual(imports.sort(), ['../lib/copy/zoomCopy', '../lib/zoom']);
  assert.ok(DAY > 0);
});
