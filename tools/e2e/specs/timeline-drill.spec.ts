// The Slow call screen on the Investigation Timeline: the slowest call in a slot, opened from a red diamond in the API lane.
//   DRILL-01  A click on the first red diamond opens the Slow call screen with "Where the time went"; every visible hint line is filled in.
//   DRILL-02  A call saved before the server recorded timings says so (the no-timings line).
//   DRILL-03  Esc returns to the timeline without another read, with focus inside the chart region; "Open in Analysis" shows Analysis.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';

const CASE_TITLE = 'Orders API stall, 19 October';

async function openTimeline(page: Page): Promise<void> {
  await goHome(page);
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
}

/** Clicks the first red diamond of the API lane at its centre. */
async function clickFirstDiamond(page: Page, n = 0): Promise<void> {
  const diamond = page.locator('.panel[data-lane=audit] .mark.flagged').nth(n);
  await expect(diamond).toBeVisible();
  await diamond.scrollIntoViewIfNeeded();
  const box = await diamond.boundingBox();
  if (!box) throw new Error('the diamond has no box');
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
}

const FULL_PERF = {
  hitCount: 3, filterTotalMs: 10, sqlCount: 2, sqlMs: 40, redisCount: 1, redisMs: 5, httpCount: 1, httpMs: 20,
  filters: [{ name: 'tenant', ms: 6 }], steps: [{ name: 'auth', ms: 4 }],
  slowSql: [{ count: 2, ms: 30 }], slowHttp: [{ count: 1, ms: 20 }],
  pressure: [{ name: 'cpu', value: 3.5 }], textStored: false
};

test('DRILL-01: a click on a red diamond opens the Slow call screen with "Where the time went" and a hint under every section', async ({ page }) => {
  // About half of the demo calls are "saved before timings"; a call without them gets a full block so every section is drawn.
  await page.route('**/api/record*', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.perf ??= FULL_PERF;
    await route.fulfill({ response, json: body });
  });
  await openTimeline(page);
  await clickFirstDiamond(page);
  const screen = page.getByTestId('slow-call-screen');
  await expect(screen).toBeVisible();
  await expect(page.locator('.tracing-paper')).toBeHidden();
  await expect(screen.getByText('Where the time went')).toBeVisible();
  // Every overview section carries a visible, non-empty hint for a first-time investigator.
  const sections = screen.getByTestId('scall-thiscall').locator('section.block');
  const count = await sections.count();
  expect(count).toBeGreaterThan(0);
  for (let i = 0; i < count; i++) {
    const hints = sections.nth(i).locator('.hint');
    expect(await hints.count(), `section ${i} has a hint`).toBeGreaterThan(0);
    for (let j = 0; j < (await hints.count()); j++) {
      await expect(hints.nth(j)).toBeVisible();
      expect(((await hints.nth(j).innerText()) ?? '').trim().length).toBeGreaterThan(10);
    }
  }
});

test('DRILL-02: a call saved without timings says so', async ({ page }) => {
  await page.route('**/api/record*', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.perf = null;
    await route.fulfill({ response, json: body });
  });
  await openTimeline(page);
  await clickFirstDiamond(page);
  const screen = page.getByTestId('slow-call-screen');
  await expect(screen).toBeVisible();
  await expect(screen.getByTestId('drill-no-timings')).toBeVisible();
});

test('DRILL-03: Esc returns to the timeline without another read, with focus inside the chart region; Open in Analysis shows Analysis', async ({ page }) => {
  let tracingRequests = 0;
  page.on('request', (request) => {
    if (/^\/api\/cases\/[^/]+\/tracing$/.test(new URL(request.url()).pathname)) tracingRequests++;
  });
  await openTimeline(page);
  await clickFirstDiamond(page);
  const screen = page.getByTestId('slow-call-screen');
  await expect(screen).toBeVisible();
  await expect(page.locator('.tracing-paper')).toBeHidden();
  const beforeEscape = tracingRequests;
  expect(beforeEscape, 'the tracing request counter observed the opening read').toBeGreaterThan(0);
  await page.keyboard.press('Escape');
  // The packet requires a 500 ms observation window for any new tracing request after Esc.
  await page.waitForTimeout(500);
  expect(tracingRequests, 'Esc does not read the timeline again').toBe(beforeEscape);
  await expect(screen).toHaveCount(0);
  await expect(page.locator('.tracing-paper')).toBeVisible();
  await expect.poll(
    () => page.evaluate(() => !!document.activeElement?.closest('[data-region=chart]')),
    { message: 'focus is inside the chart region' }
  ).toBe(true);

  await clickFirstDiamond(page);
  await expect(screen).toBeVisible();
  await screen.getByTestId('scall-analysis').click();
  await expect(screen).toHaveCount(0);
  // The timeline may stay mounted for a return; Analysis must hide it, not necessarily remove it.
  await expect(page.locator('.tracing-paper')).toBeHidden();
  await expect(page.getByRole('button', { name: /Run analysis/i })).toBeVisible();
});
