// Hub fix D: screen 112 "Add data to an open case" is the entry view of the case's Import drawer, a grid of five cards.
//   GRID-01  the header Import opens the five cards with the wireframe words, and the entries that chose files still open the list.
//   GRID-02  Files opens the existing list (heading focused); Back to Add data returns to the cards; staged files are kept and counted.
//   GRID-03  SQL Server, MongoDB and Another day go to the case's Sources tabs, Blackbox opens the Blackbox panel; Another day is off when
//            nothing can be read by day, and says so.
//   GRID-04  leaving with staged files asks first (Keep editing keeps them, Leave drops them), with no staged files nothing is asked.
//   GRID-05  while an import is sending every card is off and says why.
//   GRID-06  the cards fit at 375 px wide with no sideways scroll.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost } from '../support/api';
import { join } from 'node:path';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const NOTES = join(FILES, 'notes.txt');
const W3C = join(FILES, 'u_ex261020.log');

const closeButton = (page: Page) => page.getByRole('dialog').getByRole('button', { name: 'Close import' });
const drawerCard = (page: Page, id: string) => page.locator('[data-drawer-card="' + id + '"]');

// The header's Import is on the analysis screen, so the way there is the case page, then "Open the analysis". The page can be anywhere when this starts.
async function openGrid(page: Page, title: string) {
  const header = page.getByRole('button', { name: 'Import', exact: true });
  if (!(await header.isVisible())) {
    const analysis = page.getByRole('button', { name: 'Open the analysis' });
    if (!(await analysis.isVisible())) {
      const thisCase = page.getByRole('button', { name: 'This case', exact: true });
      if (await thisCase.isVisible()) await thisCase.click();
      else { await goHome(page); await openCase(page, title); }
    }
    await analysis.click();
  }
  await header.click();
  // From the header the drawer asks which case to use (it may already be on it).
  const use = page.getByRole('button', { name: new RegExp('^Use this case: .*' + title) });
  const grid = page.locator('#drawer-add-head');
  await expect(use.or(grid)).toBeVisible();
  if (await use.isVisible()) await use.click();
  await expect(grid).toBeVisible();
}

async function stageOne(page: Page) {
  await drawerCard(page, 'files').getByRole('button', { name: 'Add files' }).click();
  await expect(page.locator('#import-file-heading, #file-heading')).toBeVisible();
  await page.getByLabel('Choose files to import').setInputFiles([NOTES]);
  await page.getByLabel('What notes.txt is').selectOption('app-log');
}

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('GRID-01: the header Import opens the five cards with the wireframe words; Add a file still opens the list', async ({ page, api }) => {
  await createCase(api, 'QA Grid Words');
  await openGrid(page, 'QA Grid Words');

  await expect(page.locator('#import-title')).toContainText('Add data, case');
  await expect(page.getByText('Add a source after the case was created. Same cards as at creation. New data is imported separately and does not touch what is there.')).toBeVisible();
  await expect(page.locator('#drawer-add-head')).toHaveText('Add data');
  const wanted: Array<[string, string, string]> = [
    ['sql', 'SQL Server', 'Add SQL source'],
    ['mongo', 'MongoDB', 'Add Mongo source'],
    ['blackbox', 'Blackbox', 'Add a Blackbox'],
    ['files', 'Files', 'Add files'],
    ['day', 'Another day', 'Add another day']
  ];
  for (const [id, title, button] of wanted) {
    await expect(drawerCard(page, id).getByRole('heading', { name: title, level: 4 })).toBeVisible();
    await expect(drawerCard(page, id).getByRole('button', { name: button })).toBeVisible();
  }
  // SQL and Mongo stay on even with no saved connection; the list of files is not shown behind the grid.
  await expect(drawerCard(page, 'sql').getByRole('button', { name: 'Add SQL source' })).toBeEnabled();
  await expect(drawerCard(page, 'mongo').getByRole('button', { name: 'Add Mongo source' })).toBeEnabled();
  await expect(page.getByLabel('Choose files to import')).toBeHidden();

  // Close, then the "Add a file" entry goes straight to the files, as it always did.
  await closeButton(page).click();
  await page.getByRole('button', { name: 'This case', exact: true }).click();
  await page.getByRole('button', { name: 'Add a file' }).click();
  await expect(page.locator('#import-title')).toHaveText('Import files');
  await expect(page.locator('#drawer-add-head')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Many files at once', level: 5 })).toBeVisible();
});

test('GRID-02: Files opens the list, Back returns to the cards, and staged files are kept and counted', async ({ page, api }) => {
  await createCase(api, 'QA Grid Files');
  await openGrid(page, 'QA Grid Files');
  await expect(drawerCard(page, 'files').locator('[data-staged]')).toHaveCount(0);

  await stageOne(page);
  await expect(page.getByRole('row').filter({ has: page.getByText('notes.txt', { exact: true }) })).toBeVisible();

  await page.getByRole('button', { name: 'Back to Add data' }).click();
  await expect(page.locator('#drawer-add-head')).toBeVisible();
  await expect(drawerCard(page, 'files').locator('[data-staged]')).toHaveText('1 file staged, not imported');
  await expect(page.getByLabel('Choose files to import')).toBeHidden();

  // The list comes back as it was.
  await drawerCard(page, 'files').getByRole('button', { name: 'Add files' }).click();
  await expect(page.getByLabel('What notes.txt is')).toHaveValue('app-log');
  await expect(page.getByLabel('Choose files to import')).toBeVisible();
});

test('GRID-03: the other cards go to the Sources tabs and the Blackbox panel; Another day is off with nothing to read by day', async ({ page, api }) => {
  await createCase(api, 'QA Grid Routes');
  await openGrid(page, 'QA Grid Routes');
  await expect(drawerCard(page, 'day').getByRole('button', { name: 'Add another day' })).toBeDisabled();
  await expect(drawerCard(page, 'day')).toContainText('0 of 0 sources can be read by day');

  await drawerCard(page, 'sql').getByRole('button', { name: 'Add SQL source' }).click();
  await expect(page.locator('#import-title')).toHaveCount(0);
  await expect(page.getByRole('tab', { name: 'Step 7: SQL Server', exact: true })).toHaveAttribute('aria-selected', 'true');

  await openGrid(page, 'QA Grid Routes');
  await drawerCard(page, 'mongo').getByRole('button', { name: 'Add Mongo source' }).click();
  await expect(page.locator('#import-title')).toHaveCount(0);
  await expect(page.getByRole('tab', { name: 'Add data', exact: true })).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByText('Add an API audit database', { exact: true })).toBeVisible();

  await openGrid(page, 'QA Grid Routes');
  await drawerCard(page, 'blackbox').getByRole('button', { name: 'Add a Blackbox' }).click();
  await expect(page.locator('#import-title')).toHaveCount(0);
  await expect(page.locator('#blackbox-title')).toBeVisible();
});

test('GRID-04: leaving with staged files asks first; Keep editing keeps them, Leave drops them; nothing staged asks nothing', async ({ page, api }) => {
  await createCase(api, 'QA Grid Leave');
  await openGrid(page, 'QA Grid Leave');

  // Nothing staged: Close just closes.
  await closeButton(page).click();
  await expect(page.locator('#import-title')).toHaveCount(0);

  await openGrid(page, 'QA Grid Leave');
  await stageOne(page);
  await closeButton(page).click();
  const ask = page.locator('[data-leave]');
  await expect(ask).toBeVisible();
  await expect(ask).toContainText('Leave without importing?');
  await expect(ask).toContainText('1 file and unsaved fields will be lost.');
  await ask.getByRole('button', { name: 'Keep editing' }).click();
  await expect(ask).toHaveCount(0);
  await expect(page.getByLabel('What notes.txt is')).toHaveValue('app-log');

  // Escape asks as well, and a card that leaves the drawer asks before it goes.
  await page.keyboard.press('Escape');
  await expect(ask).toBeVisible();
  await ask.getByRole('button', { name: 'Keep editing' }).click();
  await page.getByRole('button', { name: 'Back to Add data' }).click();
  await drawerCard(page, 'sql').getByRole('button', { name: 'Add SQL source' }).click();
  await expect(ask).toBeVisible();
  await ask.getByRole('button', { name: 'Keep editing' }).click();
  await expect(drawerCard(page, 'sql')).toBeVisible();

  await closeButton(page).click();
  await ask.getByRole('button', { name: 'Leave' }).click();
  await expect(page.locator('#import-title')).toHaveCount(0);

  // Opened again, the staged file is gone.
  await page.getByRole('button', { name: 'Import', exact: true }).click();
  await expect(drawerCard(page, 'files').locator('[data-staged]')).toHaveCount(0);
});

test('GRID-05: while an import is sending, every card is off and says why', async ({ page, api }) => {
  await createCase(api, 'QA Grid Busy');
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route('**/api/cases/*/imports*', async (route) => {
    if (route.request().method() !== 'POST') { await route.continue(); return; }
    await gate;
    await route.continue();
  });
  await openGrid(page, 'QA Grid Busy');
  await drawerCard(page, 'files').getByRole('button', { name: 'Add files' }).click();
  await page.getByLabel('Choose files to import').setInputFiles([W3C]);
  const box = page.getByRole('combobox', { name: /^Which computer wrote/ });
  if (await box.count()) await box.first().selectOption('web-a');
  await page.getByLabel('I confirm these files meet the conditions in the notice above.').check();
  await page.getByRole('button', { name: 'Import 1 file' }).click();

  await page.getByRole('button', { name: 'Back to Add data' }).click();
  await expect(page.locator('[data-busy]')).toHaveText('An import is already running. Stop it first.');
  for (const id of ['sql', 'mongo', 'blackbox', 'files', 'day']) await expect(drawerCard(page, id).getByRole('button')).toBeDisabled();
  release();
  await expect(page.locator('[data-busy]')).toHaveCount(0, { timeout: 60_000 });
});

test('GRID-06: the cards fit at 375 px wide with no sideways scroll', async ({ page, api }) => {
  await createCase(api, 'QA Grid Narrow');
  await openGrid(page, 'QA Grid Narrow');
  await page.setViewportSize({ width: 375, height: 800 });
  await expect(drawerCard(page, 'day')).toBeVisible();
  // The drawer itself adds no sideways scroll (the page behind it is not the drawer's to fix).
  await expect.poll(() => page.getByRole('dialog').evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
  for (const id of ['sql', 'mongo', 'blackbox', 'files', 'day']) {
    const b = await drawerCard(page, id).boundingBox();
    expect(b && b.x >= 0 && b.x + b.width <= 376).toBe(true);
  }
});
