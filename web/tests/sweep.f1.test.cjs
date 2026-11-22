const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// UI sweep, package F1: the shell, the shared CSS and the shared help components. The behaviour itself is measured in a real browser by
// tools/e2e/specs/ui-sweep-f1.spec.ts; these tests pin the rules that produce it so a later edit cannot quietly drop one.
const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
const css = read('src/app.css');
const style = (relative) => { const source = read(relative); return source.slice(source.indexOf('<style>')); };
const rule = (text, selector) => {
  const start = text.indexOf(selector + ' {');
  assert.ok(start >= 0, 'missing rule: ' + selector);
  return text.slice(start, text.indexOf('}', start) + 1);
};

test('SWEEP-F1-01 (ANALYSIS-03) the open More filters panel leaves the results room: the panel and the bar are capped', () => {
  const wide = css.slice(css.indexOf('@media (min-width: 721px) {\n  .rail-layout'));
  const body = rule(wide, '.more-filters[open] > .more-body');
  // 400px = header + bar + margins + about 250px for the results (82px before).
  assert.match(body, /max-height: max\(200px, calc\(100dvh - var\(--header-h\) - 400px\)\)/);
  assert.match(body, /overflow-y: auto/);
  assert.match(rule(wide, '.scope:has(.more-filters[open])'), /max-height: 62dvh; overflow-y: auto/, 'a second cap in case the bar itself wraps to many rows');
});

test('SWEEP-F1-02 (ANALYSIS-05) a field help opening does not stretch the field next to it, and a select and an input are one height', () => {
  assert.match(rule(css, '.field-grid'), /align-items: start/);
  assert.match(rule(css, '.control-group'), /align-content: start/);
  assert.match(css, /\.control-group input:not\(\[type="checkbox"\]\):not\(\[type="radio"\]\), \.control-group select \{ min-height: 32px; \}/);
});

test('SWEEP-F1-03 (CASES-06, ANALYSIS-05, ANALYSIS-19) the ring of every help button sits behind its glyph, and the glyph is 12px', () => {
  for (const [file, cls] of [['HelpNote', 'help-toggle'], ['FieldHelp', 'field-help-toggle']]) {
    const css2 = style('src/components/' + file + '.svelte');
    assert.match(rule(css2, '.' + cls), /isolation: isolate/, file + ' keeps its own stacking context');
    assert.match(rule(css2, '.' + cls), /font-size: 12px/, file + ' glyph above the 11px floor');
    assert.match(rule(css2, '.' + cls + '::before'), /z-index: -1/, file + ' ring behind the glyph');
  }
  assert.match(rule(style('src/components/Hint.svelte'), '.hint-button'), /font-size: 12px/);
});

test('SWEEP-F1-04 (ANALYSIS-06) each "i" names its field, and a "?" popover opens above or beside its button, not over the field below', () => {
  const field = read('src/components/FieldHelp.svelte');
  assert.match(field, /const about = \$derived\(name \? 'About ' \+ name : 'About this filter'\)/);
  assert.match(field, /label\[for="' \+ id \+ '"\]/, 'the name comes from the field label');
  assert.match(field, /role="group" aria-label=\{about\}/);
  const hint = read('src/components/Hint.svelte');
  const place = hint.slice(hint.indexOf('function place()'), hint.indexOf('// The shared bus'));
  const above = place.indexOf('anchor.top - gap - height');
  const right = place.indexOf('anchor.right + gap + width');
  const below = place.indexOf('anchor.bottom + gap');
  assert.ok(above > 0 && right > above && below > right, 'order: above, then right, then below');
});

test('SWEEP-F1-05 (CASES-23, CASES-06) a help note stays a working toggle in Explain simply, folds its block, and Esc closes it', () => {
  const src = read('src/components/HelpNote.svelte');
  assert.doesNotMatch(src.slice(0, src.indexOf('<style>')), /disabled=/);
  assert.match(src, /const shown = \$derived\(eli10\.on \? !folded : open\)/);
  assert.match(src, /function flip\(\) \{ if \(eli10\.on\) folded = !folded; else open = !open; \}/);
  assert.match(src, /event\.key !== 'Escape' \|\| !shown/);
  assert.match(src, /toggle\?\.focus\(\)/, 'focus goes back to the button');
  assert.match(rule(style('src/components/HelpNote.svelte'), '.help-simple'), /width: 100%; max-width: none/);
});

test('SWEEP-F1-06 (SETUP-A11Y-04) forced colours: the selected state, brand mark and legend are drawn with system colours', () => {
  const start = css.indexOf('@media (forced-colors: active) {');
  assert.ok(start > 0);
  const block = css.slice(start, css.indexOf('/* Tap targets'));
  for (const selector of ['.chip[aria-pressed="true"]', '.segmented button[aria-pressed="true"]', '.menu-list button[aria-pressed="true"]', '.view-toggle .secondary-button[aria-pressed="true"]', '.rail-item.on', '.tab[aria-selected="true"]', '.lens-chips .chip[aria-pressed="true"]', '.nav-button[aria-expanded="true"]']) {
    assert.ok(block.includes(selector), selector);
  }
  assert.match(block, /forced-color-adjust: none; color: HighlightText; background: Highlight/);
  assert.match(block, /\.brand-mark span \{[^}]*background: CanvasText/);
  assert.match(block, /\.hero-legend i, \.series-marker \{[^}]*border: 1px solid CanvasText/);
});

test('SWEEP-F1-07 (SETUP-A11Y-10) secondary buttons, chips, the segmented control and the summary buttons use the 3:1 control line', () => {
  assert.match(rule(css, '.secondary-button'), /border: 1px solid var\(--control-line\)/);
  assert.match(rule(css, '.chip'), /border: 1px solid var\(--control-line\)/);
  assert.match(rule(css, '.segmented'), /border: 1px solid var\(--control-line\)/);
  assert.match(rule(css, '.more-filters > summary'), /border: 1px solid var\(--control-line\)/);
  assert.match(rule(css, '.menu > summary'), /border: 1px solid var\(--control-line\)/);
  assert.match(rule(style('src/brand/components/Button.svelte'), '.secondary'), /border: 1px solid var\(--control-line\)/);
});

test('SWEEP-F1-08 (SETUP-A11Y-11) one button size: 33px on a desktop for the sources and import buttons, 44px only on touch or narrow', () => {
  const sources = read('src/components/sources/sources.css');
  assert.match(sources, /\.src \.btn, \.src \.btn-primary \{ min-height: 33px;[^}]*font-size: var\(--fs-3\)/);
  assert.match(sources, /@media \(pointer: coarse\), \(max-width: 720px\) \{ \.src \.btn, \.src \.btn-primary \{ min-height: 44px;/);
  assert.match(sources, /\.src \.btn-primary:disabled \{ opacity: 1; color: var\(--text-2\); background: var\(--surface-2\)/, 'a disabled primary is neutral, not faded');
  for (const file of ['src/lib/components/importPopup/ChooseStep.svelte', 'src/lib/components/importPopup/FixCard.svelte', 'src/lib/components/importPopup/ImportPopup.svelte', 'src/lib/components/importPopup/ImportingStep.svelte', 'src/lib/components/importPopup/ResultStep.svelte', 'src/components/SlowCallScreen.svelte']) {
    const text = style(file);
    assert.doesNotMatch(text, /\.btn \{[^}]*min-height: 36px/, file);
    assert.match(text, /\.btn \{[^}]*min-height: 33px/, file);
  }
});

test('SWEEP-F1-09 (SETUP-A11Y-20) a modal drawer keeps its title row and close button in view', () => {
  const drawer = style('src/components/Drawer.svelte');
  assert.match(rule(drawer, '.drawer-header:not(.panel)'), /position: sticky; top: -22px;[^}]*background: var\(--surface\)/);
});

test('SWEEP-F1-10 (BLACKBOX-03, ANALYSIS-14) one chevron for every details row: app.css draws it, drawers and dialogs do not switch it off', () => {
  assert.match(css, /summary::before \{ content: ""; display: inline-block;[^}]*transform: rotate\(-45deg\)/);
  assert.match(css, /details\[open\]:not\([^)]*\)\) > summary::before \{ transform: rotate\(45deg\); \}/);
  // The rows that draw their own marker are left out, so a row never shows two.
  for (const own of ['.disclosure', '.auth-options', '.more-filters', '.menu', '.more']) assert.ok(css.includes(own + ')) > summary::before') || css.includes(own + ',') || css.includes(', ' + own + ')'), own);
  const drawer = read('src/components/Drawer.svelte');
  assert.doesNotMatch(drawer, /summary::after/);
  assert.doesNotMatch(css, /summary::after \{ content: none/);
});

test('SWEEP-F1-11 (CASES-01, CASES-02) the wizard bar sits on the window edge, and is gone once the case is made', () => {
  assert.match(css, /\.stage:has\(\.wizard \.bar\) \{ padding-bottom: 0; \}/);
  const wizard = read('src/components/CreateWizard.svelte');
  assert.match(wizard, /import \{ caseMade \} from '\.\.\/lib\/wizardCreate\.svelte'/);
  assert.match(wizard, /\{#if !caseMade\(\)\}\s*<footer class="bar">[\s\S]*<\/footer>\s*\{\/if\}\s*<\/section>/);
});

test('SWEEP-F1-12 (CASES-11) hub tabs look like tabs: hairline row, 3px bar under the chosen one, system highlight in forced colours', () => {
  const sources = read('src/components/sources/sources.css');
  assert.match(sources, /\.src \.tabs button \{[^}]*border-bottom: 3px solid transparent; border-radius: 0; white-space: nowrap;/);
  assert.match(sources, /\.src \.tabs button\[aria-selected='true'\],\n\.src \.tabs button\[aria-pressed='true'\] \{[^}]*border-bottom-color: var\(--accent\); font-weight: 700/);
  assert.match(sources, /@media \(forced-colors: active\)[\s\S]*Highlight/);
});

test('SWEEP-F1-13 (ANALYSIS-19) header actions get a visible hover fill', () => {
  assert.match(rule(css, '.header-button:hover'), /background: color-mix\(in srgb, var\(--text\) 9%, var\(--surface\)\)/);
});
