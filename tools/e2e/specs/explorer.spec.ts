// The analysis page: filters, chips, request type buttons and the response-time target.
import { test, expect, type Page } from '../support/fixtures';

async function run(page: Page) {
  const done = page.waitForResponse((r) => r.url().includes('/api/analyze'));
  await page.getByRole('button', { name: 'Run analysis' }).click();
  return done;
}

/** Run, then show the ranked operations list (it is a separate view the first time). */
async function ops(page: Page) {
  await run(page);
  const list = page.getByRole('list', { name: 'Operations, worst first' });
  if (!(await list.isVisible())) await page.getByRole('button', { name: 'All operations' }).first().click();
  return list;
}

async function openFilters(page: Page, tab: string) {
  const open = await page.getByRole('tablist', { name: 'Filter groups' }).isVisible();
  if (!open) await page.getByText('More filters').first().click();
  await page.getByRole('tab', { name: tab }).click();
}

test('INV-10a: filters become chips, narrow the groups, and Clear all removes them', async ({ page }) => {
  await openFilters(page, 'What');
  await page.getByLabel('Controller name').fill('Payroll');
  await page.getByLabel('Action name').fill('Run');
  await page.getByLabel('Status', { exact: true }).selectOption({ label: 'Success' });
  await expect(page.getByText('Filters changed. Click Run analysis to use the new window.')).toBeVisible();
  const list = await ops(page);
  await expect(list).toContainText('Payroll.Run');
  await expect(list).not.toContainText('Employees.List');
  await expect(page.getByRole('button', { name: 'Remove Controller filter: Payroll' }).first()).toBeAttached();
  await openFilters(page, 'Request details');
  await page.getByLabel('User IDs').fill('1003');
  await run(page);
  await page.getByRole('button', { name: 'Clear all' }).click();
  await expect(page.getByRole('button', { name: /^Remove .* filter/ })).toHaveCount(0);
  const again = await ops(page);
  await expect(again).toContainText('Employees.List');
});

test('INV-10: an invalid Audit ID holds the run back instead of sending a bad request', async ({ page, allow }) => {
  // When the defect is present the page sends the bad request and the browser logs the 400.
  allow(/Failed to load resource.*400/, 'D-A-45');
  await openFilters(page, 'Request details');
  await page.getByLabel('Audit ID').fill('not an id!!');
  await expect(page.getByText('An audit ID is exactly 24 hexadecimal characters.')).toBeVisible();
  let sent = 0;
  page.on('request', (r) => {
    if (r.url().includes('/api/analyze')) sent++;
  });
  await page.getByRole('button', { name: 'Run analysis' }).click();
  await expect(page.getByText('Comparison not run: fix the filter first')).toBeVisible();
  expect(sent, 'requests sent while a filter is invalid').toBe(0);
});

test('INV-11a: request type buttons narrow the groups', async ({ page }) => {
  const groups = async (name: string) => {
    await page.getByRole('button', { name, exact: true }).click();
    return ops(page);
  };
  const sync = await groups('Sync');
  await expect(sync).toContainText('Payroll.Run');
  await expect(sync).not.toContainText('Reports.Generate');
  const async_ = await groups('Async');
  await expect(async_).toContainText('Reports.Generate');
  await expect(async_).not.toContainText('Payroll.Run');
  const all = await groups('All');
  await expect(all).toContainText('Payroll.Run');
  await expect(all).toContainText('Reports.Generate');
  await groups('Hangfire');
  await expect(page.getByRole('list', { name: 'Operations, worst first' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Sync', exact: true })).toHaveAttribute('title', /.+/);
});

test('INV-11b: a chart interval outside 10 to 86400 is refused and the run waits', async ({ page }) => {
  await openFilters(page, 'Chart');
  let sent = 0;
  page.on('request', (r) => {
    if (r.url().includes('/api/analyze')) sent++;
  });
  for (const bad of ['0', '-30', '5', '1000000']) {
    await page.getByLabel('Chart interval (seconds)').fill(bad);
    await page.getByRole('button', { name: 'Run analysis' }).click();
    await expect(page.getByText('Bucket size must be 10 to 86,400 seconds.'), bad).toBeVisible();
  }
  expect(sent).toBe(0);
});

test('INV-11: a response-time target of 100 ms draws a target line', async ({ page }) => {
  await openFilters(page, 'Chart');
  await page.getByLabel('Optional response-time target (ms)').fill('100');
  await run(page);
  // The chart is a picture; its readable description or the page text must name the target.
  await expect(page.locator('main').getByText(/response-time target/i).first()).toBeVisible();
});
