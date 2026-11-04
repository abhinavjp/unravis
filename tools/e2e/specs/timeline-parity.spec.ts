// The page-level parts of the Investigation Timeline (the tracing-paper wireframe): the "Quick range" chip row above the two windows,
// the plain headings and help lines ("Investigation window", "Compare it with (comparison period)"), Add evidence and Refresh timeline in
// the "Sources traced" row, and the "windows changed" line. The demo clock is 2026-10-20T09:00Z, so the default incident window is
// 08:00-09:00 UTC and the default comparison period 07:00-08:00. A chip sets the windows but never reads by itself; only Start tracing
// or Refresh timeline sends a request.
//   TLPAR-01  The Quick range row sits above the windows, the six chips are in order, "Last hour" is pressed on a fresh screen, and
//             "Last 4 hours" sets 05:00-09:00 / 01:00-05:00, flips the pressed state and sends no trace; the popover keeps its own ranges.
//   TLPAR-02  The two h3 headings and their help lines; the fieldset legends still name the groups "Incident window (UTC)" and
//             "Comparison period (UTC)", and "UTC" is still on the page.
//   TLPAR-03  Add evidence sits in the "Sources traced" row and opens the Import files drawer.
//   TLPAR-04  Refresh timeline is disabled during the first read, repeats the previous request, and after a chip click it reads the
//             new window and the "windows changed" line goes away.
//   TLPAR-05  Closing the Add evidence drawer reads the timeline again.
//   TLPAR-06  Screenshots for the review: light and dark at 1280 and 375 px wide, written to .dev/tlpar-shots.
import { test, expect, type Page, type Request } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { openPicker, readWindow } from '../support/window-picker';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

const CASE_TITLE = 'Orders API stall, 19 October';
const SHOTS = join(__dirname, '..', '..', '..', '.dev', 'tlpar-shots');

type Body = Record<string, any>;

/** Opens the case timeline. */
async function openTimeline(page: Page): Promise<void> {
  await goHome(page);
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
}

const isTrace = (r: Request) => r.url().includes('/tracing') && r.method() === 'POST';
const sentBody = (r: Request): Body => JSON.parse(r.postData() ?? '{}');

/** Counts the traces the page sends from now on. */
function countTraces(page: Page): { count: () => number; bodies: Body[] } {
  const bodies: Body[] = [];
  page.on('request', (r) => { if (isTrace(r)) bodies.push(sentBody(r)); });
  return { count: () => bodies.length, bodies };
}

const quickRow = (page: Page) => page.locator('[data-testid=tp-quick-row]');
const quickChip = (page: Page, id: string) => quickRow(page).locator(`.chip[data-quick-id="${id}"]`);
const sourcesRow = (page: Page) => page.locator('[data-testid=trace-sources]');

/** Answers every trace as the server does, but holds the first POST answer back for `ms` so the loading state can be seen. */
async function delayFirstTrace(page: Page, ms: number): Promise<void> {
  let delayed = false;
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    if (!delayed) {
      delayed = true;
      await new Promise((resolve) => setTimeout(resolve, ms));
    }
    await route.fulfill({ response });
  });
}

test('TLPAR-01: the Quick range chips sit above the windows, in order; "Last 4 hours" sets 05:00-09:00 / 01:00-05:00 and sends no trace', async ({ page }) => {
  await openTimeline(page);
  await expect(page.locator('.tracing-chart')).toBeVisible();

  // The row is above the "Investigation window" heading.
  await expect(quickRow(page)).toBeVisible();
  const rowBox = await quickRow(page).boundingBox();
  const headingBox = await page.getByRole('heading', { level: 3, name: 'Investigation window', exact: true }).boundingBox();
  expect(rowBox, 'the quick range row has a box').not.toBeNull();
  expect(headingBox, 'the heading has a box').not.toBeNull();
  expect(rowBox!.y, 'the row is above the heading').toBeLessThan(headingBox!.y);

  // The six chips, in the wireframe's order.
  const chips = quickRow(page).locator('.chip');
  await expect(chips).toHaveText(['Last 15 min', 'Last hour', 'Last 4 hours', 'Last 24 hours', 'Last 7 days', 'Last 30 days']);
  expect(await chips.evaluateAll((els) => els.map((el) => el.getAttribute('data-quick-id')))).toEqual(['15m', '1h', '4h', '24h', '7d', '30d']);

  // A fresh screen shows the default hour pressed.
  await expect(quickChip(page, '1h')).toHaveAttribute('aria-pressed', 'true');
  await expect(quickChip(page, '4h')).toHaveAttribute('aria-pressed', 'false');

  // "Last 4 hours" sets both windows and flips the pressed state, but does not read by itself.
  const traces = countTraces(page);
  await quickChip(page, '4h').click();
  await expect(quickChip(page, '4h')).toHaveAttribute('aria-pressed', 'true');
  await expect(quickChip(page, '1h')).toHaveAttribute('aria-pressed', 'false');
  await page.waitForTimeout(500);
  expect(traces.count(), 'a chip sets the windows only; it never reads').toBe(0);
  expect(await readWindow(page, 'incident')).toEqual(['2026-10-20T05:00', '2026-10-20T09:00']);
  expect(await readWindow(page, 'comparison')).toEqual(['2026-10-20T01:00', '2026-10-20T05:00']);

  // The incident picker's popover still lists its own Quick ranges (with the "(default)" note the page row drops).
  const dialog = await openPicker(page, 'incident');
  await expect(dialog.getByRole('group', { name: 'Quick ranges' }).getByRole('button')).toHaveText(
    ['Last 15 min', 'Last hour (default)', 'Last 4 hours', 'Last 24 hours', 'Last 7 days', 'Last 30 days']
  );
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
});

test('TLPAR-02: the "Investigation window" and "Compare it with" headings with their help; the legends and "UTC" stay', async ({ page }) => {
  await openTimeline(page);

  await expect(page.getByRole('heading', { level: 3, name: 'Investigation window', exact: true })).toBeVisible();
  await expect(page.getByText('When did the problem happen? Up to 6 months; the default is the last hour.', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { level: 3, name: 'Compare it with (comparison period)' })).toBeVisible();
  await expect(page.getByText('This earlier period is only used for comparison. It may not have been healthy.', { exact: true })).toBeVisible();

  // The visually hidden legends keep the groups' spoken names, unchanged.
  await expect(page.getByRole('group', { name: 'Incident window (UTC)' })).toBeVisible();
  await expect(page.getByRole('group', { name: 'Comparison period (UTC)' })).toBeVisible();
  await expect(page.locator('fieldset.window-set legend')).toHaveText(['Incident window (UTC)', 'Comparison period (UTC)']);

  // "UTC" is still said on the page.
  await expect(page.getByText('Times are typed in UTC.', { exact: true })).toBeVisible();
});

test('TLPAR-03: Add evidence sits in the "Sources traced" row and opens the Import files drawer', async ({ page }) => {
  await openTimeline(page);
  await expect(sourcesRow(page).getByRole('heading', { name: 'Sources traced' })).toBeVisible();
  const add = sourcesRow(page).getByRole('button', { name: 'Add evidence' });
  await expect(add).toBeVisible();
  await add.click();
  await expect(page.locator('#import-title')).toHaveText('Import files');
});

test('TLPAR-04: Refresh timeline is disabled during the first read, repeats the request, and reads the chip\'s new window', async ({ page }) => {
  await delayFirstTrace(page, 1500);
  const traces = countTraces(page);
  await openTimeline(page);

  // While the first read is in flight the button cannot be pressed; once the chart is back it can.
  const refresh = sourcesRow(page).getByRole('button', { name: 'Refresh timeline' });
  await expect(refresh).toBeVisible();
  await expect(refresh).toBeDisabled();
  await expect(page.locator('.tracing-chart')).toBeVisible();
  await expect(refresh).toBeEnabled();
  expect(traces.count()).toBe(1);
  const firstBody = traces.bodies[0];

  // A click sends exactly one more POST with the same windows and sources.
  const sent = page.waitForRequest(isTrace);
  const answered = page.waitForResponse((r) => isTrace(r.request()));
  await refresh.click();
  const again = sentBody(await sent);
  expect(traces.count(), 'one click, one more trace').toBe(2);
  expect(again.incident).toEqual(firstBody.incident);
  expect(again.reference).toEqual(firstBody.reference);
  expect(again.sourceIds ?? []).toEqual(firstBody.sourceIds ?? []);
  await answered;

  // A chip changes the windows: the page says the timeline is stale until Refresh timeline reads the new window.
  const stale = page.locator('[data-testid=tp-stale]');
  await expect(stale).toHaveCount(0);
  await quickChip(page, '4h').click();
  await expect(stale).toBeVisible();
  const sentNew = page.waitForRequest(isTrace);
  const answeredNew = page.waitForResponse((r) => isTrace(r.request()));
  await refresh.click();
  expect(sentBody(await sentNew).incident.start).toBe('2026-10-20T05:00:00.000Z');
  await answeredNew;
  await expect(stale).toBeHidden();
});

test('TLPAR-05: closing the Add evidence drawer reads the timeline again', async ({ page }) => {
  await openTimeline(page);
  await expect(page.locator('.tracing-chart')).toBeVisible();
  const traces = countTraces(page);
  const before = traces.count();

  await sourcesRow(page).getByRole('button', { name: 'Add evidence' }).click();
  await expect(page.locator('#import-title')).toHaveText('Import files');

  // Closing the drawer (Escape, or its Close button when Escape does not close it) makes the timeline read again.
  const again = page.waitForRequest(isTrace);
  await page.keyboard.press('Escape');
  if (await page.locator('#import-title').isVisible()) {
    await page.getByRole('button', { name: 'Close', exact: true }).first().click();
  }
  await expect(page.locator('#import-title')).toBeHidden();
  await again;
  expect(traces.count(), 'the drawer closing reads the timeline again').toBe(before + 1);
  await expect(page.locator('.tracing-chart')).toBeVisible();
});

test('TLPAR-06: screenshots of the timeline page, light and dark, 1280 and 375 px wide', async ({ page }) => {
  mkdirSync(SHOTS, { recursive: true });
  await openTimeline(page);
  await expect(page.locator('.tracing-chart')).toBeVisible();
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const width of [1280, 375]) {
      await page.setViewportSize({ width, height: 900 });
      await expect(quickRow(page)).toBeVisible();
      await expect(sourcesRow(page).getByRole('button', { name: 'Refresh timeline' })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), `no sideways scroll at ${width} px, ${scheme}`).toBe(true);
      await page.screenshot({ path: join(SHOTS, `app-${scheme}-${width}.png`) });
    }
  }
});
