const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// W-shell: the drill pop-up shell. Reads the .svelte source as text, as tests/tracingPaper.test.cjs does.
const src = fs.readFileSync(path.resolve(__dirname, '..', 'src/components/DrillPopup.svelte'), 'utf8');

test('dialog attributes and test id', () => {
  for (const needle of ['role="dialog"', 'aria-modal="true"', 'aria-labelledby="drillTitle"', 'tabindex="-1"', 'data-testid="drill-popup"', 'id="drillTitle"']) {
    assert.ok(src.includes(needle), 'missing ' + needle);
  }
});

test('Overview tab shows DrillOverview, help titles, and retry refocuses the dialog', () => {
  assert.match(src, /import DrillOverview from '\.\/DrillOverview\.svelte'/);
  assert.ok(src.includes('<DrillOverview {record} />'));
  assert.ok(src.includes('title={DRILL_HELP.openAnalysis}'));
  assert.ok(src.includes('title={DRILL_HELP.title}'));
  assert.match(src, /popupEl\?\.focus\([^)]*\);\s*void load\(\)/);
});

test('tablist and one Overview tab from an array', () => {
  assert.match(src, /role="tablist" aria-label=\{DRILL_TEXT\.tabsLabel\}/);
  assert.match(src, /const tabs = \[/);
  assert.ok(src.includes('DRILL_TEXT.tabOverview'));
});

test('open flag, abort, focus restore, Esc', () => {
  assert.ok(src.includes('setDrillOpen(true)'));
  assert.ok(src.includes('setDrillOpen(false)'));
  assert.ok(src.includes('new AbortController()'));
  assert.ok(src.includes('signal.aborted'));
  assert.ok(src.includes('document.activeElement'));
  assert.match(src, /Escape/);
});

test('load guards: empty source list, not found, failure with retry, phone layout', () => {
  assert.match(src, /ids\.length === 0/);
  for (const key of ['notFound', 'loadFailed', 'retry', 'openAnalysis']) {
    assert.ok(src.includes('DRILL_TEXT.' + key), 'missing DRILL_TEXT.' + key);
  }
  assert.ok(src.includes('parsePerf('));
  assert.match(src, /max-width: 720px/);
});

test('no storage and no guided or callTrace import', () => {
  assert.doesNotMatch(src, /localStorage|sessionStorage/);
  assert.doesNotMatch(src, /from\s+['"][^'"]*(guided|callTrace)/i);
});
