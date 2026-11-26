// Tests for compare-go-failures.mjs. Run: node tools/ci/test-compare.mjs
import { spawnSync } from 'node:child_process';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const here = path.dirname(fileURLToPath(import.meta.url));
const fx = (n) => path.join(here, '..', '..', 'testdata', 'ci', n);
const run = (json) => spawnSync('node', [path.join(here, 'compare-go-failures.mjs'), '--json', fx(json), '--baseline', fx('baseline.txt')], { encoding: 'utf8' });
let r = run('go-test-ok.json');
assert.equal(r.status, 0, r.stdout);
r = run('go-test-new-failure.json');
assert.equal(r.status, 1); assert.match(r.stdout, /NEW FAILURE p\/b\.TestBrandNew/); assert.doesNotMatch(r.stdout, /NEW FAILURE p\/b\.TestKnown/);
r = run('go-test-build-fail.json');
assert.equal(r.status, 1); assert.match(r.stdout, /NEW FAILURE pkg\.p\/d/);
r = run('go-test-panic.json');
assert.equal(r.status, 1); assert.match(r.stdout, /NEW FAILURE pkg\.p\/e/);
// A Windows host uses the sibling baseline.windows.txt when it exists; any other host keeps the Linux baseline.
const runOS = (os) => spawnSync('node', [path.join(here, 'compare-go-failures.mjs'), '--json', fx('go-test-new-failure.json'), '--baseline', fx('win/baseline.txt'), '--os', os], { encoding: 'utf8' });
r = runOS('linux');
assert.equal(r.status, 1); assert.match(r.stdout, /NEW FAILURE p\/b\.TestBrandNew/);
r = runOS('win32');
assert.equal(r.status, 0, r.stdout); assert.match(r.stdout, /Windows host: comparing with .*baseline\.windows\.txt/);
console.log('compare-go-failures: ok');
// Quarantine: a listed failure is reported, not new; an expired, id-less or malformed line quarantines nothing.
const runQ = (file, today = '2026-11-26') => spawnSync('node', [path.join(here, 'compare-go-failures.mjs'), '--json', fx('go-test-new-failure.json'), '--baseline', fx('baseline.txt'), '--quarantine', fx(file), '--today', today], { encoding: 'utf8' });
r = runQ('quarantine.txt');
assert.equal(r.status, 0, r.stdout); assert.match(r.stdout, /quarantined failure \(BL-593, until 2026-12-10\): p\/b\.TestBrandNew/); assert.match(r.stdout, /0 new/);
r = runQ('quarantine.txt', '2026-12-11');
assert.equal(r.status, 1); assert.match(r.stdout, /NEW FAILURE p\/b\.TestBrandNew/);
r = runQ('quarantine-bad.txt');
assert.equal(r.status, 1); assert.match(r.stdout, /ended on 2026-11-01/); assert.match(r.stdout, /not a valid quarantine line/); assert.match(r.stdout, /NEW FAILURE p\/b\.TestBrandNew/);
console.log('quarantine: ok');
