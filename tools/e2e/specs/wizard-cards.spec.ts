// Hub fixes A, B and C on the "Import files" step of the create-a-case wizard (screens 37 and 38 of the wireframe):
//   WCARD-01  the four per-type cards carry the wireframe words, each with its own Add button and its own named file input.
//   WCARD-02  a card sets the kind of its files, shows only its own files, and counts them; "not sure" files go to Other files.
//   WCARD-03  the restored kinds (Hangfire jobs, Redis slow log, Another file or export) are in the select, and every choice can be undone.
//   WCARD-04  staged rows, their kinds and computers survive switching add-source forms and closing and opening Import files (F37);
//             Remove and Clear still work.
//   WCARD-05  the step fits at 375 px with no sideways scroll.
import { test, expect, type Page } from '../support/fixtures';
import { goHome } from '../support/ui';
import { join } from 'node:path';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const W3C = join(FILES, 'u_ex261020.log');
const HTTPERR = join(FILES, 'httperr1.log');
const NOTES = join(FILES, 'notes.txt');

async function openPanel(page: Page, title: string) {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByText('Nothing is saved until the last step.')).toBeVisible();
  await page.getByLabel('Case name').fill(title);
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('heading', { name: 'Choose what to look at' })).toBeVisible();
  await page.getByRole('button', { name: 'Import files', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Import files', level: 5 })).toBeVisible();
}

const row = (page: Page, name: string) => page.getByRole('row').filter({ has: page.getByText(name, { exact: true }) });
const card = (page: Page, id: string) => page.locator('[data-file-card="' + id + '"]');
const count = (page: Page, id: string) => page.locator('[data-card-count="' + id + '"]');

test('WCARD-01: the four cards keep the wireframe words and each has its own Add button and file input', async ({ page }) => {
  await openPanel(page, 'QA Cards Words');

  await expect(page.getByRole('heading', { name: 'Add files by type', level: 6 })).toBeVisible();
  await expect(page.getByText('For servers Unravis cannot reach: bring IIS logs, application logs, a Query Store export and an audit export.')).toBeVisible();

  const iis = card(page, 'iis');
  await expect(iis).toContainText('IIS logs (W3C)');
  await expect(iis).toContainText('Gives time-taken per request. Used for O in Step 3.');
  await expect(iis).toContainText('needs time-taken field');
  await expect(iis.getByRole('button', { name: 'Add IIS logs' })).toBeVisible();
  await expect(card(page, 'app')).toContainText('Errors and warnings near onset. Used by Step 5 only.');
  await expect(card(page, 'app').getByRole('button', { name: 'Add app logs' })).toBeVisible();
  await expect(card(page, 'querystore')).toContainText('For SQL you cannot connect to. A CSV from the sys.query_store views.');
  await expect(card(page, 'querystore').getByRole('button', { name: 'Add export' })).toBeVisible();
  await expect(card(page, 'audit')).toContainText('For a Mongo you cannot connect to.');
  await expect(card(page, 'audit').getByRole('button', { name: 'Add audit export' })).toBeVisible();

  await expect(page.getByText('No step needs application logs or a Query Store export to start. IIS logs and audit exports feed the main steps (2 to 4).')).toBeVisible();
  await expect(page.getByText('Not sure what a file is? Add any file here and Unravis suggests what it is.')).toBeVisible();

  // Every card has its own named input, and nothing has been chosen yet.
  for (const name of ['Choose IIS logs (W3C) files', 'Choose Application logs files', 'Choose Query Store export files', 'Choose Audit export files']) {
    await expect(page.getByLabel(name)).toHaveCount(1);
  }
  for (const id of ['iis', 'app', 'querystore', 'audit']) await expect(count(page, id)).toHaveText('No files chosen yet');
  // The short guides of the cards sit with the IIS guide.
  await expect(page.locator('[data-guide]').first()).toBeVisible();
});

test('WCARD-02: a card sets the kind of its files and shows only its own, and the single picker still auto-detects', async ({ page }) => {
  await openPanel(page, 'QA Cards Own');

  await page.getByLabel('Choose IIS logs (W3C) files').setInputFiles([W3C]);
  await expect(count(page, 'iis')).toHaveText('1 file chosen');
  await expect(count(page, 'app')).toHaveText('No files chosen yet');
  await expect(page.getByLabel('What u_ex261020.log is')).toHaveValue('iis-w3c');

  // A file whose name says nothing, added through the Query Store card, takes that card's kind and is counted there only.
  await page.getByLabel('Choose Query Store export files').setInputFiles([NOTES]);
  await expect(count(page, 'querystore')).toHaveText('1 file chosen');
  await expect(count(page, 'iis')).toHaveText('1 file chosen');
  await expect(page.getByLabel('What notes.txt is')).toHaveValue('sql-querystore');
  await expect(page.getByRole('heading', { level: 6, name: 'IIS logs (W3C): 1 file' })).toBeVisible();
  await expect(page.getByRole('heading', { level: 6, name: 'Query Store export: 1 file' })).toBeVisible();

  // The same file through the app logs card is its own row under its own heading.
  await page.getByLabel('Choose Application logs files').setInputFiles([HTTPERR]);
  await expect(count(page, 'app')).toHaveText('1 file chosen');
  await expect(page.getByLabel('What httperr1.log is')).toHaveValue('app-log');

  // The auto-detect picker is still there: a file added here is suggested by its name and goes to Other files.
  await page.getByLabel('Choose files to import').setInputFiles([join(FILES, 'u_ex261020.log')]);
  await expect(page.getByRole('heading', { level: 6, name: /^Other files/ })).toBeVisible();

  // Clear empties every card.
  await page.getByRole('button', { name: 'Clear' }).click();
  for (const id of ['iis', 'app', 'querystore', 'audit']) await expect(count(page, id)).toHaveText('No files chosen yet');
  await expect(page.getByRole('row')).toHaveCount(0);
});

test('WCARD-03: the restored kinds are in the select and every choice can be undone', async ({ page }) => {
  await openPanel(page, 'QA Cards Restored');
  await page.getByLabel('Choose files to import').setInputFiles([NOTES]);
  const kind = page.getByLabel('What notes.txt is');
  await expect(kind).toHaveValue('');

  const labels = await kind.locator('option').allTextContents();
  expect(labels).toEqual(expect.arrayContaining(['Hangfire jobs (CSV export)', 'Redis slow log (CSV export)', 'Another file or export', 'Don’t use this file']));

  await kind.selectOption({ label: 'Hangfire jobs (CSV export)' });
  await expect(kind).toHaveValue('hangfire-csv');
  await expect(page.getByRole('button', { name: 'Add 1 file' })).toBeEnabled();
  await kind.selectOption({ label: 'Redis slow log (CSV export)' });
  await expect(kind).toHaveValue('redis-slowlog-csv');
  await kind.selectOption({ label: 'Another file or export' });
  await expect(kind).toHaveValue('file');
  await expect(page.getByRole('button', { name: 'Add 1 file' })).toBeEnabled();

  // Unselect: back to Don't use this file, and nothing is left to add.
  await kind.selectOption({ label: 'Don’t use this file' });
  await expect(kind).toHaveValue('');
  await expect(page.getByRole('button', { name: 'Add files' })).toBeDisabled();

  // Choose again, add, and the file is held for the last step.
  await kind.selectOption({ label: 'Hangfire jobs (CSV export)' });
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await expect(page.getByText(/^1 file was added to your sources\./)).toBeVisible();
});

test('WCARD-04: staged rows survive switching add-source forms and closing Import files; Remove and Clear still work', async ({ page }) => {
  await openPanel(page, 'QA Cards Survive');
  await page.getByLabel('Choose IIS logs (W3C) files').setInputFiles([W3C]);
  await page.getByLabel('Choose files to import').setInputFiles([HTTPERR, NOTES]);
  await page.getByLabel('What notes.txt is').selectOption({ label: 'Another file or export' });
  await expect(row(page, 'u_ex261020.log')).toBeVisible();

  // Another add-source form replaces this one: the rows are still staged when it comes back.
  await page.getByRole('button', { name: 'Connect to MongoDB' }).click();
  await expect(page.getByRole('heading', { name: 'Import files', level: 5 })).toHaveCount(0);
  await page.getByRole('button', { name: 'SQL Server by address' }).click();
  await page.getByRole('button', { name: 'Import files', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Import files', level: 5 })).toBeVisible();
  await expect(row(page, 'u_ex261020.log')).toBeVisible();
  await expect(row(page, 'httperr1.log')).toBeVisible();
  await expect(page.getByLabel('What notes.txt is')).toHaveValue('file');
  await expect(count(page, 'iis')).toHaveText('1 file chosen');

  // Closing and opening the files form does not lose them either.
  await page.getByRole('button', { name: 'Import files', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Import files', level: 5 })).toHaveCount(0);
  await page.getByRole('button', { name: 'Import files', exact: true }).click();
  await expect(row(page, 'httperr1.log')).toBeVisible();
  await expect(page.getByLabel('What notes.txt is')).toHaveValue('file');

  // Remove one row, then Clear the rest.
  await page.getByRole('button', { name: 'Remove httperr1.log from the list' }).click();
  await expect(row(page, 'httperr1.log')).toHaveCount(0);
  await expect(row(page, 'notes.txt')).toBeVisible();
  await page.getByRole('button', { name: 'Clear' }).click();
  await expect(page.getByRole('row')).toHaveCount(0);
  await page.getByRole('button', { name: 'Connect to MongoDB' }).click();
  await page.getByRole('button', { name: 'Import files', exact: true }).click();
  await expect(page.getByRole('row')).toHaveCount(0);
});

test('WCARD-05: the cards fit at 375 px wide with no sideways scroll', async ({ page }) => {
  await openPanel(page, 'QA Cards Narrow');
  await page.setViewportSize({ width: 375, height: 800 });
  await expect(card(page, 'audit')).toBeVisible();
  // The step is checked as it stands once the narrow layout has settled: the files form must add no sideways scroll of its own.
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
  for (const id of ['iis', 'app', 'querystore', 'audit']) {
    const box = await card(page, id).boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= 376).toBe(true);
  }
});
