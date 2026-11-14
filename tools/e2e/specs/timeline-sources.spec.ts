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
//   TSRC-09  Files listed for a lane but never imported: the lane says they were not imported, and Add files still opens the Import files popup.
//   TSRC-11  A source that was never imported says so on its chip (flag and tooltip).
//   TSRC-12  A lane whose imported files sit outside the window says what they cover.
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

test('TSRC-09: files listed for a SQL or IIS lane but never imported are called "not imported", not "no file"', async ({ page }) => {
  const words = 'The files listed for this lane have not been imported into this case, so it holds none of their events. Import them to see this lane.';
  await answerWith(page, (body) => {
    noSqlOrIisFile(body);
    for (const lane of body.lanes) if (lane.key !== 'audit') { lane.reasonCode = 'not-imported'; lane.reason = words; }
  });
  await openTimeline(page);
  for (const lane of ['sql', 'iis']) {
    const status = page.locator(`.panel[data-lane=${lane}] [data-testid=lane-status]`);
    await expect(status).toContainText('have not been imported into this case');
    await expect(status).not.toContainText('This case has no file for this source');
    await expect(page.locator(`.panel[data-lane=${lane}] [data-testid=lane-add-files]`)).toBeVisible();
  }
  await page.locator('.panel[data-lane=iis] [data-testid=lane-add-files]').click();
  await expect(page.getByRole('dialog', { name: 'Import files' })).toBeVisible();
});

test('TSRC-11: a source that was never imported says so on its chip', async ({ page }) => {
  let sourceId = '';
  await answerWith(page, (body) => {
    body.sources[0].noFile = true;
    sourceId = body.sources[0].sourceId;
  });
  await openTimeline(page);
  await expect(row(page)).toBeVisible();
  await expect.poll(() => sourceId, { message: 'the answer carried a file source' }).not.toBe('');
  const flagged = chip(page, sourceId);
  await expect(flagged.getByTestId('chip-not-imported')).toHaveText(/not imported/);
  await expect(flagged).toHaveAttribute('title', /never imported/);
});

test('TSRC-12: a lane whose files sit outside the window says what they cover', async ({ page }) => {
  await answerWith(page, (body) => {
    const iis = body.lanes.find((lane: Body) => lane.key === 'iis');
    Object.assign(iis, { state: 'out-of-range', scanned: 0, buckets: [], events: [], importedFrom: '2001-01-01T00:00:00Z', importedTo: '2001-01-01T01:00:00Z' });
  });
  await openTimeline(page);
  const panel = page.locator('.panel[data-lane="iis"]');
  await expect(panel).toHaveAttribute('data-state', 'out-of-range');
  await expect(panel.getByTestId('lane-status')).toContainText('nothing falls inside this window');
  await expect(panel.getByTestId('lane-status')).toContainText('Imported files cover');
});

test('TSRC-13: a lane whose files cover only part of the window says what they cover', async ({ page }) => {
  await answerWith(page, (body) => {
    const iis = body.lanes.find((lane: Body) => lane.key === 'iis');
    Object.assign(iis, { state: 'out-of-range', scanned: 0, buckets: [], events: [], importedFrom: '2001-01-01T00:00:00Z', importedTo: '2001-01-02T00:00:00Z', importedPartial: true });
  });
  await openTimeline(page);
  const panel = page.locator('.panel[data-lane="iis"]');
  await expect(panel).toContainText('Some imported files cover');
  await expect(panel).toContainText('2001');
});

test('TSRC-14: a source that was never imported cannot be switched on, and the chip says why', async ({ page }) => {
  let sourceId = '';
  await answerWith(page, (body) => {
    body.sources[0].noFile = true;
    sourceId = body.sources[0].sourceId;
  });
  await openTimeline(page);
  await expect(row(page)).toBeVisible();
  await expect.poll(() => sourceId, { message: 'the answer carried a file source' }).not.toBe('');
  await expect(chip(page, sourceId)).toBeDisabled();
  await expect(page.getByTestId('chip-not-imported-reason')).toBeVisible();
});

// ---- TSQL: the Timeline reading the saved database for its window (P5). Every answer is mocked; the page and the requests it sends are the code under test.
//   TSQL-01  A case that remembers its database reads it once per new window, says so, and does not read the same window again.
//   TSQL-02  A failed read says why; Try again reads once more.
//   TSQL-03  The only saved connection of a case that never read it is offered with a button, never read unasked.
//   TSQL-04  Toggle off: nothing is read, it is offered instead, and the choice survives a reload.
//   TSQL-05  A kept day that crosses the window without being the same is named; nothing is read.
//   TSQL-06  In the demo the Timeline never reads a database and shows no database line.
//   TSRC-10  A SQL lane whose files were never imported says so, and Add files opens Import files.
type SqlMock = { posts: Body[]; days: Body[] };
const CONNECTION = { id: 'c1', label: 'Sandbox', server: 'sandbox-host', host: 'sandbox-host', port: 1433, auth: 'windows', username: '', database: 'Brain', encrypt: true, trustServerCertificate: true, hasPassword: false, savedAt: '2026-10-01T00:00:00Z' };
const keptDay = (incident: { start: string; end: string }): Body => ({ id: 1, at: '2026-10-23T10:00:00Z', kind: 'sql', label: 'Sandbox', database: 'Brain', state: 'Ready', incident, reference: incident, lane: null });
const EARLY = { start: '2026-01-01T00:00:00Z', end: '2026-01-02T00:00:00Z' };

/** Mocks info (demo or not), the saved connections, the case's lane days and the SQL read. `fail` answers the first reads with those statuses. */
async function mockSql(page: Page, opts: { connections?: Body[]; days?: Body[]; fail?: number[]; demo?: boolean } = {}): Promise<SqlMock> {
  const mock: SqlMock = { posts: [], days: [...(opts.days ?? [])] };
  const failures = [...(opts.fail ?? [])];
  await page.route('**/api/info', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.demo = opts.demo ?? false;
    await route.fulfill({ response, json: body });
  });
  await page.route('**/api/sql-connections', (route) => route.request().method() === 'GET' ? route.fulfill({ json: opts.connections ?? [CONNECTION] }) : route.continue());
  await page.route('**/api/cases/*/lanes', (route) => route.request().method() === 'GET' ? route.fulfill({ json: { days: mock.days } }) : route.continue());
  await page.route('**/api/cases/*/lanes/sql', async (route) => {
    const body = JSON.parse(route.request().postData() ?? '{}');
    mock.posts.push(body);
    const status = failures.shift();
    if (status) return route.fulfill({ status, json: { error: 'The server did not answer.' } });
    mock.days.push(keptDay(body.incident));
    await route.fulfill({ json: { saved: true, saveError: '', state: 'Ready', run: { id: mock.days.length, at: '2026-10-23T10:00:00Z', kind: 'sql', label: 'Sandbox', report: { database: 'Brain', lane: null } } } });
  });
  // The app read its info before these routes existed: read it again, so the page sees the answer above.
  await page.reload();
  await expect(page.getByRole('button', { name: /^Open case / }).first().or(page.getByRole('button', { name: /^All cases/ }))).toBeVisible();
  return mock;
}
const sqlLine = (page: Page) => page.locator('[data-testid=sql-auto]');

test('TSQL-01: a case that remembers its database reads it once for a new window, says so, and does not read the same window again', async ({ page, allow }) => {
  allow(/503 \(Service Unavailable\) \| \/api\/cases\/[^/]+\/workspace/, 'the page is told it is not the demo while the backend is, so the workspace save is refused');
  const mock = await mockSql(page, { days: [keptDay(EARLY)] });
  await openTimeline(page);
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'kept');
  expect(mock.posts, 'the first window was read once').toHaveLength(1);
  expect(mock.posts[0]).toMatchObject({ savedId: 'c1', database: 'Brain' });
  await traceEleven(page);
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'kept');
  await expect.poll(() => mock.posts.length, { message: 'a second window is a second read' }).toBe(2);
  expect(mock.posts[1].incident.start).toBe('2026-10-08T00:00:00.000Z');
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'kept');
  expect(mock.posts, 'the same window again is not read again').toHaveLength(2);
});

test('TSQL-02: a read that fails says why and Try again reads once more', async ({ page, allow }) => {
  allow(/503 \(Service Unavailable\) \| \/api\/cases\/[^/]+\/workspace/, 'the page is told it is not the demo while the backend is, so the workspace save is refused');
  allow(/429 .*\/lanes\/sql/, 'the mock refuses the first read on purpose');
  const mock = await mockSql(page, { days: [keptDay(EARLY)], fail: [429] });
  await openTimeline(page);
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'failed');
  await expect(sqlLine(page)).toContainText('The database could not be read');
  expect(mock.posts).toHaveLength(1);
  await page.locator('[data-testid=sql-auto-retry]').click();
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'kept');
  expect(mock.posts).toHaveLength(2);
});

test('TSQL-03: the only saved connection of a case that never read it is read on the first load, without a click', async ({ page, allow }) => {
  allow(/503 \(Service Unavailable\) \| \/api\/cases\/[^/]+\/workspace/, 'the page is told it is not the demo while the backend is, so the workspace save is refused');
  const mock = await mockSql(page);
  await openTimeline(page);
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'kept');
  expect(mock.posts, 'read once, automatically').toHaveLength(1);
  expect(mock.posts[0]).toMatchObject({ savedId: 'c1', database: 'Brain' });
});

test('TSQL-04: with the toggle off nothing is read, it is offered instead, and the choice survives a reload', async ({ page, allow }) => {
  allow(/503 \(Service Unavailable\) \| \/api\/cases\/[^/]+\/workspace/, 'the page is told it is not the demo while the backend is, so the workspace save is refused');
  const mock = await mockSql(page, { days: [keptDay(EARLY)] });
  await openTimeline(page);
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'kept');
  await page.locator('[data-testid=sql-auto-toggle]').uncheck();
  await traceEleven(page);
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'suggest');
  expect(mock.posts, 'only the first window was read').toHaveLength(1);
  await openTimeline(page);
  await expect(page.locator('[data-testid=sql-auto-toggle]')).not.toBeChecked();
});

test('TSQL-05: a kept day that crosses the window without being the same is named, and the database is not read', async ({ page, allow }) => {
  allow(/503 \(Service Unavailable\) \| \/api\/cases\/[^/]+\/workspace/, 'the page is told it is not the demo while the backend is, so the workspace save is refused');
  const mock = await mockSql(page, { days: [keptDay({ start: '2026-10-10T00:00:00Z', end: '2026-10-12T00:00:00Z' })] });
  await openTimeline(page);
  // The window the page opens on is not the kept day, so it is read once; wait for that before counting.
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'kept');
  const before = mock.posts.length;
  await traceEleven(page);
  await expect(sqlLine(page)).toHaveAttribute('data-kind', 'overlap');
  await expect(sqlLine(page)).toContainText('overlaps these times without being the same');
  expect(mock.posts.length, 'no read for the crossing window').toBe(before);
});

test('TSQL-06: in the demo the Timeline never reads a database and shows no database line', async ({ page }) => {
  const mock = await mockSql(page, { demo: true, days: [keptDay(EARLY)] });
  await openTimeline(page);
  await expect(page.locator('[data-testid=trace-sources]')).toBeVisible();
  await expect(sqlLine(page)).toHaveCount(0);
  expect(mock.posts).toHaveLength(0);
});

test('TSRC-10: a SQL lane with no saved database data says what the Timeline does and does not do', async ({ page }) => {
  const words = 'No database data is saved in this case for this window. The Timeline shows saved data only and never connects by itself. In the case\'s Sources, import the listed SQL file or use "Read this day" on the SQL connection.';
  await answerWith(page, (body) => {
    noSqlOrIisFile(body);
    const sql = body.lanes.find((lane: Body) => lane.key === 'sql');
    sql.reasonCode = 'not-imported'; sql.reason = words;
  });
  await openTimeline(page);
  const status = page.locator('.panel[data-lane=sql] [data-testid=lane-status]');
  await expect(status).toContainText('No database data is saved in this case for this window');
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
