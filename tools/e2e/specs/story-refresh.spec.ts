// BL-162 / BL-164 leftover: "Story so far" on Case home shows what was just added without leaving the screen.
//   STORY-01  A file added through the Add files pop-up shows in "Story so far" behind the pop-up as soon as it is kept (not after the pop-up closes),
//             with one re-read of the case, the page not going back to its loading text, and focus staying inside the pop-up.
//   STORY-02  A file the case did not keep (the same file again) adds nothing to the story and makes no re-read.
//   STORY-03  A source added in Add data (Redis by address) makes one re-read of the case, with no loading text and focus not pulled onto the story.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, HOST } from '../support/api';

const FIELDS = '#Fields: date time s-sitename s-computername s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) cs(Cookie) cs(Referer) sc-status sc-substatus sc-win32-status time-taken';
const iisLog = (tag: string) => ({
  name: 'u_ex2610' + tag + '.log',
  mimeType: 'text/plain',
  buffer: Buffer.from(['#Software: Microsoft Internet Information Services 10.0', '#Version: 1.0', '#Date: 2026-10-20 08:00:00', FIELDS,
    ...[1, 2, 3].map((i) => `2026-10-20 08:00:0${i} W3SVC1 ${HOST} 192.0.2.10 GET /api/v1/${tag}/${i} - 443 - 192.0.2.77 Mozilla/5.0 - - 200 0 0 ${100 + i}`), ''].join('\n'))
});

/** Counts the reads of the case record and of its file list from the moment it is called. */
function watchReads(page: Page, id: string) {
  const reads = { story: 0, files: 0 };
  page.on('request', (request) => {
    if (request.method() !== 'GET') return;
    const path = new URL(request.url()).pathname;
    if (path === `/api/cases/${id}`) reads.story += 1;
    else if (path === `/api/cases/${id}/imports`) reads.files += 1;
  });
  return reads;
}

const storyFiles = (page: Page) => page.locator('ol.story li').filter({ hasText: 'File added' });

async function openPopup(page: Page, title: string) {
  await goHome(page);
  await openCase(page, title);
  await expect(page.getByRole('heading', { name: 'Story so far' })).toBeVisible();
  await page.getByRole('button', { name: 'Add files', exact: true }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Add files' });
  await expect(dialog).toBeVisible();
  return dialog;
}

async function addOne(dialog: ReturnType<Page['getByRole']>, file: ReturnType<typeof iisLog>) {
  await dialog.getByLabel('Choose files to import').setInputFiles([file]);
  await dialog.getByRole('button', { name: 'Continue' }).click();
  const ack = dialog.getByRole('checkbox', { name: 'Confirm read-only' });
  await expect(ack).toBeVisible({ timeout: 30_000 });
  await ack.check();
  await dialog.getByRole('button', { name: 'Add 1 file', exact: true }).click();
}

test('STORY-01: a file added in the Add files pop-up shows in Story so far at once, with one re-read and no focus move', async ({ page, api }) => {
  await ensureHost(api);
  const title = 'QA Story Refresh';
  const id = await createCase(api, title);
  const dialog = await openPopup(page, title);
  await expect(storyFiles(page)).toHaveCount(0);

  const reads = watchReads(page, id);
  await addOne(dialog, iisLog('01'));
  // Behind the pop-up, while it is still open on the Result step.
  await expect(dialog.locator('.prog-line')).toContainText('Step 3 of 3 · Result', { timeout: 60_000 });
  await expect(storyFiles(page)).toHaveCount(1);
  await expect(storyFiles(page).first()).toContainText('u_ex261001.log');
  await expect(dialog).toBeVisible();
  // One re-read of the case and of its files for the one kept file. The page never went back to its loading text.
  expect(reads).toEqual({ story: 1, files: 1 });
  await expect(page.locator('.loading-state')).toHaveCount(0);
  // Focus is where the pop-up put it (inside it), not pulled to the page behind.
  expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true);

  // Closing the pop-up reads nothing more.
  await dialog.getByRole('button', { name: /^Close/ }).first().click();
  await expect(dialog).toHaveCount(0);
  await expect(storyFiles(page)).toHaveCount(1);
  expect(reads).toEqual({ story: 1, files: 1 });
});

test('STORY-02: a file the server did not keep adds nothing to the story and makes no re-read', async ({ page, api }) => {
  await ensureHost(api);
  const title = 'QA Story No Change';
  const id = await createCase(api, title);
  // The server's answer to the import itself is a refusal (a read that kept nothing); the check before it is the real one.
  await page.route(/\/api\/cases\/[^/]+\/imports\?/, async (route) => {
    if (route.request().method() !== 'POST' || route.request().url().includes('preview=1')) { await route.fallback(); return; }
    const result = { importer: 'iis-w3c', score: 1, tied: [], report: null, record: null };
    await route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ id: 'b'.repeat(32), state: 'done', kind: 'import', bytesRead: 0, bytesTotal: 0, result }) });
  });
  const dialog = await openPopup(page, title);
  const reads = watchReads(page, id);
  await addOne(dialog, iisLog('02'));
  await expect(dialog.locator('.prog-line')).toContainText('Step 3 of 3 · Result', { timeout: 60_000 });
  await expect(dialog.locator('.result-section h4').filter({ hasText: 'Not imported' })).toBeVisible();
  await expect(storyFiles(page)).toHaveCount(0);
  expect(reads).toEqual({ story: 0, files: 0 });
});

test('STORY-03: a source added in Add data makes one re-read of the case and the page keeps its place', async ({ page, api }) => {
  const title = 'QA Story Add Data';
  const id = await createCase(api, title);
  await goHome(page);
  await openCase(page, title);
  const hub = page.locator('[data-sources-hub]');
  await hub.getByRole('tab', { name: 'Add data', exact: true }).click();
  await hub.getByRole('button', { name: /^Redis/ }).click();
  await hub.getByLabel('Redis address').fill('10.0.0.5:6379');
  await hub.getByRole('button', { name: /^Check and add/ }).click();
  await expect(hub.locator('[data-added-note]')).toContainText('Added');
  await hub.getByRole('button', { name: 'Review', exact: true }).click();
  const reads = watchReads(page, id);
  await hub.getByRole('button', { name: 'Add 1 source to case' }).click();
  await expect.poll(() => reads.story, { timeout: 30_000 }).toBe(1);
  await page.waitForTimeout(500);
  // One read of the case (the Sources list that opens after the run reads the file list on its own, so only the case read is counted), no loading text, and the re-read does not pull focus onto the story (the hub itself moves on to the Sources list once the run ends, as before).
  expect(reads.story).toBe(1);
  await expect(page.locator('.loading-state')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Story so far' })).toBeVisible();
  expect(await page.evaluate(() => !!document.activeElement?.closest('section[aria-labelledby="home-story"]') || document.activeElement?.id === 'home-story')).toBe(false);
});
