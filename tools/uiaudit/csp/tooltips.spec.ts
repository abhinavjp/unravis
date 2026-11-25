// BL-323: a rich-text tooltip is drawn on the chart canvas, so it must fit inside the chart (an HTML tooltip could hang out of it). This opens the
// multi-API timeline ("When did APIs slow down together?", up to 8 groups, a 340 px chart), hovers it with 3 and with 8 groups selected, and measures the
// tooltip box in the canvas pixels. A control with the chart squeezed to 200 px proves the measurement does see a clipped tooltip.
//   UIAUDIT_ONLY is not used here; run with: npx playwright test -c playwright.csp.config.ts csp/tooltips.spec.ts
import { test, expect, type Page } from '@playwright/test';
import { arm, CHARTS, hoverChart, trackRequests, type Violation } from './harness';

async function openTimeline(page: Page): Promise<void> {
  await page.clock.setFixedTime(new Date(process.env.UNRAVIS_DEMO_NOW ?? '2026-10-20T09:00:00Z'));
  await page.goto(process.env.UNRAVIS_BASE_URL! + '/#t=' + process.env.UNRAVIS_TOKEN);
  await page.locator('role=button[name="Where is it?"s]').click();
  // The page's own hidden h1 has this name too, so the screen's heading is found by its id.
  await page.locator('#where-title').waitFor();
  await page.locator('summary:has-text("API groups")').click();
}

async function chooseGroups(page: Page, n: number): Promise<number> {
  const boxes = page.locator('input[type=checkbox][aria-label^="Compare timeline for"]');
  const count = await boxes.count();
  for (let i = 0; i < count; i++) await boxes.nth(i).setChecked(i < n);
  return Math.min(n, count);
}

async function timelineIndex(page: Page): Promise<number> {
  const all = await page.locator(CHARTS).evaluateAll((els) => els.map((el) => el.classList.contains('multi-chart')));
  const at = all.indexOf(true);
  expect(at, 'the multi-API timeline chart is on the page').toBeGreaterThanOrEqual(0);
  return at;
}

for (const [width, height, label] of [[1440, 900, 'desktop'], [390, 844, 'phone']] as const) {
  test(`the multi-API timeline tooltip fits its chart with 3 and with 8 groups (${label})`, async ({ page, context }) => {
    const found: Violation[] = [];
    const lines: string[] = [];
    await arm(context, found, lines);
    const requests = trackRequests(page);
    await page.setViewportSize({ width, height });
    await openTimeline(page);
    await requests.quiet();
    for (const wanted of [3, 8]) {
      const groups = await chooseGroups(page, wanted);
      expect(groups, 'the demo case offers ' + wanted + ' API groups').toBe(wanted);
      await requests.quiet();
      const at = await timelineIndex(page);
      const tip = await hoverChart(page, at, [[0.5, 0.5], [0.4, 0.5], [0.6, 0.5], [0.3, 0.4], [0.7, 0.4]], true);
      expect(tip.drawn, wanted + ' groups: a tooltip was drawn').toBe(true);
      const box = tip.box!;
      // Lines: the time line, one for each group, and maybe the caution line; 22 px each and 12 px of padding.
      expect(box.bottom - box.top + 1, wanted + ' groups: the box holds every group line').toBeGreaterThanOrEqual((wanted + 1) * 22);
      expect(box.top, wanted + ' groups: inside the top edge').toBeGreaterThanOrEqual(0);
      expect(box.bottom, wanted + ' groups: the bottom of the box is inside the chart (not cut off at ' + tip.height + ' px)').toBeLessThan(tip.height - 2);
      expect(box.right, wanted + ' groups: the right of the box is inside the chart (not cut off at ' + tip.width + ' px)').toBeLessThan(tip.width - 2);
      expect(box.left).toBeGreaterThan(0);
    }
    expect(found, 'no policy violation while hovering').toEqual([]);
    expect(lines).toEqual([]);
  });
}

test('control: a chart too short for its tooltip is seen as clipped by the same measurement', async ({ page, context }) => {
  await arm(context, [], []);
  const requests = trackRequests(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openTimeline(page);
  await requests.quiet();
  await chooseGroups(page, 8);
  await requests.quiet();
  const at = await timelineIndex(page);
  // CSSOM writes are not blocked by the policy: squeeze the chart, let ECharts resize, hover again.
  await page.locator('.multi-chart').evaluate((el) => { (el as HTMLElement).style.height = '200px'; });
  await page.locator('.multi-chart-stage').evaluate((el) => { (el as HTMLElement).style.height = '200px'; });
  await page.evaluate(() => new Promise<void>((r) => setTimeout(r, 0)));
  await expect.poll(() => page.locator('.multi-chart canvas').first().evaluate((c) => (c as HTMLCanvasElement).height), { message: 'the chart resized' }).toBeLessThanOrEqual(210);
  const tip = await hoverChart(page, at, [[0.5, 0.3], [0.4, 0.35], [0.6, 0.3]], true);
  expect(tip.drawn).toBe(true);
  expect(tip.box!.bottom, 'the tooltip runs to the bottom edge of the squeezed chart (200 px: the grid keeps 96 px of margins, so the plot is still hoverable)').toBeGreaterThanOrEqual(tip.height - 2);
});
