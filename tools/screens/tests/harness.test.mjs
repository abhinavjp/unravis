// Tests for the screenshot tools themselves: docs-check.mjs, run-screens.ps1 and the clock guard.
// Run: npm test (in tools/screens). Needs Windows PowerShell. The tests that start the demo need
// dist\unravis.exe (.\build.ps1) and skip themselves, saying so, when it is missing.
import { spawn, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';

const here = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repo = resolve(here, '..', '..');
const script = join(here, 'run-screens.ps1');
const exe = join(repo, 'dist', 'unravis.exe');
const windows = process.platform === 'win32';
const haveExe = existsSync(exe) && existsSync(join(here, 'node_modules', '@playwright', 'test'));
const sleep = (ms) => new Promise((done) => setTimeout(done, ms));

function powershell(args, options = {}) {
  return spawnSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-File', ...args], { encoding: 'utf8', ...options });
}

function leftovers() {
  return readdirSync(tmpdir()).filter((name) => /^unravis-screens-[0-9a-f]{32}$/.test(name));
}

// A project folder with just enough in it for docs-check.mjs.
function project(files) {
  const root = mkdtempSync(join(tmpdir(), 'unravis-docscheck-'));
  mkdirSync(join(root, 'tools', 'screens'), { recursive: true });
  mkdirSync(join(root, 'docs', 'user'), { recursive: true });
  mkdirSync(join(root, 'docs', 'user', 'screenshots', 'P1'), { recursive: true });
  for (const [path, body] of Object.entries(files)) {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), body);
  }
  return root;
}
const screen = (id, extra = {}) => ({ id, phase: 'P1', doc: 'a.md', narrow: false, steps: [{ goto: '/' }], ...extra });
const screensFile = (...list) => JSON.stringify({ screens: list });
function docsCheck(root) {
  const run = spawnSync(process.execPath, [join(here, 'docs-check.mjs'), root], { encoding: 'utf8' });
  rmSync(root, { recursive: true, force: true });
  return { code: run.status, out: run.stdout + run.stderr };
}

test('docs-check passes on the real docs and screens', () => {
  const run = spawnSync(process.execPath, [join(here, 'docs-check.mjs')], { encoding: 'utf8' });
  assert.equal(run.status, 0, run.stdout + run.stderr);
});

test('docs-check passes a sound project', () => {
  const root = project({
    'tools/screens/screens.json': screensFile(screen('p1-home')),
    'docs/user/a.md': '# A\n\n![Home, dark](screenshots/P1/p1-home-dark.png)\n',
    'docs/user/screenshots/P1/p1-home-dark.png': 'png'
  });
  const { code, out } = docsCheck(root);
  assert.equal(code, 0, out);
});

test('docs-check fails on an image that does not exist', () => {
  const root = project({
    'tools/screens/screens.json': screensFile(screen('p1-home')),
    'docs/user/a.md': '# A\n\n![Home, dark](screenshots/P1/p1-home-dark.png)\n'
  });
  const { code, out } = docsCheck(root);
  assert.equal(code, 1);
  assert.match(out, /p1-home-dark\.png does not exist/);
});

test('docs-check fails on a screen its page has no picture of', () => {
  const root = project({
    'tools/screens/screens.json': screensFile(screen('p1-home')),
    'docs/user/a.md': '# A\n'
  });
  const { code, out } = docsCheck(root);
  assert.equal(code, 1);
  assert.match(out, /a\.md: it has no picture of the screen p1-home/);
});

test('docs-check fails on a page that only names the screen id', () => {
  const root = project({
    'tools/screens/screens.json': screensFile(screen('p1-home')),
    'docs/user/a.md': '# A\n\nScreen id p1-home.\n'
  });
  const { code, out } = docsCheck(root);
  assert.equal(code, 1);
  assert.match(out, /a\.md: it has no picture of the screen p1-home/);
  assert.match(out, /a\.md: it names the screen id p1-home in its text/);
});

test('docs-check lets a pending screen go without a picture, but not be named', () => {
  const pending = screen('p12-update-none', { phase: 'P12' });
  const quiet = project({
    'tools/screens/screens.json': screensFile(pending),
    'docs/user/a.md': '# A\n'
  });
  const first = docsCheck(quiet);
  assert.equal(first.code, 0, first.out);
  const named = project({
    'tools/screens/screens.json': screensFile(pending),
    'docs/user/a.md': '# A\n\nSee p12-update-none.\n'
  });
  const second = docsCheck(named);
  assert.equal(second.code, 1);
  assert.match(second.out, /a\.md: it names the screen id p12-update-none in its text/);
});

test('docs-check fails on a screenshot whose alt text has no theme', () => {
  const root = project({
    'tools/screens/screens.json': screensFile(screen('p1-home')),
    'docs/user/a.md': '# A\n\n![Home](screenshots/P1/p1-home-dark.png)\n',
    'docs/user/screenshots/P1/p1-home-dark.png': 'png'
  });
  const { code, out } = docsCheck(root);
  assert.equal(code, 1);
  assert.match(out, /p1-home-dark\.png needs "dark" or "light" in its alt text/);
});

test('docs-check fails on a screen id in the prose, except on screenshots.md and inside longer words', () => {
  const files = {
    'tools/screens/screens.json': screensFile(screen('p1-home')),
    'docs/user/screenshots/P1/p1-home-dark.png': 'png'
  };
  const bad = project({ ...files, 'docs/user/a.md': '# A\n\n![Home, dark](screenshots/P1/p1-home-dark.png)\n\nThe screen p1-home shows it.\n' });
  const first = docsCheck(bad);
  assert.equal(first.code, 1);
  assert.match(first.out, /a\.md: it names the screen id p1-home in its text/);
  const fine = project({
    ...files,
    'docs/user/a.md': '# A\n\n![Home, dark](screenshots/P1/p1-home-dark.png)\n\nThe files are named like p1-home-dark.png, and p1-homepage is another word.\n',
    'docs/user/screenshots.md': '# Screenshots\n\nThe id p1-home is the start of each file name.\n'
  });
  const second = docsCheck(fine);
  assert.equal(second.code, 0, second.out);
});

test('docs-check fails on words that claim a cause or a proof, except in code and when negated', () => {
  const fence = '`'.repeat(3);
  const files = {
    'tools/screens/screens.json': screensFile(screen('p1-home')),
    'docs/user/screenshots/P1/p1-home-dark.png': 'png'
  };
  const picture = '![Home, dark](screenshots/P1/p1-home-dark.png)\n\n';
  const bad = project({ ...files, 'docs/user/a.md': '# A\n\n' + picture + 'It was caused by a lock, blocked by a job, and due to load. This proves it.\n' });
  const first = docsCheck(bad);
  assert.equal(first.code, 1);
  for (const word of ['caused', 'blocked by', 'due to', 'proves']) assert.match(first.out, new RegExp(`a\\.md: the text says "${word}"`));
  const fine = project({
    ...files,
    'docs/user/a.md': '# A\n\n' + picture
      + 'Because it is slow, it is not proven. It never proves a cause, and it cannot prove one. It isn\'t proof.\n\n'
      + 'The word `caused` and the line below are code.\n\n' + fence + '\nit was caused by x, due to y\n' + fence + '\n'
  });
  const second = docsCheck(fine);
  assert.equal(second.code, 0, second.out);
});

// The Blackbox states table has to say what the app says (web/src/lib/copy/blackboxCopy.ts, stateNext).
const copyTs = "export const BLACKBOX_TEXT = {\n  stateNext: {\n    running: 'Nothing to do. It is recording.',\n    'running-late': 'No sample for more than three intervals.',\n    stopped: 'On this PC, press Start.'\n  } as Record<PageState, string>,\n  other: 'x'\n};\n";
const blackboxDoc = (stopped, stoppedText) => `# Blackbox\n\n![Home, dark](screenshots/P1/p1-home-dark.png)\n\n| State | Readiness | What it means and what to do |\n| --- | --- | --- |\n| Running | Ready | Nothing to do. It is recording. |\n| Running late | Partly ready | No sample for more than three intervals. |\n| Stopped | ${stopped} | ${stoppedText} |\n`;
const blackboxFiles = (doc) => ({
  'tools/screens/screens.json': screensFile(screen('p1-home', { doc: 'blackbox.md' })),
  'docs/user/blackbox.md': doc,
  'docs/user/screenshots/P1/p1-home-dark.png': 'png',
  'web/src/lib/copy/blackboxCopy.ts': copyTs
});

test('docs-check passes when the Blackbox table says what the app says', () => {
  const { code, out } = docsCheck(project(blackboxFiles(blackboxDoc('Partly ready', 'On this PC, press Start.'))));
  assert.equal(code, 0, out);
});

test('docs-check fails when the Blackbox table misses a sentence of the app', () => {
  const { code, out } = docsCheck(project(blackboxFiles(blackboxDoc('Partly ready', 'Start it from Services.'))));
  assert.equal(code, 1);
  assert.match(out, /blackbox\.md: the states table does not say "On this PC, press Start\." \(state stopped\)/);
});

test('docs-check fails when the Stopped row says Cannot test yet', () => {
  const { code, out } = docsCheck(project(blackboxFiles(blackboxDoc('Cannot test yet', 'On this PC, press Start.'))));
  assert.equal(code, 1);
  assert.match(out, /blackbox\.md: the Stopped row of the states table must say Partly ready/);
});

test('docs-check fails on a screen whose page does not exist', () => {
  const root = project({
    'tools/screens/screens.json': screensFile(screen('p1-home', { doc: 'gone.md' })),
    'docs/user/a.md': '# A\n'
  });
  const { code, out } = docsCheck(root);
  assert.equal(code, 1);
  assert.match(out, /gone\.md, which does not exist/);
});

test('docs-check fails on a link to a picture of a screen that is no longer captured', () => {
  const root = project({
    'tools/screens/screens.json': screensFile(screen('p1-home')),
    'docs/user/a.md': '![Home, dark](screenshots/P1/p1-home-dark.png)\n\n![Old, dark](screenshots/P1/p1-old-dark.png)\n',
    'docs/user/screenshots/P1/p1-home-dark.png': 'png',
    'docs/user/screenshots/P1/p1-old-dark.png': 'png'
  });
  const { code, out } = docsCheck(root);
  assert.equal(code, 1);
  assert.match(out, /p1-old.*no longer captures/);
});

test('docs-check fails when a screen is listed twice, badly named, or has no steps', () => {
  const root = project({
    'tools/screens/screens.json': screensFile(screen('p1-home'), screen('p1-home'), screen('Home'), screen('p2-x'), screen('p1-empty', { steps: [] })),
    'docs/user/a.md': 'p1-home p1-empty\n'
  });
  const { code, out } = docsCheck(root);
  assert.equal(code, 1);
  assert.match(out, /p1-home is listed twice/);
  assert.match(out, /"Home" must look like/);
  assert.match(out, /p2-x must start with its phase \(P1\)/);
  assert.match(out, /p1-empty has no steps/);
});

test('docs-check takes a part phase such as P14b, still tied to its phase', () => {
  const root = project({
    'tools/screens/screens.json': screensFile(screen('p14b-held', { phase: 'P14b' }), screen('p14-held', { phase: 'P14b' })),
    'docs/user/a.md': '![Held, dark](screenshots/P1/p14b-held-dark.png)\n\n![Held, dark](screenshots/P1/p14-held-dark.png)\n',
    'docs/user/screenshots/P1/p14b-held-dark.png': 'png',
    'docs/user/screenshots/P1/p14-held-dark.png': 'png'
  });
  const { code, out } = docsCheck(root);
  assert.equal(code, 1);
  assert.doesNotMatch(out, /p14b-held/);
  assert.match(out, /p14-held must start with its phase \(P14b\)/);
});

test('a bad UNRAVIS_DEMO_NOW stops the capture with a plain message', { skip: !haveExe && 'needs npm ci in tools/screens' }, () => {
  const run = spawnSync('npx.cmd', ['playwright', 'test', '--list'], {
    cwd: here, encoding: 'utf8', shell: true, env: { ...process.env, UNRAVIS_DEMO_NOW: 'yesterday' }
  });
  assert.notEqual(run.status, 0);
  assert.match(run.stdout + run.stderr, /UNRAVIS_DEMO_NOW must look like 2026-10-20T09:00:00Z/);
});

test('run-screens.ps1 without a build says to build first and leaves nothing behind', { skip: !windows && 'Windows only' }, () => {
  const root = mkdtempSync(join(tmpdir(), 'unravis-nobuild-'));
  try {
    mkdirSync(join(root, 'tools', 'screens'), { recursive: true });
    cpSync(script, join(root, 'tools', 'screens', 'run-screens.ps1'));
    const before = leftovers();
    const run = powershell([join(root, 'tools', 'screens', 'run-screens.ps1')]);
    assert.equal(run.status, 2, run.stdout + run.stderr);
    assert.match(run.stdout + run.stderr, /dist\\unravis\.exe is missing\. Build first/);
    assert.deepEqual(leftovers(), before);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('run-screens.ps1 refuses to start while another run holds the lock', { skip: !windows && 'Windows only' }, async () => {
  const holder = spawn('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command',
    "$m = New-Object System.Threading.Mutex($false, 'Local\\UnravisScreenshotRun'); [void]$m.WaitOne(0); Write-Host held; Start-Sleep -Seconds 60"],
  { stdio: ['ignore', 'pipe', 'ignore'] });
  try {
    await new Promise((done, fail) => {
      holder.stdout.on('data', (chunk) => { if (String(chunk).includes('held')) done(); });
      holder.on('error', fail);
      setTimeout(() => fail(new Error('the lock holder did not start')), 20000);
    });
    const run = powershell([script]);
    assert.equal(run.status, 4, run.stdout + run.stderr);
    assert.match(run.stdout + run.stderr, /Another screenshot run is already going/);
  } finally {
    holder.kill();
  }
});

test('run-screens.ps1 on a busy port says why, stops, and removes its temp folder', { skip: (!windows && 'Windows only') || (!haveExe && 'needs dist\\unravis.exe and npm ci') }, async () => {
  const listener = createServer();
  await new Promise((done) => listener.listen(0, '127.0.0.1', done));
  try {
    const before = leftovers();
    const run = powershell([script, '-Port', String(listener.address().port)]);
    assert.equal(run.status, 3, run.stdout + run.stderr);
    assert.match(run.stdout + run.stderr, /Unravis did not start/);
    assert.match(run.stdout + run.stderr, /already in use/);
    assert.deepEqual(leftovers(), before);
  } finally {
    listener.close();
  }
});

test('killing run-screens.ps1 outright ends the demo with it', { skip: (!windows && 'Windows only') || (!haveExe && 'needs dist\\unravis.exe and npm ci') }, async () => {
  const before = new Set(leftovers());
  const host = spawn('powershell.exe', ['-NoProfile', '-NonInteractive', '-File', script], { stdio: 'ignore' });
  let demoPid = 0;
  try {
    // The demo is the child of the script's PowerShell. Wait until it is there.
    for (let i = 0; i < 100 && !demoPid; i++) {
      await sleep(300);
      const found = spawnSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command',
        `(Get-CimInstance Win32_Process -Filter "Name='unravis.exe' and ParentProcessId=${host.pid}").ProcessId`], { encoding: 'utf8' });
      demoPid = Number(String(found.stdout).trim()) || 0;
    }
    assert.ok(demoPid, 'the demo did not start');
    spawnSync('taskkill.exe', ['/PID', String(host.pid), '/F']);
    let alive = true;
    for (let i = 0; i < 40 && alive; i++) {
      await sleep(250);
      alive = spawnSync('tasklist.exe', ['/FI', `PID eq ${demoPid}`, '/NH'], { encoding: 'utf8' }).stdout.includes('unravis.exe');
    }
    assert.equal(alive, false, 'the demo is still running after its harness was killed');
  } finally {
    host.kill();
    // The folder of a killed run stays until the next run sweeps it; this test removes its own.
    for (const name of leftovers()) if (!before.has(name)) rmSync(join(tmpdir(), name), { recursive: true, force: true });
    await sleep(3000); // let the capture it was running notice the demo is gone
  }
});

// build.ps1 in a throwaway folder with a fake npm.cmd and a fake go.cmd, so nothing real runs.
function buildFolder() {
  const root = mkdtempSync(join(tmpdir(), 'unravis-buildps1-'));
  mkdirSync(join(root, 'bin'), { recursive: true });
  mkdirSync(join(root, 'web'), { recursive: true });
  cpSync(join(repo, 'build.ps1'), join(root, 'build.ps1'));
  writeFileSync(join(root, 'bin', 'npm.cmd'), '@echo npm %* >> "%~dp0..\\calls.txt"\r\n@exit /b 0\r\n');
  writeFileSync(join(root, 'bin', 'go.cmd'), '@echo go %* >> "%~dp0..\\calls.txt"\r\n@exit /b 1\r\n');
  return root;
}
function buildPs1(root) {
  const env = { ...process.env, PATH: join(root, 'bin') + ';' + process.env.PATH };
  return powershell([join(root, 'build.ps1'), '-GoPath', join(root, 'bin', 'go.cmd'), '-Version', 'test'], { env });
}
const calls = (root) => (existsSync(join(root, 'calls.txt')) ? readFileSync(join(root, 'calls.txt'), 'utf8') : '');

test('build.ps1 refuses to run npm when web/node_modules is a junction, and leaves the target alone', { skip: !windows && 'Windows only' }, () => {
  const root = buildFolder();
  const shared = mkdtempSync(join(tmpdir(), 'unravis-sharednm-'));
  try {
    writeFileSync(join(shared, 'sentinel.txt'), 'still here');
    symlinkSync(shared, join(root, 'web', 'node_modules'), 'junction');
    const run = buildPs1(root);
    assert.notEqual(run.status, 0, run.stdout + run.stderr);
    assert.match(run.stdout + run.stderr, /web\\node_modules is a junction/);
    assert.equal(calls(root), '', 'npm or go ran: ' + calls(root));
    assert.equal(readFileSync(join(shared, 'sentinel.txt'), 'utf8'), 'still here');
  } finally {
    rmSync(join(root, 'web', 'node_modules'), { recursive: true, force: true }); // removes the link only
    rmSync(root, { recursive: true, force: true });
    rmSync(shared, { recursive: true, force: true });
  }
});

test('build.ps1 runs npm ci as before when web/node_modules is an ordinary folder', { skip: !windows && 'Windows only' }, () => {
  const root = buildFolder();
  try {
    mkdirSync(join(root, 'web', 'node_modules'));
    buildPs1(root);
    assert.match(calls(root), /npm ci *\r?\nnpm run check *\r?\nnpm run build *\r?\n/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

// ci.yml cannot run here, so these checks hold its screenshot rules in place: the compare may
// only block once baselines made on the runner are committed, and there is a way to make them.
const ci = readFileSync(join(repo, '.github', 'workflows', 'ci.yml'), 'utf8').replace(/\r\n/g, '\n');
const ciStep = (name) => {
  const start = ci.indexOf(`- name: ${name}`);
  assert.notEqual(start, -1, `ci.yml has no step "${name}"`);
  const next = ci.indexOf('\n      - name:', start + 1);
  return ci.slice(start, next === -1 ? undefined : next);
};

test('ci.yml has a manual run that regenerates the screenshot baselines and keeps them', () => {
  assert.match(ci, /\n  workflow_dispatch:\n    inputs:\n      regenerate_baselines:\n        description: .+\n        type: boolean\n        default: false\n/);
  const regen = ciStep('Screenshots - regenerate baselines on this runner');
  assert.match(regen, /if: \$\{\{ inputs\.regenerate_baselines == true \}\}/);
  assert.match(regen, /run-screens\.ps1 -Update/);
  assert.match(regen, /RUNNER-BASELINES\.txt/);
  const keep = ciStep('Screenshots - keep the regenerated baselines');
  assert.match(keep, /name: screenshot-baselines/);
  assert.match(keep, /path: docs\/user\/screenshots/);
});

test('ci.yml compare is non-blocking until baselines from the runner are committed, and skipped when regenerating', () => {
  const compare = ciStep('Screenshots - compare with the saved pictures');
  assert.match(compare, /continue-on-error: \$\{\{ steps\.reference\.outputs\.blocking != 'true' \}\}/);
  assert.match(compare, /if: \$\{\{ inputs\.regenerate_baselines != true \}\}/);
  assert.match(compare, /run-screens\.ps1\s*$/m);
  const reference = ciStep('Screenshots - are the saved pictures from this runner?');
  assert.match(reference, /docs\/user\/screenshots\/RUNNER-BASELINES\.txt/);
  assert.match(reference, />> \$env:GITHUB_OUTPUT/);
  // The differences are kept even when the compare only warns.
  assert.match(ciStep('Screenshots - keep the differences'), /steps\.compare\.outcome == 'failure'/);
});
