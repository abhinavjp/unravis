// Blackbox charts pop-up: the first-time help, status chips, plain scale words, the "What happened" view with crash dumps and
// Windows events (text from the recorded PC stays plain text), Latest compared, Everything, and the table's "Highest at".
// Reads the demo Blackbox "web-a" (its recording carries two crash dumps, a restart and some Windows events). No values are compared
// with live Perfmon here: that stays a check on a real PC.
import { test, expect, type Page } from '../support/fixtures';

// The charts need no case store.
test.use({ demoOptions: { cases: false } });

async function openCharts(page: Page) {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Blackbox' });
  await drawer.getByRole('button', { name: /^Open status: web-a/ }).first().click();
  await page.getByRole('button', { name: 'Open charts' }).first().click();
  const charts = page.locator('dialog.bbc[open]');
  await expect(charts.locator('canvas').first()).toBeVisible();
  return charts;
}

test('Blackbox charts: help card, plain scale words with a tooltip each, quick questions and the highest-at column', async ({ page }) => {
  const charts = await openCharts(page);
  await expect(charts.getByRole('heading', { name: 'How to read these charts' })).toBeVisible();
  await expect(charts.getByRole('button', { name: 'Normal' })).toHaveAttribute('title', /linear scale/);
  await expect(charts.getByRole('button', { name: 'Log (x10 steps)' })).toHaveAttribute('title', /log scale/);
  await expect(charts.getByRole('button', { name: 'Linear', exact: true })).toHaveCount(0);
  await expect(charts.getByRole('columnheader', { name: 'Highest at' })).toBeVisible();
  await charts.getByRole('button', { name: 'Is the disk slow?' }).click();
  await expect(charts.locator('.bbc-figs tbody tr')).toHaveCount(2);
  await charts.locator('.bbc-rowbtn').first().click();
  await expect(charts.locator('.bbc-meaning')).toContainText('disk');
  // the help card is dismissed for good, and can be brought back
  await charts.getByRole('button', { name: 'Got it' }).click();
  await expect(charts.getByRole('heading', { name: 'How to read these charts' })).toHaveCount(0);
  await charts.getByRole('button', { name: 'Show help' }).click();
  await expect(charts.getByRole('heading', { name: 'How to read these charts' })).toBeVisible();
  // Esc closes only the charts pop-up
  await page.keyboard.press('Escape');
  await expect(page.locator('dialog.bbc')).toHaveCount(0);
});

test('Blackbox charts: What happened lists dumps, restarts and Windows events, and recorded names stay plain text', async ({ page }) => {
  const charts = await openCharts(page);
  await charts.getByRole('button', { name: 'What happened', exact: true }).click();
  await expect(charts.getByRole('heading', { name: 'Timeline of this time range' })).toBeVisible();
  await expect(charts.getByText('Crash dump: w3wp.exe.4412.dmp')).toBeVisible();
  // the folder also holds a month-old dump; it is not part of this range
  await expect(charts.getByText(/old-crash-from-last-month/)).toHaveCount(0);
  // a hostile file name is text, never an element
  await expect(charts.getByText('Crash dump: <img src=x onerror=alert(1)>.dmp')).toBeVisible();
  await expect(charts.locator('img')).toHaveCount(0);
  await expect(charts.getByText('The recorder started again')).toBeVisible();
  const events = charts.getByRole('table').nth(1);
  await expect(events.getByRole('row', { name: /System Critical 41 1 Usually means: Windows restarted without a clean shutdown/ })).toBeVisible();
  await expect(charts.getByRole('row', { name: /Time with no recording/ })).toBeVisible();
  // no folder from the recorded PC reaches the page
  expect(await charts.innerText()).not.toMatch(/[A-Z]:\\|\/Users\//);
});

test('Blackbox charts: Latest compared and Everything read the same recording', async ({ page }) => {
  const charts = await openCharts(page);
  await charts.getByRole('button', { name: 'Latest compared', exact: true }).click();
  await expect(charts.getByRole('img', { name: /^Processor busy \(% Processor Time\): (Near its highest|In its usual range|Near its lowest|Did not change)/ })).toBeVisible();
  await charts.getByRole('button', { name: 'Everything', exact: true }).click();
  await expect(charts.getByText('Memory that programs could use right away')).toBeVisible();
  await charts.getByRole('searchbox', { name: 'Find a counter' }).fill('disk queue');
  await expect(charts.getByRole('rowheader', { name: /Disk queue/ })).toBeVisible();
  await charts.getByRole('searchbox', { name: 'Find a counter' }).fill('zzz-no-such-counter');
  await expect(charts.getByText('No counter matches.')).toBeVisible();
  await charts.getByRole('button', { name: 'Charts', exact: true }).click();
  await expect(charts.locator('canvas').first()).toBeVisible();
});
