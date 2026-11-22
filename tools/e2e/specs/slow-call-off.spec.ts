// Query Store OFF on the Timeline and the Slow call screen, driven through the demo's own SQL lane read (BL-016). The demo databases steer the
// answer: OffKeptDb is off but kept blocks (the newest starts 15 minutes before the incident ends), OffNoneDb is off and kept nothing. The lane
// read goes through the real lane code, the case store and the screens; only the SQL Server is a fake. Demo only: a real OFF database is P-40.
//   SCALL-07  OffNoneDb: no off note is added to the lane, and the Slow call screen still opens. (First: the demo is shared by the file, and the
//             next test saves a newer day.)
//   SCALL-08  OffKeptDb: the server's SQL lane for the window is layered over the kept blocks and says Query Store was off; the Slow call screen
//             still opens and never says the database "saved no history".
// The demo case already holds imported SQL events, so its SQL lane is layered ("layered-sql") in the window it was seeded for (19 October 09:00 to
// 10:00 UTC); that window is used for the server read. The timeline page's own default window (20 October 08:00 to 09:00) has no held day in this
// demo case, so the `offkept` Slow call state itself is not reachable from this case in the demo: it is covered by the web unit tests.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';

const CASE_TITLE = 'Orders API stall, 19 October';
const WIN: Win = { start: '2026-10-19T09:00:00.000Z', end: '2026-10-19T10:00:00.000Z' };
const CASE_ID = 'demotracing'.padEnd(26, 'a');

interface Win { start: string; end: string }

/** Opens the timeline and returns the window it read (the body of its tracing request). */
async function openTimeline(page: Page): Promise<Win> {
  let win: Win | null = null;
  page.on('request', (request) => {
    if (win || request.method() !== 'POST' || !/^\/api\/cases\/[^/]+\/tracing$/.test(new URL(request.url()).pathname)) return;
    const body = JSON.parse(request.postData() ?? '{}');
    const w = body.incident ?? body.window ?? body;
    if (typeof w.start === 'string' && typeof w.end === 'string') win = { start: w.start, end: w.end };
  });
  await goHome(page);
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect.poll(() => win, 'the timeline read its window').not.toBeNull();
  return win as unknown as Win;
}

/** Saves one SQL lane day for the database, read over `win` with the same-length window right before it as the comparison. */
async function readLane(api: (m: string, p: string, b?: unknown) => Promise<{ status: number; text: string }>, database: string, win: Win): Promise<void> {
  const start = Date.parse(win.start);
  const end = Date.parse(win.end);
  const span = end - start;
  const iso = (ms: number) => new Date(ms).toISOString();
  const res = await api('POST', `/api/cases/${CASE_ID}/lanes/sql`, {
    server: 'anything.example', auth: 'sql', username: 'someone', password: 'not-a-real-password', database,
    incident: { start: win.start, end: win.end }, reference: { start: iso(start - span), end: iso(start) }
  });
  expect(res.status, res.text).toBe(200);
}

async function openSlowCall(page: Page): Promise<void> {
  const diamond = page.locator('.panel[data-lane=audit] .mark.flagged').first();
  await expect(diamond).toBeVisible();
  await diamond.scrollIntoViewIfNeeded();
  const box = await diamond.boundingBox();
  if (!box) throw new Error('the diamond has no box');
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  await expect(page.getByTestId('slow-call-screen')).toBeVisible();
  await expect(page.getByTestId('scall-lanes')).toBeVisible();
}

/** The SQL lane the server gives the timeline for the window (the same read the page makes). */
async function sqlLane(api: (m: string, p: string, b?: unknown) => Promise<{ status: number; json: any; text: string }>, win: Win): Promise<any> {
  const res = await api('POST', `/api/cases/${CASE_ID}/tracing`, { incident: win });
  expect(res.status, res.text).toBe(200);
  const lane = (res.json.lanes as any[]).find((l) => l.key === 'sql');
  expect(lane, 'the answer has a SQL lane').toBeTruthy();
  return lane;
}

test('SCALL-07: an off database that kept nothing adds no off note', async ({ page, api }) => {
  await openTimeline(page);
  await readLane(api, 'OffNoneDb', WIN);
  const lane = await sqlLane(api, WIN);
  expect(lane.reason ?? '').not.toContain('Query Store was off when this day was read');
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await openSlowCall(page);
  await expect(page.getByTestId('scall-db-state')).not.toContainText('history it kept');
  await expect(page.getByTestId('scall-lane-sql')).toBeVisible();
});

test('SCALL-08: an off database that kept blocks is layered and says Query Store was off', async ({ page, api }) => {
  await openTimeline(page);
  await readLane(api, 'OffKeptDb', WIN);
  const lane = await sqlLane(api, WIN);
  expect(lane.state).toBe('events');
  expect(lane.reasonCode).toBe('layered-sql');
  expect(lane.reason).toContain('Query Store was off when this day was read');
  expect(lane.reason).toContain('its newest block starts at 2026-10-19T09:45:00Z');
  expect(lane.reason).not.toContain('saved no history');
  expect(lane.scanned).toBeGreaterThan(0);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await openSlowCall(page);
  await expect(page.getByTestId('scall-db-state')).not.toContainText('saved no history');
  await expect(page.getByTestId('scall-lane-sql')).toBeVisible();
});
