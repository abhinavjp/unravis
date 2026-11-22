// Starts the demo app (own empty data folder, fixed clock, demo cases) and, when it builds, the setup window in its capture mode
// (it fakes every change). The addresses go to the tests through the environment. Stops both at the end.
import { spawn, type ChildProcess } from 'node:child_process';
import { existsSync } from 'node:fs';
import { startDemo, DEMO_NOW, type Demo } from '../../e2e/support/launcher';

export { DEMO_NOW };

export default async function globalSetup(): Promise<() => Promise<void>> {
  let demo: Demo | null = null;
  let setup: ChildProcess | null = null;
  const stop = async () => {
    if (setup && setup.exitCode === null) setup.kill();
    if (demo) await demo.stop();
  };
  try {
    demo = await startDemo({ cases: true });
    process.env.UNRAVIS_BASE_URL = demo.base;
    process.env.UNRAVIS_TOKEN = demo.token;
    process.env.UNRAVIS_DEMO_NOW = DEMO_NOW;

    const setupExe = process.env.UNRAVIS_SETUP_EXE;
    if (setupExe && existsSync(setupExe)) {
      setup = spawn(setupExe, ['--capture', '--no-browser'], { stdio: ['ignore', 'pipe', 'pipe'] });
      let text = '';
      const found = await new Promise<RegExpExecArray | null>((resolve) => {
        const timer = setTimeout(() => resolve(null), 30_000);
        const on = (b: Buffer) => {
          text += b.toString('utf8');
          const m = /listening at (http:\/\/127\.0\.0\.1:\d+)\/#k=([A-Za-z0-9_-]+)/.exec(text);
          if (m) {
            clearTimeout(timer);
            resolve(m);
          }
        };
        setup!.stdout!.on('data', on);
        setup!.stderr!.on('data', on);
        setup!.on('exit', () => resolve(null));
      });
      if (found) {
        process.env.UNRAVIS_SETUP_URL = found[1];
        process.env.UNRAVIS_SETUP_KEY = found[2];
      }
    }
  } catch (err) {
    await stop();
    throw err;
  }
  return stop;
}
