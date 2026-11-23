import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const read = (rel) => readFileSync(join(here, '..', '..', '..', rel), 'utf8');
const json = (rel) => JSON.parse(read(rel));

// The step language of lib/steps.ts. There is deliberately no "only on newer builds" step: a guard like that turned a renamed or
// removed part of a merged feature into "not on this build" and the audit still passed (sweep 2026-11-22, finding 7). A part a screen
// needs is a `waitFor`: when it is missing the screen is "not reached", which fails the run unless baseline.json lists it.
const KINDS = ['goto', 'click', 'fill', 'select', 'waitFor', 'scrollTo', 'clickCentre', 'theme', 'aside', 'upload', 'macro'];

function* stepsOf(list) {
  for (const s of list ?? []) {
    yield s;
    if (Array.isArray(s.aside)) yield* stepsOf(s.aside);
  }
}
function* allSteps(file) {
  const d = json(file);
  for (const screen of d.screens) {
    for (const s of stepsOf(screen.steps)) yield [screen.id, s];
    for (const s of stepsOf(screen.after)) yield [screen.id, s];
  }
  for (const [name, body] of Object.entries(d.macros ?? {})) for (const s of stepsOf(body)) yield ['macro ' + name, s];
}

for (const file of ['tools/screens/screens.json', 'tools/uiaudit/extra-screens.json']) {
  test(file + ': every step is a kind the runner knows (no `feature` guard)', () => {
    const bad = [];
    let n = 0;
    for (const [id, step] of allSteps(file)) {
      n++;
      const kinds = Object.keys(step).filter((k) => k !== 'when');
      if (kinds.length !== 1 || !KINDS.includes(kinds[0])) bad.push(id + ': ' + JSON.stringify(step).slice(0, 80));
    }
    assert.ok(n > 0);
    assert.deepEqual(bad, []);
  });
}

test('the runner has no step that skips a missing part of a screen', () => {
  const steps = read('tools/uiaudit/lib/steps.ts');
  const spec = read('tools/uiaudit/specs/audit.spec.ts');
  assert.ok(!/\bfeature\b/.test(steps), 'lib/steps.ts has a `feature` step again');
  assert.ok(!/NotOnBuild/.test(steps + spec), 'a NotOnBuild escape is back in the runner');
  for (const k of KINDS) assert.ok(steps.includes("'" + k + "' in step") || steps.includes('{ ' + k + ':'), 'steps.ts does not know ' + k);
});

test('the parts the add-files screens wait for still exist in the web source', () => {
  // Hard checks now (they were `feature` guards). If one of these is renamed, rename it in extra-screens.json too.
  const extra = JSON.stringify(json('tools/uiaudit/extra-screens.json'));
  const files = { '[data-remove-left-out]': 'web/src/components/wizard/ImportFilesPanel.svelte', '[data-hosts-summary]': 'web/src/components/wizard/ReviewStep.svelte' };
  for (const [selector, file] of Object.entries(files)) {
    assert.ok(extra.includes(JSON.stringify(selector).slice(1, -1)), 'extra-screens.json no longer waits for ' + selector);
    assert.ok(read(file).includes(selector.slice(1, -1)), file + ' no longer has ' + selector);
  }
  // And each of them is waited for with `waitFor`, in the screens that need them.
  const d = json('tools/uiaudit/extra-screens.json');
  for (const [id, selector] of [['p10-files-4', '[data-remove-left-out]'], ['p10-files-5', '[data-remove-left-out]'], ['p10-files-7', '[data-hosts-summary]']]) {
    const screen = d.screens.find((s) => s.id === id);
    assert.ok(screen, id + ' is missing');
    assert.ok(screen.steps.some((s) => s.waitFor === selector), id + ' does not waitFor ' + selector);
  }
});
