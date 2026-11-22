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
console.log('compare-go-failures: ok');
