// Case keys over the HTTP API: set, change, remove, unlock a locked case, wrong keys and the wait.
import { test, expect } from '../support/fixtures';
import { createCase, demoId } from '../support/api';

// The server answers a wrong case key with 401 and reason wrong-case-key; a locked case with no key tried is 423 case-locked.
const DEMO_KEY = 'unravis demo case key';

test('KEY-01: a case key must be at least 12 characters and typed twice the same', async ({ api }) => {
  const id = await createCase(api, 'QA Keyed');
  const short = await api('POST', `/api/cases/${id}/key/set`, { passphrase: 'short', confirm: 'short' });
  expect(short.status).toBeGreaterThanOrEqual(400);
  expect(short.status).toBeLessThan(500);
  const mismatch = await api('POST', `/api/cases/${id}/key/set`, { passphrase: 'abcdefghijkl', confirm: 'abcdefghijkx' });
  expect(mismatch.status).toBeGreaterThanOrEqual(400);
  expect(mismatch.status).toBeLessThan(500);
  expect((await api('GET', `/api/cases/${id}/key`)).json.ownKey).toBe(false);
  const ok = await api('POST', `/api/cases/${id}/key/set`, { passphrase: 'qa case key 0001', confirm: 'qa case key 0001' });
  expect(ok.status).toBe(200);
  const key = (await api('GET', `/api/cases/${id}/key`)).json;
  expect(key.ownKey).toBe(true);
  expect(key.ending).toMatch(/^[0-9a-f]{4}$/);
  expect(JSON.stringify((await api('GET', `/api/cases/${id}`)).json)).not.toContain('qa case key 0001');
});

test('KEY-02: a locked demo case lists without its title and opens with the right key', async ({ api }) => {
  const id = demoId('demokeye');
  const row = ((await api('GET', '/api/cases')).json.cases as any[]).find((c) => c.id === id);
  expect(row.keyLocked).toBe(true);
  expect(row.title).toBe('(locked)');
  expect(row.keyEnding).toMatch(/^[0-9a-f]{4}$/);
  const unlock = await api('POST', `/api/cases/${id}/key/unlock`, { passphrase: DEMO_KEY });
  expect(unlock.status).toBe(200);
  expect(unlock.json.locked).toBe(false);
  const opened = await api('GET', `/api/cases/${id}`);
  expect(opened.status).toBe(200);
  expect(opened.json.title).toBe('Payroll run discrepancy, 14 October');
});

test('KEY-03: one wrong key changes nothing and the right key still opens the case', async ({ api }) => {
  const id = demoId('demokeye');
  await api('POST', `/api/cases/${id}/key/lock`);
  const wrong = await api('POST', `/api/cases/${id}/key/unlock`, { passphrase: 'this is not the key' });
  expect(wrong.status).toBe(401);
  expect(wrong.json.reason).toBe('wrong-case-key');
  expect(wrong.text).not.toContain('this is not the key');
  const row = ((await api('GET', '/api/cases')).json.cases as any[]).find((c) => c.id === id);
  expect(row.title).toBe('(locked)');
  const right = await api('POST', `/api/cases/${id}/key/unlock`, { passphrase: DEMO_KEY });
  expect(right.status).toBe(200);
});

test('KEY-05: add, change and remove a case key on an open case', async ({ api }) => {
  const id = await createCase(api, 'QA Key Cycle');
  expect((await api('POST', `/api/cases/${id}/key/change`, { passphrase: 'qa case key 0002', newPassphrase: 'qa case key 0003', confirm: 'qa case key 0003' })).status).toBeGreaterThanOrEqual(400);
  expect((await api('POST', `/api/cases/${id}/key/remove`, { passphrase: 'qa case key 0002' })).status).toBeGreaterThanOrEqual(400);
  expect((await api('POST', `/api/cases/${id}/key/set`, { passphrase: 'qa case key 0002', confirm: 'qa case key 0002' })).status).toBe(200);
  const first = (await api('GET', `/api/cases/${id}/key`)).json.ending;
  const again = await api('POST', `/api/cases/${id}/key/set`, { passphrase: 'qa case key 0009', confirm: 'qa case key 0009' });
  expect(again.status).toBeGreaterThanOrEqual(400);
  const wrongCurrent = await api('POST', `/api/cases/${id}/key/change`, { passphrase: 'wrong current key', newPassphrase: 'qa case key 0003', confirm: 'qa case key 0003' });
  expect(wrongCurrent.status).toBe(401);
  expect((await api('GET', `/api/cases/${id}/key`)).json.ending).toBe(first);
  const change = await api('POST', `/api/cases/${id}/key/change`, { passphrase: 'qa case key 0002', newPassphrase: 'qa case key 0003', confirm: 'qa case key 0003' });
  expect(change.status).toBe(200);
  expect((await api('GET', `/api/cases/${id}/key`)).json.ending).not.toBe(first);
  const remove = await api('POST', `/api/cases/${id}/key/remove`, { passphrase: 'qa case key 0003' });
  expect(remove.status).toBe(200);
  expect((await api('GET', `/api/cases/${id}/key`)).json.ownKey).toBe(false);
});

test('KEY-04: five wrong keys, then a wait that covers even the right key', async ({ api }) => {
  const id = demoId('demokeye');
  await api('POST', `/api/cases/${id}/key/lock`);
  for (let i = 0; i < 5; i++) {
    const r = await api('POST', `/api/cases/${id}/key/unlock`, { passphrase: 'wrong one ' + i });
    expect(r.status, 'try ' + i).toBe(401);
    expect(r.json.reason).toBe('wrong-case-key');
  }
  const sixth = await api('POST', `/api/cases/${id}/key/unlock`, { passphrase: 'wrong six' });
  expect(sixth.status).toBe(429);
  expect(sixth.json.retryAfterSeconds).toBeGreaterThanOrEqual(1);
  expect(sixth.json.reason).toBe('throttled');
  const right = await api('POST', `/api/cases/${id}/key/unlock`, { passphrase: DEMO_KEY });
  expect(right.status).toBe(429);
});
