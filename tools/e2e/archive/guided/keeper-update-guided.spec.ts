// Archived with the guided specs: the Help walk-through opens Guided analysis.
import { test, expect, type Page } from '../../support/fixtures';
import { goHome } from '../../support/ui';

const CASE = 'Checkout slowdown, 17 October';

async function openHelp(page: Page) {
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  const dialog = page.getByRole('dialog').first();
  await expect(dialog).toBeVisible();
  return dialog;
}

test('UPD-07: moving around the app and opening Help ten times makes no update call and no call to any other host', async ({ page, demo }) => {
  const seen: string[] = [];
  page.on('request', (r) => seen.push(r.url()));
  const host = new URL(demo.base).host;
  await goHome(page);
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
  await page.getByRole('button', { name: 'Open guided analysis' }).click();
  await expect(page.getByRole('heading', { name: 'Guided analysis', level: 2 })).toBeVisible();
  for (const tab of ['Checklist', 'Export', 'Steps']) await page.getByRole('tab', { name: tab, exact: true }).click();
  for (let i = 0; i < 10; i++) {
    await openHelp(page);
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toHaveCount(0);
  }
  expect(seen.length).toBeGreaterThan(5);
  expect(seen.filter((u) => u.includes('update-check'))).toEqual([]);
  expect(seen.filter((u) => new URL(u).host !== host)).toEqual([]);
});
