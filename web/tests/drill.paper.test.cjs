const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// Source-text checks for the Slow call screen mount in TracingPaper.svelte, in the style of tests/drill.popup.test.cjs.
const src = fs.readFileSync(path.resolve(__dirname, '..', 'src/components/TracingPaper.svelte'), 'utf8');

test('SlowCallScreen is loaded lazily, never imported statically; the old pop-up is gone', () => {
  assert.ok(src.includes("const SlowCallScreen = lazyComponent(() => import('./SlowCallScreen.svelte'));"));
  assert.doesNotMatch(src, /^\s*import\s+SlowCallScreen\b/m);
  assert.doesNotMatch(src, /<DrillPopup/);
  assert.doesNotMatch(src, /DrillPopup/);
});

test('the section hides while the screen is open for this case, and [hidden] really hides it', () => {
  assert.ok(src.includes('<section class="tracing-paper" aria-labelledby="tp-title" hidden={callShown}>'));
  assert.ok(src.includes('const callShown = $derived(!!app.slowCall && app.slowCall.caseId === caseId);'));
  assert.ok(src.includes('.tracing-paper[hidden] { display: none; }'));
});

test('a diamond opens the screen through openCall, placed after onBarSelect', () => {
  assert.ok(src.includes('onDiamond={openCall}'));
  assert.ok(src.indexOf('function openCall') > src.indexOf('function onBarSelect'));
  assert.ok(src.includes("makeCallTarget(caseId, result.window, slot, result.lanes.find((l) => l.key === 'audit'), result.sources)"));
  assert.ok(src.includes('openSlowCall(target)'));
  assert.doesNotMatch(src, /app\.openSlowCall/);
  assert.match(src, /import \{[^}]*\bopenSlowCall\b[^}]*\bcloseSlowCall\b[^}]*\bnoteSlowCall\b[^}]*\} from '\.\.\/lib\/appState\.svelte'/);
  assert.match(src, /import \{ makeCallTarget \} from '\.\.\/lib\/drill\/callTarget';/);
});

test('the screen is a second root node after the section, keyed by slot', () => {
  const after = src.replace(/\r\n/g, '\n').indexOf('</section>\n\n{#if callShown && app.slowCall}');
  assert.ok(after > 0, 'the screen block follows the closing section tag');
  assert.ok(src.replace(/\r\n/g, '\n').lastIndexOf('</section>') === after);
  assert.ok(src.includes('{#key app.slowCall.slot}'));
  assert.ok(src.includes('<SlowCallScreen target={app.slowCall} zone={timeZone.zone} demo={!!app.info?.demo}'));
  assert.ok(src.includes('onBack={() => closeSlowCall()}'));
  assert.ok(src.includes('onFound={(call) => noteSlowCall(call)}'));
});

test('Open in Analysis hands off the own window of the call, clearing the call in place first', () => {
  assert.ok(src.includes('onOpenAnalysis={() => openAnalysisFromCall()}'));
  const fn = src.slice(src.indexOf('function openAnalysisFromCall'), src.indexOf('let returnSlot'));
  assert.ok(fn.includes('const bucket = { start: target.start, end: target.end };'));
  assert.ok(fn.indexOf('closeSlowCall({ handoff: true })') > 0);
  assert.ok(fn.indexOf('closeSlowCall({ handoff: true })') < fn.indexOf('handOffBucket(bucket'));
  assert.ok(fn.includes('open: openAnalysis'));
  assert.doesNotMatch(fn, /onBarSelect/);
  assert.ok(src.includes('onBack={() => closeSlowCall()}'));
});

test('closing the screen returns focus to the slot that was opened, or the chart region', () => {
  assert.ok(src.includes('{ returnSlot = slot; openSlowCall(target); }'));
  assert.ok(src.includes('chartFocus = { slot: returnSlot, token: ++focusToken };'));
  assert.ok(src.includes('else chartRegion?.focus();'));
  assert.ok(src.includes('if (!wasShown || shown) return;'));
  assert.ok(src.includes('void tick().then(() => {'));
});

test('a new run still clears drillSlot, and the timeline never imports guided or callTrace', () => {
  assert.match(src, /cancelled = false;\s*drillSlot = null;\s*controller\?\.abort\(\)/);
  assert.match(src, /let drillSlot = \$state<number \| null>\(null\);/);
  assert.doesNotMatch(src, /^\s*import .*from '[^']*(guided|callTrace)[^']*'/m);
});
