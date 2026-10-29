// Starts one demo app in its own empty data folder, hands back the address and the
// session token, and stops it again. No fixed port (--port 0), no real time: the
// demo clock is pinned by UNRAVIS_DEMO_NOW.
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

export const DEMO_NOW = '2026-10-20T09:00:00Z';

export const DEFAULT_EXE = 'D:\\Projects\\api-audit-explorer\\.dev\\run\\scn\\unravis-demo.exe';

export interface Demo {
  base: string;
  token: string;
  /** The one-time launch link, already used once by the launcher. */
  link: string;
  dataDir: string;
  configDir: string;
  /** Scratch folder for things the test writes (archive folder, downloads). Inside dataDir. */
  scratch: string;
  /** Everything the demo has printed so far (stdout and stderr). */
  log(): string;
  stop(): Promise<void>;
}

export interface StartOptions {
  /** Seed the demo cases (default true). With false the demo has no case store at all. */
  cases?: boolean;
}

export function exePath(): string {
  const p = resolve(process.env.UNRAVIS_EXE || DEFAULT_EXE);
  if (!existsSync(p)) {
    throw new Error('The demo exe is missing: ' + p + '. Set UNRAVIS_EXE or pass -Exe to run-e2e.ps1.');
  }
  return p;
}

const live = new Set<ChildProcess>();

function killTree(child: ChildProcess): void {
  if (child.pid === undefined || child.exitCode !== null) return;
  try {
    execFileSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
  } catch {
    try {
      child.kill();
    } catch {
      // already gone
    }
  }
}

// If node itself ends (Ctrl+C, a crash in the runner), the demos must not stay behind.
process.on('exit', () => {
  for (const c of live) killTree(c);
});

function removeFolder(dir: string): void {
  // Windows can hold a file for a moment after the process ends: a few tries, no sleep needed
  // because rmSync retries on its own.
  rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
}

export async function startDemo(opts: StartOptions = {}): Promise<Demo> {
  const exe = exePath();
  const dataDir = mkdtempSync(join(tmpdir(), 'unravis-e2e-'));
  const configDir = join(dataDir, 'config');
  const scratch = join(dataDir, 'scratch');
  mkdirSync(configDir, { recursive: true });
  mkdirSync(scratch, { recursive: true });

  const env: NodeJS.ProcessEnv = { ...process.env, AUDIT_CONFIG_DIR: configDir, UNRAVIS_DEMO_NOW: DEMO_NOW };
  if (opts.cases === false) delete env.UNRAVIS_DEMO_CASES;
  else env.UNRAVIS_DEMO_CASES = '1';

  const child = spawn(exe, ['--demo', '--no-browser', '--port', '0'], { env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  live.add(child);

  let log = '';
  let exited: number | null = null;
  const onData = (b: Buffer) => {
    log += b.toString('utf8');
  };
  child.stdout!.on('data', onData);
  child.stderr!.on('data', onData);
  child.on('exit', (code) => {
    exited = code ?? -1;
  });

  const stop = async (): Promise<void> => {
    killTree(child);
    live.delete(child);
    if (child.exitCode === null) {
      await new Promise<void>((done) => {
        child.once('exit', () => done());
        // killTree already ran; if the exit event was missed, do not hang.
        if (child.exitCode !== null) done();
      });
    }
    removeFolder(dataDir);
  };

  // The link is printed once the server listens (it goes to stderr in this build; both streams are read).
  // Event driven: resolves on the first chunk that holds the link, fails on exit or after 30 s.
  const linkRe = /(http:\/\/127\.0\.0\.1:\d+\/launch\?c=[A-Za-z0-9_-]+)/;
  const link = await new Promise<string>((resolveLink, rejectLink) => {
    const timer = setTimeout(() => finish(new Error('The demo printed no launch link in 30 seconds. Output:\n' + log.slice(0, 2000))), 30_000);
    const check = () => {
      const m = linkRe.exec(log);
      if (m) finish(null, m[1]);
    };
    const onExit = () => finish(new Error('The demo ended before it printed a launch link (exit ' + exited + '). Output:\n' + log.slice(0, 2000)));
    const finish = (err: Error | null, value = '') => {
      clearTimeout(timer);
      child.stdout!.off('data', check);
      child.stderr!.off('data', check);
      child.off('exit', onExit);
      if (err) rejectLink(err);
      else resolveLink(value);
    };
    child.stdout!.on('data', check);
    child.stderr!.on('data', check);
    child.on('exit', onExit);
    check();
  }).catch(async (err) => {
    await stop();
    throw err;
  });

  // The link works once. Consume it here and keep the token.
  const res = await fetch(link, { redirect: 'manual' });
  const location = res.headers.get('location') ?? '';
  const token = /[#&]t=([^&]+)/.exec(location)?.[1];
  if (res.status < 300 || res.status >= 400 || !token) {
    await stop();
    throw new Error('The launch link did not hand over a session (status ' + res.status + ').');
  }
  return { base: new URL(link).origin, token, link, dataDir, configDir, scratch, log: () => log, stop };
}
