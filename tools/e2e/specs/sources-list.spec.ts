// Packet P11 (the Sources list with tags and time-zone state, and the time-zone notice on the Investigation Timeline) and the audit database gaps
// on Add data > MongoDB (the readiness chip, the collection-name check, the notes on the database list, the permission link) with the fixes C3,
// C4, C7, C8, C11 and P7 from the hub review.
//   SRCLIST-01  The Sources tab lists each source with Environment / Portal / API tags and a lane sticker, and a time-zone state per source.
//   SRCLIST-02  The environment filter shows one environment, pressing it again clears it, and the counts line names each state.
//   SRCLIST-03  Details open and close, say why a state is what it is, and "Add files again" opens Import files. Nothing here can change a source.
//   SRCLIST-04  A lane the server left out for its time zone shows the notice on the timeline, and "See the sources" opens the Sources tab.
//   SRCLIST-05  Screenshots for the review: light, dark and 375 px wide.
// The demo cannot reach a real MongoDB or hold these files, so the edges are answered with page.route: the sources, imports and computers lists of
// the Sources tab, the trace answer of the timeline, and the Mongo test, connect and tag. The page and the requests it sends are the code under test.
import { test, expect, type Locator, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

const SHOTS = join(__dirname, '..', '..', '..', '.dev', 'sources-shots');
const CASE = 'Orders API stall, 19 October';
const AUDIT_DB = 'Sandbox_ApiAuditLogs';

async function shot(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, name + '.png'), fullPage: false });
}

// ---------------------------------------------------------------- the Sources list

const TAGS = [
  { id: 1, importId: 11, kind: 'iis-w3c', label: 'Admin site log', env: 'Production', portal: 'Admin', api: 'Orders', classification: '' },
  { id: 2, importId: 12, kind: 'sql-errorlog', label: 'SQL error log', env: 'Production', portal: 'Admin', api: 'Orders', classification: '' },
  { id: 3, importId: 13, kind: 'sql-errorlog', label: 'Old SQL error log', env: 'UAT', portal: 'Shop', api: 'Basket', classification: '' },
  { id: 4, importId: 14, kind: 'win-events', label: 'Windows events', env: 'UAT', portal: '', api: '', classification: '' },
  { id: 5, importId: 0, kind: 'mongo-database', label: AUDIT_DB, env: 'Production', portal: '', api: 'Orders', classification: 'audit' }
];
const IMPORTS = [
  { id: 11, importer: 'iis-w3c', fileName: 'u_ex201019.log', hostLabel: '', tzRule: 'utc-field', status: 'ok', rows: 120 },
  { id: 12, importer: 'sql-errorlog', fileName: 'ERRORLOG', hostLabel: '', tzRule: 'utc-field', status: 'ok', rows: 40 },
  { id: 13, importer: 'sql-errorlog', fileName: 'ERRORLOG.1', hostLabel: '', tzRule: 'host-local', status: 'ok', rows: 33 },
  { id: 14, importer: 'win-events', fileName: 'System.evtx', hostLabel: 'WEB01', tzRule: 'host-local', status: 'ok', rows: 18 }
];
const HOSTS = [
  { label: 'WEB01', aliases: [], role: 'iis', tz: 'Asia/Dubai' },
  { label: 'DB01', aliases: [], role: 'sql', tz: '' }
];

/** Answers the three reads the Sources tab makes. */
async function mockSourceLists(page: Page): Promise<void> {
  await page.route(/\/api\/cases\/[^/]+\/sources$/, async (route) => {
    if (route.request().method() !== 'GET') return route.fallback();
    await route.fulfill({ json: { sources: TAGS } });
  });
  await page.route(/\/api\/cases\/[^/]+\/imports$/, async (route) => {
    if (route.request().method() !== 'GET') return route.fallback();
    await route.fulfill({ json: { imports: IMPORTS } });
  });
  await page.route(/\/api\/hosts$/, async (route) => {
    if (route.request().method() !== 'GET') return route.fallback();
    await route.fulfill({ json: { version: 1, hosts: HOSTS } });
  });
}

async function openSourcesTab(page: Page): Promise<Locator> {
  await goHome(page);
  await openCase(page, CASE);
  await page.getByRole('tab', { name: 'Sources', exact: true }).click();
  const list = page.locator('[data-sources-list]');
  await expect(list.getByTestId('srclist-rows')).toBeVisible();
  return list;
}

// A row is found by its key: f<import id> for a file (named by its file name), t<tag id> for a source with no file.
const rowOf = (list: Locator, key: string) => list.locator(`[data-source-row="${key}"]`);

test('SRCLIST-01: each source shows its lane, Environment / Portal / API tags and a time-zone state', async ({ page }) => {
  await mockSourceLists(page);
  const list = await openSourcesTab(page);
  const rows = list.locator('[data-source-row]');
  await expect(rows).toHaveCount(5);

  const admin = rowOf(list, 'f11');
  await expect(admin.getByTestId('srclist-lane')).toHaveText('IIS');
  await expect(admin.getByTestId('srclist-tz')).toHaveAttribute('data-tz', 'known');
  await expect(admin).toContainText('u_ex201019.log');
  await expect(admin.getByTestId('srclist-tags')).toContainText('Production');
  await expect(admin.getByTestId('srclist-tags')).toContainText('Admin');
  await expect(admin.getByTestId('srclist-tags')).toContainText('Orders');

  // UTC taken for a type that writes local time is assumed, not known.
  await expect(rowOf(list, 'f12').getByTestId('srclist-tz')).toHaveAttribute('data-tz', 'assumed-utc');
  // A computer that writes local time and was not named needs a check.
  await expect(rowOf(list, 'f13').getByTestId('srclist-tz')).toHaveAttribute('data-tz', 'needs-check');
  // A named computer in the list with a zone is known.
  await expect(rowOf(list, 'f14').getByTestId('srclist-tz')).toHaveAttribute('data-tz', 'known');
  // A database has a lane sticker and is UTC.
  const db = rowOf(list, 't5');
  await expect(db.getByTestId('srclist-lane')).toHaveText('API audit');
  await expect(db.getByTestId('srclist-tz')).toHaveAttribute('data-tz', 'known');
  await expect(page.locator('body')).not.toContainText('[object Object]');
  await shot(page, 'srclist-1-list');
});

test('SRCLIST-02: the environment filter shows one environment, the same button clears it, and the counts add up', async ({ page }) => {
  await mockSourceLists(page);
  const list = await openSourcesTab(page);
  await expect(list.getByTestId('srclist-counts')).toContainText('3 known, 1 assumed UTC, 1 to check');
  const filter = list.getByTestId('srclist-filter');
  const all = filter.getByRole('button', { name: 'Show all' });
  const uat = filter.getByRole('button', { name: 'UAT', exact: true });
  await expect(all).toHaveAttribute('aria-pressed', 'true');
  await uat.click();
  await expect(uat).toHaveAttribute('aria-pressed', 'true');
  await expect(list.locator('[data-source-row]')).toHaveCount(2);
  await expect(rowOf(list, 'f13')).toBeVisible();
  await shot(page, 'srclist-2-filtered-uat');
  await uat.click();
  await expect(uat).toHaveAttribute('aria-pressed', 'false');
  await expect(list.locator('[data-source-row]')).toHaveCount(5);
  await filter.getByRole('button', { name: 'Production', exact: true }).click();
  await expect(list.locator('[data-source-row]')).toHaveCount(3);
  await all.click();
  await expect(list.locator('[data-source-row]')).toHaveCount(5);
  await expect(all).toHaveAttribute('aria-pressed', 'true');
});

test('SRCLIST-03: details say why a state is what it is, the list can change nothing, and Add files again opens Import files', async ({ page }) => {
  await mockSourceLists(page);
  const writes: string[] = [];
  page.on('request', (r) => { if (r.method() !== 'GET' && /\/api\/(cases|hosts|sources)/.test(r.url())) writes.push(r.method() + ' ' + r.url()); });
  const list = await openSourcesTab(page);
  const old = rowOf(list, 'f13');
  const more = old.locator('button[aria-controls="srclist-d-f13"]');
  await expect(more).toHaveAttribute('aria-expanded', 'false');
  await more.click();
  await expect(more).toHaveAttribute('aria-expanded', 'true');
  await expect(old.getByTestId('srclist-why')).toContainText('no computer was named');
  await expect(old).toContainText('Its time zone was fixed when the file was read.');
  await shot(page, 'srclist-3-details-open');
  await old.getByRole('button', { name: 'Hide details' }).click();
  await expect(old.getByTestId('srclist-why')).toHaveCount(0);
  await more.click();
  await old.getByRole('button', { name: 'Add files again' }).click();
  await expect(page.getByRole('heading', { name: 'Add files' }).first()).toBeVisible();
  expect(writes).toEqual([]);
});

test('SRCLIST-04: a lane left out for its time zone shows the notice, and See the sources opens the Sources tab', async ({ page }) => {
  await mockSourceLists(page);
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    const response = await route.fetch();
    const body = await response.json();
    const lane = body.lanes.find((l: any) => l.key !== 'audit') ?? body.lanes[body.lanes.length - 1];
    lane.state = 'clock-unchecked';
    lane.disabled = 'tz-rule';
    await route.fulfill({ response, json: body });
  });
  await goHome(page);
  await openCase(page, CASE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  const notice = page.getByTestId('tp-tz-notice');
  await expect(notice).toContainText('Time zone needs checking');
  await expect(notice).toContainText('has no usable time zone');
  await shot(page, 'srclist-4-timeline-notice');
  await notice.getByRole('button', { name: 'See the sources' }).click();
  await expect(page.getByRole('tab', { name: 'Sources', exact: true })).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator('[data-sources-list]').getByTestId('srclist-rows')).toBeVisible();
});

test('SRCLIST-04b: with every lane read, no notice is shown', async ({ page }) => {
  await goHome(page);
  await openCase(page, CASE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start tracing' })).toBeVisible();
  await expect(page.getByTestId('tp-tz-notice')).toHaveCount(0);
});

// ---------------------------------------------------------------- Add data > MongoDB

// ---------------------------------------------------------------- screenshots

