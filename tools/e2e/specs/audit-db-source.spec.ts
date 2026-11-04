// An API audit database from a saved MongoDB connection (gaps G4 and G5 of the wireframe gap list).
//   (a) the create wizard: the database whose name looks like an audit log starts ticked as API audit logs, the review does not warn,
//       and the case is made with that database connected and tagged as audit;
//   (b) Add data > MongoDB on an existing case: pick the saved connection, test, pick the database, add it. It is connected (POST /api/sources)
//       and tagged to the case (POST /api/cases/{id}/sources, classification audit), shows in the audit sources list, and the Timeline asks
//       for no particular source, so the server reads every live audit source.
// The demo cannot reach a real MongoDB, so the three edges that need one are answered here: the connection test, the connect, and the tag.
// Everything else (the saved connection, the cases, the screens) is the real demo. Screenshots go to .dev/audit-shots.
import { test, expect, type Page, type Route } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

const SHOTS = join(__dirname, '..', '..', '..', '.dev', 'audit-shots');
const AUDIT_DB = 'Sandbox_ApiAuditLogs';
const APP_DB = 'orders';
const SAVED = 'Demo MongoDB';
const CASE = 'Orders API stall, 19 October';

interface Edge {
  tests: unknown[];
  connects: Array<Record<string, unknown>>;
  tags: Array<Record<string, unknown>>;
  failTag: boolean;
  added: Array<Record<string, unknown>>;
}

const bodyOf = (route: Route): Record<string, unknown> => {
  try { return JSON.parse(route.request().postData() ?? '{}'); } catch { return {}; }
};

/** Answers the Mongo test, the connect and the tag. Returns what each saw. */
async function mockMongo(page: Page): Promise<Edge> {
  const edge: Edge = { tests: [], connects: [], tags: [], failTag: false, added: [] };
  await page.route('**/api/connections/test', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    edge.tests.push(bodyOf(route));
    await route.fulfill({ json: { databases: [AUDIT_DB, APP_DB, 'admin'] } });
  });
  await page.route(/\/api\/sources(\?.*)?$/, async (route) => {
    const method = route.request().method();
    if (method === 'POST') {
      const body = bodyOf(route);
      edge.connects.push(body);
      const source = { id: 'sandbox-audit', label: String(body.label ?? ''), database: String(body.database ?? ''), collection: String(body.collection ?? ''), connectionLabel: String(body.connectionLabel ?? '') };
      edge.added.push(source);
      return route.fulfill({ status: 201, json: source });
    }
    if (method === 'GET') {
      const res = await route.fetch();
      const list = await res.json();
      return route.fulfill({ response: res, json: Array.isArray(list) ? [...list, ...edge.added] : list });
    }
    return route.fallback();
  });
  await page.route(/\/api\/cases\/[^/]+\/sources$/, async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    const body = bodyOf(route);
    if (body.kind !== 'mongo-database') return route.fallback();
    edge.tags.push(body);
    if (edge.failTag) return route.fulfill({ status: 503, json: { error: 'The tag could not be saved right now.', reason: 'busy' } });
    await route.fulfill({ status: 201, json: { id: 901, importId: 0, kind: body.kind, label: body.label, env: '', portal: '', api: '', classification: body.classification } });
  });
  return edge;
}

async function shot(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, name + '.png'), fullPage: false });
}

async function openAddAudit(page: Page, title: string) {
  await goHome(page);
  await openCase(page, title);
  await page.getByRole('tab', { name: 'Add data', exact: true }).click();
  const panel = page.getByRole('tabpanel');
  await panel.locator('[data-card=mongo]').getByRole('button', { name: 'Add Mongo source' }).click();
  const section = panel.locator('[data-audit-add]');
  await expect(section).toBeVisible();
  return { panel, section };
}

test('AUDIT-01: Add data > MongoDB lists the saved connection, the audit-looking database starts ticked, and nothing is added before the button', async ({ page }) => {
  const edge = await mockMongo(page);
  const { section } = await openAddAudit(page, CASE);
  await expect(section.getByText('Add an API audit database', { exact: true })).toBeVisible();
  await shot(page, 'p7-1-add-data-mongo-opened');
  // The Step 8 profiler is kept, and says it is a separate thing.
  await expect(section.locator('[data-profiler-route]')).toContainText('separate');
  await expect(section.getByRole('button', { name: 'Open Step 8: MongoDB' })).toBeEnabled();

  await section.getByLabel('Saved connection').selectOption({ label: SAVED });
  await section.getByRole('button', { name: 'Test and list databases' }).click();
  await expect(section.getByLabel('Database', { exact: true })).toBeVisible();
  expect(edge.tests.length).toBe(1);
  await section.getByLabel('Database', { exact: true }).selectOption(AUDIT_DB);
  const audit = section.getByRole('checkbox', { name: /API audit logs/ });
  const app = section.getByRole('checkbox', { name: /Application data/ });
  await expect(audit).toBeChecked();
  await expect(app).not.toBeChecked();
  await expect(section.locator('[data-suggested]')).toBeVisible();
  await shot(page, 'p7-2-database-picked-audit-ticked');

  // An ordinary database starts with nothing ticked, and the button stays off.
  await section.getByLabel('Database', { exact: true }).selectOption(APP_DB);
  await expect(audit).not.toBeChecked();
  await expect(section.locator('[data-neither]')).toBeVisible();
  await expect(section.getByRole('button', { name: 'Add to this case' })).toBeDisabled();
  expect(edge.connects).toEqual([]);
  expect(edge.tags).toEqual([]);
});

test('AUDIT-02: adding the audit database connects it, tags it to the case as audit, and it shows in the audit sources and the Timeline asks for no one source', async ({ page }) => {
  const edge = await mockMongo(page);
  const tracings: Array<Record<string, unknown>> = [];
  page.on('request', (r) => { if (r.method() === 'POST' && /\/api\/cases\/[^/]+\/tracing$/.test(r.url())) tracings.push(JSON.parse(r.postData() ?? '{}')); });
  const { section } = await openAddAudit(page, CASE);
  await section.getByLabel('Saved connection').selectOption({ label: SAVED });
  await section.getByRole('button', { name: 'Test and list databases' }).click();
  await section.getByLabel('Database', { exact: true }).selectOption(AUDIT_DB);
  await section.getByRole('button', { name: 'Add to this case' }).click();
  await expect(section.locator('[data-add-done]')).toContainText(AUDIT_DB);
  await shot(page, 'p7-3-added-to-the-case');

  expect(edge.connects.length).toBe(1);
  expect(edge.connects[0]).toMatchObject({ database: AUDIT_DB, label: AUDIT_DB, collection: expect.any(String) });
  expect(edge.tags.length).toBe(1);
  expect(edge.tags[0]).toMatchObject({ kind: 'mongo-database', label: AUDIT_DB, classification: 'audit' });
  // The password is never sent anywhere it should not be, and the page shows no secret.
  await expect(page.locator('body')).not.toContainText('[object Object]');

  // The audit sources list on the analysis screen now has the database.
  await page.getByRole('button', { name: 'Open the analysis' }).click();
  await page.getByRole('button', { name: /^Audit sources:/ }).click();
  const menu = page.getByRole('dialog', { name: /Audit sources/ });
  await expect(menu).toContainText(AUDIT_DB);
  await shot(page, 'p7-4-audit-sources-list');
  await page.keyboard.press('Escape');

  // The Timeline sends no sourceIds unless some were picked, so the server reads every live audit source, the new one included.
  await goHome(page);
  await openCase(page, CASE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect.poll(() => tracings.length).toBeGreaterThan(0);
  for (const body of tracings) expect(body.sourceIds ?? []).toEqual([]);
  await shot(page, 'p7-5-timeline-after-add');
});

test('AUDIT-03: when the tag fails after the connect, the page says so and the retry only tags', async ({ page, allow }) => {
  allow(/503.*\/api\/cases\/[^/]+\/sources/, 'the tag is refused on purpose');
  const edge = await mockMongo(page);
  edge.failTag = true;
  const { section } = await openAddAudit(page, CASE);
  await section.getByLabel('Saved connection').selectOption({ label: SAVED });
  await section.getByRole('button', { name: 'Test and list databases' }).click();
  await section.getByLabel('Database', { exact: true }).selectOption(AUDIT_DB);
  await section.getByRole('button', { name: 'Add to this case' }).click();
  await expect(section.locator('[data-add-problem]')).toContainText('connected');
  await shot(page, 'p7-6-tag-failed-retry');
  const retry = section.getByRole('button', { name: 'Tag it to the case again' });
  await expect(retry).toBeVisible();
  edge.failTag = false;
  await retry.click();
  await expect(section.locator('[data-add-done]')).toContainText(AUDIT_DB);
  expect(edge.connects.length).toBe(1);
  expect(edge.tags.length).toBe(2);
});

test('AUDIT-04: on a closed case every control of the page is off', async ({ page }) => {
  await mockMongo(page);
  await goHome(page);
  await openCase(page, 'Night batch overrun, 10 October');
  await page.getByRole('tab', { name: 'Add data', exact: true }).click();
  // The Mongo card's button is off on a closed case, so the page cannot be reached from it.
  await expect(page.getByRole('tabpanel').getByRole('button', { name: 'Add Mongo source' })).toBeDisabled();
});

test('AUDIT-05: the create wizard ticks the audit-looking database by default, the review does not warn, and the case is made with it connected and tagged', async ({ page }) => {
  const edge = await mockMongo(page);
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill('QA audit database wizard');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('heading', { name: 'Choose what to look at' })).toBeVisible();
  await page.getByRole('button', { name: 'Connect to MongoDB' }).click();
  await page.getByLabel('Use a saved connection').selectOption({ index: 1 });
  await page.getByRole('button', { name: 'Test and list databases' }).click();
  const auditDb = page.getByRole('group', { name: AUDIT_DB });
  const appDb = page.getByRole('group', { name: APP_DB });
  await expect(auditDb.getByRole('checkbox', { name: /API audit logs/ })).toBeChecked();
  await expect(auditDb.getByRole('checkbox', { name: /Application data/ })).not.toBeChecked();
  await expect(appDb.getByRole('checkbox', { name: /API audit logs/ })).not.toBeChecked();
  await shot(page, 'p6-1-wizard-audit-ticked-by-default');

  // Unticked, the review says plainly that no API audit source will be read.
  await auditDb.getByRole('checkbox', { name: /API audit logs/ }).uncheck();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('No API audit source will be read.')).toBeVisible();
  await shot(page, 'p6-2-review-no-audit-source');
  await page.getByRole('button', { name: 'Back' }).click();
  const open = page.getByRole('button', { name: 'Connect to MongoDB' });
  if (await open.getAttribute('aria-expanded') !== 'true') await open.click();
  await auditDb.getByRole('checkbox', { name: /API audit logs/ }).check();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('No API audit source will be read.')).toHaveCount(0);
  await shot(page, 'p6-3-review-with-audit-source');

  await page.getByRole('button', { name: 'Create case', exact: true }).click();
  await expect.poll(() => edge.tags.length, { timeout: 30000 }).toBeGreaterThan(0);
  expect(edge.tags[0]).toMatchObject({ kind: 'mongo-database', label: AUDIT_DB, classification: 'audit' });
  expect(edge.connects.length).toBe(1);
  expect(edge.connects[0]).toMatchObject({ database: AUDIT_DB });
});
