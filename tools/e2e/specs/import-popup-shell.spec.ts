// The Import files popup (wireframe: prototype/import-popup-WIREFRAME.html): its steps, phone layout, progress and result.
//   IP-01  Choose lists the files grouped by kind; Continue starts the check.
//   IP-02  Check shows the ready group with a box for every file; unticking a file lowers the Import count.
//   IP-05  At phone width (390x844) the popup fills the screen and has no rounded corners.
//   IP-12  The Importing step shows progress for each group; Cancel stops after the file being sent.
//   IP-13  The Result step counts the files, lists Imported and Not imported, and names the computers that were added.
//   IP-16  A folder of 2,000 files: Choose lists them, Check shows a spinner and then the verdicts, Import completes.
import { test, expect, type Page } from '../support/fixtures';
import type { Locator } from '@playwright/test';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, type ApiFn } from '../support/api';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const FIELDS =
  '#Fields: date time s-sitename s-computername s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) cs(Cookie) cs(Referer) sc-status sc-substatus sc-win32-status time-taken';

/** A small IIS log written by `server`. `tag` changes the rows, so no two files are the same file. */
function w3c(server: string, tag: string): string {
  const rows = [1, 2, 3].map((i) => `2026-10-20 08:00:0${i} W3SVC1 ${server} 192.0.2.10 GET /api/v1/${tag}/${i} - 443 - 192.0.2.77 Mozilla/5.0 - - 200 0 0 ${100 + i}`);
  return ['#Software: Microsoft Internet Information Services 10.0', '#Version: 1.0', '#Date: 2026-10-20 08:00:00', FIELDS, ...rows, ''].join('\n');
}
const iis = (name: string, server: string) => ({ name, mimeType: 'text/plain', buffer: Buffer.from(w3c(server, name)) });
const text = (name: string, body = 'x\n') => ({ name, mimeType: 'text/plain', buffer: Buffer.from(body) });

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

const picker = (dialog: Locator) => dialog.getByLabel('Choose files to import');
const ack = (dialog: Locator) => dialog.getByRole('checkbox', { name: 'Confirm read-only' });
const importButton = (dialog: Locator, n: number) => dialog.getByRole('button', { name: `Import ${n} ${n === 1 ? 'file' : 'files'}`, exact: true });

/** Presses Continue and waits until the check has answered (the read-only box is drawn only then). */
async function runCheck(dialog: Locator) {
  await dialog.getByRole('button', { name: 'Continue' }).click();
  await expect(ack(dialog)).toBeVisible({ timeout: 30_000 });
}

const JOB_ID = 'a'.repeat(32);
const jobBody = (result: unknown, kind: 'check' | 'import') => JSON.stringify({ id: JOB_ID, state: 'done', kind, bytesRead: 0, bytesTotal: 0, result });

/** Remembers the file keys of the latest check request: a route handler cannot rely on reading a body that carries files. */
async function watchCheckKeys(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __checkKeys: string[] };
    const original = window.fetch.bind(window);
    window.fetch = (input, init) => {
      if (init?.body instanceof FormData && String(input).includes('/imports/check')) w.__checkKeys = init.body.getAll('key').map(String);
      return original(input, init);
    };
  });
}
const checkKeys = (page: Page): Promise<string[]> => page.evaluate(() => (window as unknown as { __checkKeys?: string[] }).__checkKeys ?? []);

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('IP-01: Choose lists the files grouped by kind, and Continue starts the check', async ({ page, api }) => {
  // Held back, so the spinner can be seen.
  await page.route(/\/api\/cases\/[^/]+\/imports\/check/, async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    await route.continue();
  });
  const dialog = await openPopup(page, api, 'QA Popup Choose');
  await expect(dialog.locator('.prog-line')).toContainText('Step 1 of 3 · Choose files to import');
  await expect(dialog.getByText('Nothing is sent until you press Import.')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Continue' })).toBeDisabled();

  await picker(dialog).setInputFiles([iis('u_ex261001.log', 'web-a'), iis('u_ex261002.log', 'web-a'), text('ERRORLOG'), text('log.txt'), text('notes.txt')]);
  await expect(dialog.locator('[data-grp] .grp-name')).toHaveText(['IIS web logs', 'SQL error log', 'log4net', 'Not recognised yet']);
  const iisGroup = dialog.locator('[data-grp="kind:iis-w3c"]');
  await expect(iisGroup).toContainText('2 files');
  await expect(iisGroup).toContainText('found');
  await expect(dialog.locator('[data-grp="kind:sql-errorlog"]')).toContainText('1 file');
  // A kind the names do not tell is always open, so a problem file cannot hide.
  await expect(dialog.locator('[data-grp="unsupported"]')).toContainText('notes.txt');
  await expect(dialog.locator('[data-grp="unsupported"]')).toContainText('needs checking');
  await expect(iisGroup).not.toContainText('u_ex261001.log');
  await iisGroup.getByRole('button').click();
  await expect(iisGroup).toContainText('u_ex261001.log');
  await expect(iisGroup).toContainText('u_ex261002.log');

  const sent = page.waitForRequest((r) => r.method() === 'POST' && r.url().includes('/imports/check'));
  await dialog.getByRole('button', { name: 'Continue' }).click();
  await sent;
  await expect(dialog.locator('.prog-line')).toContainText('Step 2 of 3 · Check files with the server before import');
  await expect(dialog.getByRole('status')).toContainText('Checking 5 files with the server…');
  await expect(dialog.getByText('Nothing is imported yet.')).toBeVisible();
  await expect(ack(dialog)).toBeVisible({ timeout: 30_000 });
  await expect(dialog.locator('[data-grp="kind:iis-w3c"]')).toContainText('2 files, ready');
});

test('IP-02: Check shows the ready group with a box for each file, and unticking a file lowers the Import count', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Ready');
  await picker(dialog).setInputFiles([iis('u_ex261011.log', 'web-a'), iis('u_ex261012.log', 'web-a'), iis('u_ex261013.log', 'web-a')]);
  await runCheck(dialog);

  const group = dialog.locator('[data-grp="kind:iis-w3c"]');
  await expect(group).toContainText('3 files, ready');
  await expect(group.locator('.grp-badge')).toHaveText('ready');
  await expect(dialog.getByText(/needed before you can import/)).toHaveCount(0);
  // The count and the button wait for the read-only box.
  await expect(importButton(dialog, 3)).toBeDisabled();
  await ack(dialog).check();
  await expect(importButton(dialog, 3)).toBeEnabled();

  await group.getByRole('button').click();
  for (const name of ['u_ex261011.log', 'u_ex261012.log', 'u_ex261013.log']) await expect(dialog.getByRole('checkbox', { name })).toBeChecked();
  await dialog.getByRole('checkbox', { name: 'u_ex261012.log' }).uncheck();
  await expect(importButton(dialog, 2)).toBeEnabled();
  await expect(dialog.getByRole('checkbox', { name: 'All 3 files' })).not.toBeChecked();

  // The "All" box ticks everything again when some are off, and clears everything when all are on.
  await dialog.getByRole('checkbox', { name: 'All 3 files' }).click();
  await expect(importButton(dialog, 3)).toBeEnabled();
  await dialog.getByRole('checkbox', { name: 'All 3 files' }).click();
  await expect(importButton(dialog, 0)).toBeDisabled();
});

test('IP-05: at phone width (390x844) the popup fills the screen and has no rounded corners', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Phone');
  const scrim = page.getByRole('button', { name: 'Close import' }).first();
  const wide = (await dialog.boundingBox())!;
  expect(wide.width).toBeLessThan(1440);
  await expect(dialog).not.toHaveCSS('border-top-left-radius', '0px');

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(async () => await dialog.boundingBox()).toEqual({ x: 0, y: 0, width: 390, height: 844 });
  for (const corner of ['top-left', 'top-right', 'bottom-left', 'bottom-right']) await expect(dialog).toHaveCSS(`border-${corner}-radius`, '0px');
  await expect(scrim).toHaveCSS('border-top-left-radius', '0px');
  expect(await scrim.boundingBox()).toEqual({ x: 0, y: 0, width: 390, height: 844 });
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);

  // Touch size and every step still reachable: the footer button is on screen.
  const next = dialog.getByRole('button', { name: 'Continue' });
  expect((await next.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  const foot = (await next.boundingBox())!;
  expect(foot.x + foot.width).toBeLessThanOrEqual(390);
  expect(foot.y + foot.height).toBeLessThanOrEqual(844);
});

test('IP-12: the Importing step shows progress for each group, and Cancel stops after the file being sent', async ({ page, api }) => {
  test.setTimeout(120_000);
  // The first read is held briefly, so the 0 of 3 state can be seen. The second is held for longer, so Cancel can be pressed while that file is being sent.
  let reads = 0;
  await page.route(/\/api\/cases\/[^/]+\/imports\?/, async (route) => {
    if (route.request().url().includes('preview=1')) {
      reads += 1;
      if (reads <= 2) await new Promise((resolve) => setTimeout(resolve, reads === 1 ? 1500 : 5000));
    }
    await route.continue();
  });
  const dialog = await openPopup(page, api, 'QA Popup Cancel');
  await picker(dialog).setInputFiles([iis('u_ex261021.log', 'web-a'), iis('u_ex261022.log', 'web-a'), iis('u_ex261023.log', 'web-a')]);
  await runCheck(dialog);
  await ack(dialog).check();
  await importButton(dialog, 3).click();

  await expect(dialog.locator('.prog-line')).toContainText('Step 3 of 3 · Importing — do not close');
  await expect(dialog.getByRole('progressbar', { name: 'Importing 0 of 3 files' })).toBeVisible();
  await expect(dialog.locator('.imp-grp')).toContainText('IIS web logs');
  await expect(dialog.locator('.imp-grp')).toContainText('0 of 3');
  await expect(dialog.getByRole('progressbar', { name: 'Importing 1 of 3 files' })).toBeVisible({ timeout: 30_000 });
  await expect(dialog.locator('.imp-grp')).toContainText('1 of 3');

  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog.locator('.prog-line')).toContainText('Step 2 of 3');
  // The file being sent finishes and leaves the list; the third is never started.
  await expect(importButton(dialog, 1)).toBeEnabled({ timeout: 45_000 });
  await expect.poll(async () => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string; imports: number }>).find((c) => c.title === 'QA Popup Cancel')?.imports, { timeout: 15_000 }).toBe(2);
});

test('IP-13: the Result step counts the files, lists Imported and Not imported, and names the computers added', async ({ page, api }) => {
  test.setTimeout(150_000);
  const dialog = await openPopup(page, api, 'QA Popup Result');
  await picker(dialog).setInputFiles([iis('u_ex261031.log', 'iis_alpha'), iis('u_ex261032.log', 'iis_alpha'), iis('u_ex261033.log', 'iis_beta'), text('app-backup.zip', 'this is not an archive')]);
  await runCheck(dialog);
  await dialog.getByRole('button', { name: 'Add all 2 servers' }).first().click();
  await expect(dialog.getByText(/needed before you can import/)).toHaveCount(0);
  await ack(dialog).check();
  await importButton(dialog, 3).click();

  await expect(dialog.locator('.prog-line')).toContainText('Step 3 of 3 · Result', { timeout: 100_000 });
  await expect(dialog.locator('.result-hero')).toContainText('3 files imported');
  await expect(dialog.locator('.result-section h4')).toHaveText(['Imported', 'Not imported']);
  const imported = dialog.locator('.result-section').filter({ hasText: 'IIS web logs' });
  await expect(imported).toContainText('3 files');
  const left = dialog.locator('.result-section').filter({ hasText: 'app-backup.zip' });
  await expect(left).toContainText('Extract it first');
  await expect(dialog.locator('.computers-line')).toContainText(/Added 2 computers:\s*iis_alpha, iis_beta\./);
  await expect(dialog.getByRole('button', { name: 'See computers' })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Open timeline' })).toBeVisible();

  await dialog.getByRole('button', { name: 'Import more' }).click();
  await expect(dialog.locator('.prog-line')).toContainText('Step 1 of 3');
  await expect(dialog.locator('[data-grp]')).toHaveCount(0);
});

test('IP-16: a folder of 2,000 files is listed without a lockup, checked behind a spinner, and imported', async ({ page, api }) => {
  test.setTimeout(240_000);
  // 2,000 files take hours to read one by one on a real server, so the server's answers are played back here.
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  await page.route(/\/api\/cases\/[^/]+\/imports\/check/, async (route) => {
    await gate;
    const files = (await checkKeys(page)).map((key) => {
      const [name, size] = key.split('|');
      return { key, name, size: Number(size), verdict: 'ready', importer: 'iis-w3c', tied: [], serverName: '', needsZone: false, reason: '', rows: 3, emitted: 3, rejected: 0 };
    });
    await route.fulfill({ status: 202, contentType: 'application/json', body: jobBody({ files, checkedAt: '2026-10-20T09:00:00Z' }, 'check') });
  });
  let saved = 0;
  await page.route(/\/api\/cases\/[^/]+\/imports\?/, async (route) => {
    if (route.request().method() !== 'POST') {
      await route.fallback();
      return;
    }
    const preview = route.request().url().includes('preview=1');
    const report = { importer: 'iis-w3c', rows: 3, emitted: 3, rejected: 0, errors: [], observedHosts: [], first: null, last: null, quality: {}, fieldRules: {}, knownGaps: [] };
    const record = preview ? null : { id: ++saved, importer: 'iis-w3c', fileName: 'x.log', sha256: '', bytes: 1, rows: 3, rejected: 0, hostLabel: 'web-a', tzRule: 'utc-field', quality: {}, knownGaps: [], imported: '2026-10-20T09:00:00Z', status: 'ok' };
    await route.fulfill({ status: 202, contentType: 'application/json', body: jobBody({ importer: 'iis-w3c', score: 1, tied: [], report, record }, 'import') });
  });

  const folder = mkdtempSync(join(tmpdir(), 'unravis-ip16-'));
  try {
    const logs = join(folder, 'logs-2000');
    mkdirSync(logs);
    for (let i = 0; i < 2000; i++) writeFileSync(join(logs, `u_ex${String(i).padStart(4, '0')}.log`), `#Software: sample ${i}\n`);
    const dialog = await openPopup(page, api, 'QA Popup Two Thousand');
    await watchCheckKeys(page);

    const started = Date.now();
    await dialog.getByLabel('Choose a folder to import').setInputFiles(logs);
    const group = dialog.locator('[data-grp="kind:iis-w3c"]');
    await expect(group).toContainText('2000 files · folder logs-2000', { timeout: 60_000 });
    await group.getByRole('button').click();
    await expect(group.locator('.file-row')).toHaveCount(2000, { timeout: 60_000 });
    // The page still answers while the list is drawn.
    expect(await page.evaluate(() => 1 + 1)).toBe(2);
    expect(Date.now() - started).toBeLessThan(90_000);

    await dialog.getByRole('button', { name: 'Continue' }).click();
    await expect(dialog.getByRole('status')).toContainText('Checking 2000 files with the server…');
    await expect(ack(dialog)).toHaveCount(0);
    release();
    await expect(ack(dialog)).toBeVisible({ timeout: 60_000 });
    await expect(dialog.locator('[data-grp="kind:iis-w3c"]')).toContainText('2000 files, ready');
    await ack(dialog).check();
    await importButton(dialog, 2000).click();

    await expect(dialog.getByRole('progressbar', { name: /^Importing \d+ of 2000 files$/ })).toBeVisible({ timeout: 30_000 });
    await expect(dialog.locator('.prog-line')).toContainText('Step 3 of 3 · Result', { timeout: 180_000 });
    await expect(dialog.locator('.result-hero')).toContainText('2000 files imported');
  } finally {
    release();
    rmSync(folder, { recursive: true, force: true });
  }
});
