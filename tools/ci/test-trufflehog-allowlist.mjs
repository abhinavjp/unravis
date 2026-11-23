// Guard for .github/trufflehog-exclude-paths.txt (the secret scan allowlist of supply-chain.yml). Run: node tools/ci/test-trufflehog-allowlist.mjs
// Every entry must be one anchored, literal repo path (only `\.` is allowed as an escape, no space around it), must name an existing
// Go test file (`*_test.go`), and the `secrets` job of the workflow must pass the list to trufflehog and must stay blocking
// (no `continue-on-error` anywhere in that job). No wildcard, directory or catch-all can get in.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const LIST = '.github/trufflehog-exclude-paths.txt';
const TEST_FILE = /_test\.go$/;
const FLAG = '--exclude-paths=.github/trufflehog-exclude-paths.txt';

/** Returns [{line, pattern, path}] for the entries; throws on a line that is not an anchored literal path. Pure. */
export function parseAllowlist(text) {
  const out = [];
  text.split(/\r?\n/).forEach((raw, i) => {
    if (!raw.trim() || raw.startsWith('#')) return;
    // trufflehog compiles every line that is not blank or a comment as it stands, so a stray space means the entry never matches.
    const m = /^\^((?:[A-Za-z0-9_\-/]|\\\.)+)\$$/.exec(raw);
    if (!m) throw new Error(`${LIST}:${i + 1}: "${raw}" must be one anchored literal path like ^dir/file_test\\.go$ (no wildcards, no groups, no space around it)`);
    out.push({ line: i + 1, pattern: raw, path: m[1].replace(/\\\./g, '.') });
  });
  return out;
}

/** The text of the `secrets:` job (from its key to the next job key or the end), or null when there is none. Pure. */
export function secretsJob(workflowText) {
  const m = /^  secrets:[ \t]*\r?\n((?:(?!  \S)[^\n]*\n?)*)/m.exec(workflowText);
  return m ? m[0] : null;
}

/** Problems with the secrets job of a workflow text; empty when it is blocking and wired to the list. Pure. */
export function checkWorkflow(workflowText) {
  const job = secretsJob(workflowText);
  if (job === null) return ['there is no `secrets:` job in supply-chain.yml (renamed or removed?)'];
  const bad = [];
  if (/continue-on-error/.test(job)) bad.push('the `secrets` job has `continue-on-error`; it must stay blocking');
  if (!/^\s*extra_args:.*--results=verified,unknown/m.test(job)) bad.push('the `secrets` job lost its `extra_args: --results=verified,unknown`');
  if (!job.split(/\r?\n/).some((l) => /^\s*extra_args:/.test(l) && l.includes(FLAG))) bad.push(`the \`secrets\` job does not pass ${FLAG}`);
  return bad;
}

// Self-test of the parser: these must all be refused.
for (const bad of ['internal/x_test.go', '^internal/.*$', '^internal/', '^.*_test\\.go$', '.*', '^(a|b)$', '^a/b$|^c$', '^$', '^[a-z]$',
  '^a/b_test\\.go$ ', ' ^a/b_test\\.go$', '\t^a/b_test\\.go$']) {
  assert.throws(() => parseAllowlist(bad), /anchored literal path/, `parser accepted ${JSON.stringify(bad)}`);
}
assert.deepEqual(parseAllowlist('# c\n\n^a/b_test\\.go$\r\n').map((e) => e.path), ['a/b_test.go']);

// Self-test of the workflow check, on a small copy of the real shape.
const WF = [
  'jobs:', '  osv:', '    steps:', '      - run: x', '        continue-on-error: true', '',
  '  secrets:', "    if: github.event_name == 'pull_request'", '    runs-on: ubuntu-24.04', '    steps:',
  '      - uses: trufflesecurity/trufflehog@abc', '        with:', `          extra_args: --results=verified,unknown ${FLAG}`, '',
  '  later:', '    steps:', '      - run: y', '        continue-on-error: true', '',
].join('\n');
assert.deepEqual(checkWorkflow(WF), [], 'a good workflow was refused');
assert.equal(checkWorkflow(WF.replace("    if: github.event_name == 'pull_request'", "    if: github.event_name == 'pull_request'\n    continue-on-error: true")).length, 1, 'job-level continue-on-error got through');
assert.equal(checkWorkflow(WF.replace('          extra_args', '        continue-on-error: true\n          extra_args')).length, 1, 'step-level continue-on-error got through');
assert.equal(checkWorkflow(WF.replace('  secrets:', '  secrets-scan:')).length, 1, 'a renamed job got through');
assert.equal(checkWorkflow(WF.replace(` ${FLAG}`, '')).length, 1, 'a dropped flag got through');
assert.equal(checkWorkflow(WF.replace(`${FLAG}`, '').replace('  later:', `  later:\n    env: ${FLAG}`)).length, 1, 'a flag in another job counted');

const entries = parseAllowlist(fs.readFileSync(path.join(repo, LIST), 'utf8'));
assert.ok(entries.length > 0, 'the allowlist is empty: remove the --exclude-paths flag from supply-chain.yml too');
assert.ok(entries.length <= 25, `${entries.length} entries: the allowlist is for a few fake-credential test files, not for whole areas`);
const seen = new Set();
for (const e of entries) {
  assert.ok(!seen.has(e.path), `${LIST}:${e.line}: ${e.path} listed twice`);
  seen.add(e.path);
  assert.ok(e.path.includes('/'), `${LIST}:${e.line}: ${e.path} is not inside a directory`);
  assert.ok(fs.statSync(path.join(repo, e.path), { throwIfNoEntry: false })?.isFile(), `${LIST}:${e.line}: ${e.path} is not an existing file (renamed or deleted? update the list)`);
  assert.ok(TEST_FILE.test(e.path), `${LIST}:${e.line}: ${e.path} is not a Go test file (*_test.go); tag a single line with trufflehog:ignore or fix the secret`);
}

const problems = checkWorkflow(fs.readFileSync(path.join(repo, '.github/workflows/supply-chain.yml'), 'utf8'));
assert.deepEqual(problems, [], problems.join('; '));

console.log(`trufflehog allowlist ok: ${entries.length} literal Go test file paths, all exist, secrets job blocking and wired in supply-chain.yml`);
