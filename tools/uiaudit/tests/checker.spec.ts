import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const root = join(__dirname, '..');
const checks = readFileSync(join(root, 'lib', 'page-checks.js'), 'utf8');

async function audit(page: Page, fixture: string) {
  await page.goto('file://' + join(root, 'fixtures', fixture));
  await page.evaluate(checks);
  return page.evaluate(() => (window as any).__uiaudit.run({})) as Promise<{ findings: { check: string; severity: string; label: string; message: string }[] }>;
}
const has = (r: { findings: { check: string; label: string }[] }, check: string, label?: string) =>
  r.findings.some((f) => f.check === check && (label === undefined || f.label.includes(label)));

test('the checker finds every planted fault in bad.html', async ({ page }) => {
  const r = await audit(page, 'bad.html');
  expect(has(r, 'page-overflow-x'), 'sideways scroll').toBe(true);
  expect(has(r, 'text-clipped', 'far too long'), 'cut-off text').toBe(true);
  expect(has(r, 'text-truncated'), 'ellipsis note').toBe(true);
  expect(has(r, 'text-spill', 'Overflowing'), 'spilling chip').toBe(true);
  expect(has(r, 'overlap', 'First'), 'overlap').toBe(true);
  expect(has(r, 'misaligned-row', 'Alpha'), 'row off line').toBe(true);
  expect(has(r, 'small-target', 'x'), 'tiny target').toBe(true);
  expect(has(r, 'no-name'), 'nameless button').toBe(true);
  expect(has(r, 'no-label'), 'unlabelled field').toBe(true);
  expect(has(r, 'low-contrast', 'Pale grey'), 'contrast').toBe(true);
  expect(has(r, 'small-text', 'Nine pixel'), 'tiny text').toBe(true);
  expect(has(r, 'bad-text', 'undefined'), 'code word').toBe(true);
  expect(has(r, 'misaligned-column', 'three'), 'stack edge').toBe(true);
  expect(has(r, 'broken-image'), 'broken picture').toBe(true);
  expect(has(r, 'theme-mismatch'), 'dark slab on a light page').toBe(true);
  expect(has(r, 'heading-jump'), 'heading jump').toBe(true);
});

test('the checker finds nothing to complain about in good.html', async ({ page }) => {
  const r = await audit(page, 'good.html');
  expect(r.findings.filter((f) => f.severity !== 'info').map((f) => f.check + ': ' + f.message)).toEqual([]);
});

test('hidden things are not reported (closed details, display none)', async ({ page }) => {
  await page.setContent('<!doctype html><html lang="en"><title>t</title><body style="margin:0"><details><summary style="min-height:30px">More</summary><button style="width:10px;height:10px">z</button></details><button style="display:none;width:10px;height:10px">y</button></body></html>');
  await page.evaluate(checks);
  const r = (await page.evaluate(() => (window as any).__uiaudit.run({}))) as { findings: { check: string }[] };
  expect(r.findings.filter((f) => f.check === 'small-target')).toEqual([]);
});

test('a hit area drawn by an absolutely positioned ::after counts for the tap target size, a decoration does not', async ({ page }) => {
  await page.setContent(`<!doctype html><html lang="en"><title>t</title><style>
    body { margin: 20px; } button { position: relative; width: 14px; height: 14px; padding: 0; border: 0; margin: 20px; display: block; }
    #grown::after { content: ''; position: absolute; inset: -6px; }
    #ring::after { content: ''; position: absolute; inset: -6px; pointer-events: none; }
    #slim::after { content: ''; position: absolute; inset: -2px; }
    #static { position: static; } #static::after { content: ''; position: absolute; inset: -6px; }
  </style><body><button id="grown">a</button><button id="ring">b</button><button id="slim">c</button><button id="static">d</button></body></html>`);
  await page.evaluate(checks);
  const r = (await page.evaluate(() => (window as any).__uiaudit.run({}))) as { findings: { check: string; severity: string; label: string; message: string }[] };
  const small = r.findings.filter((f) => f.check === 'small-target' && f.severity === 'warn').map((f) => f.label);
  expect(small.sort()).toEqual(['b', 'c', 'd']);
});
