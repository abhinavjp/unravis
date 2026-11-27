// Starts the demo app (own empty data folder, fixed clock, demo cases) and, when they build, the other programs that have a page:
// the setup window in its capture mode (it fakes every change), the Releases dashboard (on made-up release data, offline) and
// the Lite stand-in (tools/uiaudit/litefixture). The addresses go to the tests through the environment. Stops all at the end.
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { startDemo, DEMO_NOW, type Demo } from '../../e2e/support/launcher';
import type { AppName } from './steps';

export { DEMO_NOW };

type Started = { child: ChildProcess; url: string; code: string };

/** Starts a program and waits for the line that says where it listens. Returns null when it did not come up. */
async function launch(exe: string, args: string[], find: RegExp, env: NodeJS.ProcessEnv = process.env): Promise<Started | null> {
  const child = spawn(exe, args, { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true, env });
  let text = '';
  const m = await new Promise<RegExpExecArray | null>((resolve) => {
    const timer = setTimeout(() => resolve(null), 30_000);
    const on = (b: Buffer) => {
      text += b.toString('utf8');
      const r = find.exec(text);
      if (r) {
        clearTimeout(timer);
        resolve(r);
      }
    };
    child.stdout!.on('data', on);
    child.stderr!.on('data', on);
    child.on('exit', () => resolve(null));
  });
  if (!m) {
    child.kill();
    return null;
  }
  return { child, url: m[1], code: m[2] };
}

// What the release manager would have written on a PC: its state file and its audit log, with times counted back from the demo clock.
function releaseFiles(dir: string): void {
  const now = new Date(DEMO_NOW).getTime();
  const at = (minutesAgo: number) => new Date(now - minutesAgo * 60_000).toISOString();
  const lines: object[] = [];
  const full = (v: string, start: number, run: string) => {
    const names = ['preflight-ok', 'tagged', 'built', 'signed', 'published', 'feed-updated', 'verified'];
    names.forEach((event, i) => lines.push({ ts: at(start - i * 9), event, version: v, ...(event === 'built' || event === 'verified' ? { run } : {}) }));
  };
  full('0.1.0', 60 * 24 * 6, '1100000001');
  full('0.1.0-rc.55', 60 * 24 * 3, '1100000055');
  full('0.1.0-rc.58', 60 * 5, '1100000058');
  lines.push({ ts: at(60 * 4), event: 'withdrawn', version: '0.1.0-rc.58', reason: 'A slow start on one machine.' });
  ['preflight-ok', 'tagged', 'built', 'signed', 'published', 'feed-updated'].forEach((event, i) => lines.push({ ts: at(60 * 20 - i * 9), event, version: '0.1.0-rc.56', ...(event === 'built' ? { run: '1100000056' } : {}) }));
  lines.push({ ts: at(60 * 19), event: 'verify-failed', version: '0.1.0-rc.56', reason: 'The clean machine did not reach the app in 5 minutes.', run: '1100000056' });
  ['preflight-ok', 'tagged', 'built'].forEach((event, i) => lines.push({ ts: at(60 * 9 - i * 9), event, version: '0.1.0-rc.57', ...(event === 'built' ? { run: '1100000057' } : {}) }));
  lines.push({ ts: at(60 * 8), event: 'stopped', version: '0.1.0-rc.57', stage: 'Sign', reason: 'The signing passphrase was not given in time.' });
  ['preflight-ok', 'tagged'].forEach((event, i) => lines.push({ ts: at(40 - i * 9), event, version: '0.1.0-rc.59' }));
  lines.sort((a, b) => String((a as { ts: string }).ts).localeCompare(String((b as { ts: string }).ts)));
  writeFileSync(join(dir, 'audit.jsonl'), lines.map((l) => JSON.stringify(l)).join('\n') + '\n');
  writeFileSync(join(dir, 'state.json'), JSON.stringify({ version: '0.1.0-rc.59', stage: 'Tag', status: 'running', updated: at(20) }));
}

// Made-up local runs for r8 and r9. Use only the audit's temporary CI store.
function ciFiles(dir: string): void {
  const now = Date.now();
  const at = (seconds: number) => new Date(now + seconds * 1000).toISOString();
  const stamp = (time: string) => time.slice(0, 19).replace(/-/g, '').replace('T', '-').replace(/:/g, '');
  const workflow = 'ci.yml', repo = 'abhinavjp/unravis', sha = 'a'.repeat(40);
  const failed = stamp(at(-500)) + '-push-audit-failed-ci';
  const running = stamp(at(-70)) + '-push-audit-running-ci';
  const req = 'q-' + stamp(at(-75)) + '-audit';
  const write = (file: string, text: string) => {
    const path = join(dir, file);
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, text);
  };
  const step = (index: number, name: string, conclusion: string, start: number, end: number) =>
    ({ index, name, conclusion, start: at(start), end: at(end) });
  const prepare = { key: 'prepare', id: 'prepare', label: 'Prepare the code', status: 'success' };
  const jobs = [
    { ...prepare, start: at(-500), end: at(-485), steps: [step(1, 'Read the commit', 'success', -500, -485)] },
    { key: 'tests', id: 'tests', label: 'Check the app', status: 'failure', start: at(-485), end: at(-400),
      steps: [step(1, 'Build the app', 'success', -485, -460), step(2, 'Run app tests', 'failure', -460, -400)] },
    { key: 'docs', id: 'docs', label: 'Check the docs', status: 'failure', start: at(-480), end: at(-440),
      steps: [step(1, 'Check links', 'failure', -480, -440)] },
  ];
  write(`runs/${failed}/run.json`, JSON.stringify({ workflow, conclusion: 'failure', start: at(-500), end: at(-400), jobs }));
  write('runs/index.jsonl', JSON.stringify({ run_id: failed, workflow, event: 'push', repo, head_ref: 'audit-failed',
    commit_sha: sha, conclusion: 'failure', reason: 'job tests, step 2 Run app tests', start: at(-500), end: at(-400) }) + '\n');
  write(`runs/${failed}/logs/tests/02-test.log`, 'Running app tests\n--- FAIL: TestOpenExample\nExpected the example to open\n');
  write(`runs/${failed}/logs/docs/01-links.log`, 'Checking links\n::error::Example page link is missing\n');
  write(`queue/running/${req}.json`, JSON.stringify({ id: req, kind: 'push', repo, ref: 'audit-running', sha, workflow,
    state: 'running', enqueuedAt: at(-75), claimedAt: at(-70), claimedBy: 'audit-machine' }));
  write('agents/audit-machine.json', JSON.stringify({ name: 'audit-machine', state: 'ready', slots: 2, lastSeen: at(0) }));
  write(`queue/progress/${req}.json`, JSON.stringify({ request: req, total: 4, updated: at(0), jobs: [
    { ...prepare, start: at(-70), end: at(-60) },
    { key: 'tests', label: 'Check the app', status: 'running', start: at(-60) },
    { key: 'docs', label: 'Check the docs', status: 'running', start: at(-55) },
  ] }));
  write(`runs/${running}/logs/tests/01-run-app-tests.log`, 'Building the app\nRunning app tests\n');
}

export default async function globalSetup(): Promise<() => Promise<void>> {
  let demo: Demo | null = null;
  const children: ChildProcess[] = [];
  const temp: string[] = [];
  const stop = async () => {
    for (const c of children) {
      if (c.exitCode !== null) continue;
      try {
        // Windows: end the whole tree, as the demo launcher does.
        execFileSync('taskkill', ['/PID', String(c.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
      } catch {
        c.kill();
      }
    }
    if (demo) await demo.stop();
    for (const d of temp) {
      try { rmSync(d, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 }); } catch { /* the system clears its temp folder */ }
    }
  };
  process.on('exit', () => { for (const c of children) if (c.exitCode === null) c.kill(); });
  try {
    demo = await startDemo({ cases: true });
    process.env.UNRAVIS_BASE_URL = demo.base;
    process.env.UNRAVIS_TOKEN = demo.token;
    process.env.UNRAVIS_DEMO_NOW = DEMO_NOW;

    const setupExe = process.env.UNRAVIS_SETUP_EXE;
    if (setupExe && existsSync(setupExe)) {
      const s = await launch(setupExe, ['--capture', '--no-browser'], /listening at (http:\/\/127\.0\.0\.1:\d+)\/#k=([A-Za-z0-9_-]+)/);
      if (s) {
        children.push(s.child);
        process.env.UNRAVIS_SETUP_URL = s.url;
        process.env.UNRAVIS_SETUP_KEY = s.code;
      }
    }

    const apps: Partial<Record<AppName, { url: string; code: string }>> = {};
    // Releases dashboard. It reads GitHub and the signed feed; a proxy that goes nowhere keeps it offline and the page steady.
    const releasesExe = process.env.UNRAVIS_RELEASES_EXE;
    if (releasesExe && existsSync(releasesExe)) {
      const offline = { ...process.env, HTTPS_PROXY: 'http://127.0.0.1:1', HTTP_PROXY: 'http://127.0.0.1:1', NO_PROXY: '' };
      const link = /(http:\/\/127\.0\.0\.1:\d+)\/\?t=([A-Za-z0-9_-]+)/;
      const withData = mkdtempSync(join(tmpdir(), 'uiaudit-releases-'));
      const noData = mkdtempSync(join(tmpdir(), 'uiaudit-releases-empty-'));
      temp.push(withData, noData);
      releaseFiles(withData);
      ciFiles(join(withData, 'ci'));
      for (const [name, dir] of [['releases', withData], ['releases-empty', join(noData, 'missing')]] as const) {
        const r = await launch(releasesExe, ['--data', dir, '--ci-store', join(dir, 'ci'), '--port', '0', '--print-link'], link, offline);
        if (r) {
          children.push(r.child);
          apps[name] = { url: r.url, code: r.code };
        }
      }
    }
    // Lite stand-in: one that is recording, one whose Blackbox has stopped.
    const liteExe = process.env.UNRAVIS_LITE_FIXTURE_EXE;
    if (liteExe && existsSync(liteExe)) {
      const ui = process.env.UNRAVIS_LITE_UI ?? '';
      for (const [name, extra] of [['lite', []], ['lite-stopped', ['--stopped']]] as const) {
        const l = await launch(liteExe, ['--ui', ui, '--now', DEMO_NOW, ...extra], /listening at (http:\/\/127\.0\.0\.1:\d+) token=([A-Za-z0-9_-]+)/);
        if (l) {
          children.push(l.child);
          apps[name] = { url: l.url, code: l.code };
        }
      }
    }
    process.env.UNRAVIS_APPS = JSON.stringify(apps);
  } catch (err) {
    await stop();
    throw err;
  }
  return stop;
}
