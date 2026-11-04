// The notices for a read that ran out of time and returned only part of its answer. The answers are made with page.route
// (the demo data never times out), so the code under test is the page, not the server.
//   PART-01  Daily view: partial:true with daysPlanned shows "Showing N of M days", keeps the finished days drawn, and Try again reads again.
//   PART-02  Patterns drawer: a partial step-change read shows the same notice and Try again runs the search again.
//   PART-03  Timeline: a lane with reasonCode timeout shows the one "cut short" line.
// Nothing here changes demo data, so the tests may run in any order.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { fillWindows } from '../support/window-picker';

const CASE_TITLE = 'Orders API stall, 19 October';
const WIN_B = ['2026-10-19T08:00', '2026-10-19T12:00', '2026-10-19T04:00', '2026-10-19T08:00'] as const;
const CUT_WARNING = 'The read ran out of time after 3 days; only the oldest finished days are shown.';

const day = (date: string, count: number) => ({ date, start: date + 'T00:00:00Z', end: date + 'T23:59:59Z', count, p50Ms: 120, p95Ms: 340 + count });

test('PART-01: a daily read cut short says how many days it shows, keeps them drawn, and Try again reads again', async ({ page }) => {
  let reads = 0;
  await page.route(/\/api\/endpoint-daily\b/, (route) => {
    reads++;
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        operation: 'Payroll.Run', zone: 'UTC', days: [day('2026-10-15', 40), day('2026-10-16', 55), day('2026-10-17', 48)],
        warnings: [CUT_WARNING], partial: true, daysPlanned: 30
      })
    });
  });

  await page.getByRole('button', { name: 'Where is it?', exact: true }).click();
  const table = page.locator('details').filter({ has: page.locator('summary', { hasText: /All 8 API groups/ }) });
  await table.locator('summary').click();
  await table.getByRole('button', { name: /^Daily view: / }).first().click();

  const daily = page.locator('section.endpoint-daily');
  const notice = daily.locator('[data-testid=daily-partial]');
  await expect(notice).toBeVisible();
  await expect(notice).toContainText('Showing 3 of 30 days; the read ran out of time. Shorten the window or Try again.');
  // The server's own cut warning is replaced by the notice, not shown beside it.
  await expect(daily.getByText(CUT_WARNING)).toHaveCount(0);
  // The finished days stay drawn.
  await expect(daily.locator('tbody tr')).toHaveCount(3);
  await expect(daily.locator('svg.daily-chart')).toBeVisible();
  expect(reads).toBe(1);

  const retry = notice.getByRole('button', { name: 'Try again' });
  await expect(retry).toHaveAttribute('title', 'Read the same window again.');
  await retry.click();
  await expect.poll(() => reads).toBe(2);
  await expect(notice).toBeVisible();
  await expect(daily.locator('tbody tr')).toHaveCount(3);

  // A complete answer takes the notice away.
  await page.unroute(/\/api\/endpoint-daily\b/);
  await page.route(/\/api\/endpoint-daily\b/, (route) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ operation: 'Payroll.Run', zone: 'UTC', days: [day('2026-10-15', 40), day('2026-10-16', 55), day('2026-10-17', 48)], warnings: [] })
  }));
  await retry.click();
  await expect(notice).toHaveCount(0);
  await expect(daily.locator('tbody tr')).toHaveCount(3);
});

test('PART-02: a step-change read cut short shows the notice in the Patterns drawer and Try again runs the search again', async ({ page }) => {
  let runs = 0;
  // The real answer is read, then the step-change part is replaced by a cut-short one.
  await page.route(/\/api\/patterns\b/, async (route) => {
    runs++;
    const response = await route.fetch();
    const report = await response.json();
    report.stepChange = {
      zone: 'UTC', start: '2026-09-20T00:00:00Z', end: '2026-10-20T00:00:00Z', daysRead: 3, daysLeftOut: 0, partial: true,
      operations: [{ operation: 'Payroll.Run', volume: 120, daysUsed: 3, found: false, reason: 'Too few days.' }]
    };
    await route.fulfill({ response, json: report });
  });

  await page.getByRole('button', { name: 'Patterns', exact: true }).click();
  const drawer = page.getByRole('dialog').or(page.locator('.patterns-drawer')).first();
  await expect(drawer).toBeVisible();
  await page.getByLabel('Also look for a step in daily response times').check();
  await page.getByRole('button', { name: 'Look for patterns' }).click();

  const notice = page.locator('[data-testid=step-partial]');
  await expect(notice).toBeVisible();
  await expect(notice).toContainText(/^Showing 3 of \d+ days; the read ran out of time\. Shorten the window or Try again\./);
  expect(runs).toBe(1);

  await notice.getByRole('button', { name: 'Try again' }).click();
  await expect.poll(() => runs).toBe(2);
  await expect(notice).toBeVisible();
});

test('PART-03: a lane the server cut short with reasonCode timeout shows the cut-short line on the Investigation Timeline', async ({ page }) => {
  await goHome(page);
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect(page.locator('.tracing-chart')).toBeVisible();
  await expect(page.locator('[data-testid=lanes-cut-short]')).toHaveCount(0);

  // The next trace is answered as usual, except the audit lane says it ran out of time.
  await page.route('**/tracing', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    const audit = body.lanes.find((lane: { key: string }) => lane.key === 'audit');
    audit.reasonCode = 'timeout';
    await route.fulfill({ response, json: body });
  });
  await fillWindows(page, WIN_B);
  const answered = page.waitForResponse((r) => r.url().includes('/tracing') && r.request().method() === 'POST' && (r.request().postData() ?? '').includes(WIN_B[0] + ':00.000Z'));
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await answered;

  const line = page.locator('[data-testid=lanes-cut-short]');
  await expect(line).toBeVisible();
  await expect(line).toContainText('Some lanes were not read in full. What was read is drawn; the rest is marked not read.');
  // It also says which lane, and what was read of it.
  await expect(line).toContainText('API requests (audit log)');
  // The other lanes are still drawn.
  await expect(page.locator('.panel[data-lane=sql]')).toBeVisible();
  await expect(page.locator('.panel[data-lane=iis]')).toBeVisible();
});
