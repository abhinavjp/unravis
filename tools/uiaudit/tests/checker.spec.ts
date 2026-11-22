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
    /* the static control sits in a positioned box of its own: its ::after is placed against that box (and not against the whole window, where it would cover every button above) */
    #static { position: static; } #static::after { content: ''; position: absolute; inset: -6px; }
  </style><body><button id="grown">a</button><button id="ring">b</button><button id="slim">c</button><div style="position: relative; padding: 1px 0"><button id="static">d</button></div></body></html>`);
  await page.evaluate(checks);
  const r = (await page.evaluate(() => (window as any).__uiaudit.run({}))) as { findings: { check: string; severity: string; label: string; message: string }[] };
  const small = r.findings.filter((f) => f.check === 'small-target' && f.severity === 'warn').map((f) => f.label);
  expect(small.sort()).toEqual(['b', 'c', 'd']);
});

// The size of a hit area drawn by a ::before or ::after is only believed when the browser says a press there goes to the control
// (elementFromPoint at the centre and at the four corners). Each of the cases below is a 14 x 14 px button whose pseudo-element would,
// by its size and offsets alone, make the target 26 x 26 px or more. Only `good` really is. Found by the review of PR 156.
test('a hit area that is far off, clipped, covered, shrunk or past the window edge does not count for the tap target size', async ({ page }) => {
  await page.setViewportSize({ width: 600, height: 1000 });
  await page.setContent(`<!doctype html><html lang="en"><title>t</title><style>
    body { margin: 0; }
    .case { position: relative; height: 120px; margin-left: 40px; }
    button { position: absolute; left: 20px; top: 40px; width: 14px; height: 14px; padding: 0; border: 0; }
    .good::after, .scaled::after, .clipped::after, .covered::after { content: ''; position: absolute; inset: -6px; }
    /* far off: a pseudo-element 60 px below the control, a big one that answers presses and a 2 px strip (the join of the two boxes would count the gap) */
    .far::after { content: ''; position: absolute; left: 0; top: 60px; width: 30px; height: 30px; }
    .strip::after { content: ""; position: absolute; left: -8px; top: 60px; width: 30px; height: 2px; }
    /* clipped: an ancestor with overflow hidden cuts the pseudo-element off at the control's own box */
    .cut { position: absolute; left: 20px; top: 40px; width: 14px; height: 14px; overflow: hidden; }
    .cut button { left: 0; top: 0; }
    /* covered: another element lies over the whole grown area */
    .lid { position: absolute; left: 10px; top: 30px; width: 34px; height: 34px; z-index: 5; background: rgba(0,0,0,.1); }
    .scaled::after { transform: scale(0); }
    /* past the window edge: the case starts at the left edge, so the left part of the grown area is outside the window */
    .edge { margin-left: 0; }
    .edge button { left: 0; }
    .edge button::after { content: ''; position: absolute; inset: -6px; }
  </style><body>
    <div class="case"><button class="good">good</button></div>
    <div class="case"><button class="far">far</button></div>
    <div class="case"><button class="strip">strip</button></div>
    <div class="case"><div class="cut"><button class="clipped">clipped</button></div></div>
    <div class="case"><button class="covered">covered</button><div class="lid"></div></div>
    <div class="case"><button class="scaled">scaled</button></div>
    <div class="case edge"><button>edge</button></div>
  </body></html>`);
  await page.evaluate(checks);
  const r = (await page.evaluate(() => (window as any).__uiaudit.run({}))) as { findings: { check: string; severity: string; label: string; message: string }[] };
  const small = r.findings.filter((f) => f.check === 'small-target' && f.severity === 'warn');
  expect(small.map((f) => f.label).sort()).toEqual(['clipped', 'covered', 'edge', 'far', 'scaled', 'strip']);
  // and each is reported with its own size, not the size of the lie
  for (const f of small) expect(f.message, f.label).toMatch(/Tap target is 14 x 14 px/);
});
