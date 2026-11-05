// The Import files popup does not lose files by accident, and keyboard focus stays inside it.
//   IP-03  Esc with files listed asks "Discard N files?"; Keep them goes back; Discard closes the popup and clears the list.
//   IP-04  A click outside asks the same; Tab cycles inside the popup (and inside the question); Shift+Tab wraps.
import { test, expect, type Page } from '../support/fixtures';
import type { Locator } from '@playwright/test';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, type ApiFn } from '../support/api';

const text = (name: string) => ({ name, mimeType: 'text/plain', buffer: Buffer.from('x\n') });

/** Makes a case and opens the popup over its page. */
async function openPopup(page: Page, api: ApiFn, title: string): Promise<Locator> {
  await createCase(api, title);
  await goHome(page);
  await openCase(page, title);
  await page.getByRole('button', { name: 'Add a file' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Import files' });
  await expect(dialog).toBeVisible();
  return dialog;
}

const question = (page: Page) => page.getByRole('alertdialog');
const insideDialog = (dialog: Locator) => dialog.evaluate((el) => el.contains(document.activeElement));

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('IP-03: Esc with files listed asks "Discard N files?"; Keep them goes back and Discard closes the popup', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Discard');

  // Nothing listed: Esc closes at once, with no question.
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await page.getByRole('button', { name: 'Add a file' }).first().click();
  await expect(dialog).toBeVisible();

  await dialog.getByLabel('Choose files to import').setInputFiles([text('u_ex261041.log'), text('u_ex261042.log'), text('ERRORLOG')]);
  // Two groups (IIS web logs, SQL error log) hold the three files.
  await expect(dialog.locator('[data-grp]')).toHaveCount(2);
  await page.keyboard.press('Escape');
  await expect(question(page)).toBeVisible();
  await expect(question(page).getByRole('heading', { name: 'Discard 3 files?' })).toBeVisible();
  await expect(question(page)).toContainText('These files are still on the list. Discarding clears them and closes the popup.');
  await expect(question(page).getByRole('button', { name: 'Keep them' })).toBeFocused();

  // Keep them: the question goes, the popup and its files stay.
  await question(page).getByRole('button', { name: 'Keep them' }).click();
  await expect(question(page)).toHaveCount(0);
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('[data-grp="kind:iis-w3c"]')).toContainText('2 files');
  await expect(dialog.getByRole('button', { name: 'Close import' })).toBeFocused();

  // Esc once more asks again, and Esc while it is open answers "Keep them".
  await page.keyboard.press('Escape');
  await expect(question(page)).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(question(page)).toHaveCount(0);
  await expect(dialog).toBeVisible();

  // The close button asks too. Discard clears the list and closes the popup.
  await dialog.getByRole('button', { name: 'Close import' }).click();
  await expect(question(page).getByRole('heading', { name: 'Discard 3 files?' })).toBeVisible();
  await question(page).getByRole('button', { name: 'Discard', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(question(page)).toHaveCount(0);

  // A new popup starts empty.
  await page.getByRole('button', { name: 'Add a file' }).first().click();
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('[data-grp]')).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Continue' })).toBeDisabled();
});

test('IP-04: a click outside asks first, Tab cycles inside the popup and inside the question, and Shift+Tab wraps', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Focus');
  await dialog.getByLabel('Choose files to import').setInputFiles([text('u_ex261051.log'), text('u_ex261052.log')]);
  await expect(dialog.locator('[data-grp="kind:iis-w3c"]')).toContainText('2 files');

  // A click on the dim area, well outside the popup, asks the same question as Esc.
  await page.mouse.click(5, 5);
  await expect(question(page).getByRole('heading', { name: 'Discard 2 files?' })).toBeVisible();
  await expect(dialog).toBeVisible();
  // Tab stays in the question: Keep them, Discard, and back round.
  const keep = question(page).getByRole('button', { name: 'Keep them' });
  const discard = question(page).getByRole('button', { name: 'Discard', exact: true });
  await expect(keep).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(discard).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(keep).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(discard).toBeFocused();
  await keep.click();
  await expect(question(page)).toHaveCount(0);

  // Tab never leaves the popup. When it reaches the last control (Continue) the next Tab goes to the first (the close button).
  const close = dialog.getByRole('button', { name: 'Close import' });
  const last = dialog.getByRole('button', { name: 'Continue' });
  await expect(close).toBeFocused();
  let wrapped = false;
  for (let i = 0; i < 40 && !wrapped; i++) {
    const onLast = await last.evaluate((el) => el === document.activeElement);
    await page.keyboard.press('Tab');
    expect(await insideDialog(dialog)).toBe(true);
    if (onLast) {
      await expect(close).toBeFocused();
      wrapped = true;
    }
  }
  expect(wrapped, 'Tab reached the last control and wrapped to the first').toBe(true);

  // Shift+Tab on the first control goes to the last.
  await page.keyboard.press('Shift+Tab');
  await expect(last).toBeFocused();
  expect(await insideDialog(dialog)).toBe(true);
  await page.keyboard.press('Shift+Tab');
  expect(await insideDialog(dialog)).toBe(true);
});
