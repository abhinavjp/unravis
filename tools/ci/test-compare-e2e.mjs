// Self-test of tools/ci/compare-e2e-failures.mjs. Run: node tools/ci/test-compare-e2e.mjs
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { compare, readReport } from './compare-e2e-failures.mjs';

const test = (file, title, status) => ({ file, title, tests: [{ status }] });
const report = {
  suites: [
    { file: 'a.spec.ts', title: 'a.spec.ts', specs: [test('a.spec.ts', 'A-01: ok', 'expected'), test('a.spec.ts', 'A-02: bad', 'unexpected')], suites: [
      { title: 'inner', specs: [test('a.spec.ts', 'A-03: nested flaky', 'flaky'), test('a.spec.ts', 'A-04: skipped', 'skipped')] }
    ] },
    { file: 'b.spec.ts', title: 'b.spec.ts', specs: [test('b.spec.ts', 'B-01: ok', 'expected')] }
  ]
};

assert.equal(readReport(report).length, 5, 'nested suites are walked');
let r = compare(report, '');
assert.deepEqual(r.failures, ['a.spec.ts > A-02: bad', 'a.spec.ts > A-03: nested flaky'], 'unexpected and flaky fail; skipped does not');
assert.deepEqual(r.fresh, r.failures, 'with no baseline every failure is new');
assert.equal(r.skipped, 1);
assert.equal(r.passed, 2);

r = compare(report, '# note\n\na.spec.ts > A-02: bad\n');
assert.deepEqual(r.fresh, ['a.spec.ts > A-03: nested flaky'], 'a listed failure is not new; comments and blank lines are ignored');

r = compare(report, 'a.spec.ts > A-02: bad\na.spec.ts > A-03: nested flaky\nb.spec.ts > B-01: ok\n');
assert.deepEqual(r.fresh, [], 'all failures known');
assert.deepEqual(r.stale, ['b.spec.ts > B-01: ok'], 'a listed test that passes is reported to trim');

// The command line: exit codes.
const here = path.dirname(fileURLToPath(import.meta.url));
const tool = path.join(here, 'compare-e2e-failures.mjs');
const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'cmp-e2e-'));
const write = (n, s) => { const p = path.join(dir, n); fs.writeFileSync(p, s); return p; };
const run = (json, base) => spawnSync(process.execPath, [tool, '--json', json, '--baseline', base], { encoding: 'utf8' });
const empty = write('empty.txt', '');
let res = run(write('r.json', JSON.stringify(report)), empty);
assert.equal(res.status, 1); assert.match(res.stdout, /NEW FAILURE a\.spec\.ts > A-02: bad/);
res = run(write('r.json', JSON.stringify(report)), write('b.txt', 'a.spec.ts > A-02: bad\na.spec.ts > A-03: nested flaky\n'));
assert.equal(res.status, 0, res.stdout + res.stderr);
res = run(write('none.json', JSON.stringify({ suites: [] })), empty);
assert.equal(res.status, 2, 'an empty report is an error, not a pass');
res = run(write('bad.json', 'not json'), empty);
assert.equal(res.status, 2);
res = run(path.join(dir, 'missing.json'), empty);
assert.equal(res.status, 2);
fs.rmSync(dir, { recursive: true, force: true });
console.log('compare-e2e-failures: all checks passed');
