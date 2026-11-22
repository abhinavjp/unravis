// Runs the steps of one screen (the same small language as tools/screens/screens.json, see tools/screens/capture.spec.ts).
import type { Page } from '@playwright/test';

export type Step = (
  | { goto: string }
  | { click: string }
  | { fill: [string, string] }
  | { select: [string, string] }
  | { waitFor: string }
  | { scrollTo: string }
  | { theme: 'dark' | 'light' }
  | { aside: Step[] }
) & { when?: 'narrow' | 'wide' };

export interface Screen {
  id: string;
  phase: string;
  doc?: string;
  narrow?: boolean;
  app?: 'setup';
  retired?: string;
  steps: Step[];
  after?: Step[];
  /** Why this screen is in the audit list (extra screens only). */
  note?: string;
}

export interface Env {
  base: string;
  token: string;
  setupUrl: string;
  setupKey: string;
  demoNow: Date;
}

/** The header folds into a More menu below this width (same rule as screens.json). */
export const NARROW_BELOW = 1100;

export async function runSteps(page: Page, steps: Step[], env: Env, app?: 'setup'): Promise<void> {
  const narrow = (page.viewportSize()?.width ?? 1440) < NARROW_BELOW;
  for (const step of steps) {
    if (step.when && (step.when === 'narrow') !== narrow) continue;
    await runStep(page, step, env, app);
  }
}

async function runStep(page: Page, step: Step, env: Env, app?: 'setup'): Promise<void> {
  if ('aside' in step) {
    const other = await page.context().newPage();
    try {
      await other.setViewportSize(page.viewportSize() ?? { width: 1440, height: 900 });
      await other.clock.setFixedTime(env.demoNow);
      await runSteps(other, step.aside, env, app);
    } finally {
      await other.close();
    }
  } else if ('goto' in step && app === 'setup') {
    await page.goto(env.setupUrl + step.goto + '#k=' + env.setupKey);
  } else if ('goto' in step) {
    const hash = step.goto.startsWith('#') ? step.goto : '#t=' + env.token;
    await page.goto(env.base + '/' + hash);
  } else if ('click' in step) {
    const target = page.locator(step.click).first();
    // On a phone the header folds its buttons into the More menu. A step written for the wide header
    // (screens.json gives the phone its own steps only for some screens) falls back to the menu item of the same name.
    const narrow = (page.viewportSize()?.width ?? 1440) < NARROW_BELOW;
    if (narrow && !app && step.click.startsWith('role=button[') && !(await target.isVisible())) {
      const more = page.locator('role=button[name="More"s]');
      if (await more.isVisible()) {
        await more.click();
        const item = page.locator(step.click.replace('role=button[', 'role=menuitem[')).first();
        if (await item.isVisible()) {
          await item.click();
          return;
        }
        await page.keyboard.press('Escape');
      }
    }
    await target.click();
  } else if ('fill' in step) {
    await page.locator(step.fill[0]).first().fill(step.fill[1]);
  } else if ('select' in step) {
    await page.locator(step.select[0]).first().selectOption(step.select[1]);
  } else if ('waitFor' in step) {
    await page.locator(step.waitFor).first().waitFor({ state: 'visible' });
  } else if ('scrollTo' in step) {
    await page.locator(step.scrollTo).first().scrollIntoViewIfNeeded();
  } else if ('theme' in step) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), step.theme);
  } else {
    throw new Error('Unknown step: ' + JSON.stringify(step));
  }
}
