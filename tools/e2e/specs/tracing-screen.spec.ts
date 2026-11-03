// The Investigation Timeline screen on the demo case "Orders API stall, 19 October" (phases 03 and 04):
// window picker, three lanes on one UTC axis, lane toggles, zoom/brush/keyboard, scrub pill, latency selector,
// tooltips, refusals, empty and error states, and the INV-3 / INV-4 privacy canaries.
// The demo clock is 2026-10-20T09:00:00Z. The audit lane has 11 event bars on 19 Oct 16:00 to 17:00 (5 minute
// bars) and the SQL / IIS files hold events 19 Oct 09:00 to 10:00, so two windows are used:
//   WIN_A  15:00 to 18:00  audit has bars, SQL and IIS are "none-seen"
//   WIN_B  08:00 to 12:00  all three lanes have bars
// The bucket-click hand-off, 375 px, dark-mode and archived-case specs are in tracing-handoff.spec.ts.
// Nothing here changes demo data, so the tests may run in any order.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const CASE_TITLE = 'Orders API stall, 19 October';
const WIN_A = ['2026-10-19T15:00', '2026-10-19T18:00', '2026-10-19T12:00', '2026-10-19T15:00'] as const;
const WIN_B = ['2026-10-19T08:00', '2026-10-19T12:00', '2026-10-19T04:00', '2026-10-19T08:00'] as const;
const NAMES = ['API requests (audit log)', 'Database (SQL Server)', 'Web server (IIS)'];

async function openTracing(page: Page) {
  await goHome(page);
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  // The screen traces once on open with the real "now" windows; wait for it so a later run is not confused with it.
  await expect(page.locator('.tracing-chart')).toBeVisible();
}

async function fillWindows(page: Page, w: readonly string[]) {
  const inputs = page.locator('input[type=datetime-local]');
  for (let i = 0; i < 4; i++) await inputs.nth(i).fill(w[i]);
}

/** Types the four times, presses Start tracing and waits for the answer to that very request. */
async function trace(page: Page, w: readonly string[]) {
  await fillWindows(page, w);
  const answered = page.waitForResponse((r) => r.url().includes('/tracing') && r.request().method() === 'POST' && (r.request().postData() ?? '').includes(w[0] + ':00.000Z'));
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await answered;
  await expect(page.locator('.tracing-chart')).toBeVisible();
}

// The server sends every bucket of a lane that has data, empty ones included; an empty bucket is a bar of zero height.
const bars = (page: Page, lane: string) => page.locator(`.panel[data-lane=${lane}] .bar:not([style*="height:0%"]):not([style*="height: 0%"])`);
const note = (page: Page) => page.locator('.canvas-bar .zoom-note');

test('TRACE-01: the screen opens with the picker, toolbar, chart and notes regions in that order', async ({ page }) => {
  await openTracing(page);
  await expect(page.locator('[data-region]')).toHaveCount(4);
  expect(await page.locator('[data-region]').evaluateAll((els) => els.map((e) => e.getAttribute('data-region')))).toEqual(['windows', 'toolbar', 'chart', 'notes']);
  // Picker: incident first, then comparison, four boxes, UTC note, one start button, a way back.
  const sets = page.locator('[data-region=windows] fieldset');
  await expect(sets).toHaveCount(2);
  await expect(sets.nth(0).locator('legend')).toHaveText('Incident window (UTC)');
  await expect(sets.nth(1).locator('legend')).toHaveText('Comparison period (UTC)');
  await expect(page.locator('input[type=datetime-local]')).toHaveCount(4);
  await expect(page.getByText('Times are typed in UTC.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start tracing' })).toBeEnabled();
  await expect(page.getByRole('button', { name: 'Back to the case' })).toBeVisible();
  // The defaults are the hour before now, and the hour before that (real clock, so only the shape is checked).
  const vals = await page.locator('input[type=datetime-local]').evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value));
  expect(vals.every((v) => /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(v))).toBe(true);
  expect(vals[0]).toBe(vals[3]);
  // Toolbar: three lane buttons in lane order, all on, then the export-scrub pill.
  const tool = page.getByRole('toolbar', { name: 'Timeline controls' });
  const lanes = tool.getByRole('group', { name: 'Show rows' }).getByRole('button');
  expect(await lanes.allInnerTexts()).toEqual(['API audit', 'SQL', 'IIS']);
  for (let i = 0; i < 3; i++) await expect(lanes.nth(i)).toHaveAttribute('aria-pressed', 'true');
  // Back to the case returns focus to the open button.
  await page.getByRole('button', { name: 'Back to the case' }).click();
  await expect(page.getByRole('button', { name: 'Open the Investigation Timeline' })).toBeFocused();
});

test('TRACE-02: three lanes in order with plain labels, one shared UTC axis and its caption', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_A);
  expect(await page.locator('.lane-label strong').allInnerTexts()).toEqual(NAMES);
  expect(await page.locator('.panel[data-lane]').evaluateAll((els) => els.map((e) => e.getAttribute('data-lane')))).toEqual(['audit', 'sql', 'iis']);
  // Each lane head asks its plain question and says where the data came from.
  const heads = page.locator('.lane-label');
  await expect(heads.nth(0)).toContainText('Was the application slow or failing?');
  await expect(heads.nth(1)).toContainText('Were database calls slow, blocked or failing?');
  await expect(heads.nth(2)).toContainText('Were web requests slow or failing before reaching the app?');
  await expect(heads.nth(0).locator('.origin')).toContainText('Read just now');
  await expect(heads.nth(1).locator('.origin')).toContainText('From saved logs');
  await expect(heads.nth(2).locator('.origin')).toContainText('From saved logs');
  // Panel titles name what is plotted.
  await expect(page.locator('.panel[data-lane=audit] .panel-title')).toHaveText('How many requests, and how slow they were');
  await expect(page.locator('.panel[data-lane=sql] .panel-title')).toHaveText('Slow database calls (over 1 s) and failed calls');
  await expect(page.locator('.panel[data-lane=iis] .panel-title')).toHaveText('Web-server requests (errors shown striped)');
  // One axis for all lanes: 3 h in 5 minute bars = 36 columns, at most six time labels (thinned to the measured plot width so
  // none overlap), the UTC caption, no "outside the window" dashes.
  await expect(page.locator('.overlay .column')).toHaveCount(36);
  const ticks = page.locator('.axtick');
  expect(await ticks.count()).toBeGreaterThanOrEqual(3);
  expect(await ticks.count()).toBeLessThanOrEqual(6);
  const boxes = await ticks.evaluateAll((els) => els.map((e) => { const r = e.getBoundingClientRect(); return [r.left, r.right]; }));
  for (let i = 1; i < boxes.length; i++) expect(boxes[i][0]).toBeGreaterThanOrEqual(boxes[i - 1][1]);
  await expect(page.locator('.axtick').first()).toHaveText('19 Oct 15:00 UTC');
  await expect(page.locator('.axcap')).toContainText('Time (UTC). Each bar covers 5 minutes, starting at the time on its left edge; every panel uses the same time axis.');
  await expect(page.locator('.outside-dash')).toHaveCount(0);
  // The mixed-time note is there (the audit lane is live) and cannot be closed.
  const mixed = page.getByRole('note').filter({ hasText: 'Some data was collected at different times.' });
  await expect(mixed).toContainText('This note cannot be dismissed.');
  await expect(mixed.getByRole('button')).toHaveCount(0);
});

test('TRACE-03: the audit lane draws exactly 11 event bars, scaled to its tallest; empty lanes say so and draw none', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_A);
  await expect(bars(page, 'audit')).toHaveCount(11);
  await expect(page.locator('.panel[data-lane=audit]')).toHaveAttribute('data-state', 'events');
  // The left axis shows the top of the scale (the tallest bar's count) and 0; the tallest bar fills the plot.
  const axis = page.locator('.axis-left').first();
  await expect(axis.locator('.tick-top')).toHaveText(/^[1-9]\d*$/);
  await expect(axis.locator('.tick-bottom')).toHaveText('0');
  const heights = await bars(page, 'audit').evaluateAll((els) => els.map((e) => parseFloat((e as HTMLElement).style.height)));
  expect(Math.max(...heights)).toBe(100);
  expect(Math.min(...heights)).toBeGreaterThan(0);
  // SQL and IIS have files that cover the time but nothing happened inside it.
  for (const lane of ['sql', 'iis']) {
    const p = page.locator(`.panel[data-lane=${lane}]`);
    await expect(p).toHaveAttribute('data-state', 'none-seen');
    await expect(p.getByText('No events in this period')).toBeVisible();
    await expect(p.locator('.bar')).toHaveCount(0);
  }
  // With no 5xx in the demo audit rows, no bar carries an error share.
  await expect(page.locator('.panel[data-lane=audit] .bar-err')).toHaveCount(0);
});

test('TRACE-04: all three lanes draw bars on a window with SQL and IIS events; lane colours come from the design tokens', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_B);
  await expect(page.locator('.overlay .column')).toHaveCount(48);
  await expect(bars(page, 'audit')).toHaveCount(2);
  await expect(bars(page, 'sql')).toHaveCount(11);
  await expect(bars(page, 'iis')).toHaveCount(10);
  // Lane swatches: computed colour equals the CSS variable's colour, never a hex literal.
  const same = await page.evaluate(() => {
    const probe = (v: string) => {
      const d = document.createElement('div');
      d.style.background = `var(${v})`;
      document.body.appendChild(d);
      const c = getComputedStyle(d).backgroundColor;
      d.remove();
      return c;
    };
    const swatches = [...document.querySelectorAll('.lane-label .swatch')].map((s) => getComputedStyle(s).backgroundColor);
    const tools = [...document.querySelectorAll('.tt-swatch')].map((s) => getComputedStyle(s).backgroundColor);
    const want = ['--lane-audit', '--lane-sql', '--lane-iis'].map(probe);
    return { swatches, tools, want, bar: getComputedStyle(document.querySelector('.panel[data-lane=sql] .bar')!).backgroundColor, accent: probe('--accent') };
  });
  expect(same.want.every((c) => c !== 'rgba(0, 0, 0, 0)')).toBe(true);
  expect(same.swatches).toEqual(same.want);
  expect(same.tools).toEqual(same.want);
  expect(new Set(same.want).size).toBe(3);
  // Bars are filled with their lane colour token.
  expect(same.bar).toBe(same.want[1]);
  // The IIS and SQL lanes are stored reads and carry an "as of" time in the lane head.
  await expect(page.locator('.lane-label').nth(1).locator('.origin')).toContainText('From saved logs');
});

test('TRACE-05: lane toggles hide and restore rows in order without a new request', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_B);
  let posts = 0;
  page.on('request', (r) => { if (r.url().includes('/tracing') && r.method() === 'POST') posts++; });
  const tool = page.getByRole('toolbar', { name: 'Timeline controls' });
  const sql = tool.getByRole('button', { name: 'SQL' });
  await sql.click();
  await expect(sql).toHaveAttribute('aria-pressed', 'false');
  await expect(page.locator('.panel[data-lane=sql]')).toHaveCount(0);
  expect(await page.locator('.lane-label strong').allInnerTexts()).toEqual([NAMES[0], NAMES[2]]);
  await expect(page.locator('.overlay .column')).toHaveCount(48);
  // Hide the rest: the plain all-hidden sentence, no lanes.
  await tool.getByRole('button', { name: 'API audit' }).click();
  await tool.getByRole('button', { name: 'IIS' }).click();
  await expect(page.getByText('All rows are hidden. Turn a row on to see it.')).toBeVisible();
  await expect(page.locator('.panel[data-lane]')).toHaveCount(0);
  // Turn them back on in a different order: lane order is still API, SQL, IIS.
  await tool.getByRole('button', { name: 'IIS' }).click();
  await sql.click();
  await tool.getByRole('button', { name: 'API audit' }).click();
  expect(await page.locator('.lane-label strong').allInnerTexts()).toEqual(NAMES);
  await expect(page.getByText('All rows are hidden. Turn a row on to see it.')).toHaveCount(0);
  expect(posts, 'toggling a row asks the server for nothing').toBe(0);
});

test('TRACE-06: zoom in, zoom out and reset by button and by keyboard (+, -, 0), and zoom never asks the server again', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_A);
  let posts = 0;
  page.on('request', (r) => { if (r.url().includes('/tracing') && r.method() === 'POST') posts++; });
  const bar = page.getByRole('toolbar', { name: 'Chart controls' });
  const zin = bar.getByRole('button', { name: 'Zoom in' });
  const zout = bar.getByRole('button', { name: 'Zoom out' });
  const reset = bar.getByRole('button', { name: 'Reset zoom' });
  await expect(zout).toBeDisabled();
  await expect(reset).toBeDisabled();
  await expect(zin).toBeEnabled();
  await expect(bar).toContainText('Drag across the chart to zoom into those bars.');
  await expect(note(page)).toHaveText('');
  await zin.click();
  await expect(note(page)).toHaveText('Showing 18 of 36 bars.');
  await expect(page.locator('.overlay .column')).toHaveCount(18);
  await expect(reset).toBeEnabled();
  await zout.click();
  await expect(note(page)).toHaveText('');
  await expect(page.locator('.overlay .column')).toHaveCount(36);
  // Keyboard on the bucket selector.
  await page.locator('.overlay').focus();
  await page.keyboard.press('Equal');
  await expect(note(page)).toHaveText('Showing 18 of 36 bars.');
  await page.keyboard.press('Equal');
  await page.keyboard.press('Equal');
  await expect(note(page)).toHaveText('Showing 4 of 36 bars.');
  await expect(zin).toBeDisabled();
  await page.keyboard.press('Minus');
  await expect(note(page)).toHaveText('Showing 8 of 36 bars.');
  await page.keyboard.press('Digit0');
  await expect(note(page)).toHaveText('');
  await expect(page.locator('.overlay .column')).toHaveCount(36);
  await zin.click();
  await reset.click();
  await expect(zout).toBeDisabled();
  expect(posts, 'zoom is a view change only').toBe(0);
});

test('TRACE-07: dragging across the bars shows a brush and zooms to those bars', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_A);
  const box = (await page.locator('.overlay').boundingBox())!;
  const colW = box.width / 36;
  const y = box.y + 20;
  await page.mouse.move(box.x + colW * 4.5, y);
  await page.mouse.down();
  await page.mouse.move(box.x + colW * 8.5, y, { steps: 4 });
  await page.mouse.move(box.x + colW * 11.5, y, { steps: 4 });
  await expect(page.locator('.brush')).toBeVisible();
  await page.mouse.up();
  await expect(page.locator('.brush')).toHaveCount(0);
  await expect(note(page)).toHaveText('Showing 8 of 36 bars.');
  await expect(page.locator('.overlay .column')).toHaveCount(8);
  // The axis ticks now start where the brush started (19 Oct 15:20 = slot 4).
  await expect(page.locator('.axtick').first()).toHaveText('19 Oct 15:20 UTC');
  // A drag is not a click: nothing was selected and no analysis was opened.
  await expect(page.locator('.column.selected')).toHaveCount(0);
  await page.getByRole('button', { name: 'Reset zoom' }).click();
  await expect(page.locator('.overlay .column')).toHaveCount(36);
});

test('TRACE-08: the scrub pill states the export setting in plain words and never says stored data is masked', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_A);
  const pill = page.getByRole('toolbar', { name: 'Timeline controls' }).locator('button.pill');
  const text = (await pill.innerText()).trim();
  expect(['Default: export hides IP, referer and cookie hash', 'Export hides IP, referer and cookie hash', 'Export includes IP, referer and cookie hash (scrub is off)']).toContain(text);
  expect(text).not.toMatch(/mask|redact|delet|remov/i);
  await expect(pill).toHaveAttribute('aria-expanded', 'false');
  // The same words are repeated in a polite live region for screen readers.
  await expect(page.getByRole('toolbar', { name: 'Timeline controls' }).getByRole('status')).toHaveText(text);
  await pill.click();
  await expect(pill).toHaveAttribute('aria-expanded', 'true');
  await expect(page.getByText('This is the setting the next evidence export will use. Nothing saved in the case is changed here.')).toBeVisible();
  await pill.click();
  await expect(pill).toHaveAttribute('aria-expanded', 'false');
});

test('TRACE-09: P50 / P95 selector: a note without latency figures, and a line when buckets carry them', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_A);
  const group = page.getByRole('group', { name: 'Latency line' });
  const p50 = group.getByRole('button', { name: 'Typical (P50)' });
  const p95 = group.getByRole('button', { name: 'Slow end (P95)' });
  expect(await group.getByRole('button').allInnerTexts()).toEqual(['Typical (P50)', 'Slow end (P95)']);
  await expect(p95).toHaveAttribute('aria-pressed', 'true');
  await expect(p50).toHaveAttribute('aria-pressed', 'false');
  // The audit rollup returns counts only until benchmark BM-04, so the real answer carries no latency.
  await expect(page.getByText('No latency figures were returned for this window.')).toBeVisible();
  await expect(page.locator('svg.latency')).toHaveCount(0);
  await p50.click();
  await expect(p50).toHaveAttribute('aria-pressed', 'true');
  await expect(p95).toHaveAttribute('aria-pressed', 'false');

  // Same real answer, with latency figures added to the audit buckets on the way in.
  await page.route('**/api/cases/*/tracing', async (route) => {
    const res = await route.fetch();
    const json = await res.json();
    for (const lane of json.lanes) {
      if (lane.key !== 'audit') continue;
      lane.buckets.forEach((b: any, i: number) => { b.p50Ms = 100 + i * 10; b.p95Ms = 400 + i * 50; });
    }
    await route.fulfill({ response: res, json });
  });
  await trace(page, WIN_A);
  await expect(page.getByText('No latency figures were returned for this window.')).toHaveCount(0);
  const line = page.locator('svg.latency');
  await expect(line).toHaveCount(1);
  // The chart stays mounted across a re-run, so the choice made above (Typical, P50) is kept.
  await expect(line).toHaveAttribute('data-percentile', 'p50');
  await expect(page.locator('.latency-max')).toHaveText('Typical (P50): up to 200 ms');
  await group.getByRole('button', { name: 'Slow end (P95)' }).click();
  await expect(line).toHaveAttribute('data-percentile', 'p95');
  await expect(page.locator('.latency-max')).toHaveText('Slow end (P95): up to 900 ms');
  await group.getByRole('button', { name: 'Typical (P50)' }).click();
  await expect(line).toHaveAttribute('data-percentile', 'p50');
  expect(await line.locator('polyline').count()).toBeGreaterThan(0);
  // The line colour is the text token, not a literal.
  const stroke = await page.evaluate(() => {
    const d = document.createElement('div');
    d.style.color = 'var(--text)';
    document.body.appendChild(d);
    const want = getComputedStyle(d).color;
    d.remove();
    return { got: getComputedStyle(document.querySelector('svg.latency polyline')!).stroke, want };
  });
  expect(stroke.got).toBe(stroke.want);
});

test('TRACE-10: hover and keyboard tooltips come from the dictionary; lane heads show their hint on hover and are not tab stops', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_B);
  const overlay = (await page.locator('.overlay').boundingBox())!;
  const colW = overlay.width / 48;
  const mid = async (lane: string) => { await page.locator(`.panel[data-lane=${lane}]`).scrollIntoViewIfNeeded(); const b = (await page.locator(`.panel[data-lane=${lane}]`).boundingBox())!; return b.y + b.height / 2; };
  // SQL lane, slot 13 (09:05): calls from saved logs.
  await page.mouse.move(overlay.x + colW * 13.5, await mid('sql'));
  const tip = page.locator('#tc-tip');
  await expect(tip).toBeVisible();
  await expect(tip).toContainText('Database (SQL Server)');
  await expect(tip).toContainText('19 Oct 09:05 to 19 Oct 09:10 UTC');
  await expect(tip).toContainText(/\d+ calls?/);
  await expect(tip).toContainText('From saved logs (as of ');
  await expect(tip).not.toContainText('API requests (audit log)');
  // IIS lane: the "IIS point" caveat from the dictionary.
  await page.mouse.move(overlay.x + colW * 13.5, await mid('iis'));
  await expect(tip).toContainText('Web server (IIS)');
  await expect(tip).toContainText(/\d+ requests?/);
  await expect(tip).toContainText('Drawn at its timestamp until the source semantics are confirmed.');
  // Leaving the chart hides it.
  await page.mouse.move(overlay.x - 40, overlay.y - 40);
  await expect(tip).toHaveText('');
  // Keyboard: arrows move the selected bucket and show every lane that draws bars; Escape closes the tooltip.
  await page.locator('.overlay').focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('.column.selected')).toHaveCount(1);
  await expect(tip).toBeVisible();
  await expect(tip).toContainText('API requests (audit log)');
  await expect(tip).toContainText('Database (SQL Server)');
  await expect(tip).toContainText('Web server (IIS)');
  await expect(tip).toContainText('No events in this bucket');
  await page.keyboard.press('Escape');
  await expect(tip).toHaveText('');
  // Lane heads have no action, so they are not tab stops (UI-R2-07); the dictionary hint shows on hover and stays
  // available to assistive technology through aria-describedby.
  await expect(page.locator('.lane-label[tabindex]')).toHaveCount(0);
  const head = page.locator('.lane-label').first();
  await head.hover();
  await expect(head.getByRole('tooltip')).toBeVisible();
  await expect(head.getByRole('tooltip')).toContainText('Requests recorded in the API audit log.');
  await page.locator('.lane-label').nth(2).hover();
  await expect(page.locator('.lane-label').nth(2).getByRole('tooltip')).toContainText('Requests recorded in the web server log.');
});

test('TRACE-11: bad windows are refused in plain words with no request; an overlap only warns and can be fixed', async ({ page }) => {
  await openTracing(page);
  let posts = 0;
  page.on('request', (r) => { if (r.url().includes('/tracing') && r.method() === 'POST') posts++; });
  const start = page.getByRole('button', { name: 'Start tracing' });
  // Ends before it starts.
  await fillWindows(page, ['2026-10-19T18:00', '2026-10-19T15:00', '2026-10-19T12:00', '2026-10-19T15:00']);
  await start.click();
  await expect(page.getByRole('alert')).toContainText('The incident window end must be later than the start.');
  // 30 days and one minute.
  await fillWindows(page, ['2026-09-01T00:00', '2026-10-01T00:01', '2026-08-01T00:00', '2026-09-01T00:00']);
  await start.click();
  await expect(page.getByRole('alert')).toContainText('The longest window is 30 days. Shorten the incident window.');
  // A missing time.
  await page.locator('input[type=datetime-local]').nth(2).fill('');
  await start.click();
  await expect(page.getByRole('alert')).toContainText('The comparison period needs a start and an end, in UTC.');
  expect(posts, 'a refused window sends nothing').toBe(0);
  // Overlap: a warning, the run goes ahead, and one button moves the comparison period before the incident.
  await fillWindows(page, ['2026-10-19T15:00', '2026-10-19T18:00', '2026-10-19T16:00', '2026-10-19T17:00']);
  const answered = page.waitForResponse((r) => r.url().includes('/tracing') && r.request().method() === 'POST');
  await start.click();
  await answered;
  await expect(page.getByRole('alert')).toHaveCount(0);
  const warn = page.getByRole('status').filter({ hasText: 'The two windows overlap.' });
  await expect(warn).toContainText('Pick a normal period that does not share any time with the incident.');
  await expect(start).toBeEnabled();
  await warn.getByRole('button', { name: 'Move comparison period before the incident' }).click();
  const inputs = page.locator('input[type=datetime-local]');
  expect(await inputs.evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value))).toEqual(['2026-10-19T15:00', '2026-10-19T18:00', '2026-10-19T12:00', '2026-10-19T15:00']);
  await expect(page.getByText('The two windows overlap.')).toHaveCount(0);
  expect(posts).toBe(1);
});

test('TRACE-12: a window with no events, a failed read (Try again) and Cancel while reading', async ({ page, allow }) => {
  allow(/503|Failed to load resource/, 'the test answers the tracing request with a 503 on purpose');
  await openTracing(page);
  // No events anywhere in the window: every lane says so, nothing is drawn, the chart controls still work.
  await trace(page, ['2026-10-01T00:00', '2026-10-01T03:00', '2026-09-30T20:00', '2026-10-01T00:00']);
  for (const lane of ['audit', 'sql', 'iis']) {
    const p = page.locator(`.panel[data-lane=${lane}]`);
    await expect(p.getByText('No events in this period')).toBeVisible();
    await expect(p.locator('.bar')).toHaveCount(0);
  }
  await expect(page.locator('.overlay .column')).toHaveCount(36);

  // The case store is unavailable: the plain sentence and Try again; a second try (now answered) draws the chart.
  let fail = true;
  await page.route('**/api/cases/*/tracing', async (route) => {
    if (fail) await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'store down' }) });
    else await route.continue();
  });
  await fillWindows(page, WIN_A);
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await expect(page.locator('[data-region=chart]').getByRole('status')).toHaveText('The case store is unavailable right now.');
  await expect(page.locator('.tracing-chart')).toHaveCount(0);
  fail = false;
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(bars(page, 'audit')).toHaveCount(11);
  // Focus stays in the chart region instead of dropping to the page (UI-R2-02).
  await expect(page.locator('[data-region=chart]')).toBeFocused();

  // Cancel while the read is slow: the loading line goes and nothing is drawn.
  await page.unroute('**/api/cases/*/tracing');
  await page.route('**/api/cases/*/tracing', async (route) => {
    await new Promise((r) => setTimeout(r, 3000));
    await route.continue().catch(() => {});
  });
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await expect(page.getByText('Reading the window…')).toBeVisible();
  await page.getByRole('button', { name: 'Cancel' }).click();
  await expect(page.getByText('Reading the window…')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Start tracing' })).toBeEnabled();
  await page.waitForTimeout(3500);
  await expect(page.getByText('Reading the window…')).toHaveCount(0);
});

test('TRACE-13: a case with no sources shows the plain empty sentence', async ({ page }) => {
  await openTracing(page);
  await page.route('**/api/cases/*/tracing', async (route) => {
    const res = await route.fetch();
    const json = await res.json();
    json.sources = [];
    json.lanes = [];
    await route.fulfill({ response: res, json });
  });
  await fillWindows(page, WIN_A);
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await expect(page.locator('[data-region=chart]').getByRole('status')).toHaveText('No files have been added to this case yet. Add a source first.');
  await expect(page.locator('.tracing-chart')).toHaveCount(0);
});

test('TRACE-14: privacy canaries: one read-only POST per run with only windows in it, no IP, cookie, query string or user agent on screen or in the answer, no .unravis.db', async ({ page, demo }) => {
  await openTracing(page);
  const bodies: string[] = [];
  const methods: string[] = [];
  page.on('request', (r) => {
    if (!r.url().includes('/api/cases/')) return;
    if (r.method() !== 'GET') { methods.push(r.method() + ' ' + new URL(r.url()).pathname.replace(/\/cases\/[^/]+/, '/cases/{id}')); }
    if (r.url().endsWith('/tracing')) bodies.push(r.postData() ?? '');
  });
  const answered = page.waitForResponse((r) => r.url().endsWith('/tracing') && (r.request().postData() ?? '').includes('2026-10-19T08:00:00.000Z'));
  await fillWindows(page, WIN_B);
  await page.getByRole('button', { name: 'Start tracing' }).click();
  const res = await answered;
  const raw = await res.text();
  await expect(page.locator('.tracing-chart')).toBeVisible();
  // Toggling and zooming send nothing more.
  await page.getByRole('button', { name: 'Zoom in' }).click();
  await page.getByRole('toolbar', { name: 'Timeline controls' }).getByRole('button', { name: 'IIS' }).click();
  expect(methods).toEqual(['POST /api/cases/{id}/tracing']);
  expect(bodies).toHaveLength(1);
  expect(Object.keys(JSON.parse(bodies[0])).sort()).toEqual(['incident', 'reference', 'requestSeq']);
  // Nothing sensitive in the answer or on the screen (the pill's words "IP, referer and cookie hash" are the only mention).
  const lanes = JSON.parse(raw).lanes as Array<{ events?: Array<Record<string, unknown>> }>;
  const SENSITIVE = [/\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b/, /\[[0-9a-f:]{6,}\]/i, /Mozilla|MSIE|Trident|AppleWebKit|curl\//i, /\bcs\(|cookie=|ASP\.NET_SessionId|\.AspNet|set-cookie/i, /\?[A-Za-z_]+=/];
  const screen = await page.locator('.tracing-paper').innerText();
  for (const re of SENSITIVE) {
    expect(raw, 'the answer matches ' + re).not.toMatch(re);
    expect(screen, 'the screen matches ' + re).not.toMatch(re);
  }
  // INV-4: no case-store file named .unravis.db was created while tracing.
  const found: string[] = [];
  const walk = (dir: string) => {
    for (const name of readdirSync(dir)) {
      const full = join(dir, name);
      if (name.toLowerCase().endsWith('.unravis.db')) found.push(name);
      try { if (statSync(full).isDirectory()) walk(full); } catch { /* a file that vanished */ }
    }
  };
  walk(demo.dataDir);
  expect(found).toEqual([]);
});


test('TRACE-16: with no tooltip to show, the tooltip element is not drawn at all', async ({ page }) => {
  await openTracing(page);
  await trace(page, WIN_A);
  await expect(page.locator('#tc-tip')).toBeHidden();
});

test.describe('time zone', () => {
  test.use({ timezoneId: 'Asia/Kolkata' });
  test('TRACE-15: the UTC / Local switch moves the axis labels and caption only; the window and the data stay', async ({ page }) => {
    await openTracing(page);
    await trace(page, WIN_A);
    let posts = 0;
    page.on('request', (r) => { if (r.url().includes('/tracing') && r.method() === 'POST') posts++; });
    await expect(page.locator('.axcap')).toContainText('Time (UTC).');
    const group = page.getByRole('group', { name: 'Show times in' });
    await group.getByRole('button', { name: /^Local/ }).click();
    await expect(page.locator('.axcap')).not.toContainText('Time (UTC).');
    await expect(page.locator('.axtick').first()).toHaveText(/^19 Oct 20:30 /);
    // The picker still says UTC and still holds the UTC times typed.
    await expect(page.getByText('Times are typed in UTC.')).toBeVisible();
    await expect(page.locator('input[type=datetime-local]').first()).toHaveValue('2026-10-19T15:00');
    await expect(bars(page, 'audit')).toHaveCount(11);
    await group.getByRole('button', { name: 'UTC', exact: true }).click();
    await expect(page.locator('.axtick').first()).toHaveText('19 Oct 15:00 UTC');
    expect(posts).toBe(0);
  });
});
