// Source-side gaps on the Sources tab and the Investigation Timeline, each driven in the page against the demo server with real requests:
//   SRCGAP-01  Edit labels on a file's row saves its Environment, Portal and API (the first save makes the tag), and they stay after a reload.
//   SRCGAP-02  The time zone of a file's computer is saved from its row, and the list says the computer's zone applies to every file from it.
//   SRCGAP-03  An audit database row shows how many documents it holds and how old the newest one is.
//   SRCGAP-04  A hand-picked list of sources on the timeline switches on the file the drawer added, once the drawer closes.
//   SRCGAP-05  A switch that would leave more than 50 sources picked is refused, and a short note says so (the answer is changed to list 54 sources).
import { test, expect, type Locator, type Page } from '../support/fixtures';
import { createCase, ensureHost, importFile, HOST } from '../support/api';
import { goHome, openCase } from '../support/ui';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const W3C = readFileSync(join(FILES, 'u_ex261020.log'), 'utf8');

const APPLOG = [
  '2026-10-20 08:00:06,123 [1] INFO Orders.Api - order 1 placed',
  '2026-10-20 08:00:07,456 [1] WARN Orders.Api - order 2 slow',
  '2026-10-20 08:00:09,789 [1] ERROR Orders.Api - order 3 failed',
  ''
].join(String.fromCharCode(10));

type Imports = { imports: Array<{ id: number; fileName: string }> };

/** A case with one IIS file that names its computer (the demo host map knows it). Returns the case id and the import's id. */
async function caseWithFile(api: any, title: string, content = W3C, name = 'u_ex261020.log', importer = 'iis-w3c'): Promise<{ id: string; importId: number }> {
  await ensureHost(api);
  const id = await createCase(api, title);
  const r = await importFile(api, id, content, { name, importer, host: HOST });
  if (r.status !== 200 && r.status !== 201 && r.status !== 202) throw new Error('import failed: ' + r.status + ' ' + r.text);
  let importId = 0;
  await expect.poll(async () => {
    const list = ((await api('GET', '/api/cases/' + id + '/imports')).json as Imports).imports ?? [];
    importId = list.find((item) => item.fileName === name)?.id ?? 0;
    return importId;
  }, { timeout: 20_000 }).toBeGreaterThan(0);
  return { id, importId };
}

async function openList(page: Page, title: string): Promise<Locator> {
  await goHome(page);
  await openCase(page, title);
  await page.getByRole('tab', { name: 'Sources', exact: true }).click();
  const list = page.locator('[data-sources-list]');
  await expect(list.getByTestId('srclist-rows')).toBeVisible();
  return list;
}

const openDetails = async (row: Locator) => { await row.locator('button[aria-expanded]').click(); };

test('SRCGAP-01: Edit labels saves the Environment, Portal and API of a file, and a reload keeps them', async ({ page, api }) => {
  const { id, importId } = await caseWithFile(api, 'QA Source Labels');
  const list = await openList(page, 'QA Source Labels');
  const row = list.locator('[data-source-row="f' + importId + '"]');
  await expect(row.getByTestId('srclist-tags')).toContainText('no environment yet');
  await openDetails(row);
  await row.locator('[data-edit-open]').click();
  const save = row.locator('[data-edit-save]');
  await expect(save).toBeDisabled();
  await row.locator('[data-edit-env]').fill('Production');
  await row.locator('[data-edit-portal]').fill('Admin');
  await row.locator('[data-edit-api]').fill('Orders');
  await expect(save).toBeEnabled();
  await save.click();
  await expect(row.getByTestId('srclist-edit-note')).toHaveText('Saved.');
  await expect(row.getByTestId('srclist-tags')).toHaveText('Production · Admin · Orders');

  // A second edit changes the tag that now exists instead of making another one.
  await row.locator('[data-edit-open]').click();
  await row.locator('[data-edit-env]').fill('UAT');
  await row.locator('[data-edit-save]').click();
  await expect(row.getByTestId('srclist-tags')).toHaveText('UAT · Admin · Orders');
  const tags = (await api('GET', '/api/cases/' + id + '/sources')).json.sources as Array<{ env: string }>;
  expect(tags).toHaveLength(1);
  expect(tags[0].env).toBe('UAT');

  await page.reload();
  // A reload keeps the case open, so only the tab is chosen again.
  await expect(page.getByRole('heading', { name: 'QA Source Labels' })).toBeVisible();
  await page.getByRole('tab', { name: 'Sources', exact: true }).click();
  const again = page.locator('[data-sources-list]');
  await expect(again.getByTestId('srclist-rows')).toBeVisible();
  await expect(again.locator('[data-source-row="f' + importId + '"]').getByTestId('srclist-tags')).toHaveText('UAT · Admin · Orders');
});

test('SRCGAP-02: the time zone of a file’s computer is saved from its row and applies to every file from it', async ({ page, api }) => {
  // An IIS file says its times are UTC; an application log writes the clock of its computer, so only that one carries the computer's zone.
  const { importId } = await caseWithFile(api, 'QA Source Zone', APPLOG, 'orders-app.log', 'log4net-text');
  const list = await openList(page, 'QA Source Zone');
  const row = list.locator('[data-source-row="f' + importId + '"]');
  await openDetails(row);
  await expect(row.getByText('This is the time zone of ' + HOST + ', so it applies to every file from it.')).toBeVisible();
  const save = row.locator('[data-edit-zone-save]');
  await expect(save).toBeDisabled();
  await row.locator('[data-edit-zone]').fill('Not/AZone');
  await save.click();
  await expect(row.getByTestId('srclist-zone-note')).not.toHaveText('Saved.');
  await row.locator('[data-edit-zone]').fill('Asia/Dubai');
  await save.click();
  await expect(row.getByTestId('srclist-zone-note')).toHaveText('Saved.');
  const hosts = (await api('GET', '/api/hosts')).json.hosts as Array<{ label: string; tz: string }>;
  expect(hosts.find((host) => host.label === HOST)?.tz).toBe('Asia/Dubai');
  await expect(row.getByTestId('srclist-why')).toContainText('Asia/Dubai');
  await ensureHost(api);
});

test('SRCGAP-03: an audit database row shows its document count and the age of its newest document', async ({ page, api }) => {
  const id = await createCase(api, 'QA Source Size');
  // The demo has no MongoDB to test a connection against, so the case's tag list is answered here with a tag for the database the demo's live source reads.
  const sources = (await api('GET', '/api/sources')).json as Array<{ id: string; database: string }>;
  const database = sources.find((source) => source.database !== '')?.database ?? '';
  expect(database).not.toBe('');
  await page.route('**/api/cases/' + id + '/sources', async (route) => {
    if (route.request().method() !== 'GET') return route.fallback();
    await route.fulfill({ json: { sources: [{ id: 901, importId: 0, kind: 'mongo-database', label: database, env: 'Production', portal: '', api: '', classification: 'audit' }] } });
  });
  const list = await openList(page, 'QA Source Size');
  const size = list.getByTestId('srclist-size');
  await expect(size).toBeVisible();
  await expect(size).toContainText(/docs · newest .+ ago/);
});

// ---------------------------------------------------------------- the timeline after the drawer

const chip = (page: Page, sourceId: string) => page.locator('[data-testid=trace-sources] .chip[data-source-id="' + sourceId + '"]');

async function openTimeline(page: Page, title: string) {
  await goHome(page);
  await openCase(page, title);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect(page.locator('[data-testid=trace-sources]')).toBeVisible();
}

/** Adds one more IIS file through the drawer that "Add files" on an empty SQL lane opens, then closes the drawer. */
async function addThroughDrawer(page: Page, name: string, content: string) {
  await page.locator('.panel[data-lane=sql] [data-testid=lane-add-files]').click();
  await expect(page.locator('#import-title')).toHaveText('Import files');
  await page.getByLabel('Choose files to import').setInputFiles([{ name, mimeType: 'text/plain', buffer: Buffer.from(content) }]);
  await page.getByLabel('What ' + name + ' is').selectOption('iis-w3c');
  const computers = page.getByRole('combobox', { name: /^Which computer wrote/ });
  for (let i = 0; i < (await computers.count()); i++) if ((await computers.nth(i).inputValue()) === '') await computers.nth(i).selectOption(HOST);
  await page.getByLabel('I confirm these files meet the conditions in the notice above.').check();
  await page.getByRole('button', { name: 'Import 1 file' }).click();
  await expect(page.getByText(/^Finished: /)).toBeVisible({ timeout: 60_000 });
  await page.getByRole('dialog').getByRole('button', { name: 'Close import' }).click();
  await expect(page.locator('#import-title')).toHaveCount(0);
}

test('SRCGAP-04: a hand-picked list also switches on the file the drawer added, once the drawer closes', async ({ page, api }) => {
  const { id, importId } = await caseWithFile(api, 'QA Source New File');
  await openTimeline(page, 'QA Source New File');
  await expect(chip(page, 'imp_' + importId)).toHaveAttribute('aria-pressed', 'true');
  // Hand-pick: switch the live audit source Demo East off, which leaves a list of explicit sources.
  await chip(page, 'demo-east').click();
  await expect(chip(page, 'demo-east')).toHaveAttribute('aria-pressed', 'false');

  const second = W3C.replace('2026-10-20 08:00:06', '2026-10-20 08:10:06');
  await addThroughDrawer(page, 'u_ex261021.log', second);
  const list = ((await api('GET', '/api/cases/' + id + '/imports')).json as Imports).imports;
  const added = list.find((item) => item.fileName === 'u_ex261021.log');
  expect(added).toBeTruthy();
  await expect(chip(page, 'imp_' + added!.id)).toHaveAttribute('aria-pressed', 'true');
  // What was switched off stays off, and nothing is said about a cap.
  await expect(chip(page, 'demo-east')).toHaveAttribute('aria-pressed', 'false');
  await expect(page.getByTestId('tp-source-cap')).toHaveCount(0);
});

test('SRCGAP-05: a switch that would pass 50 picked sources is refused with a short note, and nothing is sent', async ({ page, api }) => {
  await caseWithFile(api, 'QA Source Cap');
  // The demo case has a handful of sources, so the answer is changed to list 54 more files.
  const fake = Array.from({ length: 54 }, (_, i) => ({ sourceId: 'imp_' + (9000 + i), label: 'Extra file ' + (i + 1) }));
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const res = await route.fetch();
    const body = await res.json();
    body.sources = [...(body.sources ?? []), ...fake];
    await route.fulfill({ response: res, json: body });
  });
  await openTimeline(page, 'QA Source Cap');
  await expect(chip(page, 'imp_9053')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByTestId('tp-source-cap')).toHaveCount(0);
  const sent: string[] = [];
  page.on('request', (r) => { if (r.url().includes('/tracing') && r.method() === 'POST') sent.push(r.postData() ?? ''); });
  await chip(page, 'imp_9000').click();
  await expect(page.getByTestId('tp-source-cap')).toHaveText('At most 50 sources can be traced at once. Switch one off first.');
  await expect(chip(page, 'imp_9000')).toHaveAttribute('aria-pressed', 'true');
  expect(sent).toHaveLength(0);
});
