'use strict';
// BL-031: self-test of scripts/check-dist.cjs. Builds throwaway git repositories under the temp folder.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { ROOTS, checkDist, blobId } = require('../scripts/check-dist.cjs');

const SCRIPT = path.join(__dirname, '..', 'scripts', 'check-dist.cjs');
const REPO_ROOT = path.join(__dirname, '..', '..');
const ENV = { ...process.env, GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: os.devNull, GITHUB_ACTIONS: '' };

function git(cwd, ...args) {
  const r = spawnSync('git', args, { cwd, env: ENV, encoding: 'utf8' });
  assert.equal(r.status, 0, 'git ' + args.join(' ') + ': ' + r.stderr);
  return r.stdout;
}

const made = [];
// Writes the three roots (each with index.html and assets/a.js), commits them with autocrlf off.
function makeRepo(opts = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'checkdist-'));
  made.push(dir);
  git(dir, 'init', '-q');
  git(dir, 'config', 'user.email', 't@example.invalid');
  git(dir, 'config', 'user.name', 't');
  git(dir, 'config', 'core.autocrlf', 'false');
  for (const root of ROOTS) {
    if (opts.skipIndex === root) {
      write(dir, root + '/assets/a.js', 'a(' + root + ')\n');
      continue;
    }
    write(dir, root + '/index.html', '<html>' + root + '</html>\nline2\n');
    write(dir, root + '/assets/a.js', 'a(' + root + ')\n');
  }
  if (opts.gitignore) write(dir, '.gitignore', opts.gitignore);
  git(dir, 'add', '-A');
  git(dir, 'commit', '-q', '-m', 'fixture');
  return dir;
}

function write(dir, rel, content) {
  const p = path.join(dir, rel);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, content);
}

test.after(() => {
  for (const d of made) fs.rmSync(d, { recursive: true, force: true });
});

test('clean fixture passes', () => {
  const dir = makeRepo();
  const r = checkDist({ repo: dir });
  assert.equal(r.ok, true);
  for (const k of ['missing', 'extra', 'changed', 'eolOnly', 'notFile', 'noIndex']) assert.deepEqual(r[k], [], k);
  assert.equal(r.total, 6);
});

test('one changed byte is found', () => {
  const dir = makeRepo();
  write(dir, 'web/dist/assets/a.js', 'b(web/dist)\n');
  const r = checkDist({ repo: dir });
  assert.deepEqual(r.changed, ['web/dist/assets/a.js']);
  assert.deepEqual(r.eolOnly, []);
  assert.equal(r.ok, false);
});

test('CRLF-only change is found even when git hides it', () => {
  const dir = makeRepo();
  git(dir, 'config', 'core.autocrlf', 'true');
  write(dir, 'web/dist/index.html', '<html>web/dist</html>\r\nline2\r\n');
  const quiet = spawnSync('git', ['diff', '--quiet'], { cwd: dir, env: ENV });
  assert.equal(quiet.status, 0, 'git diff --quiet should report clean, that is the problem this script solves');
  const r = checkDist({ repo: dir });
  assert.deepEqual(r.changed, ['web/dist/index.html']);
  assert.deepEqual(r.eolOnly, ['web/dist/index.html']);
  assert.equal(r.ok, false);
});

test('extra untracked file is found', () => {
  const dir = makeRepo();
  write(dir, 'cmd/unravis-lite/ui/extra.txt', 'x');
  assert.deepEqual(checkDist({ repo: dir }).extra, ['cmd/unravis-lite/ui/extra.txt']);
});

test('extra ignored file is found', () => {
  const dir = makeRepo({ gitignore: '*.txt\n' });
  write(dir, 'web/dist/x.txt', 'x');
  const r = checkDist({ repo: dir });
  assert.deepEqual(r.extra, ['web/dist/x.txt']);
  assert.equal(r.ok, false);
});

test('missing file is found', () => {
  const dir = makeRepo();
  fs.rmSync(path.join(dir, 'cmd/unravis-setup/ui/assets/a.js'));
  const r = checkDist({ repo: dir });
  assert.deepEqual(r.missing, ['cmd/unravis-setup/ui/assets/a.js']);
  assert.equal(r.ok, false);
});

test('empty root is found', () => {
  const dir = makeRepo();
  fs.rmSync(path.join(dir, 'cmd/unravis-lite/ui'), { recursive: true });
  const r = checkDist({ repo: dir });
  assert.deepEqual(r.missing, ['cmd/unravis-lite/ui/assets/a.js', 'cmd/unravis-lite/ui/index.html']);
  assert.equal(r.ok, false);
});

test('root without a committed index.html is found', () => {
  const dir = makeRepo({ skipIndex: 'web/dist' });
  const r = checkDist({ repo: dir });
  assert.deepEqual(r.noIndex, ['web/dist']);
  assert.equal(r.ok, false);
});

test('nested extra file is found', () => {
  const dir = makeRepo();
  write(dir, 'web/dist/assets/deep/more/x.js', 'x');
  assert.deepEqual(checkDist({ repo: dir }).extra, ['web/dist/assets/deep/more/x.js']);
});

test('symlink is refused', (t) => {
  const dir = makeRepo();
  try {
    fs.symlinkSync('index.html', path.join(dir, 'web/dist/link.html'));
  } catch (e) {
    t.skip('this system does not allow a symlink: ' + e.code);
    return;
  }
  const r = checkDist({ repo: dir });
  assert.deepEqual(r.notFile, ['web/dist/link.html']);
  assert.equal(r.ok, false);
});

test('blob id equals git hash-object', () => {
  const dir = makeRepo();
  const cases = {
    empty: Buffer.alloc(0),
    lf: Buffer.from('a\nb\n'),
    crlf: Buffer.from('a\r\nb\r\n'),
    binary: Buffer.from([0x00, 0xff, 0x0d, 0x0a]),
  };
  for (const [name, bytes] of Object.entries(cases)) {
    const f = path.join(dir, 'blob-' + name);
    fs.writeFileSync(f, bytes);
    const want = git(dir, 'hash-object', '--no-filters', f).trim();
    assert.equal(blobId(bytes), want, name);
  }
});

test('command line exit codes', () => {
  const run = (repo) => spawnSync(process.execPath, [SCRIPT, '--repo', repo], { env: ENV, encoding: 'utf8' });
  const clean = makeRepo();
  const ok = run(clean);
  assert.equal(ok.status, 0, ok.stderr);
  assert.match(ok.stdout, /matches the commit \(6 files\)/);

  const dirty = makeRepo();
  write(dirty, 'web/dist/assets/a.js', 'changed\n');
  const bad = run(dirty);
  assert.equal(bad.status, 1);
  assert.ok(bad.stderr.includes('npm run build'));
  for (const root of ROOTS) assert.ok(bad.stderr.includes(root), root);
  assert.ok(!bad.stderr.includes('::error::'), 'no annotation outside GitHub Actions');
  const annotated = spawnSync(process.execPath, [SCRIPT, '--repo', dirty], { env: { ...ENV, GITHUB_ACTIONS: 'true' }, encoding: 'utf8' });
  assert.ok(annotated.stderr.startsWith('::error::'));

  const notRepo = fs.mkdtempSync(path.join(os.tmpdir(), 'checkdist-norepo-'));
  made.push(notRepo);
  const none = run(notRepo);
  assert.equal(none.status, 2);
  assert.match(none.stderr, /Cannot check/);
});

test('control characters in names are not printed raw', (t) => {
  const dir = makeRepo();
  try {
    write(dir, 'web/dist/bad\n::error::x.js', 'x');
  } catch (e) {
    t.skip('this system does not allow that file name: ' + e.code);
    return;
  }
  const r = spawnSync(process.execPath, [SCRIPT, '--repo', dir], { env: ENV, encoding: 'utf8' });
  assert.equal(r.status, 1);
  assert.ok(!r.stderr.split('\n').some((l) => l.startsWith('::error::x')), r.stderr);
});

test('the three roots match the build', () => {
  const pkg = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'package.json'), 'utf8'));
  const calls = pkg.scripts.build.split('&&').map((s) => s.trim());
  assert.equal(calls.length, 3, 'a fourth build needs a new root in scripts/check-dist.cjs');
  for (const c of calls) assert.match(c, /^vite build\b/);
  const outDirOf = (cfgName) => {
    const text = fs.readFileSync(path.join(__dirname, '..', cfgName), 'utf8');
    const m = text.match(/outDir\s*:\s*([^\n]+)/);
    assert.ok(m, cfgName + ' has no outDir');
    return m[1];
  };
  const cfgs = calls.map((c) => (c.match(/-c\s+(\S+)/) || [])[1]);
  assert.deepEqual(cfgs.filter(Boolean).sort(), ['vite.lite.config.ts', 'vite.setup.config.ts']);
  assert.equal(cfgs.filter((c) => !c).length, 1, 'exactly one build uses the default config (web/dist)');
  assert.match(outDirOf('vite.setup.config.ts'), /cmd[\\/'"]*.*unravis-setup.*ui/);
  assert.match(outDirOf('vite.lite.config.ts'), /cmd[\\/'"]*.*unravis-lite.*ui/);
  const main = fs.readFileSync(path.join(__dirname, '..', 'vite.config.ts'), 'utf8');
  assert.ok(!/outDir/.test(main), 'the main build must write the default web/dist');
  assert.deepEqual([...ROOTS].sort(), ['cmd/unravis-lite/ui', 'cmd/unravis-setup/ui', 'web/dist']);
});

test('gitattributes keeps the web inputs LF', () => {
  const text = fs.readFileSync(path.join(REPO_ROOT, '.gitattributes'), 'utf8').replace(/\r\n/g, '\n');
  const lines = text.split('\n').map((l) => l.trim());
  for (const rule of [
    '/web/index.html text eol=lf',
    '/web/setup/index.html text eol=lf',
    '/web/lite/index.html text eol=lf',
    '/docs/user/*.md text eol=lf',
    '/web/dist/** -text',
    '/cmd/unravis-setup/ui/** -text',
    '/cmd/unravis-lite/ui/** -text',
  ]) assert.ok(lines.includes(rule), 'missing: ' + rule);
  assert.ok(!lines.some((l) => /^\/web\/\*\*\s+text/.test(l)), 'a blanket /web/** text rule would override the binary rules');
});
