// Limits and validation of the routes behind the guided analysis: checklist, saved views, investigation.
import { test, expect } from '../support/fixtures';
import { createCase, demoId } from '../support/api';

const WIN = {
  sourceIds: ['demo-west', 'demo-east'],
  incident: { start: '2026-10-20T08:45:00Z', end: '2026-10-20T09:00:00Z' },
  reference: { start: '2026-10-20T08:30:00Z', end: '2026-10-20T08:45:00Z' }
};

test('GUIDE-15: the 501st checklist item is refused with 413, and one delete makes room again', async ({ api }) => {
  const id = await createCase(api, 'QA Checklist Limit');
  const path = `/api/cases/${id}/checklist`;
  for (let i = 0; i < 500; i++) {
    const r = await api('POST', path, { list: 'todo', text: 'item ' + i });
    if (r.status !== 200) throw new Error('item ' + i + ' answered ' + r.status + ' ' + r.text);
  }
  const over = await api('POST', path, { list: 'todo', text: 'one too many' });
  expect(over.status).toBe(413);
  expect(over.json.reason).toBe('plan-full');
  const all = await api('GET', path);
  expect(all.status).toBe(200);
  expect(all.json.items).toHaveLength(500);
  const first = all.json.items[0].id as string;
  expect((await api('DELETE', `${path}/${first}`)).status).toBe(200);
  expect((await api('POST', path, { list: 'todo', text: 'fits now' })).status).toBe(200);
});

test('GUIDE-15a: an item of 301 characters is refused and nothing is stored', async ({ api }) => {
  const id = await createCase(api, 'QA Checklist Long');
  const path = `/api/cases/${id}/checklist`;
  const long = await api('POST', path, { list: 'todo', text: 'x'.repeat(301) });
  expect(long.status).toBe(400);
  expect(long.json.reason).toBe('plan-invalid');
  const ok = await api('POST', path, { list: 'todo', text: 'x'.repeat(300) });
  expect(ok.status).toBe(200);
  expect((await api('GET', path)).json.items).toHaveLength(1);
});

test('GUIDE-15b: ten items added at once all arrive and none is lost', async ({ api }) => {
  const id = await createCase(api, 'QA Checklist Parallel');
  const path = `/api/cases/${id}/checklist`;
  const results = await Promise.all(Array.from({ length: 10 }, (_, i) => api('POST', path, { list: 'todo', text: 'parallel ' + i })));
  expect(results.map((r) => r.status)).toEqual(Array(10).fill(200));
  expect((await api('GET', path)).json.items).toHaveLength(10);
});

test('MAN-09: the 201st saved view is refused with 413, and one delete makes room again', async ({ api }) => {
  const id = await createCase(api, 'QA Views Limit');
  const path = `/api/cases/${id}/views`;
  for (let i = 0; i < 200; i++) {
    const r = await api('POST', path, { name: 'view ' + i, tool: 'response-time', settings: {} });
    if (r.status !== 200) throw new Error('view ' + i + ' answered ' + r.status + ' ' + r.text);
  }
  const over = await api('POST', path, { name: 'one too many', tool: 'response-time', settings: {} });
  expect(over.status).toBe(413);
  expect(over.json.reason).toBe('plan-full');
  const list = await api('GET', path);
  expect(list.json.views).toHaveLength(200);
  expect((await api('DELETE', `${path}/${list.json.views[0].id}`)).status).toBe(200);
  expect((await api('POST', path, { name: 'fits now', tool: 'response-time', settings: {} })).status).toBe(200);
});

test('MAN-09a: a 61 character name, credentials, an address and a huge setting are refused', async ({ api }) => {
  const id = await createCase(api, 'QA Views Refusals');
  const path = `/api/cases/${id}/views`;
  const longName = await api('POST', path, { name: 'n'.repeat(61), tool: 'response-time', settings: {} });
  expect(longName.status).toBe(400);
  const creds = await api('POST', path, { name: 'creds', tool: 'response-time', settings: { password: 'abc' } });
  expect(creds.status).toBe(400);
  const url = await api('POST', path, { name: 'url', tool: 'response-time', settings: { where: 'mongodb://example.invalid/db' } });
  expect(url.status).toBe(400);
  const big = await api('POST', path, { name: 'big', tool: 'response-time', settings: { text: 'z'.repeat(9000) } });
  expect(big.status).toBe(413);
  expect(big.json.reason).toBe('plan-too-large');
  expect((await api('GET', path)).json.views).toEqual([]);
});

test('MAN-09b: a closed case keeps its views and checklist readable but refuses a change', async ({ api }) => {
  const id = demoId('democlos');
  const read = await api('GET', `/api/cases/${id}/checklist`);
  expect(read.status).toBe(200);
  const write = await api('POST', `/api/cases/${id}/checklist`, { list: 'todo', text: 'late' });
  expect(write.status).toBe(409);
  expect(write.json.reason).toBe('case-closed');
  const views = await api('POST', `/api/cases/${id}/views`, { name: 'late', tool: 'response-time', settings: {} });
  expect(views.status).toBe(409);
});

test('INV-13a: a good request reads both sources and carries the version', async ({ api }) => {
  const id = await createCase(api, 'QA Investigation Good');
  const r = await api('POST', `/api/cases/${id}/investigation`, WIN);
  expect(r.status).toBe(200);
  expect(r.json.result.version).toBe('2026-10-27');
  expect(r.json.read.requests).toBeGreaterThan(0);
  expect(r.json.read.truncated).toBe(false);
  expect(Array.isArray(r.json.read.warnings)).toBe(true);
  expect(typeof r.json.read.iisRows).toBe('number');
});

test('INV-13: empty sources, backwards, too long and unknown case are plain 4xx answers', async ({ api }) => {
  const id = await createCase(api, 'QA Investigation Refusals');
  const path = `/api/cases/${id}/investigation`;
  const bad: [string, unknown][] = [
    ['empty sources', { ...WIN, sourceIds: [] }],
    ['backwards incident', { ...WIN, incident: { start: WIN.incident.end, end: WIN.incident.start } }],
    ['over seven days', { ...WIN, incident: { start: '2026-10-10T08:45:00Z', end: '2026-10-20T09:00:00Z' } }],
    ['unknown source', { ...WIN, sourceIds: ['no-such-source'] }]
  ];
  for (const [what, body] of bad) {
    const r = await api('POST', path, body);
    expect(r.status, what).toBeGreaterThanOrEqual(400);
    expect(r.status, what).toBeLessThan(500);
    expect(r.text, what).not.toMatch(/goroutine|panic|[.]go:[0-9]+/);
  }
  const noCase = await api('POST', '/api/cases/nope-not-a-case/investigation', WIN);
  expect([400, 404]).toContain(noCase.status);
  const missing = await api('POST', '/api/cases/' + 'zzzzzzzzzzzzzzzzzzzzzzzzzz' + '/investigation', WIN);
  expect([400, 404]).toContain(missing.status);
});

test('INV-13c: overlapping windows are read as one stretch and still answer 200', async ({ api }) => {
  const id = await createCase(api, 'QA Investigation Overlap');
  const r = await api('POST', `/api/cases/${id}/investigation`, { ...WIN, reference: { start: '2026-10-20T08:50:00Z', end: '2026-10-20T08:55:00Z' } });
  expect(r.status).toBe(200);
  expect(r.json.read.truncated).toBe(false);
});

test('INV-13b: twenty runs at once answer 200 or a busy word and never a server error', async ({ api }) => {
  const id = await createCase(api, 'QA Investigation Parallel');
  const results = await Promise.all(Array.from({ length: 20 }, () => api('POST', `/api/cases/${id}/investigation`, WIN)));
  for (const r of results) {
    expect(r.status).toBeLessThan(500);
  }
  expect(results.some((r) => r.status === 200)).toBe(true);
});

test('MAN-09c: a saved view with an unknown tool or a garbage window value is refused with 400', async ({ api }) => {
  const id = await createCase(api, 'QA Views Junk');
  const path = `/api/cases/${id}/views`;
  const tool = await api('POST', path, { name: 'junk tool', tool: 'no-such-tool', settings: {} });
  expect(tool.status, 'unknown tool').toBe(400);
  const date = await api('POST', path, { name: 'junk date', tool: 'response-time', settings: { incidentStart: 'not-a-date' } });
  expect(date.status, 'garbage window').toBe(400);
  expect((await api('GET', path)).json.views).toEqual([]);
});
