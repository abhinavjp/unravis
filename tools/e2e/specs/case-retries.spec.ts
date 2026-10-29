// The "Needs another try" list on the demo case that part-failed. Order matters: the list is read
// first, then the case is closed and reopened, and the removals come last (they change the case).
import { test, expect } from '../support/fixtures';
import { demoId } from '../support/api';
import { goHome, openCase } from '../support/ui';

const CASE = 'Report export timeouts, 18 October';

test.beforeEach(async ({ page }) => {
  await goHome(page);
  await openCase(page, CASE);
});

const section = (page: import('@playwright/test').Page) => page.locator('section.block').filter({ has: page.locator('#home-retries') });

test('CASE-11: the list shows two items with different stickers and no secrets', async ({ page }) => {
  const list = section(page);
  await expect(list.getByRole('heading', { name: 'Needs another try' })).toBeVisible();
  const items = list.getByRole('listitem');
  await expect(items).toHaveCount(2);
  const imp = items.filter({ hasText: 'demo-access-web-b.log' });
  const aud = items.filter({ hasText: 'Reports database' });
  await expect(imp.getByText('Import failed', { exact: true })).toBeVisible();
  await expect(aud.getByText('Not added to analysis', { exact: true })).toBeVisible();
  // The header count agrees.
  await expect(page.locator('dt', { hasText: 'Needs another try' }).locator('xpath=following-sibling::dd')).toHaveText('2');
  // No password or file text on the list.
  const text = (await list.innerText()).toLowerCase();
  expect(text).not.toMatch(/password|secret|connection string|mongodb:\/\//);
});

test('CASE-11a: the audit item has no Try again, and says it needs the wizard and offers Remove', async ({ page }) => {
  const aud = section(page).getByRole('listitem').filter({ hasText: 'Reports database' });
  await expect(aud.getByRole('button', { name: 'Try again' })).toHaveCount(0);
  await expect(aud.getByText('This one needs details only the wizard holds. Remove it, then add it again from Connections or Add a file.')).toBeVisible();
  await expect(aud.getByRole('button', { name: 'Remove from list' })).toBeEnabled();
});

test('CASE-11b: the import item offers Add the file again, which opens the import drawer once', async ({ page }) => {
  const imp = section(page).getByRole('listitem').filter({ hasText: 'demo-access-web-b.log' });
  const again = imp.getByRole('button', { name: 'Add the file again' });
  await expect(again).toBeEnabled();
  await again.dblclick();
  await expect(page.getByRole('dialog')).toHaveCount(1);
  await expect(page.getByRole('heading', { name: 'Import files' })).toBeVisible();
  // The item is still on the list: nothing was retried or removed.
  await page.keyboard.press('Escape');
  await expect(section(page).getByRole('listitem')).toHaveCount(2);
});

test('CASE-11c: on a closed case the list says reopen first and Remove is off, then reopening restores it', async ({ page, api }) => {
  const id = demoId('demofail');
  const closed = await api('POST', `/api/cases/${id}/close`, undefined, 'open');
  expect(closed.status).toBe(200);
  await page.reload();
  await goHome(page);
  await openCase(page, CASE);
  const list = section(page);
  await expect(list.getByText('This case is closed. Reopen it to try again.').first()).toBeVisible();
  await expect(list.getByRole('button', { name: 'Try again' })).toHaveCount(0);
  await expect(list.getByRole('button', { name: 'Add the file again' })).toHaveCount(0);
  for (const b of await list.getByRole('button', { name: 'Remove from list' }).all()) await expect(b).toBeDisabled();
  const open = await api('POST', `/api/cases/${id}/reopen`, undefined, 'closed');
  expect(open.status).toBe(200);
  await page.reload();
  await goHome(page);
  await openCase(page, CASE);
  await expect(section(page).getByRole('listitem')).toHaveCount(2);
});

test('CASE-11d: Remove drops one item, the count falls and a reload keeps it gone', async ({ page, api }) => {
  const list = section(page);
  const imp = list.getByRole('listitem').filter({ hasText: 'demo-access-web-b.log' });
  await imp.getByRole('button', { name: 'Remove from list' }).click();
  await expect(list.getByRole('listitem')).toHaveCount(1);
  await expect(list.getByText('demo-access-web-b.log')).toHaveCount(0);
  await expect(page.locator('dt', { hasText: 'Needs another try' }).locator('xpath=following-sibling::dd')).toHaveText('1');
  const id = demoId('demofail');
  const res = await api('GET', `/api/cases/${id}`);
  expect(JSON.stringify(res.json.retries ?? [])).not.toContain('demo-access-web-b.log');
  await page.reload();
  await goHome(page);
  await openCase(page, CASE);
  await expect(section(page).getByRole('listitem')).toHaveCount(1);
});

test('CASE-11e: removing the last item takes the whole list away', async ({ page }) => {
  const list = section(page);
  await expect(list.getByRole('listitem')).toHaveCount(1);
  await list.getByRole('button', { name: 'Remove from list' }).click();
  await expect(page.locator('#home-retries')).toHaveCount(0);
  await expect(page.getByText('Needs another try')).toHaveCount(0);
});
