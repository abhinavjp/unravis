// A case made through the wizard with an IIS log must show that log on the Investigation Timeline. The wizard saves the file's source tag
// before the import (import id 0, not yet linked to anything); the Web server (IIS) lane then said "This case has no file for this source"
// although the file had imported. The server pairs such a tag to its import by file name.
//   TLWIZ-01  A listed server, one IIS log through the wizard: the trace holds no "no file" warning, the IIS lane is not "no-data", and the page does not say it.
//   TLWIZ-02  The same log under another name, from a server not in the host list, with a computer picked: the same three facts hold.
import { test, expect, type Page } from '../support/fixtures';
import { goHome } from '../support/ui';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// The wording the server puts in `warnings` for a selected source with no file (tracingNoFileWarn), and the page's own sentence for an empty lane.
const NO_FILE_WARN = 'A selected source has no file yet, so it shows nothing.';
const NO_FILE_TEXT = 'This case has no file for this source';

const RAW = readFileSync(join(__dirname, '..', 'fixtures', 'files', 'u_ex261020.log'), 'utf8');
// The log is dated 2020-10-20 in its body (the demo clock is 2026-10-20 09:00 UTC); the file name does not change what the lines say.
const INCIDENT = { start: '2026-10-20T00:00:00Z', end: '2026-10-21T00:00:00Z' };

type Lane = { key: string; state: string };
type Trace = { lanes: Lane[]; warnings: string[]; sources: Array<{ sourceId: string; label: string }> };

const isTrace = (r: { url(): string; request(): { method(): string } }) => r.url().includes('/tracing') && r.request().method() === 'POST';

/** Runs the wizard with one file to the end: Create case, then waits for its result table. `pick` is the computer to choose when the file asks for one. */
async function makeCaseWith(page: Page, title: string, file: { name: string; buffer: Buffer }, pick?: string) {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill(title);
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('heading', { name: 'Choose what to look at' })).toBeVisible();
  await page.getByRole('button', { name: 'Add files', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Add files', level: 5 })).toBeVisible();
  await page.getByLabel('Choose files to import').setInputFiles([{ name: file.name, mimeType: 'text/plain', buffer: file.buffer }]);
  if (pick) await page.getByLabel(`Which computer wrote ${file.name}`).selectOption(pick);
  await page.getByRole('button', { name: 'Add 1 file' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  if (pick) await expect(page.getByLabel(`Which computer wrote ${file.name}`)).toHaveValue(pick);
  await page.getByRole('checkbox', { name: /I confirm this file/ }).check();
  await page.getByRole('button', { name: 'Create case' }).click();

  const table = page.getByRole('table', { name: /result for each file/i });
  await expect(table).toBeVisible();
  const line = table.getByRole('row').filter({ hasText: file.name });
  await expect(line).toContainText('Used');
  await expect(line).not.toContainText('Rejected');
}

/** The id of the case with this title, once the case holds one import. */
async function caseWithOneImport(api: (m: string, p: string) => Promise<{ json: any }>, title: string): Promise<string> {
  await expect.poll(async () => ((await api('GET', '/api/cases')).json.cases as Array<{ title: string }>).some((c) => c.title === title), { timeout: 15000 }).toBe(true);
  const made = (await api('GET', '/api/cases')).json.cases.find((c: { title: string }) => c.title === title);
  await expect.poll(async () => (await api('GET', `/api/cases/${made.id}`)).json.imports, { timeout: 30000 }).toBe(1);
  return made.id as string;
}

/** The server's own answer for the day the log covers: never the "no file" warning, and an IIS lane that is not "no-data". */
async function expectTraceHasTheFile(api: (m: string, p: string, b?: unknown) => Promise<{ status: number; json: any }>, id: string): Promise<void> {
  const res = await api('POST', `/api/cases/${id}/tracing`, { incident: INCIDENT });
  expect(res.status).toBe(200);
  const trace = res.json as Trace;
  expect(trace.warnings, 'the file imported, so its source has a file').not.toContain(NO_FILE_WARN);
  const iis = trace.lanes.find((lane) => lane.key === 'iis');
  expect(iis, 'the IIS lane is in the answer').toBeTruthy();
  // 'no-data' is the lane state for "this case has no file for this source". Which other state it is (events, or out-of-range or
  // clock-unchecked if the file's time rule or coverage differs) is not what this test is about.
  expect(iis!.state).not.toBe('no-data');
}

/** Opens the case on the Investigation Timeline and waits for the page's own trace; returns what that trace said. */
async function openTimelineOf(page: Page): Promise<Trace> {
  // The wizard's own result screen offers the timeline: the path a person takes right after Create case.
  const answered = page.waitForResponse(isTrace);
  await page.getByRole('button', { name: 'Open the timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  const trace = (await (await answered).json()) as Trace;
  await expect(page.locator('.panel[data-lane=iis] .panel-title')).toBeVisible();
  return trace;
}

/** What the page shows once the trace is in: the IIS lane does not say "no file", and the Sources traced row lists at least one IIS source. */
async function expectPageHasTheFile(page: Page, trace: Trace): Promise<void> {
  expect(trace.warnings).not.toContain(NO_FILE_WARN);
  expect(trace.lanes.find((lane) => lane.key === 'iis')?.state).not.toBe('no-data');
  await expect(page.locator('.panel[data-lane=iis]')).not.toContainText(NO_FILE_TEXT);
  await expect(page.locator('.panel[data-lane=iis] [data-testid=lane-add-files]')).toHaveCount(0);
  const chips = page.locator('[data-testid=trace-sources] .chip');
  await expect(chips.filter({ hasText: /^IIS/ })).not.toHaveCount(0);
}

test('TLWIZ-01: an IIS log added in the wizard for a listed server shows on the Timeline, not as "no file for this source"', async ({ page, api }) => {
  expect((await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-A', role: 'iis', tz: 'UTC' }] })).status).toBe(200);
  const title = 'QA Wizard File On Timeline';
  await makeCaseWith(page, title, { name: 'u_ex261020.log', buffer: Buffer.from(RAW) });

  const id = await caseWithOneImport(api, title);
  await expectTraceHasTheFile(api, id);
  await expectPageHasTheFile(page, await openTimelineOf(page));
});

test('TLWIZ-02: the same log from a server that is not in the host list, with a computer picked, shows on the Timeline too', async ({ page, api }) => {
  expect((await api('PUT', '/api/hosts', { version: 1, hosts: [{ label: 'WEB-B', role: 'iis', tz: 'UTC' }] })).status).toBe(200);
  const title = 'QA Wizard Unlisted File On Timeline';
  // Synthetic server name, not in the host list; the other name the log is known by in this suite (UNLISTED-01).
  const buffer = Buffer.from(RAW.replace(/\bWEB-A\b/g, 'ISS-PROD-07'));
  await makeCaseWith(page, title, { name: 'u_ex261021.log', buffer }, 'WEB-B');

  const id = await caseWithOneImport(api, title);
  await expectTraceHasTheFile(api, id);
  await expectPageHasTheFile(page, await openTimelineOf(page));
});
