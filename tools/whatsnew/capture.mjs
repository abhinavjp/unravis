// Takes the pictures for What's new from the real app: starts the demo build (unravis.exe --demo), opens each screen a fragment asks
// for in Chromium, and saves a JPEG into web/public/whatsnew/<rcNN>/. The demo has a fixed clock and fixed cases, like tools/screens.
//
//   node tools/whatsnew/capture.mjs --version 0.1.0-rc.64 --exe dist/unravis.exe [--before-exe <previous release unravis.exe>] [--only <fragment>]
//
// "before" pictures come from the previous release's program (--before-exe): they show what people had until now. Without it a fragment
// that asks for a "before" stops the run, so a before and after can never be faked.
// Needs the tools in tools/screens installed once: npm ci, then npx playwright install chromium.
// Exit 0 ok, 1 a fragment or a step is wrong, 2 cannot run.

import { spawn, spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { loadFragments, planShots } from './lib.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const DEMO_NOW = '2026-10-20T09:00:00Z';
const VIEWPORT = { width: 1440, height: 900 };
const NARROW_AT = 1100;

class Stop extends Error { constructor(code, message) { super(message); this.code = code; } }

function parse(argv) {
  const o = { version: '', exe: '', beforeExe: '', only: '', repo: resolve(here, '..', '..'), fragments: '', out: '' };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const val = () => { if (i + 1 >= argv.length) throw new Stop(2, a + ' needs a value.'); return argv[++i]; };
    if (a === '--version') o.version = val().replace(/^v/i, '');
    else if (a === '--exe') o.exe = resolve(val());
    else if (a === '--before-exe') o.beforeExe = resolve(val());
    else if (a === '--only') o.only = val();
    else if (a === '--repo') o.repo = resolve(val());
    else if (a === '--fragments') o.fragments = resolve(val());
    else if (a === '--out') o.out = resolve(val());
    else throw new Stop(2, 'Unknown option ' + a + '.');
  }
  if (!o.version) throw new Stop(2, '--version is required.');
  if (!o.exe) throw new Stop(2, '--exe is required (dist/unravis.exe after .\\build.ps1).');
  o.fragments ||= join(o.repo, 'docs', 'whatsnew', 'fragments');
  o.out ||= join(o.repo, 'web', 'public');
  return o;
}

// The same step language as tools/screens/capture.spec.ts (a test in tests/ checks that both know the same step names).
async function runSteps(page, steps, ctx) {
  const narrow = (page.viewportSize()?.width ?? VIEWPORT.width) < NARROW_AT;
  for (const step of steps) {
    if (step.when && (step.when === 'narrow') !== narrow) continue;
    await runStep(page, step, ctx);
  }
}
async function runStep(page, step, ctx) {
  if ('aside' in step) {
    const other = await page.context().newPage();
    try {
      await other.setViewportSize(page.viewportSize() ?? VIEWPORT);
      await other.clock.setFixedTime(new Date(DEMO_NOW));
      await runSteps(other, step.aside, ctx);
    } finally { await other.close(); }
  } else if ('goto' in step) {
    const hash = step.goto.startsWith('#') ? step.goto : '#t=' + ctx.token;
    await page.goto(ctx.base + '/' + hash);
  } else if ('click' in step) await page.locator(step.click).first().click();
  else if ('fill' in step) await page.locator(step.fill[0]).first().fill(step.fill[1]);
  else if ('select' in step) await page.locator(step.select[0]).first().selectOption(step.select[1]);
  else if ('waitFor' in step) await page.locator(step.waitFor).first().waitFor({ state: 'visible' });
  else if ('scrollTo' in step) await page.locator(step.scrollTo).first().scrollIntoViewIfNeeded();
  else if ('theme' in step) await page.evaluate((theme) => document.documentElement.setAttribute('data-theme', theme), step.theme);
  else throw new Stop(1, 'A step has no known action: ' + JSON.stringify(step));
}

function stepsOf(spec, screens) {
  if (spec.steps) return { steps: spec.steps, after: [] };
  const screen = screens.get(spec.screen);
  if (!screen) throw new Stop(1, 'There is no screen "' + spec.screen + '" in tools/screens/screens.json.');
  if (screen.retired) throw new Stop(1, 'Screen "' + spec.screen + '" is retired (' + screen.retired + ').');
  if (screen.app === 'setup') throw new Stop(1, 'Screen "' + spec.screen + '" is in the setup window; What\'s new shows the main app.');
  return { steps: screen.steps, after: screen.after ?? [] };
}

/** Starts the demo and gives back { base, token, stop() }. The demo runs in a throw-away config folder. */
async function startDemo(exe) {
  if (!existsSync(exe)) throw new Stop(2, 'The program is missing: ' + exe + '. Build first: .\\build.ps1');
  const work = mkdtempSync(join(tmpdir(), 'unravis-whatsnew-'));
  const env = { ...process.env, AUDIT_CONFIG_DIR: join(work, 'config'), UNRAVIS_DEMO_NOW: DEMO_NOW, UNRAVIS_DEMO_CASES: '1' };
  const child = spawn(exe, ['--demo', '--no-browser', '--port', '0'], { env, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  let log = '';
  child.stdout.on('data', (d) => { log += d; });
  child.stderr.on('data', (d) => { log += d; });
  const stop = () => {
    if (child.exitCode === null) {
      if (process.platform === 'win32') spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
      else child.kill('SIGKILL');
    }
    try { rmSync(work, { recursive: true, force: true }); } catch { /* a leftover temp folder is harmless */ }
  };
  const deadline = Date.now() + 45_000;
  let link = '';
  while (Date.now() < deadline && !link) {
    await new Promise((r) => setTimeout(r, 200));
    const m = /(http:\/\/127\.0\.0\.1:\d+\/launch\?c=[A-Za-z0-9_-]+)/.exec(log);
    if (m && /listening at/.test(log)) link = m[1];
    if (child.exitCode !== null) break;
  }
  if (!link) { stop(); throw new Stop(2, 'The demo did not start. Its log says:\n' + log.split(/\r?\n/).slice(-15).join('\n')); }
  const res = await fetch(link, { redirect: 'manual' });
  const token = /[#&]t=([^&]+)/.exec(res.headers.get('location') ?? '')?.[1];
  if (!token) { stop(); throw new Stop(2, 'The launch link did not hand over a session (status ' + res.status + ').'); }
  return { base: new URL(link).origin, token, stop };
}

async function shoot(chromium, demo, shots, screens, repo, out) {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const shot of shots) {
      const { steps, after } = stepsOf(shot.spec, screens);
      const context = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: 1, timezoneId: 'UTC', locale: 'en-GB', reducedMotion: 'reduce', colorScheme: shot.spec.theme ?? 'dark' });
      const page = await context.newPage();
      const ctx = { base: demo.base, token: demo.token };
      try {
        await page.clock.setFixedTime(new Date(DEMO_NOW));
        await runSteps(page, steps, ctx);
        await page.evaluate(() => document.fonts.ready);
        await page.mouse.move(0, 0);
        await page.waitForLoadState('networkidle', { timeout: 3000 }).catch(() => undefined);
        if (shot.spec.wait) await page.waitForTimeout(shot.spec.wait);
        const target = shot.spec.clip ? page.locator(shot.spec.clip).first() : page;
        const bytes = await target.screenshot({ type: 'jpeg', quality: 85, animations: 'disabled', caret: 'hide' });
        const file = join(out, shot.path.replace(/^\//, ''));
        mkdirSync(dirname(file), { recursive: true });
        writeFileSync(file, bytes);
        console.log('  ' + shot.path + ' (' + Math.round(bytes.length / 1024) + ' KB)');
        if (after.length) await runSteps(page, after, ctx);
      } catch (e) {
        if (e instanceof Stop) throw e;
        throw new Stop(1, 'Could not take ' + shot.path + ' (' + shot.role + ' of fragment ' + shot.fragment + ', highlight ' + (shot.index + 1) + '): ' + String(e.message).split('\n')[0]);
      } finally { await context.close(); }
    }
  } finally { await browser.close(); }
}

async function main() {
  const o = parse(process.argv.slice(2));
  const screens = new Map(JSON.parse(readFileSync(join(o.repo, 'tools', 'screens', 'screens.json'), 'utf8')).screens.map((s) => [s.id, s]));
  const fragments = loadFragments(o.fragments, new Set(screens.keys())).filter((f) => !o.only || f.name === o.only);
  const { shots } = planShots(fragments, o.version);
  if (!shots.length) { console.log('No pictures to take: the fragments have no screenshot highlights.'); return; }
  const afters = shots.filter((s) => s.role === 'image');
  const befores = shots.filter((s) => s.role === 'before');
  if (befores.length && !o.beforeExe) throw new Stop(1, befores.length + ' picture(s) are "before" pictures, which come from the previous release. Pass --before-exe <previous unravis.exe> (Prepare-WhatsNew.ps1 builds it), or remove "before" from the fragment.');
  let chromium;
  try { chromium = createRequire(join(o.repo, 'tools', 'screens', 'package.json'))('@playwright/test').chromium; } catch { throw new Stop(2, 'The screenshot tools are not installed. In tools\\screens run: npm ci, then npx playwright install chromium'); }
  for (const [exe, group, label] of [[o.exe, afters, 'this version'], [o.beforeExe, befores, 'the previous version']]) {
    if (!group.length) continue;
    console.log('Taking ' + group.length + ' picture(s) from ' + label + ' (' + exe + ')');
    const demo = await startDemo(exe);
    try { await shoot(chromium, demo, group, screens, o.repo, o.out); } finally { demo.stop(); }
  }
}

main().catch((e) => { console.error(e.message); process.exit(e instanceof Stop ? e.code : 2); });
