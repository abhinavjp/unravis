// The Investigation Timeline chart frame (phase 1): a readable Y axis, gridlines, a wider centre lane, a reason panel for empty
// lanes and the explanation block under the chart. The demo clock is 2026-10-20T09:00Z; 19 Oct 09:00-10:00 has events in all three lanes.
//   TLF-01  The explanation block sits under the chart: "How to read this chart" is open, the other three are closed and open on click.
//   TLF-02  The left axis shows several ticks and a unit title, and the plot has gridlines.
//   TLF-05  With a slowness figure in the answer the API lane gets a right axis in milliseconds, hidden at 375 px.
//   TLF-03  The centre lane takes most of the width at 1280, and the Suspects rail only appears beside the chart when the window is wide.
//   TLF-08  The web-server lane shows refused answers (amber), a response-time line and a red diamond with its own legend.
//   TLF-09  The SQL lane (saved Query Store day) shows statements per interval, an average-time line, a red diamond and a legend.
//   TLF-04  A lane with nothing to draw shows a reason panel with the reason, and the page does not scroll sideways at 375 px.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { fillWindows } from '../support/window-picker';

const CASE_TITLE = 'Orders API stall, 19 October';
const WIN_ALL = ['2026-10-19T09:00', '2026-10-19T10:00', '2026-10-19T08:00', '2026-10-19T09:00'] as const;
const WIN_EMPTY = ['2026-09-01T00:00', '2026-09-01T01:00', '2026-08-31T23:00', '2026-09-01T00:00'] as const;

const isTrace = (r: { url(): string; method(): string }) => r.url().includes('/tracing') && r.method() === 'POST';

async function openTimeline(page: Page): Promise<void> {
  await goHome(page);
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect(page.locator('.tracing-chart')).toBeVisible();
}

/** Sets the windows and presses Refresh timeline, then waits for the answer. */
async function readWindow(page: Page, win: readonly [string, string, string, string]): Promise<void> {
  await fillWindows(page, win);
  const answered = page.waitForResponse((r) => isTrace(r.request()));
  await page.getByTestId('trace-sources').getByRole('button', { name: 'Refresh timeline' }).click();
  await answered;
  await expect(page.locator('.tracing-chart')).toBeVisible();
}

test('TLF-01: the explanation block sits under the chart, with the reading steps open and three more sections closed', async ({ page }) => {
  await openTimeline(page);
  const block = page.locator('.tracing-explain');
  await expect(block).toBeVisible();
  const chartBox = (await page.locator('.tracing-chart .lanes-grid').boundingBox())!;
  const blockBox = (await block.boundingBox())!;
  expect(blockBox.y, 'the explanation is under the lanes').toBeGreaterThanOrEqual(chartBox.y + chartBox.height);

  const read = page.getByTestId('explain-read');
  await expect(read).toHaveAttribute('open', '');
  await expect(read.locator('li')).toHaveCount(4);
  for (const id of ['explain-lanes', 'explain-axis', 'explain-details']) {
    const section = page.getByTestId(id);
    await expect(section, id + ' starts closed').not.toHaveAttribute('open', '');
    await section.locator('summary').click();
    await expect(section).toHaveAttribute('open', '');
  }
  await expect(page.getByTestId('explain-lanes').locator('dt')).toHaveCount(3);
  await expect(page.getByTestId('explain-axis').locator('li')).toHaveCount(3);
  await expect(page.getByTestId('explain-details-row')).toHaveCount(3);
});

test('TLF-02: the left axis has several ticks and a unit title, and the plot has gridlines', async ({ page }) => {
  await openTimeline(page);
  await readWindow(page, WIN_ALL);
  const audit = page.locator('.panel[data-lane="audit"]');
  await expect(audit).toHaveAttribute('data-state', 'events');

  const leftScale = page.locator('.axis-left .scale').first();
  expect(await leftScale.locator('.tick').count(), 'five or fewer ticks, at least three').toBeGreaterThanOrEqual(3);
  await expect(leftScale.locator('.tick-bottom')).toHaveText('0');
  await expect(leftScale.locator('.tick-top')).toHaveCount(1);
  await expect(page.locator('.axis-left .y-title').first()).toHaveText('events per slot');

  // The server now sends a slowness figure per interval, so the API lane gets a right axis in milliseconds.
  expect(await page.locator('.axis-right .tick').count(), 'right axis ticks when timings exist').toBeGreaterThanOrEqual(3);

  expect(await audit.locator('.hgrid > *').count(), 'horizontal gridlines').toBeGreaterThanOrEqual(2);
  expect(await audit.locator('.vgrid > *').count(), 'vertical gridlines at the time ticks').toBeGreaterThanOrEqual(2);
  const pointer = await audit.locator('.hgrid').evaluate((el) => getComputedStyle(el).pointerEvents);
  expect(pointer, 'gridlines never take the pointer').toBe('none');
});

test('TLF-03: at 1280 the centre lane takes most of the width, and the rail only sits beside the chart on a wide window', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await openTimeline(page);
  const grid = (await page.locator('.tracing-chart .lanes-grid').boundingBox())!;
  const panel = (await page.locator('.panel[data-lane="audit"]').boundingBox())!;
  expect(panel.width / grid.width, 'the plot is at least 60% of the lane grid').toBeGreaterThan(0.6);
  const label = (await page.locator('.lane-label').first().boundingBox())!;
  expect(label.width, 'the label column is about 9.5rem at most').toBeLessThanOrEqual(160);

  // 1280 px is under the 1480 px breakpoint, so the rail goes below the chart and the chart gets the full row.
  const rail = page.getByTestId('sus-rail');
  await expect(rail).toBeVisible();
  const railBox = (await rail.boundingBox())!;
  const chartBox = (await page.locator('.tracing-chart').boundingBox())!;
  expect(railBox.y, 'the rail is below the chart at 1280').toBeGreaterThanOrEqual(chartBox.y + chartBox.height - 1);

  await page.setViewportSize({ width: 1600, height: 900 });
  await expect.poll(async () => {
    const r = (await rail.boundingBox())!;
    const c = (await page.locator('.tracing-chart').boundingBox())!;
    return r.x >= c.x + c.width - 1;
  }, { message: 'the rail moves beside the chart at 1600' }).toBe(true);
});

test('TLF-04: a lane with nothing to draw shows a reason panel, and 375 px does not scroll sideways', async ({ page }) => {
  await openTimeline(page);
  await readWindow(page, WIN_EMPTY);
  const reasons = page.locator('.panel .reason');
  expect(await reasons.count(), 'at least one empty lane shows a reason panel').toBeGreaterThanOrEqual(1);
  const first = reasons.first();
  await expect(first.getByTestId('lane-status')).toBeVisible();
  const box = (await first.boundingBox())!;
  expect(box.height, 'the reason panel is a visible block').toBeGreaterThanOrEqual(70);
  const bg = await first.evaluate((el) => getComputedStyle(el).backgroundImage);
  expect(bg, 'hatched background').toContain('repeating-linear-gradient');

  await page.setViewportSize({ width: 375, height: 900 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), { timeout: 5000 }).toBe(true);
});

test('TLF-05: when the answer carries a slowness figure the API lane gets a right axis in milliseconds', async ({ page }) => {
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    const audit = body.lanes.find((lane: any) => lane.key === 'audit');
    if (audit) audit.buckets.forEach((b: any, i: number) => { b.p95Ms = 200 + (i % 5) * 100; b.p50Ms = 80 + (i % 3) * 20; });
    await route.fulfill({ response, json: body });
  });
  await openTimeline(page);
  await readWindow(page, WIN_ALL);
  const right = page.locator('.axis-right[data-lane="audit"]');
  await expect(right.locator('.tick')).toHaveCount(3);
  await expect(right.locator('.tick-bottom')).toHaveText(/^0\s?ms$|^0$/);
  await expect(right.locator('.y-title')).toContainText('slowness');
  await page.setViewportSize({ width: 375, height: 900 });
  await expect(right).toBeHidden();
});

// TLF-06  The API lane draws the Run Analysis chart: pale bars, a P95 line with dots, a dashed bridge over a gap, a hollow dot
//         per interval for its slowest request and a red diamond with its value where one is far above usual.
//         A legend and one sentence sit under the chart, and the tooltip carries the timing lines.
test('TLF-06: the API lane shows the Run Analysis chart with a legend, a red diamond and a sentence about it', async ({ page }) => {
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    const audit = body.lanes.find((lane: any) => lane.key === 'audit');
    if (audit) {
      const withCount = audit.buckets.filter((b: any) => b.count > 0);
      withCount.forEach((b: any, i: number) => {
        // The third and fourth busy intervals have no timed requests, so the line breaks and a dashed bridge crosses them.
        if (i === 2 || i === 3) { delete b.p95Ms; delete b.p50Ms; delete b.maxMs; delete b.timedCount; return; }
        b.timedCount = b.count; b.p50Ms = 120; b.p95Ms = 300; b.maxMs = i === withCount.length - 1 ? 60000 : 800;
      });
    }
    await route.fulfill({ response, json: body });
  });
  await openTimeline(page);
  await readWindow(page, WIN_ALL);
  const audit = page.locator('.panel[data-lane="audit"]');
  await expect(audit).toHaveAttribute('data-hero', 'true');
  expect(await audit.locator('svg.latency polyline.p95').count(), 'a solid P95 line').toBeGreaterThanOrEqual(1);
  expect(await audit.locator('svg.latency polyline.bridge').count(), 'a dashed bridge over the gap').toBeGreaterThanOrEqual(1);
  expect(await audit.locator('.dot').count(), 'a dot per interval with a P95').toBeGreaterThanOrEqual(2);
  expect(await audit.locator('.mark').count(), 'a mark per interval with a slowest request').toBeGreaterThanOrEqual(2);
  await expect(audit.locator('.mark.flagged')).toHaveCount(1);
  await expect(audit.locator('.mark-label')).toHaveText('60 s');

  const legend = page.locator('.hero-legend:not(.web-legend)');
  await expect(legend).toContainText('Requests per interval');
  await expect(legend).toContainText('Slow end (P95)');
  await expect(legend).toContainText('No timed requests between');
  await expect(legend).toContainText('Slowest request in the interval');
  await expect(page.getByTestId('hero-note')).toContainText('red diamond');
  await expect(page.getByTestId('hero-note')).toContainText('60 s');

  await expect(page.locator('.axis-right[data-lane="audit"] .tick-top')).toHaveText(/ms|s/);
  await expect(page.locator('.axis-right[data-lane="audit"] .y-title')).toContainText('slowness (Slow end (P95))');

  // The tooltip names the slow end and the slowest request of the interval under the pointer.
  const flagged = audit.locator('.mark.flagged');
  await audit.scrollIntoViewIfNeeded();
  const markBox = (await flagged.boundingBox())!;
  const barsBox = (await audit.locator('.bars').boundingBox())!;
  await page.mouse.move(markBox.x + markBox.width / 2, barsBox.y + barsBox.height / 2);
  await page.mouse.move(markBox.x + markBox.width / 2 + 1, barsBox.y + barsBox.height / 2 + 1);
  const tip = page.locator('#tc-tip');
  await expect(tip).toBeVisible();
  await expect(tip).toContainText('Slow end (P95)');
  await expect(tip).toContainText('Slowest request: 60 s');
  await expect(tip).toContainText('far above the usual P95');
});

test('TLF-07: with no timed requests the API lane draws bars only, without a legend or right axis', async ({ page }) => {
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    const audit = body.lanes.find((lane: any) => lane.key === 'audit');
    if (audit) audit.buckets.forEach((b: any) => { delete b.p95Ms; delete b.p50Ms; delete b.maxMs; delete b.timedCount; });
    await route.fulfill({ response, json: body });
  });
  await openTimeline(page);
  await readWindow(page, WIN_ALL);
  const audit = page.locator('.panel[data-lane="audit"]');
  await expect(audit).toHaveAttribute('data-state', 'events');
  await expect(audit).not.toHaveAttribute('data-hero', 'true');
  await expect(page.locator('.hero-legend:not(.web-legend)')).toHaveCount(0);
  await expect(page.locator('.axis-right[data-lane="audit"] .tick')).toHaveCount(0);
});

// TLF-08  The web-server (IIS) lane draws what the logs hold beyond a count: an amber part of each bar for refused or
//         not-found answers (4xx), a P95 line with dots, a red diamond for the slowest request, and a legend with a sentence.
test('TLF-08: the web-server lane shows refused answers, a response-time line and a red diamond with a legend', async ({ page }) => {
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    const iis = body.lanes.find((lane: any) => lane.key === 'iis');
    if (iis) {
      const busy = iis.buckets.filter((b: any) => b.count > 0);
      busy.forEach((b: any, i: number) => {
        b.clientErrors = Math.max(1, Math.floor(b.count / 3));
        b.timedCount = b.count; b.p50Ms = 90; b.p95Ms = 2500; b.maxMs = i === busy.length - 1 ? 45000 : 6000;
      });
    }
    await route.fulfill({ response, json: body });
  });
  await openTimeline(page);
  await readWindow(page, WIN_ALL);
  const iis = page.locator('.panel[data-lane="iis"]');
  await expect(iis).toHaveAttribute('data-state', 'events');
  await expect(iis).toHaveAttribute('data-hero', 'true');
  expect(await iis.locator('[data-testid="bar-warn"]').count(), 'an amber part on bars with refused answers').toBeGreaterThanOrEqual(1);
  expect(await iis.locator('svg.latency polyline.p95').count(), 'a solid P95 line').toBeGreaterThanOrEqual(1);
  await expect(iis.locator('.mark.flagged')).toHaveCount(1);
  await expect(iis.locator('.mark-label')).toHaveText('45 s');

  const legend = page.getByTestId('web-legend');
  await expect(legend).toContainText('Web-server requests per interval');
  await expect(legend).toContainText('Refused or not found (4xx)');
  await expect(legend).toContainText('Slow end (P95)');
  await expect(page.getByTestId('web-note')).toContainText('were refused or not found (4xx)');
  await expect(page.getByTestId('web-outlier-note')).toContainText('45 s');
});

// TLF-09  The SQL lane drawn from a saved Query Store day: statements per interval (bars), an average-time line, a red diamond for
//         the slowest statement, a legend and a sentence. The saved log is small, so the answer is reshaped in the browser.
test('TLF-09: the SQL lane shows statements per interval, an average-time line and a red diamond with a legend', async ({ page }) => {
  await page.route('**/tracing', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const response = await route.fetch();
    const body = await response.json();
    const sql = body.lanes.find((lane: any) => lane.key === 'sql');
    if (sql) {
      const start = Date.parse(body.window.axisStart);
      const step = body.window.bucketSeconds * 1000;
      const slots = Math.round((Date.parse(body.window.axisEnd) - start) / step);
      sql.state = 'events';
      sql.buckets = Array.from({ length: slots }, (_, i) => ({
        start: new Date(start + i * step).toISOString(), count: 40, errors: 0, timedCount: 40,
        avgMs: i < slots / 2 ? 20 : 120, maxMs: i === slots - 1 ? 30000 : 400
      }));
    }
    await route.fulfill({ response, json: body });
  });
  await openTimeline(page);
  await readWindow(page, WIN_ALL);
  const sql = page.locator('.panel[data-lane="sql"]');
  await expect(sql).toHaveAttribute('data-state', 'events');
  await expect(sql).toHaveAttribute('data-hero', 'true');
  expect(await sql.locator('svg.latency polyline.p95').count(), 'an average-time line').toBeGreaterThanOrEqual(1);
  await expect(sql.locator('.mark.flagged')).toHaveCount(1);
  const legend = page.getByTestId('sql-legend');
  await expect(legend).toContainText('Statements run per interval');
  await expect(legend).toContainText('Average time');
  await expect(legend).toContainText('Slowest statement in the interval');
  await expect(page.getByTestId('sql-outlier-note')).toContainText('30 s');
});
