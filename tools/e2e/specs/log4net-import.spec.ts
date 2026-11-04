// The log4net text-block application log (importer log4net-blocks): each entry is a few labelled lines (Logger name, Type, Server Time,
// UTC Time, Application URL, User IP) followed by the message and, for an error, the exception text. The fixture is synthetic.
//   L4N-01  Case drawer: the file is read by content (the name says nothing), one garbled block makes it Partly used, and the case lists it.
//   L4N-02  Case drawer: a file named log.txt is suggested as an application log from its name alone, and a prose file is Rejected.
//   L4N-03  Create wizard: the Application logs card holds the file, the case is made with it, and the file was read as log4net blocks.
//   L4N-04  The Investigation Timeline of the case lists the file among the Sources traced.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost } from '../support/api';
import { readFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const L4N = join(FILES, 'app-log4net.txt');
const NOTES = join(FILES, 'notes.txt');
const SHOTS = join(__dirname, '..', '..', '..', '.dev', 'l4n-shots');

const row = (page: Page, name: string) => page.getByRole('row').filter({ has: page.getByText(name, { exact: true }) });
const tick = (page: Page) => page.getByLabel('I confirm these files meet the conditions in the notice above.');
const picker = (page: Page) => page.getByLabel('Choose files to import');

async function openDrawer(page: Page, title: string) {
  await openCase(page, title);
  await page.getByRole('button', { name: 'Add a file' }).click();
  await expect(page.locator('#import-title')).toHaveText('Import files');
  const use = page.getByRole('button', { name: new RegExp('^Use this case: .*' + title) });
  if (await use.isVisible().catch(() => false)) await use.click();
  await expect(page.getByRole('heading', { name: 'Many files at once', level: 5 })).toBeVisible();
}

/** Picks a computer for every row that asks for one. */
async function chooseComputers(page: Page) {
  const boxes = page.getByRole('combobox', { name: /^Which computer wrote/ });
  for (let i = 0; i < (await boxes.count()); i++) {
    const box = boxes.nth(i);
    if ((await box.inputValue()) === '') await box.selectOption('web-a');
  }
}

async function shot(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, name + '.png'), fullPage: false });
}

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('L4N-01: a log4net block file is read by its content, the garbled block makes it Partly used, and the case lists it', async ({ page, api }) => {
  await createCase(api, 'QA Log4net Drawer');
  await goHome(page);
  await openDrawer(page, 'QA Log4net Drawer');

  // The name says nothing, so the person says it is an application log; the server then reads the content and finds the log4net layout.
  await picker(page).setInputFiles([L4N]);
  const kind = page.getByLabel('What app-log4net.txt is');
  await expect(kind).toHaveValue('');
  await kind.selectOption('app-log');
  await chooseComputers(page);
  await tick(page).check();
  await shot(page, 'drawer-before');
  await page.getByRole('button', { name: 'Import 1 file' }).click();

  await expect(page.getByText(/^Finished: /)).toBeVisible({ timeout: 60_000 });
  const r = row(page, 'app-log4net.txt');
  // Thirty-nine good blocks and one block whose time nobody can read (under the one-in-twenty limit): used, but only partly.
  await expect(r).toContainText('Partly used');
  await expect(r).not.toContainText('Rejected');
  await expect(r).toContainText(/Kept \d+ rows?\./);
  await shot(page, 'drawer-after');

  // The case holds the import, and it is the log4net block importer that read it.
  const caseId = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === 'QA Log4net Drawer').id as string;
  const imports = await api('GET', `/api/cases/${caseId}/imports`);
  expect(imports.status).toBe(200);
  const text = JSON.stringify(imports.json);
  expect(text).toContain('app-log4net.txt');
  expect(text).toContain('log4net-blocks');
  // The privacy rule of the application-log reader: the URL and the user address in the file are never kept.
  expect(text).not.toContain('app.example.test');
  expect(text).not.toContain('192.0.2.');
});

test('L4N-02: a file named log.txt is suggested as an application log, and a prose file chosen as one is Rejected', async ({ page, api }) => {
  await createCase(api, 'QA Log4net Names');
  await goHome(page);
  await openDrawer(page, 'QA Log4net Names');

  // The same content under the usual file name of a log4net block log: the list suggests the kind itself.
  await picker(page).setInputFiles([{ name: 'log.txt', mimeType: 'text/plain', buffer: readFileSync(L4N) }, { name: 'notes.txt', mimeType: 'text/plain', buffer: readFileSync(NOTES) }]);
  await expect(page.getByLabel('What log.txt is')).toHaveValue('app-log');
  await expect(page.getByLabel('What notes.txt is')).toHaveValue('');
  await page.getByLabel('What notes.txt is').selectOption('app-log');
  await chooseComputers(page);
  await tick(page).check();
  await page.getByRole('button', { name: 'Import 2 files' }).click();

  await expect(page.getByText(/^Finished: /)).toBeVisible({ timeout: 60_000 });
  await expect(row(page, 'log.txt')).toContainText('Partly used');
  // Prose is not a log: no importer claims it, and the file ends Rejected with a plain reason rather than a guess.
  await expect(row(page, 'notes.txt')).toContainText('Rejected');
  await expect(row(page, 'notes.txt')).not.toContainText('Used', { ignoreCase: false });
  await shot(page, 'drawer-names');
});

test('L4N-03: the create wizard holds the file from the Application logs card and reads it when the case is made', async ({ page, api }) => {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByText('Nothing is saved until the last step.')).toBeVisible();
  await page.getByLabel('Case name').fill('QA Log4net Wizard');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('heading', { name: 'Choose what to look at' })).toBeVisible();
  await page.getByRole('button', { name: 'Add files', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Add files', level: 5 })).toBeVisible();

  await page.getByLabel('Choose Application logs files').setInputFiles([L4N]);
  await expect(page.locator('[data-card-count="app"]')).toHaveText('1 file chosen');
  await expect(page.getByLabel('What app-log4net.txt is')).toHaveValue('app-log');
  // A text log carries no machine name, so the person says which computer wrote it (the server refuses the file without one).
  await chooseComputers(page);
  await shot(page, 'wizard-card');
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText(/^File chosen: app-log4net\.txt/)).toBeVisible();
  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();

  await expect.poll(async () => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string }>).some((c) => c.title === 'QA Log4net Wizard'), { timeout: 15_000 }).toBe(true);
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === 'QA Log4net Wizard');
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.imports, { timeout: 20_000 }).toBe(1);
  const text = JSON.stringify((await api('GET', `/api/cases/${made.id}/imports`)).json);
  expect(text).toContain('app-log4net.txt');
  expect(text).toContain('log4net-blocks');
});

test('L4N-04: the Investigation Timeline lists the file among the Sources traced', async ({ page, api }) => {
  const id = await createCase(api, 'QA Log4net Timeline');
  const form = new FormData();
  form.append('ackGating', 'true');
  form.append('ackText', (await import('../support/api')).BANNER);
  form.append('file', new Blob([readFileSync(L4N)]), 'app-log4net.txt');
  const q = new URLSearchParams({ importer: 'log4net-blocks', host: 'web-a' });
  const sent = await api('POST', `/api/cases/${id}/imports?${q.toString()}`, form, 'open');
  expect([200, 201, 202]).toContain(sent.status);

  await goHome(page);
  await openCase(page, 'QA Log4net Timeline');
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  const sources = page.locator('[data-testid=trace-sources]');
  await expect(sources).toBeVisible();
  // The application log has no lane of its own on the timeline; the file is listed as a source that is traced.
  await expect(sources.locator('.chip', { hasText: /^File/ })).toHaveCount(1);
  await expect(sources.locator('.chip', { hasText: /^File/ })).toHaveAttribute('aria-pressed', 'true');
  await shot(page, 'timeline-sources');
});
