// Launch link, session token, localhost binding and the belief header when it is missing.
import { execFileSync } from 'node:child_process';
import { test, expect } from '../support/fixtures';
import { createCase } from '../support/api';
import { goHome } from '../support/ui';

test('SEC-02: a missing or wrong session token is refused with 401 on read and write routes', async ({ api }) => {
  const reads = ['/api/cases', '/api/info', '/api/hosts', '/api/keeper/status', '/api/master-key/status'];
  for (const p of reads) {
    const none = await api('GET', p, undefined, undefined, { token: null });
    expect(none.status, 'no token ' + p).toBe(401);
    const wrong = await api('GET', p, undefined, undefined, { token: 'not-the-token' });
    expect(wrong.status, 'wrong token ' + p).toBe(401);
    expect(wrong.text).not.toContain('not-the-token');
  }
  const create = await api('POST', '/api/cases', { title: 'No token', requestId: 'qa-notoken-01' }, undefined, { token: null });
  expect(create.status).toBe(401);
  const list = (await api('GET', '/api/cases')).json.cases as any[];
  expect(list.some((c) => c.title === 'No token')).toBe(false);
});

test('SEC-07: the listener is on 127.0.0.1 and never on all addresses', async ({ demo }) => {
  const url = new URL(demo.base);
  expect(url.hostname).toBe('127.0.0.1');
  const out = execFileSync('netstat', ['-ano', '-p', 'tcp'], { encoding: 'utf8', windowsHide: true });
  const rows = out.split(/\r?\n/).filter((l) => /LISTENING/.test(l) && new RegExp(':' + url.port + '\\s').test(l));
  expect(rows.length).toBeGreaterThan(0);
  for (const row of rows) {
    const local = row.trim().split(/\s+/)[1];
    expect(local, row).toMatch(/^(127\.0\.0\.1|\[::1\]):/);
  }
});

test('SEC-08: a launch link that was already used explains itself, and the bare address gives no data', async ({ demo, page, api, allow }) => {
  // The browser logs the refused link as a failed resource; that is the point of this test (D-C-17).
  allow(/Failed to load resource.*launch/, 'D-C-17');
  const res = await page.goto(demo.link);
  const body = (await page.locator('body').innerText()).trim();
  // A plain 404 with no words is the defect: the person needs to be told the link was used.
  expect(res?.status(), 'status of a used link').not.toBe(200);
  expect(body.length, 'a used link shows text').toBeGreaterThan(0);
  expect(body).toMatch(/already (been )?used|has expired|expired/i);
  const bare = await api('GET', '/api/cases', undefined, undefined, { token: null });
  expect(bare.status).toBe(401);
});

test('SEC-08b: a launch link with one character changed is refused and gives no session', async ({ demo }) => {
  const last = demo.link.slice(-1);
  const edited = demo.link.slice(0, -1) + (last === 'a' ? 'b' : 'a');
  const res = await fetch(edited, { redirect: 'manual' });
  expect(res.status).toBeGreaterThanOrEqual(400);
  expect(res.headers.get('location') ?? '').not.toContain('t=');
});

test('CASE-10c: a move with no belief header runs, but an impossible move is still refused', async ({ api }) => {
  const id = await createCase(api, 'QA No Header');
  const reopenOpen = await api('POST', `/api/cases/${id}/reopen`);
  expect(reopenOpen.status).toBeGreaterThanOrEqual(400);
  expect(reopenOpen.status).toBeLessThan(500);
  expect((await api('GET', `/api/cases/${id}`)).json.state).toBe('open');
  const close = await api('POST', `/api/cases/${id}/close`);
  expect(close.status).toBe(200);
  expect((await api('GET', `/api/cases/${id}`)).json.state).toBe('closed');
  const reopen = await api('POST', `/api/cases/${id}/reopen`);
  expect(reopen.status).toBe(200);
  expect((await api('GET', `/api/cases/${id}`)).json.state).toBe('open');
});

test('SEC-06: the page loads nothing from any host but its own', async ({ page, demo }) => {
  const hosts = new Set<string>();
  page.on('request', (r) => hosts.add(new URL(r.url()).host));
  await page.goto(demo.base + '/#t=' + demo.token);
  await goHome(page);
  await expect(page.getByRole('button', { name: 'Create a new case' })).toBeVisible();
  expect([...hosts]).toEqual([new URL(demo.base).host]);
});
