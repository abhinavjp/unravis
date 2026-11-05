// The fix cards of the Check step in the Import files popup: a log that names a computer the list lacks, and a log that names none.
//   IP-06  A named unknown server: "Add {name} as a computer and accept {n} files" adds the computer and unlocks Import.
//   IP-07  "Add all N servers" resolves every open fix card.
//   IP-08  "Leave these N files out", then "Bring them back".
//   IP-09  A log with no computer suggests a name from its folder, and "Use {name} and accept {n} files" works.
//   IP-10  The time zone field appears only for SQL error log and Windows event log, never for IIS.
// IIS files go to the real server. The server has no reader for SQL error log or Windows event log files in every build, so the
// checks of IP-09 and IP-10 are played back here: what the card does with a verdict is what these two cases pin down.
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
const text = (name: string) => ({ name, mimeType: 'text/plain', buffer: Buffer.from('x\n') });

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
const fixesHint = (dialog: Locator, n: number) => dialog.getByText(`${n} ${n === 1 ? 'fix' : 'fixes'} needed before you can import`);
const hostsOf = async (api: ApiFn) => (await api('GET', '/api/hosts')).json.hosts as Array<{ label: string; tz?: string }>;

/** Presses Continue and waits until the check has answered (the read-only box is drawn only then). */
async function runCheck(dialog: Locator) {
  await dialog.getByRole('button', { name: 'Continue' }).click();
  await expect(ack(dialog)).toBeVisible({ timeout: 30_000 });
}

const JOB_ID = 'b'.repeat(32);

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

/**
 * Answers every check with `verdictFor(name)` and never calls the server. A file's name is read from its key (name|size|lastModified),
 * which holds the folder the file was picked from; the real server echoes only the file's base name.
 */
async function mockCheck(page: Page, verdictFor: (name: string) => Record<string, unknown>) {
  await watchCheckKeys(page);
  await page.route(/\/api\/cases\/[^/]+\/imports\/check/, async (route) => {
    const keys = await page.evaluate(() => (window as unknown as { __checkKeys?: string[] }).__checkKeys ?? []);
    const files = keys.map((key) => {
      const [name, size] = key.split('|');
      return { key, name, size: Number(size), verdict: 'ready', importer: '', tied: [], serverName: '', needsZone: false, reason: '', rows: 3, emitted: 3, rejected: 0, ...verdictFor(name) };
    });
    const result = { files, checkedAt: '2026-10-20T09:00:00Z' };
    await route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ id: JOB_ID, state: 'done', kind: 'check', bytesRead: 0, bytesTotal: 0, result }) });
  });
}

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('IP-06: a fix card for a named unknown server adds the computer, accepts its files and unlocks Import', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Fix One');
  await picker(dialog).setInputFiles([iis('u_ex261061.log', 'iis_sandboxapi'), iis('u_ex261062.log', 'iis_sandboxapi'), iis('u_ex261063.log', 'iis_sandboxapi')]);
  await runCheck(dialog);

  await expect(dialog.getByText('3 IIS files come from iis_sandboxapi, which is not on your computer list.')).toBeVisible();
  await expect(dialog.getByText('Unravis will not guess which computer wrote a log. Add it once and every file from that server is accepted.')).toBeVisible();
  await expect(dialog.getByText('IIS logs are already UTC, no time zone needed.')).toBeVisible();
  await expect(dialog.getByLabel('Time zone of its clock')).toHaveCount(0);
  await expect(fixesHint(dialog, 1)).toBeVisible();
  await expect(ack(dialog)).toBeDisabled();
  await expect(importButton(dialog, 3)).toBeDisabled();

  await dialog.getByRole('button', { name: 'Add iis_sandboxapi as a computer and accept 3 files' }).click();
  await expect(dialog.getByText('Added iis_sandboxapi, 3 files accepted')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Undo' })).toBeFocused();
  await expect(fixesHint(dialog, 1)).toHaveCount(0);
  expect((await hostsOf(api)).map((h) => h.label)).toContain('iis_sandboxapi');

  await expect(ack(dialog)).toBeEnabled();
  await ack(dialog).check();
  await expect(importButton(dialog, 3)).toBeEnabled();

  // Undo puts the card back and locks Import again.
  await dialog.getByRole('button', { name: 'Undo' }).click();
  await expect(fixesHint(dialog, 1)).toBeVisible();
  await expect(importButton(dialog, 3)).toBeDisabled();
});

test('IP-07: "Add all N servers" resolves every open fix card', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Fix All');
  await picker(dialog).setInputFiles([iis('u_ex261071.log', 'iis_alpha'), iis('u_ex261072.log', 'iis_alpha'), iis('u_ex261073.log', 'iis_beta')]);
  await runCheck(dialog);

  await expect(dialog.getByText('2 IIS files come from iis_alpha, which is not on your computer list.')).toBeVisible();
  await expect(dialog.getByText('1 IIS files come from iis_beta, which is not on your computer list.')).toBeVisible();
  await expect(fixesHint(dialog, 2)).toBeVisible();
  // One link on each open card.
  await expect(dialog.getByRole('button', { name: 'Add all 2 servers' })).toHaveCount(2);
  await dialog.getByRole('button', { name: 'Add all 2 servers' }).first().click();

  await expect(dialog.getByText('Added iis_alpha, 2 files accepted')).toBeVisible();
  await expect(dialog.getByText(/^Added iis_beta, 1 files? accepted$/)).toBeVisible();
  await expect(fixesHint(dialog, 2)).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: /^Add all \d+ servers$/ })).toHaveCount(0);
  await expect(ack(dialog)).toBeFocused();
  const labels = (await hostsOf(api)).map((h) => h.label);
  expect(labels).toEqual(expect.arrayContaining(['iis_alpha', 'iis_beta']));

  await ack(dialog).check();
  await expect(importButton(dialog, 3)).toBeEnabled();
});

test('IP-08: "Leave these N files out" drops the files from the count, and "Bring them back" restores the card', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Fix Leave');
  await picker(dialog).setInputFiles([iis('u_ex261081.log', 'web-a'), iis('u_ex261082.log', 'iis_gamma'), iis('u_ex261083.log', 'iis_gamma')]);
  await runCheck(dialog);
  await expect(fixesHint(dialog, 1)).toBeVisible();
  await expect(importButton(dialog, 3)).toBeDisabled();

  await dialog.getByRole('button', { name: 'Leave these 2 files out' }).click();
  await expect(dialog.getByText('2 IIS files left out')).toBeVisible();
  await expect(dialog.getByText('These files from iis_gamma were not added.')).toBeVisible();
  await expect(fixesHint(dialog, 1)).toHaveCount(0);
  await ack(dialog).check();
  await expect(importButton(dialog, 1)).toBeEnabled();

  await dialog.getByRole('button', { name: 'Bring them back' }).click();
  await expect(dialog.getByText('2 IIS files come from iis_gamma, which is not on your computer list.')).toBeVisible();
  await expect(fixesHint(dialog, 1)).toBeVisible();
  await expect(importButton(dialog, 3)).toBeDisabled();
  expect((await hostsOf(api)).map((h) => h.label)).not.toContain('iis_gamma');
});

test('IP-09: a log with no computer suggests a name from its folder, and "Use {name} and accept {n} files" works', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Fix Folder');
  await mockCheck(page, () => ({ verdict: 'no-computer', importer: 'sql-errorlog' }));
  const folder = mkdtempSync(join(tmpdir(), 'unravis-ip09-'));
  try {
    const logs = join(folder, 'sql-prod-2');
    mkdirSync(logs);
    for (const name of ['ERRORLOG', 'ERRORLOG.1', 'ERRORLOG.2']) writeFileSync(join(logs, name), `sample ${name}\n`);
    await dialog.getByLabel('Choose a folder to import').setInputFiles(logs);
    await expect(dialog.locator('[data-grp="kind:sql-errorlog"]')).toContainText('3 files · folder sql-prod-2');
    await runCheck(dialog);
  } finally {
    rmSync(folder, { recursive: true, force: true });
  }

  await expect(dialog.getByText('3 SQL error log files do not say which computer wrote them.')).toBeVisible();
  const name = dialog.getByLabel('Name of the computer');
  await expect(name).toHaveValue('sql-prod-2');
  await expect(dialog.getByText('Suggested from the folder name. Change it if wrong.')).toBeVisible();
  await expect(dialog.getByLabel('Time zone of its clock')).toBeVisible();
  await expect(dialog.getByText('SQL error logs use the computer’s own clock, so the time zone changes where events land on the timeline.')).toBeVisible();
  await expect(fixesHint(dialog, 1)).toBeVisible();

  // The button follows the name typed.
  await name.fill('sql-prod-9');
  await expect(dialog.getByRole('button', { name: 'Use sql-prod-9 and accept 3 files' })).toBeVisible();
  await name.fill('sql-prod-2');
  await dialog.getByLabel('Time zone of its clock').selectOption('Europe/London');
  await dialog.getByRole('button', { name: 'Use sql-prod-2 and accept 3 files' }).click();

  await expect(dialog.getByText('Added sql-prod-2, 3 files accepted')).toBeVisible();
  await expect(fixesHint(dialog, 1)).toHaveCount(0);
  const host = (await hostsOf(api)).find((h) => h.label === 'sql-prod-2');
  expect(host?.tz).toBe('Europe/London');
  await ack(dialog).check();
  await expect(importButton(dialog, 3)).toBeEnabled();
});

test('IP-10: the time zone field appears only for SQL error log and Windows event log, never for IIS', async ({ page, api }) => {
  const dialog = await openPopup(page, api, 'QA Popup Fix Zone');
  await mockCheck(page, (name) => {
    if (name.startsWith('u_ex')) return { verdict: 'needs-computer', importer: 'iis-w3c', serverName: 'iis_nozone' };
    return { verdict: 'no-computer', importer: name === 'ERRORLOG' ? 'sql-errorlog' : 'win-events' };
  });
  await picker(dialog).setInputFiles([text('u_ex261101.log'), text('ERRORLOG'), text('eventlog.csv')]);
  await runCheck(dialog);

  const cards = dialog.locator('.fix');
  await expect(cards).toHaveCount(3);
  const iisCard = cards.filter({ hasText: 'iis_nozone' });
  await expect(iisCard).toContainText('IIS logs are already UTC, no time zone needed.');
  await expect(iisCard.getByLabel('Time zone of its clock')).toHaveCount(0);
  await expect(iisCard.getByLabel('Name of the computer')).toHaveCount(0);

  const sqlCard = cards.filter({ hasText: 'SQL error log files do not say which computer wrote them.' });
  await expect(sqlCard.getByLabel('Time zone of its clock')).toBeVisible();
  await expect(sqlCard.getByLabel('Name of the computer')).toBeVisible();
  const winCard = cards.filter({ hasText: 'Windows event log files do not say which computer wrote them.' });
  await expect(winCard).toHaveCount(1);
  await expect(winCard.getByLabel('Time zone of its clock')).toBeVisible();

  // Two cards ask for a zone, and no more.
  await expect(dialog.getByLabel('Time zone of its clock')).toHaveCount(2);
});
