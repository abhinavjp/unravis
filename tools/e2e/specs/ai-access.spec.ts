// AI access (BL-347): the screen where the person approves, narrows, revokes or stops the AI programs that read audit data.
// The demo opens a real grant store of its own (UNRAVIS_DEMO_MCP, see launcher.ts) and enrols five made-up hosts through the same calls a
// host and the screen make: claude-desktop and zed (waiting), cursor (waiting, another program also tried to register as it, its name carries
// tag characters), vscode (approved) and old-laptop (revoked). Every decision below goes through the real routes. The host side of the
// story (enrol, collect the credential, call, be refused after a revoke) is covered by the Go tests over real HTTP, because the demo has
// no ag pipe.
import { test, expect, type Page } from '../support/fixtures';

type Prof = { id: string; state: string; rev: number; contested: number; tools: string[]; sources: string[]; tier: string; identifiers: boolean };

async function openPanel(page: Page) {
  await page.goto('/');
  await page.getByRole('button', { name: 'AI access', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'AI access', level: 2 })).toBeVisible();
  await expect(page.getByTestId('mcp-panel')).toBeVisible();
}

const list = async (api: any) => (await api('GET', '/api/mcp/profiles')).json as { mode: string; stopped: boolean; profiles: Prof[] };
const get = async (api: any, id: string) => (await list(api)).profiles.find((p) => p.id === id)!;

test('AIA-01: the screen lists the hosts in their states and the stop button is at the top', async ({ page, api }) => {
  const l = await list(api);
  expect(l.mode).toBe('on');
  expect(l.profiles.map((p) => p.id).sort()).toEqual(['claude-desktop', 'cursor', 'old-laptop', 'vscode', 'zed']);
  await openPanel(page);
  await expect(page.getByRole('heading', { name: /Needs your decision/ })).toBeVisible();
  await expect(page.getByTestId('mcp-card')).toHaveCount(3);
  await expect(page.getByRole('heading', { name: /Approved programs/ })).toBeVisible();
  await expect(page.locator('[data-testid="mcp-row"][data-profile="vscode"] [data-testid="mcp-row-state"]')).toHaveText(/Active|not connected yet/);
  await expect(page.locator('[data-testid="mcp-row"][data-profile="old-laptop"] [data-testid="mcp-row-state"]')).toHaveText('Revoked');
  const stop = page.getByRole('button', { name: 'Stop all AI access', exact: true });
  await expect(stop).toBeVisible();
  await expect(page.getByRole('button', { name: /Stop all AI access/ })).toHaveCount(1);
  // nothing is allowed by default: the waiting hosts have no grant
  expect((await get(api, 'claude-desktop')).state).toBe('pending');
});

test('AIA-02: host text is shown as text, never as markup, and is labelled as reported by the program', async ({ page }) => {
  await openPanel(page);
  const card = page.locator('[data-testid="mcp-card"][data-profile="cursor"]');
  await expect(card.getByTestId('mcp-host-name')).toHaveText('Cursor <b>Pro</b>');
  expect(await card.locator('b').count()).toBe(0);
  await expect(card.getByText('Reported by the program itself. Unravis cannot check it.')).toBeVisible();
  await expect(card.getByTestId('mcp-host-exe')).toContainText('Cursor.exe');
});

test('AIA-03: approve narrows to what the person picked, sends the revision of the card, and the grant is what the server stores', async ({ page, api }) => {
  const before = await get(api, 'claude-desktop');
  await openPanel(page);
  const card = page.locator('[data-testid="mcp-card"][data-profile="claude-desktop"]');
  // narrow: no breakdown tool, one source only, user ids on for a day
  await card.getByRole('checkbox', { name: 'Break requests down by status, user or version' }).uncheck();
  const sources = card.getByRole('group', { name: 'Sources it may read' }).getByRole('checkbox');
  const total = await sources.count();
  expect(total).toBeGreaterThan(1);
  for (let i = 1; i < total; i++) await sources.nth(i).uncheck();
  await card.getByRole('checkbox', { name: 'Also allow user ids and addresses' }).check();
  await card.getByRole('combobox', { name: 'Allow them for' }).selectOption({ label: '1 day' });
  const posted: any[] = [];
  page.on('request', (r) => { if (r.method() === 'POST' && r.url().includes('/api/mcp/profiles/claude-desktop/approve')) posted.push(JSON.parse(r.postData() ?? '{}')); });
  await card.getByTestId('mcp-approve').click();
  await expect(page.locator('[data-testid="mcp-row"][data-profile="claude-desktop"]')).toBeVisible();
  await expect(page.getByTestId('mcp-card')).toHaveCount(1);
  expect(posted).toHaveLength(1);
  expect(posted[0].expectRev).toBe(before.rev);
  const after = await get(api, 'claude-desktop');
  expect(after.state).toBe('active');
  expect(after.tier).toBe('aggregate');
  expect(after.tools).not.toContain('audit_get_breakdown');
  expect(after.tools).toContain('audit_compare');
  expect(after.sources).toHaveLength(1);
  expect(after.identifiers).toBe(true);
  // an approval from the same stale card is refused: the revision moved
  const stale = await api('POST', '/api/mcp/profiles/claude-desktop/approve', { expectRev: before.rev, tier: 'aggregate', sources: after.sources });
  expect(stale.status).toBe(409);
});

test('AIA-04: a host that another program also tried to register as needs its own tick, in the page and on the server', async ({ page, api }) => {
  const before = await get(api, 'cursor');
  expect(before.contested).toBeGreaterThan(0);
  await openPanel(page);
  const card = page.locator('[data-testid="mcp-card"][data-profile="cursor"]');
  await expect(card.getByTestId('mcp-contested')).toContainText('Another program also tried to register as this host');
  await expect(card.getByTestId('mcp-approve')).toBeDisabled();
  await expect(card.getByTestId('mcp-why')).toContainText('Tick the box about the other program');
  // the server refuses it too, whatever the page does
  const direct = await api('POST', '/api/mcp/profiles/cursor/approve', { expectRev: before.rev, tier: 'aggregate', sources: ['demo-west'] });
  expect(direct.status).toBe(409);
  expect(direct.json.code).toBe('contested');
  await card.getByRole('checkbox', { name: /still want to approve this one/ }).check();
  await expect(card.getByTestId('mcp-approve')).toBeEnabled();
  // (approving a contested host with the tick is covered by the Go tests over the same route)
  await card.getByRole('checkbox', { name: /still want to approve this one/ }).uncheck();
  await expect(card.getByTestId('mcp-approve')).toBeDisabled();
  expect((await get(api, 'cursor')).state).toBe('pending');
});

test('AIA-05: Start over drops a contested entry back to nothing connected; the host must register again', async ({ page, api }) => {
  await openPanel(page);
  const card = page.locator('[data-testid="mcp-card"][data-profile="cursor"]');
  await card.getByTestId('mcp-startover').click();
  await expect(page.locator('[data-testid="mcp-card"][data-profile="cursor"]').getByText('No program has connected under this name yet.')).toBeVisible();
  const p = await get(api, 'cursor');
  expect(p.state).toBe('pending');
  expect(p.contested).toBe(0);
  await expect(page.locator('[data-testid="mcp-card"][data-profile="cursor"]').getByTestId('mcp-approve')).toHaveCount(0);
});

test('AIA-06: revoke ends an approved host, Allow again starts it over, Remove clears an ended entry', async ({ page, api }) => {
  await openPanel(page);
  const row = page.locator('[data-testid="mcp-row"][data-profile="vscode"]');
  await row.getByTestId('mcp-revoke').click();
  await expect(page.locator('[data-testid="mcp-row"][data-profile="vscode"] [data-testid="mcp-row-state"]')).toHaveText('Revoked');
  expect((await get(api, 'vscode')).state).toBe('revoked');
  const ended = page.locator('[data-testid="mcp-row"][data-profile="old-laptop"]');
  await ended.getByTestId('mcp-remove-ended').click();
  await expect(page.locator('[data-testid="mcp-row"][data-profile="old-laptop"]')).toHaveCount(0);
  expect((await list(api)).profiles.some((p) => p.id === 'old-laptop')).toBe(false);
  await page.locator('[data-testid="mcp-row"][data-profile="vscode"]').getByTestId('mcp-again').click();
  await expect(page.locator('[data-testid="mcp-card"][data-profile="vscode"]')).toBeVisible();
  expect((await get(api, 'vscode')).state).toBe('pending');
});

test('AIA-07: a card that changed in another window is refused and the list is read again', async ({ page, api }) => {
  // The page keeps showing the list as it was (the other window's change is not read yet), so the card is still on screen when Deny is pressed.
  const frozen = JSON.stringify((await api('GET', '/api/mcp/profiles')).json);
  await page.route('**/api/mcp/profiles', (route) => (route.request().method() === 'GET' ? route.fulfill({ status: 200, contentType: 'application/json', body: frozen }) : route.continue()));
  await openPanel(page);
  const card = page.locator('[data-testid="mcp-card"][data-profile="zed"]');
  await expect(card).toBeVisible();
  // another window starts the host over: its revision moves while this card is still on screen
  expect((await api('POST', '/api/mcp/profiles/zed/reissue', {})).status).toBe(200);
  await card.getByTestId('mcp-deny').click();
  await expect(page.getByTestId('mcp-note')).toContainText('This entry changed while you were looking');
  expect((await get(api, 'zed')).state).toBe('pending');
});

test('AIA-08: Stop all AI access is one press, shows Resume, keeps the approvals, and Resume lifts it', async ({ page, api }) => {
  await openPanel(page);
  await page.getByRole('button', { name: 'Stop all AI access', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Resume AI access', exact: true })).toBeVisible();
  await expect(page.getByText('AI access is stopped', { exact: true })).toBeVisible();
  expect((await list(api)).stopped).toBe(true);
  expect((await get(api, 'vscode')).state).toBe('active');
  await expect(page.locator('[data-testid="mcp-row"][data-profile="vscode"] [data-testid="mcp-row-state"]')).toHaveText('Stopped');
  await page.getByRole('button', { name: 'Resume AI access', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Stop all AI access', exact: true })).toBeVisible();
  expect((await list(api)).stopped).toBe(false);
});

test('AIA-09: the routes need the session token and never answer another origin', async ({ demo, api }) => {
  const none = await fetch(demo.base + '/api/mcp/profiles');
  expect(none.status).toBe(401);
  const stop = await fetch(demo.base + '/api/mcp/stop', { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: 'https://evil.example' }, body: '{}' });
  expect(stop.status).toBe(403);
  expect((await list(api)).stopped).toBe(false);
});
