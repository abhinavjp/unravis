// UI sweep F5 (keyboard, focus and accessibility): where the keyboard lands after the app changes screen, the Connections drawer,
// the inline forget question, the Audit sources menu, and Help's jump links. Findings SETUP-A11Y-01, 02, 03, 17, 18.
import { test, expect, type Page } from '../support/fixtures';

const CASE = 'Checkout slowdown, 17 October';

async function openCaseHome(page: Page) {
  await page.getByRole('button', { name: 'All cases', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
}

const activeName = (page: Page) => page.evaluate(() => {
  const a = document.activeElement;
  return !a || a === document.body ? 'BODY' : a.tagName + ':' + ((a.getAttribute('aria-label') || a.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 40));
});

test('SWEEP-F5-03: each screen change puts the keyboard on the new screen and names the tab', async ({ page }) => {
  await page.getByRole('button', { name: 'All cases', exact: true }).click();
  await expect(page).toHaveTitle('All cases - Unravis');
  await expect.poll(() => activeName(page)).not.toBe('BODY');
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
  await expect.poll(() => activeName(page)).not.toBe('BODY');
  await expect(page).toHaveTitle(CASE + ' - Unravis');
  await page.getByRole('button', { name: /^All cases/ }).first().click();
  await expect(page).toHaveTitle('All cases - Unravis');
  await expect.poll(() => activeName(page)).not.toBe('BODY');
});

test('SWEEP-F5-03: closing "Where is it?" with Esc leaves the keyboard on the analysis, not the page body', async ({ page }) => {
  await page.getByRole('button', { name: 'Where is it?', exact: true }).first().click();
  await expect(page.getByRole('heading', { name: 'Where is it?' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page).toHaveTitle('Analysis - Unravis');
  await expect.poll(() => activeName(page)).not.toBe('BODY');
});

test('SWEEP-F5-01: the Connections drawer opens with the keyboard inside it, and Esc closes it', async ({ page }) => {
  await openCaseHome(page);
  const opener = page.getByRole('button', { name: '+ MongoDB', exact: true });
  await opener.focus();
  await page.keyboard.press('Enter');
  const drawer = page.getByRole('dialog', { name: 'Connections' });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole('heading', { name: 'Connections' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(drawer).toHaveCount(0);
});

test('SWEEP-F5-02: the forget question takes focus on Keep it, lists it first, and Esc withdraws it', async ({ page }) => {
  await openCaseHome(page);
  const block = page.getByRole('region', { name: 'Saved connections' });
  const forget = block.getByRole('button', { name: /^Forget/ }).first();
  const name = (await forget.getAttribute('aria-label')) as string;
  await forget.focus();
  await page.keyboard.press('Enter');
  const ask = block.getByRole('alertdialog');
  await expect(ask).toBeVisible();
  await expect(ask.getByRole('button', { name: 'Keep it' })).toBeFocused();
  const labels = await ask.getByRole('button').allInnerTexts();
  expect(labels).toEqual(['Keep it', 'Forget it']);
  await page.keyboard.press('Escape');
  await expect(ask).toHaveCount(0);
  await expect(block.getByRole('button', { name })).toBeFocused();
});

test('SWEEP-F5-17: tabbing out of the Audit sources menu closes it', async ({ page }) => {
  const trigger = page.getByRole('button', { name: /^Audit sources/ });
  await trigger.focus();
  await page.keyboard.press('Enter');
  const menu = page.getByRole('dialog', { name: 'Audit sources' });
  await expect(menu).toBeVisible();
  for (let i = 0; i < 40 && (await menu.count()) > 0; i++) await page.keyboard.press('Tab');
  await expect(menu).toHaveCount(0);
});

test('SWEEP-F5-18: Help has links to Updates and Support logs that move the keyboard to them', async ({ page }) => {
  await page.getByRole('button', { name: /Glossary|Help/ }).first().click();
  const help = page.getByRole('dialog');
  await help.getByTestId('jump-updates').click();
  await expect(help.getByRole('heading', { name: 'Updates' })).toBeFocused();
  await help.getByTestId('jump-logs').click();
  await expect(help.getByRole('heading', { name: 'Support logs' })).toBeFocused();
});
