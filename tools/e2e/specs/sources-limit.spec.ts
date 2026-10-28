// The 20 connection limit needs a demo of its own, because it fills the saved list.
import { test, expect, type Page } from '../support/fixtures';

async function savedConnections(page: Page) {
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case Checkout slowdown, 17 October' }).click();
  const block = page.getByRole('region', { name: 'Saved connections' });
  await expect(block).toBeVisible();
  return block;
}

test('SRC-09: at 20 saved SQL connections the button is off, a 21st is refused with 409, and forgetting one makes room', async ({ api, page }) => {
  const save = (i: number) =>
    api('POST', '/api/sql-connections', { label: 's' + i, server: `s${i}.example.invalid`, auth: 'sql', username: 'u', password: 'p', database: 'd', encrypt: 'strict' });
  for (let i = 1; i <= 19; i++) expect((await save(i)).status, 'save ' + i).toBe(200);
  const full = await save(20);
  expect(full.status).toBe(409);
  expect((await api('GET', '/api/sql-connections')).json).toHaveLength(20);
  // Saving the same server, user and database again replaces it and does not count twice.
  expect((await save(1)).status).toBe(200);
  expect((await api('GET', '/api/sql-connections')).json).toHaveLength(20);
  const block = await savedConnections(page);
  await expect(block.getByRole('button', { name: '+ SQL Server', exact: true })).toBeDisabled();
  await expect(block).toContainText('You have 20 of 20 saved SQL Server connections. Forget one before you save another.');
  await expect(block).toContainText('MongoDB connections listed (no limit): 1.');
  expect((await api('DELETE', '/api/sql-connections/demo-sql-1')).status).toBe(204);
  expect((await save(20)).status).toBe(200);
});
