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
  await expect(page.locator('#where-title')).toBeVisible();
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
  // Where focus really lands: somewhere inside the dialog (the title or its first control), never the page behind it.
  await expect.poll(() => page.evaluate(() => !!document.activeElement?.closest('[role="dialog"][aria-labelledby="connections-title"]'))).toBe(true);
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

test('SWEEP-F5-02b: the Ledger remove question focuses Keep, lists it first, and Esc gives focus back to Remove', async ({ page }) => {
  await openCaseHome(page);
  await page.getByRole('button', { name: 'Add a theory' }).first().click();
  const drawer = page.getByRole('dialog').first();
  await expect(drawer).toBeVisible();
  await drawer.locator('#ledger-new').fill('Pool exhausted at 09:10');
  await page.keyboard.press('Enter');
  const entry = drawer.getByRole('button', { name: /Pool exhausted at 09:10/ }).first();
  await expect(entry).toBeVisible();
  const remove = drawer.getByRole('button', { name: /^Delete/ }).first();
  if (!(await remove.isVisible())) await entry.click();
  await remove.focus();
  await page.keyboard.press('Enter');
  const ask = drawer.getByRole('group').filter({ has: page.getByRole('button', { name: /^Keep/ }) });
  await expect(ask.getByRole('button').first()).toBeFocused();
  expect((await ask.getByRole('button').first().innerText()).trim()).toMatch(/^Keep/);
  await page.keyboard.press('Escape');
  await expect(drawer).toBeVisible();
  await expect(ask).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: /^Delete/ }).first()).toBeFocused();
});

test('SWEEP-F5-targets: the case home jump links, Erase this case and the Help links are at least 24 px tall', async ({ page }) => {
  await openCaseHome(page);
  const jump = page.getByRole('navigation', { name: /Jump to/ }).getByRole('button');
  const count = await jump.count();
  expect(count).toBeGreaterThan(3);
  for (let i = 0; i < count; i++) {
    const box = await jump.nth(i).boundingBox();
    expect(box?.height ?? 0, 'jump link ' + i).toBeGreaterThanOrEqual(24);
  }
  const erase = page.getByRole('button', { name: 'Erase this case' }).first();
  if (await erase.count()) expect((await erase.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(24);
  await page.getByRole('button', { name: 'Glossary', exact: true }).first().click();
  for (const id of ['jump-updates', 'jump-logs']) {
    expect((await page.getByTestId(id).boundingBox())?.height ?? 0, id).toBeGreaterThanOrEqual(24);
  }
});
