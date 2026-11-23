// Self-test and guard for tools/ci/web-scope.mjs. Run from anywhere: node tools/ci/test-web-scope.mjs
// The guard scans web/tests and web/scripts for reads of repo files outside web/ and fails when one is not in WEB_READS,
// so a Go-only (or docs-only) pull request that can break a web test always runs the web job of the fast gate.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { touchesWeb } from './web-scope.mjs';

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const LIT = /(['"`])((?:\\.|(?!\1)[^\\\n])*)\1/g;

/** Repo-relative paths (outside web/) that the scripts under `dirs` name: path.join/resolve calls whose string arguments
 *  start, after dropping '..' and '.', at a top-level repo entry, and whole-string literals shaped like a relative path. */
export function scanReads(repoRoot, dirs) {
  const tops = new Set(fs.readdirSync(repoRoot).filter((n) => !['web', '.git', 'node_modules'].includes(n)));
  const found = new Map();
  const segs = (s) => s.split('/').filter((x) => x && x !== '..' && x !== '.');
  const add = (parts, file) => {
    if (!parts.length || !tops.has(parts[0])) return;
    const k = parts.join('/');
    if (!found.has(k)) found.set(k, new Set());
    found.get(k).add(path.relative(repoRoot, file).replace(/\\/g, '/'));
  };
  const scan = (file) => {
    const text = fs.readFileSync(file, 'utf8');
    for (const m of text.matchAll(/path\.(?:join|resolve)\(([^)]*)\)/g)) add([...m[1].matchAll(LIT)].flatMap((x) => segs(x[2])), file);
    for (const m of text.matchAll(LIT)) if (/^(\.\.\/)*[\w.@-]+(\/[\w.@*-]+)+\/?$/.test(m[2])) add(segs(m[2]), file);
  };
  const walk = (d) => {
    if (!fs.existsSync(d)) return;
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, e.name);
      if (e.isDirectory()) { if (e.name !== 'node_modules') walk(p); } else if (/\.(cjs|mjs|js)$/.test(e.name)) scan(p);
    }
  };
  for (const d of dirs) walk(path.join(repoRoot, d));
  return found;
}

/** The reads that touchesWeb does not cover. A directory read counts as covered when a file inside it is. */
export function unlisted(found) {
  return [...found].filter(([p]) => !touchesWeb(p) && !touchesWeb(p.replace(/\/?$/, '/x'))).map(([p, files]) => `${p} (read by ${[...files].join(', ')})`);
}

// The rule itself.
for (const f of ['web/src/App.svelte', 'web/package.json', 'internal/audit/models.go', 'internal/casestore/plan.go', 'internal/audit/thresholds.go',
  'internal/server/routes.go', 'internal/blackbox/sample.go', 'internal/calltrace/types.go', 'internal/collect/collect.go',
  'internal/collect/testdata/wording.golden.json', 'internal/importer/reasons.go', 'tools/e2e/archive/guided/a.json', 'docs/user/cases.md',
  '.gitattributes', 'cmd/unravis-releases/ui/app.js', 'internal\\casestore\\plan.go']) assert.equal(touchesWeb(f), true, f);
for (const f of ['internal/store/q.go', 'internal/casestore/other.go', 'internal/audit/other.go', 'internal/collect/other.go', 'docs/ci/RELEASE-TRAIN.md',
  'tools/ci/web-scope.mjs', 'README.md', 'cmd/unravis-releases/main.go', 'go.mod']) assert.equal(touchesWeb(f), false, f);

// The scanner, on a throwaway repo: it finds the three ways a test names a repo file, and the guard reports a path nobody listed.
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'web-scope-'));
try {
  for (const d of ['internal/zzz', 'cmd/new', 'web/tests', 'web/scripts']) fs.mkdirSync(path.join(tmp, d), { recursive: true });
  fs.writeFileSync(path.join(tmp, 'web/tests/a.test.cjs'), [
    "const p = path.join(__dirname, '../../internal/zzz/limits.go');",
    "const q = path.resolve(root, '..', 'cmd', 'new', 'x.json');",
    "const r = read('../internal/audit/models.go');",
    "const s = readRepo('internal/server/routes.go');",
    "const noise = '../../etc/passwd'; const msg = 'internal/zzz not found here, sorry'; const lib = '../lib/time';",
  ].join('\n'));
  fs.writeFileSync(path.join(tmp, 'web/scripts/b.cjs'), "const g = path.join(root, '..', '.gitattributes');\n");
  fs.writeFileSync(path.join(tmp, '.gitattributes'), '');
  const found = scanReads(tmp, ['web/tests', 'web/scripts']);
  assert.deepEqual([...found.keys()].sort(), ['.gitattributes', 'cmd/new/x.json', 'internal/audit/models.go', 'internal/server/routes.go', 'internal/zzz/limits.go']);
  assert.deepEqual(unlisted(found).map((s) => s.split(' ')[0]).sort(), ['cmd/new/x.json', 'internal/zzz/limits.go']);
} finally { fs.rmSync(tmp, { recursive: true, force: true }); }

// The guard on the real tests.
const real = scanReads(repo, ['web/tests', 'web/scripts']);
for (const must of ['internal/audit/models.go', 'internal/casestore/plan.go']) assert.ok(real.has(must), `the scanner no longer finds ${must}: did the test move, or did the scanner break?`);
const missing = unlisted(real);
assert.deepEqual(missing, [], `A web test reads a file that tools/ci/web-scope.mjs (WEB_READS) does not list, so a pull request that changes it would skip the web job:\n  ${missing.join('\n  ')}`);
console.log(`web-scope tests ok (${real.size} repo paths read by web tests, all listed)`);
