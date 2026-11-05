// Someone already working in an open case who presses an "Add files" entry point lands on the file picker of THAT case. The drawer does not ask them
// to pick a case again: the case list (Use this case / Close case / Erase case) waits behind a "Use another case" link.
//   INCASE-01  The case home's "Add a file" and the Sources hub's Add data > Files card open the file view with no "Use this case" list; the other case is
//              one "Use another case" click away.
//   INCASE-02  "Add the file again" on the "Needs another try" list opens the same file view for that case (no case list).
import { test, expect, type Page } from '../support/fixtures';
import { createCase } from '../support/api';
import { goHome, openCase } from '../support/ui';

const drawer = (page: Page) => page.getByRole('dialog');
const useThisCase = (page: Page) => page.getByRole('button', { name: /^Use this case/ });

/** The drawer is on the file picker of the case with this id: no case list, the picker is there, and the page names the case by its code. */
async function expectFileViewOfThisCase(page: Page, id: string) {
  const d = drawer(page);
  await expect(d).toHaveCount(1);
  await expect(page.locator('#import-title')).toHaveText('Import files');
  await expect(page.locator('#file-heading')).toBeVisible();
  await expect(d.getByLabel('Choose files to import')).toBeVisible();
  await expect(d.locator('#import-file')).toBeVisible();
  // Not asked to pick a case: no button of the case list is on screen (the list is hidden, so it is not in the page's accessible tree).
  await expect(useThisCase(page)).toHaveCount(0);
  await expect(d.getByRole('button', { name: /^Close case/ })).toHaveCount(0);
  await expect(d.getByRole('button', { name: /^Erase case/ })).toHaveCount(0);
  // The cards of Add data are not shown either, and the page still names the case it is working in (the history heading carries its code).
  await expect(page.locator('#drawer-add-head')).toHaveCount(0);
  await expect(page.locator('#history-heading')).toContainText(id.slice(0, 8));
  await expect(d.locator('[data-other-case]')).toHaveText('Use another case');
}

test('INCASE-01: Add files from an open case opens the file picker with no case list; Use another case brings the list', async ({ page, api }) => {
  const title = 'QA InCase Here';
  const id = await createCase(api, title);
  await createCase(api, 'QA InCase Elsewhere');
  await goHome(page);
  await openCase(page, title);
  await expect(page.getByRole('heading', { name: title })).toBeVisible();

  // Entry 1: the Files section's "Add a file".
  await page.getByRole('button', { name: 'Add a file' }).click();
  await expectFileViewOfThisCase(page, id);

  // Use another case: the list appears with a Use this case button for the other case, and hides again.
  await drawer(page).locator('[data-other-case]').click();
  await expect(page.getByRole('button', { name: /^Use this case: .*QA InCase Elsewhere/ })).toBeVisible();
  await expect(drawer(page).locator('[data-other-case]')).toHaveText('Hide the case list');
  await drawer(page).locator('[data-other-case]').click();
  await expect(useThisCase(page)).toHaveCount(0);

  // Close with Escape (nothing staged, so nothing is asked), then entry 2: the Sources hub, Add data, Files card.
  await page.keyboard.press('Escape');
  await expect(drawer(page)).toHaveCount(0);
  const hub = page.locator('[data-sources-hub]');
  await hub.getByRole('tab', { name: 'Add data', exact: true }).click();
  await hub.locator('[data-card=files]').getByRole('button', { name: 'Add files' }).click();
  await expectFileViewOfThisCase(page, id);
  await drawer(page).locator('[data-other-case]').click();
  await expect(page.getByRole('button', { name: /^Use this case: .*QA InCase Elsewhere/ })).toBeVisible();
});

test('INCASE-02: Add the file again on a "Needs another try" item opens the file picker of that case with no case list', async ({ page, api }) => {
  // The demo case that part-failed keeps one import on its retry list (see CASE-11b).
  const title = 'Report export timeouts, 18 October';
  const demoFailed = 'demofailed'.padEnd(26, 'a');
  await goHome(page);
  await openCase(page, title);
  const list = page.locator('section.block').filter({ has: page.locator('#home-retries') });
  const item = list.getByRole('listitem').filter({ hasText: 'demo-access-web-b.log' });
  await item.getByRole('button', { name: 'Add the file again' }).click();
  await expectFileViewOfThisCase(page, demoFailed);

  // Other cases exist (the demo has several), so the list is one click away.
  await drawer(page).locator('[data-other-case]').click();
  await expect(useThisCase(page).first()).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(drawer(page)).toHaveCount(0);
  // Nothing was retried or removed.
  await expect(list.getByRole('listitem')).toHaveCount(2);
  expect((await api('GET', `/api/cases/${demoFailed}`)).status).toBe(200);
});
