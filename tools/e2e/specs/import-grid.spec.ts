// How the Import files popup is reached and left, and what it holds. It replaces the old Import drawer with its grid of five cards: the popup
// takes files only (Choose, Check, Result), and SQL Server, MongoDB, the Blackbox and Another day are reached from the case page.
//   GRID-01  the header Import and the case page's Add a file open the same popup, with the wireframe words and no cards.
//   GRID-02  stepping back from Check to Choose keeps the staged files.
//   GRID-03  the other kinds of data are not in the popup; the case page's Sources tabs still reach them.
//   GRID-04  leaving with files listed asks first (Keep them keeps, Discard drops them); with no files nothing is asked.
//   GRID-05  while an import is sending, the popup cannot be closed or stepped back, and says so.
//   GRID-06  the popup fits at 375 px wide with no sideways scroll.
//   GRID-07  with one SQL day read, the popup still takes files only.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost } from '../support/api';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const W3C = join(FILES, 'u_ex261020.log');
// A server the host list never held (the list holds web-a only), so the Check step shows a fix card.
const W3C_UNLISTED = readFileSync(W3C, 'utf8').replace(/\bWEB-A\b/g, 'ISS-PROD-07');

const payload = (name: string, buffer: Buffer) => ({ name, mimeType: 'text/plain', buffer });
const popup = (page: Page) => page.getByRole('dialog', { name: 'Import files' });
const closeButton = (page: Page) => popup(page).getByRole('button', { name: 'Close import' });
const picker = (page: Page) => popup(page).getByLabel('Choose files to import', { exact: true });
const ack = (page: Page) => popup(page).getByRole('checkbox', { name: 'Confirm read-only' });

// Sideways scroll, measured the way a finger would meet it: the page's own widths, a real attempt to scroll across, and every box on the page.
async function noSidewaysScroll(page: Page) {
  await expect.poll(() => page.evaluate(() => {
    const doc = document.documentElement;
    return Math.max(doc.scrollWidth, document.body.scrollWidth) - doc.clientWidth;
  })).toBeLessThanOrEqual(0);
  const moved = await page.evaluate(() => { window.scrollTo(300, 0); document.documentElement.scrollLeft = 300; return window.scrollX + document.documentElement.scrollLeft; });
  expect(moved).toBe(0);
  const wide = await page.evaluate(() => {
    const vw = document.documentElement.clientWidth;
    return Array.from(document.querySelectorAll('body *')).filter((el) => {
      const r = el.getBoundingClientRect();
      return r.width > 0 && r.right > vw + 1 && getComputedStyle(el).position !== 'fixed';
    }).slice(0, 5).map((el) => el.tagName + '.' + String(el.className).slice(0, 40));
  });
  expect(wide).toEqual([]);
}

// The header's Import is on the analysis screen, so the way there is the case page, then "Open the analysis". The page can be anywhere when this starts.
async function openFromHeader(page: Page, title: string) {
  const header = page.getByRole('button', { name: 'Import', exact: true });
  const thisCase = page.getByRole('button', { name: 'This case', exact: true });
  // The header Import needs a case to be open: the demo screen shows it too, but no case is active there.
  if (!(await header.isVisible()) || !(await thisCase.isVisible())) {
    const analysis = page.getByRole('button', { name: 'Open the analysis' });
    if (!(await analysis.isVisible())) {
      if (await thisCase.isVisible()) await thisCase.click();
      else { await goHome(page); await openCase(page, title); }
    }
    await analysis.click();
  }
  await header.click();
  await expect(popup(page)).toBeVisible();
}

/** Stages one file and presses Continue; the Check step is done once the read-only box is drawn. */
async function stageAndCheck(page: Page, file: { name: string; mimeType: string; buffer: Buffer }) {
  await picker(page).setInputFiles([file]);
  await popup(page).getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(ack(page)).toBeVisible({ timeout: 60_000 });
}

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('GRID-01: the header Import and Add a file open the same popup with the wireframe words and no cards', async ({ page, api }) => {
  await createCase(api, 'QA Grid Words');
  await openFromHeader(page, 'QA Grid Words');

  const dlg = popup(page);
  await expect(dlg.getByRole('heading', { name: 'Import files', level: 2 })).toBeVisible();
  await expect(dlg.locator('.case-chip')).toContainText('QA Grid Words');
  await expect(dlg.locator('.stepper .stp')).toHaveText([/Choose/, /Check/, /Result/]);
  await expect(dlg.locator('.stepper .stp.cur')).toContainText('Choose');
  await expect(dlg.locator('.prog-line')).toContainText('Step 1 of 3 · Choose files to import');
  await expect(dlg.getByText('Drop files or a folder here')).toBeVisible();
  await expect(dlg.getByText('IIS web logs, log4net, SQL error logs, Windows event logs. Text and CSV only.')).toBeVisible();
  await expect(dlg.getByRole('button', { name: 'Add files', exact: true })).toBeVisible();
  await expect(dlg.getByRole('button', { name: 'Add a folder', exact: true })).toBeVisible();
  await expect(dlg.getByText('Nothing is sent until you press Import.')).toBeVisible();
  await expect(dlg.getByRole('button', { name: 'Continue', exact: true })).toBeDisabled();
  // The cards of the old drawer are gone, and so is its heading.
  await expect(page.locator('[data-drawer-card]')).toHaveCount(0);
  await expect(page.locator('#drawer-add-head')).toHaveCount(0);
  await expect(page.locator('#import-title')).toHaveCount(0);

  // Close, then the "Add a file" entry opens the same popup.
  await closeButton(page).click();
  await expect(popup(page)).toHaveCount(0);
  await page.getByRole('button', { name: 'This case', exact: true }).click();
  await page.getByRole('button', { name: 'Add a file' }).click();
  await expect(dlg.getByRole('heading', { name: 'Import files', level: 2 })).toBeVisible();
  await expect(dlg.locator('.prog-line')).toContainText('Step 1 of 3 · Choose files to import');
  await expect(page.locator('[data-drawer-card]')).toHaveCount(0);
});

test('GRID-02: stepping back from Check to Choose keeps the staged files', async ({ page, api }) => {
  await createCase(api, 'QA Grid Files');
  await openFromHeader(page, 'QA Grid Files');
  const dlg = popup(page);

  await stageAndCheck(page, payload('u_ex261020.log', readFileSync(W3C)));
  await expect(dlg.locator('.prog-line')).toContainText('Step 2 of 3');
  await expect(dlg.locator('.stepper .stp.cur')).toContainText('Check');
  // Only back: the step ahead and the current step are not buttons that can be pressed.
  await expect(dlg.locator('.stepper .stp').nth(1)).toBeDisabled();
  await expect(dlg.locator('.stepper .stp').nth(2)).toBeDisabled();

  await dlg.locator('.stepper .stp').first().click();
  await expect(dlg.locator('.prog-line')).toContainText('Step 1 of 3 · Choose files to import');
  const iis = dlg.locator('[data-grp="kind:iis-w3c"]');
  await expect(iis).toContainText('IIS web logs');
  await expect(iis).toContainText('1 file');
  await expect(dlg.getByRole('button', { name: 'Continue', exact: true })).toBeEnabled();
});

test('GRID-03: the other kinds of data are not in the popup; the case page still reaches them', async ({ page, api }) => {
  await createCase(api, 'QA Grid Routes');
  await openFromHeader(page, 'QA Grid Routes');
  const dlg = popup(page);
  await expect(dlg.getByRole('button', { name: /SQL source|Mongo source|Blackbox|another day/i })).toHaveCount(0);
  await closeButton(page).click();
  await expect(popup(page)).toHaveCount(0);

  await page.getByRole('button', { name: 'This case', exact: true }).click();
  await page.getByRole('tab', { name: 'Step 7: SQL Server', exact: true }).click();
  await expect(page.getByRole('tab', { name: 'Step 7: SQL Server', exact: true })).toHaveAttribute('aria-selected', 'true');
});

test('GRID-04: leaving with files listed asks first; Keep them keeps them, Discard drops them; nothing listed asks nothing', async ({ page, api }) => {
  await createCase(api, 'QA Grid Leave');
  await openFromHeader(page, 'QA Grid Leave');
  const dlg = popup(page);

  // Nothing listed: Close just closes.
  await closeButton(page).click();
  await expect(popup(page)).toHaveCount(0);
  await expect(page.getByRole('alertdialog')).toHaveCount(0);

  await openFromHeader(page, 'QA Grid Leave');
  await picker(page).setInputFiles([payload('u_ex261020.log', readFileSync(W3C))]);
  await expect(dlg.locator('[data-grp="kind:iis-w3c"]')).toContainText('1 file');
  await closeButton(page).click();
  const ask = page.getByRole('alertdialog', { name: 'Discard 1 file?' });
  await expect(ask).toBeVisible();
  await expect(ask).toContainText('These files are still on the list. Discarding clears them and closes the popup.');
  await ask.getByRole('button', { name: 'Keep them' }).click();
  await expect(ask).toHaveCount(0);
  await expect(dlg.locator('[data-grp="kind:iis-w3c"]')).toContainText('1 file');

  // Escape asks as well, and answers Keep them when the question is already open. A click outside the popup asks too.
  await page.keyboard.press('Escape');
  await expect(ask).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(ask).toHaveCount(0);
  await page.locator('button.scrim').click({ position: { x: 5, y: 5 } });
  await expect(ask).toBeVisible();
  await ask.getByRole('button', { name: 'Keep them' }).click();

  await closeButton(page).click();
  await ask.getByRole('button', { name: 'Discard', exact: true }).click();
  await expect(popup(page)).toHaveCount(0);

  // Opened again, the discarded file is gone.
  await openFromHeader(page, 'QA Grid Leave');
  await expect(dlg.locator('[data-grp]')).toHaveCount(0);
  await expect(dlg.getByRole('button', { name: 'Continue', exact: true })).toBeDisabled();
});

test('GRID-05: while an import is sending, the popup cannot be closed or stepped back, and says so', async ({ page, api }) => {
  await createCase(api, 'QA Grid Busy');
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route('**/api/cases/*/imports*', async (route) => {
    if (route.request().method() !== 'POST' || /\/imports\/check/.test(route.request().url())) { await route.continue(); return; }
    await gate;
    await route.continue();
  });
  await openFromHeader(page, 'QA Grid Busy');
  const dlg = popup(page);
  await stageAndCheck(page, payload('u_ex261020.log', readFileSync(W3C)));
  await ack(page).check();
  await dlg.getByRole('button', { name: 'Import 1 file', exact: true }).click();

  await expect(dlg.locator('.prog-line')).toContainText('Step 3 of 3 · Importing — do not close');
  await expect(dlg.getByText('Importing 0 of 1 files')).toBeVisible();
  await expect(dlg.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible();
  for (const index of [0, 1, 2]) await expect(dlg.locator('.stepper .stp').nth(index)).toBeDisabled();
  // Close, Esc and a click outside wait until the import ends or Cancel is pressed.
  await closeButton(page).click();
  await page.keyboard.press('Escape');
  await page.locator('button.scrim').click({ position: { x: 5, y: 5 } });
  await expect(page.getByRole('alertdialog')).toHaveCount(0);
  await expect(dlg).toBeVisible();

  release();
  await expect(dlg.locator('.result-hero')).toContainText('1 files imported', { timeout: 60_000 });
  await expect(dlg.locator('.prog-line')).toContainText('Step 3 of 3 · Result');
});

test('GRID-06: the popup fits at 375 px wide with no sideways scroll', async ({ page, api }) => {
  await createCase(api, 'QA Grid Narrow');
  await openFromHeader(page, 'QA Grid Narrow');
  const dlg = popup(page);
  await page.setViewportSize({ width: 375, height: 800 });
  await expect(dlg.getByText('Drop files or a folder here')).toBeVisible();
  await expect.poll(() => dlg.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
  await noSidewaysScroll(page);

  // The Check step with a fix card is the widest thing the popup draws.
  await stageAndCheck(page, payload('u_ex261030.log', Buffer.from(W3C_UNLISTED)));
  await expect(dlg.locator('.fix')).toContainText('ISS-PROD-07');
  await expect.poll(() => dlg.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0);
  await noSidewaysScroll(page);
  const box = await dlg.boundingBox();
  expect(box && box.x >= 0 && box.x + box.width <= 376).toBe(true);
  const primary = await dlg.getByRole('button', { name: 'Add ISS-PROD-07 as a computer and accept 1 files', exact: true }).boundingBox();
  expect(primary && primary.x >= 0 && primary.x + primary.width <= 376).toBe(true);
});

test('GRID-07: with one SQL day read, the popup still takes files only', async ({ page, api }) => {
  await createCase(api, 'QA Grid Day');
  await goHome(page);
  await openCase(page, 'QA Grid Day');
  await page.getByRole('tab', { name: 'Step 7: SQL Server', exact: true }).click();
  const panel = page.getByRole('tabpanel');
  const read = panel.getByRole('region', { name: 'Read from a server' });
  await expect(read).toBeVisible();
  await read.getByLabel('Audit database', { exact: true }).fill('AuditDb');
  await read.getByLabel('Incident starts (UTC)', { exact: true }).fill('2026-10-20 08:45');
  await read.getByLabel('Incident ends (UTC)', { exact: true }).fill('2026-10-20 09:00');
  await read.getByLabel('Reference starts (UTC)', { exact: true }).fill('2026-10-20 08:30');
  await read.getByLabel('Reference ends (UTC)', { exact: true }).fill('2026-10-20 08:45');
  await read.getByRole('button', { name: 'Read this day' }).click();
  await expect(panel.locator('[data-notice]')).toHaveText('Read and added to the case.');

  await openFromHeader(page, 'QA Grid Day');
  const dlg = popup(page);
  await expect(dlg.getByRole('heading', { name: 'Import files', level: 2 })).toBeVisible();
  await expect(dlg.getByRole('button', { name: /another day/i })).toHaveCount(0);
  await expect(page.locator('[data-drawer-card]')).toHaveCount(0);
  await expect(dlg.getByText('Drop files or a folder here')).toBeVisible();
});
