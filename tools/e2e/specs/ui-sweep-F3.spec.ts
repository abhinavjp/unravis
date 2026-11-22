// UI sweep F3 (cases, create wizard, keys, erase, update bar, names). Findings CASES-03, 04, 05, 14, 25 and SETUP-A11Y-13 on the
// demo data of the e2e fixtures. Needs UNRAVIS_EXE like the other specs; it was written without a build to run it on.
import { test, expect } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';

test('SWEEP-F3-01 (CASES-03, 04): the header says Add files and Ledger, and the ledger entry is a theory', async ({ page }) => {
  await page.getByRole('button', { name: 'All cases', exact: true }).click();
  await page.getByRole('button', { name: /^Open case Checkout slowdown/ }).click();
  const header = page.getByRole('banner');
  await expect(header.getByRole('button', { name: 'Add files', exact: true })).toBeVisible();
  await expect(header.getByRole('button', { name: /^Import\b/ })).toHaveCount(0);
  await header.getByRole('button', { name: /^Ledger/ }).click();
  const ledger = page.getByRole('dialog', { name: 'Ledger' }).or(page.getByRole('complementary', { name: 'Ledger' }));
  await expect(ledger).toBeVisible();
  await expect(ledger).not.toContainText(/hypothes/i);
  await expect(ledger.getByText('New theory').first()).toBeVisible();
});

test('SWEEP-F3-02 (CASES-14): a closed case says on what day it will be erased', async ({ page }) => {
  await goHome(page);
  await openCase(page, 'Night batch overrun, 10 October');
  await expect(page.getByText(/^It will be erased on .+ unless you reopen it\./).first()).toBeVisible();
});

test('SWEEP-F3-03 (CASES-25): a reload in the middle of the guide keeps the case name', async ({ page }) => {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill('Sweep draft name');
  await page.reload();
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByLabel('Case name')).toHaveValue('Sweep draft name');
  // The stored copy holds the name and environments only.
  const stored = await page.evaluate(() => sessionStorage.getItem('unravis.wizardDraft'));
  expect(Object.keys(JSON.parse(stored ?? '{}')).sort()).toEqual(['envText', 'keyOn', 'title']);
});

test('SWEEP-F3-04 (CASES-05): the Create button says why it is off, and the guide does not scroll sideways', async ({ page }) => {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill('Sweep blocked');
  await expect(page.getByRole('button', { name: 'Next' })).toBeVisible();
  const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(wide).toBe(false);
});

test('SWEEP-F3-05 (CASES-18): the running step of the create progress stands out from the done steps', async ({ page }) => {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill('Sweep progress');
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Create empty case' }).click();
  await expect(page.getByRole('heading', { level: 2, name: 'Sweep progress' })).toBeVisible();
});
