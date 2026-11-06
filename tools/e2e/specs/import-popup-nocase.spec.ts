// Header Import when no case is open (the popup itself is covered in import-popup-shell.spec.ts).
//   IP-17  With no case open, header Import leads to the start screen with a hint and opens no popup; with a case open, it opens the popup.
import { test, expect } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost } from '../support/api';

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('IP-17: header Import with no case open shows a hint on the start screen; with a case the popup opens', async ({ page, api }) => {
  // The app opens on the analysis with no case open yet.
  const importButton = page.getByRole('button', { name: 'Import', exact: true });
  await expect(importButton).toBeVisible();
  await importButton.click();

  // No popup. The start screen shows instead and says why nothing opened.
  await expect(page.getByRole('dialog', { name: 'Import files' })).toHaveCount(0);
  await expect(page.locator('#start-title')).toBeVisible();
  await expect(page.getByText('Pick a case first, then choose Import files.')).toBeVisible();

  // The hint can be dismissed.
  await page.getByRole('button', { name: 'Dismiss' }).click();
  await expect(page.getByText('Pick a case first, then choose Import files.')).toHaveCount(0);

  // With a case open, the same header button opens the popup.
  await createCase(api, 'QA Popup No Case');
  await page.reload();
  await goHome(page);
  await openCase(page, 'QA Popup No Case');
  await page.getByRole('button', { name: 'Open the analysis' }).click();
  await expect(importButton).toBeVisible();
  await importButton.click();
  await expect(page.getByRole('dialog', { name: 'Import files' })).toBeVisible();
});
