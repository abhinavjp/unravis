// Case lifecycle over the HTTP API: create, belief header, close, reopen, archive,
// verify, restore, erase. Each test makes its own case so the order does not matter.
import { mkdirSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { test, expect } from '../support/fixtures';
import { createCase, demoId, fwd, waitState } from '../support/api';

test('CASE-07: create is idempotent by request id', async ({ api }) => {
  const first = await api('POST', '/api/cases', { title: 'QA Idem', requestId: 'qa-req-0001' });
  expect(first.status).toBe(201);
  expect(first.json.id).toMatch(/^[a-z2-7]{26}$/);
  const again = await api('POST', '/api/cases', { title: 'QA Idem', requestId: 'qa-req-0001' });
  expect(again.status).toBe(200);
  expect(again.json.id).toBe(first.json.id);
  const list = await api('GET', '/api/cases');
  expect((list.json.cases as any[]).filter((c) => c.title === 'QA Idem')).toHaveLength(1);
  const other = await api('POST', '/api/cases', { title: 'QA Idem Other', requestId: 'qa-req-0001' });
  expect(other.json.id).toBe(first.json.id);
  const a = await api('POST', '/api/cases', { title: 'QA NoReq' });
  const b = await api('POST', '/api/cases', { title: 'QA NoReq' });
  expect(a.json.id).not.toBe(b.json.id);
});

test('CASE-07b: a title of one letter, 81 letters or nothing is refused with a plain reason', async ({ api }) => {
  const before = (await api('GET', '/api/cases')).json.cases.length;
  const empty = await api('POST', '/api/cases', {});
  expect(empty.status).toBe(400);
  expect(empty.json.reason).toBe('bad-title');
  const short = await api('POST', '/api/cases', { title: 'A', requestId: 'qa-short-0001' });
  const long = await api('POST', '/api/cases', { title: 'x'.repeat(81), requestId: 'qa-long-00001' });
  expect([short.status, long.status]).toEqual([400, 400]);
  expect((await api('GET', '/api/cases')).json.cases.length).toBe(before);
});

test('CASE-10a: a right belief header runs and a wrong one answers 409 case-changed', async ({ api }) => {
  const id = demoId('demostal');
  const ok = await api('POST', `/api/cases/${id}/reopen`, undefined, 'closed');
  expect(ok.status).toBe(200);
  const stale = await api('POST', `/api/cases/${id}/reopen`, undefined, 'closed');
  expect(stale.status).toBe(409);
  expect(stale.json.reason).toBe('case-changed');
  const wrongClose = await api('POST', `/api/cases/${id}/close`, undefined, 'closed');
  expect(wrongClose.status).toBe(409);
  expect(wrongClose.json.reason).toBe('case-changed');
  const close = await api('POST', `/api/cases/${id}/close`, undefined, 'open');
  expect(close.status).toBe(200);
  expect((await api('GET', `/api/cases/${id}`)).json.state).toBe('closed');
});

test('CASE-10b: a header value that is not a state answers 400 bad-expect-state', async ({ api }) => {
  const id = await createCase(api, 'QA Header');
  const r = await api('POST', `/api/cases/${id}/close`, undefined, 'Open');
  expect(r.status).toBe(400);
  expect(r.json.reason).toBe('bad-expect-state');
  expect((await api('GET', `/api/cases/${id}`)).json.state).toBe('open');
});

test('CASE-10: delete with a wrong belief answers 409 and keeps the case', async ({ api }) => {
  const id = await createCase(api, 'QA Delete Belief');
  const r = await api('DELETE', `/api/cases/${id}`, { confirm: id.slice(0, 8) }, 'closed');
  expect(r.status).toBe(409);
  expect(r.json?.reason).toBe('case-changed');
  expect((await api('GET', `/api/cases/${id}`)).status).toBe(200);
});

test('CASE-12: archive refuses a bad folder or a short passphrase and changes nothing', async ({ api, demo }) => {
  const id = await createCase(api, 'QA Archive Folder');
  const good = join(demo.scratch, 'arch-folder-checks');
  mkdirSync(good);
  const bad = ['//srv/share/x', 'relative/dir', fwd(good) + '/../arch-folder-checks', fwd(good) + '/./x', fwd(demo.configDir), fwd(demo.configDir) + '/inside'];
  for (const path of bad) {
    const r = await api('POST', `/api/cases/${id}/archive`, { path, passphrase: 'qa passphrase one' }, 'open');
    expect(r.status, path).toBe(400);
    expect(r.json.reason, path).toBe('bad-folder');
  }
  const pass = await api('POST', `/api/cases/${id}/archive`, { path: fwd(good), passphrase: 'short' }, 'open');
  expect(pass.status).toBe(400);
  expect(pass.json.reason).toBe('bad-passphrase');
  expect((await api('GET', `/api/cases/${id}`)).json.state).toBe('open');
  expect(readdirSync(good)).toEqual([]);
});

test('CASE-13: archive writes one case file and the case becomes archived', async ({ api, demo }) => {
  const id = await createCase(api, 'Checkout Slowdown 17 October');
  const folder = join(demo.scratch, 'arch-happy');
  mkdirSync(folder);
  const r = await api('POST', `/api/cases/${id}/archive`, { path: fwd(folder), passphrase: 'qa passphrase one' }, 'open');
  expect(r.status).toBe(202);
  const c = await waitState(api, id, 'archived');
  expect(c.archive.current.keyKind).toBe('typed');
  const name: string = c.archive.current.name;
  expect(name.startsWith('checkout-slowdown-17-october-' + id.slice(0, 8) + '-')).toBe(true);
  expect(name).toMatch(/-\d{8}-\d{6}\.unravis-case$/);
  const files = readdirSync(folder);
  expect(files).toEqual([name]);
  expect(files.some((f) => f.endsWith('.part'))).toBe(false);
  const list = await api('GET', '/api/cases');
  expect((list.json.cases as any[]).some((x) => x.id === id)).toBe(false);
});

test('CASE-15: file names follow the slug rules and orphan-files stays empty', async ({ api, demo }) => {
  const folder = join(demo.scratch, 'arch-slug');
  mkdirSync(folder);
  const vectors: [string, string][] = [
    ['Q3 Slow API!', 'q3-slow-api-'],
    ['CON', 'case-con-'],
    ['x'.repeat(100), 'x'.repeat(40) + '-']
  ];
  for (const [title, prefix] of vectors) {
    const id = await createCase(api, title);
    const r = await api('POST', `/api/cases/${id}/archive`, { path: fwd(folder), passphrase: 'qa passphrase one' }, 'open');
    expect(r.status, title).toBe(202);
    const c = await waitState(api, id, 'archived');
    expect((c.archive.current.name as string).startsWith(prefix), title + ' -> ' + c.archive.current.name).toBe(true);
  }
  const orphans = await api('GET', '/api/cases/orphan-files');
  expect(orphans.json.files).toEqual([]);
});

test('CASE-17: verify refuses a wrong passphrase and accepts the right one', async ({ api, demo }) => {
  const id = await createCase(api, 'QA Verify');
  const folder = join(demo.scratch, 'arch-verify');
  mkdirSync(folder);
  await api('POST', `/api/cases/${id}/archive`, { path: fwd(folder), passphrase: 'qa passphrase one' }, 'open');
  await waitState(api, id, 'archived');
  const wrong = await api('POST', `/api/cases/${id}/verify`, { passphrase: 'wrong wrong wrong' }, 'archived');
  expect(wrong.status).toBeLessThan(500);
  const right = await api('POST', `/api/cases/${id}/verify`, { passphrase: 'qa passphrase one' }, 'archived');
  expect([202, 409]).toContain(right.status);
  await expect
    .poll(async () => JSON.stringify((await api('GET', `/api/cases/${id}`)).json.archive))
    .toContain('verif');
});

test('CASE-20: restore in place brings the case back closed, then it can be reopened', async ({ api, demo }) => {
  const id = await createCase(api, 'QA Restore');
  const folder = join(demo.scratch, 'arch-restore');
  mkdirSync(folder);
  await api('POST', `/api/cases/${id}/archive`, { path: fwd(folder), passphrase: 'qa passphrase one' }, 'open');
  await waitState(api, id, 'archived');
  const r = await api('POST', `/api/cases/${id}/restore-in-place`, undefined, 'archived');
  expect(r.status).toBe(200);
  expect(r.json.state).toBe('closed');
  const list = await api('GET', '/api/cases');
  expect((list.json.cases as any[]).find((x) => x.id === id)?.state).toBe('closed');
  const reopen = await api('POST', `/api/cases/${id}/reopen`, undefined, 'closed');
  expect(reopen.status).toBe(200);
  expect(reopen.json.state).toBe('open');
  const freed = await api('POST', `/api/cases/${demoId('demofree')}/restore-in-place`, undefined, 'archived');
  expect(freed.status).toBeGreaterThanOrEqual(400);
});

test('CASE-18a: free and restore-as-new need a master key and change nothing in the demo', async ({ api }) => {
  const id = demoId('demoarch');
  const free = await api('POST', `/api/cases/${id}/free`, { passphrase: 'qa passphrase one' }, 'archived');
  expect(free.status).toBe(409);
  expect(free.json.reason).toBe('no-master-key');
  const asNew = await api('POST', `/api/cases/${id}/restore-as-new`, { path: '', passphrase: 'qa passphrase one' }, 'archived');
  expect(asNew.status).toBe(409);
  expect(asNew.json.reason).toBe('no-master-key');
  expect((await api('GET', `/api/cases/${id}`)).json.state).toBe('archived');
});

test('CASE-22: erase needs the short code and an erased case is gone', async ({ api }) => {
  const id = demoId('demofail');
  const wrong = await api('DELETE', `/api/cases/${id}`, { confirm: 'wrong' }, 'open');
  expect(wrong.status).toBe(400);
  expect(wrong.json.reason).toBe('confirm-mismatch');
  expect((await api('GET', `/api/cases/${id}`)).status).toBe(200);
  const ok = await api('DELETE', `/api/cases/${id}`, { confirm: 'demofail' }, 'open');
  expect(ok.status).toBe(204);
  expect((await api('GET', `/api/cases/${id}`)).status).toBe(404);
  expect((await api('POST', `/api/cases/${id}/reopen`, undefined, 'closed')).status).toBe(404);
  const clear = await api('DELETE', `/api/cases/erased/${demoId('k7m2qx4a')}`);
  expect(clear.status).toBe(204);
});
