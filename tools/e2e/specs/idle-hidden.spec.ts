// BL-429: a hidden page does no polling, a visible page picks up at once, and the update presence signal is unchanged.
//   IDLE-01  Download progress poll (one a second): runs while visible, sends nothing while hidden, one read at once on show.
//   IDLE-02  Update status read (every ten minutes): none while hidden, one at once on show once the interval has passed.
//   IDLE-03  Page presence ping (BL-168/171): every 45 s while visible and touched; none while hidden; back on show. The rule the program
//            reads ("in use" for two minutes after the last ping) is not touched here: see the Go tests of internal/server.
//   IDLE-04  Nothing comes back by itself: with the page hidden and nobody touching it, a long wait sends no request of any kind.
//   IDLE-05  A chart does not redraw for a size change while hidden, and has the new size as soon as the page is shown.
// The browser cannot really be hidden in headless Chromium, so the tests tell the page what the browser would tell it (visibilityState,
// document.hidden and a visibilitychange event; support/idle.ts). Time on the page is Playwright's clock, so ten minutes take seconds.
import { test, expect, type Page } from '../support/fixtures';
import { installIdleProbe, installVisibility, setVisible, watchRequests, idleState } from '../support/idle';

test.use({ demoOptions: { cases: false } });

const STATUS = 'GET /api/update/status';
const PRESENCE = 'POST /api/ui/presence';

/** Starts the page on the page clock with the update bar in "downloading", and settles it. */
async function open(page: Page, base: string, token: string, preview: string): Promise<void> {
  await installVisibility(page);
  await page.addInitScript((p) => { try { sessionStorage.setItem('unravis.updatePreview', p); } catch { /* none */ } }, preview);
  await page.clock.install();
  await page.goto('about:blank');
  await page.goto(base + '/#t=' + token);
  await page.clock.runFor(3_000);
  await page.mouse.move(210, 210);
}

/** Moves the page clock a minute at a time and gives the answers time to come back between steps. */
async function run(page: Page, minutes: number): Promise<void> {
  for (let m = 0; m < minutes; m++) {
    await page.clock.runFor(60_000);
    await page.waitForTimeout(150);
  }
}

/** Moves the page clock a second at a time: the next read is timed from the end of the last answer, which comes back in real time. */
async function seconds(page: Page, n: number): Promise<void> {
  for (let i = 0; i < n; i++) {
    await page.clock.runFor(1_000);
    await page.waitForTimeout(150);
  }
}

test('IDLE-01: the download progress poll stops while hidden and reads once at once on show', async ({ page, demo }) => {
  await open(page, demo.base, demo.token, 'downloading');
  const requests = watchRequests(page);
  await seconds(page, 5);
  expect(requests.count(STATUS), 'a read a second while visible').toBeGreaterThanOrEqual(4);

  await setVisible(page, false);
  await page.clock.runFor(1_500); // anything already due when the page went away
  await page.waitForTimeout(300);
  requests.reset();
  await run(page, 5);
  expect(requests.total(), 'nothing at all in five hidden minutes: ' + JSON.stringify(requests.by())).toBe(0);

  await setVisible(page, true);
  await page.waitForTimeout(500); // real time only: the page clock is not moved
  expect(requests.count(STATUS), 'one read at once on show, without waiting for a timer').toBe(1);
  await seconds(page, 3);
  expect(requests.count(STATUS), 'and the poll goes on').toBeGreaterThanOrEqual(3);
});

test('IDLE-02: the ten-minute status read does not run while hidden and is made at once on show', async ({ page, demo }) => {
  await open(page, demo.base, demo.token, '');
  const requests = watchRequests(page);
  await setVisible(page, false);
  await run(page, 25);
  expect(requests.count(STATUS), 'no status read while hidden for 25 minutes').toBe(0);
  await setVisible(page, true);
  await page.waitForTimeout(500);
  expect(requests.count(STATUS), 'one at once on show').toBe(1);
  await setVisible(page, false);
  await run(page, 1);
  await setVisible(page, true);
  await page.waitForTimeout(500);
  expect(requests.count(STATUS), 'a tab switch after a minute does not read again').toBe(1);
  await run(page, 10);
  expect(requests.count(STATUS), 'then the normal ten minutes').toBe(2);
});

test('IDLE-03: presence pings while visible and touched, none while hidden, and resumes on show', async ({ page, demo }) => {
  await open(page, demo.base, demo.token, '');
  const requests = watchRequests(page);
  for (let i = 0; i < 4; i++) { await page.mouse.move(220 + i, 220); await run(page, 1); }
  const visible = requests.count(PRESENCE);
  expect(visible, 'about one ping per 45 s over four minutes').toBeGreaterThanOrEqual(4);
  expect(visible).toBeLessThanOrEqual(6);

  await setVisible(page, false);
  requests.reset();
  await run(page, 4);
  expect(requests.count(PRESENCE), 'a hidden page sends no presence').toBe(0);

  await page.mouse.move(300, 300); // the person is back
  await setVisible(page, true);
  await page.waitForTimeout(500);
  expect(requests.count(PRESENCE), 'one ping at once on show').toBe(1);
  await page.mouse.move(310, 310);
  await run(page, 2);
  expect(requests.count(PRESENCE), 'and the 45 s rhythm again').toBeGreaterThanOrEqual(3);
});

test('IDLE-03b: presence rule unchanged: a visible page not touched for five minutes sends none', async ({ page, demo }) => {
  await open(page, demo.base, demo.token, '');
  const requests = watchRequests(page);
  await run(page, 7); // the one touch of open() ages out after five minutes
  const pings = requests.count(PRESENCE);
  expect(pings).toBeGreaterThanOrEqual(5);
  expect(pings).toBeLessThanOrEqual(8);
  requests.reset();
  await run(page, 3);
  expect(requests.count(PRESENCE), 'no touch, no ping').toBe(0);
});

test('IDLE-04: hidden and untouched, a long wait sends no request of any kind', async ({ page, demo }) => {
  await open(page, demo.base, demo.token, 'downloading');
  await setVisible(page, false);
  await page.clock.runFor(2_000);
  await page.waitForTimeout(300);
  const requests = watchRequests(page);
  await run(page, 30);
  expect(requests.total(), JSON.stringify(requests.by())).toBe(0);
});

test('IDLE-05: a chart does not redraw for a size change while hidden, and has the new size on show', async ({ page, demo }) => {
  await installIdleProbe(page);
  await page.goto('about:blank');
  await page.goto(demo.base + '/#t=' + demo.token);
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  await page.getByRole('dialog', { name: 'Blackbox' }).getByRole('button', { name: /^Open status: web-a/ }).first().click();
  await page.getByRole('button', { name: 'Open charts' }).first().click();
  const canvas = page.locator('dialog.bbc[open] canvas').first();
  await expect(canvas).toBeVisible();
  const width = () => canvas.evaluate((c) => (c as HTMLCanvasElement).width);
  const start = await width();

  await setVisible(page, false);
  await page.setViewportSize({ width: 1100, height: 900 });
  await page.waitForTimeout(800);
  expect(await width(), 'no redraw while hidden').toBe(start);

  await setVisible(page, true);
  await expect.poll(width, { message: 'the chart follows the window once shown' }).toBeLessThan(start);
  const state = await idleState(page);
  expect(state.visible).toBe(true);
});
