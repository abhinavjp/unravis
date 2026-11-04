// The "Import files" panel of the create-a-case wizard (gaps G1, G2, G8): choose several files at once, see what each one looks like,
// read the IIS guide, add them, and find on the last step that the files are already held and are not asked for again.
import { test, expect, type Page } from '../support/fixtures';
import { goHome } from '../support/ui';
import { readFileSync } from 'node:fs';
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

test('FILES-01: two IIS files and one unknown file show their kinds, the guide is there, and the files are held for the last step', async ({ page }) => {
  await openPanel(page, 'QA Import Files');

  // The guide is part of the panel and open.
  await expect(page.getByText('Before you add IIS logs: what has to be on')).toBeVisible();
  await expect(page.getByText('time-taken').first()).toBeVisible();

  await page.getByLabel('Choose files to import').setInputFiles([W3C, HTTPERR, NOTES]);

  const w3c = row(page, 'u_ex261020.log');
  const httperr = row(page, 'httperr1.log');
  const notes = row(page, 'notes.txt');
  await expect(page.getByLabel('What u_ex261020.log is')).toHaveValue('iis-w3c');
  await expect(page.getByLabel('What httperr1.log is')).toHaveValue('iis-httperr');
  await expect(page.getByLabel('What notes.txt is')).toHaveValue('');
  await expect(w3c).toContainText('Looks like: IIS web log (W3C).');
  await expect(w3c).toContainText('UTC (as this type writes it)');
  await expect(httperr).toContainText('UTC (as this type writes it)');
  await expect(notes).toContainText('Not recognised, so it is left out.');
  await expect(w3c).toContainText(/\d+(\.\d+)? (B|KB)/);
  await expect(page.getByText(/^1 file needs a computer named\./)).toBeVisible();
  await expect(page.getByText('What will be read')).toBeVisible();

  const add = page.getByRole('button', { name: 'Add 2 files' });
  await expect(add).toBeEnabled();
  await add.click();
  await expect(page.getByText('2 files were added to your sources. 1 file was left out.')).toBeVisible();
  await expect(page.getByRole('heading', { level: 4, name: /Your sources/ })).toBeVisible();

  // Review: the files are shown as chosen, and nothing asks for them again.
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText(/^File chosen: u_ex261020\.log/)).toBeVisible();
  await expect(page.getByText(/^File chosen: httperr1\.log/)).toBeVisible();
  await expect(page.locator('input[type="file"]')).toHaveCount(0);
  await expect(page.getByText('No file chosen yet.')).toHaveCount(0);
  await expect(page.getByText('Choose a file for each file source')).toHaveCount(0);
});

test('FILES-02: a kind can be changed, a compressed file is refused, and Clear empties the list', async ({ page }) => {
  await openPanel(page, 'QA Import Files Change');
  await page.getByLabel('Choose files to import').setInputFiles([
    { name: 'notes.txt', mimeType: 'text/plain', buffer: readFileSync(NOTES) },
    { name: 'logs-october.zip', mimeType: 'application/zip', buffer: Buffer.from('PK') },
    { name: 'u_ex261021.log', mimeType: 'text/plain', buffer: Buffer.alloc(0) }
  ]);
  await expect(row(page, 'logs-october.zip')).toContainText('Compressed files are refused. Extract it first.');
  await expect(row(page, 'u_ex261021.log')).toContainText('This file is empty, so it is left out.');
  await expect(page.getByRole('button', { name: 'Add files' })).toBeDisabled();

  // Choosing a kind by hand puts the unknown file back in.
  await row(page, 'notes.txt').getByRole('combobox').selectOption('app-log');
  await expect(row(page, 'notes.txt')).toContainText('Chosen by you: App log');
  await expect(page.getByRole('button', { name: 'Add 1 file' })).toBeEnabled();

  await page.getByRole('button', { name: 'Clear' }).click();
  await expect(page.getByRole('table')).toHaveCount(0);
});

test('FILES-03: removing a source takes its file with it, and Review has nothing to ask for', async ({ page }) => {
  await openPanel(page, 'QA Import Files Remove');
  await page.getByLabel('Choose files to import').setInputFiles([W3C]);
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await expect(page.getByText('1 file was added to your sources.')).toBeVisible();
  await page.getByRole('button', { name: /^Remove/ }).first().click();
  await expect(page.getByText('No sources are chosen yet.')).toBeVisible();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('No sources chosen. The case will be made empty')).toBeVisible();
  await expect(page.locator('input[type="file"]')).toHaveCount(0);
});

test('FILES-04: the case is made, and the held IIS web log is read without choosing it again', async ({ page, api }) => {
  // The W3C fixture names its server WEB-A; the host list has to know it, as it does for any file on the way in.
  const hosts = await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-A', role: 'iis', tz: 'UTC' }] });
  expect(hosts.status).toBe(200);
  await openPanel(page, 'QA Import Files Create');
  await page.getByLabel('Choose files to import').setInputFiles([W3C]);
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText(/^File chosen: u_ex261020\.log/)).toBeVisible();
  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();
  // The case opens on whichever screen the app picks; what matters is that it exists and the file was read.
  await expect.poll(async () => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string }>).some((c) => c.title === 'QA Import Files Create'), { timeout: 15000 }).toBe(true);
  const list = await api('GET', '/api/cases');
  const made = list.json.cases.find((c: { title: string }) => c.title === 'QA Import Files Create');
  expect(made).toBeTruthy();
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.imports, { timeout: 20000 }).toBe(1);
  expect(JSON.stringify((await api('GET', `/api/cases/${made.id}/imports`)).json)).toContain('u_ex261020.log');
});

test('FILES-05: a file can be dropped from the list, and a source taken out later, and neither is on Review or in what is sent', async ({ page, api }) => {
  const hosts = await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-A', role: 'iis', tz: 'UTC' }] });
  expect(hosts.status).toBe(200);
  // Every upload of the create goes to /api/cases/{id}/imports; the file name is in the multipart part, which the browser does not
  // always hand back, so the uploads are counted here and the names are checked from what the case recorded.
  let uploads = 0;
  page.on('request', (request) => {
    if (request.method() === 'POST' && /\/api\/cases\/[^/]+\/imports/.test(request.url())) uploads++;
  });
  await openPanel(page, 'QA Import Files Unselect');
  const w3c = readFileSync(W3C);
  await page.getByLabel('Choose files to import').setInputFiles([
    { name: 'u_ex261020.log', mimeType: 'text/plain', buffer: w3c },
    { name: 'u_ex261021.log', mimeType: 'text/plain', buffer: w3c },
    { name: 'u_ex261022.log', mimeType: 'text/plain', buffer: w3c },
    { name: 'httperr1.log', mimeType: 'text/plain', buffer: readFileSync(HTTPERR) }
  ]);
  await expect(page.getByRole('button', { name: 'Add 4 files' })).toBeVisible();

  // Before adding: one row is taken off the list, another is set to "Don't use this file". The rest stay.
  await page.getByRole('button', { name: 'Remove httperr1.log from the list' }).click();
  await expect(row(page, 'httperr1.log')).toHaveCount(0);
  await row(page, 'u_ex261022.log').getByRole('combobox').selectOption('');
  await expect(page.getByRole('button', { name: 'Add 2 files' })).toBeEnabled();
  await page.getByRole('button', { name: 'Add 2 files' }).click();
  await expect(page.getByText('2 files were added to your sources. 1 file was left out.')).toBeVisible();

  // After adding: one source is taken out. The other keeps its file.
  await page.getByRole('button', { name: 'Remove u_ex261021.log' }).click();
  await expect(page.getByRole('button', { name: 'Remove u_ex261021.log' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Remove u_ex261020.log' })).toBeVisible();

  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText(/^File chosen: u_ex261020\.log/)).toBeVisible();
  await expect(page.getByText('u_ex261021.log')).toHaveCount(0);
  await expect(page.getByText('u_ex261022.log')).toHaveCount(0);
  await expect(page.getByText('httperr1.log')).toHaveCount(0);

  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();
  await expect.poll(async () => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string }>).some((c) => c.title === 'QA Import Files Unselect'), { timeout: 15000 }).toBe(true);
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === 'QA Import Files Unselect');
  expect(made).toBeTruthy();
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.imports, { timeout: 20000 }).toBe(1);
  const recorded = JSON.stringify((await api('GET', `/api/cases/${made.id}/imports`)).json);
  expect(recorded).toContain('u_ex261020.log');
  expect(recorded).not.toContain('u_ex261021.log');
  expect(recorded).not.toContain('u_ex261022.log');
  expect(recorded).not.toContain('httperr1.log');
  // One file: a check of the kind, then the read. Four files would have made at least eight uploads.
  expect(uploads).toBeGreaterThan(0);
  expect(uploads).toBeLessThanOrEqual(2);
});

// ---- Computers and time zones, the result table, the actions at the end, and a dropped folder (packets P4, P5, P10)

type ApiFn = (method: string, path: string, body?: unknown) => Promise<{ status: number; json: any }>;
const putHosts = (api: ApiFn, labels: string[]) => api('PUT', '/api/hosts', { version: 1, hosts: labels.map((label) => ({ label, role: 'iis', tz: 'UTC' })) });

async function holdFiles(page: Page, title: string, files: string[]) {
  await openPanel(page, title);
  await page.getByLabel('Choose files to import').setInputFiles(files);
}

async function toCreate(page: Page) {
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
}

test('FILES-06: a file that needs a computer says so, a computer can be added from the row, and the pick is kept on the last step', async ({ page, api }) => {
  expect((await putHosts(api, ['WEB-A'])).status).toBe(200);
  await holdFiles(page, 'QA Files Computer', [HTTPERR, W3C]);

  // The W3C log names its own server, so it has no computer choice. HTTP.sys does not, so it has one, and it is UTC.
  await expect(page.getByLabel('Which computer wrote u_ex261020.log')).toHaveCount(0);
  const pick = page.getByLabel('Which computer wrote httperr1.log');
  await expect(pick).toHaveValue('');
  await expect(row(page, 'httperr1.log')).toContainText('UTC (as this type writes it)');
  await expect(page.getByText(/^1 file needs a computer named\./)).toBeVisible();

  // A name the server would refuse is caught before any call, with the reason in words.
  await pick.selectOption({ label: 'Add a computer…' });
  await page.getByLabel('Name of the computer').fill('bad name');
  await page.getByRole('button', { name: 'Save this computer' }).click();
  await expect(page.getByText('Use letters, digits, dot, dash or underscore, and start with a letter or digit.')).toBeVisible();
  // The time zone is not needed for a type that writes UTC.
  await page.getByLabel('Name of the computer').fill('WEB-H');
  await page.getByRole('button', { name: 'Save this computer' }).click();
  await expect(pick).toHaveValue('WEB-H');
  await expect(page.getByText(/need a computer named/)).toHaveCount(0);
  const saved = JSON.stringify((await api('GET', '/api/hosts')).json);
  expect(saved).toContain('WEB-H');
  expect(saved).toContain('WEB-A');

  // Changing the kind lets go of the pick: a type that needs no computer shows no choice.
  await page.getByLabel('What httperr1.log is').selectOption('iis-w3c');
  await expect(page.getByLabel('Which computer wrote httperr1.log')).toHaveCount(0);
  await page.getByLabel('What httperr1.log is').selectOption('iis-httperr');
  await expect(page.getByLabel('Which computer wrote httperr1.log')).toHaveValue('');
  await page.getByLabel('Which computer wrote httperr1.log').selectOption('WEB-H');

  await page.getByRole('button', { name: 'Add 2 files' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByLabel('Which computer wrote httperr1.log')).toHaveValue('WEB-H');
});

test('FILES-07: Create waits for a computer, and the computer named on the last step is the one the server records', async ({ page, api }) => {
  expect((await putHosts(api, ['WEB-H'])).status).toBe(200);
  await holdFiles(page, 'QA Files Host Sent', [HTTPERR]);
  // Added without a computer: allowed, with a warning, and the last step will not let the case be made until one is named.
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await toCreate(page);
  await expect(page.getByText('Pick the computer for each file below that asks for one, or take that file off the list.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Create case' })).toBeDisabled();
  await page.getByLabel('Which computer wrote httperr1.log').selectOption('WEB-H');
  await expect(page.getByRole('button', { name: 'Create case' })).toBeEnabled();
  await page.getByRole('button', { name: 'Create case' }).click();

  // The result table: file, type, rows read, rows skipped, and the result in words.
  const table = page.getByRole('table', { name: /result for each file/i });
  await expect(table).toBeVisible();
  for (const head of ['File', 'Type', 'Rows read', 'Skipped', 'Result']) await expect(table.getByRole('columnheader', { name: head })).toBeVisible();
  const line = table.getByRole('row').filter({ hasText: 'httperr1.log' });
  await expect(line).toContainText('Read');
  await expect(line.getByRole('cell').nth(1)).toHaveText(/^\d[\d,]*$/);
  await expect.poll(async () => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string }>).some((c) => c.title === 'QA Files Host Sent'), { timeout: 15000 }).toBe(true);
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === 'QA Files Host Sent');
  const recorded = JSON.stringify((await api('GET', `/api/cases/${made.id}/imports`)).json);
  expect(recorded).toContain('httperr1.log');
  expect(recorded).toContain('WEB-H');
});

test('FILES-08: a file that was not read tells why and can be tried again, and the end of the run offers the timeline, the case and more files', async ({ page, api, allow }) => {
  allow(/422|409/, 'a file the server refuses on purpose');
  // Only WEB-H is known, so the W3C log (which names WEB-A) is refused and the HTTP.sys log (picked on WEB-H) is read.
  expect((await putHosts(api, ['WEB-H'])).status).toBe(200);
  await holdFiles(page, 'QA Files Result Table', [W3C, HTTPERR]);
  await page.getByLabel('Which computer wrote httperr1.log').selectOption('WEB-H');
  await page.getByRole('button', { name: 'Add 2 files' }).click();
  await toCreate(page);
  await page.getByRole('button', { name: 'Create case' }).click();

  const table = page.getByRole('table', { name: /result for each file/i });
  const ok = table.getByRole('row').filter({ hasText: 'httperr1.log' });
  const bad = table.getByRole('row').filter({ hasText: 'u_ex261020.log' });
  await expect(ok).toContainText('Read');
  await expect(bad).toContainText('Not read');
  await expect(page.getByRole('button', { name: 'Try u_ex261020.log again' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Leave u_ex261020.log out' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Try the failed files again' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open the timeline' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add more files' })).toBeVisible();

  // The person adds the missing computer (the list belongs to the server), then tries that file again.
  expect((await putHosts(api, ['WEB-H', 'WEB-A'])).status).toBe(200);
  await page.getByRole('button', { name: 'Try u_ex261020.log again' }).click();
  await expect(bad).toContainText('Read');
  await expect(bad).not.toContainText('Not read');
  await expect(page.getByRole('button', { name: 'Try the failed files again' })).toHaveCount(0);

  // The case has one import for each file, not two for the one that was tried again.
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === 'QA Files Result Table');
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.imports, { timeout: 20000 }).toBe(2);

  await page.getByRole('button', { name: 'Open the timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
});

test('FILES-09: a file that was not read can be left out, and Add more files opens the import drawer on the case', async ({ page, api, allow }) => {
  allow(/422|409/, 'a file the server refuses on purpose');
  expect((await putHosts(api, ['WEB-H'])).status).toBe(200);
  await holdFiles(page, 'QA Files Leave Out', [W3C, HTTPERR]);
  await page.getByLabel('Which computer wrote httperr1.log').selectOption('WEB-H');
  await page.getByRole('button', { name: 'Add 2 files' }).click();
  await toCreate(page);
  await page.getByRole('button', { name: 'Create case' }).click();

  const table = page.getByRole('table', { name: /result for each file/i });
  await expect(table.getByRole('row').filter({ hasText: 'u_ex261020.log' })).toContainText('Not read');
  await page.getByRole('button', { name: 'Leave u_ex261020.log out' }).click();
  await expect(page.getByText('The file was left out. The case keeps what it already has.')).toBeVisible();
  await expect(table.getByRole('row').filter({ hasText: 'u_ex261020.log' })).toHaveCount(0);
  await expect(table.getByRole('row').filter({ hasText: 'httperr1.log' })).toContainText('Read');
  await expect(page.getByRole('button', { name: 'Try the failed files again' })).toHaveCount(0);

  await page.getByRole('button', { name: 'Add more files' }).click();
  await expect(page.getByRole('heading', { name: /import/i }).first()).toBeVisible();
});

test('FILES-10: a dropped folder is walked, nested folders included, and every file keeps the folder it came from', async ({ page }) => {
  await openPanel(page, 'QA Files Folder Drop');
  const w3c = readFileSync(W3C, 'utf8');
  const httperr = readFileSync(HTTPERR, 'utf8');
  // A drop with a real folder cannot be made from a script, so the drop carries entries shaped like the browser's own: a folder
  // that gives its entries one at a time, with folders inside it.
  await page.evaluate(({ w3c, httperr }) => {
    const file = (name: string, text: string) => new File([text], name, { type: 'text/plain' });
    const fileEntry = (name: string, text: string) => ({ isFile: true, isDirectory: false, name, file: (ok: (f: File) => void) => ok(file(name, text)) });
    const dirEntry = (name: string, kids: any[]) => ({
      isFile: false, isDirectory: true, name,
      createReader() { let at = 0; return { readEntries(ok: (e: any[]) => void) { const part = kids.slice(at, at + 1); at += 1; ok(part); } }; }
    });
    const tree = dirEntry('logs', [fileEntry('notes.txt', 'hello'), dirEntry('W3SVC1', [fileEntry('u_ex261020.log', w3c)]), dirEntry('HTTPERR', [fileEntry('httperr1.log', httperr)])]);
    const target = document.querySelector('.drop') as HTMLElement;
    const event = new Event('drop', { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'dataTransfer', { value: { items: [{ kind: 'file', webkitGetAsEntry: () => tree }], files: [] } });
    target.dispatchEvent(event);
  }, { w3c, httperr });

  await expect(row(page, 'logs/W3SVC1/u_ex261020.log')).toBeVisible();
  await expect(row(page, 'logs/HTTPERR/httperr1.log')).toBeVisible();
  await expect(row(page, 'logs/notes.txt')).toContainText('Not recognised');
  await expect(page.getByLabel('What logs/W3SVC1/u_ex261020.log is')).toHaveValue('iis-w3c');
  await expect(page.getByLabel('What logs/HTTPERR/httperr1.log is')).toHaveValue('iis-httperr');
  await expect(page.getByRole('button', { name: 'Add 2 files' })).toBeVisible();
});
