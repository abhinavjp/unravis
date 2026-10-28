// Files imported into a case, from the small fixtures. IMP ids are extra: the catalogue has no import scenario.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test, expect } from '../support/fixtures';
import { createCase, ensureHost, importFile } from '../support/api';

const FILES = join(__dirname, '..', 'fixtures', 'files');
const read = (n: string) => readFileSync(join(FILES, n));

test('IMP-01: a good access log imports all 24 rows and is counted on the case', async ({ api }) => {
  await ensureHost(api);
  const id = await createCase(api, 'Import Good');
  const r = await importFile(api, id, read('access-good.log'), { name: 'access-good.log' });
  expect(r.status, r.text).toBeLessThan(300);
  const text = JSON.stringify(r.json);
  expect(text).toMatch(/24/);
  const info = await api('GET', '/api/cases/' + id);
  expect(info.json.imports).toBe(1);
  const list = await api('GET', '/api/cases/' + id + '/imports');
  expect(JSON.stringify(list.json)).toContain('access-good.log');
});

test('IMP-02: a log with one impossible date imports with one rejected row', async ({ api }) => {
  await ensureHost(api);
  const id = await createCase(api, 'Import Bad Time');
  const r = await importFile(api, id, read('access-bad-time.log'), { name: 'access-bad-time.log' });
  expect(r.status, r.text).toBeLessThan(300);
  expect(JSON.stringify(r.json)).toMatch(/"rejected":\s*1\b/);
});

test('IMP-03: an empty file and a file with a line past the limit are refused in words, never a server error', async ({ api }) => {
  await ensureHost(api);
  const id = await createCase(api, 'Import Refusals');
  const empty = await importFile(api, id, read('empty.log'), { name: 'empty.log' });
  expect(empty.status, empty.text).toBeGreaterThanOrEqual(400);
  expect(empty.status).toBeLessThan(500);
  const long = await importFile(api, id, read('access-long-line.log'), { name: 'access-long-line.log' });
  expect(long.status, long.text).toBeLessThan(500);
  expect(long.text).not.toMatch(/panic|goroutine|stack/i);
});

test('IMP-04: an import without the acknowledgement fields is refused', async ({ api }) => {
  await ensureHost(api);
  const id = await createCase(api, 'Import No Ack');
  const r = await importFile(api, id, read('access-good.log'), { name: 'x.log', ack: false });
  expect(r.status, r.text).toBeGreaterThanOrEqual(400);
  expect(r.status).toBeLessThan(500);
});
