// A file refused only because its computer is missing sends the Import files popup back to that computer's fix card.
//   IP-14  Two IIS logs, the second from a computer that is dropped from the list after it was added on the card. The first imports,
//          the second is refused for the missing computer, and the popup returns to the Check step on that fix card, focused, with
//          the first file already off the list. Adding the computer again lets the second file in, and the Result step counts both.
//   IP-19  A wizard file refused for an unlisted computer saves that name. Try again on Case home opens Check on its fix card.
//   IP-20  A retry with no computer name, or a different refusal, still opens Choose.
import { test, expect, type Page } from '../support/fixtures';
import type { Locator } from '@playwright/test';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, type ApiFn } from '../support/api';

const FIELDS =
  '#Fields: date time s-sitename s-computername s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) cs(Cookie) cs(Referer) sc-status sc-substatus sc-win32-status time-taken';

/** A small IIS log written by `server`. `tag` changes the rows, so no two files are the same file. */
function w3c(server: string, tag: string): string {
  const rows = [1, 2, 3].map((i) => `2026-10-20 08:00:0${i} W3SVC1 ${server} 192.0.2.10 GET /api/v1/${tag}/${i} - 443 - 192.0.2.77 Mozilla/5.0 - - 200 0 0 ${100 + i}`);
  return ['#Software: Microsoft Internet Information Services 10.0', '#Version: 1.0', '#Date: 2026-10-20 08:00:00', FIELDS, ...rows, ''].join('\n');
}
const iis = (name: string, server: string) => ({ name, mimeType: 'text/plain', buffer: Buffer.from(w3c(server, name)) });

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

const ack = (dialog: Locator) => dialog.getByRole('checkbox', { name: 'Confirm read-only' });
const importButton = (dialog: Locator, n: number) => dialog.getByRole('button', { name: `Import ${n} ${n === 1 ? 'file' : 'files'}`, exact: true });
const importsOf = async (api: ApiFn, title: string) => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string; imports: number }>).find((c) => c.title === title)?.imports;

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('IP-14: a file refused for a missing computer reopens the popup on that fix card, and the retry lets it in', async ({ page, api, allow }) => {
  test.setTimeout(150_000);
  allow(/422|409/, 'a file the server refuses on purpose: its computer was dropped from the list');
  const title = 'QA Popup Retry';
  const dialog = await openPopup(page, api, title);
  await dialog.getByLabel('Choose files to import').setInputFiles([iis('u_ex261141.log', 'web-a'), iis('u_ex261142.log', 'iis_late')]);
  await dialog.getByRole('button', { name: 'Continue' }).click();
  await expect(ack(dialog)).toBeVisible({ timeout: 30_000 });

  const add = dialog.getByRole('button', { name: 'Add iis_late as a computer and accept 1 file' });
  await add.click();
  await expect(dialog.getByText('Added iis_late, 1 file accepted')).toBeVisible();
  await ack(dialog).check();
  await expect(importButton(dialog, 2)).toBeEnabled();

  // The computer is taken off the list behind the popup's back, so the server will refuse the second file.
  const dropped = await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'web-a', role: 'iis', tz: 'UTC' }] });
  expect(dropped.status).toBe(200);
  await importButton(dialog, 2).click();

  // Back on the Check step, on the card of the computer that is missing, with the first file already imported and gone from the list.
  await expect(dialog.locator('.prog-line')).toContainText('Step 2 of 3', { timeout: 90_000 });
  await expect(dialog.getByText('1 IIS file comes from iis_late, which is not on your computer list.')).toBeVisible();
  await expect(add).toBeFocused();
  await expect(dialog.getByText('1 fix needed before you can import')).toBeVisible();
  await expect(dialog.locator('[data-grp="kind:iis-w3c"]')).toHaveCount(0);
  await expect(importButton(dialog, 1)).toBeDisabled();
  await expect.poll(() => importsOf(api, title), { timeout: 15_000 }).toBe(1);
  await expect(dialog.locator('.result-hero')).toHaveCount(0);

  // The card works as before: add the computer, confirm, import the one file left.
  await add.click();
  await expect(dialog.getByText('Added iis_late, 1 file accepted')).toBeVisible();
  if (!(await ack(dialog).isChecked())) await ack(dialog).check();
  await expect(importButton(dialog, 1)).toBeEnabled();
  await importButton(dialog, 1).click();

  // The Result step tells the whole story, including the file that went in the first time.
  await expect(dialog.locator('.prog-line')).toContainText('Step 3 of 3 · Result', { timeout: 90_000 });
  await expect(dialog.locator('.result-hero')).toContainText('2 files imported');
  await expect(dialog.locator('.result-section').filter({ hasText: 'IIS web logs' })).toContainText('2 files');
  await expect.poll(() => importsOf(api, title), { timeout: 15_000 }).toBe(2);
});

test('IP-19: Try again on a saved unlisted-computer retry opens Check on its fix card', async ({ page, api, allow }) => {
  test.setTimeout(150_000);
  allow(/422|409/, 'a wizard file refused because its computer is not on the list');
  const title = 'QA Saved Popup Retry';
  const file = iis('u_ex261119.log', 'iis_missing');
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill(title);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Add files', exact: true }).click();
  await page.getByLabel('Choose files to import').setInputFiles([file]);
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();
  await expect(page.getByRole('table', { name: /result for each file/i })).toContainText('Rejected', { timeout: 90_000 });

  // Read the saved case, not a popup state supplied by the test. No files are held after leaving the wizard.
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === title);
  expect(made).toBeTruthy();
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.retries, { timeout: 30_000 }).toEqual([
    expect.objectContaining({ kind: 'import', reason: 'unknown-host', payload: { serverName: 'iis_missing' } })
  ]);
  await page.getByRole('button', { name: 'Open the case anyway', exact: true }).click();
  await page.getByRole('button', { name: 'Add the file again', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Import files' });
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('.prog-line')).toContainText('Step 2 of 3');
  await expect(dialog.locator('.fix[data-card="open"]')).toContainText('iis_missing is not on your computer list.');
  await expect(dialog.getByText('Add this computer, then choose the file again.')).toBeVisible();
  const add = dialog.getByRole('button', { name: 'Add iis_missing as a computer', exact: true });
  await expect(add).toBeFocused();
  await expect(ack(dialog)).toBeDisabled();

  // Add the computer first, then pick the file again. The ordinary check now accepts it.
  await add.click();
  await expect(dialog.locator('.prog-line')).toContainText('Step 1 of 3');
  await dialog.getByLabel('Choose files to import').setInputFiles([file]);
  await dialog.getByRole('button', { name: 'Continue' }).click();
  await expect(dialog.locator('[data-grp="kind:iis-w3c"]')).toContainText('1 file, ready', { timeout: 30_000 });
  await expect(dialog.locator('.fix')).toHaveCount(0);
  await ack(dialog).check();
  await expect(importButton(dialog, 1)).toBeEnabled();
});

test('IP-20: a retry without an unlisted computer name still opens Choose', async ({ page, api }) => {
  const title = 'QA Popup Retry Fallback';
  const id = await createCase(api, title);
  const saved = await api('PUT', `/api/cases/${id}/retries`, { items: [
    { kind: 'import', key: 'file:1', label: 'No computer name', reason: 'unknown-host' },
    { kind: 'import', key: 'file:2', label: 'Other refusal', reason: 'unsupported-format', payload: { serverName: 'iis_missing' } }
  ] }, 'open');
  expect(saved.status).toBe(200);
  await goHome(page);
  await openCase(page, title);
  for (const label of ['No computer name', 'Other refusal']) {
    await page.locator('li[data-retry]').filter({ hasText: label }).getByRole('button', { name: 'Add the file again', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Import files' });
    await expect(dialog.locator('.prog-line')).toContainText('Step 1 of 3');
    await expect(dialog.getByLabel('Choose files to import')).toBeAttached();
    await expect(dialog.locator('.fix')).toHaveCount(0);
    await dialog.getByRole('button', { name: 'Close import', exact: true }).click();
    await expect(dialog).toHaveCount(0);
  }
});
