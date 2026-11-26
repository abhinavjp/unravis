// Shared parts of the strict-CSP run (csp.spec.ts, tooltips.spec.ts): the policy, the way a violation is caught, and condition waits (no sleeps).
import type { BrowserContext, Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expandMacros, type Screen, type Step } from '../lib/steps';

const repo = join(__dirname, '..', '..', '..');

/** The exact policy of the desktop shell. */
export const CSP: string = JSON.parse(readFileSync(join(repo, 'desktop', 'src-tauri', 'tauri.conf.json'), 'utf8')).app.security.csp;

export interface Violation { directive: string; blocked: string; source: string; sample: string; page: string; disposition?: string }

/** BL-324 trials, off in the normal strict-CSP run: a second, report-only policy, and the freeze that Tauri's `freezePrototype` does at document start. */
export interface Trial { reportOnly?: string; freezePrototype?: boolean }

/**
 * Makes every page document of the context run under the desktop policy and collects what breaks it: `securitypolicyviolation` events (an init script in every
 * frame that lets scripts run) and the browser's own "Refused to ..." console lines (frames a sandbox keeps scripts out of).
 */
export async function arm(context: BrowserContext, found: Violation[], consoleLines: string[], trial: Trial = {}): Promise<void> {
  await context.exposeBinding('__cspViolation', (_src, v: Violation) => { found.push(v); });
  await context.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      const v = { directive: e.effectiveDirective, blocked: e.blockedURI, source: e.sourceFile + ':' + e.lineNumber, sample: e.sample, page: location.href.replace(/#.*/, ''), disposition: e.disposition };
      (window as unknown as { __cspViolation: (v: unknown) => void }).__cspViolation(v);
    });
  });
  if (trial.freezePrototype) {
    // What Tauri does with `freezePrototype: true`: Object.prototype is frozen before any page script runs.
    await context.addInitScript(() => { Object.freeze(Object.prototype); });
  }
  await context.route('**/*', async (route) => {
    const request = route.request();
    if (request.resourceType() !== 'document') return route.continue();
    const response = await route.fetch();
    const headers: Record<string, string> = { ...response.headers(), 'content-security-policy': CSP };
    if (trial.reportOnly) headers['content-security-policy-report-only'] = trial.reportOnly;
    await route.fulfill({ response, headers });
  });
  context.on('console', (m) => { if (/Content Security Policy|Refused to (apply|load|execute|connect|create|frame|evaluate)/i.test(m.text()) && !(trial.reportOnly && /Report Only/i.test(m.text()))) consoleLines.push(m.text().slice(0, 400)); });
}

/** Two animation frames: whatever the page queued before (a violation event, a canvas redraw) has run when this returns. A round trip, not a sleep. */
export const frames = (page: Page): Promise<void> =>
  page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))));

/**
 * Waits until the app has no request in flight for `quietMs`. It never gives up quietly: a page that keeps fetching fails with a message that
 * lists the requests still open. (Long-lived event streams and sockets are not requests the page waits for.)
 */
export function trackRequests(page: Page): { quiet(quietMs?: number, timeoutMs?: number): Promise<void> } {
  const open = new Set<string>();
  const key = (r: { url(): string; method(): string }) => r.method() + ' ' + r.url();
  const ignore = (type: string) => type === 'eventsource' || type === 'websocket';
  const ids = new Map<object, string>();
  let n = 0;
  const id = (r: object & { url(): string; method(): string }) => { if (!ids.has(r)) ids.set(r, key(r) + '#' + n++); return ids.get(r)!; };
  page.on('request', (r) => { if (!ignore(r.resourceType())) open.add(id(r)); });
  const done = (r: object & { url(): string; method(): string }) => { open.delete(id(r)); };
  page.on('requestfinished', done);
  page.on('requestfailed', done);
  return {
    async quiet(quietMs = 300, timeoutMs = 15_000) {
      const start = Date.now();
      let since = Date.now();
      while (Date.now() - since < quietMs) {
        if (open.size) since = Date.now();
        if (Date.now() - start > timeoutMs) throw new Error('The page did not go quiet in ' + timeoutMs + ' ms. Still open: ' + [...open].slice(0, 5).join(', '));
        await frames(page);
      }
    }
  };
}

export interface Box { top: number; bottom: number; left: number; right: number; count: number }

/**
 * Compares the canvases of one chart with the picture taken by `rest` and returns the box of what changed, counting only rows and columns that hold a
 * run of changed pixels (a tooltip box, not the 1 px axis pointer line or a marker). All in the page: a canvas is megabytes, never sent to the test.
 * `mode` 'rest' keeps the resting picture; 'diff' measures against it.
 */
export async function chartDiff(page: Page, chart: string, index: number, mode: 'rest' | 'diff', run = 60): Promise<{ width: number; height: number; box: Box | null }> {
  return page.evaluate(([selector, at, m, minRun]) => {
    const root = document.querySelectorAll(selector as string)[at as number]!;
    const canvases = [...root.querySelectorAll('canvas')];
    const now = canvases.map((c) => c.getContext('2d')!.getImageData(0, 0, c.width, c.height).data);
    const w = window as unknown as { __rest?: Uint8ClampedArray[] };
    const width = canvases[0]?.width ?? 0, height = canvases[0]?.height ?? 0;
    if (m === 'rest') { w.__rest = now; return { width, height, box: null }; }
    const before = w.__rest ?? [];
    const rows = new Array<number>(height).fill(0), cols = new Array<number>(width).fill(0);
    let count = 0;
    now.forEach((q, layer) => {
      const p = before[layer];
      if (!p || p.length !== q.length) return;
      for (let i = 0; i < q.length; i += 4) {
        if (Math.abs(p[i] - q[i]) + Math.abs(p[i + 1] - q[i + 1]) + Math.abs(p[i + 2] - q[i + 2]) > 48) { const px = i >> 2; rows[Math.floor(px / width)]++; cols[px % width]++; count++; }
      }
    });
    const ys = rows.flatMap((n, y) => (n >= (minRun as number) ? [y] : [])), xs = cols.flatMap((n, x) => (n >= (minRun as number) / 2 ? [x] : []));
    return { width, height, box: ys.length && xs.length ? { top: ys[0], bottom: ys[ys.length - 1], left: xs[0], right: xs[xs.length - 1], count } : null };
  }, [chart, index, mode, run] as const);
}

/** Every ECharts chart on the page (ECharts marks its container with this attribute). */
export const CHARTS = '[_echarts_instance_]';

/**
 * Hovers one chart on a grid of points and reports whether a tooltip box was drawn (a run of changed pixels at least 60 wide and 20 tall against the
 * resting chart) and the biggest box seen. The pointer is parked away from the chart for the resting picture.
 */
export async function hoverChart(page: Page, index: number, points: Array<[number, number]> = [[0.25, 0.3], [0.5, 0.3], [0.75, 0.3], [0.25, 0.6], [0.5, 0.6], [0.75, 0.6], [0.5, 0.8]], all = false): Promise<{ drawn: boolean; box: Box | null; height: number; width: number }> {
  const target = page.locator(CHARTS).nth(index);
  if (!(await target.isVisible())) return { drawn: false, box: null, height: 0, width: 0 }; // a chart kept in the page but hidden (no data yet)
  // In the page, not through Playwright's actionability wait: a chart that is still settling its size is not "stable" and would stall the sweep.
  const rect = await target.evaluate((el) => { el.scrollIntoView({ block: 'center' }); const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; });
  if (rect.width < 80 || rect.height < 40) return { drawn: false, box: null, height: 0, width: 0 };
  await page.mouse.move(1, 1);
  await frames(page);
  const rest = await chartDiff(page, CHARTS, index, 'rest');
  let best: Box | null = null;
  for (const [fx, fy] of points) {
    await page.mouse.move(rect.x + rect.width * fx, rect.y + rect.height * fy, { steps: 2 });
    await frames(page);
    const box = (await chartDiff(page, CHARTS, index, 'diff')).box;
    if (box && box.bottom - box.top >= 20 && box.right - box.left >= 60 && (!best || (box.bottom - box.top) * (box.right - box.left) > (best.bottom - best.top) * (best.right - best.left))) best = box;
    if (best && !all) break; // one drawn tooltip is the proof; `all` keeps sweeping to find the biggest box
  }
  return { drawn: best !== null, box: best, height: rest.height, width: rest.width };
}

/** The screens of the full app that the CSP runs open: tools/screens/screens.json plus extra-screens.json (`UIAUDIT_ONLY=a,b` picks some). The other programs (setup window, Releases dashboard, Lite) are not shown in the desktop shell. */
export function loadScreens(): Screen[] {
  const root = join(__dirname, '..');
  const base: Screen[] = JSON.parse(readFileSync(join(root, '..', 'screens', 'screens.json'), 'utf8')).screens;
  const extraFile: { macros?: Record<string, Step[]>; screens: Screen[] } = JSON.parse(readFileSync(join(root, 'extra-screens.json'), 'utf8'));
  const extra: Screen[] = extraFile.screens.map((s) => ({ ...s, steps: expandMacros(s.steps, extraFile.macros ?? {}), after: s.after && expandMacros(s.after, extraFile.macros ?? {}) }));
  const only = (process.env.UIAUDIT_ONLY ?? '').split(',').map((s) => s.trim()).filter(Boolean);
  return [...base, ...extra].filter((s) => !s.app && !s.retired && (!only.length || only.includes(s.id)));
}
