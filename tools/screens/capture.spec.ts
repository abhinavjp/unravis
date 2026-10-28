import { expect, test, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// One screen per entry of screens.json. A step is one of:
//   { "goto": "/" }                     open the app (the session token is added) or a hash route
//   { "click": "<selector>" }           click one element
//   { "fill": ["<selector>", "text"] }  type into one field
//   { "waitFor": "<selector>" }         wait until one element shows
//   { "scrollTo": "<selector>" }        bring one element into view
//   { "theme": "dark" | "light" }       force the page theme (the project's colour scheme decides otherwise)
//   { "aside": [ ...steps ] }           run the steps in a second tab of the same browser, then close it (a case moved by another tab)
// Any step may add "when": "narrow" or "wide" to run only below or above the 1100 px width where the header folds into a More menu.
// A screen with "app": "setup" is a page of the setup window (P11). It runs against the screenshot build of unravis-setup
// (run-screens.ps1 starts it with --capture, which fakes every change), and its goto path picks a fake run with ?scenario=<name>.
// Selectors are Playwright selectors, for example role=button[name="Glossary"].
type Step = (
  | { goto: string }
  | { click: string }
  | { fill: [string, string] }
  | { waitFor: string }
  | { scrollTo: string }
  | { theme: 'dark' | 'light' }
  | { aside: Step[] }
) & { when?: 'narrow' | 'wide' };
// `after` runs once the picture is taken, to put back what the steps changed on the demo (the demo is shared by every screen).
interface Screen { id: string; phase: string; doc: string; narrow: boolean; app?: 'setup'; steps: Step[]; after?: Step[] }

const screens: Screen[] = JSON.parse(readFileSync(join(__dirname, 'screens.json'), 'utf8')).screens;
const only = (process.env.UNRAVIS_ONLY ?? '').split(',').map((part) => part.trim()).filter(Boolean);
const demoNow = new Date(process.env.UNRAVIS_DEMO_NOW ?? '2026-10-20T09:00:00Z');
if (Number.isNaN(demoNow.getTime())) {
  throw new Error('UNRAVIS_DEMO_NOW must look like 2026-10-20T09:00:00Z, but it is "' + process.env.UNRAVIS_DEMO_NOW + '". run-screens.ps1 sets it for you; leave it unset.');
}

const setupUrl = process.env.UNRAVIS_SETUP_URL ?? '';
const setupKey = process.env.UNRAVIS_SETUP_KEY ?? '';

// The setup window runs one fake run at a time; the progress screen holds its run until Cancel. This ends any run left by the
// screen before, so the next one can start.
async function freeSetup(): Promise<void> {
  const headers = { 'X-Setup-Key': setupKey };
  await fetch(setupUrl + '/api/cancel', { method: 'POST', headers });
  for (let i = 0; i < 50; i++) {
    const state = await (await fetch(setupUrl + '/api/progress?since=0', { headers })).json() as { running: boolean };
    if (!state.running) return;
    await new Promise((done) => setTimeout(done, 100));
  }
  throw new Error('The setup window did not stop its last fake run.');
}

async function runSteps(page: Page, steps: Step[], app?: 'setup'): Promise<void> {
  const narrow = (page.viewportSize()?.width ?? 1440) < 1100;
  for (const step of steps) {
    if (step.when && (step.when === 'narrow') !== narrow) continue;
    await run(page, step, app);
  }
}

async function run(page: Page, step: Step, app?: 'setup'): Promise<void> {
  if ('aside' in step) {
    const other = await page.context().newPage();
    try {
      await other.setViewportSize(page.viewportSize() ?? { width: 1440, height: 900 });
      await other.clock.setFixedTime(demoNow);
      await runSteps(other, step.aside, app);
    } finally {
      await other.close();
    }
  } else if ('goto' in step && app === 'setup') {
    // The setup page takes its key from the address fragment, as the real setup window does.
    await page.goto(setupUrl + step.goto + '#k=' + setupKey);
  } else if ('goto' in step) {
    const base = process.env.UNRAVIS_BASE_URL!;
    // The first open carries the token, the way the launch link does; later opens keep the one in the tab.
    const hash = step.goto.startsWith('#') ? step.goto : '#t=' + process.env.UNRAVIS_TOKEN;
    await page.goto(base + '/' + hash);
  } else if ('click' in step) {
    await page.locator(step.click).first().click();
  } else if ('fill' in step) {
    await page.locator(step.fill[0]).first().fill(step.fill[1]);
  } else if ('waitFor' in step) {
    await page.locator(step.waitFor).first().waitFor({ state: 'visible' });
  } else if ('scrollTo' in step) {
    await page.locator(step.scrollTo).first().scrollIntoViewIfNeeded();
  } else if ('theme' in step) {
    await page.evaluate((theme) => document.documentElement.setAttribute('data-theme', theme), step.theme);
  } else {
    throw new Error('A step in screens.json has no known action: ' + JSON.stringify(step) + '. Use goto, click, fill, waitFor, scrollTo, theme or aside.');
  }
}

for (const screen of screens) {
  if (only.length && !only.includes(screen.id)) continue;
  test(screen.id, async ({ page }, info) => {
    test.skip(info.project.name.endsWith('-narrow') && !screen.narrow, 'this screen has no narrow picture');
    test.skip(screen.app === 'setup' && !setupUrl, 'the setup window is not running (run-screens.ps1 starts it when Go is found)');
    test.skip(screen.app !== 'setup' && !process.env.UNRAVIS_BASE_URL, 'the demo is not running');
    if (screen.app === 'setup') await freeSetup();
    // The page clock stands still at the demo's reference time, so a relative time reads the same on every run.
    await page.clock.setFixedTime(demoNow);
    await runSteps(page, screen.steps, screen.app);
    await page.evaluate(() => document.fonts.ready);
    await page.mouse.move(0, 0);
    // The setup window asks for progress all the time while a run is going, so it is never idle; give it a moment instead.
    await page.waitForLoadState('networkidle', screen.app === 'setup' ? { timeout: 1500 } : undefined).catch(() => undefined);
    try {
      await expect(page).toHaveScreenshot([screen.phase, screen.id + '.png']);
    } finally {
      if (screen.after) await runSteps(page, screen.after, screen.app);
    }
  });
}
