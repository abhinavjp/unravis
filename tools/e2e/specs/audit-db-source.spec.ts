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
  /** Answers for the next POST /api/sources calls, in order. When empty the connect succeeds. */
  replies: Array<{ status: number; json: Record<string, unknown> }>;
}

const bodyOf = (route: Route): Record<string, unknown> => {
  try { return JSON.parse(route.request().postData() ?? '{}'); } catch { return {}; }
};

/** Answers the Mongo test, the connect and the tag. Returns what each saw. */
async function mockMongo(page: Page): Promise<Edge> {
  const edge: Edge = { tests: [], connects: [], tags: [], failTag: false, added: [], replies: [] };
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
      const reply = edge.replies.shift();
      if (reply) return route.fulfill(reply);
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






test('AUDIT-05: the create wizard suggests the audit-looking database without switching it on, the review does not warn, and the case is made with it connected and tagged', async ({ page }) => {
  const edge = await mockMongo(page);
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill('QA audit database wizard');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByRole('heading', { name: 'Choose what to look at' })).toBeVisible();
  await page.getByRole('button', { name: /^MongoDB/ }).click();
  await page.getByLabel('Use a saved connection').selectOption({ index: 1 });
  await page.getByRole('button', { name: 'Test and list databases' }).click();
  const auditDb = page.getByRole('group', { name: AUDIT_DB });
  const appDb = page.getByRole('group', { name: APP_DB });
  // The audit-looking name is suggested, not switched on for the person.
  await expect(auditDb.getByRole('switch', { name: /API audit logs/ })).not.toBeChecked();
  await expect(auditDb.getByText('The name looks like an API audit log. Switch it on if it is.')).toBeVisible();
  await expect(auditDb.getByRole('switch', { name: /Application data/ })).not.toBeChecked();
  await expect(appDb.getByRole('switch', { name: /API audit logs/ })).not.toBeChecked();
  await shot(page, 'p6-1-wizard-audit-suggested');

  // Not switched on, the review says plainly that no API audit source will be read.
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('No API audit source will be read.')).toBeVisible();
  await shot(page, 'p6-2-review-no-audit-source');
  await page.getByRole('button', { name: 'Back' }).click();
  const open = page.getByRole('button', { name: /^MongoDB/ });
  if (await open.count()) await open.click();
  if (!(await auditDb.count())) {
    await page.getByLabel('Use a saved connection').selectOption({ index: 1 });
    await page.getByRole('button', { name: 'Test and list databases' }).click();
  }
  await auditDb.getByRole('switch', { name: /API audit logs/ }).check();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('No API audit source will be read.')).toHaveCount(0);
  await shot(page, 'p6-3-review-with-audit-source');

  await page.getByRole('button', { name: 'Create case', exact: true }).click();
  await expect.poll(() => edge.tags.length, { timeout: 30000 }).toBeGreaterThan(0);
  expect(edge.tags[0]).toMatchObject({ kind: 'mongo-database', label: AUDIT_DB, classification: 'audit' });
  expect(edge.connects.length).toBe(1);
  expect(edge.connects[0]).toMatchObject({ database: AUDIT_DB });
});

/** The create wizard, up to and including the click on Create case, with the audit database ticked. */
async function createWizardCase(page: Page, name: string, collection = '') {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill(name);
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: /^MongoDB/ }).click();
  await page.getByLabel('Use a saved connection').selectOption({ index: 1 });
  if (collection !== '') await page.getByLabel('Audit log collection').fill(collection);
  await page.getByRole('button', { name: 'Test and list databases' }).click();
  await page.getByRole('group', { name: AUDIT_DB }).getByRole('switch', { name: /API audit logs/ }).check();
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Create case', exact: true }).click();
}

test('AUDIT-06: the wizard retries once with the only audit-looking collection when the guessed name is not in the database', async ({ page, allow }) => {
  allow(/400.*\/api\/sources/, 'the first connect is refused on purpose: wrong collection name');
  const edge = await mockMongo(page);
  edge.replies.push({ status: 400, json: { error: 'The database has no collection called ApiAuditLog.', code: 'collection_missing', stage: 'source', collections: ['SystemApiAuditLog'] } });
  await createWizardCase(page, 'QA audit collection fallback', 'ApiAuditLog');
  await expect.poll(() => edge.connects.length, { timeout: 30000 }).toBe(2);
  expect(edge.connects[0]).toMatchObject({ database: AUDIT_DB, collection: 'ApiAuditLog' });
  expect(edge.connects[1]).toMatchObject({ database: AUDIT_DB, collection: 'SystemApiAuditLog' });
  expect(edge.tags[0]).toMatchObject({ kind: 'mongo-database', classification: 'audit' });
  await expect(page.getByText('was not added to the analysis')).toHaveCount(0);
});

test('AUDIT-06b: a collection name the person saved or typed is never swapped for another; the failure names the audit-looking ones', async ({ page, allow }) => {
  allow(/400.*\/api\/sources/, 'the connect is refused on purpose: the saved collection name is not in the database');
  const edge = await mockMongo(page);
  edge.replies.push({ status: 400, json: { error: 'The database has no collection called Logs.', code: 'collection_missing', stage: 'source', collections: ['SystemApiAuditLog'] } });
  await createWizardCase(page, 'QA audit collection kept', 'Logs');
  await expect(page.getByText('was not added to the analysis')).toBeVisible({ timeout: 30000 });
  expect(edge.connects.length).toBe(1);
  expect(edge.connects[0]).toMatchObject({ database: AUDIT_DB, collection: 'Logs' });
});

test('AUDIT-07: when the account may not read the audit collection, the last screen says so instead of the generic message', async ({ page, allow }) => {
  allow(/400.*\/api\/sources/, 'the connect is refused on purpose: no access to the collection');
  const edge = await mockMongo(page);
  edge.replies.push({ status: 400, json: { error: 'The account is not allowed to read the collection Logs in the database.', code: 'permission_denied', stage: 'source', collections: ['AuditOne', 'AuditTwo'] } });
  await createWizardCase(page, 'QA audit collection denied');
  const problem = page.getByText('was not added to the analysis');
  await expect(problem).toBeVisible({ timeout: 30000 });
  await expect(page.getByText('not allowed to read the audit collection')).toBeVisible();
  await expect(page.getByText('The server did not accept the details')).toHaveCount(0);
  expect(edge.connects.length).toBe(1);
});
