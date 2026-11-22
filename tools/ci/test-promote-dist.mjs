// Self-test of tools/release/promote-dist.mjs. Builds throw-away git repositories under the temp folder; no network.
//   node tools/ci/test-promote-dist.mjs
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { assertFreshClone, buildEnv, copyRoots, gitBaseEnv, sameRoots } from '../release/promote-dist.mjs';

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
    for (const k of ['GH_TOKEN', 'GITHUB_TOKEN', 'GH_ENTERPRISE_TOKEN', 'GITHUB_ENTERPRISE_TOKEN', 'CUSTOM_VAR', 'SSL_CERT_FILE']) assert.equal(k in out, false, k);
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

test('build code that plants a hook or git config in its clone never runs with the token', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const marker = join(f.base, 'hook-ran.txt');
    // The "build" writes a pre-push and a post-commit hook, points core.hooksPath and core.fsmonitor at scripts, then builds normally.
    writeFileSync(join(f.base, 'plant.cjs'), `
      const fs = require('node:fs'), path = require('node:path');
      const git = path.resolve('..', '.git');
      const hooks = path.join(git, 'hooks'); fs.mkdirSync(hooks, { recursive: true });
      const body = '#!/bin/sh\\necho ran >> ' + ${JSON.stringify(JSON.stringify(marker))} + '\\n';
      for (const h of ['pre-push', 'post-commit', 'pre-commit', 'fsmonitor']) { fs.writeFileSync(path.join(hooks, h), body); fs.chmodSync(path.join(hooks, h), 0o755); }
      fs.appendFileSync(path.join(git, 'config'), '[core]\\n\\thooksPath = ' + hooks.replace(/\\\\/g, '/') + '\\n\\tfsmonitor = ' + path.join(hooks, 'fsmonitor').replace(/\\\\/g, '/') + '\\n');
    `);
    const before = remoteHead(f);
    const r = promote(f, [], { build: 'node ' + JSON.stringify(join(f.base, 'plant.cjs')) + ' && node build.cjs' });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.notEqual(remoteHead(f), before, 'the commit was pushed');
    assert.equal(existsSync(marker), false, 'a planted hook ran');
    // The same hook in the commit clone's own folder, if it were there, would fire: prove the plant itself is effective in a clone.
    const probe = join(f.base, 'probe');
    sh(f.base, 'git', ['clone', '-q', f.url, probe]);
    mkdirSync(join(probe, '.git', 'hooks'), { recursive: true });
    writeFileSync(join(probe, '.git', 'hooks', 'pre-commit'), '#!/bin/sh\necho ran >> ' + JSON.stringify(marker) + '\n');
    chmodSync(join(probe, '.git', 'hooks', 'pre-commit'), 0o755);
    writeFileSync(join(probe, 'x.txt'), 'x');
    sh(probe, 'git', ['add', '-A']);
    sh(probe, 'git', ['-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-q', '-m', 'x']);
    assert.equal(existsSync(marker), true, 'the test hook is not effective, so the check above proves nothing');
  } finally {
    f.cleanup();
  }
});

test('a build that leaves a link or a .git entry in a generated folder stops before any commit', () => {
  for (const [name, plant] of [['link', "fs.symlinkSync('/etc/passwd', 'dist/assets/link.js')"], ['.git', "fs.mkdirSync('dist/.git')"]]) {
    const f = fixture('new', { dist: 'old' });
    try {
      writeFileSync(join(f.base, 'plant2.cjs'), "const fs = require('node:fs'); " + plant + ';');
      const before = remoteHead(f);
      const r = promote(f, [], { build: 'node build.cjs && node ' + JSON.stringify(join(f.base, 'plant2.cjs')) });
      assert.equal(r.status, 1, name + ': ' + r.stdout + r.stderr);
      assert.match(r.stderr, /The build produced/);
      assert.equal(remoteHead(f), before);
    } finally {
      f.cleanup();
    }
  }
});

test('copyRoots replaces the generated folders and nothing else', () => {
  const base = mkdtempSync(join(tmpdir(), 'pd-copy-'));
  try {
    const a = join(base, 'a'), b = join(base, 'b');
    for (const [root, text] of [['web/dist', 'new'], ['cmd/unravis-setup/ui', 'new']]) { mkdirSync(join(a, root, 'assets'), { recursive: true }); writeFileSync(join(a, root, 'assets', 'x.js'), text); }
    mkdirSync(join(a, 'web', 'src'), { recursive: true }); writeFileSync(join(a, 'web', 'src', 'evil.txt'), 'e');
    mkdirSync(join(b, 'web/dist'), { recursive: true }); writeFileSync(join(b, 'web/dist/old.js'), 'old');
    mkdirSync(join(b, 'cmd/unravis-lite/ui'), { recursive: true }); writeFileSync(join(b, 'cmd/unravis-lite/ui/gone.js'), 'g');
    copyRoots(a, b);
    assert.equal(existsSync(join(b, 'web/dist/old.js')), false);
    assert.equal(readFileSync(join(b, 'web/dist/assets/x.js'), 'utf8'), 'new');
    assert.equal(existsSync(join(b, 'cmd/unravis-lite/ui')), false, 'a folder the build did not make is removed');
    assert.equal(existsSync(join(b, 'web/src/evil.txt')), false, 'only the three folders are copied');
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
});

test('buildEnv keeps registry and certificate settings that carry no password', () => {
  const scratch = mkdtempSync(join(tmpdir(), 'pd-env2-'));
  try {
    const out = buildEnv({ PATH: '/usr/bin', npm_config_registry: 'https://mirror.example/npm/', npm_config_cafile: '/etc/ca.pem', npm_config_proxy: 'http://user:pw@proxy.invalid:3128', npm_config__authtoken: 'x', SSL_CERT_FILE: '/etc/ca.pem' }, scratch);
    assert.equal(out.npm_config_registry, 'https://mirror.example/npm/');
    assert.equal(out.npm_config_cafile, '/etc/ca.pem');
    assert.equal(out.SSL_CERT_FILE, '/etc/ca.pem');
    assert.equal('npm_config_proxy' in out, false, 'a proxy with a password is dropped');
    assert.equal('npm_config__authtoken' in out, false);
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
});

test('a build that plants a git filter, attributes, proxy or config in any clone next to it does not run it with the token', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const marker = join(f.base, 'filter-ran.txt');
    // The "build" looks for every commit clone this program could have next to it (old layout: ../../repo; any unravis-dist-*/repo in the
    // temp folder) and plants a clean filter for every file, a proxy and sslVerify=false. Under the right order there is nothing to plant in.
    writeFileSync(join(f.base, 'plant3.cjs'), `
      const fs = require('node:fs'), path = require('node:path'), os = require('node:os');
      const marker = ${JSON.stringify(marker)};
      const targets = new Set([path.resolve('..', '..', 'repo')]);
      for (const d of fs.readdirSync(os.tmpdir())) if (d.startsWith('unravis-dist-')) targets.add(path.join(os.tmpdir(), d, 'repo'));
      let planted = 0;
      for (const t of targets) {
        const git = path.join(t, '.git');
        if (!fs.existsSync(git)) continue;
        planted++;
        fs.mkdirSync(path.join(git, 'info'), { recursive: true });
        fs.appendFileSync(path.join(git, 'info', 'attributes'), '* filter=x\\n');
        fs.appendFileSync(path.join(git, 'config'), '[filter "x"]\\n\\tclean = \\"sh -c \\'echo token=$GH_TOKEN >> ' + marker + '; cat\\'\\"\\n[http]\\n\\tproxy = http://127.0.0.1:9\\n\\tsslVerify = false\\n');
      }
      fs.writeFileSync(${JSON.stringify(join(f.base, 'planted-count.txt'))}, String(planted));
    `);
    const before = remoteHead(f);
    const r = promote(f, [], { env: { ...process.env, GH_TOKEN: 'tok-from-parent' }, build: 'node ' + JSON.stringify(join(f.base, 'plant3.cjs')) + ' && node build.cjs' });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.notEqual(remoteHead(f), before, 'the commit was pushed');
    assert.equal(existsSync(marker), false, 'a planted filter ran: ' + (existsSync(marker) ? readFileSync(marker, 'utf8') : ''));
    assert.equal(readFileSync(join(f.base, 'planted-count.txt'), 'utf8'), '0', 'the commit clone existed while the build ran');
  } finally {
    f.cleanup();
  }
});

test('a process the build left running plants a filter in the commit clone after the freshness check: no token is printed and nothing is pushed', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const marker = join(f.base, 'late-filter.txt');
    // The build starts a detached child that waits until the commit clone has the built folders (the first freshness check is behind
    // us, `git add` is next) and then plants a clean filter for every file plus attributes, and an http.proxy for the push.
    writeFileSync(join(f.base, 'late.cjs'), `
      const fs = require('node:fs'), path = require('node:path'), os = require('node:os');
      const end = Date.now() + 60000;
      const tick = () => {
        let hit = '';
        for (const d of fs.readdirSync(os.tmpdir())) {
          if (!d.startsWith('unravis-dist-') || d.startsWith('unravis-dist-build-')) continue;
          const repo = path.join(os.tmpdir(), d, 'repo');
          let txt = ''; try { txt = fs.readFileSync(path.join(repo, 'web', 'dist', 'index.html'), 'utf8'); } catch {}
          if (txt.includes('new') && fs.existsSync(path.join(repo, '.git', 'config'))) hit = repo; // the built file, not the committed one ('old')
        }
        if (!hit) { if (Date.now() < end) setTimeout(tick, 2); return; }
        fs.mkdirSync(path.join(hit, '.git', 'info'), { recursive: true });
        fs.appendFileSync(path.join(hit, '.git', 'info', 'attributes'), '* filter=x\\n');
        fs.appendFileSync(path.join(hit, '.git', 'config'), '[filter "x"]\\n\\tclean = \\"sh -c \\'echo token=$GH_TOKEN$GITHUB_TOKEN >> ' + ${JSON.stringify(marker)} + '; cat\\'\\"\\n[http]\\n\\tproxy = http://127.0.0.1:9\\n');
      };
      tick();
    `);
    writeFileSync(join(f.base, 'spawn-late.cjs'), `require('node:child_process').spawn(process.execPath, [${JSON.stringify(join(f.base, 'late.cjs'))}], { detached: true, stdio: 'ignore' }).unref();`);
    const before = remoteHead(f);
    const r = promote(f, [], { env: { ...process.env, GH_TOKEN: 'tok-from-parent', GITHUB_TOKEN: 'tok-from-parent' }, build: 'node ' + JSON.stringify(join(f.base, 'spawn-late.cjs')) + ' && node build.cjs' });
    assert.equal(r.status, 1, r.stdout + r.stderr);
    assert.match(r.stderr, /fresh clone/);
    assert.equal(remoteHead(f), before, 'nothing was pushed');
    // The filter really ran (the test is not vacuous), and what it saw holds no token.
    assert.equal(existsSync(marker), true, 'the late filter never ran, so the test proves nothing');
    const lines = readFileSync(marker, 'utf8').trim().split('\n');
    assert.ok(lines.length > 0 && lines.every((l) => l === 'token='), 'the filter saw a token: ' + lines[0]);
  } finally {
    f.cleanup();
  }
});

test('the build clone is gone before the commit clone exists, and the commit clone is fresh', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    // The build lists the temp folder: no unravis-dist-* (commit clone) folder may exist yet.
    writeFileSync(join(f.base, 'ls.cjs'), `const fs=require('node:fs'),os=require('node:os');fs.writeFileSync(${JSON.stringify(join(f.base, 'ls.txt'))}, fs.readdirSync(os.tmpdir()).filter((d)=>d.startsWith('unravis-dist-') && !d.startsWith('unravis-dist-build-')).join(','));`);
    const r = promote(f, ['--no-push', '--keep'], { build: 'node ' + JSON.stringify(join(f.base, 'ls.cjs')) + ' && node build.cjs' });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    const kept = /Kept (\S+)/.exec(r.stdout)[1];
    try {
      assert.equal(readFileSync(join(f.base, 'ls.txt'), 'utf8').split(',').filter((d) => d && join(tmpdir(), d) !== kept).length, 0, 'a commit clone folder existed during the build');
      assert.equal(existsSync(join(kept, 'build')), false);
      assertFreshClone(join(kept, 'repo'));
    } finally {
      rmSync(kept, { recursive: true, force: true });
    }
  } finally {
    f.cleanup();
  }
});

test('assertFreshClone refuses a filter, a hook, a proxy and an info/attributes file', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const c = join(f.base, 'clone');
    sh(f.base, 'git', ['clone', '-q', f.url, c]);
    assertFreshClone(c);
    const plants = {
      filter: (d) => sh(d, 'git', ['config', 'filter.x.clean', 'cat']),
      http: (d) => sh(d, 'git', ['config', 'http.proxy', 'http://127.0.0.1:9']),
      fsmonitor: (d) => sh(d, 'git', ['config', 'core.fsmonitor', 'true']),
      hook: (d) => writeFileSync(join(d, '.git', 'hooks', 'pre-push'), '#!/bin/sh\n'),
      attributes: (d) => { mkdirSync(join(d, '.git', 'info'), { recursive: true }); writeFileSync(join(d, '.git', 'info', 'attributes'), '* filter=x\n'); },
    };
    for (const [name, plant] of Object.entries(plants)) {
      const d = c + '-' + name;
      sh(f.base, 'git', ['clone', '-q', f.url, d]);
      plant(d);
      assert.throws(() => assertFreshClone(d), /fresh clone/, name);
    }
  } finally {
    f.cleanup();
  }
});

test('gitBaseEnv drops variables that redirect git, keeps the rest', () => {
  const e = gitBaseEnv({ GIT_DIR: '/x', GIT_WORK_TREE: '/y', GIT_INDEX_FILE: '/z', GIT_SSH_COMMAND: 'evil', GIT_ASKPASS: 'a', GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: 'k', GIT_CONFIG_VALUE_0: 'v', GIT_CONFIG_GLOBAL: '/g', GIT_CONFIG_NOSYSTEM: '1', GIT_SSL_CAINFO: '/ca.pem', PATH: '/bin', GH_TOKEN: 't', GITHUB_TOKEN: 't', NPM_TOKEN: 't', MY_SECRET: 's' });
  for (const k of ['GH_TOKEN', 'GITHUB_TOKEN', 'NPM_TOKEN', 'MY_SECRET']) assert.equal(k in e, false, k);
  for (const k of ['GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_SSH_COMMAND', 'GIT_ASKPASS', 'GIT_CONFIG_COUNT', 'GIT_CONFIG_KEY_0', 'GIT_CONFIG_VALUE_0']) assert.equal(k in e, false, k);
  assert.deepEqual([e.GIT_CONFIG_GLOBAL, e.GIT_CONFIG_NOSYSTEM, e.GIT_SSL_CAINFO, e.PATH, e.GIT_NO_REPLACE_OBJECTS, e.GIT_TERMINAL_PROMPT], ['/g', '1', '/ca.pem', '/bin', '1', '0']);
});

test('a GIT_DIR in the caller\'s environment does not send the commit to another repository', () => {
  const f = fixture('new', { dist: 'old' });
  try {
    const other = join(f.base, 'other');
    sh(f.base, 'git', ['init', '-q', other]);
    const before = remoteHead(f);
    const r = promote(f, [], { env: { ...process.env, GIT_DIR: join(other, '.git'), GIT_WORK_TREE: other } });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.notEqual(remoteHead(f), before);
    assert.equal(sh(other, 'git', ['rev-list', '--all', '--count']), '0');
  } finally {
    f.cleanup();
  }
});

// A real npm, under the real buildEnv: a package with a local file: dependency and a lock file, offline. Catches an npm setting that
// makes every `npm ci` fail (npm 10 stops with "double-loading config" when the user and global config are one file).
function realNpmFixture(base) {
  const dep = join(base, 'dep'), app = join(base, 'app');
  mkdirSync(dep, { recursive: true }); mkdirSync(app, { recursive: true });
  writeFileSync(join(dep, 'package.json'), JSON.stringify({ name: 'dep', version: '1.0.0', main: 'index.js' }));
  writeFileSync(join(dep, 'index.js'), 'module.exports = 7;\n');
  writeFileSync(join(app, 'package.json'), JSON.stringify({ name: 'app', version: '1.0.0', private: true, dependencies: { dep: 'file:../dep' } }));
  return app;
}
const npmRun = (args, cwd, env) => spawnSync('npm', args, { cwd, env, encoding: 'utf8', shell: process.platform === 'win32' });

test('a real `npm ci` works under buildEnv (and the config files npm loads are three different ones)', () => {
  const base = mkdtempSync(join(tmpdir(), 'pd-npm-'));
  try {
    const app = realNpmFixture(base);
    const env = buildEnv(process.env, join(base, 'scratch'));
    assert.equal(new Set([env.npm_config_userconfig, env.npm_config_globalconfig, env.GIT_CONFIG_GLOBAL]).size, 3);
    const lock = npmRun(['install', '--package-lock-only', '--offline', '--ignore-scripts', '--no-audit', '--no-fund'], app, env);
    assert.equal(lock.status, 0, lock.stdout + lock.stderr);
    const ci = npmRun(['ci', '--offline', '--no-audit', '--no-fund'], app, env);
    assert.equal(ci.status, 0, ci.stdout + ci.stderr);
    assert.ok(!/double-loading/.test(ci.stdout + ci.stderr));
    assert.equal(spawnSync(process.execPath, ['-e', 'process.exit(require("dep") === 7 ? 0 : 3)'], { cwd: app }).status, 0, 'dep was installed');
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
});

test('promote-dist runs a real `npm ci` (offline) from its own install step, in the build clone only', () => {
  const base = mkdtempSync(join(tmpdir(), 'pd-npm2-'));
  try {
    const app = realNpmFixture(base);
    const lockEnv = buildEnv(process.env, join(base, 'scratch'));
    assert.equal(npmRun(['install', '--package-lock-only', '--offline', '--ignore-scripts', '--no-audit', '--no-fund'], app, lockEnv).status, 0);
    // The fixture repository gets the package (web/package.json, the lock file and the dep) and the same build as the other tests.
    const dep = join(base, 'dep');
    const f = fixture('new', { dist: 'old', extra: {} });
    try {
      const w = join(f.work, 'web');
      writeFileSync(join(w, 'package.json'), JSON.stringify({ name: 'app', version: '1.0.0', private: true, dependencies: { dep: 'file:../dep' } }));
      writeFileSync(join(w, 'package-lock.json'), readFileSync(join(app, 'package-lock.json'), 'utf8'));
      cpSync(dep, join(f.work, 'dep'), { recursive: true });
      git(f.work, 'add', '-A');
      git(f.work, 'commit', '-q', '-m', 'package');
      git(f.work, 'push', '-q', f.url, 'promote/abc1234');
      const before = remoteHead(f);
      const r = spawnSync(process.execPath, [script, '--repo', 'o/r', '--remote', f.url, '--branch', 'promote/abc1234', '--node-version', nodeNow, '--install-cmd', 'npm ci --offline --no-audit --no-fund', '--build-cmd', 'node build.cjs'], { encoding: 'utf8' });
      assert.equal(r.status, 0, r.stdout + r.stderr);
      assert.notEqual(remoteHead(f), before);
      assert.ok(!/double-loading/.test(r.stdout + r.stderr));
    } finally {
      f.cleanup();
    }
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
});

test('sameRoots compares names and bytes of the three folders', () => {
  const base = mkdtempSync(join(tmpdir(), 'pd-same-'));
  try {
    const mk = (d, text) => { mkdirSync(join(base, d, 'web/dist'), { recursive: true }); writeFileSync(join(base, d, 'web/dist/a.js'), text); };
    mk('a', 'x'); mk('b', 'x'); mk('c', 'y');
    assert.equal(sameRoots(join(base, 'a'), join(base, 'b')), true);
    assert.equal(sameRoots(join(base, 'a'), join(base, 'c')), false);
    writeFileSync(join(base, 'b', 'web/dist/extra.js'), 'x');
    assert.equal(sameRoots(join(base, 'a'), join(base, 'b')), false);
  } finally {
    rmSync(base, { recursive: true, force: true });
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
