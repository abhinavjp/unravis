// Opens every screen (tools/screens/screens.json plus extra-screens.json) in the project's look, checks the layout
// in the page (lib/page-checks.js), saves a screenshot with the findings outlined and a data file next to it.
// The data files are turned into report/REPORT.md by run-audit.mjs. A finding does NOT fail a test here.
import { test, type Page } from '@playwright/test';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { runSteps, type Screen, type Env } from '../lib/steps';
import { ALLOWED } from '../../e2e/support/allow-list';

const root = join(__dirname, '..');
const reportDir = process.env.UIAUDIT_REPORT_DIR || join(root, 'report');
mkdirSync(join(reportDir, 'data'), { recursive: true });
mkdirSync(join(reportDir, 'shots'), { recursive: true });

const pageChecks = readFileSync(join(root, 'lib', 'page-checks.js'), 'utf8');
const base: Screen[] = JSON.parse(readFileSync(join(root, '..', 'screens', 'screens.json'), 'utf8')).screens;
const extra: Screen[] = JSON.parse(readFileSync(join(root, 'extra-screens.json'), 'utf8')).screens;
const only = (process.env.UIAUDIT_ONLY ?? '').split(',').map((s) => s.trim()).filter(Boolean);
const screens = [...base, ...extra].filter((s) => !only.length || only.includes(s.id));

const MAX_SHOT_HEIGHT = 6000;

interface Issue { check: string; severity: 'error' | 'warn' | 'info'; message: string; label: string; selector: string; rect: { x: number; y: number; w: number; h: number }; [k: string]: unknown }

// The setup window runs one fake run at a time and a screen can hold its run open; end it before the next screen starts.
async function freeSetup(env: Env): Promise<void> {
  const headers = { 'X-Setup-Key': env.setupKey };
  await fetch(env.setupUrl + '/api/cancel', { method: 'POST', headers }).catch(() => undefined);
  for (let i = 0; i < 50; i++) {
    const state = (await (await fetch(env.setupUrl + '/api/progress?since=0', { headers })).json().catch(() => ({ running: false }))) as { running: boolean };
    if (!state.running) return;
    await new Promise((done) => setTimeout(done, 100));
  }
}

async function settle(page: Page): Promise<void> {
  await page.evaluate(() => document.fonts.ready);
  await page.mouse.move(0, 0);
  await page.waitForLoadState('networkidle', { timeout: 2500 }).catch(() => undefined);
  await page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))));
}

// The app is a fixed-height shell with inner scrollers, so a normal full-page picture would show one window of it.
// Make the window as tall as the content (up to a limit) so every control is on screen at once, then look.
async function growToContent(page: Page, width: number): Promise<number> {
  for (let i = 0; i < 3; i++) {
    const need = await page.evaluate(() => {
      let extra = 0;
      document.querySelectorAll('*').forEach((el) => {
        const s = getComputedStyle(el);
        if (/(auto|scroll)/.test(s.overflowY) && el.scrollHeight > el.clientHeight + 1 && el.clientHeight > 150) {
          extra = Math.max(extra, el.scrollHeight - el.clientHeight);
        }
      });
      return Math.max(document.documentElement.scrollHeight - innerHeight, 0) + extra;
    });
    if (need < 2) break;
    const h = Math.min((page.viewportSize()?.height ?? 900) + need, MAX_SHOT_HEIGHT);
    await page.setViewportSize({ width, height: Math.ceil(h) });
    await settle(page);
    if (h >= MAX_SHOT_HEIGHT) break;
  }
  return page.viewportSize()!.height;
}

for (const screen of screens) {
  test(screen.id, async ({ page }, info) => {
    const variant = info.project.name; // desktop-dark, phone-light, ...
    const [size, scheme] = variant.split('-');
    const width = page.viewportSize()!.width;
    const baseHeight = page.viewportSize()!.height;
    const env: Env = {
      base: process.env.UNRAVIS_BASE_URL!,
      token: process.env.UNRAVIS_TOKEN!,
      setupUrl: process.env.UNRAVIS_SETUP_URL ?? '',
      setupKey: process.env.UNRAVIS_SETUP_KEY ?? '',
      demoNow: new Date(process.env.UNRAVIS_DEMO_NOW ?? '2026-10-20T09:00:00Z')
    };
    const out = join(reportDir, 'data', screen.id + '__' + variant + '.json');
    const shot = 'shots/' + screen.id + '__' + variant + '.png';
    const write = (o: object) => writeFileSync(out, JSON.stringify({ id: screen.id, phase: screen.phase, note: screen.note ?? '', variant, size, scheme, ...o }, null, 1));

    if (screen.retired) return write({ status: 'skipped', reason: 'retired: ' + screen.retired });
    if (screen.app === 'setup' && !env.setupUrl) return write({ status: 'skipped', reason: 'the setup window (capture build) was not started on this run' });

    if (screen.app === 'setup') await freeSetup(env);

    const browserIssues: Issue[] = [];
    const rel = (u: string) => { try { return new URL(u).pathname; } catch { return u; } };
    const note = (check: string, severity: Issue['severity'], message: string, label: string) => {
      if (ALLOWED.some((a) => a.match.test(check + ': ' + message))) return;
      browserIssues.push({ check, severity, message, label, selector: '', rect: { x: 0, y: 0, w: 0, h: 0 } });
    };
    // The browser prints a console error for every 4xx answer, even when the app expected it (a stale tab, a locked case).
    // Those are listed as notes through the response handler below; a 5xx, a script error or a failed request still is an error.
    page.on('console', (m) => {
      if (m.type() !== 'error') return;
      if (/Failed to load resource: the server responded with a status of 4\d\d/.test(m.text())) return;
      note('console-error', 'error', m.text().slice(0, 300), rel(m.location().url));
    });
    page.on('pageerror', (e) => note('page-error', 'error', String(e.message).slice(0, 300), ''));
    page.on('requestfailed', (r) => { const why = r.failure()?.errorText ?? ''; if (!/ERR_ABORTED/.test(why)) note('request-failed', 'error', why + ' ' + rel(r.url()), rel(r.url())); });
    page.on('response', (r) => { if (r.status() >= 400) note('http-error', r.status() >= 500 ? 'error' : 'info', 'HTTP ' + r.status() + ' ' + r.request().method() + ' ' + rel(r.url()), r.request().method() + ' ' + rel(r.url())); });

    await page.addInitScript((choice) => {
      try {
        localStorage.setItem('unravis.theme', choice);
        if (localStorage.getItem('unravis.timeZone') === null) localStorage.setItem('unravis.timeZone', 'utc');
      } catch { /* blocked storage: the system colour scheme still applies */ }
    }, scheme);
    await page.clock.setFixedTime(env.demoNow);

    try {
      await runSteps(page, screen.steps, env, screen.app);
    } catch (err) {
      const msg = String((err as Error).message).split('\n').slice(0, 3).join(' ').slice(0, 400);
      return write({ status: 'unreachable', reason: msg });
    }

    await settle(page);
    const height = await growToContent(page, width);
    await page.evaluate(pageChecks);
    const result = await page.evaluate(() => (window as any).__uiaudit.run({}));
    const rank = { error: 0, warn: 1, info: 2 } as const;
    const findings: Issue[] = [...browserIssues, ...(result.findings as Issue[])].sort((a, b) => rank[a.severity] - rank[b.severity]);

    // The page must really be in the look this run claims (a theme that did not apply would make two runs identical).
    if ((scheme === 'dark') !== result.meta.darkSurface) {
      findings.unshift({ check: 'theme-not-applied', severity: 'error', message: 'This run asked for the ' + scheme + ' theme but the page is drawn ' + (result.meta.darkSurface ? 'dark' : 'light') + '.', label: '', selector: '', rect: { x: 0, y: 0, w: 0, h: 0 } });
    }

    await page.evaluate((f) => (window as any).__uiaudit.annotate(f), findings);
    await page.screenshot({ path: join(reportDir, shot), fullPage: true, animations: 'disabled', caret: 'hide' });
    write({ status: 'checked', shot, findings, totals: result.totals, meta: { ...result.meta, shotHeight: height } });

    await page.setViewportSize({ width, height: baseHeight });
    if (screen.after) await runSteps(page, screen.after, env, screen.app).catch(() => undefined);
  });
}
