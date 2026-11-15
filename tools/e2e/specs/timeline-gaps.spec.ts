// The Investigation Timeline against the tracing-paper wireframe, the last gaps: one controls card, the UTC readout under the pickers,
// 12-hour time boxes, "Each bar = ..." under the chart, and the Suspects rail. The demo clock is 2026-10-20T09:00Z, so the default
// incident window is 08:00-09:00 UTC and the default comparison period 07:00-08:00; the demo audit lane (read live) rises at 08:30.
//   TLGAP-01  The quick ranges, both windows, the UTC readout, the Sources traced row, Add evidence and Start tracing are in ONE card;
//             the chart is outside it; the readout sits under the windows and above the sources row.
//   TLGAP-02  The UTC readout names both periods in UTC, follows a quick range and a typed window, and sends no trace by itself.
//   TLGAP-03  The time boxes are 12-hour (hour 1-12, minute, AM or PM): 22:30 is 10 : 30 PM, the readout and the button follow.
//   TLGAP-04  The chart says what one bar covers ("Each bar = 1 minute") and the figure follows the window.
//   TLGAP-05  The Suspects rail: beside the chart at 1600 (the breakpoint is 1480 so the plot keeps its width), below it at 1280 and 375; legend, summary, pinned card, orange L1, grey cards say what
//             they need, evidence opens by keyboard, See all and Show fewer, ruled-out details.
//   TLGAP-06  Show on chart selects the bar, shows its text and focuses the bucket selector, and sends no second trace; arrow keys go on.
//   TLGAP-10  A rise in the saved SQL day's average time makes a card dated by call time ("time first rose at"), and a flat average rules D1 out.
//   TLGAP-07  A rise in the case-stored SQL lane (answer reshaped in the browser) makes a card with a SQL moment and a Show on chart button.
//   TLGAP-08  Honest empty states: a window with no events says so, and clearing every source hides the rail.
//   TLGAP-09  Screenshots for the review: light and dark at 1280 and 375 px wide, written to .dev/tlgap-shots; no sideways scroll, header included.
import { test, expect, type Locator, type Page, type Request } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { fillWindows, openPicker, pickerButton, pickerDialog, pickerTime, readTime, setWindow, fillTime } from '../support/window-picker';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

const CASE_TITLE = 'Orders API stall, 19 October';
const SHOTS = join(__dirname, '..', '..', '..', '.dev', 'tlgap-shots');
// A day with events in all three lanes (the demo's saved SQL and web-server logs sit at 19 Oct 09:10).
const WIN_ALL = ['2026-10-19T09:00', '2026-10-19T10:00', '2026-10-19T08:00', '2026-10-19T09:00'] as const;
const WIN_EMPTY = ['2026-09-01T00:00', '2026-09-01T01:00', '2026-08-31T23:00', '2026-09-01T00:00'] as const;

async function openTimeline(page: Page): Promise<void> {
  await goHome(page);
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect(page.locator('.tracing-chart')).toBeVisible();
}

const isTrace = (r: Request) => r.url().includes('/tracing') && r.method() === 'POST';
function countTraces(page: Page): { count: () => number } {
  let n = 0;
  page.on('request', (r) => { if (isTrace(r)) n++; });
  return { count: () => n };
}

const card = (page: Page) => page.locator('section.card.trctl');
const rail = (page: Page) => page.getByTestId('sus-rail');
const suspect = (page: Page, id: string) => rail(page).locator(`[data-sus="${id}"]`);
const refresh = (page: Page) => page.getByTestId('trace-sources').getByRole('button', { name: 'Start tracing' });

/** Presses Start tracing and waits for the answer. */
async function refreshAndWait(page: Page): Promise<void> {
  const answered = page.waitForResponse((r) => isTrace(r.request()));
  await refresh(page).click();
  await answered;
  await expect(page.locator('.tracing-chart')).toBeVisible();
}

/** The page never scrolls sideways and the header stays inside the window. */
async function noSideScroll(page: Page, message: string): Promise<void> {
  await expect.poll(() => page.evaluate(() => new Promise<string>((done) => requestAnimationFrame(() => requestAnimationFrame(() => {
    const wide = [...document.querySelectorAll('header *')].filter((el) => el.getBoundingClientRect().right > window.innerWidth + 1 && el.getBoundingClientRect().width > 0).map((el) => el.tagName + '.' + el.className).slice(0, 5);
    done(document.documentElement.scrollWidth <= window.innerWidth + 1 && wide.length === 0 ? '' : `scrollWidth ${document.documentElement.scrollWidth} of ${window.innerWidth}; header parts past the edge: ${wide.join(', ')}`);
  })))), { message, timeout: 5000 }).toBe('');
}

test('TLGAP-01: the quick ranges, the windows, the readout and the Sources traced row are one card; the chart is not in it', async ({ page }) => {
  await openTimeline(page);
  await expect(card(page)).toHaveCount(1);
  await expect(card(page).getByTestId('tp-quick-row')).toBeVisible();
  await expect(pickerButton(page, 'incident')).toBeVisible();
  await expect(card(page).getByRole('button', { name: /^Incident window: / })).toBeVisible();
  await expect(card(page).getByRole('button', { name: /^Comparison period: / })).toBeVisible();
  await expect(card(page).getByTestId('tp-metaline')).toBeVisible();
  await expect(card(page).getByTestId('trace-sources')).toBeVisible();
  await expect(card(page).getByRole('button', { name: 'Add evidence' })).toBeVisible();
  await expect(card(page).getByRole('button', { name: 'Start tracing' })).toBeVisible();
  await expect(card(page).locator('.tracing-chart')).toHaveCount(0);
  // Top to bottom: quick range, windows, readout, sources row.
  const y = async (loc: Locator) => (await loc.boundingBox())!.y;
  const ys = [await y(card(page).getByTestId('tp-quick-row')), await y(pickerButton(page, 'incident')), await y(card(page).getByTestId('tp-metaline')), await y(card(page).getByTestId('trace-sources'))];
  expect(ys, 'quick range, windows, readout, sources row run top to bottom').toEqual([...ys].sort((a, b) => a - b));
  // The sources row sits inside the card's box.
  const cardBox = (await card(page).boundingBox())!;
  const rowBox = (await card(page).getByTestId('trace-sources').boundingBox())!;
  expect(rowBox.y).toBeGreaterThanOrEqual(cardBox.y);
  expect(rowBox.y + rowBox.height).toBeLessThanOrEqual(cardBox.y + cardBox.height + 1);
  expect(rowBox.x).toBeGreaterThanOrEqual(cardBox.x);
  expect(rowBox.x + rowBox.width).toBeLessThanOrEqual(cardBox.x + cardBox.width + 1);
  // At 375 px the Sources traced box keeps the card's own gutter: it does not stick out or sit further in than the other rows.
  await page.setViewportSize({ width: 375, height: 900 });
  await noSideScroll(page, 'no sideways scroll at 375 px');
  const narrowCard = (await card(page).boundingBox())!;
  const narrowRow = (await card(page).getByTestId('trace-sources').boundingBox())!;
  const narrowQuick = (await card(page).getByTestId('tp-quick-row').boundingBox())!;
  expect(narrowRow.x + narrowRow.width, 'the sources row stays in the card').toBeLessThanOrEqual(narrowCard.x + narrowCard.width + 1);
  expect(Math.abs(narrowRow.x - narrowQuick.x), 'the sources row lines up with the quick range row').toBeLessThanOrEqual(2);
});

test('TLGAP-02: the UTC readout names both periods, follows a quick range and a typed window, and sends no trace by itself', async ({ page }) => {
  await openTimeline(page);
  const traces = countTraces(page);
  const echo = card(page).getByTestId('tp-metaline');
  await expect(echo).toContainText('UTC 2026-10-20T08:00:00Z – 2026-10-20T09:00:00Z');
  await expect(echo).toContainText('compared with');
  await expect(echo).toContainText('UTC 2026-10-20T07:00:00Z – 2026-10-20T08:00:00Z');
  await expect(echo).toContainText('Times are in');
  await expect(echo).toContainText('stores UTC');
  await card(page).locator('.chip[data-quick-id="4h"]').click();
  await expect(card(page).getByTestId('utc-echo-incident')).toHaveText('UTC 2026-10-20T05:00:00Z – 2026-10-20T09:00:00Z');
  await expect(card(page).getByTestId('utc-echo-reference')).toHaveText('UTC 2026-10-20T01:00:00Z – 2026-10-20T05:00:00Z');
  await setWindow(page, 'incident', '2026-10-19T22:30', '2026-10-20T01:30');
  await expect(card(page).getByTestId('utc-echo-incident')).toHaveText('UTC 2026-10-19T22:30:00Z – 2026-10-20T01:30:00Z');
  expect(traces.count(), 'a quick range and a picker never read by themselves').toBe(0);
});

test('TLGAP-03: the time boxes are 12-hour (hour, minute, AM or PM) and the window, readout and button follow', async ({ page }) => {
  await openTimeline(page);
  const dialog = await openPicker(page, 'incident');
  const startBox = pickerTime(page, 'incident', 'start time');
  await expect(startBox.getByRole('combobox')).toHaveCount(3);
  const hour = startBox.getByRole('combobox', { name: 'Incident window start time hour', exact: true });
  const minute = startBox.getByRole('combobox', { name: 'Incident window start time minute', exact: true });
  const period = startBox.getByRole('combobox', { name: 'Incident window start time AM or PM', exact: true });
  const hourOptions = await hour.locator('option').evaluateAll((els) => els.map((el) => (el as HTMLOptionElement).value).filter((v) => v !== ''));
  expect(hourOptions).toEqual(['1', '2', '3', '4', '5', '6', '7', '8', '9', '10', '11', '12']);
  expect((await minute.locator('option').evaluateAll((els) => els.map((el) => (el as HTMLOptionElement).value).filter((v) => v !== ''))).length).toBe(60);
  expect(await period.locator('option').evaluateAll((els) => els.map((el) => (el as HTMLOptionElement).value).filter((v) => v !== ''))).toEqual(['AM', 'PM']);
  // 08:00 is 8 : 00 AM; 22:30 is 10 : 30 PM; 00:15 is 12 : 15 AM; 12:00 is 12 : 00 PM.
  expect([await hour.inputValue(), await minute.inputValue(), await period.inputValue()]).toEqual(['8', '0', 'AM']);
  for (const [hhmm, shown] of [['22:30', ['10', '30', 'PM']], ['00:15', ['12', '15', 'AM']], ['12:00', ['12', '0', 'PM']]] as const) {
    await fillTime(page, 'incident', 'start time', hhmm);
    expect([await hour.inputValue(), await minute.inputValue(), await period.inputValue()]).toEqual(shown);
    expect(await readTime(page, 'incident', 'start time')).toBe(hhmm);
  }
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog).toBeHidden();
  await setWindow(page, 'incident', '2026-10-19T22:30', '2026-10-20T01:30');
  await expect(pickerButton(page, 'incident')).toContainText('22:30');
  await expect(card(page).getByTestId('utc-echo-incident')).toHaveText('UTC 2026-10-19T22:30:00Z – 2026-10-20T01:30:00Z');
  // The box is a keyboard control: a drop-down takes the arrow keys and Escape still closes the picker.
  await openPicker(page, 'incident');
  await pickerTime(page, 'incident', 'end time').getByRole('combobox', { name: 'Incident window end time AM or PM', exact: true }).focus();
  await page.keyboard.press('Escape');
  await expect(pickerDialog(page, 'incident')).toBeHidden();
});

test('TLGAP-04: the chart says what one bar covers, and the figure follows the window', async ({ page }) => {
  await openTimeline(page);
  const line = page.getByTestId('bar-size');
  await expect(line).toBeVisible();
  await expect(line).toContainText('Each bar = 1 minute');
  await expect(line).toContainText('60 bars on the axis');
  await card(page).locator('.chip[data-quick-id="7d"]').click();
  await refreshAndWait(page);
  await expect(line).toContainText(/^Each bar = \d+ (minutes?|hours?)/);
  await expect(line).not.toContainText('Each bar = 1 minute');
  await expect(line).toContainText('longer periods group more time into each bar');
});

test('TLGAP-05: the Suspects rail sits beside the chart at 1600 (below it at 1280 and at 375), with the wireframe pieces', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await openTimeline(page);
  await expect(rail(page)).toBeVisible();
  await expect(rail(page).getByRole('heading', { name: 'Suspects', level: 3 })).toBeVisible();
  await expect(rail(page)).toContainText('Suspects, not causes.');
  await expect(page.getByTestId('sus-scope')).toContainText('event counts for each lane');
  await expect(page.getByTestId('sus-nored')).toContainText('nothing is red here');
  await expect(rail(page).getByRole('list', { name: 'What the colours mean' }).locator('li')).toHaveCount(4);
  await expect(page.getByTestId('sus-summary')).toHaveText(/^Checked 26 suspects: 0 red, 1 orange, \d+ grey, \d+ ruled out \(green\)\.$/);
  // Beside the chart: to the right of the controls card, in a column of about 380 px.
  const railBox = (await rail(page).boundingBox())!;
  const cardBox = (await card(page).boundingBox())!;
  expect(railBox.x, 'the rail is right of the controls card').toBeGreaterThanOrEqual(cardBox.x + cardBox.width);
  expect(railBox.width).toBeGreaterThanOrEqual(340);
  expect(railBox.width).toBeLessThanOrEqual(381);
  // The data-check card is pinned first; L1 is orange (the live audit lane rises); grey cards say what they need.
  await expect(rail(page).locator('[data-sus]').first()).toHaveAttribute('data-sus', 'Q2');
  await expect(suspect(page, 'Q2')).toContainText('Data check');
  await expect(suspect(page, 'L1')).toHaveAttribute('data-colour', 'orange');
  await expect(suspect(page, 'L1')).toContainText('Orange · Consistent.');
  await expect(suspect(page, 'L1')).toContainText('first rose at');
  const greyCard = rail(page).locator('[data-colour="grey"]').nth(1);
  await expect(greyCard).toContainText('Cannot test yet');
  await expect(greyCard.locator('.susneeds')).toContainText('Needs:');
  // Evidence opens from the keyboard and shows the table, the cheapest check and what would overturn it.
  const summary = suspect(page, 'L1').locator('summary');
  await summary.focus();
  await page.keyboard.press('Enter');
  await expect(suspect(page, 'L1').locator('details')).toHaveAttribute('open', '');
  await expect(suspect(page, 'L1').getByRole('columnheader')).toHaveText(['Signal', 'Role', 'Reading', 'Why']);
  await expect(suspect(page, 'L1')).toContainText('Cheapest check:');
  await expect(suspect(page, 'L1')).toContainText('What would overturn it:');
  // See all lists every suspect it can read, with the ruled-out ones in their own details; Show fewer puts it back.
  const all = page.getByTestId('sus-all');
  await expect(all).toHaveAttribute('aria-expanded', 'false');
  const before = await rail(page).locator('[data-sus]').count();
  await all.focus();
  await page.keyboard.press('Enter');
  await expect(all).toHaveAttribute('aria-expanded', 'true');
  await expect(all).toBeFocused();
  await expect(all).toHaveText('Show fewer');
  expect(await rail(page).locator('[data-sus]').count()).toBeGreaterThan(before);
  expect(await rail(page).locator('article[data-sus]').count()).toBe(26);
  await expect(page.getByTestId('sus-ruledout')).toBeVisible();
  await expect(page.getByTestId('sus-ruledout').locator(':scope > summary')).toContainText(/Ruled out by the data \(\d+\)/);
  await expect(page.getByTestId('sus-notshown')).toContainText('more checks in the full Unravis catalogue');
  await page.keyboard.press('Enter');
  await expect(all).toHaveAttribute('aria-expanded', 'false');
  expect(await rail(page).locator('[data-sus]').count()).toBe(before);
  // 375 px: the rail follows the chart, full width of the page column.
  await page.setViewportSize({ width: 375, height: 900 });
  await noSideScroll(page, 'no sideways scroll at 375 px');
  const narrowRail = (await rail(page).boundingBox())!;
  const chartBox = (await page.locator('.tracing-chart').boundingBox())!;
  expect(narrowRail.y, 'the rail is below the chart').toBeGreaterThanOrEqual(chartBox.y + chartBox.height - 1);
  expect(narrowRail.width).toBeGreaterThan(300);
});

test('TLGAP-06: Show on chart selects the bar, shows its text, focuses the selector and sends no second trace; the arrow keys go on', async ({ page }) => {
  await openTimeline(page);
  const traces = countTraces(page);
  const button = page.getByRole('button', { name: 'Show L1 on the chart', exact: true });
  await expect(button).toBeVisible();
  await button.focus();
  await page.keyboard.press('Enter');
  const overlay = page.locator('.tracing-chart .overlay');
  await expect(overlay).toBeFocused();
  const tip = page.locator('#tc-tip');
  await expect(tip).toBeVisible();
  await expect(tip).toContainText('API requests');
  // The bar the rail names: the first half-hour boundary of the default window (08:30 is bar 31 of 60), one bar per minute.
  const first = await tip.innerText();
  await page.keyboard.press('ArrowRight');
  await expect.poll(async () => tip.innerText()).not.toBe(first);
  // The chart is brought into view.
  const box = (await overlay.boundingBox())!;
  const viewport = page.viewportSize()!;
  expect(box.y + box.height).toBeGreaterThan(0);
  expect(box.y).toBeLessThan(viewport.height);
  // A second press of the same button asks again (zooming back out first when a zoom hid the bar) and still sends nothing.
  await page.getByRole('button', { name: 'Show L1 on the chart', exact: true }).click();
  await expect(overlay).toBeFocused();
  expect(traces.count(), 'the rail reads the answer already held and sends no trace').toBe(0);
});

test('TLGAP-07: a rise in the case-stored SQL lane makes a card with a SQL moment and Show on chart', async ({ page }) => {
  // The saved SQL log is small, so the answer is reshaped here: the SQL lane gets a quiet first half and a busy second half.
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    const sql = body.lanes.find((lane: any) => lane.key === 'sql');
    if (sql) {
      const start = Date.parse(body.window.axisStart);
      const step = body.window.bucketSeconds * 1000;
      const slots = Math.round((Date.parse(body.window.axisEnd) - start) / step);
      sql.state = 'events';
      sql.buckets = Array.from({ length: slots }, (_, i) => ({ start: new Date(start + i * step).toISOString(), count: i < slots / 2 ? 1 : 6, errors: 0 }));
    }
    await route.fulfill({ response, json: body });
  });
  await openTimeline(page);
  await fillWindows(page, WIN_ALL);
  await refreshAndWait(page);
  await expect(rail(page)).toBeVisible();
  // The SQL suspects are grey (their other readings are not in the counts), so they sit under See all.
  await page.getByTestId('sus-all').click();
  const moment = rail(page).locator('.susmoment', { hasText: 'Database (SQL Server) count first rose at' });
  await expect(moment.first()).toBeVisible();
  const owner = rail(page).locator('article[data-sus]', { has: page.locator('.susmoment', { hasText: 'Database (SQL Server)' }) }).first();
  await expect(owner).toHaveAttribute('data-colour', /orange|green|grey/);
  const traces = countTraces(page);
  await owner.getByRole('button', { name: /^Show .+ on the chart$/ }).click();
  await expect(page.locator('.tracing-chart .overlay')).toBeFocused();
  await expect(page.locator('#tc-tip')).toContainText('Database (SQL Server)');
  expect(traces.count()).toBe(0);
});

test('TLGAP-10: a rise in the saved SQL day average time is dated by call time, and a flat one rules the database out', async ({ page }) => {
  let rising = true;
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    const sql = body.lanes.find((lane: any) => lane.key === 'sql');
    if (sql) {
      const start = Date.parse(body.window.axisStart);
      const step = body.window.bucketSeconds * 1000;
      const slots = Math.round((Date.parse(body.window.axisEnd) - start) / step);
      sql.state = 'events';
      sql.buckets = Array.from({ length: slots }, (_, i) => ({
        start: new Date(start + i * step).toISOString(), count: 40, errors: 0, timedCount: 40,
        avgMs: rising && i >= slots / 2 ? 120 : 20, maxMs: 300
      }));
    }
    await route.fulfill({ response, json: body });
  });
  await openTimeline(page);
  await fillWindows(page, WIN_ALL);
  await refreshAndWait(page);
  await page.getByTestId('sus-all').click();
  await expect(rail(page).locator('.susmoment', { hasText: 'Database (SQL Server) time first rose at' }).first()).toBeVisible();
  rising = false;
  await refreshAndWait(page);
  if ((await page.getByTestId('sus-all').getAttribute('aria-expanded')) !== 'true') await page.getByTestId('sus-all').click();
  await expect(rail(page).locator('.susmoment', { hasText: 'Database (SQL Server) time first rose at' })).toHaveCount(0);
});

test('TLGAP-08: a window with no events says so in the rail, and clearing every source hides the rail', async ({ page }) => {
  await openTimeline(page);
  await fillWindows(page, WIN_EMPTY);
  await refreshAndWait(page);
  await expect(rail(page)).toBeVisible();
  await expect(page.getByTestId('sus-empty')).toContainText('No lane has events in this window');
  await expect(rail(page).locator('article[data-colour="orange"], article[data-colour="red"]')).toHaveCount(0);
  await expect(rail(page).getByRole('button', { name: /^Show .+ on the chart$/ })).toHaveCount(0);
  await expect(page.getByTestId('sus-summary')).toContainText('0 red, 0 orange');
  // Nothing selected: no read, no rail.
  const more = page.getByTestId('sources-more');
  if (!(await more.evaluate((el) => (el as HTMLDetailsElement).open))) await more.locator('summary').click();
  await card(page).getByTestId('trace-sources').getByRole('button', { name: 'Clear all' }).click();
  await expect(rail(page)).toHaveCount(0);
  await card(page).getByTestId('trace-sources').getByRole('button', { name: 'Select all' }).click();
  await expect(page.locator('.tracing-chart')).toBeVisible();
});

test('TLGAP-09: screenshots of the timeline with the rail, light and dark, 1280 and 375 px wide; nothing scrolls sideways', async ({ page }) => {
  mkdirSync(SHOTS, { recursive: true });
  await openTimeline(page);
  await expect(rail(page)).toBeVisible();
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const width of [1280, 375]) {
      await page.setViewportSize({ width, height: 900 });
      await expect(card(page)).toBeVisible();
      await noSideScroll(page, `no sideways scroll at ${width} px, ${scheme}`);
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({ path: join(SHOTS, `timeline-top-${scheme}-${width}.png`) });
      await page.screenshot({ path: join(SHOTS, `timeline-full-${scheme}-${width}.png`), fullPage: true });
      await page.getByRole('button', { name: 'Show L1 on the chart', exact: true }).click();
      await expect(page.locator('#tc-tip')).toBeVisible();
      await page.screenshot({ path: join(SHOTS, `timeline-show-${scheme}-${width}.png`) });
      await rail(page).scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(SHOTS, `timeline-rail-${scheme}-${width}.png`) });
    }
  }
});
