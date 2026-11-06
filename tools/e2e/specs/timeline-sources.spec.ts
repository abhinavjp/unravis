// The Investigation Timeline reading what it did not read, and the "Sources traced" row (the chip row of the tracing-paper wireframe).
// Answers are changed with page.route where the demo cannot make the state (a refused or half-read audit lane, a saved database that is not
// connected, a case with no SQL or IIS file); the page and the request it sends are the code under test. Nothing here changes demo data.
//   TSRC-01  A refused API lane on an 11-day window: the banner says what was not read, and the page never also says "No events in this window".
//   TSRC-02  A chunked read that stopped: "Read 4 of 11 days, through <time>" on the API lane, the unread days drawn and labelled "not read".
//   TSRC-03  The row lists the live audit sources and the files; switching one off sends sourceIds, the lane names what it read, and a reload keeps it.
//   TSRC-04  A source can be switched off and on again: each click sends the matching request, the lane follows, "all on" sends no sourceIds.
//   TSRC-05  Everything switched off: nothing is read, the screen says so (not "all"), the choice survives a reload, Select all reads again.
//   TSRC-06  A saved audit database that is not connected is listed as "saved, not connected", on its lane too, and Connect opens the connect flow.
//   TSRC-07  A SQL or IIS lane with no file says to add a file, and Add files opens the Import files popup.
//   TSRC-08  Screenshots for the review: light, dark and 375 px wide, with the chunked read, the saved database and the empty SQL / IIS lanes.
import { test, expect, type Page, type Request } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { fillWindows } from '../support/window-picker';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

const CASE_TITLE = 'Orders API stall, 19 October';
// Eleven days each, so the read is chunked (the server reads a window longer than 24 hours in pieces of at most 24 hours).
const WIN_11 = ['2026-10-08T00:00', '2026-10-19T00:00', '2026-09-27T00:00', '2026-10-08T00:00'] as const;
const SHOTS = join(__dirname, '..', '..', '..', '.dev', 'timeline-shots');
const DAY_MS = 86_400_000;

type Body = Record<string, any>;
type Edit = (body: Body) => void;

/** Answers every trace as the server does, after `edit` has changed the answer. */
async function answerWith(page: Page, edit?: Edit): Promise<void> {
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    edit?.(body);
    await route.fulfill({ response, json: body });
  });
}

/** Opens the case timeline. `width` narrows the window after the start screen is reached (a phone has no "All cases" button). */
async function openTimeline(page: Page, width?: number): Promise<void> {
  await goHome(page);
  if (width) await page.setViewportSize({ width, height: 812 });
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
}

const isTrace = (r: Request) => r.url().includes('/tracing') && r.method() === 'POST';
const sentBody = (r: Request): Body => JSON.parse(r.postData() ?? '{}');

/** Counts the traces the page sends from now on. */
function countTraces(page: Page): { count: () => number; bodies: Body[] } {
  const bodies: Body[] = [];
  page.on('request', (r) => { if (isTrace(r)) bodies.push(sentBody(r)); });
  return { count: () => bodies.length, bodies };
}

const row = (page: Page) => page.locator('[data-testid=trace-sources]');
const chip = (page: Page, id: string) => row(page).locator(`.chip[data-source-id="${id}"]`);
const idsOf = (body: Body): string[] => [...((body.sourceIds as string[] | undefined) ?? [])].sort();
const DEMO_IDS = ['demo-east', 'demo-west', 'imp_1', 'imp_2', 'imp_3'];

/** Clicks a chip and returns the request that click made. */
async function clickAndRead(page: Page, click: () => Promise<void>): Promise<Body> {
  const sent = page.waitForRequest(isTrace);
  await click();
  return sentBody(await sent);
}

/** Types the 11-day windows, presses Start tracing and waits for that trace. */
async function traceEleven(page: Page): Promise<void> {
  await fillWindows(page, WIN_11);
  const answered = page.waitForResponse((r) => isTrace(r.request()) && (r.request().postData() ?? '').includes('2026-10-08T00:00:00.000Z'));
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await answered;
}

/** The audit lane read 4 of 11 days: later cells are 0 and not read, and the lane says how far it got. */
function stoppedAfterFourDays(body: Body): void {
  const audit = body.lanes.find((lane: Body) => lane.key === 'audit');
  const through = Date.parse(body.window.start) + 4 * DAY_MS;
  audit.chunksTotal = 11;
  audit.chunksRead = 4;
  audit.readThrough = new Date(through).toISOString();
  audit.reasonCode = 'timeout';
  audit.state = 'incomplete';
  audit.complete = false;
  for (const bucket of audit.buckets) {
    if (Date.parse(bucket.start) >= through) { bucket.count = 0; bucket.errors = 0; bucket.complete = false; }
  }
}

function refusedApiLane(body: Body): void {
  const audit = body.lanes.find((lane: Body) => lane.key === 'audit');
  audit.state = 'incomplete';
  audit.reasonCode = 'broad-scan';
  audit.complete = false;
  audit.buckets = [];
  audit.sourcesRead = [];
  for (const lane of body.lanes) if (lane.key !== 'audit') { lane.state = 'no-data'; lane.buckets = []; lane.sources = []; }
}

function noSqlOrIisFile(body: Body): void {
  for (const lane of body.lanes) if (lane.key !== 'audit') { lane.state = 'no-data'; lane.buckets = []; lane.sources = []; }
  body.sources = [];
}

test('TSRC-01: a refused API lane on an 11-day window says what was not read and never also "No events in this window"', async ({ page }) => {
  await answerWith(page, refusedApiLane);
  await openTimeline(page);
  await traceEleven(page);

  const banner = page.locator('[data-testid=lanes-cut-short]');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('Some lanes were not read in full.');
  await expect(banner).toContainText('API requests (audit log): not read.');
  // The two never appear together.
  await expect(page.getByText('No events in this window')).toHaveCount(0);
  await expect(page.getByText('No data in this window')).toHaveCount(0);
  // The lane says why, in words that do not tell a person to start the program differently, and that this is not "no traffic".
  const audit = page.locator('.panel[data-lane=audit]');
  await expect(audit).toContainText('would not read a window this long');
  await expect(audit).toContainText('This is not the same as no traffic');
  await expect(page.locator('.tracing-chart')).not.toContainText('allow-broad-scans');
  // The other lanes keep their own words (no file), and are not blamed.
  await expect(page.locator('.panel[data-lane=sql]')).toBeVisible();
  await expect(page.locator('.panel[data-lane=iis]')).toBeVisible();
});

test('TSRC-02: a chunked read that stopped says "Read 4 of 11 days", draws the unread days hatched and calls them "not read"', async ({ page }) => {
  await answerWith(page, stoppedAfterFourDays);
  await openTimeline(page);
  await traceEleven(page);

  const chunk = page.locator('[data-testid=chunk-line][data-lane=audit]');
  await expect(chunk).toBeVisible();
  await expect(chunk).toHaveText(/^Read 4 of 11 days, through .+/);
  const runs = page.locator('.panel[data-lane=audit] [data-testid=unread-run]');
  expect(await runs.count()).toBeGreaterThan(0);
  await expect(runs.first()).toContainText('not read');
  // The banner lists the same facts, and the unread part is never described as empty.
  const banner = page.locator('[data-testid=lanes-cut-short]');
  await expect(banner).toContainText('Read 4 of 11 days');
  await expect(banner).toContainText('marked not read');
  await expect(page.getByText('No events in this window')).toHaveCount(0);
  // The lanes that were read in full show no such line.
  await expect(page.locator('[data-testid=chunk-line]')).toHaveCount(1);
});

test('TSRC-03: the row lists the live audit sources and the files; switching one off sends sourceIds, the lane names what it read, a reload keeps it', async ({ page }) => {
  await openTimeline(page);
  await expect(row(page)).toBeVisible();
  await expect(row(page).getByRole('heading', { name: 'Sources traced' })).toBeVisible();
  expect(await row(page).locator('.chip').allInnerTexts()).toEqual(
    expect.arrayContaining(['API audit · Demo West', 'API audit · Demo East', 'SQL · sql-xe', 'IIS · iis-w3c', 'IIS · iis-httperr'])
  );
  for (const id of DEMO_IDS) await expect(chip(page, id)).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('[data-testid=sources-summary]')).toHaveText('Tracing all 5 sources.');
  // Each audit lane says which sources it read.
  await expect(page.locator('[data-testid=lane-sources-read][data-lane=audit]')).toHaveText('Read: Demo West, Demo East');

  // Demo East off: the request names every other source, and the lane lists only the one it read.
  const body = await clickAndRead(page, () => chip(page, 'demo-east').click());
  expect(idsOf(body)).toEqual(['demo-west', 'imp_1', 'imp_2', 'imp_3']);
  await expect(chip(page, 'demo-east')).toHaveAttribute('aria-pressed', 'false');
  await expect(page.locator('[data-testid=sources-summary]')).toHaveText('Tracing 4 of 5 sources.');
  await expect(page.locator('[data-testid=lane-sources-read][data-lane=audit]')).toHaveText('Read: Demo West');

  // A reload keeps the choice for this case in this tab, and the first request already carries it.
  const first = page.waitForRequest(isTrace);
  await page.reload();
  // The app comes back on the same screen of the same case.
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  expect(idsOf(sentBody(await first))).toEqual(['demo-west', 'imp_1', 'imp_2', 'imp_3']);
  await expect(chip(page, 'demo-east')).toHaveAttribute('aria-pressed', 'false');
  await expect(chip(page, 'demo-west')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('[data-testid=lane-sources-read][data-lane=audit]')).toHaveText('Read: Demo West');
});

test('TSRC-04: a source can be switched off and on again; each click sends the matching request and the lane follows', async ({ page }) => {
  await openTimeline(page);
  await expect(page.locator('.tracing-chart')).toBeVisible();

  // SQL off: the request leaves out the SQL file, the chip is off, and the SQL lane says no source is selected (not "no file").
  const off = await clickAndRead(page, () => chip(page, 'imp_1').click());
  expect(idsOf(off)).toEqual(['demo-east', 'demo-west', 'imp_2', 'imp_3']);
  await expect(chip(page, 'imp_1')).toHaveAttribute('aria-pressed', 'false');
  const sqlStatus = page.locator('.panel[data-lane=sql] [data-testid=lane-status]');
  await expect(sqlStatus).toHaveText('No source for this lane is selected.');
  await expect(page.locator('.panel[data-lane=sql] [data-testid=lane-add-files]')).toHaveCount(0);
  await expect(page.locator('.panel[data-lane=iis]')).not.toContainText('No source for this lane is selected.');
  const stored = await page.evaluate(() => Object.keys(sessionStorage).concat(Object.keys(localStorage)).filter((k) => k.startsWith('unravis.tracing.sources.')));
  expect(stored.length, 'the choice is kept for this case').toBe(1);

  // SQL on again: every source is on, so the request names none ("all"), the lane reads normally and the kept choice is dropped.
  const on = await clickAndRead(page, () => chip(page, 'imp_1').click());
  expect(on.sourceIds ?? []).toEqual([]);
  await expect(chip(page, 'imp_1')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('.panel[data-lane=sql]')).not.toContainText('No source for this lane is selected.');
  await expect(page.locator('[data-testid=sources-summary]')).toHaveText('Tracing all 5 sources.');
  const left = await page.evaluate(() => Object.keys(sessionStorage).concat(Object.keys(localStorage)).filter((k) => k.startsWith('unravis.tracing.sources.')));
  expect(left.length, 'all sources on keeps nothing').toBe(0);
});

// Select all and Clear all wait in a disclosure under the chips (the prototype's Sources row has only the chips and the buttons).
async function openMore(page: Page) {
  const more = page.getByTestId('sources-more');
  if (!(await more.evaluate((el) => (el as HTMLDetailsElement).open))) await more.locator('summary').click();
}

test('TSRC-05: with every source switched off nothing is read and the screen says so; a reload keeps it; Select all reads again', async ({ page }) => {
  await openTimeline(page);
  await expect(page.locator('.tracing-chart')).toBeVisible();
  const traces = countTraces(page);

  // One at a time, to the last one: the four before it still read, the last click reads nothing.
  for (const id of DEMO_IDS.slice(0, 4)) {
    const sent = await clickAndRead(page, () => chip(page, id).click());
    expect(sent.sourceIds.length).toBe(DEMO_IDS.length - DEMO_IDS.indexOf(id) - 1);
  }
  const before = traces.count();
  await chip(page, 'imp_3').click();
  for (const id of DEMO_IDS) await expect(chip(page, id)).toHaveAttribute('aria-pressed', 'false');
  const none = page.locator('[data-testid=tp-none-selected]');
  await expect(none).toHaveText('No sources are selected, so nothing is traced. Choose at least one source.');
  await expect(page.locator('[data-testid=sources-summary]')).toHaveText('Tracing no sources.');
  await expect(page.locator('.tracing-chart')).toHaveCount(0);
  await expect(page.getByText('No events in this window')).toHaveCount(0);
  await expect(page.locator('[data-testid=sources-none]')).toBeDisabled();
  // Nothing was sent for it: not an "all" request in disguise.
  await page.waitForTimeout(400);
  expect(traces.count()).toBe(before);

  // A reload keeps "none" and still sends nothing.
  const sentBeforeReload = traces.count();
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect(none).toBeVisible();
  for (const id of DEMO_IDS) await expect(chip(page, id)).toHaveAttribute('aria-pressed', 'false');
  await page.waitForTimeout(400);
  expect(traces.count()).toBe(sentBeforeReload);

  // Select all reads everything again with no sourceIds (the existing contract for "all").
  await openMore(page);
  const all = await clickAndRead(page, () => page.locator('[data-testid=sources-all]').click());
  expect(all.sourceIds ?? []).toEqual([]);
  await expect(none).toHaveCount(0);
  await expect(page.locator('.tracing-chart')).toBeVisible();
  for (const id of DEMO_IDS) await expect(chip(page, id)).toHaveAttribute('aria-pressed', 'true');

  // Clear all is one click: it also reads nothing.
  const afterAll = traces.count();
  await openMore(page);
  await page.locator('[data-testid=sources-none]').click();
  await expect(none).toBeVisible();
  await page.waitForTimeout(300);
  expect(traces.count()).toBe(afterAll);
});

test('TSRC-06: a saved audit database that is not connected is listed as "saved, not connected" and Connect opens the connect flow', async ({ page }) => {
  await answerWith(page, (body) => {
    body.savedNotConnected = ['orders_audit'];
    const audit = body.lanes.find((lane: Body) => lane.key === 'audit');
    audit.savedNotConnected = ['orders_audit'];
  });
  await openTimeline(page);
  const saved = page.locator('[data-testid=source-saved]');
  await expect(saved).toHaveCount(1);
  await expect(saved).toContainText('orders_audit');
  await expect(saved).toContainText('saved, not connected');
  // It is not a toggle: the live sources still are.
  await expect(row(page).locator('.chip')).toHaveCount(5);
  // The API lane names it too, so the lane never reads as if that database had been checked.
  await expect(page.locator('[data-testid=lane-saved][data-lane=audit]')).toHaveText('Saved, not connected: orders_audit');
  await saved.getByRole('button', { name: 'Connect' }).click();
  await expect(page.getByRole('dialog', { name: 'Connections' })).toBeVisible();
});

test('TSRC-07: a SQL or IIS lane with no file says to add a file, and Add files opens the Import files popup', async ({ page }) => {
  await answerWith(page, noSqlOrIisFile);
  await openTimeline(page);
  for (const lane of ['sql', 'iis']) {
    const status = page.locator(`.panel[data-lane=${lane}] [data-testid=lane-status]`);
    await expect(status).toContainText('This case has no file for this source.');
    await expect(status).toContainText('Add a file to see this lane');
    await expect(page.locator(`.panel[data-lane=${lane}] [data-testid=lane-add-files]`)).toHaveText('Add files');
  }
  // The API lane is a live lane and has no such button.
  await expect(page.locator('.panel[data-lane=audit] [data-testid=lane-add-files]')).toHaveCount(0);
  await page.locator('.panel[data-lane=sql] [data-testid=lane-add-files]').click();
  await expect(page.getByRole('dialog', { name: 'Import files' })).toBeVisible();
});

for (const [name, scheme, width] of [['light', 'light', 1280], ['dark', 'dark', 1280], ['375', 'light', 375]] as const) {
  test(`TSRC-08${name === 'light' ? 'a' : name === 'dark' ? 'b' : 'c'}: screenshot, ${name}${name === '375' ? ' px wide' : ''}: chunked read, saved database and empty SQL / IIS lanes, no sideways scroll`, async ({ page }) => {
    mkdirSync(SHOTS, { recursive: true });
    await page.emulateMedia({ colorScheme: scheme });
    await answerWith(page, (body) => {
      stoppedAfterFourDays(body);
      noSqlOrIisFile(body);
      body.savedNotConnected = ['orders_audit'];
      body.lanes.find((lane: Body) => lane.key === 'audit').savedNotConnected = ['orders_audit'];
    });
    await openTimeline(page, width);
    await traceEleven(page);
    await expect(page.locator('[data-testid=chunk-line]')).toBeVisible();
    await expect(page.locator('[data-testid=unread-run]').first()).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow, 'nothing scrolls sideways').toBeLessThanOrEqual(0);
    await page.screenshot({ path: join(SHOTS, `timeline-sources-${name}.png`), fullPage: true });
  });
}
