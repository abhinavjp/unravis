// After an import, "Open timeline" in the Import files popup closes it and shows the case's Investigation Timeline with the imported source in place.
//   IP-15  Two IIS logs imported through the popup; Open timeline shows the Investigation Timeline, the IIS lane is not "no file for this source",
//          and the server's own answer for the day agrees.
import { test, expect, type Page } from '../support/fixtures';
import type { Locator } from '@playwright/test';
import { goHome, openCase } from '../support/ui';
import { createCase, ensureHost, type ApiFn } from '../support/api';

const FIELDS =
  '#Fields: date time s-sitename s-computername s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) cs(Cookie) cs(Referer) sc-status sc-substatus sc-win32-status time-taken';

/** A small IIS log written by `server`, dated on the demo clock's day. `tag` changes the rows, so no two files are the same file. */
function w3c(server: string, tag: string): string {
  const rows = [1, 2, 3].map((i) => `2026-10-20 08:00:0${i} W3SVC1 ${server} 192.0.2.10 GET /api/v1/${tag}/${i} - 443 - 192.0.2.77 Mozilla/5.0 - - 200 0 0 ${100 + i}`);
  return ['#Software: Microsoft Internet Information Services 10.0', '#Version: 1.0', '#Date: 2026-10-20 08:00:00', FIELDS, ...rows, ''].join('\n');
}
const iis = (name: string, server: string) => ({ name, mimeType: 'text/plain', buffer: Buffer.from(w3c(server, name)) });

// The words the server and the page use for a selected source with no file.
const NO_FILE_WARN = 'A selected source has no file yet, so it shows nothing.';
const NO_FILE_TEXT = 'This case has no file for this source';
const INCIDENT = { start: '2026-10-20T00:00:00Z', end: '2026-10-21T00:00:00Z' };

type Lane = { key: string; state: string };
type Trace = { lanes: Lane[]; warnings: string[] };

const isTrace = (r: { url(): string; request(): { method(): string } }) => r.url().includes('/tracing') && r.request().method() === 'POST';

/** Makes a case and opens the popup over its page. */
async function openPopup(page: Page, api: ApiFn, title: string): Promise<{ id: string; dialog: Locator }> {
  const id = await createCase(api, title);
  await goHome(page);
  await openCase(page, title);
  await page.getByRole('button', { name: 'Add a file' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Import files' });
  await expect(dialog).toBeVisible();
  return { id, dialog };
}

test.beforeEach(async ({ api }) => {
  await ensureHost(api);
});

test('IP-15: after an import, Open timeline shows the case Investigation Timeline with the imported source lines present', async ({ page, api }) => {
  test.setTimeout(120_000);
  const { id, dialog } = await openPopup(page, api, 'QA Popup Timeline');
  await dialog.getByLabel('Choose files to import').setInputFiles([iis('u_ex261151.log', 'web-a'), iis('u_ex261152.log', 'web-a')]);
  await dialog.getByRole('button', { name: 'Continue' }).click();
  const ack = dialog.getByRole('checkbox', { name: 'Confirm read-only' });
  await expect(ack).toBeVisible({ timeout: 30_000 });
  await ack.check();
  await dialog.getByRole('button', { name: 'Import 2 files', exact: true }).click();
  await expect(dialog.locator('.result-hero')).toContainText('2 files imported', { timeout: 90_000 });

  // The case holds both files.
  await expect.poll(async () => (await api('GET', `/api/cases/${id}`)).json.imports, { timeout: 30_000 }).toBe(2);
  const recorded = JSON.stringify((await api('GET', `/api/cases/${id}/imports`)).json);
  expect(recorded).toContain('u_ex261151.log');
  expect(recorded).toContain('u_ex261152.log');

  const answered = page.waitForResponse(isTrace);
  await dialog.getByRole('button', { name: 'Open timeline' }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();

  // What the page asked for and was told: the IIS source has a file, so its lane is not the "no file" lane.
  const trace = (await (await answered).json()) as Trace;
  expect(trace.warnings).not.toContain(NO_FILE_WARN);
  expect(trace.lanes.find((lane) => lane.key === 'iis')?.state).not.toBe('no-data');
  const lane = page.locator('.panel[data-lane=iis]');
  await expect(lane.locator('.panel-title')).toBeVisible();
  await expect(lane).not.toContainText(NO_FILE_TEXT);
  await expect(lane.locator('[data-testid=lane-add-files]')).toHaveCount(0);

  // The server's own answer for the day the logs cover says the same.
  const day = await api('POST', `/api/cases/${id}/tracing`, { incident: INCIDENT });
  expect(day.status).toBe(200);
  expect((day.json as Trace).warnings).not.toContain(NO_FILE_WARN);
  expect((day.json as Trace).lanes.find((l) => l.key === 'iis')?.state).not.toBe('no-data');
});
