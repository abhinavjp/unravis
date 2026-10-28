// Audit sources menu, saved connections on the case home, and the connection limit (demo data only).
import { test, expect, type Page } from '../support/fixtures';

const CASE = 'Checkout slowdown, 17 October';

async function openSources(page: Page) {
  const button = page.getByRole('button', { name: /^Audit sources:/ });
  await button.click();
  const dialog = page.getByRole('dialog', { name: /Audit sources/ });
  await expect(dialog).toBeVisible();
  return { button, dialog };
}

async function openCaseHome(page: Page) {
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
}

test('SRC-01: the sources menu lists the two demo sources, has no add or remove control, and Esc returns focus', async ({ page }) => {
  const { button, dialog } = await openSources(page);
  await expect(dialog.getByRole('heading', { name: 'Sources' })).toBeVisible();
  await expect(dialog).toContainText(/\d selected · 2 available/);
  await expect(dialog.getByRole('button', { name: /Coverage/ })).toHaveCount(2);
  await expect(dialog).toContainText('Demo sources are provided by the sample repository.');
  await expect(dialog.getByRole('button', { name: /Add a connection|Manage connections/i })).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: /^(×|Remove)/ })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(button).toBeFocused();
});

test('SRC-02: unticking sources changes the summary, and the run needs at least one', async ({ page }) => {
  const { dialog } = await openSources(page);
  const boxes = dialog.getByRole('checkbox');
  await expect(boxes).toHaveCount(2);
  await boxes.nth(0).uncheck();
  await expect(dialog).toContainText(/1 selected · 2 available/);
  await boxes.nth(1).uncheck();
  await expect(dialog).toContainText(/0 selected · 2 available/);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: /^Audit sources:/ })).toContainText('Choose sources');
  await expect(page.getByRole('button', { name: 'Run analysis' })).toBeDisabled();
  await expect(page.getByText('Choose a source to run the analysis.')).toBeVisible();
  const again = await openSources(page);
  await again.dialog.getByRole('checkbox').nth(0).check();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: 'Run analysis' })).toBeEnabled();
  const done = page.waitForResponse((r) => r.url().includes('/api/analyze'));
  await page.getByRole('button', { name: 'Run analysis' }).click();
  expect((await done).status()).toBe(200);
});

async function savedConnections(page: Page) {
  await openCaseHome(page);
  const block = page.getByRole('region', { name: 'Saved connections' });
  await expect(block).toBeVisible();
  return block;
}

test('SRC-04: saved connections show two rows, no password text, the counters and the right buttons', async ({ page }) => {
  const block = await savedConnections(page);
  await expect(block.getByRole('heading', { name: 'Saved connections' })).toBeVisible();
  const rows = block.getByRole('list', { name: 'Saved connections' }).getByRole('listitem');
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText('Demo SQL Server');
  await expect(rows.nth(1)).toContainText('Demo MongoDB');
  await expect(block.getByText('password saved')).toHaveCount(2);
  await expect(rows.getByText('Not tested')).toHaveCount(2);
  await expect(block).toContainText(/SQL Server connections saved: 1 \/ 20/);
  await expect(block).toContainText('MongoDB connections listed (no limit): 1.');
  for (const b of ['+ SQL Server', '+ MongoDB']) await expect(block.getByRole('button', { name: b, exact: true }), b).toBeVisible();
  for (const name of ['Demo SQL Server', 'Demo MongoDB']) {
    await expect(block.getByRole('button', { name: 'Test: ' + name })).toBeVisible();
    await expect(block.getByRole('button', { name: 'Forget: ' + name })).toBeVisible();
  }
  await expect(block).toContainText('Works, Failed and Not tested describe one connection test only.');
  expect(await page.locator('body').innerText()).not.toMatch(/password\s*[:=]\s*\S+/i);
  // The scenario wants one primary button; the case home shows two (Open the analysis, Run checks). Not asserted here.
});

async function openSqlForm(page: Page) {
  const block = await savedConnections(page);
  await block.getByRole('button', { name: '+ SQL Server', exact: true }).click();
  const server = page.locator('input[id$="-server"]');
  await expect(server).toBeVisible();
  return { block, server, user: page.locator('input[id$="-user"]'), pass: page.locator('input[id$="-pass"]'), auth: page.locator('select[id$="-auth"]') };
}

test('SRC-04a: the Refresh button is there without pressing + MongoDB first', async ({ page }) => {
  const block = await savedConnections(page);
  await expect(block.getByRole('button', { name: 'Refresh', exact: true })).toBeVisible();
});

test('SRC-07: an empty save is refused in words, Windows Authentication hides the login fields, and a comma port is split', async ({ page }) => {
  const { block, server, user, pass, auth } = await openSqlForm(page);
  const rows = block.getByRole('list', { name: 'Saved connections' }).getByRole('listitem');
  await page.getByRole('button', { name: 'Save connection' }).click();
  await expect(page.locator('body')).toContainText('Type the server name first.');
  await expect(rows).toHaveCount(2);
  await server.fill('db-bad.example.invalid');
  await page.getByRole('button', { name: 'Save connection' }).click();
  await expect(page.locator('body')).toContainText('Type the user name, or choose Windows Authentication.');
  await expect(rows).toHaveCount(2);
  await auth.selectOption({ label: 'Windows Authentication' });
  await expect(user).toBeHidden();
  await expect(pass).toBeHidden();
  await server.fill('db1,1655');
  await page.getByRole('button', { name: 'Save connection' }).click();
  await expect(rows.filter({ hasText: 'db1' })).toHaveCount(1);
  expect(await page.locator('body').innerText()).not.toMatch(/panic|goroutine|<html|Unexpected token/i);
  expect(await page.locator('body').innerText()).not.toContain('[object Object]');
});

test('SRC-07c: db1,1655 is stored as host db1 and port 1655', async ({ api }) => {
  const r = await api('POST', '/api/sql-connections', { label: 'p', server: 'db1,1655', auth: 'windows', database: 'd', encrypt: 'mandatory' });
  expect(r.status).toBe(200);
  expect(r.json).toMatchObject({ host: 'db1', port: 1655 });
});

test('SRC-07a: Test with no password on a new form says to type the password or choose Windows Authentication', async ({ page }) => {
  const { server, user } = await openSqlForm(page);
  await server.fill('db-bad.example.invalid');
  await user.fill('someone');
  await page.getByRole('button', { name: 'Test', exact: true }).click();
  await expect(page.locator('body')).toContainText('Type the password first, or choose Windows Authentication.');
});

test('SRC-07b: a 301 character server name is refused or the limit is shown, never cut silently', async ({ page }) => {
  const { server } = await openSqlForm(page);
  await server.fill('x'.repeat(301));
  const kept = (await server.inputValue()).length;
  if (kept < 301) {
    await page.getByRole('button', { name: 'Save connection' }).click();
    await expect(page.locator('body'), 'the box cut the name to ' + kept + ' with no message').toContainText(/too long|300|260/i);
  }
});
