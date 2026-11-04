// Gate 4 break-it pass. Small defects found by clicking the app the way a clumsy person would.
// Each test asserts the EXPECTED behaviour. All three defects are fixed, so no test is marked `test.fail` or `test.fixme` any more.
//   G4-01  Choosing the same file twice must not list it twice (or must say it is already there).
//   G4-02  A double click on Remove should drop one file, not two.
//   G4-03  Escape on the drawer's "leave without importing?" question answers Keep editing, and the staged files stay.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost } from '../support/api';
import { join } from 'node:path';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const W3C = join(FILES, 'u_ex261020.log');
const HTTPERR = join(FILES, 'httperr1.log');
const NOTES = join(FILES, 'notes.txt');

const picker = (page: Page) => page.getByLabel('Choose files to import');
const fileRows = (page: Page, name: string) => page.getByRole('row').filter({ has: page.getByText(name, { exact: true }) });

async function openFilesPanelInWizard(page: Page, title: string) {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill(title);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Add files', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Add files', level: 5 })).toBeVisible();
}

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('G4-01: choosing the same file twice does not list it twice', async ({ page }) => {
  await openFilesPanelInWizard(page, 'QA G4 duplicate file');
  await picker(page).setInputFiles([W3C]);
  await expect(fileRows(page, 'u_ex261020.log')).toHaveCount(1);
  await picker(page).setInputFiles([W3C]);
  // Fixed means: still one row, or the person is told it is already there.
  await expect
    .poll(async () => (await fileRows(page, 'u_ex261020.log').count()) === 1 || (await page.getByText(/already (in the list|chosen|added)/i).count()) > 0)
    .toBe(true);
});

test('G4-02: a double click on Remove drops one file, not two', async ({ page }) => {
  // Steps: create-case wizard, Sources, Add files, choose three files, double-click the first "Remove ... from the list".
  // Was: the second click landed on the next row's Remove (the rows move up under the pointer), so two files went. Fixed: the second click of a
  // double click is ignored (ImportFilesPanel onRemoveClick).
  await openFilesPanelInWizard(page, 'QA G4 double remove');
  await picker(page).setInputFiles([W3C, HTTPERR, NOTES]);
  const rows = page.getByRole('row');
  const before = await rows.count();
  await page.getByRole('button', { name: /^Remove .* from the list/ }).first().dblclick();
  await expect(rows).toHaveCount(before - 1);
});

test('G4-03: Escape on the drawer leave question answers Keep editing and the staged files stay', async ({ page, api }) => {
  await createCase(api, 'QA G4 escape prompt');
  await goHome(page);
  await openCase(page, 'QA G4 escape prompt');
  await page.getByRole('button', { name: 'Add a file' }).click();
  await expect(page.locator('#import-title')).toHaveText('Import files');
  const use = page.getByRole('button', { name: /^Use this case: .*QA G4 escape prompt/ });
  if (await use.isVisible().catch(() => false)) await use.click();
  await picker(page).setInputFiles([W3C]);
  await expect(fileRows(page, 'u_ex261020.log')).toHaveCount(1);

  await page.keyboard.press('Escape');
  const keep = page.getByRole('button', { name: 'Keep editing' });
  await expect(keep).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(keep).toBeHidden();
  await expect(fileRows(page, 'u_ex261020.log')).toHaveCount(1);
});
