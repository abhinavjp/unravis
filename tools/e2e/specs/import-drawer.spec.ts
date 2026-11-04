// P9 and P13. The import drawer of an open case takes many files at once (files, a folder, drag and drop, a .zip), shows what each one is,
// lets the person leave any of them out, and ends every file as Used, Partly used or Rejected with a plain reason and Try again.
// P13: the chips of the create wizard use the one readiness vocabulary (Ready, Partly ready, Cannot test yet, Failed).
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, importFile } from '../support/api';
import { readFileSync, mkdirSync } from 'node:fs';
import { deflateRawSync } from 'node:zlib';
import { join } from 'node:path';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const W3C = join(FILES, 'u_ex261020.log');
const HTTPERR = join(FILES, 'httperr1.log');
const NOTES = join(FILES, 'notes.txt');
const SHOTS = 'D:\\Projects\\api-audit-explorer\\.dev\\import-shots';

/** Opens the case, then the Import drawer for it (the drawer may already be on that case, or may ask to use it). */
async function openDrawerFor(page: Page, title: string) {
  await openCase(page, title);
  await page.getByRole('button', { name: 'Add a file' }).click();
  await expect(page.locator('#import-title')).toHaveText('Import files');
  const use = page.getByRole('button', { name: new RegExp('^Use this case: .*' + title) });
  if (await use.isVisible().catch(() => false)) await use.click();
}

async function openDrawer(page: Page, title: string) {
  await openDrawerFor(page, title);
  await expect(page.getByRole('heading', { name: 'Many files at once', level: 5 })).toBeVisible();
}

const row = (page: Page, name: string) => page.getByRole('row').filter({ has: page.getByText(name, { exact: true }) });
const tick = (page: Page) => page.getByLabel('I confirm these files meet the conditions in the notice above.');
const picker = (page: Page) => page.getByLabel('Choose files to import');

// A small zip writer so the test needs no archive tool: [{ name, data, method }]
function crc32(data: Buffer): number {
  let crc = ~0;
  for (const byte of data) {
    crc ^= byte;
    for (let k = 0; k < 8; k++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return ~crc >>> 0;
}
function makeZip(entries: Array<{ name: string; data: Buffer; method?: 0 | 8 }>): Buffer {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;
  for (const entry of entries) {
    const name = Buffer.from(entry.name, 'utf8');
    const method = entry.method ?? 8;
    const body = method === 8 ? deflateRawSync(entry.data) : entry.data;
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0); local.writeUInt16LE(20, 4); local.writeUInt16LE(0x800, 6); local.writeUInt16LE(method, 8);
    local.writeUInt32LE(crc32(entry.data), 14); local.writeUInt32LE(body.length, 18); local.writeUInt32LE(entry.data.length, 22); local.writeUInt16LE(name.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0); central.writeUInt16LE(20, 4); central.writeUInt16LE(20, 6); central.writeUInt16LE(0x800, 8); central.writeUInt16LE(method, 10);
    central.writeUInt32LE(crc32(entry.data), 16); central.writeUInt32LE(body.length, 20); central.writeUInt32LE(entry.data.length, 24); central.writeUInt16LE(name.length, 28); central.writeUInt32LE(offset, 42);
    locals.push(local, name, body);
    centrals.push(central, name);
    offset += 30 + name.length + body.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0); end.writeUInt16LE(entries.length, 8); end.writeUInt16LE(entries.length, 10); end.writeUInt32LE(directory.length, 12); end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}

/** Picks a computer for every row that asks for one (the first computer on the list). */
async function chooseComputers(page: Page) {
  const boxes = page.getByRole('combobox', { name: /^Which computer wrote/ });
  for (let i = 0; i < (await boxes.count()); i++) {
    const box = boxes.nth(i);
    if ((await box.inputValue()) === '') await box.selectOption('web-a');
  }
}

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('IMPORT-01: several files at once each show a kind, the unknown one is left out, and every imported file ends with a result', async ({ page, api }) => {
  await createCase(api, 'QA Import Many');
  await goHome(page);
  await openDrawer(page, 'QA Import Many');

  await picker(page).setInputFiles([W3C, HTTPERR, NOTES]);
  await expect(page.getByLabel('What u_ex261020.log is')).toHaveValue('iis-w3c');
  await expect(page.getByLabel('What httperr1.log is')).toHaveValue('iis-httperr');
  await expect(page.getByLabel('What notes.txt is')).toHaveValue('');
  await expect(row(page, 'notes.txt')).toContainText('Not recognised, so it is left out.');
  await chooseComputers(page);

  // The button waits for the notice to be confirmed, and counts only files that will be sent.
  const send = page.getByRole('button', { name: 'Import 2 files' });
  await expect(send).toBeDisabled();
  await tick(page).check();
  await expect(send).toBeEnabled();
  await send.click();

  await expect(page.getByText(/^Finished: /)).toBeVisible({ timeout: 60_000 });
  for (const name of ['u_ex261020.log', 'httperr1.log']) {
    // Both fixtures are clean files: the verdict is exactly Used, with the kept count, and neither of the other two verdicts.
    await expect(row(page, name)).toContainText('Used');
    await expect(row(page, name)).not.toContainText(/Partly used|Rejected/);
    await expect(row(page, name)).toContainText(/Kept \d+ rows?\./);
  }
  // The file that was left out is not sent and has no result.
  await expect(row(page, 'notes.txt')).not.toContainText(/Used|Rejected/);
  // The case shows the imports.
  const list = await api('GET', '/api/cases');
  expect(JSON.stringify(list.json)).toContain('QA Import Many');
});

test('IMPORT-02: any row can be left out: Remove, the Don\u2019t use choice and Clear all take files off the list before anything is sent', async ({ page, api }) => {
  await createCase(api, 'QA Import Leave Out');
  await goHome(page);
  await openDrawer(page, 'QA Import Leave Out');
  await picker(page).setInputFiles([W3C, HTTPERR, NOTES]);
  await chooseComputers(page);
  await expect(page.getByRole('button', { name: 'Import 2 files' })).toBeVisible();

  await page.getByRole('button', { name: 'Remove u_ex261020.log from the list' }).click();
  await expect(row(page, 'u_ex261020.log')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Import 1 file' })).toBeVisible();

  await page.getByLabel('What httperr1.log is').selectOption('');
  await expect(page.getByRole('button', { name: 'Import files', exact: true })).toBeDisabled();

  await page.getByLabel('What notes.txt is').selectOption('app-log');
  await expect(page.getByRole('button', { name: 'Import 1 file' })).toBeVisible();

  await page.getByRole('button', { name: 'Clear' }).click();
  await expect(page.getByRole('region', { name: 'Many files at once' }).getByRole('table')).toHaveCount(0);
  const mine = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === 'QA Import Leave Out');
  expect(mine.imports).toBe(0);
});

test('IMPORT-03: a .zip is opened in the page and its files join the list; a file that is not a zip is told in words', async ({ page, api }) => {
  await createCase(api, 'QA Import Zip');
  await goHome(page);
  await openDrawer(page, 'QA Import Zip');

  const zip = makeZip([
    { name: 'logs/u_ex261020.log', data: readFileSync(W3C) },
    { name: 'logs/', data: Buffer.alloc(0), method: 0 },
    { name: 'logs/httperr1.log', data: readFileSync(HTTPERR), method: 0 },
    { name: '__MACOSX/logs/._u_ex261020.log', data: Buffer.from('junk') }
  ]);
  await picker(page).setInputFiles([{ name: 'october.zip', mimeType: 'application/zip', buffer: zip }]);
  // Each file keeps the zip's name in front of its own path, so it is clear where it came from.
  await expect(page.getByLabel('What october.zip/logs/u_ex261020.log is')).toHaveValue('iis-w3c');
  await expect(page.getByLabel('What october.zip/logs/httperr1.log is')).toHaveValue('iis-httperr');
  await expect(page.getByRole('row', { name: /october.zip$/ })).toHaveCount(0);
  await expect(page.getByLabel(/What .*._u_ex261020.log is/)).toHaveCount(0);
  await chooseComputers(page);
  await tick(page).check();
  await page.getByRole('button', { name: 'Import 2 files' }).click();
  await expect(page.getByText(/^Finished: /)).toBeVisible({ timeout: 60_000 });

  await page.getByRole('button', { name: 'Clear' }).click();
  await picker(page).setInputFiles([{ name: 'fake.zip', mimeType: 'application/zip', buffer: Buffer.from('this is plain text and not a zip file at all') }]);
  await expect(page.getByText('fake.zip: This does not look like a zip file, so it was left out.')).toBeVisible();
  await expect(page.getByRole('region', { name: 'Many files at once' }).getByRole('table')).toHaveCount(0);
});

test('IMPORT-04: a file that fails never stops the others; it is Rejected with a reason, and Try again is there', async ({ page, api }) => {
  await createCase(api, 'QA Import Isolation');
  await goHome(page);
  await openDrawer(page, 'QA Import Isolation');

  const bad = { name: 'u_ex261021.log', mimeType: 'text/plain', buffer: Buffer.from('#Software: Microsoft Internet Information Services 10.0\n#Fields: date time\nnot a log line at all\n') };
  await picker(page).setInputFiles([
    { name: 'u_ex261019.log', mimeType: 'text/plain', buffer: readFileSync(W3C) },
    bad,
    { name: 'httperr1.log', mimeType: 'text/plain', buffer: readFileSync(HTTPERR) }
  ]);
  await chooseComputers(page);
  await tick(page).check();
  await page.getByRole('button', { name: 'Import 3 files' }).click();
  await expect(page.getByText(/^Finished: /)).toBeVisible({ timeout: 60_000 });

  // The last file was still sent after the middle one failed.
  await expect(row(page, 'httperr1.log')).toContainText(/Used|Partly used/);
  await expect(row(page, 'u_ex261019.log')).toContainText(/Used|Partly used/);
  await expect(row(page, 'u_ex261021.log')).toContainText('Rejected');
  const again = row(page, 'u_ex261021.log').getByRole('button', { name: /^Try again/ });
  await expect(again).toBeVisible();
  // Files that went in are not offered again.
  await expect(row(page, 'httperr1.log').getByRole('button', { name: /^Try again/ })).toHaveCount(0);

  // Try again needs the notice confirmed again, then sends that one file only.
  await again.click();
  await expect(page.getByText('Tick the box to confirm the notice, then import.')).toBeVisible();
  await tick(page).check();
  await again.click();
  await expect(row(page, 'u_ex261021.log')).toContainText('Rejected');
  await expect(page.getByText(/^Finished: 1 rejected\./)).toBeVisible({ timeout: 60_000 });
});

test('IMPORT-05: sending the same file again is Rejected as already imported, and the new file beside it is Used', async ({ page, api }) => {
  await createCase(api, 'QA Import Twice');
  await goHome(page);
  await openDrawer(page, 'QA Import Twice');
  const w3c = { name: 'u_ex261020.log', mimeType: 'text/plain', buffer: readFileSync(W3C) };
  await picker(page).setInputFiles([w3c]);
  await chooseComputers(page);
  await tick(page).check();
  await page.getByRole('button', { name: 'Import 1 file' }).click();
  await expect(page.getByText(/^Finished: /)).toBeVisible({ timeout: 60_000 });
  await page.getByRole('button', { name: 'Clear' }).click();

  await picker(page).setInputFiles([w3c, { name: 'httperr1.log', mimeType: 'text/plain', buffer: readFileSync(HTTPERR) }]);
  await chooseComputers(page);
  await tick(page).check();
  await page.getByRole('button', { name: 'Import 2 files' }).click();
  await expect(page.getByText(/^Finished: /)).toBeVisible({ timeout: 60_000 });
  await expect(row(page, 'u_ex261020.log')).toContainText('Rejected');
  await expect(row(page, 'u_ex261020.log')).toContainText('already imported');
  await expect(row(page, 'httperr1.log')).toContainText(/Used|Partly used/);
});

test('IMPORT-06: the one-file import still works beside the many-files part', async ({ page, api }) => {
  await createCase(api, 'QA Import Single');
  await goHome(page);
  await openDrawer(page, 'QA Import Single');
  await expect(page.locator('#import-file')).toBeVisible();
  await page.locator('#import-file').setInputFiles(W3C);
  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
  await page.getByRole('button', { name: 'Import file', exact: true }).click();
  await expect(page.getByText(/^Imported /).first()).toBeVisible({ timeout: 60_000 });
});

test('IMPORT-07: a closed case has no many-files part', async ({ page, api }) => {
  const id = await createCase(api, 'QA Import Closed');
  const closed = await api('POST', `/api/cases/${id}/close`, undefined, 'open');
  expect(closed.status).toBe(200);
  await createCase(api, 'QA Import Still Open');
  await goHome(page);
  await openDrawerFor(page, 'QA Import Still Open');
  await page.getByRole('button', { name: /^Use this case: .*QA Import Closed/ }).click();
  await expect(page.getByText('This case is closed. Choose or create another case to import files.')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Many files at once' })).toHaveCount(0);
});

for (const [name, scheme, width, height] of [['a', 'light', 1280, 1000], ['b', 'dark', 1280, 1000], ['c', 'light', 375, 900]] as const) {
  test(`IMPORT-08${name}: screenshot, ${scheme}${width === 375 ? ', 375 px wide' : ''}: the list with results, no sideways scroll`, async ({ page, api }) => {
    mkdirSync(SHOTS, { recursive: true });
    await page.emulateMedia({ colorScheme: scheme });
    await page.setViewportSize({ width: 1280, height });
    await createCase(api, 'QA Import Shots ' + name);
    await goHome(page);
    await openDrawer(page, 'QA Import Shots ' + name);
    await picker(page).setInputFiles([
      { name: 'u_ex261019.log', mimeType: 'text/plain', buffer: readFileSync(W3C) },
      { name: 'u_ex261021.log', mimeType: 'text/plain', buffer: Buffer.from('#Software: Microsoft Internet Information Services 10.0\n#Fields: date time\nnot a log line at all\n') },
      { name: 'httperr1.log', mimeType: 'text/plain', buffer: readFileSync(HTTPERR) },
      { name: 'notes.txt', mimeType: 'text/plain', buffer: readFileSync(NOTES) }
    ]);
    await chooseComputers(page);
    await tick(page).check();
    await page.getByRole('button', { name: 'Import 3 files' }).click();
    await expect(page.getByText(/^Finished: /)).toBeVisible({ timeout: 60_000 });
    await page.setViewportSize({ width, height });
    await page.getByRole('heading', { name: 'Many files at once', level: 5 }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: join(SHOTS, `import-drawer-${scheme}-${width}.png`), fullPage: false });
    await page.locator('.import-drawer').first().screenshot({ path: join(SHOTS, `import-drawer-${scheme}-${width}-full.png`) }).catch(() => {});
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(wide).toBe(false);
  });
}

const finding = (id: string, over: Record<string, unknown>) => ({
  id, detector: 'iis', kind: 'log-set', importer: 'iis-w3c', host: 'WEB01', display: 'Source ' + id, confidence: 90, shows: 'discover.iis.shows', how: '',
  sizeBytes: 2048, files: 3, oldest: '2026-10-01T00:00:00Z', newest: '2026-10-20T00:00:00Z', needsAdmin: false, cost: 'small', verified: true, unreadable: '', suggest: null, ...over
});

/** A scan with one verified file source, one file source that could not be read yet, and one that needs an export. */
async function mockScan(page: Page) {
  await page.route('**/api/discover/capabilities', (route) => route.fulfill({ json: { supported: true, elevated: false, os: 'windows' } }));
  await page.route(/\/api\/discover\/scans$/, (route) => route.fulfill({ status: 202, json: { scanId: '0123456789abcdef0123456789abcdef' } }));
  await page.route(/\/api\/discover\/scans\/0123456789abcdef0123456789abcdef$/, (route) => route.fulfill({
    json: {
      state: 'done', startedAt: new Date().toISOString(), gaps: [], progress: [],
      host: { label: 'WEB01', proposed: false, tz: 'UTC', cores: 4, aliases: [] },
      findings: [
        finding('okfile01', { display: 'IIS logs, ready' }),
        finding('unverif1', { display: 'App log not read yet', verified: false, importer: '', unreadable: 'discover.wrongformat.shows', detector: 'applog', kind: 'log-file' }),
        finding('exportme', { display: 'Needs an export', kind: 'export-needed', importer: '', verified: false, detector: 'sqlxe', how: 'discover.xel.how', shows: '' })
      ]
    }
  }));
}

test('READY-01: the sources step of the create wizard names each found source with one of the four readiness words and a tooltip', async ({ page }) => {
  await mockScan(page);
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill('QA Ready Chips');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('heading', { name: 'Choose what to look at' })).toBeVisible();
  await page.getByRole('button', { name: 'Scan this computer' }).click();
  const chips = page.locator('ul.chips li.chip:first-child');
  await expect.poll(async () => chips.count(), { timeout: 20_000 }).toBe(3);
  const words = new Set(['Ready', 'Partly ready', 'Cannot test yet', 'Failed']);
  const texts = (await chips.allInnerTexts()).map((t) => t.trim());
  for (const text of texts) expect(words.has(text), 'chip: ' + text).toBe(true);
  expect(texts.filter((t) => t === 'Ready')).toHaveLength(1);
  expect(texts.filter((t) => t === 'Cannot test yet')).toHaveLength(2);
  for (const tip of await chips.evaluateAll((all) => all.map((el) => el.getAttribute('title') ?? ''))) expect(tip.length).toBeGreaterThan(10);
  await expect(page.locator('body')).not.toContainText(/Not checked|Needs steps|Could not be read/);
});

test('READY-02: the case home of a case with imports never uses the old readiness words', async ({ page, api }) => {
  // A case with a kept file, so the lists and chips have something to describe (an empty case shows almost none of them).
  await ensureHost(api);
  const id = await createCase(api, 'QA Ready Words');
  const kept = await importFile(api, id, readFileSync(join(FILES, 'u_ex261020.log')), { name: 'u_ex261020.log', importer: 'iis-w3c' });
  expect(kept.status, kept.text).toBeLessThan(300);
  await goHome(page);
  await openCase(page, 'QA Ready Words');
  await expect(page.getByRole('heading', { name: 'Health checks', level: 3 }).first()).toBeVisible();
  await expect(page.locator('body')).toContainText('u_ex261020.log');
  await expect(page.locator('body')).not.toContainText(/\bNot checked\b|\bNeeds steps\b|\bNot ready\b/);
  // Every readiness sticker on the page uses one of the four words (a connection test sticker may also say Works or Not tested).
  const stickers = (await page.locator('.src-sticker').allInnerTexts()).map((t) => t.trim());
  expect(stickers.length, 'the case home shows at least one readiness sticker').toBeGreaterThan(0);
  for (const text of stickers) expect(['Ready', 'Partly ready', 'Cannot test yet', 'Failed', 'Works', 'Not tested'], 'sticker: ' + text).toContain(text);
});
