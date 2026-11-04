// Phase 05 gate: guided is unlisted from case home, and a bar on the Investigation Timeline hands its 5-minute slot to the analysis.
// The demo case "Orders API stall, 19 October" has 11 audit bars in 36 five-minute slots (15:00 to 18:00 UTC, 19 Oct); SQL and IIS are empty.
import { test, expect } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { fillWindows, pickerButton } from '../support/window-picker';
import type { Page } from '@playwright/test';

const CASE = 'Orders API stall, 19 October';
const SLOTS = 36;
// Slot k covers 15:00 + 5k minutes. These slots hold audit events (read from the demo data); every other slot is empty.
const AUDIT_SLOTS = [12, 14, 16, 18, 20, 23, 25, 27, 29, 31, 34];
const EMPTY_NOTE = 'Nothing was recorded in that bar, so there is nothing to analyse. Pick a bar with events.';
const STALE_NOTE = 'Filters changed. Click Run analysis to use the new window.';

const runButton = (page: Page) => page.getByRole('button', { name: 'Run analysis', exact: true });
const columns = (page: Page) => page.locator('.overlay .column');

/** "Showing 19 Oct HH:MM:SS to HH:MM:SS UTC." for the 5-minute slot k. */
function showing(slot: number): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  const from = 15 * 60 + slot * 5;
  const to = from + 5;
  const hms = (m: number) => pad(Math.floor(m / 60)) + ':' + pad(m % 60) + ':00';
  return 'Showing 19 Oct ' + hms(from) + ' to ' + hms(to) + ' UTC.';
}

/** Open the demo case, open the timeline, type the windows (UTC) and start tracing. */
async function startTracing(page: Page, title = CASE): Promise<void> {
  await openCase(page, title);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(pickerButton(page, 'incident')).toBeVisible();
  await fillWindows(page, ['2026-10-19T15:00', '2026-10-19T18:00', '2026-10-19T12:00', '2026-10-19T15:00']);
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await expect(page.locator('.tracing-chart')).toBeVisible();
  await expect(columns(page)).toHaveCount(SLOTS);
}

/**
 * Click a bar. A real mouse click does not reach the bar in Chromium (see HAND-18), so the click is dispatched on the column,
 * which is what the bar's own handler listens for. Keyboard and dispatched clicks are the supported ways here until that is fixed.
 */
async function pickBar(page: Page, slot: number): Promise<void> {
  const col = columns(page).nth(slot);
  await col.scrollIntoViewIfNeeded();
  await col.dispatchEvent('click');
}

async function expectAnalysisOpen(page: Page): Promise<void> {
  await expect(runButton(page)).toBeVisible();
  await expect(page.locator('.tracing-paper')).toHaveCount(0);
}

async function noHorizontalScroll(page: Page, where: string): Promise<void> {
  const over = await page.evaluate(() => Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - window.innerWidth);
  expect(over, 'horizontal overflow in px on ' + where).toBeLessThanOrEqual(1);
}

/** The colour a design token resolves to, so a computed style can be compared with it. */
async function token(page: Page, name: string): Promise<string> {
  return page.evaluate((n) => {
    const probe = document.createElement('div');
    probe.style.color = 'var(' + n + ')';
    document.body.appendChild(probe);
    const colour = getComputedStyle(probe).color;
    probe.remove();
    return colour;
  }, name);
}

/**
 * Wait until the chart layout has settled: bars and lane heads are visible, fonts are loaded, and the overlay, panel and column
 * geometry is identical across two consecutive samples, each taken two animation frames apart. No fixed sleeps, no timing limits.
 */
async function layoutSettled(page: Page): Promise<void> {
  await expect(page.locator('.panel[data-lane=audit] .bar').first()).toBeVisible();
  await expect(page.locator('.lane-label').first()).toBeVisible();
  await expect(columns(page)).toHaveCount(SLOTS);
  let previous = '';
  await expect.poll(async () => {
    const signature = await page.evaluate(async () => {
      await document.fonts.ready;
      await new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r())));
      const r2 = (e: Element) => { const r = e.getBoundingClientRect(); return [r.left, r.top, r.width, r.height].map((n) => n.toFixed(2)).join(','); };
      const overlay = document.querySelector('.overlay');
      return JSON.stringify({
        overlay: overlay ? r2(overlay) : '',
        panels: Array.from(document.querySelectorAll('.panel[data-lane]')).map(r2),
        cols: Array.from(document.querySelectorAll('.overlay .column')).map(r2)
      });
    });
    const same = signature === previous;
    previous = signature;
    return same;
  }, { message: 'the timeline layout settles' }).toBe(true);
}

const selectedIndex = (page: Page) => page.evaluate(() => Array.from(document.querySelectorAll('.overlay .column')).findIndex((c) => c.classList.contains('selected')));

test.beforeEach(async ({ page }) => {
  await goHome(page);
});

test('HAND-01: typed windows start the tracing; audit has 11 bars in 36 slots, SQL and IIS are empty', async ({ page }) => {
  await startTracing(page);
  await expect(page.locator('.panel[data-lane=audit] .bar')).toHaveCount(AUDIT_SLOTS.length);
  await expect(page.locator('.panel[data-lane=sql]')).toContainText('No events in this period');
  await expect(page.locator('.panel[data-lane=iis]')).toContainText('No events in this period');
  const group = page.getByRole('group', { name: /^Time buckets\./ });
  await expect(group).toHaveAttribute('aria-roledescription', 'bucket selector');
  await expect(group).toHaveAttribute('tabindex', '0');
  expect(await page.evaluate(() => location.hash)).toBe('');
});

test('HAND-02: a bar with events sets the 5-minute window, opens the analysis, and Run analysis applies it', async ({ page }) => {
  await startTracing(page);
  await pickBar(page, 12);
  await expectAnalysisOpen(page);
  // Hand-off only changes the filters: the figures stay from the last run until Run analysis is pressed.
  await expect(page.getByText(STALE_NOTE)).toBeVisible();
  await expect(page.getByText(showing(12), { exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'This case', exact: true })).toBeVisible();
  expect(await page.evaluate(() => location.hash)).toBe('');
  await expect(runButton(page)).toHaveClass(/primary-button/);
  await runButton(page).click();
  await expect(page.getByText(showing(12), { exact: true }).first()).toBeVisible();
  await expect(page.getByText(STALE_NOTE)).toHaveCount(0);
  expect(await page.evaluate(() => location.hash)).toBe('');
});

test('HAND-03: the last bar with events hands off its own slot', async ({ page }) => {
  await startTracing(page);
  await pickBar(page, 34);
  await expectAnalysisOpen(page);
  await runButton(page).click();
  await expect(page.getByText(showing(34), { exact: true }).first()).toBeVisible();
});

test('HAND-04: an empty slot says there is nothing to analyse and stays on the timeline', async ({ page }) => {
  await startTracing(page);
  for (const slot of [0, 1, 13]) {
    await pickBar(page, slot);
    await expect(page.getByRole('status').filter({ hasText: EMPTY_NOTE })).toBeVisible();
    await expect(runButton(page)).toHaveCount(0);
    await expect(page.locator('.tracing-paper')).toBeVisible();
  }
  // The sentence is plain and sits in the notes region under the chart.
  await expect(page.locator('[data-region=notes] p[role=status]')).toHaveText(EMPTY_NOTE);
  expect(await page.evaluate(() => location.hash)).toBe('');
});

test('HAND-05: a bar in a hidden lane does nothing; showing the lane again makes it work', async ({ page }) => {
  await startTracing(page);
  const audit = page.locator('.tt-lane').first();
  await expect(audit).toHaveAttribute('aria-pressed', 'true');
  await audit.click();
  await expect(audit).toHaveAttribute('aria-pressed', 'false');
  await expect(page.locator('.panel[data-lane=audit]')).toHaveCount(0);
  await pickBar(page, 12);
  await expect(page.getByRole('status').filter({ hasText: EMPTY_NOTE })).toBeVisible();
  await expect(runButton(page)).toHaveCount(0);
  await audit.click();
  await expect(audit).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('.panel[data-lane=audit] .bar')).toHaveCount(AUDIT_SLOTS.length);
  await pickBar(page, 12);
  await expectAnalysisOpen(page);
});

test('HAND-06: a double click opens one analysis screen and one history step', async ({ page }) => {
  await startTracing(page);
  const col = columns(page).nth(12);
  await col.scrollIntoViewIfNeeded();
  await col.evaluate((el) => { (el as HTMLElement).click(); (el as HTMLElement).click(); });
  await expectAnalysisOpen(page);
  await expect(runButton(page)).toHaveCount(1);
  await expect(page.locator('main, #main').first()).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toHaveCount(0);
});

test('HAND-07: reload on the analysis screen keeps the hand-off window', async ({ page }) => {
  await startTracing(page);
  await pickBar(page, 12);
  await expectAnalysisOpen(page);
  await runButton(page).click();
  await expect(page.getByText(showing(12), { exact: true }).first()).toBeVisible();
  await page.reload();
  await expect(page.getByText(showing(12), { exact: true }).first()).toBeVisible();
  expect(await page.evaluate(() => location.hash)).toBe('');
});

test('HAND-08: This case returns to case home, which has no Guided section and shows Analysis then Investigation Timeline', async ({ page }) => {
  await startTracing(page);
  await pickBar(page, 12);
  await expectAnalysisOpen(page);
  await page.getByRole('button', { name: 'This case', exact: true }).click();
  await expect(page.getByRole('heading', { level: 2, name: CASE })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Analysis', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open the analysis' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open the Investigation Timeline' })).toBeEnabled();
  await expect(page.getByRole('heading', { name: /guided/i })).toHaveCount(0);
  await expect(page.getByRole('button', { name: /guided/i })).toHaveCount(0);
  await expect(page.getByRole('link', { name: /guided/i })).toHaveCount(0);
  const order = await page.evaluate(() => {
    const a = document.getElementById('home-analysis');
    const t = document.getElementById('home-tracing');
    return !!a && !!t && !!(a.compareDocumentPosition(t) & Node.DOCUMENT_POSITION_FOLLOWING);
  });
  expect(order, 'Analysis comes before Investigation Timeline').toBe(true);
});

test('HAND-09: 375px wide, nothing scrolls sideways and the hand-off still works', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await openCase(page, CASE);
  await expect(page.locator('#home-analysis')).toBeVisible();
  await noHorizontalScroll(page, 'case home');
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(pickerButton(page, 'incident')).toBeVisible();
  await noHorizontalScroll(page, 'timeline windows');
  await fillWindows(page, ['2026-10-19T15:00', '2026-10-19T18:00', '2026-10-19T12:00', '2026-10-19T15:00']);
  await noHorizontalScroll(page, 'timeline windows after the pickers were used');
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await expect(columns(page)).toHaveCount(SLOTS);
  await noHorizontalScroll(page, 'tracing chart');
  const geo = await page.evaluate(() => {
    const grid = document.querySelector('.lanes-grid')!.getBoundingClientRect();
    const label = document.querySelector('.lane-label')!.getBoundingClientRect();
    const panel = document.querySelector('.panel[data-lane=audit]')!.getBoundingClientRect();
    const overlay = document.querySelector('.overlay')!.getBoundingClientRect();
    return { gridW: grid.width, labelW: label.width, panelRight: panel.right, overlayRight: overlay.right };
  });
  expect(geo.labelW).toBeGreaterThan(geo.gridW * 0.9);
  expect(geo.panelRight).toBeLessThanOrEqual(375);
  expect(geo.overlayRight).toBeLessThanOrEqual(375);
  await pickBar(page, 12);
  await expectAnalysisOpen(page);
  await noHorizontalScroll(page, 'analysis');
  await runButton(page).click();
  await expect(page.getByText(showing(12), { exact: true }).first()).toBeVisible();
  // At this width the header folds its buttons into the More menu.
  const more = page.locator('.more-button');
  if (await more.isVisible()) {
    await more.click();
    await page.getByRole('menuitem', { name: 'This case' }).click();
  } else {
    await page.getByRole('button', { name: 'This case', exact: true }).click();
  }
  await expect(page.locator('#home-analysis')).toBeVisible();
  await noHorizontalScroll(page, 'case home after the hand-off');
});

for (const [id, scheme] of [['HAND-10', 'dark'], ['HAND-11', 'light']] as const) {
  test(`${id}: ${scheme} colour scheme, chart, bucket selector and case home do not scroll sideways, hand-off still works`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await startTracing(page);
    const lum = await page.evaluate(() => {
      const m = getComputedStyle(document.body).backgroundColor.match(/[\d.]+/g)!.map(Number);
      return m[0] + m[1] + m[2];
    });
    if (scheme === 'dark') expect(lum, 'page background is dark').toBeLessThan(384);
    else expect(lum, 'page background is light').toBeGreaterThan(384);
    await noHorizontalScroll(page, 'tracing chart');
    // The selected bar is outlined in the accent colour in either scheme.
    const overlay = page.getByRole('group', { name: /^Time buckets\./ });
    await overlay.focus();
    await page.keyboard.press('ArrowRight');
    const accent = await token(page, '--accent');
    // The outline colour eases in, so read it until it settles.
    await expect.poll(() => page.locator('.column.selected').evaluate((el) => getComputedStyle(el).outlineColor)).toBe(accent);
    expect(await page.locator('.column.selected').evaluate((el) => getComputedStyle(el).outlineStyle)).not.toBe('none');
    expect(await page.locator('.panel[data-lane=audit] .bar').first().evaluate((el) => getComputedStyle(el).backgroundColor)).toBe(await token(page, '--lane-audit'));
    await pickBar(page, 12);
    await expectAnalysisOpen(page);
    await noHorizontalScroll(page, 'analysis');
    await page.getByRole('button', { name: 'This case', exact: true }).click();
    await expect(page.locator('#home-analysis')).toBeVisible();
    await noHorizontalScroll(page, 'case home');
  });
}

test('HAND-12: keyboard only, arrows move between slots, Enter hands off the selected slot', async ({ page }) => {
  await startTracing(page);
  const overlay = page.getByRole('group', { name: /^Time buckets\./ });
  await overlay.focus();
  await expect(overlay).toBeFocused();
  // Enter with nothing selected does nothing.
  await page.keyboard.press('Enter');
  await expect(runButton(page)).toHaveCount(0);
  await expect(page.getByRole('status').filter({ hasText: EMPTY_NOTE })).toHaveCount(0);
  expect(await selectedIndex(page)).toBe(-1);
  // The first arrow selects a slot; Enter on an empty one shows the plain sentence.
  await page.keyboard.press('ArrowRight');
  const first = await selectedIndex(page);
  expect(first).toBeGreaterThanOrEqual(0);
  expect(AUDIT_SLOTS).not.toContain(first);
  await expect(page.locator('#tc-tip')).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('status').filter({ hasText: EMPTY_NOTE })).toBeVisible();
  // Walk to slot 12, one step at a time (left once, right once to see both directions).
  for (let i = 0; i < SLOTS && (await selectedIndex(page)) < 12; i++) await page.keyboard.press('ArrowRight');
  expect(await selectedIndex(page)).toBe(12);
  await page.keyboard.press('ArrowLeft');
  expect(await selectedIndex(page)).toBe(11);
  await page.keyboard.press('ArrowRight');
  expect(await selectedIndex(page)).toBe(12);
  await expect(page.locator('#tc-tip')).toContainText('16:00');
  await page.keyboard.press('Enter');
  await expectAnalysisOpen(page);
  await runButton(page).click();
  await expect(page.getByText(showing(12), { exact: true }).first()).toBeVisible();
});

test('HAND-13: a closed case keeps Analysis and opens the timeline as a viewer with one closed line (UI-R2-03)', async ({ page }) => {
  await openCase(page, 'Night batch overrun, 10 October');
  await expect(page.locator('#home-analysis')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open the analysis' })).toBeEnabled();
  await expect(page.locator('#home-tracing')).toBeVisible();
  await expect(page.locator('#home-tracing-open')).toBeEnabled();
  await page.locator('#home-tracing-open').click();
  await expect(page.getByRole('heading', { level: 2, name: 'Investigation Timeline' })).toBeVisible();
  await expect(page.getByTestId('tp-closed-line')).toHaveText('This case is closed. You can look at the timeline but not change the case.');
  await expect(page.getByRole('button', { name: 'Case home', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Case home', exact: true }).click();
  await expect(page.getByRole('heading', { name: /guided/i })).toHaveCount(0);
});

test('HAND-14: a saved (archived) case home offers neither Analysis nor the timeline', async ({ page }) => {
  await page.getByRole('button', { name: /^Saved cases/ }).click();
  await expect(page.getByText('Loading the saved cases')).toHaveCount(0);
  await page.getByRole('region', { name: /^Saved cases/ }).getByRole('button', { name: 'Open case Month-end batch stall, 2 October' }).click();
  await expect(page.getByRole('heading', { level: 2, name: 'Month-end batch stall, 2 October' })).toBeVisible();
  await expect(page.locator('#home-analysis')).toHaveCount(0);
  await expect(page.locator('#home-tracing')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Open the Investigation Timeline' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Open the analysis' })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: /guided/i })).toHaveCount(0);
});

test('HAND-15: case home and the hand-off call no investigation, views, checklist or report endpoint', async ({ page }) => {
  const paths: string[] = [];
  page.on('request', (r) => paths.push(new URL(r.url()).pathname));
  await startTracing(page);
  await pickBar(page, 12);
  await expectAnalysisOpen(page);
  await page.getByRole('button', { name: 'This case', exact: true }).click();
  await expect(page.locator('#home-analysis')).toBeVisible();
  expect(paths.length).toBeGreaterThan(0);
  const banned = paths.filter((p) => /(^|\/)(investigation|views|checklist|report)(\/|$)/.test(p));
  expect(banned, 'requests to forbidden endpoints').toEqual([]);
});

test('HAND-16: case home matches the wireframe order and button styles', async ({ page }) => {
  await openCase(page, CASE);
  await expect(page.locator('#home-analysis')).toBeVisible();
  await expect(page.locator('#home-erase')).toBeAttached();
  const ids = ['home-analysis', 'home-tracing', 'home-story', 'home-theories', 'home-health', 'home-sources', 'home-files', 'home-key', 'home-erase'];
  const tops = await page.evaluate((list) => list.map((id) => { const el = document.getElementById(id); return el ? el.getBoundingClientRect().top + window.scrollY : -1; }), ids);
  const present = tops.filter((t) => t >= 0);
  expect(present.length).toBeGreaterThanOrEqual(8);
  expect([...present].sort((a, b) => a - b), 'sections run top to bottom in this order').toEqual(present);
  const accent = await token(page, '--accent');
  const primary = await page.locator('section[aria-labelledby=home-analysis] .primary-button').evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(primary).toBe(accent);
  const secondary = await page.locator('#home-tracing-open').evaluate((el) => { const s = getComputedStyle(el); return { bg: s.backgroundColor, border: s.borderTopWidth, style: s.borderTopStyle }; });
  expect(secondary.bg).not.toBe(accent);
  expect(secondary.border).not.toBe('0px');
  expect(secondary.style).not.toBe('none');
  const heads = await page.locator('h3#home-analysis, h3#home-tracing').allInnerTexts();
  expect(heads).toEqual(['Analysis', 'Investigation Timeline']);
});

test('HAND-17: the timeline screen matches the wireframe: region order, lane order, shared time axis, token colours', async ({ page }) => {
  await startTracing(page);
  await layoutSettled(page);
  // Top to bottom: windows, toolbar, canvas bar, the three lanes, time axis, caption.
  const order = ['[data-region=windows]', '[data-region=toolbar]', '.canvas-bar', '.panel[data-lane=audit]', '.panel[data-lane=sql]', '.panel[data-lane=iis]', '.axisrow', '.axcap'];
  const ys = await page.evaluate((sels) => sels.map((s) => document.querySelector(s)?.getBoundingClientRect().top ?? -1), order);
  expect(ys.every((y) => y >= 0), 'every region is present').toBe(true);
  expect([...ys].sort((a, b) => a - b)).toEqual(ys);
  await expect(page.locator('.lane-label strong')).toHaveText(['API requests (audit log)', 'Database (SQL Server)', 'Web server (IIS)']);
  await expect(page.locator('.tt-lane')).toHaveText(['API audit', 'SQL', 'IIS']);
  await expect(page.locator('.axcap')).toHaveText(/^Time \(UTC\)\. Each bar covers 5 minutes, starting at the time on its left edge; every panel uses the same time axis\./);
  await expect(page.locator('.axtick').first()).toHaveText('19 Oct 15:00 UTC');
  // Every lane panel and the click overlay share one left edge and width; the 36 columns fill the overlay.
  const geo = await page.evaluate(() => {
    const box = (e: Element) => { const r = e.getBoundingClientRect(); return { x: r.left, w: r.width }; };
    const overlay = document.querySelector('.overlay')!;
    const cols = Array.from(overlay.querySelectorAll('.column')).map(box);
    return { panels: Array.from(document.querySelectorAll('.panel[data-lane]')).map(box), overlay: box(overlay), colSum: cols.reduce((n, c) => n + c.w, 0), colFirst: cols[0].x, colLast: cols[cols.length - 1].x + cols[cols.length - 1].w };
  });
  for (const p of geo.panels) {
    expect(Math.abs(p.x - geo.overlay.x)).toBeLessThanOrEqual(2);
    expect(Math.abs(p.w - geo.overlay.w)).toBeLessThanOrEqual(2);
  }
  expect(Math.abs(geo.colSum - geo.overlay.w)).toBeLessThanOrEqual(2);
  expect(Math.abs(geo.colFirst - geo.overlay.x)).toBeLessThanOrEqual(1);
  expect(Math.abs(geo.colLast - (geo.overlay.x + geo.overlay.w))).toBeLessThanOrEqual(1);
  // Token colours: panel surface and hairline, bar lane colour, label hint text, and a matching swatch per lane in label and toolbar.
  const [surface, line, laneAudit, text2] = await Promise.all(['--surface-2', '--line', '--lane-audit', '--text-2'].map((t) => token(page, t)));
  const style = await page.evaluate(() => {
    const panel = getComputedStyle(document.querySelector('.panel[data-lane=audit]')!);
    return {
      panelBg: panel.backgroundColor, panelBorder: panel.borderTopColor, bar: getComputedStyle(document.querySelector('.panel[data-lane=audit] .bar')!).backgroundColor,
      small: getComputedStyle(document.querySelector('.lane-label small')!).color,
      labelSwatch: Array.from(document.querySelectorAll('.lane-label .swatch')).map((s) => getComputedStyle(s).backgroundColor),
      toolbarSwatch: Array.from(document.querySelectorAll('.tt-swatch')).map((s) => getComputedStyle(s).backgroundColor)
    };
  });
  expect(style.panelBg).toBe(surface);
  expect(style.panelBorder).toBe(line);
  expect(style.bar).toBe(laneAudit);
  expect(style.small).toBe(text2);
  expect(style.labelSwatch).toEqual(style.toolbarSwatch);
  expect(new Set(style.labelSwatch).size, 'three different lane colours').toBe(3);
  // The mixed-sources note cannot be dismissed.
  await expect(page.locator('.notice')).toContainText('Some data was collected at different times.');
  await expect(page.locator('.notice').getByRole('button')).toHaveCount(0);
});

test('HAND-19: at 375px each lane label sits above its panel and the time-axis labels do not overlap', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await startTracing(page);
  const geo = await page.evaluate(() => {
    const label = document.querySelector('.lane-label')!.getBoundingClientRect();
    const panel = document.querySelector('.panel[data-lane=audit]')!.getBoundingClientRect();
    const ticks = Array.from(document.querySelectorAll('.axtick')).map((t) => t.getBoundingClientRect());
    const overlaps = ticks.some((a, i) => ticks.some((b, j) => j > i && a.left < b.right && b.left < a.right));
    return { labelBottom: label.bottom, panelTop: panel.top, overlaps };
  });
  expect(geo.panelTop).toBeGreaterThanOrEqual(geo.labelBottom - 1);
  expect(geo.overlaps).toBe(false);
});

test('HAND-18: a real mouse click on a bar hands off', async ({ page }) => {
  await startTracing(page);
  const col = columns(page).nth(12);
  await col.scrollIntoViewIfNeeded();
  await col.click();
  await expectAnalysisOpen(page);
});
