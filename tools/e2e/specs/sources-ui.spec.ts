// Source rows in the browser: the coverage drawer, testing and saving connections, forgetting one, the Connections drawer.
// The demo keeps its state across the tests of this file, so the order below matters: reads first, the forget test last.
import { test, expect, type Page } from '../support/fixtures';

const CASE = 'Checkout slowdown, 17 October';

async function openCaseHome(page: Page) {
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
}

async function savedConnections(page: Page) {
  await openCaseHome(page);
  const block = page.getByRole('region', { name: 'Saved connections' });
  await expect(block).toBeVisible();
  return block;
}

const rowsOf = (block: ReturnType<Page['locator']>) => block.getByRole('list', { name: 'Saved connections' }).getByRole('listitem');

async function openSqlForm(page: Page) {
  const block = await savedConnections(page);
  await block.getByRole('button', { name: '+ SQL Server', exact: true }).click();
  const server = page.locator('input[id$="-server"]');
  await expect(server).toBeVisible();
  return { block, server, user: page.locator('input[id$="-user"]'), pass: page.locator('input[id$="-pass"]'), auth: page.locator('select[id$="-auth"]') };
}

test('SRC-03: the coverage drawer opens with no request, Check coverage makes exactly one, and Escape returns focus', async ({ page }) => {
  const calls: string[] = [];
  page.on('request', (r) => { if (/\/api\/sources\/[^/]+\/coverage/.test(r.url())) calls.push(r.method() + ' ' + new URL(r.url()).pathname); });
  await page.getByRole('button', { name: /^Audit sources:/ }).click();
  const menu = page.getByRole('dialog', { name: /Audit sources/ });
  await menu.getByRole('button', { name: 'Coverage census: Demo West' }).click();
  const drawer = page.getByRole('dialog', { name: 'Coverage census' });
  await expect(drawer).toBeVisible();
  expect(calls).toEqual([]);
  const check = drawer.getByRole('button', { name: /Check coverage/ });
  await expect(check).toBeVisible();
  await check.click();
  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0]).toBe('GET /api/sources/demo-west/coverage');
  await expect(drawer.getByRole('table')).toBeVisible();
  await expect(page.locator('body')).not.toContainText('[object Object]');
  await page.keyboard.press('Escape');
  await expect(drawer).toHaveCount(0);
  await expect(page.getByRole('button', { name: /^Audit sources:/ })).toBeFocused();
  expect(calls.length).toBe(1);
});

test('SRC-05: Test on the SQL row says Works with the demo server line, a double click makes one request, and the Mongo row says testing is not available in the demo', async ({ page }) => {
  const block = await savedConnections(page);
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST' && /\/api\/sql-connections\/test/.test(r.url())) posts.push(r.url()); });
  const sql = rowsOf(block).filter({ hasText: 'Demo SQL Server' });
  await sql.getByRole('button', { name: 'Test: Demo SQL Server' }).dblclick();
  await expect(sql.getByText('Works', { exact: true })).toBeVisible();
  await expect(sql).toContainText('Connected. SQL Server 16.0.1000.6 Developer (demo).');
  expect(posts.length).toBe(1);
  await expect(sql.getByText('Not tested')).toHaveCount(0);
  const mongo = rowsOf(block).filter({ hasText: 'Demo MongoDB' });
  await mongo.getByRole('button', { name: 'Test: Demo MongoDB' }).click();
  await expect(block.getByText('Testing is not available here. Real connections are tested on your own computer.')).toBeVisible();
  await expect(mongo).toContainText('Not tested');
  expect(posts.length).toBe(1);
  expect(await page.locator('body').innerText()).not.toMatch(/password\s*[:=]\s*\S+/i);
});

test('SRC-06: a saved SQL connection shows password saved, Edit keeps the password hidden, and saving with the box empty keeps it', async ({ page, api }) => {
  const SECRET = 'ubk-test-NOT-A-REAL-PASSWORD-91';
  const { block, server, user, pass } = await openSqlForm(page);
  await page.locator('input[id$="-name"]').fill('QA Reports SQL');
  await server.fill('qa-sql.example.invalid');
  await user.fill('qa_reader');
  await pass.fill(SECRET);
  await page.getByLabel('Remember password').check();
  const save = page.getByRole('button', { name: 'Save connection' });
  await save.dblclick();
  const row = rowsOf(block).filter({ hasText: 'qa-sql.example.invalid' });
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('· password saved');
  await expect(row).not.toContainText('no password saved');
  // Never in the page, never in the API answer.
  expect(await page.content()).not.toContain(SECRET);
  const list = await api('GET', '/api/sql-connections');
  expect(JSON.stringify(list.json)).not.toContain(SECRET);
  expect(rowsOf(block).filter({ hasText: 'qa-sql.example.invalid' })).toHaveCount(1);

  // Edit: the box is empty and the note says the password is kept.
  await row.getByRole('button', { name: /^Edit/ }).click();
  await expect(page.locator('input[id$="-pass"]')).toHaveValue('');
  await expect(page.getByText('A password is saved. Leave the password empty to keep it.')).toBeVisible();
  expect(await page.content()).not.toContain(SECRET);
  await page.locator('input[id$="-name"]').fill('QA Reports SQL renamed');
  await page.getByRole('button', { name: 'Save connection' }).click();
  const edited = rowsOf(block).filter({ hasText: 'QA Reports SQL renamed' });
  await expect(edited).toHaveCount(1);
  await expect(edited).toContainText('qa-sql.example.invalid');
  await expect(edited).toContainText('· password saved');
  await expect(edited).not.toContainText('no password saved');
  // Same server, login and database: the rename replaced the row, it did not add one.
  await expect(rowsOf(block).filter({ hasText: 'qa-sql.example.invalid' })).toHaveCount(1);
  expect(await page.content()).not.toContain(SECRET);
});

test('SRC-06a: pointing a saved connection at another server does not carry the saved password along', async ({ page }) => {
  const block = await savedConnections(page);
  const row = rowsOf(block).filter({ hasText: 'qa-sql.example.invalid' });
  await row.getByRole('button', { name: /^Edit/ }).click();
  await page.locator('input[id$="-server"]').fill('qa-sql2.example.invalid');
  await page.getByRole('button', { name: 'Save connection' }).click();
  const moved = rowsOf(block).filter({ hasText: 'qa-sql2.example.invalid' });
  await expect(moved).toHaveCount(1);
  // A password saved for one server is never sent to another: the new entry has none.
  await expect(moved).toContainText('no password saved');
  // The first entry is untouched and still holds its password.
  await expect(rowsOf(block).filter({ hasText: 'qa-sql.example.invalid:1433' })).toContainText('· password saved');
});

test('SRC-08: Forget asks first, Keep it changes nothing, Forget it removes the row and the counter drops', async ({ page }) => {
  const block = await savedConnections(page);
  const rows = rowsOf(block);
  const before = await rows.count();
  const counter = block.getByText(/SQL Server connections saved: \d+ \/ 20/);
  const countBefore = Number(((await counter.innerText()).match(/saved: (\d+)/) as RegExpMatchArray)[1]);
  const target = rows.filter({ hasText: 'qa-sql2.example.invalid' });
  await expect(target).toHaveCount(1);
  const name = (await target.getByRole('button', { name: /^Forget/ }).getAttribute('aria-label')) as string;
  await target.getByRole('button', { name: /^Forget/ }).click();
  await expect(block.getByText('Forget this connection? Its saved password goes too. Cases keep the data they already read. No server is touched.')).toBeVisible();
  await block.getByRole('button', { name: 'Keep it' }).click();
  await expect(rows).toHaveCount(before);
  await target.getByRole('button', { name: new RegExp('^' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$') }).click();
  await block.getByRole('button', { name: 'Forget it' }).click();
  await expect(rows).toHaveCount(before - 1);
  await expect(block.getByText('qa-sql2.example.invalid')).toHaveCount(0);
  await expect(block.getByText(new RegExp('SQL Server connections saved: ' + (countBefore - 1) + ' / 20'))).toBeVisible();
  // The other rows are untouched.
  await expect(rows.filter({ hasText: 'Demo MongoDB' })).toHaveCount(1);
});

test('SRC-10: the Connections drawer lists the saved Mongo connection without its address, and there is no way to create one in the demo', async ({ page }) => {
  const block = await savedConnections(page);
  await block.getByRole('button', { name: '+ MongoDB', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: /Connections/ });
  await expect(drawer).toBeVisible();
  // In the demo the drawer opens on the New connection tab and explains why nothing can be added.
  await expect(drawer.getByText('This is the demo')).toBeVisible();
  await expect(drawer.getByText('The demo uses sample data, so new connections cannot be added here.')).toBeVisible();
  await expect(drawer.getByRole('button', { name: /Save connection|Test connection/ })).toHaveCount(0);
  await drawer.getByRole('button', { name: 'Saved connections' }).click();
  await expect(drawer).toContainText('Demo MongoDB');
  await expect(drawer).toContainText('unravis_ro');
  // The address shows with no login in it.
  await expect(drawer).toContainText('mongodb://demo-mongo.example.invalid:27017');
  expect(await drawer.innerText()).not.toMatch(/mongodb(\+srv)?:\/\/[^\s]*@/i);
  await expect(drawer.getByRole('button', { name: 'Create a connection' })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: /Use settings|Forget/ })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(drawer).toHaveCount(0);
});

async function withPreview(page: Page, preview: string) {
  await page.evaluate((x) => { location.hash = '#keeper=' + x; }, preview);
}

test('SRC-11: with the keeper running the rows carry a sticker and Test still works, and with it stopped the sticker says it cannot be used', async ({ page }) => {
  await withPreview(page, 'saved');
  const block = await savedConnections(page);
  const sql = rowsOf(block).filter({ hasText: 'Demo SQL Server' });
  const sticker = sql.locator('[data-keeper-held]');
  await expect(sticker).toHaveText('Password held by the key keeper');
  // A sticker, not a control.
  await expect(sql.getByRole('button', { name: /Password held/ })).toHaveCount(0);
  await sql.getByRole('button', { name: 'Test: Demo SQL Server' }).click();
  await expect(sql).toContainText('Connected. SQL Server 16.0.1000.6 Developer (demo).');

  await withPreview(page, 'stopped');
  await expect(page.locator('[data-keeper=stopped] [data-keeper-head]')).toHaveText('The key keeper is not running');
  await expect(sticker).toHaveText('Password held by the key keeper, which cannot be used until it runs again');
});

test('SRC-11b: with the keeper stopped the sources menu unticks the held sources and they cannot be ticked', async ({ page }) => {
  await withPreview(page, 'stopped');
  await page.getByRole('button', { name: /^Audit sources:/ }).click();
  const menu = page.getByRole('dialog', { name: /Audit sources/ });
  await expect(menu.locator('[data-keeper-sources-stopped]')).toBeVisible();
  const boxes = menu.getByRole('checkbox');
  for (const box of await boxes.all()) {
    await expect(box).not.toBeChecked();
    await expect(box).toBeDisabled();
  }
  await expect(menu).toContainText(/0 selected/);
});

test('SRC-11a: changing the preview in the address while the page is open updates it without a reload', async ({ page }) => {
  await withPreview(page, 'saved');
  const block = await savedConnections(page);
  await expect(page.locator('[data-keeper-held]').first()).toHaveText('Password held by the key keeper');
  await withPreview(page, 'stopped');
  await expect(page.locator('[data-keeper=stopped]')).toBeVisible();
  await withPreview(page, 'nonsense');
  await expect(page.locator('[data-keeper=stopped]')).toBeVisible();
  await expect(block).toBeVisible();
});
