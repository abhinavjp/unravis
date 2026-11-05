// A real IIS log names its own server in s-computername, and a server that was never put in the host list is the usual case.
// The wizard has to take such a file under the computer the person picked for it, and to say plainly what to do when none was picked.
//   UNLISTED-01  A W3C log whose server is not in the list, with a computer picked: it is read ("Used"), filed under the picked computer.
//   UNLISTED-02  The same log with no computer picked: it is refused, and the reason tells the person to pick the computer or add it.
import { test, expect, type Page } from '../support/fixtures';
import { goHome } from '../support/ui';
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
  await page.getByRole('button', { name: 'Add files', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Add files', level: 5 })).toBeVisible();
  await page.getByLabel('Choose files to import').setInputFiles([FILE]);
}

test('UNLISTED-01: an IIS log from a server that is not in the host list is read under the computer picked for it', async ({ page, api }) => {
  expect((await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-B', role: 'iis', tz: 'UTC' }] })).status).toBe(200);
  const title = 'QA Unlisted Server Picked';
  await holdFile(page, title);

  // The choice is there, optional, and starts empty.
  const pick = page.getByLabel('Which computer wrote u_ex261021.log');
  await expect(pick).toHaveValue('');
  await pick.selectOption('WEB-B');
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByLabel('Which computer wrote u_ex261021.log')).toHaveValue('WEB-B');
  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();

  // The result table: the file is Used, not Rejected.
  const table = page.getByRole('table', { name: /result for each file/i });
  await expect(table).toBeVisible();
  const line = table.getByRole('row').filter({ hasText: 'u_ex261021.log' });
  await expect(line).toContainText('Used');
  await expect(line).not.toContainText('Rejected');

  // What the case holds: one import, filed under the picked computer; the server name in the file is not kept.
  await expect.poll(async () => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string }>).some((c) => c.title === title), { timeout: 15000 }).toBe(true);
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === title);
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.imports, { timeout: 30000 }).toBe(1);
  const imports = (await api('GET', `/api/cases/${made.id}/imports`)).json;
  const recorded = JSON.stringify(imports);
  expect(recorded).toContain('u_ex261021.log');
  expect(recorded).toContain('WEB-B');
  expect(recorded).not.toContain('ISS-PROD-07');
});

test('UNLISTED-02: the same log with no computer picked is refused, and the reason says to pick the computer or add it', async ({ page, api, allow }) => {
  allow(/422|409/, 'a file the server refuses on purpose');
  expect((await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-B', role: 'iis', tz: 'UTC' }] })).status).toBe(200);
  await holdFile(page, 'QA Unlisted Server Not Picked');

  // Adding it without a computer is allowed: the choice is optional, because a file may name a server that is in the list.
  await expect(page.getByLabel('Which computer wrote u_ex261021.log')).toHaveValue('');
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();

  const table = page.getByRole('table', { name: /result for each file/i });
  const line = table.getByRole('row').filter({ hasText: 'u_ex261021.log' });
  await expect(line).toContainText('Rejected');
  await expect(line).toContainText('not in your list');
  await expect(line).toContainText('Pick the computer that wrote it');
  await expect(line).not.toContainText('ISS-PROD-07');
});
