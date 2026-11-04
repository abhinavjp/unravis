// Packet P11 (the Sources list with tags and time-zone state, and the time-zone notice on the Investigation Timeline) and the audit database gaps
// on Add data > MongoDB (the readiness chip, the collection-name check, the notes on the database list, the permission link) with the fixes C3,
// C4, C7, C8, C11 and P7 from the hub review.
//   SRCLIST-01  The Sources tab lists each source with Environment / Portal / API tags and a lane sticker, and a time-zone state per source.
//   SRCLIST-02  The environment filter shows one environment, pressing it again clears it, and the counts line names each state.
//   SRCLIST-03  Details open and close, say why a state is what it is, and "Add files again" opens Import files. Nothing here can change a source.
//   SRCLIST-04  A lane the server left out for its time zone shows the notice on the timeline, and "See the sources" opens the Sources tab.
//   SRCLIST-05  Screenshots for the review: light, dark and 375 px wide.
//   AUDITGAP-01 The readiness chip says Cannot test yet, Ready, Partly ready and Failed at the right moments, and no "Attach to a server" is offered.
//   AUDITGAP-02 The collection name is checked for its shape (empty, wildcard, bad character, system.), and the Add button follows.
//   AUDITGAP-03 The server's warning is shown without the address, and a list cut at 200 names says so.
//   AUDITGAP-04 The saved connection cannot be changed during a test, and a late answer for another connection is ignored (C3).
//   AUDITGAP-05 A second test with the password field empty sends the password again (C7); a failed retest leaves Add off and does not break it (C4).
//   AUDITGAP-06 After an Application-data-only add the ticks and the collection are locked (C8); a connected database cannot be swapped (P7).
//   AUDITGAP-07 Check permission opens Step 8: MongoDB.
//   AUDITGAP-08 Screenshots for the review: light, dark and 375 px wide.
// The demo cannot reach a real MongoDB or hold these files, so the edges are answered with page.route: the sources, imports and computers lists of
// the Sources tab, the trace answer of the timeline, and the Mongo test, connect and tag. The page and the requests it sends are the code under test.
import { test, expect, type Locator, type Page, type Route } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

const SHOTS = join(__dirname, '..', '..', '..', '.dev', 'sources-shots');
const CASE = 'Orders API stall, 19 October';
const SAVED = 'Demo MongoDB';
const AUDIT_DB = 'Sandbox_ApiAuditLogs';
const APP_DB = 'orders';

const bodyOf = (route: Route): Record<string, any> => {
  try { return JSON.parse(route.request().postData() ?? '{}'); } catch { return {}; }
};

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
  await expect(page.getByRole('heading', { name: 'Import files' }).first()).toBeVisible();
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

interface Edge {
  tests: Array<Record<string, any>>;
  connects: Array<Record<string, any>>;
  tags: Array<Record<string, any>>;
  databases: string[];
  warning: string;
  failTest: boolean;
  delayTest: Promise<void> | null;
}

/** Answers the Mongo test, the connect and the tag. Returns what each saw, and switches for what the next test answers. */
async function mockMongo(page: Page): Promise<Edge> {
  const edge: Edge = { tests: [], connects: [], tags: [], databases: [AUDIT_DB, APP_DB, 'admin'], warning: '', failTest: false, delayTest: null };
  const added: Array<Record<string, unknown>> = [];
  await page.route('**/api/connections/test', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    edge.tests.push(bodyOf(route));
    if (edge.delayTest) await edge.delayTest;
    if (edge.failTest) return route.fulfill({ status: 502, json: { error: 'The server did not answer.', reason: 'unreachable' } });
    await route.fulfill({ json: { databases: edge.databases, ...(edge.warning ? { warning: edge.warning } : {}) } });
  });
  await page.route(/\/api\/sources(\?.*)?$/, async (route) => {
    const method = route.request().method();
    if (method === 'POST') {
      const body = bodyOf(route);
      edge.connects.push(body);
      const source = { id: 'gap-' + edge.connects.length, label: String(body.label ?? ''), database: String(body.database ?? ''), collection: String(body.collection ?? ''), connectionLabel: String(body.connectionLabel ?? '') };
      added.push(source);
      return route.fulfill({ status: 201, json: source });
    }
    if (method === 'GET') {
      const res = await route.fetch();
      const list = await res.json();
      return route.fulfill({ response: res, json: Array.isArray(list) ? [...list, ...added] : list });
    }
    return route.fallback();
  });
  await page.route(/\/api\/cases\/[^/]+\/sources$/, async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    const body = bodyOf(route);
    if (body.kind !== 'mongo-database') return route.fallback();
    edge.tags.push(body);
    await route.fulfill({ status: 201, json: { id: 900 + edge.tags.length, importId: 0, kind: body.kind, label: body.label, env: '', portal: '', api: '', classification: body.classification } });
  });
  return edge;
}

async function openAddAudit(page: Page) {
  await goHome(page);
  await openCase(page, CASE);
  await page.getByRole('tab', { name: 'Add data', exact: true }).click();
  const panel = page.getByRole('tabpanel');
  await panel.locator('[data-card=mongo]').getByRole('button', { name: 'Add Mongo source' }).click();
  const section = panel.locator('[data-audit-add]');
  await expect(section).toBeVisible();
  return section;
}

const chip = (section: Locator) => section.getByTestId('audit-ready-chip');
const testButton = (section: Locator) => section.getByRole('button', { name: /^(Test and list databases|Testing)/ });
const saved = (section: Locator) => section.getByLabel('Saved connection');
const database = (section: Locator) => section.getByLabel('Database', { exact: true });
const collection = (section: Locator) => section.getByLabel('Audit collection');
const addButton = (section: Locator) => section.getByRole('button', { name: 'Add to this case' });

async function connectAndTest(section: Locator): Promise<void> {
  await saved(section).selectOption({ label: SAVED });
  await testButton(section).click();
  await expect(database(section)).toBeVisible();
}

test('AUDITGAP-01: the readiness chip follows the flow in the four readiness words, and nothing offers Attach to a server', async ({ page, allow }) => {
  allow(/502.*\/api\/connections\/test/, 'the test is refused on purpose');
  const edge = await mockMongo(page);
  const section = await openAddAudit(page);
  // No connection chosen yet: the chip is not shown.
  await expect(chip(section)).toHaveCount(0);
  await saved(section).selectOption({ label: SAVED });
  await expect(chip(section)).toHaveText('Cannot test yet');
  await expect(section.getByTestId('audit-ready')).toHaveAttribute('data-ready', 'cannot');
  await testButton(section).click();
  await expect(database(section)).toBeVisible();
  await expect(chip(section)).toHaveText('Cannot test yet');
  await expect(section.getByTestId('audit-ready-hint')).toContainText('Choose a database');
  await database(section).selectOption(AUDIT_DB);
  await expect(chip(section)).toHaveText('Ready');
  await expect(section.getByTestId('audit-ready')).toHaveAttribute('data-ready', 'ready');
  await shot(page, 'auditgap-1-ready');
  // An application database is only noted on the case: partly ready.
  await database(section).selectOption(APP_DB);
  await section.getByRole('checkbox', { name: /Application data/ }).check();
  await expect(chip(section)).toHaveText('Partly ready');
  await section.getByRole('checkbox', { name: /Application data/ }).uncheck();
  await expect(chip(section)).toHaveText('Cannot test yet');
  // A failed test says Failed.
  edge.failTest = true;
  await testButton(section).click();
  await expect(chip(section)).toHaveText('Failed');
  await expect(section.getByTestId('audit-ready')).toHaveAttribute('data-ready', 'failed');
  await expect(section.getByText('Attach to a server')).toHaveCount(0);
  await expect(page.locator('body')).not.toContainText('[object Object]');
});

test('AUDITGAP-02: the collection name is checked for its shape, and Add follows', async ({ page }) => {
  await mockMongo(page);
  const section = await openAddAudit(page);
  await connectAndTest(section);
  await database(section).selectOption(AUDIT_DB);
  const note = section.locator('[data-collection-check]');
  await expect(note).toHaveAttribute('data-collection-check', 'ok');
  await expect(addButton(section)).toBeEnabled();
  const cases: Array<[string, string]> = [
    ['ApiAudit*', 'pattern'], ['Api?Log', 'pattern'], ['Api Log', 'bad-char'], ['a$b', 'bad-char'], ['System.profile', 'system'], ['', 'empty'], ['a'.repeat(121), 'too-long']
  ];
  for (const [value, problem] of cases) {
    // The input stops at 120 characters, so the longest case is set on the element directly.
    if (problem === 'too-long') await collection(section).evaluate((el, v) => { (el as HTMLInputElement).maxLength = 500; (el as HTMLInputElement).value = v; el.dispatchEvent(new Event('input', { bubbles: true })); }, value);
    else await collection(section).fill(value);
    await expect(note, JSON.stringify(value.slice(0, 12))).toHaveAttribute('data-collection-check', problem);
    await expect(addButton(section)).toBeDisabled();
    await expect(chip(section)).toHaveText('Cannot test yet');
    await expect(collection(section)).toHaveAttribute('aria-invalid', 'true');
  }
  await shot(page, 'auditgap-2-collection-problem');
  await collection(section).fill('ApiAuditLog');
  await expect(note).toHaveAttribute('data-collection-check', 'ok');
  await expect(addButton(section)).toBeEnabled();
  await expect(chip(section)).toHaveText('Ready');
});

test('AUDITGAP-03: the server warning is shown without secrets, and a list cut at 200 names says so', async ({ page }) => {
  const edge = await mockMongo(page);
  edge.warning = 'The login can list databases but not read admin.';
  edge.databases = [AUDIT_DB, ...Array.from({ length: 249 }, (_, i) => 'db_' + String(i).padStart(3, '0'))];
  const section = await openAddAudit(page);
  await connectAndTest(section);
  await expect(section.locator('[data-test-warning]')).toContainText('The login can list databases but not read admin.');
  await expect(section.locator('[data-list-cut]')).toContainText('Only the first 200 databases are listed');
  expect(await database(section).locator('option').count()).toBe(201);
  await shot(page, 'auditgap-3-warning-and-cut');
  // A retest with a short list and no warning clears both notes.
  edge.warning = '';
  edge.databases = [AUDIT_DB];
  await testButton(section).click();
  await expect(section.locator('[data-test-warning]')).toHaveCount(0);
  await expect(section.locator('[data-list-cut]')).toHaveCount(0);
});

test('AUDITGAP-04: the saved connection is locked during a test (C3)', async ({ page }) => {
  const edge = await mockMongo(page);
  let release: () => void = () => {};
  edge.delayTest = new Promise<void>((resolve) => { release = resolve; });
  const section = await openAddAudit(page);
  await saved(section).selectOption({ label: SAVED });
  await testButton(section).click();
  await expect(section.getByRole('button', { name: /^Testing/ })).toBeDisabled();
  await expect(saved(section)).toBeDisabled();
  await expect(chip(section)).toHaveText('Cannot test yet');
  await expect(section.getByTestId('audit-ready-hint')).toContainText('Testing');
  await shot(page, 'auditgap-4-testing');
  release();
  await expect(database(section)).toBeVisible();
  await expect(saved(section)).toBeEnabled();
  expect(edge.tests.length).toBe(1);
});

test('AUDITGAP-05: a retest with an empty password field sends the password again (C7), and a failed retest keeps Add off and working (C4)', async ({ page, allow }) => {
  allow(/502.*\/api\/connections\/test/, 'the retest is refused on purpose');
  const edge = await mockMongo(page);
  const section = await openAddAudit(page);
  await saved(section).selectOption({ label: SAVED });
  await section.locator('input[type=password]').fill('hunter2-for-the-test');
  await testButton(section).click();
  await expect(database(section)).toBeVisible();
  await expect(section.locator('input[type=password]')).toHaveValue('');
  await testButton(section).click();
  await expect.poll(() => edge.tests.length).toBe(2);
  expect(edge.tests[0].password).toBe('hunter2-for-the-test');
  expect(edge.tests[1].password).toBe('hunter2-for-the-test');
  await expect(database(section)).toBeVisible();

  // A failed retest forgets the list and the connection: Add is gone, the page does not throw, and a good test brings it back.
  await database(section).selectOption(AUDIT_DB);
  await expect(addButton(section)).toBeEnabled();
  edge.failTest = true;
  await testButton(section).click();
  await expect(section.locator('.say.bad').first()).toBeVisible();
  await expect(database(section)).toHaveCount(0);
  await expect(addButton(section)).toHaveCount(0);
  await expect(chip(section)).toHaveText('Failed');
  edge.failTest = false;
  await testButton(section).click();
  await expect(database(section)).toBeVisible();
  await database(section).selectOption(AUDIT_DB);
  await addButton(section).click();
  await expect(section.locator('[data-add-done]')).toContainText(AUDIT_DB);
  expect(edge.connects.length).toBe(1);
  expect(edge.tags.length).toBe(1);
});

test('AUDITGAP-06: a connected audit database cannot be swapped (P7); after an Application-data add the ticks and the collection are locked (C8)', async ({ page }) => {
  const edge = await mockMongo(page);
  const section = await openAddAudit(page);
  await connectAndTest(section);
  await database(section).selectOption(AUDIT_DB);
  await addButton(section).click();
  await expect(section.locator('[data-add-done]')).toBeVisible();
  // P7: the connected database is fixed, so no second source can be connected from this screen.
  await expect(database(section)).toBeDisabled();
  await expect(chip(section)).toHaveText('Ready');
  expect(edge.connects.length).toBe(1);
  expect(edge.tags[0]).toMatchObject({ kind: 'mongo-database', label: AUDIT_DB, classification: 'audit' });
  await shot(page, 'auditgap-6-audit-added');

  // C8: an Application-data-only add connects nothing, and its ticks and collection no longer do anything, so they are off.
  await section.getByRole('button', { name: 'Add another database' }).click();
  await expect(database(section)).toBeEnabled();
  await database(section).selectOption(APP_DB);
  await section.getByRole('checkbox', { name: /Application data/ }).check();
  await addButton(section).click();
  await expect(section.locator('[data-add-done]')).toBeVisible();
  expect(edge.tags[1]).toMatchObject({ kind: 'mongo-database', label: APP_DB, classification: 'other' });
  await expect(section.getByRole('checkbox', { name: /Application data/ })).toBeDisabled();
  await expect(section.getByRole('checkbox', { name: /API audit logs/ })).toBeDisabled();
  await expect(collection(section)).toBeDisabled();
  await expect(chip(section)).toHaveText('Partly ready');
  await shot(page, 'auditgap-6-locked-after-app-add');
  expect(edge.connects.length).toBe(1);
});

test('AUDITGAP-07: Check permission opens Step 8: MongoDB', async ({ page }) => {
  await mockMongo(page);
  const section = await openAddAudit(page);
  const link = section.getByTestId('audit-check-permission');
  await expect(link).toHaveText('Check permission');
  await link.click();
  await expect(page.getByRole('tab', { name: 'Step 8: MongoDB', exact: true })).toHaveAttribute('aria-selected', 'true');
});

// ---------------------------------------------------------------- screenshots

for (const [name, scheme, width] of [['light', 'light', 1280], ['dark', 'dark', 1280], ['375', 'light', 375]] as const) {
  test(`SRCLIST-05${name === 'light' ? 'a' : name === 'dark' ? 'b' : 'c'}: Sources tab and Add audit database, ${name}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await mockSourceLists(page);
    const edge = await mockMongo(page);
    edge.warning = 'The login can list databases but not read admin.';
    await goHome(page);
    if (width < 800) await page.setViewportSize({ width, height: 812 });
    await openCase(page, CASE);
    await page.getByRole('tab', { name: 'Sources', exact: true }).click();
    await expect(page.locator('[data-sources-list]').getByTestId('srclist-rows')).toBeVisible();
    await rowOf(page.locator('[data-sources-list]'), 'f13').getByRole('button', { name: /^Details/ }).click();
    await shot(page, `sources-list-${name}`);
    await page.getByRole('tab', { name: 'Add data', exact: true }).click();
    const panel = page.getByRole('tabpanel');
    await panel.locator('[data-card=mongo]').getByRole('button', { name: 'Add Mongo source' }).click();
    const section = panel.locator('[data-audit-add]');
    await connectAndTest(section);
    await database(section).selectOption(AUDIT_DB);
    await section.scrollIntoViewIfNeeded();
    await shot(page, `audit-gap-${name}`);
    await collection(section).fill('Api*');
    await section.getByTestId('audit-ready').scrollIntoViewIfNeeded();
    await shot(page, `audit-gap-problem-${name}`);
  });
}
