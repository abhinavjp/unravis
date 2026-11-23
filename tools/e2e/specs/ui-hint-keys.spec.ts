// BL-495f: how a "?" hint (the glossary popover) opens and closes. Decision (2026-11-23, owner's rule: decide small choices): it opens on a click and
// on Enter or Space with the keyboard focus on the button (the button's own click), takes the focus into the popover, closes on Esc (focus back on the
// button), on Tab past either end, and on a click or focus anywhere else. It never opens on hover or on focus alone: touch cannot hover, and a popover
// that opens when the focus only passes by would pull the focus away from a person who is tabbing through the page.
//   HINTKEY-01  hover alone, and focus alone, open nothing
//   HINTKEY-02  Enter opens it with the focus inside, Esc closes it and gives the focus back
//   HINTKEY-03  Space opens it, Tab out of the popover closes it and the focus goes on from the button
//   HINTKEY-04  a click opens it, a click elsewhere closes it, and the button says it opens a dialog
import { test, expect, type Page } from '../support/fixtures';

const button = (page: Page) => page.getByRole('button', { name: /^What does .* mean\?$/ }).first();
const popover = (page: Page) => page.locator('.hint-popover');

test('HINTKEY-01 hover alone and focus alone do not open a hint', async ({ page }) => {
  const b = button(page);
  await expect(b).toBeVisible();
  await b.hover();
  await page.waitForTimeout(400);
  await expect(popover(page)).toHaveCount(0);
  await page.mouse.move(5, 5);
  await b.focus();
  await page.waitForTimeout(400);
  await expect(popover(page)).toHaveCount(0);
  await expect(b).toHaveAttribute('aria-expanded', 'false');
});

test('HINTKEY-02 Enter opens the hint with the focus inside, Esc closes it and gives the focus back', async ({ page }) => {
  const b = button(page);
  await b.focus();
  await page.keyboard.press('Enter');
  await expect(popover(page)).toBeVisible();
  await expect(b).toHaveAttribute('aria-expanded', 'true');
  await expect(popover(page)).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(popover(page)).toHaveCount(0);
  await expect(b).toBeFocused();
  await expect(b).toHaveAttribute('aria-expanded', 'false');
});

test('HINTKEY-03 Space opens the hint and Tab out of it closes it, the focus goes on from the button', async ({ page }) => {
  const b = button(page);
  await b.focus();
  await page.keyboard.press('Space');
  await expect(popover(page)).toBeFocused();
  // popover, then "Learn more in the glossary", then past the end.
  await page.keyboard.press('Tab');
  await expect(popover(page).getByRole('button', { name: 'Learn more in the glossary' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(popover(page)).toHaveCount(0);
  await expect(b).not.toBeFocused();
  await expect(b).toHaveAttribute('aria-expanded', 'false');
});

test('HINTKEY-04 a click opens the hint, a click elsewhere closes it, and the button says it opens a dialog', async ({ page }) => {
  const b = button(page);
  await expect(b).toHaveAttribute('aria-haspopup', 'dialog');
  await b.click();
  await expect(popover(page)).toBeVisible();
  await page.mouse.click(700, 880);
  await expect(popover(page)).toHaveCount(0);
});
