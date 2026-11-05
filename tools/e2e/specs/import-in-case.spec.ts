// Someone already working in an open case who presses an "Add files" entry point lands in the Import files popup of THAT case. The popup does not
// ask them to pick a case: the case is named in its title row, and there is no case list inside it.
//   INCASE-01  The case home's "Add a file" and the Sources hub's Add data > Files card open the popup for this case, with no "Use this case" list.
//   INCASE-02  "Add the file again" on the "Needs another try" list opens the same popup for that case (no case list).
import { test, expect, type Page } from '../support/fixtures';
import { createCase } from '../support/api';
import { goHome, openCase } from '../support/ui';

const popup = (page: Page) => page.getByRole('dialog', { name: 'Import files' });
const useThisCase = (page: Page) => page.getByRole('button', { name: /^Use this case/ });

/** The popup is on the Choose step of the case with this title: no case list, the file picker is there, and the page names the case by its code. */
async function expectPopupOfThisCase(page: Page, title: string, id: string) {
  const d = popup(page);
  await expect(d).toHaveCount(1);
  await expect(d.getByRole('heading', { name: 'Import files', level: 2 })).toBeVisible();
  await expect(d.locator('.case-chip')).toContainText(title);
  await expect(d.locator('.prog-line')).toContainText('Step 1 of 3 · Choose files to import');
  await expect(d.getByText('Drop files or a folder here')).toBeVisible();
  await expect(d.getByRole('button', { name: 'Add files', exact: true })).toBeVisible();
  await expect(d.getByLabel('Choose files to import', { exact: true })).toHaveCount(1);
  // Not asked to pick a case: no case list, no way to switch case, and no drawer chrome of the old Import drawer.
  await expect(useThisCase(page)).toHaveCount(0);
  await expect(d.getByRole('button', { name: /^Close case/ })).toHaveCount(0);
  await expect(d.getByRole('button', { name: /^Erase case/ })).toHaveCount(0);
  await expect(page.locator('[data-other-case]')).toHaveCount(0);
  await expect(page.locator('#drawer-add-head')).toHaveCount(0);
  await expect(page.locator('#import-title')).toHaveCount(0);
  // The page behind still names the case it is working in (the case home shows its short code).
  await expect(page.getByText('Your case · ' + id.slice(0, 8))).toBeVisible();
}

test('INCASE-01: Add files from an open case opens the popup for that case with no case list', async ({ page, api }) => {
  const title = 'QA InCase Here';
  const id = await createCase(api, title);
  await createCase(api, 'QA InCase Elsewhere');
  await goHome(page);
  await openCase(page, title);
  await expect(page.getByRole('heading', { name: title })).toBeVisible();

  // Entry 1: the Files section's "Add a file".
  await page.getByRole('button', { name: 'Add a file' }).click();
  await expectPopupOfThisCase(page, title, id);
  // The other case is not offered anywhere inside the popup.
  await expect(popup(page)).not.toContainText('QA InCase Elsewhere');

  // Close with Escape (nothing staged, so nothing is asked), then entry 2: the Sources hub, Add data, Files card.
  await page.keyboard.press('Escape');
  await expect(popup(page)).toHaveCount(0);
  const hub = page.locator('[data-sources-hub]');
  await hub.getByRole('tab', { name: 'Add data', exact: true }).click();
  await hub.locator('[data-card=files]').getByRole('button', { name: 'Add files' }).click();
  await expectPopupOfThisCase(page, title, id);
  await expect(popup(page)).not.toContainText('QA InCase Elsewhere');
});

test('INCASE-02: Add the file again on a "Needs another try" item opens the popup of that case with no case list', async ({ page, api }) => {
  // The demo case that part-failed keeps one import on its retry list (see CASE-11b).
  const title = 'Report export timeouts, 18 October';
  const demoFailed = 'demofailed'.padEnd(26, 'a');
  await goHome(page);
  await openCase(page, title);
  const list = page.locator('section.block').filter({ has: page.locator('#home-retries') });
  const item = list.getByRole('listitem').filter({ hasText: 'demo-access-web-b.log' });
  await item.getByRole('button', { name: 'Add the file again' }).click();
  await expectPopupOfThisCase(page, title, demoFailed);

  await page.keyboard.press('Escape');
  await expect(popup(page)).toHaveCount(0);
  // Nothing was retried or removed.
  await expect(list.getByRole('listitem')).toHaveCount(2);
  expect((await api('GET', `/api/cases/${demoFailed}`)).status).toBe(200);
});
