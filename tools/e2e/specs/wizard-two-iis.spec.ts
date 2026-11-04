// Hub review B, C1 + C5: a create-a-case wizard with two IIS web logs, one tagged Production and one UAT. The tags are posted with import id 0
// before the files are read, so the Sources tab has to pair them by the file name (the saved label is the base name only).
//   TWOIIS-01  Two files, two environments: the Sources tab shows two rows, each with its own environment, and the filter finds the right file.
import { test, expect, type Page } from '../support/fixtures';
import { goHome } from '../support/ui';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const W3C = readFileSync(join(__dirname, '..', 'fixtures', 'files', 'u_ex261020.log'), 'utf8');
// Different bytes, or the second file is refused as a copy of the first.
const NEXT_DAY = W3C.replace(/2026-10-20/g, '2026-10-21');

async function openFilesPanel(page: Page, title: string) {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill(title);
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('heading', { name: 'Choose what to look at' })).toBeVisible();
  await page.getByRole('button', { name: 'Add files', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Add files', level: 5 })).toBeVisible();
}

test('TWOIIS-01: two IIS logs tagged Production and UAT keep their own environment on the Sources tab', async ({ page, api }) => {
  const hosts = await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-A', role: 'iis', tz: 'UTC' }] });
  expect(hosts.status).toBe(200);
  const title = 'QA Two IIS Environments';
  await openFilesPanel(page, title);
  // A folder pick names the file with its folder; only the file name may be saved as the source label.
  await page.getByLabel('Choose files to import').setInputFiles([
    { name: 'u_ex261020.log', mimeType: 'text/plain', buffer: Buffer.from(W3C) },
    { name: 'u_ex261021.log', mimeType: 'text/plain', buffer: Buffer.from(NEXT_DAY) }
  ]);
  await page.getByRole('button', { name: 'Add 2 files' }).click();
  await expect(page.getByText('2 files were added to your sources.')).toBeVisible();

  // The first file is Production, the second UAT.
  const prod = page.getByRole('group', { name: /u_ex261020\.log/ }).getByLabel('Environment');
  const uat = page.getByRole('group', { name: /u_ex261021\.log/ }).getByLabel('Environment');
  await prod.fill('Production');
  await prod.press('Enter');
  await uat.fill('UAT');
  await uat.press('Enter');
  await expect(prod).toHaveValue('Production');
  await expect(uat).toHaveValue('UAT');

  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();
  await expect.poll(async () => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string }>).some((c) => c.title === title), { timeout: 15000 }).toBe(true);
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === title);
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.imports, { timeout: 30000 }).toBe(2);

  // What the case holds: the tags carry the file name as their label, and both files were read.
  const tags = (await api('GET', `/api/cases/${made.id}/sources`)).json.sources as Array<{ label: string; env: string; importId: number }>;
  expect(tags.map((t) => t.label).sort()).toEqual(['u_ex261020.log', 'u_ex261021.log']);

  // The Sources tab: two rows (not four), each with its own environment.
  await page.getByRole('button', { name: 'Open the timeline' }).click();
  await page.getByRole('button', { name: 'Case home', exact: true }).click();
  await page.getByRole('tab', { name: 'Sources', exact: true }).click();
  const list = page.locator('[data-sources-list]');
  await expect(list.getByTestId('srclist-rows')).toBeVisible();
  await expect(list.locator('[data-source-row]')).toHaveCount(2);
  const first = list.locator('[data-source-row]').filter({ hasText: 'u_ex261020.log' });
  const second = list.locator('[data-source-row]').filter({ hasText: 'u_ex261021.log' });
  await expect(first.getByTestId('srclist-tags')).toContainText('Production');
  await expect(first.getByTestId('srclist-tags')).not.toContainText('UAT');
  await expect(second.getByTestId('srclist-tags')).toContainText('UAT');
  await expect(second.getByTestId('srclist-tags')).not.toContainText('Production');

  // The filter finds the file, not a loose tag.
  await list.getByTestId('srclist-filter').getByRole('button', { name: 'UAT', exact: true }).click();
  await expect(list.locator('[data-source-row]')).toHaveCount(1);
  await expect(list.locator('[data-source-row]')).toContainText('u_ex261021.log');
});

// Hub review B, C9: Remove destroys the button the keyboard was on, so the focus goes to the Remove button that took its place.
test('TWOIIS-02: removing a row moves the focus to the next Remove, else the one before, else the choose-files button', async ({ page }) => {
  await openFilesPanel(page, 'QA Remove Focus');
  const file = (name: string) => ({ name, mimeType: 'text/plain', buffer: Buffer.from(W3C) });
  await page.getByLabel('Choose files to import').setInputFiles([file('u_ex261020.log'), file('u_ex261021.log'), file('u_ex261022.log')]);
  const remove = (name: string) => page.getByRole('button', { name: `Remove ${name} from the list` });
  await expect(remove('u_ex261022.log')).toBeVisible();

  await remove('u_ex261021.log').click();
  await expect(remove('u_ex261021.log')).toHaveCount(0);
  await expect(remove('u_ex261022.log')).toBeFocused();

  // The last row goes: the one before it takes the focus.
  await remove('u_ex261022.log').click();
  await expect(remove('u_ex261020.log')).toBeFocused();

  // The only row goes: the button that chooses files takes the focus.
  await remove('u_ex261020.log').click();
  await expect(page.getByRole('button', { name: 'Choose files', exact: true })).toBeFocused();
});
