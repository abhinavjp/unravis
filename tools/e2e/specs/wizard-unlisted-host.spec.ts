// A real IIS log names its own server in s-computername, and a server that was never put in the host list is the usual case.
// The wizard has to take such a file under the computer the person picked for it, and to say plainly what to do when none was picked.
//   UNLISTED-01  A W3C log whose server is not in the list: the wizard adds that server to the list by itself, the log is read ("Used"), and nothing is asked.
//   UNLISTED-02  The same log when the list cannot be saved: it is refused as needing a computer, and the row tells the person what to do.
//   UNLISTED-03  The same log: it stays under the name it carries, and the last step offers no pick that would change nothing.
// The same file in the Import files popup of an open case is covered by IMPORT-09 and IMPORT-10 (import-drawer.spec.ts): a fix card adds the computer.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, filesTile } from '../support/ui';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// The fixture names its server WEB-A; this one is renamed to a server the host list does not hold. Synthetic name.
const W3C = readFileSync(join(__dirname, '..', 'fixtures', 'files', 'u_ex261020.log'), 'utf8').replace(/\bWEB-A\b/g, 'ISS-PROD-07');
const FILE = { name: 'u_ex261021.log', mimeType: 'text/plain', buffer: Buffer.from(W3C) };

async function holdFile(page: Page, title: string) {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill(title);
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('heading', { name: 'Choose what to look at' })).toBeVisible();
  await filesTile(page).click();
  await expect(page.getByRole('heading', { name: 'Add files', level: 5 })).toBeVisible();
  await page.getByLabel('Choose files to import').setInputFiles([FILE]);
}

test('UNLISTED-01: an IIS log from a server that is not in the host list is read, and its server is added to the list without asking', async ({ page, api }) => {
  expect((await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-B', role: 'iis', tz: 'UTC' }] })).status).toBe(200);
  const title = 'QA Unlisted Server Auto';
  await holdFile(page, title);

  // Nothing to answer per file: the panel says the computer is picked for the person.
  await expect(page.getByLabel('Which computer wrote u_ex261021.log')).toHaveCount(0);
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.locator('[data-hosts-summary]')).toHaveText('1 file under ISS-PROD-07.');
  await page.getByRole('checkbox', { name: /I confirm (this file|these files)/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();

  // The result table: the file is Used, not Rejected.
  const table = page.getByRole('table', { name: /result for each file/i });
  await expect(table).toBeVisible();
  const line = table.getByRole('row').filter({ hasText: 'u_ex261021.log' });
  await expect(line).toContainText('Used');
  await expect(line).not.toContainText('Rejected');

  // What the case holds: one import, filed under the server the file names, which is now in the list.
  await expect.poll(async () => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string }>).some((c) => c.title === title), { timeout: 15000 }).toBe(true);
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === title);
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.imports, { timeout: 30000 }).toBe(1);
  const recorded = JSON.stringify((await api('GET', `/api/cases/${made.id}/imports`)).json);
  expect(recorded).toContain('u_ex261021.log');
  expect(recorded).toContain('ISS-PROD-07');
  const hosts = JSON.stringify((await api('GET', '/api/hosts')).json);
  expect(hosts).toContain('ISS-PROD-07');
  expect(hosts).toContain('WEB-B');
});

test('UNLISTED-02: when the host list cannot be saved, the same log is refused as needing a computer, and the row says what to do', async ({ page, api, allow }) => {
  allow(/422|409|500/, 'a file the server refuses on purpose, and a host list that cannot be saved');
  expect((await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-B', role: 'iis', tz: 'UTC' }] })).status).toBe(200);
  await page.route('**/api/hosts', (route) => (route.request().method() === 'PUT' ? route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"blocked for the test"}' }) : route.continue()));
  await holdFile(page, 'QA Unlisted Server Not Saved');

  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('checkbox', { name: /I confirm (this file|these files)/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();

  const table = page.getByRole('table', { name: /result for each file/i });
  const line = table.getByRole('row').filter({ hasText: 'u_ex261021.log' });
  await expect(line).toContainText('Rejected');
  await expect(line).toContainText('needs a computer');
  await expect(line).toContainText('Add it to the list, then try again');
  await expect(line).toContainText('is not on your computer list');
});

test('UNLISTED-03: a log that names its own computer stays under that name, and the last step offers no pick for it', async ({ page, api }) => {
  expect((await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-B', role: 'iis', tz: 'UTC' }] })).status).toBe(200);
  const title = 'QA Unlisted Server Stays';
  await holdFile(page, title);
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.locator('section.hosts').getByRole('button', { name: 'Change' }).click();
  await expect(page.getByLabel('Which computer wrote u_ex261021.log')).toHaveCount(0);
  await page.locator('section.hosts').getByLabel('Use one computer for every file').selectOption('WEB-B');
  await expect(page.locator('[data-hosts-summary]')).toHaveText('1 file under ISS-PROD-07.');
  await page.getByRole('checkbox', { name: /I confirm (this file|these files)/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();

  const line = page.getByRole('table', { name: /result for each file/i }).getByRole('row').filter({ hasText: 'u_ex261021.log' });
  await expect(line).toContainText('Used');
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === title);
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.imports, { timeout: 30000 }).toBe(1);
  const recorded = JSON.stringify((await api('GET', `/api/cases/${made.id}/imports`)).json);
  expect(recorded).toContain('ISS-PROD-07');
  expect(recorded).not.toContain('WEB-B');
});
