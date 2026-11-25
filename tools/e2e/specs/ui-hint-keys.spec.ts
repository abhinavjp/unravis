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

/** Watches the page for `ms` and says whether a hint popover was ever in it (a MutationObserver sees one that shows and goes within a frame). */
const popoverEverAppears = (page: Page, ms: number) =>
  page.evaluate(
    (wait) =>
      new Promise<boolean>((done) => {
        let seen = !!document.querySelector('.hint-popover');
        const watch = new MutationObserver(() => { if (document.querySelector('.hint-popover')) seen = true; });
        watch.observe(document.body, { childList: true, subtree: true });
        setTimeout(() => { watch.disconnect(); done(seen); }, wait);
      }),
    ms
  );

test('HINTKEY-01 hover alone and focus alone do not open a hint', async ({ page }) => {
  const b = button(page);
  await expect(b).toBeVisible();
  // "Nothing happens" is watched for 400 ms by an observer in the page, so a popover that shows and goes between two checks is seen too.
  const hovered = popoverEverAppears(page, 400);
  await b.hover();
  expect(await hovered).toBe(false);
  await page.mouse.move(5, 5);
  const focused = popoverEverAppears(page, 400);
  await b.focus();
  expect(await focused).toBe(false);
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
  // A press on neutral page chrome, not a coordinate that may hold a control after a layout change.
  await page.locator('.brand-name').click();
  await expect(popover(page)).toHaveCount(0);
});

test('SETUP-A11Y-15: the screen has one page heading, the brand is not a heading, and hint buttons sit beside headings', async ({ page }) => {
  const nestedHints = () => page.locator('h1,h2,h3,h4,h5,h6').evaluateAll(heads => heads.flatMap(head => [...head.querySelectorAll('.hint-button')]).map(button => button.textContent));
  await page.getByRole('button', { name: 'Where is it?', exact: true }).click();
  await page.getByRole('button', { name: /^\d+ Payroll\.Run synchronous/ }).click();
  const panel = page.getByRole('complementary', { name: 'Payroll.Run' });
  await panel.getByRole('tab', { name: 'Incomplete' }).click();
  await expect(panel.getByRole('heading', { name: 'Incomplete requests' })).toBeVisible();
  expect(await nestedHints(), 'request view hint buttons nested in headings').toEqual([]);
  await page.getByRole('button', { name: 'Patterns', exact: true }).click();
  await expect(page.locator('.patterns-drawer')).toBeVisible();
  expect(await nestedHints(), 'patterns hint buttons nested in headings').toEqual([]);
  await page.keyboard.press('Escape');
  await expect(page.locator('.patterns-drawer')).toHaveCount(0);
  await page.getByRole('button', { name: 'Export', exact: true }).click();
  await page.getByRole('button', { name: 'Preview export', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'What is hidden' })).toBeVisible();
  expect(await nestedHints(), 'export hint buttons nested in headings').toEqual([]);
  await expect(page.locator('h1')).toHaveCount(1);
  await expect(page.locator('h1')).toHaveText('Evidence');
  // the h1 is the first heading of the document, ahead of the filter panel's h2 and h3 headings
  expect(await page.locator('h1, h2, h3, h4, h5, h6').first().evaluate((el) => el.tagName)).toBe('H1');
  await expect(page.locator('.brand-lockup h1')).toHaveCount(0);
});
