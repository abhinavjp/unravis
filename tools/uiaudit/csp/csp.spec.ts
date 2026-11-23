// BL-323: opens every screen of the full app (tools/screens/screens.json plus extra-screens.json, the same list the UI audit uses) under the
// strict Content-Security-Policy of the desktop shell and fails on any violation.
//
//   cd tools/uiaudit && npx playwright test -c playwright.csp.config.ts            (all screens)
//   UIAUDIT_ONLY=p1-home,x-where-is-it npx playwright test -c playwright.csp.config.ts
//
// How: the demo app answers every page document; this run adds the policy as a response header (context.route), so the page runs exactly as it
// would in the desktop window: no inline script, no inline style attribute, no `unsafe-eval`, images and fonts from the app's own origin only.
// A violation is caught three ways, so none is missed: the `securitypolicyviolation` event (an init script in every frame that lets it run),
// the browser's "Refused to ..." console line (frames a sandbox keeps scripts out of, such as the report preview), and `pageerror`.
// CSSOM writes (`element.style.x = ...`, `style:left={v}` in Svelte, ECharts positioning) are not blocked by `style-src`; markup `style="..."` is.
import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { runSteps, expandMacros, type Screen, type Env, type Step } from '../lib/steps';
import { CSP, CHARTS, arm, frames, hoverChart, trackRequests, type Violation } from './harness';

const root = join(__dirname, '..');

const base: Screen[] = JSON.parse(readFileSync(join(root, '..', 'screens', 'screens.json'), 'utf8')).screens;
const extraFile: { macros?: Record<string, Step[]>; screens: Screen[] } = JSON.parse(readFileSync(join(root, 'extra-screens.json'), 'utf8'));
const extra: Screen[] = extraFile.screens.map((s) => ({ ...s, steps: expandMacros(s.steps, extraFile.macros ?? {}), after: s.after && expandMacros(s.after, extraFile.macros ?? {}) }));
const only = (process.env.UIAUDIT_ONLY ?? '').split(',').map((s) => s.trim()).filter(Boolean);
// The other programs (setup window, Releases dashboard, Lite) are not shown in the desktop shell: only the full app.
const screens = [...base, ...extra].filter((s) => !s.app && !s.retired && (!only.length || only.includes(s.id)));

/** Screens whose charts must draw a hover tooltip in this run: a sweep that misses every chart fails them (a tooltip no one drew is a tooltip no one checked). */
const TOOLTIP_SCREENS: string[] = ['x-case-analysis', 'x-analysis-daily-hourly', 'x-where-groups', 'x-where-volume', 'x-blackbox-charts', 'x-blackbox-charts-sql'];

test('the policy under test is the strict one', () => {
  expect(CSP).toContain("script-src 'self'");
  expect(CSP).toContain("style-src 'self'");
  expect(CSP).not.toMatch(/unsafe-inline|unsafe-eval|data:|blob:|\*/);
});

// The harness itself: the policy is really enforced on the page, a style written into markup is reported, and a style written through the CSSOM is not
// (spec 2.28 rests on this). If the first fails, every green screen below means nothing.
test('self-check: a markup style and a data: picture are blocked and reported; CSSOM styles are not', async ({ page, context }) => {
  const found: Violation[] = [];
  const lines: string[] = [];
  await arm(context, found, lines);
  await page.goto(process.env.UNRAVIS_BASE_URL! + '/#t=' + process.env.UNRAVIS_TOKEN);
  await page.waitForSelector('#app *');
  await page.evaluate(() => {
    const ok = document.createElement('div');
    ok.style.color = 'red'; ok.style.cssText = 'left: 3px'; ok.style.setProperty('--k', '1');
    document.body.appendChild(ok);
  });
  await frames(page); // a violation event is a queued task: after two animation frames it has been delivered
  expect(found.map((v) => v.directive), 'CSSOM writes are not violations').toEqual([]);
  await page.evaluate(() => {
    const bad = document.createElement('div');
    bad.innerHTML = '<b style="color:red">x</b><img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=">';
    document.body.appendChild(bad);
  });
  await expect.poll(() => [...new Set(found.map((v) => v.directive))].sort()).toEqual(['img-src', 'style-src-attr']);
});

for (const screen of screens) {
  test(screen.id, async ({ page, context }) => {
    const env: Env = {
      base: process.env.UNRAVIS_BASE_URL!,
      token: process.env.UNRAVIS_TOKEN!,
      setupUrl: '', setupKey: '', apps: {},
      demoNow: new Date(process.env.UNRAVIS_DEMO_NOW ?? '2026-10-20T09:00:00Z')
    };
    const found: Violation[] = [];
    const consoleLines: string[] = [];
    const errors: string[] = [];
    await arm(context, found, consoleLines);
    const requests = trackRequests(page);
    page.on('pageerror', (e) => errors.push(String(e.message).slice(0, 300)));
    await page.addInitScript(() => { try { localStorage.setItem('unravis.theme', 'dark'); } catch { /* blocked storage */ } });
    await page.clock.setFixedTime(env.demoNow);
    try {
      await runSteps(page, screen.steps, env);
    } catch (err) {
      // A screen the steps cannot reach is a gap the UI audit reports; here it is a failure too, so a CSP break that hides a button shows up.
      throw new Error('The screen was not reached: ' + String((err as Error).message).split('\n').slice(0, 3).join(' '));
    }
    await page.evaluate(() => document.fonts.ready);
    await requests.quiet(); // fails, with the open requests named, when the page never goes quiet: no silent time box
    await frames(page);
    // Hover tooltips (ECharts, the tracing chart) are built on the first pointer move over a chart: sweep the pointer across every chart on the screen and
    // count the charts that drew a tooltip box (compared with the resting canvas).
    let drawn = 0;
    const charts = await page.locator(CHARTS).count();
    for (let i = 0; i < charts; i++) if ((await hoverChart(page, i)).drawn) drawn++;
    for (const plot of await page.locator('.tc-plot, .panel').all()) {
      const box = await plot.boundingBox().catch(() => null);
      if (!box || box.width < 80 || box.height < 40) continue;
      for (const f of [0.25, 0.5, 0.75]) await page.mouse.move(box.x + box.width * f, box.y + box.height * 0.5, { steps: 3 });
      await frames(page);
    }
    // CSP_SHOTS=<folder> keeps a picture of each screen with the pointer still over its last chart, to look at the rich-text tooltips.
    if (process.env.CSP_SHOTS) await page.screenshot({ path: join(process.env.CSP_SHOTS, screen.id + '.png') });
    await page.mouse.move(2, 2);
    if (screen.after) await runSteps(page, screen.after, env);

    const lines = [
      ...found.map((v) => `${v.directive} blocked ${v.blocked || '(inline)'} at ${v.source} ${v.sample ? 'sample "' + v.sample + '"' : ''} on ${v.page}`),
      // The console repeats every event; a line is only added when no event of its directive was seen (a frame the init script could not reach).
      ...consoleLines.filter((l) => { const d = /directive[: ]+"?([a-z-]+)/i.exec(l)?.[1]; return !d || !found.some((v) => v.directive.startsWith(d)); })
    ];
    expect(lines, 'Content-Security-Policy violations on ' + screen.id).toEqual([]);
    expect(errors, 'script errors on ' + screen.id).toEqual([]);
    test.info().annotations.push({ type: 'tooltips', description: drawn + ' of ' + charts + ' charts drew one' });
    if (process.env.CSP_TIPS) console.log('TIPS ' + screen.id + ' ' + drawn + '/' + charts);
    if (TOOLTIP_SCREENS.includes(screen.id)) expect(drawn, 'charts that drew a hover tooltip on ' + screen.id + ' (of ' + charts + ')').toBeGreaterThan(0);
  });
}
