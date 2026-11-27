// Self-test for precheck.mjs. Run: node tools/prototypes/test-precheck.mjs
// Needs Playwright + Chromium; on this PC set PRECHECK_NODE_MODULES=D:\Projects\api-audit-explorer\tools\e2e\node_modules
// Exits 0 on pass, 1 on assertion failure, 2 if the browser/Playwright cannot be found (never a silent pass).
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'precheck-test-'));
const run = (name) => {
  const r = spawnSync(process.execPath, [path.join(here, 'precheck.mjs'), path.join(here, 'testdata', name), '--out', path.join(tmp, name)], { encoding: 'utf8', timeout: 120000 });
  return { status: r.status, text: `${r.stdout}\n${r.stderr}` };
};
let code = 0;
try {
  const bad = run('bad.html');
  const good = run('good.html');
  for (const [n, r] of [['bad', bad], ['good', good]]) {
    if (r.status === 2) { console.error(`test-precheck: tooling problem running ${n}.html (no Playwright/browser?):\n${r.text}`); code = 2; }
  }
  if (!code) {
    assert.equal(bad.status, 1, `bad.html should exit 1\n${bad.text}`);
    for (const c of ['first-screen-words', 'overflow-390', 'console-errors', 'click-dead']) assert.match(bad.text, new RegExp(`^FAIL ${c} - `, 'm'), `bad.html missing FAIL ${c}\n${bad.text}`);
    assert.match(bad.text, /boom/, 'console error text should be shown');
    assert.equal(good.status, 0, `good.html should exit 0\n${good.text}`);
    assert.doesNotMatch(good.text, /^FAIL /m, `good.html should have no FAIL\n${good.text}`);
    assert.match(good.text, /^SUMMARY first-screen words: \d+/m);
    for (const f of ['first-screen-1440.png', 'first-screen-390.png', 'visible-text.txt']) assert.ok(fs.existsSync(path.join(tmp, 'good.html', f)), `missing output ${f}`);
    console.log('test-precheck: OK');
  }
} catch (e) {
  console.error(e.message || e);
  code = 1;
} finally {
  fs.rmSync(tmp, { recursive: true, force: true });
}
process.exit(code);
