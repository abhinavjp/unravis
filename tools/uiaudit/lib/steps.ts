// Runs the steps of one screen (the same small language as tools/screens/screens.json, see tools/screens/capture.spec.ts).
import type { Page } from '@playwright/test';
import { makeUploads } from './fixtures';
import { createCase, ensureHost, importFile, makeApi, HOST } from '../../e2e/support/api';
import { perfmonCsv } from '../../e2e/support/perfmon';

export type Step = (
  | { goto: string }
  | { click: string }
  | { fill: [string, string] }
  | { select: [string, string] }
  | { waitFor: string }
  | { scrollTo: string }
  /** A mouse click on the middle of an element that takes no pointer events itself (a mark drawn under the chart's click layer): what a person's click does. */
  | { clickCentre: string }
  | { theme: 'dark' | 'light' }
  | { aside: Step[] }
  /** Hands made-up fixture files (lib/fixtures.ts, by key) to a file input: [selector of the input, [fixture keys]]. */
  | { upload: [string, string[]] }
  /** Makes a case with this title that holds a made-up Windows counters file (real API, real importer): the resource counters panel has something to draw. The screen must say `restoreHosts` and end with `dropCase`. */
  | { seedCounters: string }
  /** Erases the case that `seedCounters` made with this title (closed first), and its tombstone, so later screens see the demo as it was. */
  | { dropCase: string }
  /** Steps listed once under `macros` in extra-screens.json (see expandMacros). */
  | { macro: string }
) & { when?: 'narrow' | 'wide' };

/** Which program a screen belongs to: none is the full app. The others are started by global-setup (see there). */
export type AppName = 'setup' | 'releases' | 'releases-empty' | 'lite' | 'lite-stopped';

export interface Screen {
  id: string;
  phase: string;
  doc?: string;
  narrow?: boolean;
  app?: AppName;
  retired?: string;
  steps: Step[];
  after?: Step[];
  /** Why this screen is in the audit list (extra screens only). */
  note?: string;
  /** The steps change the list of computers on the shared demo (the add-files review does): it is read before and put back after. */
  restoreHosts?: boolean;
}

/** Replaces every `{ macro }` step by the steps of that name (macros may use other macros). */
export function expandMacros(steps: Step[], macros: Record<string, Step[]>, depth = 0): Step[] {
  if (depth > 5) throw new Error('Macros nest too deep.');
  return steps.flatMap((step) => {
    if ('macro' in step) {
      const body = macros[step.macro];
      if (!body) throw new Error('Unknown macro: ' + step.macro);
      return expandMacros(body, macros, depth + 1).map((s) => (step.when ? { ...s, when: step.when } : s));
    }
    return [step];
  });
}

export interface Env {
  base: string;
  token: string;
  setupUrl: string;
  setupKey: string;
  /** The other small programs, by app name: where they listen and the session code their page needs. */
  apps: Partial<Record<AppName, { url: string; code: string }>>;
  demoNow: Date;
}

/** The header folds into a More menu below this width (same rule as screens.json). */
export const NARROW_BELOW = 1100;

/** The cases `seedCounters` made, by title, until `dropCase` erases them. */
const seeded = new Map<string, string>();

export async function runSteps(page: Page, steps: Step[], env: Env, app?: AppName): Promise<void> {
  const narrow = (page.viewportSize()?.width ?? 1440) < NARROW_BELOW;
  for (const step of steps) {
    if (step.when && (step.when === 'narrow') !== narrow) continue;
    await runStep(page, step, env, app);
  }
}

async function runStep(page: Page, step: Step, env: Env, app?: AppName): Promise<void> {
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
  } else if ('goto' in step && app && app !== 'setup') {
    const a = env.apps[app];
    if (!a) throw new Error('The ' + app + ' program was not started on this run.');
    // Releases takes its code in the query (it sets a cookie), Lite and the full app in the fragment.
    await page.goto(a.url + (app.startsWith('releases') ? '/?t=' + a.code : '/#t=' + a.code));
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
  } else if ('clickCentre' in step) {
    const target = page.locator(step.clickCentre).first();
    await target.scrollIntoViewIfNeeded();
    const box = await target.boundingBox();
    if (!box) throw new Error('clickCentre: no box for ' + step.clickCentre);
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  } else if ('upload' in step) {
    // The real file input of the screen (a visually hidden <input type=file>): the page takes the files as if they were chosen in the dialog.
    await page.locator(step.upload[0]).first().setInputFiles(makeUploads(step.upload[1]));
  } else if ('seedCounters' in step) {
    const api = makeApi(env.base, env.token);
    await ensureHost(api);
    const id = await createCase(api, step.seedCounters);
    seeded.set(step.seedCounters, id);
    const result = await importFile(api, id, perfmonCsv(), { name: 'perf.csv', importer: 'win-counters', host: HOST });
    if (result.status >= 300) throw new Error('seedCounters: the import was refused: ' + result.status + ' ' + result.text.slice(0, 200));
  } else if ('dropCase' in step) {
    const id = seeded.get(step.dropCase);
    if (!id) return;
    seeded.delete(step.dropCase);
    const api = makeApi(env.base, env.token);
    await api('POST', '/api/cases/' + id + '/close', undefined, 'open');
    await api('DELETE', '/api/cases/' + id, { confirm: id.slice(0, 8) }, 'closed');
    await api('DELETE', '/api/cases/erased/' + id);
  } else if ('theme' in step) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), step.theme);
  } else {
    throw new Error('Unknown step: ' + JSON.stringify(step));
  }
}
