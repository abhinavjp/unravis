// Self-test of tools/release/promote-dist.mjs. Builds throw-away git repositories under the temp folder; no network.
//   node tools/ci/test-promote-dist.mjs
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, '..', '..');
const script = join(repoRoot, 'tools', 'release', 'promote-dist.mjs');
const nodeNow = process.version.replace(/^v/, '');
let n = 0;
const test = (name, fn) => {
  fn();
  n++;
  console.log('ok - ' + name);
};

const sh = (cwd, cmd, args) => {
  const r = spawnSync(cmd, args, { cwd, encoding: 'utf8' });
  assert.equal(r.status, 0, cmd + ' ' + args.join(' ') + ': ' + r.stderr);
  return r.stdout.trim();
};
const git = (cwd, ...a) => sh(cwd, 'git', ['-c', 'user.name=t', '-c', 'user.email=t@t', '-c', 'commit.gpgsign=false', '-c', 'core.autocrlf=false', ...a]);

// The "build": writes every output folder from web/src/v.txt. mode 'flaky' makes the output differ on every run.
const BUILD = `
const fs = require('node:fs');
const v = fs.readFileSync('src/v.txt', 'utf8').trim();
const mode = fs.existsSync('src/mode.txt') ? fs.readFileSync('src/mode.txt', 'utf8').trim() : '';
if (mode === 'fail') process.exit(3);
for (const d of ['dist', '../cmd/unravis-setup/ui', '../cmd/unravis-lite/ui']) {
  fs.mkdirSync(d + '/assets', { recursive: true });
  fs.writeFileSync(d + '/index.html', '<p>' + v + (mode === 'flaky' ? Math.random() : '') + '</p>\\n');
  fs.writeFileSync(d + '/assets/a.js', 'var v="' + v + '";\\n');
}
`;

function fixture(srcValue, { dist = srcValue, extra = {} } = {}) {
  const base = mkdtempSync(join(tmpdir(), 'pd-test-'));
  const work = join(base, 'work');
  const bare = join(base, 'remote.git');
  mkdirSync(join(work, 'web', 'scripts'), { recursive: true });
  mkdirSync(join(work, 'web', 'src'), { recursive: true });
  cpSync(join(repoRoot, 'web', 'scripts', 'check-dist.cjs'), join(work, 'web', 'scripts', 'check-dist.cjs'));
  writeFileSync(join(work, 'web', 'build.cjs'), BUILD);
  writeFileSync(join(work, 'web', 'src', 'v.txt'), srcValue + '\n');
  for (const [f, c] of Object.entries(extra)) writeFileSync(join(work, f), c);
  sh(work, 'git', ['init', '-q', '-b', 'promote/abc1234']);
  // the committed generated files, as an older promotion left them
  for (const d of ['web/dist', 'cmd/unravis-setup/ui', 'cmd/unravis-lite/ui']) {
    mkdirSync(join(work, d, 'assets'), { recursive: true });
    writeFileSync(join(work, d, 'index.html'), '<p>' + dist + '</p>\n');
    writeFileSync(join(work, d, 'assets', 'a.js'), 'var v="' + dist + '";\n');
  }
  git(work, 'add', '-A');
  git(work, 'commit', '-q', '-m', 'start');
  sh(base, 'git', ['init', '-q', '--bare', remoteName(bare)]);
  git(work, 'push', '-q', pathToFileURL(bare).href, 'promote/abc1234');
  return { base, work, bare, url: pathToFileURL(bare).href, cleanup: () => rmSync(base, { recursive: true, force: true }) };
}
const remoteName = (p) => p;

const promote = (f, extra = []) =>
  spawnSync(process.execPath, [script, '--repo', 'o/r', '--remote', f.url, '--branch', 'promote/abc1234', '--node-version', nodeNow, '--install-cmd', 'node -e 0', '--build-cmd', 'node build.cjs', ...extra], { encoding: 'utf8' });
const remoteHead = (f) => sh(f.bare, 'git', ['rev-parse', 'refs/heads/promote/abc1234']);

test('stale committed files: one commit, only the three folders, pushed', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const before = remoteHead(f);
    const r = promote(f);
    assert.equal(r.status, 0, r.stdout + r.stderr);
    const after = remoteHead(f);
    assert.notEqual(after, before);
    assert.match(r.stdout, new RegExp('DIST_COMMIT=' + after));
    const names = sh(f.bare, 'git', ['diff', '--name-only', before, after]).split('\n').sort();
    assert.deepEqual(names, ['cmd/unravis-lite/ui/assets/a.js', 'cmd/unravis-lite/ui/index.html', 'cmd/unravis-setup/ui/assets/a.js', 'cmd/unravis-setup/ui/index.html', 'web/dist/assets/a.js', 'web/dist/index.html']);
    assert.equal(sh(f.bare, 'git', ['log', '-1', '--format=%s', after]), 'Rebuild web files for promotion abc1234');
    assert.equal(sh(f.bare, 'git', ['rev-list', '--count', before + '..' + after]), '1');
  } finally {
    f.cleanup();
  }
});

test('files already equal to a fresh build: nothing committed, nothing pushed', () => {
  const f = fixture('same');
  try {
    const before = remoteHead(f);
    const r = promote(f);
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.match(r.stdout, /DIST_COMMIT=none/);
    assert.equal(remoteHead(f), before);
  } finally {
    f.cleanup();
  }
});

test('--no-push: commit made, remote unchanged', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const before = remoteHead(f);
    const r = promote(f, ['--no-push']);
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.match(r.stdout, /DIST_COMMIT=[0-9a-f]{40}/);
    assert.equal(remoteHead(f), before);
    const kept = /Kept (\S+)/.exec(r.stdout);
    assert.ok(kept);
    rmSync(kept[1], { recursive: true, force: true });
  } finally {
    f.cleanup();
  }
});

test('a build that is not reproducible stops before the push', () => {
  const f = fixture('new', { dist: 'old', extra: { 'web/src/mode.txt': 'flaky\n' } });
  try {
    const before = remoteHead(f);
    const r = promote(f);
    assert.equal(r.status, 1, r.stdout + r.stderr);
    assert.match(r.stderr, /not reproducible/);
    assert.equal(remoteHead(f), before);
  } finally {
    f.cleanup();
  }
});

test('a failing build stops, nothing pushed', () => {
  const f = fixture('new', { dist: 'old', extra: { 'web/src/mode.txt': 'fail\n' } });
  try {
    const before = remoteHead(f);
    const r = promote(f);
    assert.equal(r.status, 1, r.stdout + r.stderr);
    assert.match(r.stderr, /build failed/);
    assert.equal(remoteHead(f), before);
  } finally {
    f.cleanup();
  }
});

test('a different Node version stops at once (exit 2)', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const r = spawnSync(process.execPath, [script, '--repo', 'o/r', '--remote', f.url, '--branch', 'promote/abc1234', '--node-version', '1.2.3'], { encoding: 'utf8' });
    assert.equal(r.status, 2);
    assert.match(r.stderr, /built with 1\.2\.3/);
  } finally {
    f.cleanup();
  }
});

test('bad arguments stop (exit 2)', () => {
  assert.equal(spawnSync(process.execPath, [script, '--bogus'], { encoding: 'utf8' }).status, 2);
  assert.equal(spawnSync(process.execPath, [script, '--branch', 'x', '--repo', 'not a repo'], { encoding: 'utf8' }).status, 2);
});

test('the pinned Node version matches the workflows, and the commit subject matches ReleaseLib.ps1', () => {
  const src = readFileSync(script, 'utf8');
  const pin = /NODE_VERSION = '([^']+)'/.exec(src)[1];
  for (const wf of ['ci.yml', 'release.yml', 'fast-gate.yml']) {
    const t = readFileSync(join(repoRoot, '.github', 'workflows', wf), 'utf8');
    assert.ok(t.includes("node-version: '" + pin + "'"), wf + ' does not pin node-version ' + pin);
  }
  const subject = /COMMIT_SUBJECT = '([^']+)'/.exec(src)[1];
  const lib = readFileSync(join(repoRoot, 'tools', 'release', 'ReleaseLib.ps1'), 'utf8');
  assert.ok(lib.includes(subject), 'ReleaseLib.ps1 does not know the commit subject "' + subject + '"');
  const ps = readFileSync(join(repoRoot, 'tools', 'release', 'Promote-Develop.ps1'), 'utf8');
  assert.ok(ps.includes(pin), 'Promote-Develop.ps1 does not name Node ' + pin);
});

console.log(n + ' tests passed');
