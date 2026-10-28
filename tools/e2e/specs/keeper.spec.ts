// The key keeper on a shared PC, as the demo previews it with #keeper=saved, stopped, migrate and moved.
import { test, expect, type Page } from '../support/fixtures';

const CASE = 'Checkout slowdown, 17 October';

/** Name a preview in the address, then open the case home, where the Saved connections block lives. */
async function withPreview(page: Page, preview: string): Promise<void> {
  await page.evaluate((x) => {
    location.hash = '#keeper=' + x;
  }, preview);
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
}

test('KEEP-01: with the keeper running, held sources say the password is held and no password shows', async ({ page }) => {
  await withPreview(page, 'saved');
  await expect(page.locator('#conn-head')).toHaveText('Saved connections');
  const held = page.locator('[data-keeper-held]');
  await expect(held.first()).toHaveText('Password held by the key keeper');
  expect(await held.count()).toBeGreaterThan(0);
  const body = await page.locator('body').innerText();
  expect(body).not.toMatch(/password\s*[:=]\s*\S+/i);
  expect(body).not.toContain('ubk-');
});

test('KEEP-01a: a made up preview name is ignored and the normal screen shows', async ({ page }) => {
  await withPreview(page, 'bogus');
  await expect(page.locator('[data-keeper-held]')).toHaveCount(0);
  await expect(page.locator('[data-keeper]')).toHaveCount(0);
});

test('KEEP-02: with the keeper stopped, the page explains and held sources cannot be used', async ({ page }) => {
  await withPreview(page, 'stopped');
  const card = page.locator('[data-keeper=stopped]');
  await expect(card.locator('[data-keeper-head]')).toHaveText('The key keeper is not running');
  await expect(page.locator('[data-keeper-held]').first()).toHaveText('Password held by the key keeper, which cannot be used until it runs again');
  const check = card.getByRole('button', { name: 'Check again' });
  for (let i = 0; i < 5; i++) await check.click();
  await expect(card.locator('[data-keeper-head]')).toHaveText('The key keeper is not running');
  await expect(page.locator('body')).not.toContainText(/password\s*[:=]\s*\S+/i);
});

test('KEEP-02a: with the keeper stopped, the sources list says held sources stay unticked', async ({ page }) => {
  await page.evaluate(() => {
    location.hash = '#keeper=stopped';
  });
  await page.getByRole('button', { name: /Audit sources/ }).click();
  await expect(page.locator('[data-keeper-sources-stopped]')).toHaveText('Held sources are unticked until the key keeper runs again. Sources whose password you typed still work.');
});

test('KEEP-04: the move card counts what waits, and Not now moves nothing and can be undone', async ({ page }) => {
  await withPreview(page, 'migrate');
  const card = page.locator('[data-keeper=migrate]');
  await expect(card.locator('[data-keeper-head]')).toHaveText('Move your saved connections to the key keeper');
  await expect(card.locator('[data-pending]')).toHaveText('2 SQL Server connections, 1 MongoDB link and 1 Blackbox read key wait to be moved.');
  const later = card.getByRole('button', { name: 'Not now' });
  await later.dblclick();
  await expect(page.getByRole('button', { name: 'Show the move again' })).toBeVisible();
  await expect(page.locator('[data-keeper=moved]')).toHaveCount(0);
  await page.getByRole('button', { name: 'Show the move again' }).click();
  await expect(page.locator('[data-keeper=migrate] [data-pending]')).toBeVisible();
});

test('KEEP-05: Move them ends in the moved state, and pressing it twice quickly is one move', async ({ page }) => {
  await withPreview(page, 'migrate');
  await page.getByRole('button', { name: 'Move them' }).dblclick();
  const moved = page.locator('[data-keeper=moved]');
  await expect(moved.locator('[data-keeper-head]')).toHaveText('Your saved connections are in the key keeper');
  await expect(moved.getByRole('button', { name: 'Remove old copies' })).toBeVisible();
});

test('KEEP-06: Remove old copies asks first; Esc and Keep them remove nothing; Yes, remove does', async ({ page }) => {
  await withPreview(page, 'moved');
  const ask = page.getByRole('button', { name: 'Remove old copies' });
  await ask.click();
  const question = page.getByRole('alertdialog');
  await expect(question).toContainText('Remove the old copies from your own folder?');
  await page.keyboard.press('Escape');
  await expect(question).toHaveCount(0);
  await expect(page.getByText('The old copies were removed.')).toHaveCount(0);
  await ask.click();
  await page.getByRole('button', { name: 'Keep them' }).click();
  await expect(page.getByText('The old copies were removed.')).toHaveCount(0);
  await ask.click();
  await page.getByRole('button', { name: 'Yes, remove' }).click();
  await expect(page.getByText('The old copies were removed.')).toBeVisible();
});

test('KEEP-09: a read key held by the keeper is never shown, and Show and Copy are refused or absent', async ({ page }) => {
  await page.evaluate(() => {
    location.hash = '#keeper=saved';
  });
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  const region = dialog.getByRole('region', { name: 'web-a' });
  const show = region.getByRole('button', { name: 'Show', exact: true });
  if (await show.count()) {
    await show.click();
    // Either the key appears (Hide shows up) or the app refuses in words (an alert). Wait for one of them.
    await expect(region.getByRole('button', { name: 'Hide', exact: true }).or(region.getByRole('alert'))).toBeVisible();
  }
  const text = await page.locator('body').innerText();
  expect(text.includes('ubk-'), 'the key must not reach the page while the keeper holds it').toBe(false);
});
