const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// BL-473, 474, 476, 477, 478: layout, size and colour fixes the UI audit found. Source checks only; the audit itself (tools/uiaudit) is the real proof.
const root = path.resolve(__dirname, '..');
const read = (r) => fs.readFileSync(path.join(root, r), 'utf8').replace(/\r\n/g, '\n');

test('BL-473: the step rail keeps clear of the 40px close button on desktop too', () => {
  const css = read('src/components/blackboxWizard/WizardDialog.svelte');
  const rule = css.split('\n').find((line) => /^\s*\.shell-rail \{/.test(line));
  assert.ok(rule, 'desktop .shell-rail rule exists');
  // 12px in + 40px button + 8px gap
  assert.match(rule, /padding: var\(--space-5\) calc\(var\(--space-3\) \+ 40px \+ var\(--space-2\)\) 0 var\(--space-6\)/);
});

test('BL-474: the field-help "i" starts after the "?" hint, also on touch', () => {
  const css = read('src/components/FieldHelp.svelte');
  assert.match(css, /:global\(\.hint-wrap ~ \.field-help-toggle\) \{ margin-inline-start: var\(--space-1\); \}/);
  // in the When group the hint is inside the heading and the "i" follows it, so the heading carries the gap
  const when = read('src/components/filters/WhenGroup.svelte');
  assert.match(when, /\.when-window h3 \{ margin-inline-end: var\(--space-1\); \}/);
  assert.match(when, /pointer: coarse\), \(max-width: 720px\) \{ \.when-window h3 \{ margin-inline-end: var\(--space-2\); \} \}/);
  // the rule must come after the touch/narrow rule so that it wins on equal terms too
  assert.ok(css.indexOf('.hint-wrap ~ .field-help-toggle') > css.indexOf('pointer: coarse'));
});

test('BL-476: dimmed Daily rows use the muted text colour, never opacity', () => {
  const css = read('src/components/DailyTable.svelte');
  assert.match(css, /tr\.low td, tr\.low th \{ color: var\(--text-2\); \}/);
  assert.doesNotMatch(css, /tr\.low[^{]*\{[^}]*opacity/);
});

test('BL-477: pop-up note, table first column and pager share the 12px / 6px lines', () => {
  const css = read('src/app.css');
  assert.match(css, /\.popover-foot > \.demo-note \{ margin-inline: 6px; \}/);
  assert.match(css, /\.group-pagination \{[^}]*padding: 12px;/);
  assert.match(css, /@media \(min-width: 721px\) \{ \.group-table-scroll th:first-child, \.group-table-scroll td:first-child \{ padding-left: 12px; \} \}/);
});

test('BL-478: the chart help line and the selected-lines notes are at least 11px', () => {
  assert.match(read('src/components/TracingChart.svelte'), /\.canvas-bar small \{ font-size: inherit; \}/);
  assert.match(read('src/app.css'), /\.timeline-selected small \{[^}]*font-size: 12px;/);
});
