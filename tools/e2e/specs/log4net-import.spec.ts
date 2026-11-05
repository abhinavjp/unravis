// The log4net text-block application log (importer log4net-blocks): each entry is a few labelled lines (Logger name, Type, Server Time,
// UTC Time, Application URL, User IP) followed by the message and, for an error, the exception text. The fixture is synthetic.
//   L4N-01  Import files popup: the file is read by content (the name says nothing) and asks for a computer; sent with one, the garbled block is
//           left out and the case lists it.
//   L4N-02  Import files popup: a file named log.txt is suggested as an application log from its name alone, and a prose file is not supported.
//   L4N-03  Create wizard: the Application logs card holds the file, the case is made with it, and the file was read as log4net blocks.
//   L4N-04  The Investigation Timeline of the case lists the file among the Sources traced.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, importFile } from '../support/api';
import { readFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const L4N = join(FILES, 'app-log4net.txt');
const NOTES = join(FILES, 'notes.txt');
const SHOTS = join(__dirname, '..', '..', '..', '.dev', 'l4n-shots');

const popup = (page: Page) => page.getByRole('dialog', { name: 'Import files' });
const ack = (page: Page) => popup(page).getByRole('checkbox', { name: 'Confirm read-only' });

async function openPopup(page: Page, title: string) {
  await openCase(page, title);
  await page.getByRole('button', { name: 'Add a file' }).click();
  await expect(popup(page).getByRole('heading', { name: 'Import files', level: 2 })).toBeVisible();
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

test('L4N-01: a log4net block file is read by its content and asks for a computer; sent with one, the garbled block is left out and the case lists it', async ({ page, api }) => {
  const caseId = await createCase(api, 'QA Log4net Popup');
  await goHome(page);
  await openPopup(page, 'QA Log4net Popup');
  const dlg = popup(page);

  // The name says nothing, so the file waits in the group nobody recognised until the server reads its content.
  await dlg.getByLabel('Choose files to import', { exact: true }).setInputFiles([{ name: 'app-log4net.txt', mimeType: 'text/plain', buffer: readFileSync(L4N) }]);
  await expect(dlg.locator('[data-grp="unsupported"]')).toContainText('app-log4net.txt');
  await shot(page, 'popup-before');
  await dlg.getByRole('button', { name: 'Continue', exact: true }).click();
  // The server found the log4net layout, so the file is not "not supported"; a log names no computer, so a card asks for one before it is sent.
  await expect(dlg.getByText('1 fix needed before you can import')).toBeVisible({ timeout: 60_000 });
  await expect(dlg.locator('.fix')).toHaveCount(1);
  await expect(dlg.locator('.notsupp')).toHaveCount(0);
  await expect(ack(page)).toBeDisabled();
  await shot(page, 'popup-after');

  // With a computer, the block importer reads it: thirty-nine good blocks and one block whose time nobody can read (under the one-in-twenty limit).
  const sent = await importFile(api, caseId, readFileSync(L4N), { name: 'app-log4net.txt', importer: 'log4net-blocks', host: 'web-a' });
  expect(sent.status, sent.text).toBeLessThan(300);
  const imports = await api('GET', `/api/cases/${caseId}/imports`);
  expect(imports.status).toBe(200);
  const text = JSON.stringify(imports.json);
  expect(text).toContain('app-log4net.txt');
  expect(text).toContain('log4net-blocks');
  expect(text).toMatch(/"rejected":\s*1\b/);
  // The privacy rule of the application-log reader: the URL and the user address in the file are never kept.
  expect(text).not.toContain('app.example.test');
  expect(text).not.toContain('192.0.2.');
});

test('L4N-02: a file named log.txt is suggested as an application log, and a prose file is not supported', async ({ page, api }) => {
  await createCase(api, 'QA Log4net Names');
  await goHome(page);
  await openPopup(page, 'QA Log4net Names');
  const dlg = popup(page);

  // The same content under the usual file name of a log4net block log: the list suggests the kind itself.
  await dlg.getByLabel('Choose files to import', { exact: true }).setInputFiles([
    { name: 'log.txt', mimeType: 'text/plain', buffer: readFileSync(L4N) },
    { name: 'notes.txt', mimeType: 'text/plain', buffer: readFileSync(NOTES) }
  ]);
  const suggested = dlg.locator('[data-grp="kind:app-log"]');
  await expect(suggested).toContainText('log4net');
  await expect(suggested).toContainText('1 file');
  await expect(dlg.locator('[data-grp="unsupported"]')).toContainText('notes.txt');
  await dlg.getByRole('button', { name: 'Continue', exact: true }).click();

  // Prose is not a log: no importer claims it, so it is not supported rather than guessed. The log asks for a computer.
  await expect(dlg.getByText('1 fix needed before you can import')).toBeVisible({ timeout: 60_000 });
  await expect(dlg.locator('.notsupp')).toContainText('1 files are not supported');
  await expect(dlg.locator('.notsupp-row').filter({ hasText: 'notes.txt' })).toBeVisible();
  await expect(dlg.locator('.notsupp-row').filter({ hasText: 'log.txt' })).toHaveCount(0);
  await shot(page, 'popup-names');
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
