// Guard for .github/trufflehog-exclude-paths.txt (the secret scan allowlist of supply-chain.yml). Run: node tools/ci/test-trufflehog-allowlist.mjs
// Every entry must be one anchored, literal repo path (only `\.` is allowed as an escape), must name a file that exists, must be a
// test or fixture file, and the workflow must pass the file to trufflehog. No wildcard, directory or catch-all can get in.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const LIST = '.github/trufflehog-exclude-paths.txt';
const TEST_FILE = /(_test\.go|\.test\.[mc]?[jt]sx?|\.test\.mjs|^tools\/release\/Test-[\w.-]+\.ps1|(^|\/)testdata\/.+)$/;

/** Returns [{line, pattern, path}] for the entries; throws on a line that is not an anchored literal path. Pure. */
export function parseAllowlist(text) {
  const out = [];
  text.split(/\r?\n/).forEach((raw, i) => {
    const s = raw.trim();
    if (!s || s.startsWith('#')) return;
    const m = /^\^((?:[A-Za-z0-9_\-/]|\\\.)+)\$$/.exec(s);
    if (!m) throw new Error(`${LIST}:${i + 1}: "${s}" must be one anchored literal path like ^dir/file_test\\.go$ (no wildcards, no groups)`);
    out.push({ line: i + 1, pattern: s, path: m[1].replace(/\\\./g, '.') });
  });
  return out;
}

// Self-test of the parser: these must all be refused.
for (const bad of ['internal/x_test.go', '^internal/.*$', '^internal/', '^.*_test\\.go$', '.*', '^(a|b)$', '^a/b$|^c$', '^$', '^[a-z]$']) {
  assert.throws(() => parseAllowlist(bad), /anchored literal path/, `parser accepted ${JSON.stringify(bad)}`);
}
assert.deepEqual(parseAllowlist('# c\n\n^a/b_test\\.go$\n').map((e) => e.path), ['a/b_test.go']);

const entries = parseAllowlist(fs.readFileSync(path.join(repo, LIST), 'utf8'));
assert.ok(entries.length > 0, 'the allowlist is empty: remove the --exclude-paths flag from supply-chain.yml too');
assert.ok(entries.length <= 25, `${entries.length} entries: the allowlist is for a few fake-credential fixtures, not for whole areas`);
const seen = new Set();
for (const e of entries) {
  assert.ok(!seen.has(e.path), `${LIST}:${e.line}: ${e.path} listed twice`);
  seen.add(e.path);
  assert.ok(e.path.includes('/'), `${LIST}:${e.line}: ${e.path} is not inside a directory`);
  assert.ok(fs.statSync(path.join(repo, e.path), { throwIfNoEntry: false })?.isFile(), `${LIST}:${e.line}: ${e.path} is not an existing file (renamed or deleted? update the list)`);
  assert.ok(TEST_FILE.test(e.path), `${LIST}:${e.line}: ${e.path} is not a test or fixture file; fix the secret instead of hiding it`);
}

const wf = fs.readFileSync(path.join(repo, '.github/workflows/supply-chain.yml'), 'utf8');
assert.match(wf, /extra_args:\s*--results=verified,unknown\s+--exclude-paths=\.github\/trufflehog-exclude-paths\.txt\s*$/m, 'supply-chain.yml must pass the list to trufflehog next to --results=verified,unknown');
assert.ok(!/trufflehog[\s\S]{0,400}continue-on-error/.test(wf.slice(wf.indexOf('secrets:'))), 'the secrets job must stay blocking');

console.log(`trufflehog allowlist ok: ${entries.length} literal test-file paths, all exist, wired in supply-chain.yml`);
