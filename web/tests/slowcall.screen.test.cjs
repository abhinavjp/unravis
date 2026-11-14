const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { pathToFileURL } = require('node:url');
const { compileModule } = require('svelte/compiler');

// The Slow call screen shell, tested as source text (.svelte files are not loaded in node).
// The restart effect is also extracted and run against the real Svelte runtime, so the rule
// "a same-value target replacement must not restart the controller" is checked as behaviour.

const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'src/components/SlowCallScreen.svelte'), 'utf8');
const markup = source.replace(/<script[\s\S]*?<\/script>/, '').replace(/<style[\s\S]*?<\/style>/, '');

test('the screen root, back link and Analysis button are there', () => {
  for (const needle of ['data-testid="slow-call-screen"', 'scall-back', 'scall-analysis', 'scall-back-btn', 'scall-body', 'scall-thiscall', 'scall-finding', 'aria-labelledby="scall-title"']) {
    assert.ok(source.includes(needle), needle);
  }
});

test('the breadcrumb, overview and controller are wired', () => {
  for (const needle of ['DRILL_TEXT.crumbCall(', '<DrillOverview', 'createCallController(', 'dispose()', '.retry()', 'fetchTracing(', 'nextRequestSeq()', 'fetchCallEvents']) {
    assert.ok(source.includes(needle), needle);
  }
});

test('it is a page, not a dialog, and does not use the guided or call-trace code', () => {
  assert.ok(!source.includes('role="dialog"'));
  assert.ok(!source.includes('aria-modal'));
  assert.ok(!/guided/i.test(source));
  assert.ok(!/callTrace/.test(source));
});

test('no sentence is written in the markup', () => {
  assert.ok(!/>[A-Z][a-z]+ [a-z]+ [a-z]+</.test(markup));
});

test('no new colour literals', () => {
  const style = (source.match(/<style[\s\S]*?<\/style>/) || [''])[0];
  assert.ok(!/#[0-9a-fA-F]{3,8}\b/.test(style));
  assert.ok(!/\brgba?\(/.test(style));
});

test('layout W1: all six children are imported and used once, in the work order DOM order', () => {
  for (const name of ['CallWindowBar', 'CallLanes', 'CallMinimap', 'CallNearby', 'CallAllSources', 'CallInspector']) {
    assert.equal((source.match(new RegExp(`import ${name} from`, 'g')) || []).length, 1, `${name} import`);
    assert.equal((markup.match(new RegExp(`<${name}[\\s/>]`, 'g')) || []).length, 1, `${name} used`);
  }
  assert.ok(markup.includes('data-layout="w1"'));
  const order = ['scall-grid', 'scall-main', '<CallWindowBar', 'data-testid="scall-pin"', '<CallLanes', '<CallMinimap', 'data-testid="scall-thiscall"', 'data-testid="scall-tabs"', '<CallNearby', '<CallAllSources', '<CallInspector'].map((n) => markup.indexOf(n));
  assert.ok(order.every((at) => at >= 0), 'every part is there');
  assert.deepEqual([...order].sort((a, b) => a - b), order, 'DOM order');
});

test('the timeline is in normal flow (no sticky overlay) and the lane-fit rule holds; nothing scrolls on its own', () => {
  const style = (source.match(/<style[\s\S]*?<\/style>/) || [''])[0];
  // Owner report: facts and lists scrolled behind a sticky chart block and were clipped. The timeline now scrolls with the page.
  assert.ok(!/position:\s*sticky/.test(style));
  assert.ok(style.includes('--scall-lane-h'));
  assert.ok(!/overflow(-[xy])?\s*:\s*(auto|scroll)/.test(source));
  assert.ok(!/\bheight\s*:\s*\d/.test((style.match(/\.scall-(main|pin|grid)[^{]*\{[^}]*\}/g) || []).join(' ')));
});

test('the window is previewed only by the minimap and committed by the window bar and the minimap', () => {
  assert.equal((source.match(/controller\??\.preview\(/g) || []).length, 1);
  assert.ok(/onPreview=\{[^}]*controller\??\.preview\(/.test(source));
  assert.ok((source.match(/controller\??\.commit\(/g) || []).length >= 2);
  assert.ok(/onCommit=\{\(w\) => controller\??\.commit\(/.test(source));
  assert.ok(source.includes('fetchLaneDays('));
});

test('the tabs follow the WAI-ARIA pattern', () => {
  for (const needle of ['role="tablist"', 'role="tab"', 'role="tabpanel"', 'aria-selected', 'aria-controls', 'ArrowRight', 'ArrowLeft', "'Home'", "'End'"]) {
    assert.ok(source.includes(needle), needle);
  }
});

test('RB-5 F-01: only timed IIS bars reach the list as timed items; zero-length IIS markers go in as markers', () => {
  assert.ok(/iisBars:\s*lanes\.iis\.bars\.filter\(\(b\) => b\.kind === 'bar'\)/.test(source));
  assert.ok(!/iisBars:\s*lanes\.iis\.bars,/.test(source));
  assert.ok(/b\.kind === 'marker'/.test(source));
  assert.ok(/marker: true/.test(source) && /tookMs: null/.test(source));
});

test('RB-5 F-02: a missing API duration is never turned into a 0 ms bar', () => {
  assert.ok(!/Number\.isFinite\(row\.durationMs\) \? row\.durationMs : 0/.test(source));
  assert.ok(/row\.durationMs == null \|\| !Number\.isFinite\(row\.durationMs\)\) return \[\]/.test(source));
});

test('RB-5 F-03/F-04: the failed and cut flags are read and shown with the copy keys', () => {
  for (const [flag, key, id] of [
    ['slowRowsFailed', 'slowRowsFailed', 'scall-slowrows-failed'],
    ['overviewFailed', 'overviewFailed', 'scall-overview-failed'],
    ['eventsTruncated', 'eventsTruncated', 'scall-events-truncated']
  ]) {
    assert.ok(markup.includes(`bench.s.${flag}`), flag);
    assert.ok(markup.includes(`DRILL_TEXT.${key}`), key);
    assert.ok(markup.includes(`data-testid="${id}"`), id);
    assert.ok(new RegExp(`role="status"[^>]*data-testid="${id}"`).test(markup), `${id} role`);
  }
});

test('RB-5 F-08: pretend only counts in demo mode', () => {
  assert.ok(source.includes('const pretendNow = demo ? pretend : null;'));
  assert.ok(/pretend: pretendNow/.test(source));
  assert.ok(!/\bpretend &&/.test(source));
});

test('RB-5: the lane fields the parser keeps pass straight through toLaneIn', () => {
  const fn = source.slice(source.indexOf('function toLaneIn'), source.indexOf('const SQL_EVENT_SOURCES'));
  for (const needle of ['lane.reasonCode', 'lane.blockSeconds', 'lane.buckets', 'lane.events']) assert.ok(fn.includes(needle), needle);
});

test('RB-5: the database block length falls back to the Query Store interval', () => {
  assert.ok(source.includes('queryStore?.intervalMinutes'));
  assert.ok(source.includes('lane.blockSeconds ?? qsBlockSeconds(lane.key)'));
  assert.ok(/minutes \* 60/.test(source));
});

// --- The restart key ---------------------------------------------------------------------------
// The parent (TracingPaper) replaces `target` with a NEW object once the call is found, keeping the
// same case and slot. Reading `target.caseId` inside the effect tracks the whole `target` prop, so
// that replacement restarted the controller, which found the call again, which replaced the target
// again: an endless search loop. The effect must read a derived primitive key instead, so only a
// changed case or slot VALUE restarts the controller.

test('the restart effect reads a derived case:slot key, never the target prop itself', () => {
  assert.ok(/const restartKey = \$derived\(`\$\{target\.caseId\}:\$\{target\.slot\}`\);/.test(source), 'a primitive key derived from the case and slot values');
  const effect = /\$effect\(\(\) => \{[\s\S]*?\n  \}\);/.exec(source);
  assert.ok(effect, 'the restart effect is there');
  const body = effect[0];
  assert.ok(body.includes('void restartKey;'), 'the effect reads the primitive key');
  assert.ok(!/void target\.(caseId|slot)/.test(body), 'the effect must not track the target prop reference');
  // Creation and the state reset stay untracked, and the cleanup still disposes on a real restart.
  assert.ok(/untrack\(\(\) => \{[\s\S]*?callState = null;[\s\S]*?selectedKey = null;[\s\S]*?createCallController\(target, deps,/.test(body), 'creation and reset inside untrack');
  assert.ok(/return \(\) => \{\s*ctl\.dispose\(\);\s*if \(controller === ctl\) controller = null;\s*\};/.test(body), 'the cleanup disposes and clears');
});

// The runtime harness: the screen's real key + effect code, compiled by the real Svelte compiler and
// run by the real Svelte client runtime (no DOM needed — the effect only makes and disposes a controller).
// `target` behaves like a prop: a derived over a state source, so replacing the object re-reads the key
// exactly as a parent replacement does.
const svelteClientUrl = pathToFileURL(path.join(root, 'node_modules/svelte/src/index-client.js')).href;
const svelteInternalUrl = pathToFileURL(path.join(root, 'node_modules/svelte/src/internal/client/index.js')).href;
let harnessSeq = 0;

function extractRestartCode() {
  const key = /const restartKey = \$derived\([\s\S]*?\);/.exec(source);
  const effect = /\$effect\(\(\) => \{[\s\S]*?\n  \}\);/.exec(source);
  assert.ok(key && effect, 'the restart key and effect are in the source');
  return { key: key[0], effect: effect[0] };
}

async function buildRestartHarness(keyCode, effectCode) {
  const moduleSource = `// harness ${++harnessSeq}
import { untrack } from 'svelte';
let callState = $state(null);
let controller = $state.raw(null);
let selectedKey = $state(null);
const deps = {};
let targetSource = $state(null);
const target = $derived(targetSource);
export const log = [];
export const created = [];
export const controllers = [];
function createCallController(t, d, onState, onFound) {
  const ctl = { start() { log.push('start'); }, dispose() { log.push('dispose'); } };
  created.push(t);
  controllers.push(ctl);
  log.push('create');
  return ctl;
}
function onFound(call) { log.push('found'); }
export function setTarget(t) { targetSource = t; }
export function peek() { return { callState, selectedKey, controller }; }
export function init() {
${keyCode}
${effectCode}
}
`;
  const compiled = compileModule(moduleSource, { filename: 'slowcall-restart-harness.svelte.js', generate: 'client' });
  assert.deepEqual(compiled.warnings.map((w) => w.code), [], 'the harness compiles without warnings');
  const code = compiled.js.code
    .replace("from 'svelte/internal/client'", `from '${svelteInternalUrl}'`)
    .replace("from 'svelte'", `from '${svelteClientUrl}'`);
  return import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'));
}

test('runtime: a same-value target replacement keeps the controller; a different slot or case restarts it', async () => {
  const { flushSync } = await import(svelteClientUrl);
  const { effect_root } = await import(svelteInternalUrl);
  const { key, effect } = extractRestartCode();
  const harness = await buildRestartHarness(key, effect);
  const caseId = 'abcdefghijklmnopqrstuvwxyz';
  // A stored call (the reload shape): the controller must receive the target as-is, call included.
  const stored = { caseId, slot: 4, start: '2026-10-19T10:00:00Z', end: '2026-10-19T10:05:00Z', sourceIds: ['src-a'], call: { sourceId: 'src-a', id: 'call-1' } };
  const dispose = effect_root(() => { harness.init(); harness.setTarget(stored); });
  flushSync();
  assert.deepEqual(harness.log, ['create', 'start'], 'one controller, started');
  // ($state proxies the object, so compare the contents: the stored call must reach the controller, so find() skips the search.)
  assert.deepEqual(harness.created[0], stored, 'the stored target reaches the controller with its call (reload skips the search)');
  assert.equal(harness.peek().controller, harness.controllers[0]);

  // The parent replaces `target` once the call is found: a NEW object, same case and slot (the withCall shape).
  const found = { ...stored, sourceIds: [...stored.sourceIds], call: { sourceId: 'src-a', id: 'call-1' } };
  assert.notEqual(found, stored, 'the replacement is a different object');
  harness.setTarget(found);
  flushSync();
  harness.setTarget({ ...found, sourceIds: [...found.sourceIds] });
  flushSync();
  assert.deepEqual(harness.log, ['create', 'start'], 'a same-value replacement must not dispose or recreate the controller');
  assert.equal(harness.peek().controller, harness.controllers[0], 'the original controller stays in place');

  // A genuinely different slot restarts: the old controller is disposed, a new one created and started.
  harness.setTarget({ ...found, slot: 5 });
  flushSync();
  assert.deepEqual(harness.log, ['create', 'start', 'dispose', 'create', 'start'], 'a different slot restarts');
  assert.equal(harness.created.at(-1).slot, 5);
  assert.equal(harness.peek().controller, harness.controllers.at(-1));

  // A different case with the same slot also restarts.
  const otherCase = 'zyxwvutsrqponmlkjihgfedcba';
  harness.setTarget({ ...found, slot: 5, caseId: otherCase });
  flushSync();
  assert.deepEqual(harness.log, ['create', 'start', 'dispose', 'create', 'start', 'dispose', 'create', 'start'], 'a different case restarts');
  assert.equal(harness.created.at(-1).caseId, otherCase);

  // Unmount disposes the live controller and clears the state.
  dispose();
  flushSync();
  assert.deepEqual(harness.log, ['create', 'start', 'dispose', 'create', 'start', 'dispose', 'create', 'start', 'dispose'], 'unmount disposes');
  assert.equal(harness.peek().controller, null);
  assert.equal(harness.peek().callState, null);
  assert.equal(harness.peek().selectedKey, null);
});

test('runtime: the harness sees the pre-fix bug — tracking the prop restarts on a same-value replacement', async () => {
  const { flushSync } = await import(svelteClientUrl);
  const { effect_root } = await import(svelteInternalUrl);
  // The pre-fix effect read the prop fields directly, which tracks the whole `target` reference.
  // This counter-proof shows the harness above can observe a restart, so its pass is meaningful.
  const oldEffect = `$effect(() => {
    void target.caseId;
    void target.slot;
    const ctl = untrack(() => {
      callState = null;
      selectedKey = null;
      const made = createCallController(target, deps, (next) => { callState = next; }, (call) => onFound(call));
      controller = made;
      made.start();
      return made;
    });
    return () => {
      ctl.dispose();
      if (controller === ctl) controller = null;
    };
  });`;
  const harness = await buildRestartHarness('', oldEffect);
  const base = { caseId: 'abcdefghijklmnopqrstuvwxyz', slot: 4, start: '2026-10-19T10:00:00Z', end: '2026-10-19T10:05:00Z', sourceIds: ['src-a'] };
  const dispose = effect_root(() => { harness.init(); harness.setTarget(base); });
  flushSync();
  assert.deepEqual(harness.log, ['create', 'start']);
  harness.setTarget({ ...base, sourceIds: [...base.sourceIds], call: { sourceId: 'src-a', id: 'call-1' } });
  flushSync();
  assert.deepEqual(harness.log, ['create', 'start', 'dispose', 'create', 'start'], 'the old tracking restarts on a same-value replacement — the loop this fix removes');
  dispose();
  flushSync();
});
