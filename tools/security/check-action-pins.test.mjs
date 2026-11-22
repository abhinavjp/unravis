import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { checkText } from './check-action-pins.mjs';

const sha = 'a'.repeat(40);
test('tag-pinned action fails', () => assert.equal(checkText('    - uses: actions/checkout@v4\n').length, 1));
test('branch-pinned action fails', () => assert.equal(checkText('      uses: foo/bar@main\n').length, 1));
test('sha-pinned action passes', () => assert.equal(checkText(`      - uses: actions/checkout@${sha} # v4\n`).length, 0));
test('local action passes', () => assert.equal(checkText('      uses: ./.github/actions/x\n').length, 0));
test('aquasecurity is banned even when pinned', () => assert.equal(checkText(`      uses: aquasecurity/trivy-action@${sha}\n`).length, 1));
test('bad-workflow fixture fails', () => assert.ok(checkText(readFileSync(new URL('../../testdata/bad-workflow.yml', import.meta.url), 'utf8')).length >= 1));
