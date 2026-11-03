// Archived with the guided specs: the Export tab sits inside Guided analysis.
import { test, expect, type Page } from '../../support/fixtures';
import { goHome, openCase } from '../../support/ui';

async function openExportTab(page: Page) {
  await goHome(page);
  await openCase(page, 'Checkout slowdown, 17 October');
  await page.getByRole('button', { name: 'Open guided analysis' }).click();
  await page.getByRole('tab', { name: 'Export' }).click();
}

test('EXP-04a: the Export tab before any run says so in one sentence', async ({ page }) => {
  await openExportTab(page);
  await expect(page.getByText('Nothing has been analysed yet. The report will say so and hold only the checklist, if you include it.')).toBeVisible();
});
