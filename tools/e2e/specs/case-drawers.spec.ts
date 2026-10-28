// Archived list, the free and restore drawers without a master key, and erasing from the screen.
import { test, expect } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';

test.beforeEach(async ({ page }) => {
  await goHome(page);
});

async function archivedList(page: import('@playwright/test').Page) {
  await page.getByRole('button', { name: /^Archived cases/ }).click();
  await expect(page.getByText('Loading the archived cases')).toHaveCount(0);
  return page.getByRole('region', { name: /^Archived cases/ });
}

test('CASE-16: the archived list shows kept and freed rows with the right actions, and goes back', async ({ page }) => {
  const list = await archivedList(page);
  await expect(list.getByRole('heading', { name: 'Archived cases (2)' })).toBeVisible();
  await expect(list).toContainText('Archived on 6 Oct 2026');
  await expect(list).toContainText('Working copy kept');
  await expect(list).toContainText('Space freed');
  await expect(list).toContainText('Key ending 7c3e');
  await expect(list.getByText('2 of 2 archived cases shown.')).toBeVisible();
  await expect(list.getByRole('button', { name: /^Restore: Month-end batch stall/ })).toBeVisible();
  await expect(list.getByRole('button', { name: 'Show more archived cases' })).toHaveCount(0);
  await list.getByRole('button', { name: 'Back to the working list' }).click();
  await expect(page.getByRole('button', { name: 'Open case Checkout slowdown, 17 October' })).toBeVisible();
});

test('CASE-18a: Free the space and Restore as new say a master key is needed and change nothing', async ({ page, api }) => {
  const list = await archivedList(page);
  await list.getByRole('button', { name: 'Open case Month-end batch stall, 2 October' }).click();
  await page.getByRole('button', { name: 'Free the space' }).click();
  const needed = page.getByRole('region', { name: 'A master key is needed' });
  await expect(needed).toContainText('Freeing the space needs the master key of this computer, and none is set up yet.');
  await expect(needed).toContainText('The working copy and the case file are not changed.');
  await needed.getByRole('button', { name: 'Not now' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.getByRole('button', { name: 'Restore from the case file as a new case' }).click();
  await expect(page.getByRole('region', { name: 'A master key is needed' })).toContainText('The archived case and the case file are not changed.');
  const rows = (await api('GET', '/api/cases')).json;
  expect(rows.archivedTotal).toBe(2);
});

test('CASE-18: "Set a master key" in the Free drawer opens the master key set-up', async ({ page }) => {
  const list = await archivedList(page);
  await list.getByRole('button', { name: 'Open case Month-end batch stall, 2 October' }).click();
  await page.getByRole('button', { name: 'Free the space' }).click();
  await page.getByRole('region', { name: 'A master key is needed' }).getByRole('button', { name: 'Set a master key' }).click();
  await expect(page.getByText('Protect your cases with a master key').first()).toBeVisible();
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('CASE-21a: erase needs the typed code, a wrong code never erases, and an Erased line is left', async ({ page, api }) => {
  await openCase(page, 'Report export timeouts, 18 October');
  await page.getByRole('region', { name: 'Erase this case' }).getByRole('button', { name: 'Erase this case' }).click();
  const dialog = page.getByRole('region', { name: 'Erase this case' });
  await expect(dialog).toContainText('This cannot be undone.');
  const confirm = dialog.getByRole('button', { name: 'Erase this case' });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole('textbox').fill('DEMOFAIX');
  await expect(confirm).toBeDisabled();
  await dialog.getByRole('textbox').fill(' DEMO fail ');
  await confirm.click();
  await expect(page.getByText('demofail is removed from this computer.')).toBeVisible();
  await page.getByRole('button', { name: 'Back to the list' }).click();
  await expect(page.getByText('Erased case demofail')).toBeVisible();
  await expect(page.getByRole('button', { name: /^Open case Report export timeouts/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Clear demofail' }).click();
  await expect(page.getByText('Erased case demofail')).toHaveCount(0);
  const list = (await api('GET', '/api/cases')).json.cases as any[];
  expect(list.some((c) => c.id.startsWith('demofail'))).toBe(false);
});

test('CASE-21: a case whose working copy is missing can be removed from the list', async ({ page }) => {
  const row = page.getByRole('listitem').filter({ hasText: 'Search index rebuild, 9 October' });
  await expect(row).toContainText('Working copy missing');
  await expect(row.getByRole('button', { name: /Erase/ })).toBeVisible();
});
