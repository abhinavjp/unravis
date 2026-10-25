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
const brushLib = loadTypeScript('src/lib/zoomBrush.ts');
const { zoomCopy, allZoomStrings } = loadTypeScript('src/lib/copy/zoomCopy.ts');
const { help } = loadTypeScript('src/lib/copy/helpCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { compile } = require('svelte/compiler');

const componentPath = path.join(root, 'src/components/ZoomStrip.svelte');
const source = fs.readFileSync(componentPath, 'utf8');
const appSource = fs.readFileSync(path.join(root, 'src/App.svelte'), 'utf8');

// Render the real components on the server side of Svelte, in memory, so the text and the buttons they draw can be checked.
function loadComponent(file, resolve) {
  const full = path.join(root, file);
  const compiled = compile(fs.readFileSync(full, 'utf8'), { filename: path.basename(file), generate: 'server' });
  const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(full);
  new Function('require', 'module', 'exports', js)((request) => resolve(request) ?? nodeRequire(request), module, module.exports);
  return module.exports.default;
}
const HelpNote = loadComponent('src/components/HelpNote.svelte', (request) => ({
  '../lib/copy/helpCopy': { help }, '../lib/eli10.svelte': { eli10: { on: false } }
})[request]);
const Strip = loadComponent('src/components/ZoomStrip.svelte', (request) => ({
  '../lib/zoom': z, '../lib/zoomBrush': brushLib, '../lib/copy/zoomCopy': { zoomCopy }, './HelpNote.svelte': { default: HelpNote }
})[request]);
const { render } = require('svelte/server');
const noop = () => {};
const html = (model, extra = {}) => render(Strip, { props: { model, investigate: { enabled: true, reason: '' }, backLabel: null, onBack: noop, onEarlier: noop, onLater: noop, onZoomIn: noop, onZoomOut: noop, onReset: noop, onJump: noop, onBrush: noop, onInvestigate: noop, onWindowBack: noop, ...extra } }).body;

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
// `aria-disabled` is not the `disabled` attribute: the second one takes the button out of the tab order.
const isDisabled = (markup, label) => /(^|\s)disabled(=|\s|$)/.test(buttonOf(markup, label));
const attr = (text, name) => (text.match(new RegExp(' ' + name + '="([^"]*)"')) || [])[1];
const sliders = (markup) => [...markup.matchAll(/<div class="nav-handle[^"]*"([^>]*)>/g)].map((match) => match[1]);
const styleOf = () => source.slice(source.indexOf('<style>'));

test('ZoomStrip.svelte compiles without warnings and keeps its text out of the markup', () => {
  const result = compile(source, { filename: 'ZoomStrip.svelte', generate: 'client' });
  assert.deepEqual(result.warnings.map((x) => x.code), []);
  const markup = source.slice(source.indexOf('</script>'), source.indexOf('<style>'));
  const literal = markup.replace(/\{[^}]*\}/g, '').replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
  assert.equal(literal, '', 'static text in markup: ' + literal);
});

test('the strip is always open: no details, no summary, and the hint and every action are on screen', () => {
  const markup = html(topModel);
  assert.doesNotMatch(markup, /<details|<summary/);
  assert.doesNotMatch(source, /<details|<summary|bind:open|\bopen\b/);
  assert.ok(markup.includes(zoomCopy.hint));
  assert.match(markup, /aria-label="Zoom"/);
  for (const label of [zoomCopy.back, zoomCopy.earlier, zoomCopy.later, zoomCopy.zoomIn, zoomCopy.zoomOut, zoomCopy.reset]) buttonOf(markup, label);
});

test('at the whole period only Zoom in is on, and the trail has one current step', () => {
  const markup = html(topModel);
  assert.equal(isDisabled(markup, zoomCopy.zoomIn), false);
  for (const label of [zoomCopy.back, zoomCopy.earlier, zoomCopy.later, zoomCopy.zoomOut, zoomCopy.reset]) assert.equal(isDisabled(markup, label), true, label + ' should be off');
  assert.equal([...markup.matchAll(/aria-current="step"/g)].length, 1);
  assert.match(markup, /<span class="crumb-current[^"]*" aria-current="step">7 days<\/span>/);
  assert.equal([...markup.matchAll(/class="crumb-link[ "]/g)].length, 0);
  assert.match(markup, /Whole period/);
  assert.match(markup, /Interval: 2 hours \(automatic\)\./);
});

test('when zoomed, the trail links back up and the movement buttons turn on', () => {
  const markup = html(hourModel);
  assert.equal([...markup.matchAll(/class="crumb-link[ "]/g)].length, 2, 'two earlier steps are links');
  assert.match(markup, /<span class="crumb-current[^"]*" aria-current="step">24 Oct 14:00<\/span>/);
  const labels = [...markup.matchAll(/class="crumb-link[^"]*"[^>]*>([^<]*)<\/button>/g)].map((match) => match[1]);
  assert.deepEqual(labels, ['7 days', '24 Oct']);
  for (const label of [zoomCopy.back, zoomCopy.earlier, zoomCopy.later, zoomCopy.zoomIn, zoomCopy.zoomOut, zoomCopy.reset]) assert.equal(isDisabled(markup, label), false, label + ' should be on');
  assert.match(markup, /Hour/);
  assert.match(markup, /Interval: 30 seconds \(automatic\)\./);
});

test('the day level reads as Day and keeps its own breadcrumb', () => {
  const markup = html(dayModel);
  assert.equal(dayModel.level, 'day');
  assert.match(markup, /<strong class="level[^"]*">Day<\/strong>/);
  assert.equal([...markup.matchAll(/class="crumb-link[ "]/g)].length, 1);
});

test('at the smallest window Zoom in is off but Zoom out is on', () => {
  let stack = hourStack;
  stack = z.zoomIntoSlot(stack, z.slotsOf(z.currentOf(stack), 60)[7], zone);
  const model = z.stripModel({ ...baseInput, stack, bucket: z.bucketForView(z.currentOf(stack)) });
  assert.equal(model.level, 'minute');
  const markup = html(model);
  assert.equal(isDisabled(markup, zoomCopy.zoomIn), true);
  assert.equal(isDisabled(markup, zoomCopy.zoomOut), false);
  assert.equal(isDisabled(markup, zoomCopy.back), false);
});

test('a window with no data, a read in progress and a failed read each say so in words', () => {
  const none = html(z.stripModel({ ...baseInput, stack: dayStack, bucket: z.bucketForView(z.currentOf(dayStack)), hasData: false }));
  assert.ok(none.includes(zoomCopy.noData));
  const busy = html(z.stripModel({ ...baseInput, stack: dayStack, bucket: z.bucketForView(z.currentOf(dayStack)), loading: true }));
  assert.ok(busy.includes(zoomCopy.loading));
  assert.doesNotMatch(busy, /As of /, 'no "as of" for a read that is still running');
  const failed = html(z.stripModel({ ...baseInput, stack: dayStack, bucket: z.bucketForView(z.currentOf(dayStack)), error: 'The server did not answer.' }));
  assert.match(failed, /data-tone="error"/);
  assert.match(failed, /could not be read\. The server did not answer\./);
  const fine = html(dayModel);
  assert.doesNotMatch(fine, /class="zoom-message[ "]/);
  assert.match(fine, /As of 14:03:22 UTC\./);
});

test('the navigator draws one bar per slot with the zoomed part as a body, or says the period is empty', () => {
  const markup = html(dayModel);
  assert.equal([...markup.matchAll(/<rect class="bar[ "]/g)].length, baseInput.overviewCounts.length);
  assert.doesNotMatch(markup, /<rect class="mark/, 'the zoomed part is a div body now');
  assert.match(markup, /<svg class="overview[^"]*"[^>]*aria-hidden="true"/, 'the bars are decoration under the group');
  assert.doesNotMatch(markup, /overview-empty/);
  assert.match(markup, /<div class="nav-body[^"]*" style="left: 28\.57\d*%; width: 14\.28\d*%;"/);
});

test('the brush is two sliders in a labelled group, with spoken values', () => {
  const markup = html(dayModel);
  const found = sliders(markup);
  assert.equal(found.length, 2);
  assert.equal([...markup.matchAll(/role="slider"/g)].length, 2);
  const { brush, overview } = dayModel;
  assert.ok(overview.alt);
  assert.match(markup, /<div class="nav-track[^"]*" role="group" aria-label="[^"]+"/);
  assert.equal(attr(found[0], 'aria-label'), zoomCopy.brushStart);
  assert.equal(attr(found[1], 'aria-label'), zoomCopy.brushEnd);
  for (const one of found) assert.equal(attr(one, 'tabindex'), '0');
  // Each handle may go as far as one floor from the other (changed: both used to span the whole root).
  assert.equal(attr(found[0], 'aria-valuemin'), String(brush.rootStart));
  assert.equal(attr(found[0], 'aria-valuemax'), String(brush.viewEnd - z.MIN_VIEW_MS));
  assert.equal(attr(found[1], 'aria-valuemin'), String(brush.viewStart + z.MIN_VIEW_MS));
  assert.equal(attr(found[1], 'aria-valuemax'), String(brush.rootEnd));
  assert.equal(attr(found[0], 'aria-valuenow'), String(brush.viewStart));
  assert.equal(attr(found[1], 'aria-valuenow'), String(brush.viewEnd));
  assert.equal(attr(found[0], 'aria-valuetext'), brush.startText);
  assert.equal(attr(found[1], 'aria-valuetext'), brush.endText);
});

test('the brush still draws with no data, and says so', () => {
  const empty = html(z.stripModel({ ...baseInput, overviewCounts: [], stack: dayStack, bucket: z.bucketForView(z.currentOf(dayStack)) }));
  assert.equal(sliders(empty).length, 2);
  assert.ok(empty.includes(zoomCopy.overviewEmpty));
  assert.doesNotMatch(empty, /<rect class="bar[ "]/);
});

test('the navigator has an always-visible plain line, with Earlier and Later on its two ends', () => {
  const markup = html(topModel);
  assert.ok(markup.includes(help['drill-navigator'].plain));
  assert.match(source, /<HelpNote id="drill-navigator" \/>/);
  assert.match(buttonOf(markup, zoomCopy.earlier), /nav-earlier/);
  assert.match(buttonOf(markup, zoomCopy.later), /nav-later/);
  const navigator = markup.indexOf('class="navigator');
  assert.ok(markup.indexOf('class="zoom-buttons') < navigator);
  assert.ok(navigator < markup.indexOf('nav-earlier'));
  assert.ok(markup.indexOf('nav-earlier') < markup.indexOf('class="nav-track'));
  assert.ok(markup.indexOf('class="nav-track') < markup.indexOf('nav-later'));
});

test('Investigate is hidden at the whole period and shown once drilled', () => {
  const top = html(topModel);
  assert.doesNotMatch(top, /investigate-chip/);
  assert.ok(!top.includes('>' + zoomCopy.investigate + '<'));
  const drilled = html(dayModel);
  assert.match(drilled, /investigate-chip/);
  assert.ok(drilled.includes('>' + zoomCopy.investigate + '<'));
});

test('Investigate is a secondary button, and says in words why it is unavailable', () => {
  const on = buttonOf(html(dayModel), zoomCopy.investigate);
  assert.match(on, /class="secondary-button investigate[ "]/);
  assert.match(on, /aria-disabled="false"/);
  assert.doesNotMatch(on, /aria-describedby/);
  const markup = html(dayModel, { investigate: { enabled: false, reason: zoomCopy.investigateBusy } });
  const off = buttonOf(markup, zoomCopy.investigate);
  assert.match(off, /aria-disabled="true"/);
  assert.doesNotMatch(off.replace('aria-disabled', ''), /\bdisabled\b/, 'it stays focusable: no disabled attribute');
  assert.doesNotMatch(off, /primary-button/);
  assert.match(off, /title="[^"]+"/);
  const id = attr(off, 'aria-describedby');
  assert.ok(id);
  assert.ok(new RegExp('<span class="chip-reason[^"]*" id="' + id + '">' + zoomCopy.investigateBusy + '</span>').test(markup));
  assert.match(source, /if \(investigate\.enabled\) \{ onInvestigate\(\); return; \}[\s\S]*spoken = investigate\.reason/, 'pressing it while unavailable only speaks the reason');
  assert.doesNotMatch(source, /primary-button/);
});

test('"Back to ..." shows only when it is given, and its press goes to onWindowBack', () => {
  const given = html(dayModel, { backLabel: 'Back to 7 days' });
  assert.ok(given.includes('>Back to 7 days</button>'));
  assert.match(source, /onclick=\{onWindowBack\}/);
  assert.doesNotMatch(html(dayModel, { backLabel: null }), /window-back/);
  assert.doesNotMatch(html(dayModel), /Back to /);
});

test('every button calls its handler (source)', () => {
  assert.match(source, /onclick=\{investigateNow\}/);
  assert.match(source, /onclick=\{onBack\}/);
  assert.match(source, /onclick=\{onEarlier\}/);
  assert.match(source, /onclick=\{onLater\}/);
  assert.match(source, /onclick=\{onZoomIn\}/);
  assert.match(source, /onclick=\{onZoomOut\}/);
  assert.match(source, /onclick=\{onReset\}/);
  assert.match(source, /investigate\.enabled\) \{ onInvestigate\(\)/, 'the chip calls investigate when it is on');
});

test('the strip puts the window back on pointercancel and on Escape, and reports nothing', () => {
  assert.match(source, /onpointercancel=\{cancel\}/);
  assert.match(source, /function cancel\(event: PointerEvent\) \{ if \(drag && event\.pointerId === drag\.id\) revert\(\); \}/);
  const revert = source.match(/function revert\(\) \{([^}]*)\}/)[1];
  assert.match(revert, /preview = null/);
  assert.doesNotMatch(revert, /onBrush|emit/);
  const esc = source.match(/function escape\(event: KeyboardEvent\) \{([\s\S]*?)\n  \}/)[1];
  assert.match(esc, /event\.key !== 'Escape'/);
  assert.match(esc, /revert\(\)/);
  assert.match(esc, /event\.preventDefault\(\)/);
  assert.match(esc, /event\.stopPropagation\(\)/);
  assert.doesNotMatch(esc, /onBrush|emit/);
  assert.match(source, /<svelte:window onkeydowncapture=\{escape\} \/>/, 'caught before the page-wide Escape handler steps out');
});

test('the brush reports one window per gesture', () => {
  assert.equal([...source.matchAll(/\bonBrush\(/g)].length, 1, 'one place reports a window');
  assert.match(source, /function emit\(win: ZoomWindow\) \{ if \(!sameWindow\(win, view\)\) onBrush\(win\); \}/);
  assert.doesNotMatch(source.match(/function move\([\s\S]*?\n  \}/)[0], /emit\(|onBrush/, 'moving only previews');
  assert.match(source.match(/function up\([\s\S]*?\n  \}/)[0], /emit\(next\)/);
  assert.match(source, /const KEY_COMMIT_MS = 300;/);
  assert.match(source, /timer = setTimeout\(flush, KEY_COMMIT_MS\)/);
  assert.match(source, /function flush\(\) \{[\s\S]*?emit\(win\)/);
  assert.equal([...source.matchAll(/onblur=\{flush\}/g)].length, 2, 'both handles commit on blur');
  assert.equal([...source.matchAll(/\bemit\(/g)].length, 3, 'the declaration, pointer up and the debounced or blur flush');
});

test('keys on a handle go through brushKey with a 300 ms debounce and leave other keys alone', () => {
  assert.match(source, /onkeydown=\{\(event\) => keydown\('start', event\)\}/);
  assert.match(source, /onkeydown=\{\(event\) => keydown\('end', event\)\}/);
  const fn = source.match(/function keydown\([\s\S]*?\n  \}/)[0];
  assert.match(fn, /brushKey\(handle, event\.key, root, shown, brush\.stepMs\)/);
  assert.match(fn, /if \(!next\) return;\s*\n\s*event\.preventDefault\(\)/, 'only keys the slider handles are stopped');
  assert.match(fn, /event\.altKey \|\| event\.ctrlKey \|\| event\.metaKey/, 'browser shortcuts pass through');
  // The key meanings (arrows, PageUp and PageDown, Home and End) are pinned in the brush tests; here they are checked on the strip's own inputs.
  const rootWindow = { start: dayModel.brush.rootStart, end: dayModel.brush.rootEnd };
  const view = { start: dayModel.brush.viewStart, end: dayModel.brush.viewEnd };
  const later = brushLib.brushKey('start', 'PageUp', rootWindow, view, dayModel.brush.stepMs);
  assert.equal(later.start, view.end);
  assert.equal(brushLib.brushKey('end', 'Tab', rootWindow, view, dayModel.brush.stepMs), null);
});

test('coarse pointers get wider handles and the page can still scroll up and down over the strip', () => {
  const style = styleOf();
  const coarse = style.match(/@media \(pointer: coarse\) \{([\s\S]*?)\n  \}/);
  assert.ok(coarse, 'a coarse-pointer block');
  const width = coarse[1].match(/\.nav-handle \{ width: (\d+)px/);
  assert.ok(width && Number(width[1]) >= 24, 'a 24px hit area');
  assert.match(style, /touch-action: pan-y/);
});

test('no transition or animation, no colour literal, radius and accent only from tokens', () => {
  const style = styleOf();
  assert.doesNotMatch(style, /transition|animation|@keyframes/);
  assert.doesNotMatch(style, /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/);
  assert.doesNotMatch(style, /border-radius: \d/);
  assert.doesNotMatch(style, /summary/);
  assert.doesNotMatch(style, /background: var\(--accent\)/, 'no solid accent fill outside primary buttons');
});

test('every strip string passes the copy lint, and the removed copy and store state are gone', () => {
  const entries = allZoomStrings().map((text, index) => ({ id: 'zoom' + index, text }));
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  assert.equal('expand' in zoomCopy, false);
  const copySource = fs.readFileSync(path.join(root, 'src/lib/copy/zoomCopy.ts'), 'utf8');
  assert.doesNotMatch(copySource, /collapsed|expand:/);
  const store = fs.readFileSync(path.join(root, 'src/lib/zoomView.svelte.ts'), 'utf8');
  assert.doesNotMatch(store, /let open\b|get open\(|set open\(|open = open/);
  for (const id of ['drill-navigator', 'drill-investigate', 'drill-back']) assert.ok(help[id] && help[id].plain && help[id].simple, id);
});

test('the live region carries the range, the preview and a spoken reason', () => {
  assert.match(source, /role="status" aria-live="polite">\{model\.rangeLine\} \{model\.message\} \{model\.notice\} \{announcement\} \{previewText\} \{spoken\}/);
  assert.match(html(hourModel, { announcement: 'Part 3 of 60: 14:02 to 14:03 UTC.' }), /role="status"[^>]*>[^<]*Showing 24 Oct 14:00 to 15:00 UTC\.[^<]*Part 3 of 60/);
});

test('App wires the strip: investigate, back to the previous window, the brush, and no bind:open', () => {
  const tag = appSource.match(/<ZoomStrip[\s\S]*?\/>/)[0];
  assert.match(tag, /onInvestigate=\{investigateWindow\}/);
  assert.match(tag, /onWindowBack=\{backToPreviousWindow\}/);
  assert.match(tag, /onBrush=\{win => zoomView\.setWindow\(win\)\}/);
  assert.match(tag, /\{investigate\}/);
  assert.match(tag, /backLabel=\{windowBackLabel\}/);
  assert.doesNotMatch(tag, /bind:open/);
  assert.match(appSource, /investigateState\(\{ isRoot: zoomModel\?\.isRoot \?\? true, runLoading: loading, stale: results\.displayedRunIsStale \}\)/);
  assert.match(appSource, /results\.windowBack \? backLabel\(results\.windowBack\) : null/);
  assert.doesNotMatch(appSource, /zoomView\.open/);
});

test('the strip file imports nothing but the zoom libraries, the copy, the help note and Svelte', () => {
  const imports = [...source.matchAll(/^\s*import .* from '([^']+)'/gm)].map((match) => match[1]);
  assert.deepEqual(imports.sort(), ['../lib/copy/zoomCopy', '../lib/zoom', '../lib/zoomBrush', './HelpNote.svelte']);
  assert.ok(DAY > 0);
});

test('the slider readouts are built from the window on show (preview or committed), with the model formatter and zone', () => {
  assert.match(source, /aria-valuetext=\{startText\}/);
  assert.match(source, /aria-valuetext=\{endText\}/);
  assert.doesNotMatch(source, /brush\.startText|brush\.endText/, 'the committed text is not used for the readout');
  assert.match(source, /startText = \$derived\(brushEdgeText\(shown\.start, shown, zone\)\)/);
  assert.match(source, /endText = \$derived\(brushEdgeText\(shown\.end, shown, zone\)\)/);
  // Same words as the model gives the committed window, in the zone it uses, and to the second under ten minutes.
  const marks = { start: Date.parse('2001-10-24T14:03:22Z'), end: Date.parse('2001-10-24T14:08:22Z') };
  assert.match(z.brushEdgeText(marks.start, marks, 'UTC'), /14:03:22/);
  const wide = { start: marks.start, end: marks.start + 2 * 3_600_000 };
  assert.doesNotMatch(z.brushEdgeText(wide.start, wide, 'UTC'), /14:03:22/);
  const zoomed = z.stripModel({ ...baseInput, stack: hourStack, bucket: z.bucketForView(z.currentOf(hourStack)) });
  const hv = z.currentOf(hourStack);
  assert.equal(zoomed.brush.startText, z.brushEdgeText(hv.start, hv, zone));
  assert.equal(zoomed.brush.endText, z.brushEdgeText(hv.end, hv, zone));
});

test('a pointer move does not read layout: the track box is taken once, when the drag begins', () => {
  const move = source.slice(source.indexOf('function move('), source.indexOf('function up('));
  assert.doesNotMatch(move, /getBoundingClientRect|offsetWidth|getComputedStyle/);
  assert.match(move, /drag\.rect/);
  const down = source.slice(source.indexOf('function down('), source.indexOf('function move('));
  assert.equal((down.match(/getBoundingClientRect/g) || []).length, 1);
  assert.match(down, /rect \}/);
});
