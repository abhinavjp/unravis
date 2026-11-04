// Large windows must never leave a screen feeling stuck. Delayed and hung answers are made with page.route (no big dataset):
//   LW-01  Timeline: a slow read swaps the plain line for the "Still reading" line after about 6 s, and Cancel stays.
//   LW-02  Timeline: a read that passes its time limit says so in plain words, with Try again that reads the window again.
//   LW-03  Daily and hourly view: Cancel while reading, then Try again; both reads start together.
//   LW-04  Daily and hourly view: a read that passes its time limit shows the same sentence and Try again.
// The browser's request time limit is 150 s. LW-02 and LW-04 shorten it to 1.5 s inside the page (AbortSignal.timeout only),
// so the test does not wait two and a half minutes; the code under test is unchanged.
// The demo clock is 2026-10-20T09:00:00Z. Nothing here changes demo data, so the tests may run in any order.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';

const CASE_TITLE = 'Orders API stall, 19 October';
const WIN_A = ['2026-10-19T15:00', '2026-10-19T18:00', '2026-10-19T12:00', '2026-10-19T15:00'] as const;
const TIMEOUT_SENTENCE = 'This is taking longer than expected. Try a shorter window.';
const STILL_READING = 'Still reading. A large window can take a minute. You can cancel.';

async function openTracing(page: Page) {
  await goHome(page);
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect(page.locator('.tracing-chart')).toBeVisible();
}

async function fillWindows(page: Page, w: readonly string[]) {
  const inputs = page.locator('input[type=datetime-local]');
  for (let i = 0; i < 4; i++) await inputs.nth(i).fill(w[i]);
}

/** Shortens the request time limit seen by the page. Only the limit changes; the app code is the same. */
async function shortenTimeLimit(page: Page, ms = 1500) {
  await page.evaluate((limit) => {
    const original = AbortSignal.timeout.bind(AbortSignal);
    AbortSignal.timeout = (asked: number) => original(Math.min(asked, limit));
  }, ms);
}

test('LW-01: the timeline says it is still reading after a few seconds and keeps Cancel', async ({ page }) => {
  await openTracing(page);
  // Never answer: the read hangs until it is cancelled.
  await page.route('**/tracing', () => {});
  await fillWindows(page, WIN_A);
  await page.getByRole('button', { name: 'Start tracing' }).click();

  const veil = page.locator('.chart-veil');
  await expect(veil.getByRole('status')).toHaveText('Reading the window…');
  const cancel = veil.getByRole('button', { name: 'Cancel' });
  await expect(cancel).toBeVisible();
  await expect(cancel).toHaveAttribute('title', 'Stop reading. Nothing is changed.');
  // After about 6 s the second line replaces the first and Cancel is still there.
  await expect(veil.getByRole('status')).toHaveText(STILL_READING, { timeout: 15_000 });
  await expect(cancel).toBeVisible();

  await cancel.click();
  await expect(page.getByText('Cancelled. The chart still shows the previous result.')).toBeVisible();
  await expect(veil).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Start tracing' })).toBeEnabled();
});

test('LW-02: a timeline read that passes its time limit says so and Try again reads it again', async ({ page }) => {
  await openTracing(page);
  await shortenTimeLimit(page);
  await page.route('**/tracing', () => {});
  await fillWindows(page, WIN_A);
  await page.getByRole('button', { name: 'Start tracing' }).click();

  const panel = page.locator('.error-panel');
  await expect(panel.getByRole('status')).toHaveText(TIMEOUT_SENTENCE, { timeout: 15_000 });
  const retry = panel.getByRole('button', { name: 'Try again' });
  await expect(retry).toHaveAttribute('title', 'Read the same window again.');

  // The server is back: the very same window is read and the chart appears.
  await page.unroute('**/tracing');
  const answered = page.waitForResponse((r) => r.url().includes('/tracing') && r.request().method() === 'POST' && (r.request().postData() ?? '').includes(WIN_A[0] + ':00.000Z'));
  await retry.click();
  await answered;
  await expect(page.locator('.tracing-chart')).toBeVisible();
  await expect(panel).toHaveCount(0);
});

test('LW-03: the daily and hourly view starts both reads together, can be cancelled, and can be tried again', async ({ page, allow }) => {
  allow(/Failed to load resource.*500/, 'a failed daily read is provoked on purpose');
  const seen: string[] = [];
  page.on('request', (r) => {
    const path = new URL(r.url()).pathname;
    if (path === '/api/daily' || path === '/api/hourgrid') seen.push(path);
  });
  // The daily read hangs; the hour grid is allowed through.
  await page.route(/\/api\/daily\b/, () => {});
  await page.getByRole('button', { name: /^Daily and hourly view/ }).click();

  const status = page.locator('.rollup-status');
  await expect(status).toContainText('Reading the sources…');
  const cancel = status.getByRole('button', { name: 'Cancel' });
  await expect(cancel).toHaveAttribute('title', 'Stop reading. Nothing is changed.');
  expect(seen, 'both reads were started without waiting for the first').toEqual(expect.arrayContaining(['/api/daily', '/api/hourgrid']));
  await expect(status).toContainText(STILL_READING, { timeout: 15_000 });
  await expect(cancel).toBeVisible();

  await cancel.click();
  await expect(status).toContainText('Cancelled. Choose Try again to read the sources again.');
  await expect(status.getByRole('button', { name: 'Reading' })).toHaveCount(0);

  // A failed read shows its message with Try again beside it.
  await page.unroute(/\/api\/daily\b/);
  await page.route(/\/api\/daily\b/, (route) => route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'The daily read failed.' }) }));
  await status.getByRole('button', { name: 'Try again' }).click();
  const alert = page.locator('.rollup-body [role=alert]').filter({ hasText: 'The daily read failed.' });
  await expect(alert).toBeVisible();
  const retry = alert.getByRole('button', { name: 'Try again' });
  await expect(retry).toHaveAttribute('title', 'Read the same window again.');

  // The server is back: Try again fills the view.
  await page.unroute(/\/api\/daily\b/);
  await retry.click();
  await expect(page.getByRole('heading', { name: 'Each day' })).toBeVisible();
  await expect(alert).toHaveCount(0);
});

test('LW-04: a daily read that passes its time limit shows the plain sentence with Try again', async ({ page }) => {
  await shortenTimeLimit(page);
  await page.route(/\/api\/daily\b/, () => {});
  await page.getByRole('button', { name: /^Daily and hourly view/ }).click();

  const alert = page.locator('.rollup-body [role=alert]').filter({ hasText: TIMEOUT_SENTENCE });
  await expect(alert).toBeVisible({ timeout: 15_000 });
  await page.unroute(/\/api\/daily\b/);
  await alert.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('heading', { name: 'Each day' })).toBeVisible();
});
