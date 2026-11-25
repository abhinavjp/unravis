// BL-324: Trusted Types report-only, and the `freezePrototype` trial, over every screen of the full app (the screens, steps and chart hover sweep of csp.spec.ts).
//
//   cd tools/uiaudit && npx playwright test -c playwright.tt.config.ts                  (both trials, all screens)
//   TT_TRIAL=tt|freeze   picks one      UIAUDIT_ONLY=p1-home,...   picks screens      TT_REPORT=<file>   writes the findings as JSON
//
// Trial `tt`: every page document is answered with the desktop policy (enforced, as in the shell) AND a second header,
// `Content-Security-Policy-Report-Only: require-trusted-types-for 'script'; trusted-types 'none'`. Report-only changes nothing on the page; each place
// that writes a string to a script sink (innerHTML, eval, a script's src or text, ...) or creates a Trusted Types policy comes back as a
// `securitypolicyviolation` with disposition "report". `trusted-types 'none'` makes a library that creates a policy of its own show up with its name.
// Trial `freeze`: `Object.freeze(Object.prototype)` runs before any page script, as Tauri's `freezePrototype: true` does; the screen fails on a script error
// or console error. Both trials fail a screen with findings, so a clean run is the evidence for turning the setting on.
import { test, expect } from '@playwright/test';
import { appendFileSync } from 'node:fs';
import { runSteps, type Env } from '../lib/steps';
import { CHARTS, arm, frames, hoverChart, loadScreens, trackRequests, type Trial, type Violation } from './harness';

const screens = loadScreens();
const TRIALS: Record<string, Trial> = {
  tt: { reportOnly: "require-trusted-types-for 'script'; trusted-types 'none'" },
  freeze: { freezePrototype: true }
};
const picked = (process.env.TT_TRIAL ?? '').split(',').map((s) => s.trim()).filter(Boolean);
const trials = Object.keys(TRIALS).filter((t) => !picked.length || picked.includes(t));

// The harness itself: a string written to innerHTML is reported by the report-only policy but not blocked, and a frozen Object.prototype is really frozen.
test('self-check: the report-only policy reports an innerHTML string and does not block it; the freeze holds', async ({ page, context, browser }) => {
  const found: Violation[] = [];
  await arm(context, found, [], TRIALS.tt);
  await page.goto(process.env.UNRAVIS_BASE_URL! + '/#t=' + process.env.UNRAVIS_TOKEN);
  await page.waitForSelector('#app *');
  await page.evaluate(() => { const d = document.createElement('div'); d.innerHTML = '<b id="tt-probe">x</b>'; document.body.appendChild(d); });
  await frames(page);
  expect(await page.locator('#tt-probe').count(), 'report-only must not block the write').toBe(1);
  await expect.poll(() => found.filter((v) => v.directive === 'require-trusted-types-for' && v.disposition === 'report').length).toBeGreaterThan(0);
  const frozenContext = await browser.newContext(); // a context takes one arm() only
  const frozen = await frozenContext.newPage();
  await arm(frozenContext, [], [], TRIALS.freeze);
  await frozen.goto(process.env.UNRAVIS_BASE_URL! + '/#t=' + process.env.UNRAVIS_TOKEN);
  expect(await frozen.evaluate(() => Object.isFrozen(Object.prototype))).toBe(true);
  await frozenContext.close();
});

for (const trial of trials) {
  for (const screen of screens) {
    test(trial + ' ' + screen.id, async ({ page, context }) => {
      const env: Env = {
        base: process.env.UNRAVIS_BASE_URL!,
        token: process.env.UNRAVIS_TOKEN!,
        setupUrl: '', setupKey: '', apps: {},
        demoNow: new Date(process.env.UNRAVIS_DEMO_NOW ?? '2026-10-20T09:00:00Z')
      };
      const found: Violation[] = [];
      const consoleLines: string[] = [];
      const errors: string[] = [];
      await arm(context, found, consoleLines, TRIALS[trial]);
      const requests = trackRequests(page);
      page.on('pageerror', (e) => errors.push('script error: ' + String(e.message).slice(0, 300)));
      page.on('console', (m) => { if (m.type() === 'error') errors.push('console error: ' + m.text().slice(0, 300)); });
      await page.addInitScript(() => { try { localStorage.setItem('unravis.theme', 'dark'); } catch { /* blocked storage */ } });
      // A promise that rejects because of a frozen prototype is not a pageerror: report it as a console error.
      await page.addInitScript(() => addEventListener('unhandledrejection', (e) => console.error('unhandled rejection: ' + String((e.reason && (e.reason.stack || e.reason.message)) || e.reason).slice(0, 400))));
      await page.clock.setFixedTime(env.demoNow);
      try {
        await runSteps(page, screen.steps, env);
      } catch (err) {
        throw new Error('The screen was not reached: ' + String((err as Error).message).split('\n').slice(0, 3).join(' ') + (errors.length ? ' | page errors: ' + errors.slice(0, 3).join(' ; ') : ''));
      }
      await page.evaluate(() => document.fonts.ready);
      await requests.quiet();
      await frames(page);
      const charts = await page.locator(CHARTS).count();
      for (let i = 0; i < charts; i++) await hoverChart(page, i);
      await page.mouse.move(2, 2);
      if (screen.after) await runSteps(page, screen.after, env);
      await frames(page);

      const lines = [
        ...found.map((v) => `${v.directive} (${v.disposition}) ${v.blocked || '(no uri)'} at ${v.source} ${v.sample ? 'sample "' + v.sample + '"' : ''}`),
        ...errors
      ];
      if (process.env.TT_REPORT) appendFileSync(process.env.TT_REPORT, JSON.stringify({ trial, screen: screen.id, lines }) + '\n');
      expect(lines, trial + ' findings on ' + screen.id).toEqual([]);
    });
  }
}
