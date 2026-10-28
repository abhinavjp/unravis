// Check for updates: the canned answers the demo gives, in Help and from the route.
import { test, expect, type Page } from '../support/fixtures';

async function check(page: Page, preview: string) {
  if (preview) {
    await page.evaluate((x) => {
      location.hash = '#update=' + x;
    }, preview);
  }
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  const dialog = page.getByRole('dialog').first();
  await dialog.getByTestId('update-check-button').click();
  const result = dialog.locator('[data-state]');
  await expect(result).toBeVisible();
  return { dialog, result };
}

test('UPD-01: latest says you are on the latest version and offers no release link', async ({ page }) => {
  const { dialog, result } = await check(page, 'latest');
  await expect(result).toHaveAttribute('data-state', 'latest');
  await expect(result).toHaveText('You are on the latest version (v1.2.0).');
  await expect(dialog.getByRole('link', { name: /release page/i })).toHaveCount(0);
});

test('UPD-02: available names both versions and links to the project releases only', async ({ page }) => {
  const { dialog, result } = await check(page, 'available');
  await expect(result).toHaveAttribute('data-state', 'available');
  await expect(result).toContainText('Version 1.3.0 is available. You have v1.2.0.');
  const link = dialog.getByRole('link', { name: /release page/i });
  await expect(link).toHaveAttribute('href', /^https:\/\/github\.com\/abhinavjp\/unravis\//);
  await expect(link).toHaveAttribute('rel', /noopener/);
  await expect(link).toHaveAttribute('target', '_blank');
});

test('UPD-03: offline says it could not check, in words, with no link', async ({ page }) => {
  const { dialog, result } = await check(page, 'offline');
  await expect(result).toHaveAttribute('data-state', 'unreachable');
  await expect(result).toContainText('Could not check for updates. Check your connection and try again later.');
  await expect(dialog.getByRole('link', { name: /release page/i })).toHaveCount(0);
});

test('UPD-04: a development build says there is nothing to compare', async ({ page }) => {
  const { result } = await check(page, '');
  await expect(result).toHaveAttribute('data-state', 'dev');
  await expect(result).toContainText('This is a development build, so there is nothing to compare.');
});

test('UPD-06: the route always answers 200 with one of four statuses, and GET is refused', async ({ api }) => {
  const latest = await api('POST', '/api/update-check?preview=latest');
  expect(latest.status).toBe(200);
  expect(latest.json.status).toBe('latest');
  expect(latest.json.current).toBe('1.2.0');
  const available = await api('POST', '/api/update-check?preview=available');
  expect(available.status).toBe(200);
  expect(available.json.status).toBe('available');
  expect(available.json.latest).toBe('1.3.0');
  expect(available.json.url).toBe('https://github.com/abhinavjp/unravis/releases/tag/v1.3.0');
  const offline = await api('POST', '/api/update-check?preview=offline');
  expect(offline.status).toBe(200);
  expect(offline.json.status).toBe('unreachable');
  const none = await api('POST', '/api/update-check');
  expect(none.status).toBe(200);
  expect(none.json.status).toBe('dev');
  const bogus = await api('POST', '/api/update-check?preview=bogus');
  expect(bogus.status).toBe(200);
  expect(['dev', 'latest', 'available', 'unreachable']).toContain(bogus.json.status);
  const get = await api('GET', '/api/update-check');
  expect(get.status).toBe(405);
});
