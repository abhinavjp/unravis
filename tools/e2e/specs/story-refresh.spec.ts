// BL-162 / BL-164 leftover: "Story so far" on Case home shows what was just added without leaving the screen.
//   STORY-01  A file added through the Add files pop-up shows in "Story so far" behind the pop-up as soon as it is kept (not after the pop-up closes),
//             with one re-read of the case, the page not going back to its loading text, and focus staying inside the pop-up.
//   STORY-02  A file the real server refuses as a duplicate (the same file went in by the API after the pop-up had checked it) adds nothing to the story and makes no re-read.
//   STORY-03  A source added in Add data (Redis by address) makes one re-read of the case, with no loading text and focus not pulled onto the story.
//             A source added this way (Redis, SQL, Mongo, Blackbox) has no line in "Story so far": the story is built from case history, files, health runs
//             and theories, and the server writes no history for a source. Only files and found-on-this-PC logs (which are files) show a new line.
//   STORY-04  A file added while a read of the page is slow (held on the case read) still ends up in the story: the re-read waits for the page read, then runs once.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, importFile, HOST } from '../support/api';

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

/** Picks the file, runs the check, ticks the box, then (optionally) runs `beforeAdd` and presses Add. */
async function addOne(dialog: ReturnType<Page['getByRole']>, file: ReturnType<typeof iisLog>, beforeAdd?: () => Promise<void>) {
  await dialog.getByLabel('Choose files to import').setInputFiles([file]);
  await dialog.getByRole('button', { name: 'Continue' }).click();
  const ack = dialog.getByRole('checkbox', { name: 'Confirm read-only' });
  await expect(ack).toBeVisible({ timeout: 30_000 });
  await ack.check();
  await beforeAdd?.();
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

test('STORY-02: a file the server refuses as a duplicate adds nothing to the story and makes no re-read', async ({ page, api }) => {
  await ensureHost(api);
  const title = 'QA Story No Change';
  const id = await createCase(api, title);
  const dialog = await openPopup(page, title);
  const file = iisLog('02');
  const reads = watchReads(page, id);
  // The check passes; then the same file goes into the case by the API, so the pop-up's own import reaches the real server second and is refused as a duplicate.
  await addOne(dialog, file, async () => {
    const first = await importFile(api, id, file.buffer, { name: file.name, importer: 'iis-w3c', host: HOST });
    expect(first.status).toBeLessThan(300);
  });
  await expect(dialog.locator('.prog-line')).toContainText('Step 3 of 3 · Result', { timeout: 60_000 });
  await expect(dialog.locator('.result-section h4').filter({ hasText: 'Not imported' })).toBeVisible();
  await expect(dialog.locator('.result-section h4').filter({ hasText: /^Imported/ })).toHaveCount(0);
  // The page never read the case again: the file the API added is not in its story (it is only there after a reload).
  await page.waitForTimeout(500);
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

test('STORY-04: a file added while a read of the page is slow still ends up in the story', async ({ page, api, allow }) => {
  allow(/423|locked/i, 'the first re-read is answered with "case locked" on purpose, which makes the page read itself again');
  await ensureHost(api);
  const title = 'QA Story Slow Read';
  const id = await createCase(api, title);
  const dialog = await openPopup(page, title);

  // File one is added; its re-read is answered 423 once (the case looks locked), so the page reads itself again. That page read is fetched at once
  // from the real server but its answer is held back, so it carries file one only.
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  let caseReads = 0;
  let held!: () => void;
  const heldSeen = new Promise<void>((resolve) => (held = resolve));
  await page.route(new RegExp(`/api/cases/${id}$`), async (route) => {
    if (route.request().method() !== 'GET') { await route.fallback(); return; }
    caseReads += 1;
    if (caseReads === 1) {
      await route.fulfill({ status: 423, contentType: 'application/json', body: JSON.stringify({ error: 'The case is locked.', reason: 'case-locked' }) });
      return;
    }
    const answer = await route.fetch();
    if (caseReads === 2) { held(); await gate; }
    await route.fulfill({ response: answer });
  });
  await addOne(dialog, iisLog('04'));
  await heldSeen;
  await expect(dialog.locator('.prog-line')).toContainText('Step 3 of 3 · Result', { timeout: 60_000 });

  // File two is added while that page read is still waiting. The story must not take its answer as the last word.
  await dialog.getByRole('button', { name: 'Import more' }).click();
  await addOne(dialog, iisLog('05'));
  await expect(dialog.locator('.prog-line')).toContainText('Step 3 of 3 · Result', { timeout: 60_000 });
  expect(caseReads).toBe(2);
  release();
  await expect(storyFiles(page)).toHaveCount(2);
  expect(caseReads).toBe(3);
  await expect(page.locator('.loading-state')).toHaveCount(0);
});
