const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// Window control bar (P-26): .svelte files are tested as source text.

const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'src/components/CallWindowBar.svelte'), 'utf8');
const scriptEnd = source.indexOf('</script>');
const styleStart = source.indexOf('<style>');
const script = source.slice(0, scriptEnd);
const markup = source.slice(scriptEnd + '</script>'.length, styleStart === -1 ? undefined : styleStart);

test('every test id from the packet is in the markup', () => {
  const ids = [
    'scall-window', 'scall-preset-{m}', 'scall-custom', 'scall-before', 'scall-after', 'scall-apply',
    'scall-range', 'scall-earlier', 'scall-later', 'scall-tocall', 'scall-window-line', 'scall-outside',
    'scall-src-{s.key}'
  ];
  for (const id of ids) assert.ok(markup.includes(`data-testid="${id}"`), `missing data-testid ${id}`);
  // the three source ids come from api / iis / db keys
  for (const key of ['api', 'iis', 'db']) assert.match(script, new RegExp(`key: '${key}'`));
});

test('group role, label, aria-live, alert, fieldset and legend', () => {
  assert.match(markup, /role="group" aria-label=\{DRILL_TEXT\.windowLabel\}/);
  assert.match(markup, /aria-live="polite"/);
  assert.match(markup, /role="alert"/);
  assert.match(markup, /<fieldset[^>]*>\s*<legend>\{DRILL_TEXT\.sourcesLabel\}<\/legend>/);
  assert.match(markup, /aria-pressed=\{presetOf\(win\) === m\}/);
  assert.match(markup, /aria-expanded=\{customOpen\}/);
  assert.match(markup, /\{#if win\.shiftMs !== 0\}/);
});

test('preset click commits a symmetric, unmoved window', () => {
  assert.match(markup, /onCommit\(\{ before: m, after: m, shiftMs: 0 \}\)/);
  assert.match(markup, /WINDOW_PRESETS/);
});

test('logic comes from the model: clampMinutes, canStep, stepWindow, backToCall', () => {
  assert.ok(script.includes('clampMinutes('));
  assert.ok(markup.includes('canStep('));
  assert.ok(markup.includes('stepWindow('));
  assert.ok(markup.includes('backToCall('));
  assert.match(script, /from '\.\.\/lib\/drill\/callWindow'/);
  assert.match(markup, /min=\{WINDOW_MIN\} max=\{WINDOW_MAX\} step="1"/);
});

test('apply does not commit when either box is out of range', () => {
  const apply = script.slice(script.indexOf('function apply'), script.indexOf('function setSource'));
  assert.match(apply, /before === null \|\| after === null/);
  assert.ok(apply.indexOf('return;') < apply.indexOf('onCommit('));
  assert.match(markup, /DRILL_TEXT\.windowRange/);
});

test('the three window-line variants and the outside note use the copy', () => {
  for (const key of ['windowWhole', 'windowLineMoved', 'windowLine', 'legendOutside', 'laneApi', 'laneIis', 'laneDb', 'earlier', 'later', 'backToCall']) {
    assert.ok(source.includes(`DRILL_TEXT.${key}`), `missing DRILL_TEXT.${key}`);
  }
  assert.match(script, /coversOverview\(callStartMs, win\)/);
  assert.match(script, /callInside\(callStartMs, callEndMs, win\)/);
  assert.match(script, /formatTime\(ms, \{ zone, date: false, suffix: false, precision: 'minute' \}\)/);
});

test('at least one source stays on: the last checked box is disabled', () => {
  assert.match(markup, /disabled=\{shown\[s\.key\] && onCount <= 1\}/);
});

test('no number literal 120 or 15 in the markup, no English sentence literal', () => {
  assert.doesNotMatch(markup, /\b(120|15)\b/);
  // drop tags and {expressions}; what is left is literal text
  const literal = markup.replace(/\{[^{}]*(\{[^{}]*\}[^{}]*)*\}/g, ' ').replace(/<[^>]*>/g, ' ');
  assert.doesNotMatch(literal, /[A-Za-z]{2,}/, 'literal words found in markup: ' + literal.trim().slice(0, 80));
  // no string literals with a space in the script (no sentences)
  assert.doesNotMatch(script, /'[A-Za-z]+ [A-Za-z ]+'/);
});

test('copy keys used exist in DRILL_TEXT', () => {
  const copy = fs.readFileSync(path.join(root, 'src/lib/drill/drillCopy.ts'), 'utf8');
  const used = new Set([...source.matchAll(/DRILL_TEXT\.(\w+)/g)].map((m) => m[1]));
  for (const key of used) assert.match(copy, new RegExp(`\\b${key}\\s*:`), `DRILL_TEXT.${key} missing`);
});
