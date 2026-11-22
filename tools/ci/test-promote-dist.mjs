// Self-test of tools/release/promote-dist.mjs. Builds throw-away git repositories under the temp folder; no network.
//   node tools/ci/test-promote-dist.mjs
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { buildEnv } from '../release/promote-dist.mjs';

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

const promote = (f, extra = [], opts = {}) =>
  spawnSync(process.execPath, [opts.script || script, '--repo', 'o/r', '--remote', f.url, '--branch', 'promote/abc1234', '--node-version', nodeNow, '--install-cmd', 'node -e 0', '--build-cmd', opts.build || 'node build.cjs', ...extra], { encoding: 'utf8', env: opts.env || process.env });
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

test('no git identity and no signing prompt: the fallback identity is used', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const env = { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1', HOME: f.base };
    const r = promote(f, [], { env });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.equal(sh(f.bare, 'git', ['log', '-1', '--format=%an <%ae>', 'promote/abc1234']), 'Unravis promotion <noreply@users.noreply.github.com>');
  } finally {
    f.cleanup();
  }
});

test('the install and the build get no GitHub token; the install runs twice (clean node_modules second time)', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const env = { ...process.env, GH_TOKEN: 'secret', GITHUB_TOKEN: 'secret' };
    const r = promote(f, [], { env, build: 'node -e "process.exit(process.env.GH_TOKEN || process.env.GITHUB_TOKEN ? 7 : 0)" && node build.cjs' });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.equal((r.stdout.match(/^> install/gm) || []).length, 2);
  } finally {
    f.cleanup();
  }
});

const LEAK = 'ghp_' + 'Leak1234567890abcdefghijklmnopqrstuv';
test('buildEnv: no token variable, no credential helper, private home, a gh that gives nothing', () => {
  const scratch = mkdtempSync(join(tmpdir(), 'pd-env-'));
  try {
    const dirty = {
      PATH: '/usr/bin' + (process.platform === 'win32' ? ';' : ':') + '/bin', HOME: '/home/real', USERPROFILE: 'C:\\Users\\real',
      GH_TOKEN: LEAK, GITHUB_TOKEN: LEAK, GH_ENTERPRISE_TOKEN: LEAK, GITHUB_ENTERPRISE_TOKEN: LEAK, NODE_AUTH_TOKEN: 'n', NPM_TOKEN: 'n',
      npm_config__authtoken: 'n', npm_config_registry: 'https://example.invalid/', AWS_SECRET_ACCESS_KEY: 'a', SIGNING_PASSPHRASE: 'p',
      GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: 'http.https://github.com/.extraheader', GIT_CONFIG_VALUE_0: 'AUTHORIZATION: basic ' + Buffer.from('x-access-token:' + LEAK).toString('base64'),
      GH_CONFIG_DIR: '/home/real/.config/gh', GIT_ASKPASS: '/usr/bin/askpass', SSH_AUTH_SOCK: '/tmp/agent', CUSTOM_VAR: 'x',
      HTTPS_PROXY: 'http://proxy.invalid:3128', NODE_EXTRA_CA_CERTS: '/etc/ca.pem', LANG: 'C', TEMP: '/tmp',
      SSL_CERT_FILE: LEAK, // a token pasted into an allowed name is still dropped
    };
    const out = buildEnv(dirty, scratch);
    const bad = Object.entries(out).filter(([k, v]) => /(TOKEN|SECRET|PASSPHRASE|PASSWORD|CREDENTIAL|ASKPASS|AUTH_SOCK)/i.test(k) || v.includes('ghp_') || v.includes('x-access-token') || v.includes(Buffer.from('x-access-token:' + LEAK).toString('base64')));
    assert.deepEqual(bad, []);
    for (const k of ['GH_TOKEN', 'GITHUB_TOKEN', 'GH_ENTERPRISE_TOKEN', 'GITHUB_ENTERPRISE_TOKEN', 'npm_config_registry', 'CUSTOM_VAR', 'SSL_CERT_FILE']) assert.equal(k in out, false, k);
    // git: the credential helper list is reset, nothing from the system or the person's own git config is read.
    assert.deepEqual([out.GIT_CONFIG_COUNT, out.GIT_CONFIG_KEY_0, out.GIT_CONFIG_VALUE_0, out.GIT_CONFIG_NOSYSTEM, out.GIT_TERMINAL_PROMPT], ['1', 'credential.helper', '', '1', '0']);
    assert.ok(out.GIT_CONFIG_GLOBAL.startsWith(scratch) && readFileSync(out.GIT_CONFIG_GLOBAL, 'utf8') === '');
    // A private home and gh config folder, both empty and under the scratch folder.
    for (const k of ['HOME', 'USERPROFILE', 'GH_CONFIG_DIR', 'XDG_CONFIG_HOME', 'npm_config_cache']) assert.ok(out[k].startsWith(scratch), k + ' = ' + out[k]);
    assert.deepEqual(readdirSync(out.GH_CONFIG_DIR), []);
    // What it keeps, so a build still works.
    assert.equal(out.HTTPS_PROXY, 'http://proxy.invalid:3128');
    assert.equal(out.NODE_EXTRA_CA_CERTS, '/etc/ca.pem');
    assert.equal(out.LANG, 'C');
    assert.ok(out.PATH.startsWith(join(scratch, 'bin')) && out.PATH.includes('/usr/bin'));
    assert.ok(existsSync(join(scratch, 'bin', 'gh')) && existsSync(join(scratch, 'bin', 'gh.cmd')));
    // A Windows-style "Path" key is found and put first-in-line too.
    const w = buildEnv({ Path: 'C:\\Windows', SystemRoot: 'C:\\Windows', GH_TOKEN: LEAK }, scratch);
    assert.ok(w.Path.startsWith(join(scratch, 'bin')) && !('PATH' in w) && w.SystemRoot === 'C:\\Windows' && !('GH_TOKEN' in w));
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
});

test('the real build and install run with that environment: no token variable, and `gh auth token` finds nothing', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    // A fake signed-in gh earlier on PATH than anything else, a gh config folder holding a login, and a credential helper in HOME.
    const fake = join(f.base, 'fakebin');
    mkdirSync(fake);
    const ghHome = join(f.base, 'realhome');
    mkdirSync(join(ghHome, '.config', 'gh'), { recursive: true });
    writeFileSync(join(ghHome, '.config', 'gh', 'hosts.yml'), 'github.com:\n  oauth_token: ' + LEAK + '\n');
    if (process.platform !== 'win32') {
      writeFileSync(join(fake, 'gh'), '#!/bin/sh\necho ' + LEAK + '\n');
      chmodSync(join(fake, 'gh'), 0o755);
    }
    const dump = join(f.base, 'dump.json');
    // The script writes what the dependency code would see: every variable, what `gh auth token` prints, and what git says about credential.helper.
    writeFileSync(join(f.base, 'probe.cjs'), `
      const cp = require('node:child_process');
      const o = { env: process.env, gh: '', git: '' };
      const g = cp.spawnSync('gh', ['auth', 'token'], { encoding: 'utf8', shell: process.platform === 'win32' });
      o.gh = String(g.stdout || '') + '|' + g.status;
      const c = cp.spawnSync('git', ['config', '--get-all', 'credential.helper'], { encoding: 'utf8' });
      o.git = String(c.stdout || '') + '|' + c.status;
      require('node:fs').writeFileSync(${JSON.stringify(dump)}, JSON.stringify(o));
    `);
    const env = {
      ...process.env, HOME: ghHome, USERPROFILE: ghHome, GH_TOKEN: LEAK, GITHUB_TOKEN: LEAK, GH_ENTERPRISE_TOKEN: LEAK,
      PATH: fake + (process.platform === 'win32' ? ';' : ':') + (process.env.PATH || ''),
    };
    const probe = JSON.stringify(join(f.base, 'probe.cjs'));
    const r = promote(f, [], { env, build: 'node ' + probe + ' && node build.cjs' });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    const seen = JSON.parse(readFileSync(dump, 'utf8'));
    assert.ok(!JSON.stringify(seen).includes(LEAK), 'the token reached the build');
    assert.deepEqual(Object.keys(seen.env).filter((k) => /TOKEN|SECRET|PASSWORD|CREDENTIAL/i.test(k)), []);
    assert.notEqual(seen.env.HOME, ghHome);
    assert.equal(seen.env.GIT_CONFIG_KEY_0, 'credential.helper');
    assert.match(seen.gh, /\|(1|null)$/, 'gh gave a result: ' + seen.gh);
    assert.ok(!seen.gh.startsWith(LEAK));
  } finally {
    f.cleanup();
  }
});

test('started through a symlink: it still does the work (real path compare)', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const link = join(f.base, 'linked');
    symlinkSync(join(repoRoot, 'tools', 'release'), link, 'dir');
    const before = remoteHead(f);
    const r = promote(f, [], { script: join(link, 'promote-dist.mjs') });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.match(r.stdout, /DIST_COMMIT=[0-9a-f]{40}/);
    assert.notEqual(remoteHead(f), before);
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
