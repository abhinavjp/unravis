// A file the Import files popup cannot read is listed on the Check step, never sent, and left for the person to sort out.
//   IP-11  "This file is not supported": Remove drops the file, "Tell us what this is" picks a kind, and an archive only offers Remove from list.
//   IP-18  One unsupported file and a single-file Check group use singular counts.
// The check of server.log is played back with no reason code, which is the case that shows the plain sentence. The archive is turned
// away by the page itself (a .zip that cannot be opened is never sent), so it needs no server answer.
import { test, expect, type Page } from '../support/fixtures';
import type { Locator } from '@playwright/test';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, type ApiFn } from '../support/api';

const JOB_ID = 'd'.repeat(32);
const KINDS = ['IIS web log', 'log4net', 'SQL error log', 'Windows event log', 'Hangfire', 'other'];

/** Makes a case and opens the popup over its page. */
async function openPopup(page: Page, api: ApiFn, title: string): Promise<Locator> {
  await createCase(api, title);
  await goHome(page);
  await openCase(page, title);
  await page.getByRole('button', { name: 'Add a file' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Import files' });
  await expect(dialog).toBeVisible();
  return dialog;
}

/** Answers checks with "not supported" and no reason, except an optional ready file, and never calls the server. */
async function mockNotSupported(page: Page, readyName = '') {
  await page.evaluate(() => {
    const w = window as unknown as { __checkKeys: string[] };
    const original = window.fetch.bind(window);
    window.fetch = (input, init) => {
      if (init?.body instanceof FormData && String(input).includes('/imports/check')) w.__checkKeys = init.body.getAll('key').map(String);
      return original(input, init);
    };
  });
  await page.route(/\/api\/cases\/[^/]+\/imports\/check/, async (route) => {
    const keys = await page.evaluate(() => (window as unknown as { __checkKeys?: string[] }).__checkKeys ?? []);
    const files = keys.map((key) => {
      const [name, size] = key.split('|');
      return { key, name, size: Number(size), verdict: name === readyName ? 'ready' : 'not-supported', importer: name === readyName ? 'iis-w3c' : '', tied: [], serverName: '', needsZone: false, reason: '', rows: 0, emitted: 0, rejected: 0 };
    });
    const result = { files, checkedAt: '2026-10-20T09:00:00Z' };
    await route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ id: JOB_ID, state: 'done', kind: 'check', bytesRead: 0, bytesTotal: 0, result }) });
  });
}

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('IP-11: a not-supported file says so, Remove drops it, and "Tell us what this is" picks a kind', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Unsupported');
  await mockNotSupported(page);
  await dialog.getByLabel('Choose files to import').setInputFiles([
    { name: 'app-backup.zip', mimeType: 'application/zip', buffer: Buffer.from('this is not an archive') },
    { name: 'server.log', mimeType: 'text/plain', buffer: Buffer.from('x\n') }
  ]);
  // Neither name tells the kind, so Choose lists both under "Not recognised yet".
  await expect(dialog.locator('[data-grp="unsupported"]')).toContainText('2 files');
  await dialog.getByRole('button', { name: 'Continue' }).click();
  await expect(dialog.getByRole('checkbox', { name: 'Confirm read-only' })).toBeVisible({ timeout: 30_000 });

  const group = dialog.locator('.notsupp');
  await expect(group.locator('.notsupp-head')).toContainText('2 files are not supported');
  const zip = group.locator('.notsupp-row').filter({ hasText: 'app-backup.zip' });
  const log = group.locator('.notsupp-row').filter({ hasText: 'server.log' });
  await expect(log.locator('.reason')).toHaveText('This file is not supported');
  await expect(zip.locator('.reason')).toContainText('This is a compressed file. Extract it first');
  // An archive cannot be told apart by naming a kind, so it only offers Remove from list.
  await expect(zip.getByRole('combobox')).toHaveCount(0);
  await expect(zip.getByRole('button', { name: 'Remove from list', exact: true })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Import 0 files', exact: true })).toBeDisabled();

  // The kind picker lists the kinds in the wireframe's order, and keeps the one picked.
  const pick = log.getByRole('combobox', { name: 'Tell us what this is' });
  await expect(pick.locator('option')).toHaveText(['Tell us what this is', ...KINDS]);
  await expect(pick).toHaveValue('');
  await pick.selectOption('IIS web log');
  await expect(pick).toHaveValue('IIS web log');

  await log.getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(group.locator('.notsupp-row').filter({ hasText: 'server.log' })).toHaveCount(0);
  await expect(group.locator('.notsupp-head')).toHaveText('1 file is not supported');

  await zip.getByRole('button', { name: 'Remove from list' }).click();
  await expect(group).toContainText('All unsupported files removed.');
  await expect(group.locator('.notsupp-row').filter({ hasText: 'app-backup.zip' })).toHaveCount(0);
});

test('IP-18: one unsupported file and a single-file group use singular copy in Check', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Singular');
  await mockNotSupported(page, 'u_ex261018.log');
  await dialog.getByLabel('Choose files to import').setInputFiles([
    { name: 'server.log', mimeType: 'text/plain', buffer: Buffer.from('x\n') }
  ]);

  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route(/\/api\/cases\/[^/]+\/imports\/check/, async (route) => {
    await gate;
    await route.fallback();
  }, { times: 1 });
  await dialog.getByRole('button', { name: 'Continue' }).click();
  try {
    await expect(dialog.locator('.checking')).toContainText('Checking 1 file with the server…');
  } finally {
    release();
  }
  await expect(dialog.locator('.notsupp-head')).toHaveText('1 file is not supported');
  await expect(dialog.locator('.notsupp-row')).toHaveCount(1);

  // Keep the unsupported file and add one ready file, so Check also draws a selectable single-file group.
  await dialog.locator('.stepper .stp').first().click();
  await dialog.getByLabel('Choose files to import').setInputFiles([
    { name: 'u_ex261018.log', mimeType: 'text/plain', buffer: Buffer.from('x\n') }
  ]);
  await dialog.getByRole('button', { name: 'Continue' }).click();
  const ready = dialog.locator('[data-grp="kind:iis-w3c"]');
  await expect(ready.locator('.grp-meta')).toHaveText('1 file, ready');
  await ready.locator('.grp-head').click();
  await expect(ready.locator('.grp-all')).toHaveText('All 1 file');
  await expect(dialog.locator('.notsupp-head')).toHaveText('1 file is not supported');
  await expect(dialog.locator('.notsupp-row')).toHaveCount(1);
});
