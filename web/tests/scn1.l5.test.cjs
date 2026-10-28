'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript, step, suspect, result, change } = require('./guided.fixture.cjs');

const read = (relative) => fs.readFileSync(path.join(__dirname, '..', relative), 'utf8');
const guided = (name) => read('src/lib/components/guided/' + name);
const store = () => read('src/lib/guided/guided.svelte.ts');
const SHELL = loadTypeScript('src/lib/copy/guidedShellCopy.ts').SHELL_TEXT;

// Every string of the shell copy, and every function of it called with a word and a few numbers.
function shellLines() {
  const out = [];
  for (const [key, value] of Object.entries(SHELL)) {
    if (typeof value === 'string') out.push([key, value]);
    else if (typeof value === 'function') for (const arg of ['zzz', 0, 1, 3, 300]) out.push([`${key}(${arg})`, String(value(arg))]);
  }
  return out;
}

test('L5 shell copy: plain sentences, no emoji, no dashes, no causal words', () => {
  const lines = shellLines();
  assert.ok(lines.length > 0);
  for (const [key, text] of lines) {
    assert.ok(text.trim() === text && text.length > 0, key);
    assert.ok(!/\p{Extended_Pictographic}/u.test(text), `${key}: emoji`);
    assert.ok(!/[‒-―−]/.test(text) && !/\s-\s/.test(text) && !text.includes('--'), `${key}: dash`);
    assert.ok(!/\b(because|caused|causes|due to|culprit|root cause)\b/i.test(text), `${key}: causal word`);
  }
});

test('B-02: a step change moves the keyboard to the step title', () => {
  assert.match(store(), /titleFocus = \$state\(0\);/);
  assert.match(store(), /goTo\(step: number\): void \{ this\.step = clampStep\(step\); this\.titleFocus\+\+; \}/);
  assert.match(guided('StepShell.svelte'), /<h3 id=\{'step-title-' \+ step\} tabindex="-1">/);
  const g = guided('GuidedAnalysis.svelte');
  assert.match(g, /let seenFocus = untrack\(\(\) => store\.titleFocus\);/);
  assert.match(g, /document\.getElementById\('step-title-' \+ store\.step\)\?\.focus\(\)/);
  assert.match(g, /store\.mode !== 'guided' \|\| panel !== 'main'/);
});

test('B-31: the heading names the mode on screen', () => {
  assert.equal(SHELL.manualHeading, 'Manual tools');
  assert.equal(SHELL.manualHeading, loadTypeScript('src/lib/copy/guidedCopy.ts').GUIDED_TEXT.toolsHeading);
  const g = guided('GuidedAnalysis.svelte');
  assert.match(g, /<h2 tabindex="-1" bind:this=\{heading\}>\{store\.mode === 'guided' \? T\.heading : S\.manualHeading\}<\/h2>/);
  assert.match(g, /import \{ SHELL_TEXT as S \} from '\.\.\/\.\.\/copy\/guidedShellCopy';/);
});
