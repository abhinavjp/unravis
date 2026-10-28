// Guided analysis (steps 0 to 10) and the manual tools, with fixed windows on the demo day.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';

async function openGuided(page: Page) {
  await goHome(page);
  await openCase(page, 'Checkout slowdown, 17 October');
  await page.getByRole('button', { name: 'Open guided analysis' }).click();
  await expect(page.getByRole('heading', { name: 'Guided analysis', level: 2 })).toBeVisible();
}

async function setWindows(page: Page, inc: [string, string], ref: [string, string]) {
  const incident = page.getByRole('group', { name: 'Incident window' });
  const reference = page.getByRole('group', { name: 'Reference window (normal)' });
  await incident.getByLabel('From').fill(inc[0]);
  await incident.getByLabel('To').fill(inc[1]);
  await reference.getByLabel('From').fill(ref[0]);
  await reference.getByLabel('To').fill(ref[1]);
}

async function toManual(page: Page) {
  await page.getByText('Case actions').click();
  await page.getByRole('button', { name: 'Switch to Manual tools' }).click();
  await expect(page.getByText('You are using Manual tools.')).toBeVisible();
}

async function openSlowest(page: Page) {
  await page.getByRole('tab', { name: 'Tools' }).click();
  await page.getByRole('button', { name: /Slowest calls/ }).first().click();
  await expect(page.locator('#tools-title')).toHaveText('Slowest calls');
  const win = page.locator('details.windows');
  if (!(await win.evaluate((d) => (d as HTMLDetailsElement).open))) await win.locator('summary').click();
}

const INC: [string, string] = ['2026-10-20T08:45', '2026-10-20T09:00'];
const REF: [string, string] = ['2026-10-20T08:30', '2026-10-20T08:45'];

test('GUIDE-04: backwards, overlapping and too long windows keep Run the steps turned off', async ({ page }) => {
  await openGuided(page);
  const run = page.getByRole('button', { name: 'Run the steps' });
  await expect(run).toBeDisabled();
  await setWindows(page, INC, REF);
  await expect(run).toBeEnabled();
  await setWindows(page, ['2026-10-20T09:00', '2026-10-20T08:45'], REF);
  await expect(run).toBeDisabled();
  await expect(page.getByText('Fix the windows to run the steps.').first()).toBeVisible();
  await setWindows(page, INC, ['2026-10-20T08:50', '2026-10-20T08:55']);
  await expect(run).toBeDisabled();
  await setWindows(page, ['2026-10-12T08:00', '2026-10-20T09:00'], ['2026-10-11T08:00', '2026-10-12T08:00']);
  await expect(run).toBeDisabled();
  await setWindows(page, INC, REF);
  await expect(run).toBeEnabled();
});

test('GUIDE-19a: every step can be reached before a run and says the run is still to come', async ({ page }) => {
  await openGuided(page);
  await setWindows(page, INC, REF);
  for (const n of [10, 5, 0]) {
    await page.getByRole('navigation', { name: 'Guided analysis steps' }).getByRole('button', { name: new RegExp('^' + n + ' ') }).click();
    await expect(page.locator('#step-title-' + n)).toBeVisible();
  }
  await page.getByRole('navigation', { name: 'Guided analysis steps' }).getByRole('button', { name: /^3 / }).click();
  await expect(page.getByText('This step shows the last run. Run the steps first to fill it in.')).toBeVisible();
  for (let n = 4; n <= 10; n++) {
    await page.getByRole('button', { name: 'Next step' }).click();
  }
  await expect(page.locator('#step-title-10')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Next step' })).toBeDisabled();
});

test('GUIDE-19: the keyboard focus moves to the step title when the step changes', async ({ page }) => {
  await openGuided(page);
  await setWindows(page, INC, REF);
  await page.getByRole('navigation', { name: 'Guided analysis steps' }).getByRole('button', { name: /^3 / }).click();
  await expect(page.locator('#step-title-3')).toBeFocused();
  await page.getByRole('button', { name: 'Next step' }).click();
  await expect(page.locator('#step-title-4')).toBeFocused();
});

test('GUIDE-02: running the steps on the demo day gives every step a status word', async ({ page }) => {
  await openGuided(page);
  await setWindows(page, INC, REF);
  await page.getByRole('button', { name: 'Run the steps' }).click();
  const rail = page.getByRole('navigation', { name: 'Guided analysis steps' });
  await expect(rail.getByRole('button', { name: /Waiting/ })).toHaveCount(0);
  const words = /Ready|Partly ready|Cannot test yet|Failed/;
  for (let n = 0; n <= 10; n++) {
    await expect(rail.getByRole('button', { name: new RegExp('^' + n + ' ') })).toHaveAccessibleName(words);
  }
});

test('MAN-04a: the slowest calls tool lists calls without user, address or URL, and says so for an empty window', async ({ page }) => {
  await openGuided(page);
  await toManual(page);
  await openSlowest(page);
  await setWindows(page, INC, REF);
  await page.getByRole('button', { name: 'Show the slowest calls' }).click();
  const table = page.getByRole('table').first();
  await expect(table).toContainText('Payroll.Run');
  await expect(table.getByRole('columnheader')).toHaveText(['API', 'Started (UTC)', 'Time']);
  await setWindows(page, ['2026-10-17T10:30', '2026-10-17T11:00'], ['2026-10-17T09:30', '2026-10-17T10:30']);
  await page.getByRole('button', { name: 'Show the slowest calls' }).click();
  await expect(page.getByText('This window holds no audit records, so there is nothing to list.')).toBeVisible();
});

test('MAN-04: a wide window of 2.5 hours lists the long West stalls instead of failing', async ({ page, allow }) => {
  allow(/Failed to load resource.*400/, 'D-B-01');
  await openGuided(page);
  await toManual(page);
  await openSlowest(page);
  await setWindows(page, ['2026-10-20T05:30', '2026-10-20T08:00'], ['2026-10-20T03:00', '2026-10-20T05:30']);
  await page.getByRole('button', { name: 'Show the slowest calls' }).click();
  await expect(page.getByRole('table').first()).toContainText('Payroll.Run');
  await expect(page.getByText('The tool could not run.')).toHaveCount(0);
});
