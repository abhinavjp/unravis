// Performance check of the Investigation Timeline on a large generated dataset (cmd/unravis-perfdata).
// Off unless UNRAVIS_PERF_CFG points at the cfg folder of a generated dataset, for example
//   .dev\perf-data\S\cfg      (manifest.json sits next to it, one folder up)
// The real app (UNRAVIS_LIVE_EXE, default unravis.exe beside the demo exe) is started on a COPY of that folder,
// so the dataset itself is never changed. Tests:
//   PERF-01  the widest allowed window (186 days) draws a chart (or a plain partial-read notice) within the budget
//   PERF-02  a 7-day window at the end of the dataset draws a chart
// Budget: UNRAVIS_PERF_BUDGET_MS (default 90000). Timings go into the test annotations and the console.
import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { startLive, type Live } from '../support/live-launcher';

const CFG = process.env.UNRAVIS_PERF_CFG ?? '';
const BUDGET_MS = Number(process.env.UNRAVIS_PERF_BUDGET_MS || 90_000);
const MAX_BARS_PER_LANE = 2000;
const DAY = 24 * 3600 * 1000;

interface Manifest {
  caseTitle: string;
  windowStart: string;
  windowEnd: string;
  totals: { iis: number; sql: number };
}

function manifest(): Manifest {
  return JSON.parse(readFileSync(join(dirname(resolve(CFG)), 'manifest.json'), 'utf8')) as Manifest;
}

/** YYYY-MM-DDTHH:mm in UTC, the form a datetime-local box takes. */
const box = (ms: number) => new Date(ms).toISOString().slice(0, 16);

async function fillWindows(page: Page, w: readonly string[]) {
  const inputs = page.locator('input[type=datetime-local]');
  for (let i = 0; i < 4; i++) await inputs.nth(i).fill(w[i]);
}

async function openTracing(page: Page, title: string) {
  await page.getByRole('button', { name: 'Open case ' + title }).click();
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect(page.locator('.tracing-chart')).toBeVisible({ timeout: 60_000 });
}

/** Types the windows, presses Start tracing, and returns the milliseconds until the chart is drawn again. */
async function trace(page: Page, w: readonly string[]): Promise<{ ms: number; status: number; bytes: number }> {
  await fillWindows(page, w);
  const answered = page.waitForResponse((r) => r.url().includes('/tracing') && r.request().method() === 'POST' && (r.request().postData() ?? '').includes(w[0] + ':00.000Z'), { timeout: BUDGET_MS });
  const t0 = Date.now();
  await page.getByRole('button', { name: 'Start tracing' }).click();
  const res = await answered;
  const body = await res.body();
  await expect(page.locator('.tracing-chart')).toBeVisible({ timeout: BUDGET_MS });
  await expect(page.locator('.chart-veil')).toHaveCount(0, { timeout: BUDGET_MS });
  return { ms: Date.now() - t0, status: res.status(), bytes: body.length };
}

test.describe('Investigation Timeline on a generated dataset', () => {
  test.skip(!CFG, 'Set UNRAVIS_PERF_CFG to the cfg folder of a generated dataset to run the performance checks.');
  test.describe.configure({ mode: 'serial' });
  test.setTimeout(240_000);

  let app: Live;
  let m: Manifest;
  const problems: string[] = [];

  test.beforeAll(async () => {
    m = manifest();
    app = await startLive({ configDir: CFG });
  });
  test.afterAll(async () => {
    await app?.stop();
  });

  test.beforeEach(async ({ page }) => {
    problems.length = 0;
    page.on('console', (msg) => {
      if (msg.type() === 'error') problems.push('console: ' + msg.text());
    });
    page.on('pageerror', (e) => problems.push('pageerror: ' + e.message));
    page.on('requestfailed', (r) => {
      if (!/ERR_ABORTED/.test(r.failure()?.errorText ?? '')) problems.push('requestfailed: ' + r.url());
    });
    await page.goto(app.base + '/#t=' + app.token);
    await expect(page.locator('#start-title')).toBeVisible({ timeout: 30_000 });
  });

  async function check(page: Page, label: string, r: { ms: number; status: number; bytes: number }) {
    test.info().annotations.push({ type: label, description: `${r.ms} ms, HTTP ${r.status}, ${r.bytes} bytes` });
    console.log(`[perf] ${label}: ${r.ms} ms, HTTP ${r.status}, ${r.bytes} bytes`);
    expect(r.status, 'tracing answer status').toBe(200);
    expect(r.bytes, 'tracing answer size').toBeLessThan(2 * 1024 * 1024);
    expect(r.ms, 'time to a drawn chart').toBeLessThan(BUDGET_MS);
    // Not a blank screen, not an error panel.
    await expect(page.locator('.error-panel')).toHaveCount(0);
    await expect(page.locator('.panel[data-lane]').first()).toBeVisible();
    for (const lane of ['audit', 'sql', 'iis']) {
      const n = await page.locator(`.panel[data-lane=${lane}] .bar`).count();
      expect(n, `bars in the ${lane} lane`).toBeLessThanOrEqual(MAX_BARS_PER_LANE);
    }
    const iisBars = await page.locator('.panel[data-lane=iis] .bar').count();
    expect(iisBars, 'the web-server lane has bars').toBeGreaterThan(0);
    expect(problems, 'browser errors').toEqual([]);
  }

  test('PERF-01: the widest window (186 days) draws a chart within the budget', async ({ page }) => {
    await openTracing(page, m.caseTitle);
    const end = Date.parse(m.windowEnd);
    const start = end - 186 * DAY;
    const r = await trace(page, [box(start), box(end), box(start - 3600_000), box(start)]);
    await check(page, 'wide-186d', r);
    // A lane that hit its time limit says so in plain words; that is allowed, a silent blank is not.
    const notice = await page.getByText(/Showing part of this lane/).count();
    test.info().annotations.push({ type: 'partial-notices', description: String(notice) });
  });

  test('PERF-02: a 7-day window at the end of the dataset draws a chart', async ({ page }) => {
    await openTracing(page, m.caseTitle);
    const end = Date.parse(m.windowEnd);
    const start = end - 7 * DAY;
    const r = await trace(page, [box(start), box(end), box(start - 3600_000), box(start)]);
    await check(page, 'narrow-7d', r);
  });
});
