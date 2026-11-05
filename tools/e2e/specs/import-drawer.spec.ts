// P9, P13 and P15. The "Import files" popup of an open case takes many files at once (files, a folder, drag and drop, a .zip) in three steps:
// Choose, Check (the server reads every file without keeping it, and says what each one needs) and Result. It lets the person leave any file out,
// fix a missing computer on a card, and ends with a count of what came in and what did not.
// P13: the chips of the create wizard use the one readiness vocabulary (Ready, Partly ready, Cannot test yet, Failed).
//   IMPORT-01  several files at once: kinds in the Choose step, a verdict for each file in the Check step, a count in the Result step
//   IMPORT-02  any file can be left out: untick, Remove, and Close with the Discard question, all before anything is sent
//   IMPORT-03  a .zip is opened in the page and its files join the list; a file that is not a zip is told in words
//   IMPORT-04  a file that fails never stops the others; it is listed under Not imported
//   IMPORT-05  sending the same file again is not sent; the new file beside it is imported
//   IMPORT-06  one file goes through the same three steps
//   IMPORT-07  a closed case cannot open the popup
//   IMPORT-08  screenshots, light and dark, 375 px wide
//   IMPORT-09  a file from a server the computer list lacks gets a fix card: leave out, bring back, add and accept
//   IMPORT-10  a computer that left the list after the check sends the person back to its card, and adding it finishes the import
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, importFile, type ApiFn } from '../support/api';
import { readFileSync, mkdirSync } from 'node:fs';
import { deflateRawSync } from 'node:zlib';
import { join } from 'node:path';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const W3C = join(FILES, 'u_ex261020.log');
const NOTES = join(FILES, 'notes.txt');
const SHOTS = 'D:\\Projects\\api-audit-explorer\\.dev\\import-shots';

const payload = (name: string, buffer: Buffer) => ({ name, mimeType: 'text/plain', buffer });
// The same log under another letter case of its server name: different bytes, so it is not a duplicate, and the host list still knows it.
const W3C_OTHER = readFileSync(W3C, 'utf8').replace(/\bWEB-A\b/g, 'web-a');
// A server the host list never held (the list holds web-a only).
const W3C_UNLISTED = readFileSync(W3C, 'utf8').replace(/\bWEB-A\b/g, 'ISS-PROD-07');
const BAD_LOG = '#Software: Microsoft Internet Information Services 10.0\n#Fields: date time\nnot a log line at all\n';

const popup = (page: Page) => page.getByRole('dialog', { name: 'Import files' });
const picker = (page: Page) => popup(page).getByLabel('Choose files to import', { exact: true });
const ack = (page: Page) => popup(page).getByRole('checkbox', { name: 'Confirm read-only' });
const hero = (page: Page) => popup(page).locator('.result-hero');
const group = (page: Page, name: string) => popup(page).locator(`[data-grp="${name}"]`);
const notSupportedRow = (page: Page, name: string) => popup(page).locator('.notsupp-row').filter({ hasText: name });

/** Opens the case, then the Import files popup from the case page's "Add a file". */
async function openPopup(page: Page, title: string) {
  await openCase(page, title);
  await page.getByRole('button', { name: 'Add a file' }).click();
  await expect(popup(page)).toBeVisible();
  await expect(popup(page).getByRole('heading', { name: 'Import files', level: 2 })).toBeVisible();
}

/** Presses Continue and waits until the check is back (the read-only box is drawn once it is). */
async function toCheck(page: Page) {
  await popup(page).getByRole('button', { name: 'Continue', exact: true }).click();
  await expect(ack(page)).toBeVisible({ timeout: 60_000 });
}

/** Ticks the read-only box and presses Import, whatever number of files the button counts. */
async function importAll(page: Page) {
  await ack(page).check();
  const send = popup(page).getByRole('button', { name: /^Import \d+ files?$/ });
  await expect(send).toBeEnabled();
  await send.click();
}

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

const caseImports = async (api: ApiFn, title: string): Promise<number> =>
  (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === title).imports as number;

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('IMPORT-01: several files at once each show a kind, the unknown one is not supported, and the result counts what came in', async ({ page, api }) => {
  await createCase(api, 'QA Import Many');
  await goHome(page);
  await openPopup(page, 'QA Import Many');
  const dlg = popup(page);
  await expect(dlg.locator('.case-chip')).toContainText('QA Import Many');
  await expect(dlg.locator('.prog-line')).toContainText('Step 1 of 3 · Choose files to import');
  await expect(dlg.getByRole('button', { name: 'Continue', exact: true })).toBeDisabled();

  await picker(page).setInputFiles([payload('u_ex261020.log', readFileSync(W3C)), payload('u_ex261021.log', Buffer.from(W3C_OTHER)), payload('notes.txt', readFileSync(NOTES))]);
  // Step 1: one group for each kind the names suggest; the file nobody recognised stays in view with a warning.
  const iis = group(page, 'kind:iis-w3c');
  await expect(iis).toContainText('IIS web logs');
  await expect(iis).toContainText('2 files');
  await expect(iis).toContainText('found');
  const unknown = group(page, 'unsupported');
  await expect(unknown).toContainText('Not recognised yet');
  await expect(unknown).toContainText('needs checking');
  await expect(unknown).toContainText('notes.txt');
  await iis.locator('.grp-head').click();
  await expect(iis).toContainText('u_ex261020.log');
  await expect(iis).toContainText('u_ex261021.log');

  // Step 2: the server's verdict for each file. Both logs are ready; the prose file is not supported.
  await toCheck(page);
  await expect(dlg.locator('.prog-line')).toContainText('Step 2 of 3 · Check files with the server before import');
  await expect(group(page, 'kind:iis-w3c')).toContainText('2 files, ready');
  await expect(dlg.locator('.notsupp')).toContainText('1 files are not supported');
  await expect(notSupportedRow(page, 'notes.txt')).toBeVisible();
  await expect(dlg.getByText('I understand this is read-only and nothing is changed on these computers')).toBeVisible();
  // The button waits for the read-only box and counts only files that will be sent.
  const send = dlg.getByRole('button', { name: 'Import 2 files', exact: true });
  await expect(send).toBeDisabled();
  await ack(page).check();
  await expect(send).toBeEnabled();
  await send.click();

  // Step 3: the count, the files by kind, and the file that was not imported.
  await expect(hero(page)).toContainText('2 files imported', { timeout: 60_000 });
  await expect(dlg.locator('.prog-line')).toContainText('Step 3 of 3 · Result');
  await expect(dlg.getByRole('heading', { name: '// Imported', level: 4, exact: true })).toBeVisible();
  await expect(dlg.locator('.result-line').filter({ hasText: 'IIS web logs' })).toContainText('2 files');
  await expect(dlg.getByRole('heading', { name: '// Not imported', level: 4, exact: true })).toBeVisible();
  await expect(dlg.locator('.result-line').filter({ hasText: 'notes.txt' })).toBeVisible();
  await expect(dlg.getByRole('button', { name: 'Import more', exact: true })).toBeVisible();
  await expect(dlg.getByRole('button', { name: 'Open timeline', exact: true })).toBeVisible();
  await expect.poll(() => caseImports(api, 'QA Import Many')).toBe(2);
});

test('IMPORT-02: any file can be left out: untick, Remove and Close with the Discard question take files off the list before anything is sent', async ({ page, api }) => {
  await createCase(api, 'QA Import Leave Out');
  await goHome(page);
  await openPopup(page, 'QA Import Leave Out');
  const dlg = popup(page);
  await picker(page).setInputFiles([payload('u_ex261020.log', readFileSync(W3C)), payload('u_ex261021.log', Buffer.from(W3C_OTHER)), payload('notes.txt', readFileSync(NOTES))]);
  await toCheck(page);
  await expect(dlg.getByRole('button', { name: 'Import 2 files', exact: true })).toBeVisible();

  // Untick one file of the ready group: the button counts one less. Ticking it again brings it back.
  await group(page, 'kind:iis-w3c').locator('.grp-head').click();
  await dlg.getByRole('checkbox', { name: 'u_ex261020.log', exact: true }).uncheck();
  await expect(dlg.getByRole('button', { name: 'Import 1 file', exact: true })).toBeVisible();
  await dlg.getByRole('checkbox', { name: 'u_ex261020.log', exact: true }).check();
  await expect(dlg.getByRole('button', { name: 'Import 2 files', exact: true })).toBeVisible();

  // Remove takes a file that is not supported off the list.
  await notSupportedRow(page, 'notes.txt').getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(dlg.getByText('All unsupported files removed.')).toBeVisible();
  await expect(notSupportedRow(page, 'notes.txt')).toHaveCount(0);

  // Close asks first while files are listed. Keep them (or Esc) keeps the popup; Discard clears it.
  await dlg.getByRole('button', { name: 'Close import' }).click();
  const ask = page.getByRole('alertdialog', { name: 'Discard 2 files?' });
  await expect(ask).toBeVisible();
  await expect(ask).toContainText('These files are still on the list. Discarding clears them and closes the popup.');
  await ask.getByRole('button', { name: 'Keep them' }).click();
  await expect(ask).toHaveCount(0);
  await expect(dlg.getByRole('button', { name: 'Import 2 files', exact: true })).toBeVisible();
  await dlg.getByRole('button', { name: 'Close import' }).click();
  await expect(ask).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(ask).toHaveCount(0);
  await expect(dlg).toBeVisible();
  await dlg.getByRole('button', { name: 'Close import' }).click();
  await ask.getByRole('button', { name: 'Discard', exact: true }).click();
  await expect(popup(page)).toHaveCount(0);
  expect(await caseImports(api, 'QA Import Leave Out')).toBe(0);
});

test('IMPORT-03: a .zip is opened in the page and its files join the list; a file that is not a zip is told in words', async ({ page, api }) => {
  await createCase(api, 'QA Import Zip');
  await goHome(page);
  await openPopup(page, 'QA Import Zip');
  const dlg = popup(page);

  const zip = makeZip([
    { name: 'logs/u_ex261020.log', data: readFileSync(W3C) },
    { name: 'logs/', data: Buffer.alloc(0), method: 0 },
    { name: 'logs/u_ex261021.log', data: Buffer.from(W3C_OTHER), method: 0 },
    { name: '__MACOSX/logs/._u_ex261020.log', data: Buffer.from('junk') }
  ]);
  await picker(page).setInputFiles([{ name: 'october.zip', mimeType: 'application/zip', buffer: zip }]);
  // Each file keeps the zip's name in front of its own path, so it is clear where it came from.
  const iis = group(page, 'kind:iis-w3c');
  await expect(iis).toContainText('2 files · folder october.zip');
  await iis.locator('.grp-head').click();
  await expect(iis).toContainText('october.zip/logs/u_ex261020.log');
  await expect(iis).toContainText('october.zip/logs/u_ex261021.log');
  await expect(dlg.locator('.file-name').filter({ hasText: /^october\.zip$/ })).toHaveCount(0);
  await expect(dlg.getByText('._u_ex261020.log')).toHaveCount(0);

  await toCheck(page);
  await expect(dlg.getByRole('button', { name: 'Import 2 files', exact: true })).toBeVisible();
  await importAll(page);
  await expect(hero(page)).toContainText('2 files imported', { timeout: 60_000 });

  // A file named .zip that is not one stays on the list as a file that cannot be read, with the way out in words.
  await dlg.getByRole('button', { name: 'Import more', exact: true }).click();
  await expect(dlg.locator('.prog-line')).toContainText('Step 1 of 3');
  await picker(page).setInputFiles([{ name: 'fake.zip', mimeType: 'application/zip', buffer: Buffer.from('this is plain text and not a zip file at all') }]);
  await expect(group(page, 'unsupported')).toContainText('fake.zip');
  await toCheck(page);
  const row = notSupportedRow(page, 'fake.zip');
  await expect(row).toContainText('This is a compressed file. Extract it first, then choose the extracted file.');
  await expect(row.getByRole('button', { name: 'Remove from list', exact: true })).toBeVisible();
  await expect(dlg.getByRole('button', { name: 'Import 0 files', exact: true })).toBeDisabled();
});

test('IMPORT-04: a file that fails never stops the others; it is listed under Not imported', async ({ page, api, allow }) => {
  allow(/422|409/, 'a file the server refuses on purpose');
  await createCase(api, 'QA Import Isolation');
  await goHome(page);
  await openPopup(page, 'QA Import Isolation');
  const dlg = popup(page);

  await picker(page).setInputFiles([
    payload('u_ex261019.log', readFileSync(W3C)),
    payload('u_ex261021.log', Buffer.from(BAD_LOG)),
    payload('u_ex261022.log', Buffer.from(W3C_OTHER))
  ]);
  await toCheck(page);
  await importAll(page);

  // The files after the bad one were still sent. The bad one is counted in Not imported, whether the check or the import refused it.
  await expect(hero(page)).toContainText('2 files imported', { timeout: 60_000 });
  await expect(dlg.getByRole('heading', { name: '// Not imported', level: 4, exact: true })).toBeVisible();
  await expect(dlg.locator('.result-line').filter({ hasText: 'u_ex261021.log' })).toBeVisible();
  await expect(dlg.locator('.result-line').filter({ hasText: 'u_ex261019.log' })).toHaveCount(0);
  await expect(dlg.locator('.result-line').filter({ hasText: 'u_ex261022.log' })).toHaveCount(0);
  await expect.poll(() => caseImports(api, 'QA Import Isolation')).toBe(2);
});

test('IMPORT-05: a file already in the case is not sent again, and the new file beside it is imported', async ({ page, api, allow }) => {
  allow(/422|409/, 'a file the server refuses on purpose');
  await createCase(api, 'QA Import Twice');
  await goHome(page);
  await openPopup(page, 'QA Import Twice');
  const dlg = popup(page);
  const w3c = payload('u_ex261020.log', readFileSync(W3C));
  await picker(page).setInputFiles([w3c]);
  await toCheck(page);
  await importAll(page);
  await expect(hero(page)).toContainText('1 files imported', { timeout: 60_000 });

  await dlg.getByRole('button', { name: 'Import more', exact: true }).click();
  await picker(page).setInputFiles([w3c, payload('u_ex261021.log', Buffer.from(W3C_OTHER))]);
  await toCheck(page);
  // The check reads every file without keeping it, so it already knows the first one is in the case: that file is left out of the count.
  await expect(notSupportedRow(page, 'u_ex261020.log')).toBeVisible();
  await expect(dlg.getByRole('button', { name: 'Import 1 file', exact: true })).toBeVisible();
  await importAll(page);
  await expect(hero(page)).toContainText('1 files imported', { timeout: 60_000 });
  await expect(dlg.locator('.result-line').filter({ hasText: 'u_ex261020.log' })).toBeVisible();
  await expect.poll(() => caseImports(api, 'QA Import Twice')).toBe(2);
});

test('IMPORT-06: one file goes through the same three steps as many', async ({ page, api }) => {
  await createCase(api, 'QA Import Single');
  await goHome(page);
  await openPopup(page, 'QA Import Single');
  const dlg = popup(page);
  // There is no second, single-file part beside the popup any more.
  await expect(page.locator('#import-file')).toHaveCount(0);
  await expect(page.getByText('Many files at once')).toHaveCount(0);
  await picker(page).setInputFiles([payload('u_ex261020.log', readFileSync(W3C))]);
  await toCheck(page);
  const send = dlg.getByRole('button', { name: 'Import 1 file', exact: true });
  await expect(send).toBeDisabled();
  await importAll(page);
  await expect(hero(page)).toContainText('1 files imported', { timeout: 60_000 });
  await expect.poll(() => caseImports(api, 'QA Import Single')).toBe(1);
});

test('IMPORT-07: a closed case cannot open the popup', async ({ page, api }) => {
  const id = await createCase(api, 'QA Import Closed');
  const closed = await api('POST', `/api/cases/${id}/close`, undefined, 'open');
  expect(closed.status).toBe(200);
  await goHome(page);
  await openCase(page, 'QA Import Closed');
  await expect(page.getByRole('button', { name: 'Add a file' })).toBeDisabled();
  await expect(popup(page)).toHaveCount(0);
});

for (const [name, scheme, width, height] of [['a', 'light', 1280, 1000], ['b', 'dark', 1280, 1000], ['c', 'light', 375, 900]] as const) {
  test(`IMPORT-08${name}: screenshot, ${scheme}${width === 375 ? ', 375 px wide' : ''}: the result with a file left out, no sideways scroll`, async ({ page, api }) => {
    mkdirSync(SHOTS, { recursive: true });
    await page.emulateMedia({ colorScheme: scheme });
    await page.setViewportSize({ width: 1280, height });
    await createCase(api, 'QA Import Shots ' + name);
    await goHome(page);
    await openPopup(page, 'QA Import Shots ' + name);
    await picker(page).setInputFiles([
      payload('u_ex261019.log', readFileSync(W3C)),
      payload('u_ex261021.log', Buffer.from(BAD_LOG)),
      payload('u_ex261022.log', Buffer.from(W3C_OTHER)),
      payload('notes.txt', readFileSync(NOTES))
    ]);
    await toCheck(page);
    await importAll(page);
    await expect(hero(page)).toContainText('2 files imported', { timeout: 60_000 });
    await page.setViewportSize({ width, height });
    await page.screenshot({ path: join(SHOTS, `import-popup-${scheme}-${width}.png`), fullPage: false });
    await popup(page).screenshot({ path: join(SHOTS, `import-popup-${scheme}-${width}-full.png`) }).catch(() => {});
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(wide).toBe(false);
    const box = await popup(page).boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= width + 1).toBe(true);
  });
}

test('IMPORT-09: a file from a server the computer list lacks gets a fix card: leave out, bring back, add and accept', async ({ page, api }) => {
  const id = await createCase(api, 'QA Import Fix Card');
  await goHome(page);
  await openPopup(page, 'QA Import Fix Card');
  const dlg = popup(page);
  await picker(page).setInputFiles([payload('u_ex261030.log', Buffer.from(W3C_UNLISTED))]);
  await toCheck(page);

  const card = dlg.locator('.fix');
  await expect(card).toContainText('1 IIS files come from ISS-PROD-07, which is not on your computer list.');
  await expect(card).toContainText('Unravis will not guess which computer wrote a log. Add it once and every file from that server is accepted.');
  await expect(card).toContainText('IIS logs are already UTC, no time zone needed.');
  // Nothing can be sent while the card is open.
  await expect(dlg.getByText('1 fix needed before you can import')).toBeVisible();
  await expect(ack(page)).toBeDisabled();
  await expect(dlg.getByRole('button', { name: 'Import 1 file', exact: true })).toBeDisabled();

  // Leave the file out, then bring it back.
  await card.getByRole('button', { name: 'Leave these 1 files out', exact: true }).click();
  await expect(card).toContainText('1 IIS files left out');
  await expect(dlg.getByText('1 fix needed before you can import')).toHaveCount(0);
  await expect(dlg.getByRole('button', { name: 'Import 0 files', exact: true })).toBeDisabled();
  await card.getByRole('button', { name: 'Bring them back', exact: true }).click();
  await expect(dlg.getByText('1 fix needed before you can import')).toBeVisible();

  // Add the computer once, and the file is accepted.
  await card.getByRole('button', { name: 'Add ISS-PROD-07 as a computer and accept 1 files', exact: true }).click();
  await expect(card).toContainText('Added ISS-PROD-07, 1 files accepted');
  await expect(card.getByRole('button', { name: 'Undo', exact: true })).toBeVisible();
  const hosts = JSON.stringify((await api('GET', '/api/hosts')).json);
  expect(hosts).toContain('ISS-PROD-07');
  expect(hosts).toContain('web-a');

  await importAll(page);
  await expect(hero(page)).toContainText('1 files imported', { timeout: 60_000 });
  await expect(dlg.locator('.computers-line')).toContainText('Added 1 computers: ISS-PROD-07.');
  await expect.poll(() => caseImports(api, 'QA Import Fix Card')).toBe(1);
  // The case keeps the file under the computer that was just added.
  const recorded = JSON.stringify((await api('GET', `/api/cases/${id}/imports`)).json);
  expect(recorded).toContain('u_ex261030.log');
  expect(recorded).toContain('ISS-PROD-07');
});

test('IMPORT-10: a computer that left the list after the check sends the person back to its card, and adding it finishes the import', async ({ page, api, allow }) => {
  allow(/422|409/, 'a file the server refuses on purpose');
  await createCase(api, 'QA Import Retry');
  await goHome(page);
  await openPopup(page, 'QA Import Retry');
  const dlg = popup(page);
  await picker(page).setInputFiles([payload('u_ex261020.log', readFileSync(W3C))]);
  await toCheck(page);
  await expect(group(page, 'kind:iis-w3c')).toContainText('1 file, ready');
  await ack(page).check();

  // The list changes between the check and the import: web-a is gone, so the server refuses the file for its computer.
  expect((await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-B', role: 'iis', tz: 'UTC' }] })).status).toBe(200);
  await dlg.getByRole('button', { name: 'Import 1 file', exact: true }).click();

  // Back on the Check step, on the card of that server, which is open and focused.
  const card = dlg.locator('.fix');
  await expect(card).toContainText('1 IIS files come from WEB-A, which is not on your computer list.', { timeout: 60_000 });
  await expect(dlg.locator('.prog-line')).toContainText('Step 2 of 3');
  const add = card.getByRole('button', { name: 'Add WEB-A as a computer and accept 1 files', exact: true });
  await expect(add).toBeFocused();
  await add.click();
  await expect(card).toContainText('Added WEB-A, 1 files accepted');

  await importAll(page);
  await expect(hero(page)).toContainText('1 files imported', { timeout: 60_000 });
  await expect.poll(() => caseImports(api, 'QA Import Retry')).toBe(1);
});

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
