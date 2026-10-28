// Help and glossary, the build line and the in-app user guide.
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { test, expect, type Page } from '../support/fixtures';
import { exePath } from '../support/launcher';

const ROOT = resolve(__dirname, '..', '..', '..');
const SLUGS = ['index', 'getting-started', 'install', 'cases', 'sources', 'blackbox', 'guided-analysis', 'manual-tools', 'upgrade-and-uninstall', 'troubleshooting', 'security'];

async function openHelp(page: Page) {
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  const dialog = page.getByRole('dialog').first();
  await expect(dialog.locator('#glossary-title')).toHaveText('Help and glossary');
  return dialog;
}

test('HELP-01: Help opens with the start steps, a search field, the glossary, the build line and the guide button', async ({ page }) => {
  const dialog = await openHelp(page);
  await expect(dialog.locator('#help-start-title')).toHaveText('Start here');
  await expect(dialog.getByLabel('Search terms')).toBeVisible();
  await expect(dialog.locator('#glossary-count')).toHaveText(/^\d+ terms$/);
  await expect(dialog.getByRole('heading', { name: 'Glossary', exact: true })).toBeVisible();
  await expect(dialog.getByTestId('build-line')).toBeVisible();
  await expect(dialog.getByTestId('guide-open')).toHaveText('Open the user guide');
  await expect(dialog.getByTestId('update-check-button')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Close help and glossary' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  const again = await openHelp(page);
  await expect(again.locator('#glossary-count')).toHaveText(/^\d+ terms$/);
});

test('HELP-02: searching narrows the list, nothing found says so in words, and clearing restores it', async ({ page }) => {
  const dialog = await openHelp(page);
  const count = dialog.locator('#glossary-count');
  const full = (await count.innerText()).trim();
  const field = dialog.getByLabel('Search terms');
  await field.fill('P95');
  await expect(count).not.toHaveText(full);
  await field.fill('zzzz');
  await expect(dialog.getByText('No terms match "zzzz". Try P95, incomplete, episode or coverage.')).toBeVisible();
  await field.fill('');
  await expect(count).toHaveText(full);
  await field.fill('<img src=x onerror=alert(1)>');
  await expect(dialog.locator('img')).toHaveCount(0);
});

test('HELP-03: the build line holds a version and no folder, user name or secret', async ({ page }) => {
  const dialog = await openHelp(page);
  const line = (await dialog.getByTestId('build-line').innerText()).trim();
  const run = spawnSync(exePath(), ['--version'], { encoding: 'utf8', windowsHide: true });
  const version = run.stdout.replace(/^Unravis\s*/i, '').trim();
  expect(line, 'the build line carries the same version as --version').toContain(version);
  expect(line).not.toMatch(/[A-Za-z]:\\|\/Users\/|token|password|key/i);
});

test('HELP-04: the guide has the 11 pages in order, the current one is marked, and no screenshots page', async ({ page }) => {
  const dialog = await openHelp(page);
  await dialog.getByTestId('guide-open').click();
  const nav = dialog.locator('[data-testid^="guide-nav-"]');
  await expect(nav).toHaveCount(11);
  const ids = await nav.evaluateAll((els) => els.map((e) => e.getAttribute('data-testid')));
  expect(ids).toEqual(SLUGS.map((s) => 'guide-nav-' + s));
  await expect(dialog.getByTestId('guide-nav-index')).toHaveAttribute('aria-current', 'page');
  await expect(dialog.getByTestId('guide-nav-screenshots')).toHaveCount(0);
  await expect(dialog.getByTestId('guide-body')).toContainText('Unravis user guide');
  for (const slug of ['troubleshooting', 'install', 'security']) {
    await dialog.getByTestId('guide-nav-' + slug).click();
    await expect(dialog.getByTestId('guide-nav-' + slug)).toHaveAttribute('aria-current', 'page');
  }
  await expect(dialog.getByTestId('guide-body').getByRole('heading').first()).toBeVisible();
});

test('HELP-05: Back from the guide shows Help again with focus on the guide button', async ({ page }) => {
  const dialog = await openHelp(page);
  await dialog.getByTestId('guide-open').click();
  await dialog.getByTestId('guide-nav-cases').click();
  await dialog.getByTestId('guide-back').click();
  await expect(dialog.getByTestId('guide-open')).toBeVisible();
  await expect(dialog.getByTestId('guide-open')).toBeFocused();
});

test('HELP-13: the guide reader never puts text into the page as markup', async () => {
  const src = readFileSync(join(ROOT, 'web', 'src', 'components', 'HelpGuide.svelte'), 'utf8');
  expect(src.includes('{@html'), 'HelpGuide.svelte renders raw html').toBe(false);
});
