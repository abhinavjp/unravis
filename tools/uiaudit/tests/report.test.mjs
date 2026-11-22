import { test } from 'node:test';
import assert from 'node:assert/strict';
import { classify, tally, verdict, renderReport, findKnown } from '../lib/report.mjs';

const mk = (id, variant, findings, status = 'checked') => ({ id, variant, size: variant.split('-')[0], scheme: variant.split('-')[1], phase: 'P1', status, shot: 'shots/' + id + '__' + variant + '.png', findings, reason: status === 'unreachable' ? 'no button' : undefined });
const f = (check, severity, label = 'x') => ({ check, severity, label, message: 'm', selector: 's', rect: { x: 0, y: 0, w: 1, h: 1 } });

test('a new error fails the run, a known one does not', () => {
  const results = [mk('a', 'desktop-dark', [f('overlap', 'error', 'Save')])];
  assert.equal(verdict(tally(classify(results, { entries: [] }))), 'FAIL');
  const known = classify([mk('a', 'desktop-dark', [f('overlap', 'error', 'Save')])], { entries: [{ screen: 'a', check: 'overlap', why: 'tracked', item: 'BL-1' }] });
  assert.equal(verdict(tally(known)), 'PASS');
  assert.equal(tally(known).knownError, 1);
});

test('warnings and notes never fail the run', () => {
  const t = tally(classify([mk('a', 'phone-dark', [f('small-target', 'warn'), f('text-truncated', 'info')])], { entries: [] }));
  assert.equal(verdict(t), 'PASS');
  assert.equal(t.newWarn, 1);
  assert.equal(t.info, 1);
});

test('an unreachable screen fails unless the baseline lists it', () => {
  const r = [mk('a', 'desktop-dark', undefined, 'unreachable')];
  assert.equal(verdict(tally(classify(r, { entries: [] }))), 'FAIL');
  const ok = classify([mk('a', 'desktop-dark', undefined, 'unreachable')], { entries: [{ screen: 'a', check: 'unreachable', why: 'windows only' }] });
  assert.equal(verdict(tally(ok)), 'PASS');
});

test('baseline entries match with * and ignore the theme', () => {
  const r = mk('p1-home', 'phone-light', []);
  assert.ok(findKnown({ entries: [{ screen: '*', check: 'small-text', label: 'What does * (?)', size: 'phone' }] }, r, f('small-text', 'warn', 'What does P# mean? (?)')));
  assert.equal(findKnown({ entries: [{ screen: 'other', check: 'small-text' }] }, r, f('small-text', 'warn')), null);
});

test('the report names every part and links the screenshots', () => {
  const results = classify([
    mk('a', 'desktop-dark', [f('overlap', 'error', 'Save')]),
    mk('a', 'desktop-light', []),
    mk('b', 'desktop-dark', undefined, 'unreachable')
  ], { entries: [] });
  const md = renderReport({ results, baseline: { entries: [] }, run: { when: 'now', build: 'b', demoNow: 'n', browser: 'c' } });
  for (const part of ['# Unravis UI audit report', '## Verdict: FAIL', '## Problems on screen', '### a', '![a desktop-dark](shots/a__desktop-dark.png)', '## Screens that could not be reached', '## All screens', '| b |']) {
    assert.ok(md.includes(part), 'missing: ' + part);
  }
});
