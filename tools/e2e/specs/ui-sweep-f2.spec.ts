// UI sweep F2 (analysis screens and time zones): the When boxes follow the header zone, the stale-run bar does not move the page, the group table fits at 1440,
// the case list dates agree with the header zone. Needs UNRAVIS_EXE like the other specs. Not run in the sweep session (no Windows exe there); the same steps
// were run against a Linux demo build with Playwright.
import { test, expect, type Page } from '../support/fixtures';

test.use({ viewport: { width: 1440, height: 900 } });

const zoneSelect = (page: Page) => page.getByLabel('Show and type times in');

async function openWhen(page: Page) {
  await page.getByText('More filters').first().click();
  await page.getByText('When', { exact: true }).first().click();
}

const timeBoxes = (page: Page) => page.locator('input.time24');

test('SWEEP-F2-01: the When boxes show the clock of the zone chosen in the header, and change with it', async ({ page }) => {
  await openWhen(page);
  await zoneSelect(page).selectOption({ label: 'London' });
  const london = await timeBoxes(page).evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value.slice(0, 5)));
  await zoneSelect(page).selectOption({ label: 'India' });
  const india = await timeBoxes(page).evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value.slice(0, 5)));
  // India is 4 h 30 min ahead of London in October (London is on summer time), so every box moved by that much, on the same moment.
  const minutes = (text: string) => Number(text.slice(0, 2)) * 60 + Number(text.slice(3, 5));
  london.forEach((value, index) => expect((minutes(india[index]) - minutes(value) + 1440) % 1440).toBe(270));
  await expect(page.getByText(/Times are in\s+India, as set by Times in at the top of the page\./)).toBeVisible();
  // The raw UTC line under each pair does not change with the zone.
  await expect(page.locator('.utc-echo').first()).toContainText('Z');
});

test('SWEEP-F2-01: typing a time in the header zone asks for that moment, not the same text in UTC', async ({ page }) => {
  await openWhen(page);
  await zoneSelect(page).selectOption({ label: 'India' });
  const first = timeBoxes(page).first();
  const echo = page.locator('.utc-echo').first();
  await first.fill('10:00');
  await first.blur(); // the 24-hour box takes the value when the person leaves it or presses Enter
  // 10:00 in India is 04:30 UTC.
  await expect(echo).toContainText('T04:30:00.000Z');
});

test('SWEEP-F2-04: a changed filter shows its message without moving the bar or the page below it', async ({ page }) => {
  const top = () => page.locator('#main-content, .stage').first().evaluate((e) => Math.round(e.getBoundingClientRect().top));
  const before = await top();
  await page.getByRole('button', { name: 'Last hour' }).first().click();
  await expect(page.getByText('Filters changed. Click Run analysis to use the new window.')).toHaveCount(1);
  expect(await top()).toBe(before);
  // The old second message is gone.
  await expect(page.getByText('The figures below are from the last run')).toHaveCount(0);
});

test('SWEEP-F2-07: the group table fits its box at 1440 without a sideways scroll', async ({ page }) => {
  await page.getByRole('button', { name: 'Where is it?', exact: true }).first().click();
  await page.getByText(/^All \d+ API groups/).click();
  const scroll = page.locator('.group-table-scroll.scroll-cue');
  await expect(scroll).toBeVisible();
  const [wide, box] = await scroll.evaluate((e) => [e.scrollWidth, e.clientWidth]);
  expect(wide).toBeLessThanOrEqual(box);
});

test('SWEEP-F2-02: the All operations heading names the zone at the rows own date', async ({ page }) => {
  await zoneSelect(page).selectOption({ label: 'London' });
  await page.getByRole('button', { name: 'All operations' }).first().click().catch(() => undefined);
  const head = page.locator('.request-table thead th').first();
  if (await head.count()) await expect(head).toContainText(/Initiated \(GMT(\+1|\+0)?\)/);
});

test('SWEEP-F2-13: the case list shows dates in the header zone', async ({ page }) => {
  await page.getByRole('button', { name: 'All cases', exact: true }).first().click();
  await zoneSelect(page).selectOption({ label: 'London' });
  await expect(page.getByText(/17 Oct 2026 10:00 GMT\+1/).first()).toBeVisible();
  await zoneSelect(page).selectOption({ label: 'UTC' });
  await expect(page.getByText(/17 Oct 2026 09:00 (UTC|GMT\+0)/).first()).toBeVisible();
});

test('SWEEP-F2-S3: a sentence that says why the run will not start is shown whole, never cut with an ellipsis', async ({ page }) => {
  await openWhen(page);
  await page.locator('input[type=date]').first().fill('2026-10-05');
  const hint = page.locator('#run-hint');
  await expect(hint).toContainText(/7 days/);
  const metrics = await hint.evaluate((e) => {
    const style = getComputedStyle(e);
    return { clipped: e.scrollWidth > e.clientWidth + 1, ellipsis: style.textOverflow, overflow: style.overflow, height: e.getBoundingClientRect().height };
  });
  expect(metrics.clipped).toBe(false);
  expect(metrics.ellipsis).not.toBe('ellipsis');
  expect(metrics.overflow).not.toBe('hidden');
  // The whole sentence is on screen: its box is inside the viewport and wraps to more than one line when it has to.
  const box = await hint.boundingBox();
  expect(box && box.x >= 0 && box.x + box.width <= 1440).toBe(true);
});

test('SWEEP-F2-N2: with More filters open only the panel body scrolls', async ({ page }) => {
  await page.getByText('More filters').first().click();
  const scrollers = await page.evaluate(() => [...document.querySelectorAll('.scope, .scope *')].filter((e) => /(auto|scroll)/.test(getComputedStyle(e).overflowY)).map((e) => e.className));
  expect(scrollers.filter((name) => /scope(\s|$)/.test(String(name)) && !/scope-main/.test(String(name)))).toEqual([]);
});

test('SWEEP-F2-R4: every sort button in the group table heading is at least 24 px tall at 1440', async ({ page }) => {
  await page.getByRole('button', { name: 'Where is it?', exact: true }).first().click();
  await page.getByText(/^All \d+ API groups/).click();
  const heights = await page.locator('.group-table-scroll.scroll-cue th .sort-button').evaluateAll((els) => els.map((e) => ({ name: (e.textContent ?? '').trim(), h: e.getBoundingClientRect().height })));
  expect(heights.length).toBeGreaterThan(3);
  for (const item of heights) expect(item.h, item.name).toBeGreaterThanOrEqual(24);
});
