// Starts the real (non-demo) app on a COPY of a prepared config folder (for example the perfdata cfg),
// hands back the address and the session token, and stops it again. The copy is deleted on stop, so the
// prepared dataset is never changed. AUDIT_CONFIG_DIR is set, so the app uses per-user mode with the
// cases under <config>/cases and never touches the real Windows profile or the shared ProgramData folder.
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { cpSync, existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { DEFAULT_EXE } from './launcher';

export interface Live {
  base: string;
  token: string;
  /** The one-time launch link, already used once by the launcher. */
  link: string;
  /** The per-run copy of the config folder (the app's AUDIT_CONFIG_DIR). */
  configDir: string;
  /** Everything the app has printed so far (stdout and stderr). */
  log(): string;
  stop(): Promise<void>;
}

export interface LiveOptions {
  /** The prepared config folder to copy (holds cases/). It is not changed. */
  configDir: string;
  /** The app exe. Default: UNRAVIS_LIVE_EXE, else unravis.exe next to the demo exe. */
  exe?: string;
}

export function liveExePath(exe?: string): string {
  const p = resolve(exe || process.env.UNRAVIS_LIVE_EXE || join(dirname(DEFAULT_EXE), 'unravis.exe'));
  if (!existsSync(p)) {
    throw new Error('The app exe is missing: ' + p + '. Set UNRAVIS_LIVE_EXE.');
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

// If node itself ends, the apps must not stay behind.
process.on('exit', () => {
  for (const c of live) killTree(c);
});

function removeFolder(dir: string): void {
  rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
}

export async function startLive(opts: LiveOptions): Promise<Live> {
  const exe = liveExePath(opts.exe);
  const source = resolve(opts.configDir);
  if (!existsSync(join(source, 'cases'))) {
    throw new Error('The config folder has no cases folder: ' + source);
  }
  const root = mkdtempSync(join(tmpdir(), 'unravis-perf-'));
  const configDir = join(root, 'config');
  cpSync(source, configDir, { recursive: true });

  const env: NodeJS.ProcessEnv = { ...process.env, AUDIT_CONFIG_DIR: configDir };
  delete env.UNRAVIS_DATA_DIR;
  delete env.UNRAVIS_DEMO_CASES;

  const child = spawn(exe, ['--no-browser', '--port', '0'], { env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
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
        if (child.exitCode !== null) done();
      });
    }
    removeFolder(root);
  };

  const linkRe = /(http:\/\/127\.0\.0\.1:\d+\/launch\?c=[A-Za-z0-9_-]+)/;
  const link = await new Promise<string>((resolveLink, rejectLink) => {
    const timer = setTimeout(() => finish(new Error('The app printed no launch link in 60 seconds. Output:\n' + log.slice(0, 2000))), 60_000);
    const check = () => {
      const m = linkRe.exec(log);
      if (m) finish(null, m[1]);
    };
    const onExit = () => finish(new Error('The app ended before it printed a launch link (exit ' + exited + '). Output:\n' + log.slice(0, 2000)));
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

  const res = await fetch(link, { redirect: 'manual' });
  const location = res.headers.get('location') ?? '';
  const token = /[#&]t=([^&]+)/.exec(location)?.[1];
  if (res.status < 300 || res.status >= 400 || !token) {
    await stop();
    throw new Error('The launch link did not hand over a session (status ' + res.status + ').');
  }
  return { base: new URL(link).origin, token, link, configDir, log: () => log, stop };
}
