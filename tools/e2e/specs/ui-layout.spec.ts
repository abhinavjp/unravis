// Layout fixes the UI audit found (BL-472 to BL-478, BL-482). These measure the real page in a real browser: sizes, positions and
// computed colours, never the CSS text. A later rule that wins over a fixed one makes them fail, a harmless rewrite of the CSS does not.
//   UIFIX-01  Place a Blackbox: the close button does not sit on a step name (BL-473)
//   UIFIX-02  More filters: the "?" and the "i" beside Reference do not overlap, on desktop and on touch (BL-474)
//   UIFIX-03  Daily and hourly view: dimmed rows keep full opacity and 4.5:1 text contrast in both themes (BL-476)
//   UIFIX-04  All operations: first column, pager and header start on one line; the Sources note lines up with the list (BL-477)
//   UIFIX-05  The selected-lines notes of the timeline and the chart help line are at least 11px (BL-478)
//   UIFIX-06  Tap targets of 24 x 24 px or more: Daily view, Show matching requests, Help guide index links, Stop showing, Mark the line (BL-475 group 1)
//   UIFIX-09  Desktop does not move: tables that got bigger tap targets keep every row height and column width (BL-475 group 1)
//   UIFIX-12  Tap targets of 24 x 24 px or more: the hour squares of the Daily and hourly view and the two zoom slider handles; a press 9px inside the edge grabs the handle; a press on a painted square lands on that square (BL-475 groups 2 and 3)
//   UIFIX-13  Desktop does not move: the hour grid, its legend and the zoom strip look the same pixel for pixel as with the old rules put back (BL-475 groups 2 and 3)
//   UIFIX-07  Add files on a phone: the column heads are painted nowhere, yet stay in the page for screen readers (BL-482). A guard only: the finding was a false positive of the audit, so the audit run proves the fix
//   UIFIX-08  Phone header with "Choose sources" fits the window (BL-472); 390 and 360 px fail on the old header, 320 px is a no-regression case
//   UIFIX-14  Tap targets of 24 x 24 px or more and desktop does not move: the "Change" link of the Review step (14a), the "Open the release page" link of the update bar (14b), the unfinished-request counts of Where is it? (14c), and the keyboard focus rings and rest pictures of those three, which stay the old ones (14f) (BL-475 group 4)
//   UIFIX-15  Daily and hourly view: the four legend squares show the heat colours of the squares they stand for, dark and light (BL-493)
//   UIFIX-16  Blackbox charts: the Counters / Programs tab strip is not squeezed (24 px or more) and nothing outside the side column moves (BL-496)
//   UIFIX-10  Slow call screen: the time zone label, the peak and zero labels and the "drag" hint are at least 11px, the lanes and the minimap do not move, nothing collides (BL-483)
//   UIFIX-17  Investigation timeline: the UTC times of the incident and comparison windows are drawn as visible text (not only a tooltip), stay UTC when "Times in" changes, fit at 320, 360 and 390 px, and the one-line readout does not grow on a wide desktop
//   UIFIX-19  Slow call screen tap targets of 24 px (BL-492): the "Timeline" crumb button (19a), the minimap box on a phone (19b), the lane bars (19c); nothing moves and nothing is painted differently (19d, 19f); a press lands on the right bar and never on a neighbour (19e)
//   UIFIX-30  Requests over time (home): the left, right and bottom axes have a title with the unit, painted inside the same chart box, in both themes; the box and the rectangles around it do not move; 4.5:1 contrast (BL-495 part)
//   UIFIX-31  Requests over time on real value ranges (also: one slow request reads the same in the tooltip and the note, 31e) (a busy hour in the millions, a slow hour in seconds, a sub-millisecond hour, no timing at all): one unit and one precision on each axis, tooltip with the interval's start and end and the P95 and slowest request alike, no NaN
//   UIFIX-32  Requests over time on a phone (320, 360, 390 px): the axis titles are painted, the page does not scroll sideways and the chart box keeps its 220 px
//   UIFIX-40  Clickable table rows are real buttons (BL-495 part): the Slow call lists of nearby calls (40a) and of all sources (40b) have one <button> per row (name, 24 px press area, nothing moves, no focus stop on the row itself); Tab, Enter and Space act once (40c, 40d); the mouse still takes the whole row (40d); a button press never goes on to the row (40c, 40d); on a phone a cell is not named twice (40f); "Show only" of the Request volume table stays a real button (40e)
//   UIFIX-50  Row buttons of the All operations request list and "Show only" (BL-495 part): "Nearby activity" and "Inspect" are real <button type=button> of one size on one line each, named by their request (API, time to the millisecond, source and record id) with the visible words first (50a); the same Enter and Space act once, with the focus ring (50b, 50c); "Show only" of the Request volume table is a quiet button with a visible edge and one size, 24 px press area, and the table still fits its box (50d)
//   UIFIX-51  Slowest tab (BL-495 part n): "Inspect window" of the exact time windows and "Nearby activity" of the slowest requests are real <button type=button> quiet buttons of one size with a visible edge, named by the window (start to end, zone) or by the request (API, time to the millisecond, source, record id) with the visible words first (51a); Enter and Space act once each, with the focus ring (51b, 51c)
//   UIFIX-52  Slowest tab (BL-537): the small line under the API name of each slowest request is at least 11px (the audit's floor) and the two headings start their words where the controls and notes under them start; the heading boxes and the list's other columns stay where they were (52a, 52b)
//   UIFIX-53  Slowest tab (BL-538): the "API / source" column of the slowest-request list and the first column of the exact time windows have a sensible width: the API name stays on one line, the rows are short (the 50-row list was about 11,000 px tall at 1440 px), and at 1440 px the list fits its panel so "Nearby activity" is not pushed out
import { test, expect, type Page } from '../support/fixtures';
import { openCase, layoutSettled } from '../support/ui';
import { sweepTip } from '../support/chart-tip';

type Box = { x: number; y: number; w: number; h: number };

const overlap = (a: Box, b: Box) => {
  const ix = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
  const iy = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
  return ix > 0.5 && iy > 0.5 ? ix * iy : 0;
};

/** Open the Blackbox drawer from the header button, or from the More menu when the header is narrow. */
async function openBlackbox(page: Page): Promise<void> {
  const direct = page.getByRole('button', { name: 'Blackbox', exact: true });
  if (await direct.isVisible()) await direct.click();
  else {
    await page.getByRole('button', { name: 'More', exact: true }).click();
    await page.getByRole('menuitem', { name: 'Blackbox' }).click();
  }
  await expect(page.locator('#blackbox-title')).toBeVisible();
}

test('UIFIX-01 Place a Blackbox: the close button keeps clear of every step name', async ({ page }) => {
  await openBlackbox(page);
  await page.getByRole('button', { name: 'Open status: web-a' }).waitFor();
  await page.getByRole('button', { name: 'Place a Blackbox' }).click();
  const close = page.getByRole('button', { name: 'Close set-up' });
  await expect(close).toBeVisible();
  const clashes = await page.evaluate(() => {
    const b = document.querySelector<HTMLElement>('button[aria-label="Close set-up"]')!.getBoundingClientRect();
    const out: string[] = [];
    document.querySelectorAll<HTMLElement>('.shell-rail *').forEach((el) => {
      if (!el.childElementCount && el.textContent?.trim()) {
        const r = el.getBoundingClientRect();
        if (r.width && Math.min(r.right, b.right) - Math.max(r.left, b.left) > 0.5 && Math.min(r.bottom, b.bottom) - Math.max(r.top, b.top) > 0.5) out.push(el.textContent.trim());
      }
    });
    return out;
  });
  expect(clashes, 'step names under the close button').toEqual([]);
});

for (const touch of [false, true]) {
  test('UIFIX-02' + (touch ? 'b' : 'a') + ' More filters: the "?" for Reference and the "i" beside it do not overlap' + (touch ? ' (touch size)' : ''), async ({ page }) => {
    if (touch) await page.setViewportSize({ width: 600, height: 900 }); // narrow screens use the 32px touch size of the "i"
    await page.getByText('More filters').first().click();
    const help = page.getByRole('button', { name: 'What does Reference period mean?' });
    await expect(help).toBeVisible();
    const boxes = await page.evaluate(() => {
      const q = document.querySelector<HTMLElement>('button[aria-label="What does Reference period mean?"]')!;
      const line = q.closest('.label-line')!;
      const i = line.querySelector<HTMLElement>('.field-help-toggle')!;
      const r = (e: Element): Box => { const b = e.getBoundingClientRect(); return { x: b.x, y: b.y, w: b.width, h: b.height }; };
      return { q: r(q), i: r(i) };
    });
    expect(overlap(boxes.q, boxes.i), JSON.stringify(boxes)).toBe(0);
    // the heading carries its "?" and the "i" follows it on the same line
    expect(boxes.i.x).toBeGreaterThanOrEqual(boxes.q.x + boxes.q.w - 0.5);
  });
}

// WCAG contrast of the text colour against the first opaque background behind it.
async function lowRowFacts(page: Page) {
  return page.evaluate(() => {
    const parse = (c: string) => { const m = /rgba?\(([^)]+)\)/.exec(c)!; const p = m[1].split(/[ ,/]+/).map(Number); return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 }; };
    const lin = (v: number) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
    const lum = (c: { r: number; g: number; b: number }) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
    const backdrop = (el: Element) => { for (let e: Element | null = el; e; e = e.parentElement) { const c = parse(getComputedStyle(e).backgroundColor); if (c.a > 0.99) return c; } return { r: 255, g: 255, b: 255, a: 1 }; };
    const out: { text: string; opacity: string; ratio: number }[] = [];
    document.querySelectorAll('tr.low td, tr.low th').forEach((el) => {
      const s = getComputedStyle(el);
      const fg = parse(s.color);
      const bg = backdrop(el);
      const l1 = lum(fg), l2 = lum(bg);
      out.push({ text: (el.textContent || '').trim().slice(0, 20), opacity: s.opacity, ratio: (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05) });
    });
    return out;
  });
}

for (const scheme of ['dark', 'light'] as const) {
  test('UIFIX-03 Daily and hourly view: dimmed rows are readable (' + scheme + ')', async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await page.getByRole('button', { name: /^Daily and hourly view/ }).click();
    await expect(page.locator('tr.low').first()).toBeVisible();
    const rows = await lowRowFacts(page);
    expect(rows.length).toBeGreaterThan(0);
    for (const r of rows) {
      expect(r.opacity, r.text).toBe('1');
      expect(r.ratio, r.text + ' contrast').toBeGreaterThanOrEqual(4.5);
    }
  });
}

test('UIFIX-04a All operations: the first column, the header cell and the pager share one left line', async ({ page }) => {
  await page.getByRole('button', { name: 'All operations', exact: true }).click();
  await page.getByRole('tab', { name: 'Failures' }).waitFor();
  const lines = await page.evaluate(() => {
    const left = (el: Element | null) => { if (!el) return null; const r = el.getBoundingClientRect(); const pad = parseFloat(getComputedStyle(el).paddingLeft) || 0; return Math.round((r.left + pad) * 10) / 10; };
    return {
      head: left(document.querySelector('.group-table-scroll th:first-child')),
      cell: left(document.querySelector('.group-table-scroll tbody td:first-child')),
      pager: left(document.querySelector('.group-pagination'))
    };
  });
  expect(lines.head, 'header cell').not.toBeNull();
  expect(lines.cell, 'first column').toBe(lines.head);
  if (lines.pager !== null) expect(lines.pager, 'pager row').toBe(lines.head);
});

test('UIFIX-04b Sources pop-up: the demo note starts on the same line as the list above it', async ({ page }) => {
  await page.getByRole('button', { name: /^Audit sources/ }).click();
  const note = page.locator('.popover-foot > .demo-note');
  await expect(note).toBeVisible();
  const lines = await page.evaluate(() => {
    const x = (el: Element | null) => (el ? Math.round(el.getBoundingClientRect().left * 10) / 10 : null);
    const textX = (el: Element | null) => { if (!el) return null; const r = document.createRange(); r.selectNodeContents(el); return Math.round(r.getBoundingClientRect().left * 10) / 10; };
    return { note: textX(document.querySelector('.popover-foot > .demo-note')), count: textX(document.querySelector('.popover .source-count')), list: x(document.querySelector('.popover .source-list')) };
  });
  // the note's text and the "n selected" line are both inset by the same 6px from the pop-up's content edge
  expect(lines.note).toBe(lines.count);
});

test('UIFIX-05 The selected-lines notes of the Where is it? timeline are at least 11px', async ({ page }) => {
  await page.getByRole('button', { name: 'Where is it?', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Where is it?' })).toBeVisible();
  const sizes = await page.evaluate(() => [...document.querySelectorAll<HTMLElement>('.timeline-selected small, .canvas-bar small')].filter((e) => e.offsetParent).map((e) => parseFloat(getComputedStyle(e).fontSize)));
  for (const s of sizes) expect(s).toBeGreaterThanOrEqual(11);
});

test('UIFIX-06a Where is it?: the Daily view buttons are 24 x 24 px or more', async ({ page }) => {
  await page.getByRole('button', { name: 'Where is it?', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Where is it?' })).toBeVisible();
  await page.locator('summary', { hasText: 'API groups' }).click();
  const buttons = page.locator('button.daily-open');
  await expect(buttons.first()).toBeVisible();
  for (const b of await buttons.all()) {
    const box = (await b.boundingBox())!;
    expect(box.height, await b.getAttribute('aria-label') ?? 'Daily view').toBeGreaterThanOrEqual(24);
    expect(box.width).toBeGreaterThanOrEqual(24);
  }
});

test('UIFIX-06b All operations, Breakdown: "Show matching requests" buttons are 24 px tall', async ({ page }) => {
  await page.getByRole('button', { name: 'All operations', exact: true }).click();
  await page.getByRole('tab', { name: 'Breakdown' }).click();
  const picks = page.locator('button.breakdown-pick:not([disabled])');
  await expect(picks.first()).toBeVisible();
  for (const b of await picks.all()) expect((await b.boundingBox())!.height).toBeGreaterThanOrEqual(24);
});

test('UIFIX-06d Blackbox charts: "Stop showing" and "Mark the line" are 24 px or more', async ({ page }) => {
  await openBlackbox(page);
  await page.getByRole('button', { name: /^Open status: web-a/ }).first().click();
  await page.getByRole('button', { name: 'Open charts' }).first().click();
  const charts = page.locator('dialog.bbc[open]');
  await expect(charts.locator('canvas').first()).toBeVisible();
  const rowButtons = charts.locator('.bbc-rowbtn');
  await expect(rowButtons.first()).toBeVisible();
  for (const b of await rowButtons.all()) expect((await b.boundingBox())!.height, 'Mark the line').toBeGreaterThanOrEqual(24);
  const stops = charts.locator('.bbc-x');
  expect(await stops.count()).toBeGreaterThan(0);
  for (const b of await stops.all()) {
    const box = (await b.boundingBox())!;
    expect(box.height, 'Stop showing').toBeGreaterThanOrEqual(24);
    expect(box.width, 'Stop showing').toBeGreaterThanOrEqual(24);
  }
});

test('UIFIX-06c Help guide: the index links are 24 px tall and sit in their rows', async ({ page }) => {
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  await page.locator('#glossary-title').waitFor();
  await page.locator('[data-testid=guide-open]').click();
  await page.locator('[data-testid=guide-nav-index]').waitFor();
  await page.locator('[data-testid=guide-nav-index]').click();
  const links = page.locator('[data-testid=guide-body] td .guide-link');
  await expect(links.first()).toBeVisible();
  const facts = await links.evaluateAll((els) => els.map((e) => { const r = e.getBoundingClientRect(); const row = e.closest('tr')!.getBoundingClientRect(); return { h: r.height, rowH: row.height }; }));
  expect(facts.length).toBeGreaterThan(3);
  for (const f of facts) {
    expect(f.h).toBeGreaterThanOrEqual(24);
    // the row is not taller than its link plus its own cell padding: the extra hit area is margin-neutral
    expect(f.rowH).toBeLessThan(f.h + 24);
  }
});

test('UIFIX-07 Add files on a phone: the column heads are not painted, and are still in the page (guard; the audit itself is the proof of BL-482)', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('menuitem', { name: 'All cases', exact: true }).click();
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByRole('textbox', { name: 'Case name' }).fill('Layout case');
  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await page.getByRole('button', { name: /^Files/ }).click();
  await page.getByRole('heading', { name: 'Add files by type' }).waitFor();
  await page.setInputFiles('input[aria-label="Choose files to import"]', [{ name: 'u_ex201020.log', mimeType: 'text/plain', buffer: Buffer.from('#Software: Microsoft Internet Information Services 10.0\n#Version: 1.0\n#Fields: date time cs-method cs-uri-stem sc-status\n2026-10-20 09:00:00 GET /a 200\n') }]);
  await page.locator('[data-file="u_ex201020.log"]').waitFor();
  // What matters to a person: no pixel of a column head is painted (the heads are a 1px clipped box, each cell is labelled from them).
  // Measured as the area of the head text that is left inside every clipping ancestor.
  const painted = await page.evaluate(() => {
    const out: string[] = [];
    document.querySelectorAll<HTMLElement>('thead th').forEach((th) => {
      const r = document.createRange();
      r.selectNodeContents(th);
      const box = r.getBoundingClientRect();
      let area = { l: box.left, t: box.top, r: box.right, b: box.bottom };
      for (let a: HTMLElement | null = th; a; a = a.parentElement) {
        const cs = getComputedStyle(a);
        if (/(hidden|clip)/.test(cs.overflowX) || /(hidden|clip)/.test(cs.overflowY) || cs.clip === 'rect(0px, 0px, 0px, 0px)') {
          const b = a.getBoundingClientRect();
          area = { l: Math.max(area.l, b.left), t: Math.max(area.t, b.top), r: Math.min(area.r, b.right), b: Math.min(area.b, b.bottom) };
          if (cs.clip === 'rect(0px, 0px, 0px, 0px)') area = { l: 0, t: 0, r: 0, b: 0 };
        }
      }
      const w = Math.max(0, area.r - area.l), h = Math.max(0, area.b - area.t);
      if (w * h > 1) out.push((th.textContent || '').trim() + ' ' + Math.round(w) + 'x' + Math.round(h));
    });
    return out;
  });
  expect(painted, 'column heads that can be seen on a phone').toEqual([]);
  // the heads are still there for assistive technology
  expect(await page.locator('thead th').count()).toBeGreaterThanOrEqual(4);
});

for (const width of [390, 360, 320]) {
  test('UIFIX-08 Phone header with "Choose sources" fits a ' + width + ' px window' + (width === 320 ? ' (no regression: already fitted before)' : ''), async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.evaluate(() => { location.hash = '#keeper=stopped'; });
    const trigger = page.getByRole('button', { name: /^Audit sources/ });
    await expect(trigger).toBeVisible();
    await expect(trigger).toHaveAttribute('aria-label', 'Audit sources: Choose sources');
    const facts = await page.evaluate(() => {
      const root = document.documentElement;
      const theme = document.querySelector<HTMLElement>('.theme-toggle')!.getBoundingClientRect();
      const header = document.querySelector<HTMLElement>('header.topbar')!;
      return { scroll: root.scrollWidth, win: window.innerWidth, themeRight: theme.right, headerOver: header.scrollWidth - header.clientWidth };
    });
    expect(facts.scroll, 'page scrolls sideways').toBeLessThanOrEqual(facts.win);
    expect(facts.themeRight, 'theme button is cut off').toBeLessThanOrEqual(facts.win);
    expect(facts.headerOver).toBeLessThanOrEqual(0);
    // the shortened label is still readable as the start of the words
    const label = await trigger.locator('.label').evaluate((el) => ({ shown: el.clientWidth, full: el.scrollWidth }));
    expect(label.shown).toBeGreaterThan(30);
  });
}

// BL-475 group 1 makes five kinds of target 24px big without moving anything (padding cancelled by negative margin). The owner rule is that
// desktop must not change, not even by a sub-pixel. This puts the old rules of these targets back as a stylesheet and compares: font and
// platform independent, and it fails as soon as a tap-target rule changes the size of its row or column.
const DEVELOP_RULES: [string, Record<string, string>][] = [
  ['.daily-open', { display: 'block', padding: '2px 0', margin: '0', 'min-height': '0', transition: 'none' }],
  ['.breakdown-pick', { padding: '0', margin: '0', 'min-height': '0', transition: 'none' }],
  ['.bbc-rowbtn', { padding: '0', margin: '0', 'min-height': '0', transition: 'none' }],
  ['.bbc-x', { padding: '0 6px', margin: '0', 'min-width': '0', 'min-height': '0', transition: 'none' }],
  ['td .guide-link, th .guide-link', { display: 'inline', padding: '0', margin: '0', 'min-height': '0', transition: 'none' }]
];

type Shape = { w: number; h: number; rows: number[]; cols: number[] };

/** The table's size now, and with the old rules of the tap targets put back as inline styles. One evaluation, so nothing moves in between. */
async function shapes(page: Page, table: string): Promise<{ now: Shape; old: Shape; touched: number }> {
  return page.locator(table).first().evaluate((t, rules) => {
    const r = (e: Element) => e.getBoundingClientRect();
    const shape = (): Shape => ({ w: r(t).width, h: r(t).height, rows: [...t.querySelectorAll('tr')].map((x) => r(x).height), cols: [...(t.querySelector('tr')?.children ?? [])].map((x) => r(x).width) });
    const now = shape();
    const saved: [HTMLElement, string | null][] = [];
    for (const [selector, declarations] of rules) {
      t.querySelectorAll<HTMLElement>(selector).forEach((el) => {
        saved.push([el, el.getAttribute('style')]);
        for (const [k, v] of Object.entries(declarations)) el.style.setProperty(k, v, 'important');
      });
    }
    void document.body.offsetHeight;
    const old = shape();
    for (const [el, style] of saved) { if (style === null) el.removeAttribute('style'); else el.setAttribute('style', style); }
    return { now, old, touched: saved.length };
  }, DEVELOP_RULES);
}

async function expectSameAsDevelop(page: Page, table: string, label: string): Promise<void> {
  const { now, old, touched } = await shapes(page, table);
  expect(touched, label + ': no tap target found to compare').toBeGreaterThan(0);
  const near = (a: number[], b: number[]) => a.length === b.length && a.every((v, i) => Math.abs(v - b[i]) < 0.05);
  expect(near([now.w, now.h], [old.w, old.h]), label + ' table size now ' + now.w + ' x ' + now.h + ', with the old rules ' + old.w + ' x ' + old.h).toBe(true);
  expect(near(now.rows, old.rows), label + ' row heights now ' + now.rows.join(',') + ' with the old rules ' + old.rows.join(',')).toBe(true);
  expect(near(now.cols, old.cols), label + ' column widths now ' + now.cols.join(',') + ' with the old rules ' + old.cols.join(',')).toBe(true);
}

test('UIFIX-09a Where is it?: the API groups table keeps every row height', async ({ page }) => {
  await page.getByRole('button', { name: 'Where is it?', exact: true }).click();
  await page.locator('summary', { hasText: 'API groups' }).click();
  await expect(page.locator('button.daily-open').first()).toBeVisible();
  await expectSameAsDevelop(page, 'table:has(button.daily-open)', 'API groups');
});

test('UIFIX-09b All operations, Breakdown: the table keeps every row height', async ({ page }) => {
  await page.getByRole('button', { name: 'All operations', exact: true }).click();
  await page.getByRole('tab', { name: 'Breakdown' }).click();
  await expect(page.locator('button.breakdown-pick').first()).toBeVisible();
  await expectSameAsDevelop(page, 'table:has(button.breakdown-pick)', 'Breakdown');
});

test('UIFIX-09c Help guide index: the table keeps every row height', async ({ page }) => {
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  await page.locator('#glossary-title').waitFor();
  await page.locator('[data-testid=guide-open]').click();
  await page.locator('[data-testid=guide-nav-index]').click();
  await expect(page.locator('[data-testid=guide-body] td .guide-link').first()).toBeVisible();
  await expectSameAsDevelop(page, '[data-testid=guide-body] table:has(.guide-link)', 'Guide index');
});

test('UIFIX-09d Blackbox charts: the figures table keeps its row heights and column widths', async ({ page }) => {
  await openBlackbox(page);
  await page.getByRole('button', { name: /^Open status: web-a/ }).first().click();
  await page.getByRole('button', { name: 'Open charts' }).first().click();
  await expect(page.locator('dialog.bbc[open] canvas').first()).toBeVisible();
  await expect(page.locator('dialog.bbc[open] .bbc-x').first()).toBeVisible();
  await expectSameAsDevelop(page, 'dialog.bbc[open] table:has(.bbc-x)', 'Charts figures');
});

// BL-475 groups 2 and 3. The hour squares are 24px tall buttons that paint their 20px square in a ::before, and the zoom handles are 24px
// wide with the same 4px grip. Nothing may move on desktop: the same old-rules comparison as UIFIX-09, plus the painted parts.
async function openDailyHourly(page: Page): Promise<void> {
  await page.getByRole('button', { name: /^Daily and hourly view/ }).click();
  await expect(page.locator('.hm-grid')).toBeVisible();
  await expect(page.locator('button.hm-cell').first()).toBeVisible();
}

test('UIFIX-12a Daily and hourly view: every hour square is 24 x 24 px or more', async ({ page }) => {
  await openDailyHourly(page);
  const sizes = await page.locator('button.hm-cell').evaluateAll((els) => els.map((e) => { const r = e.getBoundingClientRect(); return { w: r.width, h: r.height, name: e.getAttribute('aria-label') }; }));
  expect(sizes.length).toBeGreaterThan(100);
  for (const z of sizes) {
    expect(z.h, z.name ?? 'hour square').toBeGreaterThanOrEqual(24);
    expect(z.w, z.name ?? 'hour square').toBeGreaterThanOrEqual(24);
  }
});

test('UIFIX-12b Daily and hourly view on a phone: the hour squares are 24 x 24 px and no two overlap', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDailyHourly(page);
  const cells = await page.locator('button.hm-cell').evaluateAll((els) => els.map((e) => { const r = e.getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height }; }));
  for (const c of cells) { expect(c.w).toBeGreaterThanOrEqual(24); expect(c.h).toBeGreaterThanOrEqual(24); }
  const clashes = cells.flatMap((a, i) => cells.slice(i + 1).filter((b) => overlap(a, b) > 0).map(() => i));
  expect(clashes, 'squares that overlap another square').toEqual([]);
});

test('UIFIX-12c Daily and hourly view: the two zoom handles are 24 x 24 px or more', async ({ page }) => {
  await openDailyHourly(page);
  const handles = page.locator('.nav-handle');
  expect(await handles.count()).toBe(2);
  for (const h of await handles.all()) {
    const box = (await h.boundingBox())!;
    expect(box.width, await h.getAttribute('aria-label') ?? 'zoom handle').toBeGreaterThanOrEqual(24);
    expect(box.height).toBeGreaterThanOrEqual(24);
  }
});

test('UIFIX-12d Zoom strip: a press 9px inside the edge grabs the handle (the grab zone is the handle, 12px each side)', async ({ page }) => {
  await openDailyHourly(page);
  const start = page.locator('.nav-handle').first();
  const end = page.locator('.nav-handle').nth(1);
  const endBefore = await end.getAttribute('aria-valuenow');
  const startBefore = await start.getAttribute('aria-valuenow');
  await start.scrollIntoViewIfNeeded();
  const box = (await start.boundingBox())!;
  const x = box.x + box.width / 2 + 9, y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + 300, y, { steps: 3 });
  // while the button is down the handles show the preview: the start edge has moved, the end edge has not (a press that missed the handle would draw a new range)
  await expect.poll(async () => start.getAttribute('aria-valuenow')).not.toBe(startBefore);
  expect(await end.getAttribute('aria-valuenow'), 'only the start edge moves').toBe(endBefore);
  await page.mouse.up();
});

// The rules of develop for the hour grid, its legend and the zoom handles, as they were before BL-475 groups 2 and 3 (copied from develop at e784a5f).
// They are put back on the new build as a stylesheet in which every declaration is !important, followed by rules that switch the new
// pseudo-elements off. The page must then look the same pixel for pixel: a rule that moves, resizes or recolours anything (the legend
// squares excepted: they were grey on develop because `.hm-swatch` came after `.level-N`, and are now the heat colours, see UIFIX-15) fails here.
const OLD_HEATMAP_CSS = `
  .hm-cell, .hm-gap { display: block; width: 100%; height: 20px; padding: 0; border-radius: 1px; }
  .hm-cell { border: 1px solid var(--line-soft); background: var(--surface-2); cursor: pointer; }
  .hm-cell.empty { cursor: default; background: transparent; }
  .hm-gap { border: 1px solid transparent; }
  .level-1 { background: var(--heat-1); }
  .level-2 { background: var(--heat-2); }
  .level-3 { background: var(--heat-3); }
  .level-4 { background: var(--heat-4); }
  .hm-cell.noise { border: 1px dashed var(--text-2); }
  .hm-cell:hover:not(.empty) { outline: 1px solid var(--text); outline-offset: 0; }
  .hm-cell:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; position: relative; z-index: 1; }
  .hm-swatch { display: inline-block; width: 16px; height: 12px; border: 1px solid var(--control-line); border-radius: 1px; background: var(--surface-2); } /* PR 157 (ANALYSIS) gave the legend squares the control line colour on purpose */
  /* BL-493 (UIFIX-15): the legend squares show the heat colours. This is the one rule of the old set that is NOT develop's: develop paints them grey (the bug), so the comparison would otherwise fail on exactly the intended change. Everything else of the legend is still compared. */
  .hm-swatch.level-1 { background: var(--heat-1); }
  .hm-swatch.level-2 { background: var(--heat-2); }
  .hm-swatch.level-3 { background: var(--heat-3); }
  .hm-swatch.level-4 { background: var(--heat-4); }
  .hm-swatch.noise { background: var(--surface-2); border: 1px dashed var(--text-2); margin-left: 8px; }
  .hm-swatch.empty { background: transparent; margin-left: 8px; }
  .nav-handle { position: absolute; top: 0; bottom: 0; width: 12px; margin-left: -6px; cursor: ew-resize; touch-action: pan-y; }
  .nav-handle:focus-visible { outline: 2px solid var(--focus); outline-offset: 0; }
`;
const OFF_NEW_CSS = '.hm-cell { margin: 0 !important; position: static !important; pointer-events: auto !important; } .hm-cell:focus-visible { position: relative !important; } .hm-cell::before, .hm-cell::after, .nav-handle::after { content: none !important; }';
const important = (css: string) => css.replace(/;(?=\s*(\}|$))/g, ';').replace(/([^{};\s][^{};]*?);/g, (m) => (m.includes('!important') ? m : m.slice(0, -1) + ' !important;'));

/** The same screenshots with the new rules and with the old ones put back. */
async function paintedBoth(page: Page, take: () => Promise<Buffer[]>): Promise<{ now: Buffer[]; old: Buffer[] }> {
  const now = await take();
  const style = await page.addStyleTag({ content: important(OLD_HEATMAP_CSS) + OFF_NEW_CSS });
  const old = await take();
  await style.evaluate((el) => el.remove());
  return { now, old };
}

for (const scheme of ['dark', 'light'] as const) {
  test('UIFIX-13a Daily and hourly view: the hour grid and its legend look exactly as before (' + scheme + ')', async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await openDailyHourly(page);
    const hm = page.locator('.hm');
    await hm.scrollIntoViewIfNeeded();
    const cells = page.locator('button.hm-cell:not(.empty)');
    expect(await cells.count()).toBeGreaterThan(8);
    const { now, old } = await paintedBoth(page, async () => {
      const shots: Buffer[] = [];
      await page.mouse.move(0, 0);
      shots.push(await hm.screenshot());
      for (const i of [0, 3, 7]) {
        await cells.nth(i).hover();
        shots.push(await hm.screenshot()); // hover ring
        await cells.nth(i).focus();
        await page.keyboard.press('ArrowRight');
        await page.keyboard.press('ArrowLeft');
        await page.mouse.move(0, 0);
        shots.push(await hm.screenshot()); // focus ring
      }
      await page.locator('body').click({ position: { x: 1, y: 1 } });
      return shots;
    });
    const names = ['rest', 'hover 0', 'focus 0', 'hover 3', 'focus 3', 'hover 7', 'focus 7'];
    now.forEach((shot, i) => expect(shot.equals(old[i]), names[i] + ': the picture differs from the old rules').toBe(true));
    // The legend squares are the one intended difference from develop (BL-493): UIFIX-15 checks their colours.
  });

  test('UIFIX-13b Zoom strip: the strip, the grip and the focus ring look exactly as before (' + scheme + ')', async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await openDailyHourly(page);
    const nav = page.locator('.navigator');
    await nav.scrollIntoViewIfNeeded();
    const { now, old } = await paintedBoth(page, async () => {
      const shots: Buffer[] = [];
      await page.mouse.move(0, 0);
      shots.push(await nav.screenshot());
      await page.locator('.nav-handle').first().focus();
      await page.keyboard.press('Shift');
      shots.push(await nav.screenshot()); // focus ring of the start handle
      await page.locator('.nav-handle').nth(1).focus();
      await page.keyboard.press('Shift');
      shots.push(await nav.screenshot());
      await page.locator('body').click({ position: { x: 1, y: 1 } });
      return shots;
    });
    now.forEach((shot, i) => expect(shot.equals(old[i]), ['rest', 'focus start', 'focus end'][i] + ': the picture differs from the old rules').toBe(true));
  });
}

test('UIFIX-13c Daily and hourly view: the size and place of every row and square equal the old rules', async ({ page }) => {
  await openDailyHourly(page);
  const grid = page.locator('.hm-grid');
  const measure = () => grid.evaluate((g) => {
    const box = (e: Element) => { const b = e.getBoundingClientRect(); return [b.x, b.y, b.width, b.height]; };
    return { grid: box(g), slots: [...g.querySelectorAll('.hm-cell-wrap')].map(box), rows: [...g.querySelectorAll('.hm-row')].map(box) };
  });
  const now = await measure();
  const style = await page.addStyleTag({ content: important(OLD_HEATMAP_CSS) + OFF_NEW_CSS });
  const old = await measure();
  await style.evaluate((el) => el.remove());
  const near = (a: number[], b: number[]) => a.length === b.length && a.every((v, i) => Math.abs(v - b[i]) < 0.05);
  expect(near(now.grid, old.grid), 'grid box now ' + now.grid + ' old ' + old.grid).toBe(true);
  expect(now.slots.length).toBeGreaterThan(100);
  now.slots.forEach((box, i) => expect(near(box, old.slots[i]), 'slot ' + i + ' now ' + box + ' old ' + old.slots[i]).toBe(true));
  now.rows.forEach((box, i) => expect(near(box, old.rows[i]), 'row ' + i + ' now ' + box + ' old ' + old.rows[i]).toBe(true));
});

test('UIFIX-12e Daily and hourly view: a press anywhere on a painted square, edges included, lands on that square', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 3000 }); // elementFromPoint only sees what is on screen
  await openDailyHourly(page);
  const facts = await page.evaluate(() => {
    const cells = [...document.querySelectorAll<HTMLElement>('button.hm-cell')];
    const wrong: string[] = [];
    let checked = 0;
    for (const c of cells.filter((_, i) => i % 3 === 0)) {
      const b = c.getBoundingClientRect(); // the 24px button; the painted square is 2px in from top and bottom
      const x = b.x + b.width / 2, top = b.top + 2, bottom = b.bottom - 2;
      // the whole height of the square, in half pixel steps and at both edges: no press may fall through to the hour above or below
      const ys = [top + 0.05, bottom - 0.05];
      for (let y = top; y < bottom; y += 0.5) ys.push(y + 0.05);
      for (const y of ys) {
        checked++;
        const hit = document.elementFromPoint(x, y)?.closest('button.hm-cell');
        if (hit !== c) wrong.push((c.getAttribute('aria-label') ?? '') + ' at ' + (y - top).toFixed(2));
      }
    }
    return { wrong: wrong.slice(0, 5), checked };
  });
  expect(facts.checked).toBeGreaterThan(500);
  expect(facts.wrong).toEqual([]);
});
// BL-483: four labels of the Slow call screen were 10px, under the app's own 11px floor. Raising them must not move the lanes (Slow call lane
// rule, BL-005: no inner scrolling, the lanes stay visible together) and the owner rule is that desktop wins over the phone. Like UIFIX-09
// this puts the old 10px back as inline styles and compares the rectangles in one evaluation, so nothing moves in between.
// BL-493: the legend under the hour grid. `.hm-swatch` (grey) used to come after `.level-N` and win, so all four squares were grey.
for (const scheme of ['dark', 'light'] as const) {
  test('UIFIX-15 Daily and hourly view: each legend square is the colour of the hour squares of its level (' + scheme + ')', async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await openDailyHourly(page);
    await page.locator('.hm').scrollIntoViewIfNeeded();
    const facts = await page.evaluate(() => {
      const bg = (e: Element | null, pseudo?: string) => (e ? getComputedStyle(e, pseudo).backgroundColor : null);
      const swatches = [...document.querySelectorAll('.hm-legend .hm-swatch')];
      // a colour as the browser computes it (a probe element, so var(--heat-N) and rgb() compare the same way)
      const probe = document.createElement('span');
      document.querySelector('.hm')!.appendChild(probe);
      probe.style.transition = 'none'; // else the computed colour is still on its way from transparent
      void probe.offsetWidth;
      const resolve = (v: string) => { probe.style.background = v; return getComputedStyle(probe).backgroundColor; };
      const out = [1, 2, 3, 4].map((n) => {
        // A cell of this level if the demo data has one; if not, a copy of a real cell with the level class put on it, so the browser still applies the cell rules of that level.
        let cell = document.querySelector('button.hm-cell.level-' + n);
        const real = !!cell;
        if (!cell) {
          const model = document.querySelector('button.hm-cell:not(.empty)') as HTMLElement;
          const copy = model.cloneNode(false) as HTMLElement;
          copy.classList.remove('level-1', 'level-2', 'level-3', 'level-4', 'empty', 'noise');
          copy.classList.add('level-' + n);
          copy.style.transition = 'none';
          model.parentElement!.appendChild(copy);
          void copy.offsetWidth;
          cell = copy;
        }
        const swatch = swatches.find((e) => e.classList.contains('level-' + n)) ?? null;
        const r = { level: n, real, cell: bg(cell, '::before'), swatch: bg(swatch), token: resolve('var(--heat-' + n + ')'), index: swatch ? swatches.indexOf(swatch) : -1 };
        if (!real) cell!.remove();
        return r;
      });
      const grey = resolve('var(--surface-2)');
      probe.remove();
      return { out, grey, count: swatches.length };
    });
    expect(facts.count, 'legend squares: four levels, few requests, none').toBe(6);
    for (const f of facts.out) {
      expect(f.swatch, 'legend square of level ' + f.level + ' against an hour square of level ' + f.level).toBe(f.cell);
      expect(f.swatch, 'legend square of level ' + f.level + ' is the heat colour').toBe(f.token);
      expect(f.swatch, 'legend square of level ' + f.level + ' is not the grey of the "few requests" square').not.toBe(facts.grey);
      expect(f.index, 'legend order: Lower to Higher').toBe(f.level - 1);
    }
    expect(new Set(facts.out.map((f) => f.swatch)).size, 'four different colours').toBe(4);
  });
}

// BL-496: the side column of the Blackbox charts is a scrolling flex column, and the tab strip inside it has overflow: hidden (so no minimum
// height). With a long list it was squeezed to 12 px (its buttons to 10 px) and the words were cut off. It must keep its own height; the
// charts, the bar and the title must not move, and in the side column only what comes below the strip moves down.
async function openCharts(page: Page): Promise<void> {
  await openBlackbox(page);
  await page.getByRole('button', { name: /^Open status: web-a/ }).first().click();
  await page.getByRole('button', { name: 'Open charts' }).first().click();
  await expect(page.locator('dialog.bbc[open] canvas').first()).toBeVisible();
}

test('UIFIX-16a Blackbox charts: the Counters and Programs tabs are 24 px or more tall and their words are not cut off', async ({ page }) => {
  await openCharts(page);
  const facts = await page.locator('.bbc-tabs').evaluate((tabs) => {
    const side = tabs.parentElement!;
    return {
      tabsHeight: tabs.getBoundingClientRect().height,
      clipped: tabs.scrollHeight > tabs.clientHeight,
      buttons: [...tabs.querySelectorAll('button')].map((b) => ({ text: b.textContent, h: b.getBoundingClientRect().height, w: b.getBoundingClientRect().width, clipped: b.scrollHeight > b.clientHeight })),
      sideScrolls: side.scrollHeight > side.clientHeight
    };
  });
  expect(facts.buttons.length).toBe(2);
  for (const b of facts.buttons) {
    expect(b.h, b.text + ' tab height').toBeGreaterThanOrEqual(24);
    expect(b.w, b.text + ' tab width').toBeGreaterThanOrEqual(24);
    expect(b.clipped, b.text + ': text cut off').toBe(false);
  }
  expect(facts.clipped, 'the strip is shorter than its content').toBe(false);
  expect(facts.tabsHeight).toBeGreaterThanOrEqual(24);
  // The window of the demo has more in the side column than fits: that is the situation of the bug. Say so if the data ever stops doing that.
  expect(facts.sideScrolls, 'precondition: the side column has more than fits (else this test cannot show the squeeze)').toBe(true);
  // the Programs tab too
  await page.locator('.bbc-tabs button').nth(1).click();
  const programs = await page.locator('.bbc-tabs button').evaluateAll((bs) => bs.map((b) => b.getBoundingClientRect().height));
  for (const h of programs) expect(h, 'tab height after opening Programs').toBeGreaterThanOrEqual(24);
});

test('UIFIX-16b Blackbox charts: nothing outside the side column moves, and the side column keeps its size', async ({ page }) => {
  await openCharts(page);
  const facts = await page.locator('dialog.bbc[open]').evaluate((dlg) => {
    const side = dlg.querySelector('.bbc-side') as HTMLElement;
    const tabs = dlg.querySelector('.bbc-tabs') as HTMLElement;
    const snap = () => [...dlg.querySelectorAll('*')].filter((e) => !side.contains(e) && !e.closest('svg')).map((e) => {
      const r = e.getBoundingClientRect();
      return { name: e.tagName.toLowerCase() + '.' + (e.getAttribute('class') ?? '').split(' ')[0] + ' ' + (e.textContent ?? '').trim().slice(0, 20), box: [r.left, r.top, r.width, r.height] };
    });
    const sideBox = () => { const r = side.getBoundingClientRect(); return [r.left, r.top, r.width, r.height]; };
    const inSide = () => [...side.children].map((e) => e.getBoundingClientRect().top);
    const now = snap(), nowSide = sideBox(), nowIn = inSide(), nowH = tabs.getBoundingClientRect().height;
    // the old rule put back: the strip may shrink again
    const sheet = document.createElement('style');
    sheet.textContent = '.bbc-tabs { flex-shrink: 1 !important; transition: none !important; }'; // no transition, or the computed value is still on its way from the new value
    document.head.appendChild(sheet);
    void document.body.offsetHeight;
    const old = snap(), oldSide = sideBox(), oldIn = inSide(), oldH = tabs.getBoundingClientRect().height;
    sheet.remove();
    const moved = now.filter((n, i) => !old[i] || n.box.some((v, j) => Math.abs(v - old[i].box[j]) > 0.05)).map((n) => n.name);
    return { moved, total: now.length, sideSame: nowSide.every((v, i) => Math.abs(v - oldSide[i]) < 0.05), nowH, oldH, shift: nowIn.map((t, i) => Math.round((t - oldIn[i]) * 10) / 10) };
  });
  expect(facts.total, 'boxes outside the side column that were compared').toBeGreaterThan(10);
  expect(facts.oldH, 'precondition: with the old rule the strip is squeezed').toBeLessThan(24);
  expect(facts.nowH).toBeGreaterThanOrEqual(24);
  expect(facts.moved, 'boxes outside the side column that moved').toEqual([]);
  expect(facts.sideSame, 'the side column itself keeps its place and size').toBe(true);
  // inside the column: the strip stays put, everything below it moves down by exactly what the strip grew
  expect(facts.shift[0], 'the strip starts where it did').toBe(0);
  for (const d of facts.shift.slice(1)) expect(d, 'what comes below the strip moves down by the growth of the strip').toBeCloseTo(facts.nowH - facts.oldH, 1);
});

const SCALL_LABELS = ['.scall-zone', '.y-peak', '.y-zero', '.scall-map-drag'];

async function openSlowCallScreen(page: Page): Promise<void> {
  // The start screen: "All cases" is in the header on a wide window and in the More menu on a narrow one.
  if (!(await page.locator('#start-title').isVisible())) {
    const direct = page.getByRole('button', { name: /^All cases/ }).first();
    if (await direct.isVisible()) await direct.click();
    else {
      await page.getByRole('button', { name: 'More', exact: true }).click();
      await page.getByRole('menuitem', { name: 'All cases', exact: true }).click();
    }
  }
  await expect(page.locator('#start-title')).toBeVisible();
  await openCase(page, 'Orders API stall, 19 October');
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  const diamond = page.locator('.panel[data-lane=audit] .mark.flagged').first();
  await expect(diamond).toBeVisible();
  await diamond.scrollIntoViewIfNeeded();
  const box = await diamond.boundingBox();
  if (!box) throw new Error('the diamond has no box');
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  await expect(page.getByTestId('slow-call-screen')).toBeVisible();
  await expect(page.getByTestId('scall-lanes')).toBeVisible();
  await expect(page.getByTestId('scall-lane-sql')).toBeVisible();
  await expect(page.locator('.y-peak').first()).toBeVisible();
}

test('UIFIX-10a Slow call screen: the zone label, the peak and zero labels and the "drag" hint are at least 11px', async ({ page }) => {
  await openSlowCallScreen(page);
  await expect(page.getByTestId('scall-map-box')).toBeVisible();
  for (const selector of SCALL_LABELS) {
    const sizes = await page.locator(selector).evaluateAll((els) => els.map((e) => parseFloat(getComputedStyle(e).fontSize)));
    expect(sizes.length, selector + ' is on the screen').toBeGreaterThan(0);
    for (const s of sizes) expect(s, selector + ' font size').toBeGreaterThanOrEqual(11);
  }
});

type Rect = { l: number; t: number; w: number; h: number };

/** Lane, minimap and label rectangles now, then again with the old 10px put back on the four labels. */
async function scallRects(page: Page): Promise<{ now: Record<string, Rect[]>; old: Record<string, Rect[]>; labels: number }> {
  return page.evaluate((selectors) => {
    const rect = (e: Element): Rect => { const r = e.getBoundingClientRect(); return { l: r.left, t: r.top, w: r.width, h: r.height }; };
    const grab = (): Record<string, Rect[]> => ({
      lanes: [...document.querySelectorAll('[data-testid^="scall-lane-"]')].map(rect),
      chart: [...document.querySelectorAll('[data-testid^="scall-lane-"] svg')].map(rect),
      map: [...document.querySelectorAll('[data-testid="scall-map"], [data-testid="scall-map-box"]')].map(rect),
      axis: [...document.querySelectorAll('.scall-axis')].map(rect),
      readout: [...document.querySelectorAll('[data-testid="scall-readout"]')].map(rect),
      page: [rect(document.documentElement)]
    });
    const now = grab();
    const saved: [HTMLElement, string | null][] = [];
    document.querySelectorAll<HTMLElement>(selectors.join(',')).forEach((el) => {
      saved.push([el, el.getAttribute('style')]);
      el.style.setProperty('font-size', '10px', 'important');
    });
    void document.body.offsetHeight;
    const old = grab();
    for (const [el, style] of saved) { if (style === null) el.removeAttribute('style'); else el.setAttribute('style', style); }
    return { now, old, labels: saved.length };
  }, SCALL_LABELS);
}

for (const size of [{ width: 1440, height: 900 }, { width: 1100, height: 800 }, { width: 1024, height: 768 }]) {
  test('UIFIX-10b Slow call screen at ' + size.width + 'x' + size.height + ': the lanes and the minimap keep every rectangle, and the labels fit', async ({ page }) => {
    await page.setViewportSize(size);
    await openSlowCallScreen(page);
    await expect(page.getByTestId('scall-map-box')).toBeVisible();
    const { now, old, labels } = await scallRects(page);
    expect(labels, 'labels found to compare').toBeGreaterThanOrEqual(4);
    for (const key of Object.keys(now)) {
      expect(now[key].length, key + ' rectangles found').toBeGreaterThan(0);
      expect(now[key].length, key + ' count').toBe(old[key].length);
      now[key].forEach((r, i) => {
        const o = old[key][i];
        for (const f of ['l', 't', 'w', 'h'] as const) expect(Math.abs(r[f] - o[f]), key + '[' + i + '].' + f + ' now ' + r[f] + ', with 10px labels ' + o[f]).toBeLessThan(0.05);
      });
    }
    // Each label sits inside the box that carries it, the peak and zero labels of a lane do not touch, and the zone label keeps clear of the axis title and the ticks.
    const facts = await page.evaluate(() => {
      type B = { x: number; y: number; w: number; h: number };
      const r = (e: Element | Range): B => { const b = e.getBoundingClientRect(); return { x: b.left, y: b.top, w: b.width, h: b.height }; };
      // The painted line of text: as wide as the text and one font size tall, centred in the line (the font's own leading is not ink).
      const text = (e: Element): B => { const range = document.createRange(); range.selectNodeContents(e); const b = r(range); const fs = parseFloat(getComputedStyle(e).fontSize); return { x: b.x, y: b.y + (b.h - fs) / 2, w: b.w, h: fs }; };
      const inside = (a: B, p: B) => a.x >= p.x - 0.5 && a.y >= p.y - 0.5 && a.x + a.w <= p.x + p.w + 0.5 && a.y + a.h <= p.y + p.h + 0.5;
      const touch = (a: B, b: B) => Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x) > 0 && Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y) > 0;
      const out: string[] = [];
      document.querySelectorAll('.y-peak').forEach((peak) => {
        const zero = peak.parentElement?.querySelector('.y-zero');
        const host = peak.parentElement;
        if (!zero || !host) { out.push('peak without zero or host'); return; }
        const a = r(peak), b = r(zero), h = r(host);
        if (!inside(a, h)) out.push('peak outside its lane: ' + JSON.stringify([a, h]));
        if (!inside(b, h)) out.push('zero outside its lane: ' + JSON.stringify([b, h]));
        if (touch(a, b)) out.push('peak and zero overlap: ' + JSON.stringify([a, b]));
      });
      const zone = document.querySelector('.scall-zone'), axis = document.querySelector('.scall-axis'), title = document.querySelector('.scall-xtitle');
      if (zone && axis) {
        const z = r(zone);
        if (!inside(z, r(axis))) out.push('zone label outside the axis: ' + JSON.stringify([z, r(axis)]));
        if (title && touch(text(title), text(zone))) out.push('zone label touches the axis title');
        document.querySelectorAll('.tick').forEach((tick) => { if (touch(text(tick), text(zone))) out.push('zone label touches a tick'); });
      }
      const drag = document.querySelector('.scall-map-drag'), box = document.querySelector('[data-testid="scall-map-box"]');
      if (drag && box && !inside(r(drag), r(box))) out.push('drag hint outside the minimap box: ' + JSON.stringify([r(drag), r(box)]));
      return out;
    });
    expect(facts, 'label collisions').toEqual([]);
  });
}

for (const width of [320, 360, 390]) {
  test('UIFIX-10c Slow call screen on a ' + width + ' px phone: the page does not scroll sideways, the labels are 11px', async ({ page }) => {
    await page.setViewportSize({ width, height: 800 });
    await openSlowCallScreen(page);
    const facts = await page.evaluate((selectors) => ({
      over: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      sizes: selectors.flatMap((s) => [...document.querySelectorAll(s)].map((e) => parseFloat(getComputedStyle(e).fontSize)))
    }), SCALL_LABELS);
    expect(facts.over, 'sideways overflow in px').toBeLessThanOrEqual(0);
    for (const s of facts.sizes) expect(s).toBeGreaterThanOrEqual(11);
  });
}

// BL-475 group 4: the last small targets of the audit. Like UIFIX-09 the old rules of each target are put back as inline styles and every
// rectangle of the page part is compared in one evaluation: a target may grow (that is the point), but it may not move anything else, and its
// margin box (the box plus its margins, which is what the neighbours see) must stay what it was. Desktop wins over the phone.
type Rules = [string, Record<string, string>][];

/** Every box inside `root` now and with `rules` put back. `moved` lists the boxes that differ; targets are compared by margin box. */
async function layoutMoves(page: Page, root: string, rules: Rules, hideCss = ''): Promise<{ touched: number; moved: string[]; total: number }> {
  return page.locator(root).first().evaluate((container, [rs, css]) => {
    const targets = new Set<Element>();
    for (const [selector] of rs) container.querySelectorAll(selector).forEach((e) => targets.add(e));
    // Boxes inside a closed <details> are skipped: the browser lays them out only when something asks, so the first read can say 0 x 0 and the second a real size (seen in the Slow call screen's closed "How to read" notes). They are not on screen, so they cannot have moved.
    const snap = () => [...container.querySelectorAll('*')].filter((e) => !e.closest('svg') || e.tagName.toLowerCase() === 'svg').filter((e) => { const d = e.closest('details:not([open])'); return !d || e === d || !!e.closest('summary'); }).map((e) => {
      const r = e.getBoundingClientRect();
      const m = targets.has(e) ? getComputedStyle(e) : null;
      const ml = m ? parseFloat(m.marginLeft) : 0, mt = m ? parseFloat(m.marginTop) : 0, mr = m ? parseFloat(m.marginRight) : 0, mb = m ? parseFloat(m.marginBottom) : 0;
      return { name: e.tagName.toLowerCase() + '.' + (e.getAttribute('class') ?? '').split(' ')[0] + ' ' + (e.textContent ?? '').trim().slice(0, 24), box: [r.left - ml, r.top - mt, r.width + ml + mr, r.height + mt + mb] };
    });
    const now = snap();
    const saved: [HTMLElement, string | null][] = [];
    for (const [selector, declarations] of rs) {
      container.querySelectorAll<HTMLElement>(selector).forEach((el) => {
        saved.push([el, el.getAttribute('style')]);
        for (const [k, v] of Object.entries(declarations)) el.style.setProperty(k, v, 'important');
      });
    }
    const sheet = document.createElement('style');
    sheet.textContent = css;
    document.head.appendChild(sheet);
    void document.body.offsetHeight;
    const old = snap();
    sheet.remove();
    for (const [el, style] of saved) { if (style === null) el.removeAttribute('style'); else el.setAttribute('style', style); }
    const moved: string[] = [];
    now.forEach((n, i) => { if (!old[i] || n.box.some((v, j) => Math.abs(v - old[i].box[j]) > 0.05)) moved.push(n.name + ' now ' + n.box.map((v) => v.toFixed(2)).join(',') + ' old ' + (old[i]?.box ?? []).map((v) => v.toFixed(2)).join(',')); });
    return { touched: saved.length, moved: moved.slice(0, 6), total: now.length };
  }, [rules, hideCss] as [Rules, string]);
}

async function expectNothingMoved(page: Page, root: string, rules: Rules, label: string, hideCss = ''): Promise<void> {
  const facts = await layoutMoves(page, root, rules, hideCss);
  expect(facts.touched, label + ': no target found to compare').toBeGreaterThan(0);
  expect(facts.total, label + ': nothing measured').toBeGreaterThan(5);
  expect(facts.moved, label + ': boxes that moved against the old rules').toEqual([]);
}

/**
 * The area that answers a press: the box of every match joined with its absolutely positioned ::after. It must be 24 x 24 px or more, and a
 * press just inside each corner of it must land on the control (nothing in front of it). The box itself is not asked to grow (develop's
 * focus ring and layout stay as they are).
 */
async function expectHitArea(page: Page, selector: string, label: string): Promise<void> {
  const facts = await page.locator(selector).evaluateAll((els) => els.map((e) => {
    const r = e.getBoundingClientRect(), cs = getComputedStyle(e), p = getComputedStyle(e, '::after');
    let left = r.left, top = r.top, right = r.right, bottom = r.bottom;
    if (p.content !== 'none' && p.position === 'absolute' && p.pointerEvents !== 'none') {
      const x = r.left + (parseFloat(cs.borderLeftWidth) || 0) + parseFloat(p.left), y = r.top + (parseFloat(cs.borderTopWidth) || 0) + parseFloat(p.top);
      left = Math.min(left, x); top = Math.min(top, y); right = Math.max(right, x + parseFloat(p.width)); bottom = Math.max(bottom, y + parseFloat(p.height));
    }
    e.scrollIntoView({ block: 'center' });
    const q = e.getBoundingClientRect(), dx = q.left - r.left, dy = q.top - r.top;
    const wrong = [[left + 1, top + 1], [right - 1, top + 1], [left + 1, bottom - 1], [right - 1, bottom - 1], [(left + right) / 2, top + 1], [(left + right) / 2, bottom - 1]]
      .filter(([x, y]) => document.elementFromPoint(x + dx, y + dy)?.closest('button, a') !== e).length;
    return { w: right - left, h: bottom - top, wrong };
  }));
  expect(facts.length, label + ': nothing found').toBeGreaterThan(0);
  for (const f of facts) {
    expect(f.w, label + ' hit area width').toBeGreaterThanOrEqual(24);
    expect(f.h, label + ' hit area height').toBeGreaterThanOrEqual(24);
    expect(f.wrong, label + ': presses inside the hit area that miss the control').toBe(0);
  }
}

async function openReviewStep(page: Page): Promise<void> {
  await openBlackbox(page);
  await page.getByRole('button', { name: 'Open status: web-a' }).waitFor();
  await page.getByRole('button', { name: 'Place a Blackbox' }).click();
  await page.getByRole('textbox', { name: /^Name/ }).fill('sql-prod-01');
  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await page.getByRole('radio', { name: /On another server/ }).click();
  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'What should it record?' })).toBeVisible();
  for (let i = 0; i < 5; i++) await page.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Build the pack' })).toBeVisible();
  await expect(page.locator('.with-change button.change').first()).toBeVisible();
}

// develop has no pseudo-element on these targets: the old state is the same page with the pseudo-element removed.
const REVIEW_OLD: Rules = [['.with-change .change', { position: 'static' }]];
const REVIEW_HIDE = '.with-change .change::after { content: none !important; }';

test('UIFIX-14a Place a Blackbox, Review: the "Change" link next to "Runs" answers a press in 24 x 24 px or more and the card does not move', async ({ page, allow }) => {
  allow(/blackbox\/validate/, 'the wizard asks the server to check the name step on the way to Review');
  await openReviewStep(page);
  await expectHitArea(page, '.with-change button.change', 'Review Change link');
  await expectNothingMoved(page, '.cards', REVIEW_OLD, 'Review step', REVIEW_HIDE);
});

const UPDATE_OLD: Rules = [['.update-bar-link', { position: 'static' }]];
const UPDATE_HIDE = '.update-bar-link::after { content: none !important; }';

async function openUpdateBar(page: Page): Promise<void> {
  await page.evaluate(() => { location.hash = '#update=available'; });
  const direct = page.getByRole('button', { name: 'Glossary', exact: true });
  if (await direct.isVisible()) await direct.click();
  else {
    await page.getByRole('button', { name: 'More', exact: true }).click();
    await page.getByRole('menuitem', { name: 'Glossary' }).click();
  }
  const dialog = page.getByRole('dialog').first();
  await dialog.getByTestId('update-check-button').click();
  await expect(page.getByTestId('update-bar-link')).toBeAttached();
}

for (const width of [1440, 390]) {
  test('UIFIX-14b Update bar at ' + width + ' px: "Open the release page" answers a press in 24 px or more and the bar keeps its size', async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await openUpdateBar(page);
    await page.keyboard.press('Escape'); // the help window is modal: close it so nothing sits in front of the bar
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expectHitArea(page, '[data-testid=update-bar-link]', 'Update bar link');
    await expectNothingMoved(page, '.update-bar', UPDATE_OLD, 'Update bar', UPDATE_HIDE);
  });
}

const COUNT_OLD: Rules = [['td.numeric .api-select', { position: 'static' }]];
const COUNT_HIDE = 'td.numeric .api-select::after { content: none !important; }';

for (const width of [1440, 390]) {
  test('UIFIX-14c Where is it? at ' + width + ' px: the unfinished-request counts answer a press in 24 px or more and the table does not move', async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.getByRole('button', { name: 'Where is it?', exact: true }).click();
    await page.locator('summary', { hasText: 'Quality of the data' }).click();
    const counts = page.locator('td.numeric button.api-select');
    await expect(counts.first()).toBeVisible();
    await expectHitArea(page, 'td.numeric button.api-select', 'Unfinished count');
    await expectNothingMoved(page, 'table:has(td.numeric button.api-select)', COUNT_OLD, 'Unfinished-requests table', COUNT_HIDE);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), 'sideways overflow').toBeLessThanOrEqual(0);
  });
}

// BL-475 group 4, review of PR 156: the keyboard focus ring and the look at rest of these targets are what develop draws (the hit area is a
// pseudo-element, the box is untouched). Like UIFIX-13 this takes the pseudo-element away as a stylesheet and compares the pictures byte for byte.
/** How many pixels differ between two PNG pictures, and the largest step of any colour channel (compared in the browser, so no image library is needed). */
async function pictureDifference(page: Page, a: Buffer, b: Buffer): Promise<{ pixels: number; levels: number; sameSize: boolean }> {
  return page.evaluate(async ([x, y]) => {
    const load = async (b64: string) => {
      const bytes = Uint8Array.from(atob(b64), (ch) => ch.charCodeAt(0)); // no fetch of a data: address (the page's security policy refuses it)
      const bmp = await createImageBitmap(new Blob([bytes], { type: 'image/png' }));
      const c = new OffscreenCanvas(bmp.width, bmp.height), g = c.getContext('2d')!;
      g.drawImage(bmp, 0, 0);
      return g.getImageData(0, 0, bmp.width, bmp.height);
    };
    const p = await load(x), q = await load(y);
    if (p.width !== q.width || p.height !== q.height) return { pixels: Infinity, levels: 255, sameSize: false };
    let pixels = 0, levels = 0;
    for (let i = 0; i < p.data.length; i += 4) {
      let m = 0;
      for (let k = 0; k < 4; k++) m = Math.max(m, Math.abs(p.data[i + k] - q.data[i + k]));
      if (m) { pixels++; levels = Math.max(levels, m); }
    }
    return { pixels, levels, sameSize: true };
  }, [a.toString('base64'), b.toString('base64')]);
}

const OLD_FOCUS: Record<string, { css: string; open: (page: Page) => Promise<void> }> = {
  'Review Change link': { css: '.with-change .change { position: static; } .with-change .change::after { content: none; }', open: openReviewStep },
  'update bar link': {
    css: '.update-bar-link { position: static; } .update-bar-link::after { content: none; }',
    open: async (page) => { await openUpdateBar(page); await page.keyboard.press('Escape'); await expect(page.getByRole('dialog')).toHaveCount(0); }
  },
  'unfinished count': {
    css: 'td.numeric .api-select { position: static; } td.numeric .api-select::after { content: none; }',
    open: async (page) => { await page.getByRole('button', { name: 'Where is it?', exact: true }).click(); await page.locator('summary', { hasText: 'Quality of the data' }).click(); }
  }
};
const FOCUS_TARGET: Record<string, string> = { 'Review Change link': '.with-change button.change', 'update bar link': '[data-testid=update-bar-link]', 'unfinished count': 'td.numeric button.api-select' };

for (const scheme of ['dark', 'light'] as const) {
  for (const name of Object.keys(OLD_FOCUS)) {
    test('UIFIX-14f ' + name + ': the keyboard focus ring and the rest look are what the old rules draw (' + scheme + ')', async ({ page, allow }) => {
      allow(/blackbox\/validate/, 'the wizard asks the server to check the name step on the way to Review');
      await page.emulateMedia({ colorScheme: scheme });
      await OLD_FOCUS[name].open(page);
      const target = page.locator(FOCUS_TARGET[name]).first();
      await expect(target).toBeVisible();
      await target.scrollIntoViewIfNeeded();
      // the old margin box (what the neighbours see), widened by 14px for the ring
      const clip = await target.evaluate((e) => {
        const r = e.getBoundingClientRect(), s = getComputedStyle(e);
        const x = r.left - parseFloat(s.marginLeft) - 14, y = r.top - parseFloat(s.marginTop) - 14;
        const fx = Math.max(0, Math.floor(x)), fy = Math.max(0, Math.floor(y)); // whole pixels: a fractional clip is resampled
        return { x: fx, y: fy, width: Math.ceil(x + r.width + parseFloat(s.marginLeft) + parseFloat(s.marginRight) + 28 - fx), height: Math.ceil(y + r.height + parseFloat(s.marginTop) + parseFloat(s.marginBottom) + 28 - fy) };
      });
      await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; caret-color: transparent !important; }' });
      const take = async (): Promise<Buffer[]> => {
        await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
        await page.mouse.move(0, 0);
        const rest = await page.screenshot({ clip });
        await target.focus();
        await page.keyboard.press('Shift');
        const focused = await page.screenshot({ clip });
        await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
        return [rest, focused];
      };
      const now = await take();
      const style = await page.addStyleTag({ content: important(OLD_FOCUS[name].css) });
      const old = await take();
      await style.evaluate((el) => el.remove());
      // Byte for byte, except the rounded ring of the Review link: a positioned control rasterises the four arcs of its ring a hair differently
      // (measured: 4 to 9 pixels, up to 8 of 255 colour levels, differing from run to run on the same build), so there a handful of pixels may differ slightly. A moved or resized ring differs by far more.
      const tolerance = name === 'Review Change link' ? { pixels: 12, levels: 8 } : { pixels: 0, levels: 0 };
      for (const [i, what] of ['at rest', 'focused'].entries()) {
        const d = await pictureDifference(page, now[i], old[i]);
        expect(d.pixels, what + ': pixels that differ from the old rules (largest step ' + d.levels + ')').toBeLessThanOrEqual(tolerance.pixels);
        expect(d.levels, what + ': the largest colour step against the old rules').toBeLessThanOrEqual(tolerance.levels);
      }
      expect(now[1].equals(now[0]), 'focused: nothing is drawn, so the ring is missing').toBe(false);
    });
  }
}

// UIFIX-17: the UTC times of the two windows of the Investigation timeline were only in a title (tooltip) and in clipped, visually hidden text.
// They come from the same fields as the tooltip (the window boxes, sent to the server as UTC) and are now drawn in the readout under the pickers.
async function openTimelineReadout(page: Page): Promise<void> {
  await openSlowCallScreenStart(page);
  await page.getByRole('button', { name: 'Open the Investigation timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation timeline' })).toBeVisible();
  await expect(page.locator('.tracing-chart')).toBeVisible();
}

async function openSlowCallScreenStart(page: Page): Promise<void> {
  if (!(await page.locator('#start-title').isVisible())) {
    const direct = page.getByRole('button', { name: /^All cases/ }).first();
    if (await direct.isVisible()) await direct.click();
    else {
      await page.getByRole('button', { name: 'More', exact: true }).click();
      await page.getByRole('menuitem', { name: 'All cases', exact: true }).click();
    }
  }
  await expect(page.locator('#start-title')).toBeVisible();
  await openCase(page, 'Orders API stall, 19 October');
}

/** What is drawn for one UTC echo: painted text boxes, not clipped, not hidden, inside the card, 4.5:1 text contrast. */
async function utcEchoFacts(page: Page, testId: string) {
  return page.evaluate((id) => {
    const el = document.querySelector<HTMLElement>('[data-testid="' + id + '"]')!;
    const card = document.querySelector<HTMLElement>('.trctl')!.getBoundingClientRect();
    const s = getComputedStyle(el);
    const range = document.createRange();
    range.selectNodeContents(el);
    const rects = [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
    const parse = (c: string) => { const m = /rgba?\(([^)]+)\)/.exec(c)!; const p = m[1].split(/[ ,/]+/).map(Number); return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 }; };
    const lin = (v: number) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
    const lum = (c: { r: number; g: number; b: number }) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
    let bg = { r: 255, g: 255, b: 255, a: 1 };
    for (let e: Element | null = el; e; e = e.parentElement) { const c = parse(getComputedStyle(e).backgroundColor); if (c.a > 0.99) { bg = c; break; } }
    const l1 = lum(parse(s.color)), l2 = lum(bg);
    let clipped = false;
    for (let e: HTMLElement | null = el; e && e !== document.body; e = e.parentElement) {
      const t = getComputedStyle(e);
      if (t.clipPath !== 'none' || t.visibility === 'hidden' || t.display === 'none' || t.opacity === '0') clipped = true;
    }
    const top = rects.length ? document.elementFromPoint(rects[0].x + rects[0].width / 2, rects[0].y + rects[0].height / 2) : null;
    return {
      text: (el.textContent || '').replace(/\s+/g, ' ').trim(),
      painted: rects.length,
      narrowest: Math.min(...rects.map((r) => r.width)),
      tallest: Math.max(...rects.map((r) => r.height)),
      inCard: rects.every((r) => r.left >= card.left - 0.5 && r.right <= card.right + 0.5 && r.top >= card.top - 0.5 && r.bottom <= card.bottom + 0.5),
      position: s.position,
      fontSize: parseFloat(s.fontSize),
      clipped,
      onTop: !!top && (el === top || el.contains(top) || top.contains(el)),
      ratio: (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05)
    };
  }, testId);
}

for (const scheme of ['dark', 'light'] as const) {
  test('UIFIX-17a Investigation timeline (' + scheme + '): both UTC times are drawn text, not clipped, readable, and follow the windows', async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await openTimelineReadout(page);
    const incident = await utcEchoFacts(page, 'utc-echo-incident');
    const reference = await utcEchoFacts(page, 'utc-echo-reference');
    // The demo clock is 2026-10-20T09:00Z: incident 08:00-09:00 UTC, comparison 07:00-08:00 UTC (the same fields the tooltip reads).
    expect(incident.text).toBe('UTC 2026-10-20T08:00Z \u2013 2026-10-20T09:00Z');
    expect(reference.text).toBe('UTC 2026-10-20T07:00Z \u2013 2026-10-20T08:00Z');
    for (const [name, f] of [['incident', incident], ['comparison', reference]] as const) {
      expect(f.painted, name + ': text boxes painted').toBeGreaterThan(0);
      expect(f.narrowest, name + ': not a 1 px clipped box').toBeGreaterThan(20);
      expect(f.tallest, name + ': not a 1 px clipped box').toBeGreaterThan(8);
      expect(f.position, name + ': not pulled out of the flow').not.toBe('absolute');
      expect(f.clipped, name + ': no clip, hidden or transparent ancestor').toBe(false);
      expect(f.inCard, name + ': inside the controls card').toBe(true);
      expect(f.onTop, name + ': nothing covers it').toBe(true);
      expect(f.fontSize, name + ': at least 11 px').toBeGreaterThanOrEqual(11);
      expect(f.ratio, name + ': text contrast').toBeGreaterThanOrEqual(4.5);
    }
    // A quick range changes the visible text, so it is read live from the window boxes and not a constant.
    await page.locator('.trctl .chip[data-quick-id="4h"]').click();
    await expect(page.getByTestId('utc-echo-incident')).toHaveText('UTC 2026-10-20T05:00Z \u2013 2026-10-20T09:00Z');
    expect((await utcEchoFacts(page, 'utc-echo-incident')).painted).toBeGreaterThan(0);
  });
}

test('UIFIX-17b Investigation timeline: the UTC text stays UTC when "Times in" changes; only the zone name beside it changes', async ({ page }) => {
  await openTimelineReadout(page);
  const line = page.getByTestId('tp-metaline');
  const zone = line.locator('b');
  const echo = async () => [await page.getByTestId('utc-echo-incident').innerText(), await page.getByTestId('utc-echo-reference').innerText()];
  const before = await echo();
  const zoneBefore = await zone.innerText();
  await page.getByLabel('Show and type times in').selectOption({ label: 'India' });
  await expect(zone).not.toHaveText(zoneBefore);
  expect(await echo(), 'the UTC strings did not move with the zone').toEqual(before);
  expect((await utcEchoFacts(page, 'utc-echo-incident')).painted).toBeGreaterThan(0);
  await expect(line).toContainText('Times are in');
});

for (const width of [320, 360, 390]) {
  for (const scheme of ['dark', 'light'] as const) {
    test('UIFIX-17c Investigation timeline at ' + width + ' px (' + scheme + '): the UTC lines wrap inside the card and the page does not scroll sideways', async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme });
      await openTimelineReadout(page);
      await page.setViewportSize({ width, height: 800 });
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), { message: 'no sideways scroll' }).toBe(true);
      for (const id of ['utc-echo-incident', 'utc-echo-reference']) {
        const f = await utcEchoFacts(page, id);
        expect(f.painted, id).toBeGreaterThan(0);
        expect(f.inCard, id + ' inside the card').toBe(true);
        expect(f.clipped, id).toBe(false);
      }
      const spill = await page.evaluate(() => { const m = document.querySelector<HTMLElement>('[data-testid="tp-metaline"]')!; return m.scrollWidth - m.clientWidth; });
      expect(spill, 'the readout paragraph has nothing wider than itself').toBeLessThanOrEqual(1);
    });
  }
}

test('UIFIX-17d Investigation timeline on a desktop: the readout is one line and the controls card is not taller at 1100, 1280, 1366, 1440 and 1920 px', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openTimelineReadout(page);
  const seen: string[] = [];
  for (const width of [1920, 1440, 1366, 1280, 1100]) {
    await page.setViewportSize({ width, height: 900 });
    await page.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));
    const m = await page.evaluate(() => {
      const line = document.querySelector<HTMLElement>('[data-testid="tp-metaline"]')!;
      const lh = parseFloat(getComputedStyle(line).lineHeight) || parseFloat(getComputedStyle(line).fontSize) * 1.4;
      const sources = document.querySelector<HTMLElement>('[data-testid="trace-sources"]')!.getBoundingClientRect();
      return { h: line.getBoundingClientRect().height, lh, card: document.querySelector('.trctl')!.getBoundingClientRect().height, line: line.getBoundingClientRect().bottom, sources: sources.top };
    });
    seen.push(width + ' px: readout ' + Math.round(m.h) + ' px, card ' + Math.round(m.card) + ' px');
    expect(m.h, width + ' px: one line of text (measured 17 px before the change; seen so far: ' + seen.join('; ') + ')').toBeLessThanOrEqual(m.lh * 1.5);
    expect(m.card, width + ' px: the controls card keeps its measured height of 320 px').toBeLessThanOrEqual(322);
  }
});

// UIFIX-19 (BL-492): the three small tap targets of the Slow call screen get 24 px press areas without moving anything (Slow call lane rule, BL-005:
// the lanes keep their height, nothing scrolls inside, the lanes stay visible together). The crumb button and the minimap box grow with an invisible ::after
// (box, layout and focus ring stay develop's); the lane bars grow with an invisible rectangle inside the bar's <g> that is cut so that no two areas
// overlap (a bar in a stack of bar rows gets what its row allows, a bar with room reaches 24 px).
const SCALL_SIZES = [{ width: 1440, height: 900 }, { width: 1100, height: 800 }, { width: 1024, height: 768 }, { width: 390, height: 844 }];

/** The press area of every match (its box joined with its ::after) must be 24 x 24 px or more, answer a press at its centre and corners, and leave every other control alone. */
async function expectScallHitArea(page: Page, selector: string, label: string, minWidth = 24, within = ''): Promise<void> {
  // within: look for other controls only inside the closest such ancestor (a table row), not in the whole page: behind a side drawer the page keeps its own controls
  const facts = await page.locator(selector).evaluateAll((els, scopeSel) => els.map((e) => {
    e.scrollIntoView({ block: 'center' });
    const r = e.getBoundingClientRect(), cs = getComputedStyle(e), p = getComputedStyle(e, '::after');
    let left = r.left, top = r.top, right = r.right, bottom = r.bottom;
    if (p.content !== 'none' && p.position === 'absolute' && p.pointerEvents !== 'none') {
      const x = r.left + (parseFloat(cs.borderLeftWidth) || 0) + parseFloat(p.left), y = r.top + (parseFloat(cs.borderTopWidth) || 0) + parseFloat(p.top);
      left = Math.min(left, x); top = Math.min(top, y); right = Math.max(right, x + parseFloat(p.width)); bottom = Math.max(bottom, y + parseFloat(p.height));
    }
    const corners = (l: number, t: number, rr: number, b: number) => [[l + 1, t + 1], [rr - 1, t + 1], [l + 1, b - 1], [rr - 1, b - 1]];
    const spots = [...corners(r.left, r.top, r.right, r.bottom), [(left + right) / 2, (top + bottom) / 2]];
    if (right - left > r.width + 0.5 || bottom - top > r.height + 0.5) {
      const x = r.left + (parseFloat(cs.borderLeftWidth) || 0) + parseFloat(p.left), y = r.top + (parseFloat(cs.borderTopWidth) || 0) + parseFloat(p.top);
      spots.push(...corners(x, y, x + parseFloat(p.width), y + parseFloat(p.height)));
    }
    const wrong = spots.filter(([x, y]) => { const hit = document.elementFromPoint(x, y); return !(hit === e || (hit && e.contains(hit))); }).length;
    // every other control of the page must stay out of the press area
    const others = [...(scopeSel ? e.closest(scopeSel) ?? document : document).querySelectorAll('button, a[href], input, select, summary, [role=button], [role=slider], [tabindex]:not([tabindex="-1"])')]
      .filter((o) => o !== e && !o.contains(e) && !e.contains(o) && !(o.closest('svg') && e.closest('svg')))
      .filter((o) => { const b = o.getBoundingClientRect(); return Math.min(b.right, right) - Math.max(b.left, left) > 0.5 && Math.min(b.bottom, bottom) - Math.max(b.top, top) > 0.5; })
      .map((o) => (o.getAttribute('aria-label') || o.textContent || o.tagName).trim().slice(0, 30));
    return { w: right - left, h: bottom - top, wrong, others };
  }), within);
  expect(facts.length, label + ': nothing found').toBeGreaterThan(0);
  for (const f of facts) {
    expect(f.w, label + ' press area width').toBeGreaterThanOrEqual(minWidth);
    expect(f.h, label + ' press area height').toBeGreaterThanOrEqual(24);
    expect(f.wrong, label + ': corners and centre of the press area that miss the control').toBe(0);
    expect(f.others, label + ': other controls inside the press area').toEqual([]);
  }
}

const CRUMB_OLD: Rules = [['.scall-crumb .link', { position: 'static' }]];
const CRUMB_HIDE = '.scall-crumb .link::after { content: none !important; }';
const MAPBOX_OLD: Rules = [['.scall-map-box', { position: 'absolute' }]];
const MAPBOX_HIDE = '.scall-map-box::after { content: none !important; }';

for (const size of SCALL_SIZES) {
  test('UIFIX-19a Slow call screen at ' + size.width + ' px: the "Timeline" crumb answers a press in 24 px or more and the header does not move', async ({ page }) => {
    await page.setViewportSize(size);
    await openSlowCallScreen(page);
    await expectScallHitArea(page, '[data-testid=scall-back]', 'Timeline crumb');
    await expectNothingMoved(page, '[data-testid=slow-call-screen]', CRUMB_OLD, 'Slow call screen', CRUMB_HIDE);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), 'sideways overflow').toBeLessThanOrEqual(0);
  });
}

for (const width of [1440, 1100, 1024, 390, 360, 320]) {
  test('UIFIX-19b Slow call screen at ' + width + ' px: the minimap box answers a press in 24 px or more' + (width >= 1024 ? ' (a wide box gets no extra area)' : '') + ' and nothing moves', async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await openSlowCallScreen(page);
    const box = page.getByTestId('scall-map-box');
    await expect(box).toBeVisible();
    await expectScallHitArea(page, '[data-testid=scall-map-box]', 'Minimap box');
    const own = await box.evaluate((e) => { const r = e.getBoundingClientRect(), p = getComputedStyle(e, '::after'); return { w: r.width, afterW: parseFloat(p.width), afterL: parseFloat(p.left) }; });
    // a box that is 24 px or wider has no growth at all (::after is the padding box); a narrower one grows to exactly 24 px, evenly on both sides
    if (own.w >= 24) expect(own.afterL, 'no growth on a wide box').toBeGreaterThanOrEqual(0);
    else expect(own.afterW, 'the grown area is 24 px wide').toBeCloseTo(24, 1);
    await expectNothingMoved(page, '[data-testid=slow-call-screen]', MAPBOX_OLD, 'Slow call screen', MAPBOX_HIDE);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), 'sideways overflow').toBeLessThanOrEqual(0);
  });
}

test('UIFIX-19b2 Slow call screen on a phone: a press just outside the narrow minimap box (inside its press area) grabs the box and drags it', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openSlowCallScreen(page);
  const box = page.getByTestId('scall-map-box');
  await box.scrollIntoViewIfNeeded();
  const r = await box.boundingBox();
  if (!r) throw new Error('no box');
  expect(r.width, 'the box is narrower than 24 px on a phone').toBeLessThan(24);
  const before = await box.getAttribute('aria-valuenow');
  const y = r.y + r.height / 2;
  const x = r.x - (24 - r.width) / 2 + 1; // 1 px inside the left end of the press area, outside the drawn box
  expect(x).toBeLessThan(r.x);
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + 20, y, { steps: 4 });
  await expect(box).toHaveClass(/dragging/);
  await page.mouse.up();
  await expect.poll(async () => box.getAttribute('aria-valuenow'), 'the box moved with the drag').not.toBe(before);
});

/** The drawn bars with their press rectangles, in screen pixels, and the lane drawing each one sits in. */
async function barFacts(page: Page) {
  await page.getByTestId('scall-lanes').scrollIntoViewIfNeeded();
  // the lanes measure their own drawing after the first paint: wait until two reads a moment apart agree
  const read = () => page.evaluate(() => [...document.querySelectorAll('.bar-hit')].map((e) => { const b = e.getBoundingClientRect(); return [b.left, b.top, b.width, b.height].map((v) => Math.round(v * 10)); }).join('|'));
  let last = await read();
  for (let i = 0; i < 20; i++) { await page.waitForTimeout(150); const now = await read(); if (now === last) break; last = now; }
  return page.evaluate(() => {
    const rect = (e: Element) => { const b = e.getBoundingClientRect(); return { l: b.left, t: b.top, r: b.right, b: b.bottom }; };
    return [...document.querySelectorAll('.bar-g')].map((g, i) => ({
      i,
      label: g.getAttribute('aria-label') ?? '',
      bar: rect(g.querySelector('.bar')!),
      hit: rect(g.querySelector('.bar-hit')!),
      svg: rect(g.closest('svg')!),
      lane: g.closest('[data-lane]')?.getAttribute('data-lane') ?? ''
    }));
  });
}

for (const size of SCALL_SIZES) {
  test('UIFIX-19c Slow call screen at ' + size.width + 'x' + size.height + ': every lane bar has a press area that holds the bar, stays in its lane, never overlaps another and is as big as its neighbours allow', async ({ page }) => {
    await page.setViewportSize(size);
    await openSlowCallScreen(page);
    const bars = await barFacts(page);
    expect(bars.length, 'bars on the screen').toBeGreaterThan(1);
    const eps = 0.6;
    const overlapArea = (a: { l: number; t: number; r: number; b: number }, b: { l: number; t: number; r: number; b: number }) =>
      Math.max(0, Math.min(a.r, b.r) - Math.max(a.l, b.l)) * Math.max(0, Math.min(a.b, b.b) - Math.max(a.t, b.t));
    for (const b of bars) {
      // holds its bar, inside its own lane drawing (never reaches the lane above or below)
      expect(b.hit.l, b.label + ' left').toBeLessThanOrEqual(b.bar.l + eps);
      expect(b.hit.r, b.label + ' right').toBeGreaterThanOrEqual(b.bar.r - eps);
      expect(b.hit.t, b.label + ' top').toBeLessThanOrEqual(b.bar.t + eps);
      expect(b.hit.b, b.label + ' bottom').toBeGreaterThanOrEqual(b.bar.b - eps);
      expect(b.hit.t, b.label + ' inside the lane drawing (top)').toBeGreaterThanOrEqual(b.svg.t - eps);
      expect(b.hit.b, b.label + ' inside the lane drawing (bottom)').toBeLessThanOrEqual(b.svg.b + eps);
      expect(b.hit.l, b.label + ' inside the lane drawing (left)').toBeGreaterThanOrEqual(b.svg.l - eps);
      expect(b.hit.r, b.label + ' inside the lane drawing (right)').toBeLessThanOrEqual(b.svg.r + eps);
      // grew: taller than the bar by something, and as wide as 24 px
      expect(b.hit.b - b.hit.t, b.label + ' taller than the bar').toBeGreaterThan(b.bar.b - b.bar.t + 1);
      expect(b.hit.r - b.hit.l, b.label + ' width').toBeGreaterThanOrEqual(24 - eps);
      // as big as the neighbours allow: 24 px tall, or each end that is short touches the drawing's edge or the area of the next bar
      const height = b.hit.b - b.hit.t;
      if (height < 24 - eps) {
        for (const end of ['t', 'b'] as const) {
          const edge = end === 't' ? b.svg.t : b.svg.b;
          const atEdge = Math.abs(b.hit[end] - edge) <= eps;
          const atNeighbour = bars.some((o) => o !== b && o.lane === b.lane && Math.min(o.hit.r, b.hit.r) - Math.max(o.hit.l, b.hit.l) > 0 && Math.abs(o.hit[end === 't' ? 'b' : 't'] - b.hit[end]) <= eps);
          expect(atEdge || atNeighbour, b.label + ' is short (' + height.toFixed(1) + ' px) and its ' + (end === 't' ? 'top' : 'bottom') + ' end touches neither the lane edge nor another area').toBe(true);
        }
      }
      for (const o of bars) {
        if (o === b) continue;
        expect(overlapArea(b.hit, o.hit), b.label + ' press area overlaps another bar\'s').toBeLessThan(0.5);
        expect(overlapArea(b.hit, o.bar), b.label + ' press area covers another bar').toBeLessThan(0.5);
      }
    }
    // the browser agrees: the middle and the four corners of each area answer for that bar
    const wrong = await page.evaluate(() => {
      const out: string[] = [];
      document.querySelectorAll('.bar-g').forEach((g) => {
        const b = g.querySelector('.bar-hit')!.getBoundingClientRect();
        for (const [x, y] of [[b.left + 1, b.top + 1], [b.right - 1, b.top + 1], [b.left + 1, b.bottom - 1], [b.right - 1, b.bottom - 1], [(b.left + b.right) / 2, (b.top + b.bottom) / 2]]) {
          const hit = document.elementFromPoint(x, y);
          if (hit?.closest('.bar-g') !== g) out.push(g.getAttribute('aria-label') + ' at ' + Math.round(x) + ',' + Math.round(y) + ' goes to ' + (hit?.getAttribute('class') ?? hit?.tagName));
        }
      });
      return out;
    });
    expect(wrong, 'presses inside an area that miss its bar').toEqual([]);
    // at least the lowest bar of the stack reaches the full 24 px on the desktop sizes (a stack of three rows shares its room)
    if (size.width >= 1024) expect(Math.max(...bars.map((b) => b.hit.b - b.hit.t)), 'the bar with room above and below').toBeGreaterThanOrEqual(24 - eps);
  });
}

for (const scheme of ['dark', 'light'] as const) {
  for (const size of SCALL_SIZES) {
    test('UIFIX-19d Slow call screen at ' + size.width + ' px (' + scheme + '): with the press areas hidden every box of the screen is where it was, and the painted lanes are the same pixels', async ({ page }) => {
      await page.emulateMedia({ colorScheme: scheme });
      await page.setViewportSize(size);
      await openSlowCallScreen(page);
      const lanes = page.getByTestId('scall-lanes');
      await lanes.scrollIntoViewIfNeeded();
      await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; caret-color: transparent !important; }' });
      await page.mouse.move(0, 0);
      const snap = () => page.evaluate(() => [...document.querySelectorAll('[data-testid=slow-call-screen] *')]
        .filter((e) => !e.matches('.bar-hit, .bar-g') && !e.closest('.bar-hit') && (!e.closest('details:not([open])') || e.matches('details, summary')))
        .map((e) => { const r = e.getBoundingClientRect(); return [e.tagName + '.' + (e.getAttribute('class') ?? '').split(' ')[0], r.left, r.top, r.width, r.height] as [string, number, number, number, number]; }));
      const clip = await lanes.boundingBox();
      if (!clip) throw new Error('no lanes box');
      const now = await snap();
      const shotNow = await page.screenshot({ clip: { x: Math.max(0, clip.x), y: Math.max(0, clip.y), width: clip.width, height: clip.height } });
      const sheet = await page.addStyleTag({ content: '.bar-hit { display: none !important; } .scall-crumb .link::after, .scall-map-box::after { content: none !important; }' });
      const old = await snap();
      const shotOld = await page.screenshot({ clip: { x: Math.max(0, clip.x), y: Math.max(0, clip.y), width: clip.width, height: clip.height } });
      await sheet.evaluate((el) => el.remove());
      expect(now.length, size.width + ' ' + scheme + ': boxes compared').toBeGreaterThan(50);
      expect(now.length).toBe(old.length);
      now.forEach((n, i) => { for (let k = 1; k < 5; k++) expect(Math.abs((n[k] as number) - (old[i][k] as number)), size.width + ' ' + scheme + ' ' + n[0] + ' value ' + k).toBeLessThan(0.05); });
      const d = await pictureDifference(page, shotNow, shotOld);
      expect(d.pixels, size.width + ' ' + scheme + ': pixels of the lanes that differ (largest step ' + d.levels + ')').toBe(0);
    });
  }
}

test('UIFIX-19e Slow call screen: a press goes to the bar it is nearest to, an area never takes a press from the chart outside it or from the next lane', async ({ page }) => {
  await openSlowCallScreen(page);
  const bars = await barFacts(page);
  const stack = bars.filter((b) => b.lane === bars[0].lane).sort((a, b) => a.bar.t - b.bar.t);
  expect(stack.length, 'a stack of bars in one lane').toBeGreaterThan(1);
  const pressed = async () => page.evaluate(() => [...document.querySelectorAll('.bar-g')].map((g) => g.getAttribute('aria-pressed') === 'true'));
  const click = async (x: number, y: number) => { await page.mouse.click(x, y); };
  const cx = (b: typeof stack[number]) => (b.bar.l + b.bar.r) / 2;
  // the gap between two stacked bars: 1 px on each side of the middle of the gap goes to the bar on that side
  for (let k = 0; k + 1 < stack.length; k++) {
    const upper = stack[k], lower = stack[k + 1];
    const mid = (upper.bar.b + lower.bar.t) / 2;
    await click(cx(upper), mid - 1);
    expect((await pressed())[upper.i], 'just above the middle of the gap: the upper bar').toBe(true);
    expect((await pressed())[lower.i], 'just above the middle of the gap: not the lower bar').toBe(false);
    await click(cx(upper), mid + 1);
    expect((await pressed())[lower.i], 'just below the middle of the gap: the lower bar').toBe(true);
    expect((await pressed())[upper.i], 'just below the middle of the gap: not the upper bar').toBe(false);
  }
  // a press in the grown part below the lowest bar selects it; a press 2 px past its area belongs to the chart (the readout is pinned, the bar is not selected)
  const lowest = stack[stack.length - 1];
  const inside = Math.min(lowest.hit.b - 2, lowest.bar.b + 4);
  await click(cx(lowest), inside);
  expect((await pressed())[lowest.i], 'in the grown part below the bar').toBe(true);
  await click(cx(stack[0]), stack[0].bar.t + 2); // a different bar is selected, so a press past the area that selects nothing leaves it as it is
  expect((await pressed())[stack[0].i]).toBe(true);
  const past = lowest.hit.b + 2;
  expect(past, 'there is chart below the area').toBeLessThan(lowest.svg.b);
  await click(cx(lowest), past);
  expect((await pressed())[lowest.i], 'past the area the lowest bar is not selected (the chart takes the press)').toBe(false);
  expect((await pressed())[stack[0].i], 'past the area the earlier selection stays').toBe(true);
  await expect(page.locator('.scall-line.pinned')).toHaveCount(1);
  // the lane boundary: one pixel below the drawing of a lane is not a bar of that lane
  for (const b of bars) {
    const below = await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.closest('.bar-g')?.getAttribute('aria-label') ?? null, [(b.hit.l + b.hit.r) / 2, b.svg.b + 1] as [number, number]);
    expect(below, 'one pixel below the lane drawing of ' + b.label).toBeNull();
    const above = await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.closest('.bar-g')?.getAttribute('aria-label') ?? null, [(b.hit.l + b.hit.r) / 2, b.svg.t - 1] as [number, number]);
    expect(above, 'one pixel above the lane drawing of ' + b.label).toBeNull();
  }
});

for (const scheme of ['dark', 'light'] as const) {
  test('UIFIX-19f Slow call screen (' + scheme + '): the keyboard focus rings and the rest pictures of the crumb button, the minimap box and a lane bar are the old ones', async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await page.setViewportSize({ width: 1440, height: 900 });
    await openSlowCallScreen(page);
    await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; caret-color: transparent !important; }' });
    for (const [name, selector] of [['crumb', '[data-testid=scall-back]'], ['minimap box', '[data-testid=scall-map-box]'], ['lane bar', '.bar-g']] as const) {
      const target = page.locator(selector).first();
      await target.scrollIntoViewIfNeeded();
      const clip = await target.evaluate((e) => {
        const r = e.getBoundingClientRect();
        const fx = Math.max(0, Math.floor(r.left - 14)), fy = Math.max(0, Math.floor(r.top - 14));
        return { x: fx, y: fy, width: Math.ceil(r.right + 14 - fx), height: Math.ceil(r.bottom + 14 - fy) };
      });
      const take = async (): Promise<Buffer[]> => {
        await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
        await page.mouse.move(0, 0);
        const rest = await page.screenshot({ clip });
        await target.focus();
        await page.keyboard.press('Shift');
        const focused = await page.screenshot({ clip });
        await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
        return [rest, focused];
      };
      const now = await take();
      const style = await page.addStyleTag({ content: '.bar-hit { display: none !important; } .scall-crumb .link::after, .scall-map-box::after { content: none !important; } .scall-crumb .link { position: static !important; }' });
      const old = await take();
      await style.evaluate((el) => el.remove());
      for (const [i, what] of ['at rest', 'focused'].entries()) {
        const d = await pictureDifference(page, now[i], old[i]);
        expect(d.pixels, name + ' ' + what + ': pixels that differ from the old rules (largest step ' + d.levels + ')').toBe(0);
      }
      expect(now[1].equals(now[0]), name + ' focused: nothing is drawn, so the ring is missing').toBe(false);
    }
  });
}

// ---------------------------------------------------------------------------------------------------------------------------------------------
// BL-495 part: axis titles and units on the Requests-over-time chart of the home screen (UIFIX-30 to 32).
// The chart is a canvas, so these read it three ways: the accessible name (the axes in words), the pixels (a title is a long stretch of ink in
// a strip no tick label reaches) and the hover text. The chart box and the boxes around it are compared with the sizes measured on develop.
const HERO_SIZES = [{ width: 1440, height: 900 }, { width: 1280, height: 720 }, { width: 1100, height: 800 }];

type HeroFacts = { stage: number[]; chart: number[]; canvas: number[]; head: number[]; legend: number[]; leftRows: number; rightRows: number; bottomCols: number; outer: number; label: string };

/** Where the chart sits and what is painted in the strips that hold the axis titles. Strips are in canvas pixels (device scale 1). */
async function heroFacts(page: Page): Promise<HeroFacts> {
  await page.locator('.hero-chart canvas').first().waitFor();
  await page.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));
  await page.waitForTimeout(300);
  return page.evaluate(() => {
    const r = (e: Element | null) => { const b = e!.getBoundingClientRect(); return [b.left, b.top, b.width, b.height]; };
    const canvas = document.querySelector<HTMLCanvasElement>('.hero-chart canvas')!;
    const w = canvas.width, h = canvas.height;
    const data = canvas.getContext('2d')!.getImageData(0, 0, w, h).data;
    const ink = (x: number, y: number) => data[(y * w + x) * 4 + 3] > 8;
    // Rows with ink in a vertical strip (left title, right title) and columns with ink in the bottom strip, the middle half only (tick labels sit on the left and right).
    const rowsIn = (x0: number, x1: number) => { let n = 0; for (let y = 0; y < h; y++) for (let x = x0; x < x1; x++) if (ink(x, y)) { n++; break; } return n; };
    let bottom = 0;
    for (let x = Math.floor(w * .45); x < Math.floor(w * .55); x++) for (let y = h - 22; y < h; y++) if (ink(x, y)) { bottom++; break; }
    let outer = 0;
    for (let y = 0; y < h; y++) for (const x of [0, 1, w - 2, w - 1]) if (ink(x, y)) outer++;
    for (let x = 0; x < w; x++) for (const y of [0, 1, h - 2, h - 1]) if (ink(x, y)) outer++;
    return {
      stage: r(document.querySelector('.hero-stage')), chart: r(document.querySelector('.hero-chart')), canvas: [w, h], head: r(document.querySelector('.hero-head')), legend: r(document.querySelector('.hero-legend')),
      leftRows: rowsIn(4, 26), rightRows: rowsIn(w - 24, w - 4), bottomCols: bottom, outer, label: document.querySelector('.hero-chart')!.getAttribute('aria-label') ?? ''
    };
  });
}

for (const scheme of ['dark', 'light'] as const) for (const size of HERO_SIZES) {
  test('UIFIX-30a Requests over time at ' + size.width + 'x' + size.height + ' (' + scheme + '): axis titles are painted inside the chart box and the box does not move', async ({ page }) => {
    await page.setViewportSize(size);
    await page.emulateMedia({ colorScheme: scheme });
    const f = await heroFacts(page);
    // The box: measured on develop at these sizes, 280 px tall, as wide as the page content, the canvas as large as the box. Nothing around it moved.
    expect(f.stage[3], 'chart box height (280 px on develop)').toBe(280);
    expect(f.chart).toEqual(f.stage);
    expect(f.canvas, 'canvas as large as its box').toEqual([Math.round(f.stage[2]), 280]);
    expect(f.stage[0], 'the box starts at the content edge').toBe(24);
    expect(f.stage[2], 'the box spans the content width (page width minus two 24 px gutters)').toBe(size.width - 48);
    expect(f.legend[1] + f.legend[3], 'the key sits above the box').toBeLessThanOrEqual(f.stage[1]);
    expect(f.head[1] + f.head[3], 'the heading sits above the box').toBeLessThanOrEqual(f.stage[1]);
    // The three axis titles are long stretches of ink where no tick label reaches (a 22-letter title is well over 90 rows tall; three tick labels are about 30).
    expect(f.leftRows, 'left title "Requests per interval"').toBeGreaterThan(90);
    expect(f.rightRows, 'right title "Response time (ms)"').toBeGreaterThan(70);
    expect(f.bottomCols, 'bottom title "Time (UTC)" in the middle of the bottom strip').toBeGreaterThan(20);
    expect(f.outer, 'nothing is painted on the outermost 2 px of the box (titles fit inside it)').toBe(0);
    expect(f.label).toContain('Left axis: requests per interval, whole requests.');
    expect(f.label).toContain('Right axis: response time in milliseconds');
    expect(f.label).toContain('Bottom axis: time, in UTC.');
    // Same page, no sideways scroll.
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(over).toBeLessThanOrEqual(0);
  });
}

// Contrast of the colours the chart draws its text in (it reads these two tokens, see palette() in lib/charts.ts) against the page behind it.
for (const scheme of ['dark', 'light'] as const) {
  test('UIFIX-30b Requests over time (' + scheme + '): axis titles and tick labels have 4.5:1 contrast on the page', async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await page.locator('.hero-chart canvas').first().waitFor();
    const facts = await page.evaluate(() => {
      const parse = (c: string) => { const m = /rgba?\(([^)]+)\)/.exec(c); if (m) { const p = m[1].split(/[ ,/]+/).map(Number); return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 }; } const hex = c.trim().replace('#', ''); return { r: parseInt(hex.slice(0, 2), 16), g: parseInt(hex.slice(2, 4), 16), b: parseInt(hex.slice(4, 6), 16), a: 1 }; };
      const lin = (v: number) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
      const lum = (c: { r: number; g: number; b: number }) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
      let bg = { r: 255, g: 255, b: 255, a: 1 };
      for (let e: Element | null = document.querySelector('.hero-stage'); e; e = e.parentElement) { const c = parse(getComputedStyle(e).backgroundColor); if (c.a > 0.99) { bg = c; break; } }
      const root = getComputedStyle(document.documentElement);
      const ratio = (token: string) => { const fg = parse(root.getPropertyValue(token)); const a = lum(fg), b = lum(bg); return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05); };
      return { title: ratio('--text-2'), tick: ratio('--accent') };
    });
    expect(facts.title, 'axis title colour (--text-2)').toBeGreaterThanOrEqual(4.5);
    expect(facts.tick, 'right axis tick colour (--accent)').toBeGreaterThanOrEqual(4.5);
  });
}

type Window_ = { at: string; count: number; timing: { count: number; meanMs: number | null; minMs: number | null; maxMs: number | null; p50: null; p95: { valueMs: number; lowerMs: number; upperMs: number } | null; histogram: [] } };

/** Run the analysis again with the load windows replaced by `make(index, slotStart)`: the page draws exactly what the server could send. */
async function rerunWithWindows(page: Page, make: (i: number, at: number) => { count: number; p95: number | null; max: number | null }) {
  await page.route('**/api/analyze', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    const q = body.query;
    const width = q.bucketSeconds * 1000, start = Math.floor(Date.parse(q.start) / width) * width;
    const out: Window_[] = [];
    for (let at = start, i = 0; at < Date.parse(q.end); at += width, i++) {
      const w = make(i, at);
      out.push({ at: new Date(at).toISOString(), count: w.count, timing: { count: w.p95 == null ? 0 : w.count, meanMs: null, minMs: null, maxMs: w.max, p50: null, p95: w.p95 == null ? null : { valueMs: w.p95, lowerMs: w.p95, upperMs: w.p95 }, histogram: [] } });
    }
    body.loadWindows = out;
    await route.fulfill({ response, json: body });
  });
  const done = page.waitForResponse((r) => r.url().includes('/api/analyze'));
  await page.getByRole('button', { name: 'Run analysis' }).click();
  await done;
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await page.waitForTimeout(400);
}

/** Sweep the pointer across the plot until the tooltip text matches, and return it; fails with the last text seen. The tooltip is drawn on the canvas (BL-323): support/chart-tip.ts reads it from the chart. */
async function heroTip(page: Page, wanted: RegExp): Promise<string> {
  return sweepTip(page, '.hero-chart', wanted);
}

test('UIFIX-31a Requests over time, a slow hour: P95 and the slowest request in seconds with one decimal each, interval start and end named', async ({ page }) => {
  await rerunWithWindows(page, (i) => ({ count: 1200 + i, p95: i === 3 ? 65_700 : 900 + i * 10, max: i === 3 ? 68_000 : 1500 }));
  const f = await heroFacts(page);
  expect(f.label).toContain('Right axis: response time in seconds');
  expect(f.rightRows, 'right title painted').toBeGreaterThan(70);
  expect(f.outer).toBe(0);
  const tip = await heroTip(page, /Slowest request: 68\.0 s/);
  expect(tip).toMatch(/^20 Oct \d\d:\d\d:\d\d to \d\d:\d\d:\d\d UTC Requests: 1,203 P95: ≈ 65\.7 s Slowest request: 68\.0 s/);
  expect(tip, 'no NaN or undefined').not.toMatch(/NaN|undefined|Infinity/);
});

// Millions of requests make the left labels wide ("3,000,000"): the left margin grows from 46 to about 96 px, and the title must still fit inside the same box.
for (const size of [{ width: 1100, height: 800 }, { width: 1280, height: 720 }, { width: 1440, height: 900 }, { width: 1920, height: 1080 }, { width: 320, height: 800 }]) {
  test('UIFIX-31b Requests over time, a busy hour at ' + size.width + ' px: counts in the millions keep whole numbers with separators and room for the title', async ({ page }) => {
    await page.setViewportSize(size);
    await rerunWithWindows(page, (i) => ({ count: 2_000_000 + i * 150_000, p95: 120 + i, max: 400 }));
    const f = await heroFacts(page);
    expect(f.stage[3], 'the box keeps its height').toBe(size.width < 500 ? 220 : 280);
    expect(f.leftRows, 'left title painted beside millions').toBeGreaterThan(90);
    expect(f.rightRows, 'right title painted').toBeGreaterThan(70);
    expect(f.outer, 'title and wide labels stay inside the box').toBe(0);
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(over, 'sideways overflow in px').toBeLessThanOrEqual(0);
    const tip = await heroTip(page, /P95: ≈ 12\d ms/);
    expect(tip).toMatch(/Requests: 2,\d{3},\d{3} P95: ≈ 12\d ms Slowest request: 400 ms/);
  });
}

test('UIFIX-31e Requests over time, a fast P95 with one 68 s outlier: the tooltip and the note under the chart write the slowest request alike, never "68,000 ms"', async ({ page }) => {
  await rerunWithWindows(page, (i) => ({ count: 30, p95: 43, max: i === 3 ? 68_000 : 80 }));
  const tip = await heroTip(page, /Slowest request: 68\.0 s/);
  expect(tip).toContain('P95: ≈ 43 ms');
  expect(tip).not.toContain('68,000');
  const note = page.locator('.hero-note');
  await expect(note).toContainText('up to 68.0 s');
  await expect(note).not.toContainText('68,000');
});

test('UIFIX-31c Requests over time, a sub-millisecond hour and an hour with no timing: no NaN, the axis starts at 0 and the title is there', async ({ page }) => {
  await rerunWithWindows(page, (i) => i % 2 ? { count: 4, p95: null, max: null } : { count: 7, p95: 0.4, max: 0.9 });
  const f = await heroFacts(page);
  expect(f.label).toContain('Right axis: response time in milliseconds');
  expect(f.leftRows).toBeGreaterThan(90);
  expect(f.rightRows).toBeGreaterThan(70);
  const noTiming = await heroTip(page, /P95: no timed requests/);
  expect(noTiming).toContain('P95: no timed requests');
  const tiny = await heroTip(page, /P95: ≈ <1 ms/);
  expect(tiny).toContain('P95: ≈ <1 ms');
  expect(tiny).toContain('Slowest request: <1 ms');
  expect(tiny + noTiming).not.toMatch(/NaN|undefined|Infinity/);
});

test('UIFIX-31d Requests over time, a quiet hour (every interval zero): the empty message shows, not a chart of NaN', async ({ page }) => {
  await rerunWithWindows(page, () => ({ count: 0, p95: null, max: null }));
  await expect(page.locator('.hero-empty')).toBeVisible();
  await expect(page.locator('.hero-empty')).toContainText('No requests in this period');
  await expect(page.locator('.hero-chart')).toHaveAttribute('aria-hidden', 'true');
});

for (const width of [320, 360, 390]) {
  test('UIFIX-32 Requests over time on a ' + width + ' px phone: titles painted, box 220 px, no sideways scroll', async ({ page }) => {
    await page.setViewportSize({ width, height: 800 });
    const f = await heroFacts(page);
    expect(f.stage[3], 'chart box height (220 px on develop)').toBe(220);
    expect(f.stage[2], 'the box spans the content width (two 14 px gutters)').toBe(width - 28);
    expect(f.leftRows).toBeGreaterThan(90);
    expect(f.rightRows).toBeGreaterThan(70);
    expect(f.outer).toBe(0);
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(over, 'sideways overflow in px').toBeLessThanOrEqual(0);
  });
}

// UIFIX-40 (BL-495 part): a clickable table row is a real button. On develop the Slow call lists were <tr tabindex="0"> with a click handler: no role,
// no name, no pressed state. Now the API cell (Nearby calls) or the What cell (All sources) holds a <button type="button"> that looks like the text it
// replaced; the row keeps its mouse press. The box of the button, the rows and the page are the ones the plain text made (40a, 40b measure it by
// putting the plain text back), the press area grows with an invisible ::after, and the focus ring is the global 2 px ring.
const ROW_BUTTONS = { calls: '[data-testid=scall-nearby-open]', all: '[data-testid=scall-all-open]' } as const;
const ROW_TABLES = { calls: '[data-testid=scall-nearby]', all: '[data-testid=scall-all]' } as const;
type RowList = keyof typeof ROW_BUTTONS;

async function openRowList(page: Page, list: RowList): Promise<void> {
  await openSlowCallScreen(page);
  if (list === 'all') await page.getByTestId('scall-tab-all').click();
  await expect(page.locator(ROW_TABLES[list])).toBeVisible();
  await expect(page.locator(ROW_BUTTONS[list]).first()).toBeVisible();
}

/** Every row and cell box of the list, the table and the page, then the same with each row button replaced by the plain text it holds (the markup develop had). */
async function rowListBoxesWithAndWithout(page: Page, list: RowList) {
  return page.evaluate(({ table, button }) => {
    const rects = () => {
      const t = document.querySelector<HTMLElement>(table)!;
      return [t, ...t.querySelectorAll('tr, td, th')].map((e) => { const r = e.getBoundingClientRect(); return [r.x, r.y, r.width, r.height].map((v) => Math.round(v * 100) / 100); })
        .concat([[document.documentElement.scrollWidth, document.documentElement.scrollHeight, 0, 0]]);
    };
    const withButtons = rects();
    const buttons = [...document.querySelectorAll<HTMLElement>(button)];
    buttons.forEach((b) => b.replaceWith(...b.childNodes));
    const plain = rects();
    return { n: buttons.length, withButtons, plain };
  }, { table: ROW_TABLES[list], button: ROW_BUTTONS[list] });
}

for (const list of ['calls', 'all'] as const) {
  for (const size of SCALL_SIZES) {
    test('UIFIX-40' + (list === 'calls' ? 'a' : 'b') + ' Slow call screen at ' + size.width + ' px, ' + (list === 'calls' ? 'nearby calls' : 'all sources') + ': every row has one real button, a 24 px press area, and nothing moves', async ({ page }) => {
      await page.setViewportSize(size);
      await openRowList(page, list);
      const rows = await page.locator(ROW_TABLES[list] + ' tbody tr').count();
      expect(rows, 'rows of the list').toBeGreaterThan(2);
      // a real button, with a name, one per row, and nothing else of the row takes focus
      expect(await page.locator(ROW_BUTTONS[list]).count(), 'one row button per row').toBe(rows);
      expect(await page.locator(ROW_TABLES[list] + ' tbody tr[tabindex]').count(), 'a row is not a focus stop of its own').toBe(0);
      expect(await page.locator(ROW_TABLES[list] + ' tbody tr[aria-selected]').count(), 'a row has no aria-selected (it is not a grid row)').toBe(0);
      const tags = await page.locator(ROW_BUTTONS[list]).evaluateAll((els) => els.map((e) => [e.tagName, e.getAttribute('type')].join(' ')));
      expect([...new Set(tags)]).toEqual(['BUTTON button']);
      const named = await page.getByRole('button', { name: list === 'calls' ? /: open this call, started \d/ : /\S/ }).evaluateAll((els, sel) => els.filter((e) => e.matches(sel)).length, ROW_BUTTONS[list]);
      expect(named, 'row buttons reachable by role and name').toBe(rows);
      if (list === 'all') {
        expect(await page.locator(ROW_BUTTONS[list] + '[aria-pressed]').count(), 'a toggle says whether it is pressed').toBe(rows);
        const names = await page.locator(ROW_BUTTONS[list]).evaluateAll((els) => els.map((e) => e.getAttribute('aria-label') ?? ''));
        expect(names.filter((n) => !n), 'every All sources button has a name').toEqual([]);
        expect(new Set(names).size, 'the demo rows get different names (what, source, time to the millisecond, duration): ' + names.join(' | ')).toBe(names.length);
      }
      // the press area is 24 x 24 px or more and no other control sits inside it
      await expectScallHitArea(page, ROW_BUTTONS[list], 'Row button', 24);
      // nothing moved: the same boxes as with the plain text the button replaced
      // (Not for All sources on a phone: that table became a card list in this change (BL-521), and in a card the plain text next to a <strong> is two grid items, so the plain-text version is not a baseline there.)
      if (list === 'calls' || size.width > 760) {
        const f = await rowListBoxesWithAndWithout(page, list);
        expect(f.n).toBe(rows);
        expect(f.withButtons.length).toBe(f.plain.length);
        expect(f.withButtons.filter((r, i) => r.some((v, k) => Math.abs(v - f.plain[i][k]) > 0.01)), 'boxes that differ from the plain-text version').toEqual([]);
      }
      // (BL-521: on develop the All sources table ran 39 px past a 390 px window; it is a card list on a phone now.)
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), 'sideways overflow').toBeLessThanOrEqual(0);
    });
  }
}

/** Counts the real click events that reach the page (one per Enter or Space on a button), so a double activation cannot hide. */
async function countClicks(page: Page, selector: string): Promise<void> {
  await page.evaluate((sel) => {
    (window as unknown as { __clicks: number }).__clicks = 0;
    const w = window as unknown as { __clicks: number; __escaped: number };
    w.__escaped = 0;
    document.addEventListener('click', (e) => { if ((e.target as Element | null)?.closest(sel)) w.__clicks++; }, true);
    // The app reacts to a click in its own handlers, which sit on the page root. A button that keeps its press to itself (stopPropagation) never lets it
    // go on to the row's handler or on to the window; with that call removed the click arrives here and the row selects a second time.
    window.addEventListener('click', (e) => { if ((e.target as Element | null)?.closest(sel)) w.__escaped++; });
  }, selector);
}
const clicks = (page: Page) => page.evaluate(() => (window as unknown as { __clicks: number }).__clicks);
/** Presses of a row button that went on past the app's handlers (to the row, or on to the window): must be 0, one press selects once. */
const escaped = (page: Page) => page.evaluate(() => (window as unknown as { __escaped: number }).__escaped);

/** Tab from the list's tab until a row button has focus (at most 12 presses). */
async function tabToRowButton(page: Page, list: RowList): Promise<void> {
  await page.getByTestId(list === 'calls' ? 'scall-tab-calls' : 'scall-tab-all').focus();
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press('Tab');
    if (await page.evaluate((sel) => document.activeElement?.matches(sel) ?? false, ROW_BUTTONS[list])) return;
  }
  throw new Error('Tab did not reach a row button within 12 presses');
}

/** The ring of the focused element once the colour and width transition has ended (the first frames still show the start of it). */
async function focusRing(page: Page) {
  const read = () => page.evaluate(() => { const s = getComputedStyle(document.activeElement!); return { style: s.outlineStyle, width: parseFloat(s.outlineWidth), color: s.outlineColor, offset: s.outlineOffset }; });
  let last = JSON.stringify(await read());
  for (let i = 0; i < 20; i++) {
    await page.waitForTimeout(50);
    const now = JSON.stringify(await read());
    if (now === last) break;
    last = now;
  }
  return JSON.parse(last) as { style: string; width: number; color: string; offset: string };
}

test('UIFIX-40c Slow call screen, nearby calls: Tab reaches the row button, it shows the focus ring, Enter and Space each open a call once', async ({ page }) => {
  await openRowList(page, 'calls');
  await countClicks(page, ROW_BUTTONS.calls);
  await tabToRowButton(page, 'calls');
  const ring = await focusRing(page);
  expect(ring.style).toBe('solid');
  expect(ring.width).toBe(2);
  expect(ring.offset, 'the ring sits 2 px outside the button, like every other button').toBe('2px');
  expect(ring.color, 'the ring is painted').not.toMatch(/rgba\(0, 0, 0, 0\)|transparent/);
  // a row is one stop: Tab goes from one row button to the next. The first row may be the open call, so go down to a row that is not.
  const label = () => page.evaluate(() => document.activeElement?.getAttribute('aria-label'));
  const isThis = () => page.evaluate(() => document.activeElement?.getAttribute('aria-current') === 'true');
  let guard = 0;
  while ((await isThis()) && guard++ < 6) await page.keyboard.press('Tab');
  const first = (await label())!;
  expect(first).toMatch(/: open this call, started \d/);
  const current = page.locator(ROW_BUTTONS.calls + '[aria-current=true]');
  await page.keyboard.press('Enter');
  // Enter opened that call: the screen re-centres on it, so its row is now the one marked "this call", and the title takes focus
  await expect(current, 'the opened call is marked this call').toHaveAttribute('aria-label', first);
  await expect(page.locator('#scall-title'), 'focus goes to the title of the opened call').toBeFocused();
  expect(await clicks(page), 'one click for one Enter').toBe(1);
  // Space on a row button of another call
  await tabToRowButton(page, 'calls');
  guard = 0;
  while (((await isThis()) || (await label()) === first) && guard++ < 8) await page.keyboard.press('Tab');
  const second = (await label())!;
  expect(second).not.toBe(first);
  await page.keyboard.press('Space');
  await expect(current, 'Space opened the second call').toHaveAttribute('aria-label', second);
  expect(await clicks(page), 'one more click for one Space').toBe(2);
  expect(await escaped(page), 'a button press stops at the button: the row does not select a second time').toBe(0);
});

test('UIFIX-40d Slow call screen, all sources: Tab, Enter and Space press a row button once; the pressed state moves; a mouse press on the row still selects it', async ({ page }) => {
  await openRowList(page, 'all');
  await countClicks(page, ROW_BUTTONS.all);
  await tabToRowButton(page, 'all');
  expect((await focusRing(page)).style).toBe('solid');
  const pressed = page.locator(ROW_BUTTONS.all + '[aria-pressed=true]');
  expect(await pressed.count(), 'nothing selected yet').toBe(0);
  const buttons = page.locator(ROW_BUTTONS.all);
  await page.keyboard.press('Enter');
  expect(await clicks(page), 'one click for one Enter').toBe(1);
  await expect(buttons.nth(0)).toHaveAttribute('aria-pressed', 'true');
  expect(await pressed.count()).toBe(1);
  await page.keyboard.press('Tab');
  await page.keyboard.press('Space');
  expect(await clicks(page), 'one click for one Space').toBe(2);
  await expect(buttons.nth(1)).toHaveAttribute('aria-pressed', 'true');
  expect(await pressed.count(), 'only the last pressed row is selected').toBe(1);
  await expect(page.locator(ROW_TABLES.all + ' tbody tr.selected')).toHaveCount(1);
  // the mouse keeps the whole row: a press on the time cell of the third row selects it
  const cell = page.locator(ROW_TABLES.all + ' tbody tr').nth(2).locator('td.time');
  await cell.click();
  await expect(buttons.nth(2)).toHaveAttribute('aria-pressed', 'true');
  expect(await pressed.count()).toBe(1);
  // a press on the button itself with the mouse selects once as well (the click event count counts button presses only; the press on the time cell above is no button press)
  const clicksBefore = await clicks(page);
  await buttons.nth(1).click();
  await expect(buttons.nth(1)).toHaveAttribute('aria-pressed', 'true');
  expect(await pressed.count()).toBe(1);
  expect(await clicks(page) - clicksBefore, 'one click for one mouse press on the button').toBe(1);
  expect(await escaped(page), 'a press on a button (Enter, Space or mouse) stops at the button: the row does not select a second time').toBe(0);
});

test('UIFIX-40e Where is it?: "Show only" of the Request volume table is a real button; Tab and Enter filter the histogram once', async ({ page }) => {
  await page.getByRole('button', { name: 'Where is it?', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Where is it?' })).toBeVisible();
  await page.locator('summary', { hasText: 'Request volume' }).click();
  const only = page.getByRole('button', { name: /^Show only .* in histogram$/ });
  await expect(only.first()).toBeVisible();
  const n = await only.count();
  expect(n, 'one per row').toBeGreaterThan(1);
  expect(await page.locator('.volume-table td button.quiet-button').evaluateAll((els) => els.map((e) => e.tagName + ' ' + e.getAttribute('type')).filter((t) => t !== 'BUTTON button')), 'every Show only is a <button type=button>').toEqual([]);
  // Tab from the disclosure to the first Show only: the controls of the table come first (window, search), the rows after
  await page.locator('summary', { hasText: 'Request volume' }).focus();
  let reached = false;
  for (let i = 0; i < 20 && !reached; i++) {
    await page.keyboard.press('Tab');
    reached = await page.evaluate(() => document.activeElement?.matches('.volume-table td button.quiet-button') ?? false);
  }
  expect(reached, 'Tab reaches a Show only button').toBe(true);
  expect((await focusRing(page)).style).toBe('solid');
  const label = await page.evaluate(() => document.activeElement?.getAttribute('aria-label'));
  const api = label!.replace(/^Show only /, '').replace(/ in histogram$/, '');
  await page.keyboard.press('Enter');
  // the table now lists the one API (one row), once: the filter ran and did not toggle back
  await expect(page.locator('.volume-table tbody tr')).toHaveCount(1);
  await expect(page.locator('.volume-table tbody tr th')).toHaveText(api);
});

// UIFIX-40f: on a phone the lists are card lists whose lines carry the column name as CSS text. That text must stay out of the name of the cell (the
// column header, kept for screen readers, already names the column), or a screen reader says "Time Time 08:00:00".
for (const list of ['calls', 'all'] as const) {
  test('UIFIX-40f Slow call screen on a 390 px phone, ' + (list === 'calls' ? 'nearby calls' : 'all sources') + ': a cell is named by its content, not by the column name drawn before it', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openRowList(page, list);
    const heads = await page.locator(ROW_TABLES[list] + ' thead th').evaluateAll((els) => els.map((e) => (e.textContent ?? '').trim()));
    expect(heads.length).toBeGreaterThan(2);
    // the drawn label is still there (the cards stay labelled for the eye)...
    const drawn = await page.locator(ROW_TABLES[list] + ' tbody tr').first().locator('td').first().evaluate((e) => getComputedStyle(e, '::before').content);
    expect(drawn, 'the column name is drawn before the value').toContain(heads[0]);
    // ...but the accessibility tree names the cell by its value only
    const snapshot = await page.locator(ROW_TABLES[list]).ariaSnapshot();
    const cells = [...snapshot.matchAll(/- cell "([^"]*)"/g)].map((m) => m[1]);
    expect(cells.length, 'cells in the accessibility tree').toBeGreaterThan(4);
    for (const name of cells) for (const head of heads) expect(name.startsWith(head + ' '), 'cell "' + name + '" repeats the column name "' + head + '"').toBe(false);
  });
}

// UIFIX-50 (BL-495 part): the buttons at the end of a table row. On develop "Nearby activity" (143 px) and "Inspect" (81 px) of the All operations request
// list were two sizes stacked in one cell, both named only by their words (the first said nothing about WHICH request, the second the record id), and
// "Show only" in the Request volume table was faint text with a transparent edge. Now they are quiet buttons with a visible edge, one size in a column,
// named "Nearby activity for Payroll.Run at 08:59:52.123, Demo West / demo.audit, record <id>" / "Inspect record for ..." / "Show only Payroll.Run in histogram".
const OP_SIZES = [{ width: 1440, height: 900 }, { width: 1280, height: 800 }, { width: 1100, height: 800 }, { width: 1024, height: 768 }, { width: 390, height: 844 }];
const OP_BUTTONS = '.request-table .row-actions button';

async function openRequestList(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'All operations', exact: true }).click();
  await page.getByRole('tab', { name: 'Slowest' }).click();
  await expect(page.locator(OP_BUTTONS).first()).toBeVisible();
}

for (const size of OP_SIZES) {
  test('UIFIX-50a All operations at ' + size.width + ' px: Nearby activity and Inspect are real buttons of one size on one line each, named by their request', async ({ page }) => {
    await page.setViewportSize(size);
    await openRequestList(page);
    const rows = await page.locator('.request-table tbody tr').count();
    expect(rows, 'rows of the request list').toBeGreaterThan(2);
    expect(await page.locator(OP_BUTTONS).count(), 'two buttons per row').toBe(rows * 2);
    const facts = await page.locator('.request-table tbody tr').evaluateAll((trs) => trs.map((tr) => {
      const [near, inspect] = [...tr.querySelectorAll<HTMLElement>('.row-actions button')];
      const read = (b: HTMLElement) => {
        const r = b.getBoundingClientRect(), range = document.createRange();
        range.selectNodeContents(b);
        const cs = getComputedStyle(b);
        return { tag: b.tagName + ' ' + b.getAttribute('type'), w: Math.round(r.width * 100) / 100, h: Math.round(r.height * 100) / 100, lines: new Set([...range.getClientRects()].map((q) => Math.round(q.top))).size, text: (b.textContent ?? '').trim(), name: b.getAttribute('aria-label') ?? '', edge: cs.borderTopStyle + ' ' + cs.borderTopWidth + ' ' + cs.borderTopColor, nowrap: cs.whiteSpace };
      };
      return { near: read(near), inspect: read(inspect), id: (tr.querySelector('.record-id')?.textContent ?? '').trim() };
    }));
    for (const f of facts) {
      expect([f.near.tag, f.inspect.tag], 'both are <button type=button>').toEqual(['BUTTON button', 'BUTTON button']);
      expect(f.near.w, 'both buttons are as wide as each other').toBeCloseTo(f.inspect.w, 1);
      expect(f.near.h, 'both buttons are as tall as each other').toBeCloseTo(f.inspect.h, 1);
      expect(f.near.lines, 'Nearby activity stays on one line').toBe(1);
      expect(f.inspect.lines, 'Inspect stays on one line').toBe(1);
      expect(f.near.nowrap).toBe('nowrap');
      expect(f.near.edge, 'a visible edge, not faint text').not.toMatch(/rgba\(0, 0, 0, 0\)|none|0px/);
      // the name starts with the visible words (label in name) and says which request
      expect(f.near.name).toMatch(/^Nearby activity for [\w.]+ at \d\d:\d\d:\d\d\.\d{3}, Demo (West|East) \/ \S+, record \S+$/);
      expect(f.inspect.name).toMatch(/^Inspect record for [\w.]+ at \d\d:\d\d:\d\d\.\d{3}, Demo (West|East) \/ \S+, record \S+$/);
      expect(f.id, 'the row shows its record id').not.toBe('');
      expect(f.near.name.endsWith(', record ' + f.id), 'the name ends with the record id of its row (the only part that is unique on live data)').toBe(true);
      expect(f.inspect.name.endsWith(', record ' + f.id)).toBe(true);
      expect(f.near.name.startsWith(f.near.text)).toBe(true);
      expect(f.inspect.name.startsWith(f.inspect.text)).toBe(true);
    }
    // the same sizes in every row of the column
    expect(new Set(facts.map((f) => f.near.w + 'x' + f.near.h + '/' + f.inspect.w + 'x' + f.inspect.h)).size, 'every row has the same two boxes').toBe(1);
    const names = facts.flatMap((f) => [f.near.name, f.inspect.name]);
    expect(new Set(names).size, 'every button has its own name: ' + names.join(' | ')).toBe(names.length);
    // the accessibility tree (what a screen reader gets)
    const snapshot = await page.locator('.request-table tbody tr').first().ariaSnapshot();
    expect(snapshot).toMatch(/- button "Nearby activity for Payroll\.Run at \d\d:\d\d:\d\d\.\d{3}, Demo (West|East) \/ \S+, record \S+"/);
    expect(snapshot).toMatch(/- button "Inspect record for Payroll\.Run at \d\d:\d\d:\d\d\.\d{3}, Demo (West|East) \/ \S+, record \S+"/);
    await expectScallHitArea(page, OP_BUTTONS, 'Row action', 24, 'tr');
    if (size.width >= 1024) {
      const [wide, box] = await page.locator('.request-scroll').first().evaluate((e) => [e.scrollWidth, e.clientWidth]);
      expect(wide, 'the request list fits its box').toBeLessThanOrEqual(box);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), 'sideways overflow of the page').toBeLessThanOrEqual(0);
  });
}

test('UIFIX-50b All operations: Tab reaches Nearby activity then Inspect of the same row; Enter and Space on Nearby activity act once each; the focus ring shows', async ({ page }) => {
  await openRequestList(page);
  const first = page.locator(OP_BUTTONS).nth(0);
  await countClicks(page, OP_BUTTONS);
  await first.focus();
  await page.keyboard.press('Shift+Tab'); // arrive by keyboard, so the ring is the keyboard ring
  await page.keyboard.press('Tab');
  expect(await page.evaluate(() => document.activeElement?.getAttribute('aria-label') ?? '')).toMatch(/^Nearby activity for /);
  const ring = await focusRing(page);
  expect(ring.style).toBe('solid');
  expect(ring.width).toBe(2);
  expect(ring.color, 'the ring is painted').not.toMatch(/rgba\(0, 0, 0, 0\)|transparent/);
  const nameOfFocus = () => page.evaluate(() => document.activeElement?.getAttribute('aria-label') ?? '');
  const firstName = await nameOfFocus();
  expect(firstName).toMatch(/^Nearby activity for /);
  await page.keyboard.press('Enter');
  expect(await clicks(page), 'one click for one Enter').toBe(1);
  await expect(page.locator('#nearby-activity'), 'Nearby activity opened').toBeAttached();
  await page.keyboard.press('Space');
  expect(await clicks(page), 'one more click for one Space').toBe(2);
  expect(await nameOfFocus(), 'the press kept the focus on the button').toBe(firstName);
  await page.keyboard.press('Tab');
  expect(await nameOfFocus(), 'Tab goes on to the Inspect button of the same row').toBe(firstName.replace('Nearby activity for', 'Inspect record for'));
});

for (const key of ['Enter', 'Space'] as const) {
  test('UIFIX-50c All operations: ' + key + ' on Inspect opens the record once', async ({ page }) => {
    await openRequestList(page);
    await countClicks(page, OP_BUTTONS);
    const inspect = page.locator(OP_BUTTONS).nth(1);
    await inspect.focus();
    const name = (await inspect.getAttribute('aria-label'))!;
    expect(name).toMatch(/^Inspect record for /);
    await page.keyboard.press(key);
    expect(await clicks(page), 'one click for one press').toBe(1);
    await expect(page.getByRole('heading', { name: /^Record demo-/ })).toHaveCount(1);
    await page.keyboard.press('Escape');
    await expect(page.getByRole('heading', { name: /^Record demo-/ })).toHaveCount(0);
    await expect(page.locator(OP_BUTTONS).nth(1), 'the button gets the focus back').toBeFocused();
  });
}

for (const size of OP_SIZES) {
  test('UIFIX-50d Where is it? at ' + size.width + ' px: "Show only" is a quiet button with a visible edge, one size in the column, a 24 px press area, and the table fits its box', async ({ page }) => {
    await page.setViewportSize(size);
    await page.getByRole('button', { name: 'Where is it?', exact: true }).first().click();
    await page.locator('summary', { hasText: 'Request volume' }).click();
    const sel = '.request-volume .volume-table td button[aria-label^="Show only"]';
    await expect(page.locator(sel).first()).toBeVisible();
    const facts = await page.locator(sel).evaluateAll((els) => els.map((e) => {
      const r = e.getBoundingClientRect(), cs = getComputedStyle(e);
      return { w: Math.round(r.width * 100) / 100, h: Math.round(r.height * 100) / 100, edge: cs.borderTopStyle + ' ' + cs.borderTopWidth + ' ' + cs.borderTopColor, name: e.getAttribute('aria-label') ?? '', tag: e.tagName + ' ' + e.getAttribute('type'), nowrap: cs.whiteSpace };
    }));
    expect(facts.length, 'one per row').toBeGreaterThan(1);
    expect(new Set(facts.map((f) => f.w + 'x' + f.h)).size, 'one size in the column').toBe(1);
    for (const f of facts) {
      expect(f.tag).toBe('BUTTON button');
      expect(f.edge, 'a visible edge, not faint text').not.toMatch(/rgba\(0, 0, 0, 0\)|none|0px/);
      expect(f.nowrap).toBe('nowrap');
      expect(f.name).toMatch(/^Show only [\w.]+ in histogram$/);
    }
    const snapshot = await page.locator('.request-volume .volume-table tbody tr').first().ariaSnapshot();
    expect(snapshot).toMatch(/- button "Show only Payroll\.Run in histogram"/);
    await expectScallHitArea(page, sel, 'Show only', 24, 'tr');
    const [wide, box] = await page.locator('.request-volume .volume-table-wrap').first().evaluate((e) => [e.scrollWidth, e.clientWidth]);
    expect(wide, 'the table fits its box').toBeLessThanOrEqual(box);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), 'sideways overflow of the page').toBeLessThanOrEqual(0);
  });
}

// UIFIX-51 (BL-495 part n): the same fix as UIFIX-50 for the Slowest tab of All operations (TrafficSlowness). On develop "Inspect window" (10 of them in the table of
// exact time windows) and "Nearby activity" (one per slowest request) were faint quiet buttons (a transparent edge) named only by their words, so a screen reader
// user heard the same name ten or fifty times. Now they are quiet buttons with a visible edge, one size in a column, named "Inspect window for 20 Oct 08:00:00 to
// 08:05:00 UTC" and "Nearby activity for Payroll.Run at 08:59:52.123, Demo West / demo.audit, record <id>".
const WIN_BUTTONS = '.traffic-slowness .traffic-windows tbody td button';
const SLOW_BUTTONS = '.traffic-slowness .slow-result tbody td button';
const WIN_NAME = /^Inspect window for \d{1,2} \w{3} \d\d:\d\d:\d\d to (\d{1,2} \w{3} )?\d\d:\d\d:\d\d UTC( \(partial\))?$/;
const NEAR_NAME = /^Nearby activity for [\w.]+ at \d\d:\d\d:\d\d\.\d{3}, Demo (West|East) \/ \S+, record \S+$/;

async function openSlowestLists(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'All operations', exact: true }).click();
  await page.getByRole('tab', { name: 'Slowest' }).click();
  await page.getByRole('button', { name: 'Load 50 slowest requests' }).click();
  if (!(await page.locator('.traffic-windows').evaluate((e) => (e as HTMLDetailsElement).open))) await page.locator('.traffic-slowness summary', { hasText: 'Exact time windows' }).click();
  await expect(page.locator(SLOW_BUTTONS).first()).toBeVisible();
  await expect(page.locator(WIN_BUTTONS).first()).toBeVisible();
}

const buttonFacts = (page: Page, selector: string) => page.locator(selector).evaluateAll((els) => els.map((e) => {
  const r = e.getBoundingClientRect(), range = document.createRange();
  range.selectNodeContents(e);
  const cs = getComputedStyle(e);
  return { tag: e.tagName + ' ' + e.getAttribute('type'), w: Math.round(r.width * 100) / 100, h: Math.round(r.height * 100) / 100, lines: new Set([...range.getClientRects()].map((q) => Math.round(q.top))).size, text: (e.textContent ?? '').trim(), name: e.getAttribute('aria-label') ?? '', edge: cs.borderTopStyle + ' ' + cs.borderTopWidth + ' ' + cs.borderTopColor, nowrap: cs.whiteSpace, api: ((e.closest('tr')?.querySelector('th')?.firstChild?.textContent) ?? '').trim(), clock: ((e.closest('tr')?.querySelector('td')?.textContent ?? '').match(/\d\d:\d\d:\d\d/) ?? [''])[0] };
}));

for (const size of OP_SIZES) {
  test('UIFIX-51a Slowest tab at ' + size.width + ' px: Inspect window and Nearby activity are real quiet buttons of one size with a visible edge, named by their window or request', async ({ page }) => {
    await page.setViewportSize(size);
    await openSlowestLists(page);
    const wins = await buttonFacts(page, WIN_BUTTONS), near = await buttonFacts(page, SLOW_BUTTONS);
    expect(wins.length, 'a button per window row').toBeGreaterThan(2);
    expect(near.length, 'a button per slowest request').toBeGreaterThan(2);
    for (const [list, facts, pattern, words] of [['window', wins, WIN_NAME, 'Inspect window'], ['request', near, NEAR_NAME, 'Nearby activity']] as const) {
      expect(new Set(facts.map((f) => f.w + 'x' + f.h)).size, list + ' buttons: one size in the column').toBe(1);
      for (const f of facts) {
        expect(f.tag, 'a real button').toBe('BUTTON button');
        expect(f.text).toBe(words);
        expect(f.lines, 'one line').toBe(1);
        expect(f.nowrap).toBe('nowrap');
        expect(f.edge, 'a visible edge, not faint text').not.toMatch(/rgba\(0, 0, 0, 0\)|none|0px/);
        expect(f.name, 'the name').toMatch(pattern);
        expect(f.name.startsWith(f.text), 'the visible words come first in the name').toBe(true);
      }
      const names = facts.map((f) => f.name);
      expect(new Set(names).size, 'every ' + list + ' button has its own name: ' + names.join(' | ')).toBe(names.length);
    }
    // the name says which row it belongs to: the API (the row heading) and the start time (the Started cell, to the second) of its own row
    for (const f of near) {
      expect(f.api, 'the row shows its API').toMatch(/^[\w.]+\.[\w]+$/);
      expect(f.clock, 'the row shows its start time').toMatch(/^\d\d:\d\d:\d\d$/);
      expect(f.name.startsWith('Nearby activity for ' + f.api + ' at ' + f.clock + '.'), 'the name holds the API and start time of its own row: ' + f.name + ' / ' + f.api + ' ' + f.clock).toBe(true);
    }
    const windowRows = await page.locator('.traffic-windows tbody tr').evaluateAll((trs) => trs.map((tr) => ({ heading: (tr.querySelector('th')?.textContent ?? '').trim(), name: tr.querySelector('button')?.getAttribute('aria-label') ?? '' })));
    for (const row of windowRows) {
      const [from, to] = row.heading.replace(' (partial)', '').split('–');
      expect(row.name.includes(from.trim()), 'the name holds the start the row shows: ' + row.heading + ' / ' + row.name).toBe(true);
      expect(row.name.endsWith(to.trim().replace(/^\d{1,2} \w{3} /, '') + ' UTC' + (row.heading.includes('(partial)') ? ' (partial)' : '')), 'the name holds the end the row shows').toBe(true);
    }
    // the accessibility tree (what a screen reader gets)
    const snapshot = await page.locator('.traffic-slowness').first().ariaSnapshot();
    expect(snapshot).toMatch(/- button "Inspect window for \d{1,2} \w{3} \d\d:\d\d:\d\d to [^"]+ UTC"/);
    expect(snapshot).toMatch(/- button "Nearby activity for Payroll\.Run at \d\d:\d\d:\d\d\.\d{3}, Demo (West|East) \/ \S+, record \S+"/);
    await expectScallHitArea(page, WIN_BUTTONS, 'Inspect window', 24, 'tr');
    await expectScallHitArea(page, SLOW_BUTTONS, 'Nearby activity', 24, 'tr');
    // (both tables are wider than their box at 1440 px and below and scroll inside it, as before: the boxes are measured unchanged in the pull request)
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), 'sideways overflow of the page').toBeLessThanOrEqual(0);
  });
}

test('UIFIX-51b Slowest tab: Tab reaches Inspect window, the focus ring shows, Enter and Space each pick the window once and the list is read for it', async ({ page }) => {
  await openSlowestLists(page);
  await countClicks(page, WIN_BUTTONS);
  const first = page.locator(WIN_BUTTONS).nth(2);
  await first.focus();
  await page.keyboard.press('Shift+Tab'); // arrive by keyboard, so the ring is the keyboard ring
  await page.keyboard.press('Tab');
  const nameOfFocus = () => page.evaluate(() => document.activeElement?.getAttribute('aria-label') ?? '');
  const name = await nameOfFocus();
  expect(name).toMatch(WIN_NAME);
  expect(name, 'Tab reached the button it left').toBe((await first.getAttribute('aria-label'))!);
  const ring = await focusRing(page);
  expect(ring.style).toBe('solid');
  expect(ring.width).toBe(2);
  expect(ring.color, 'the ring is painted').not.toMatch(/rgba\(0, 0, 0, 0\)|transparent/);
  const heading = page.locator('.traffic-slowness .section-heading p', { hasText: /Selected window|Full investigation/ });
  await expect(heading).toContainText('Full investigation');
  await page.keyboard.press('Enter');
  expect(await clicks(page), 'one click for one Enter').toBe(1);
  await expect(heading, 'the window is picked').toContainText('Selected window');
  await page.keyboard.press('Space');
  expect(await clicks(page), 'one more click for one Space').toBe(2);
  expect(await nameOfFocus(), 'the press kept the focus on the button').toBe(name);
  await expect(heading).toContainText('Selected window');
});

for (const key of ['Enter', 'Space'] as const) {
  test('UIFIX-51c Slowest tab: ' + key + ' on Nearby activity of a slowest request opens its surrounding activity once, with the focus ring', async ({ page }) => {
    await openSlowestLists(page);
    await countClicks(page, SLOW_BUTTONS);
    const button = page.locator(SLOW_BUTTONS).nth(1);
    await button.focus();
    await page.keyboard.press('Shift+Tab');
    await page.keyboard.press('Tab');
    const name = (await button.getAttribute('aria-label'))!;
    expect(name).toMatch(NEAR_NAME);
    expect(await page.evaluate(() => document.activeElement?.getAttribute('aria-label') ?? ''), 'Tab reached it').toBe(name);
    const ring = await focusRing(page);
    expect(ring.style).toBe('solid');
    expect(ring.width).toBe(2);
    expect(ring.color, 'the ring is painted').not.toMatch(/rgba\(0, 0, 0, 0\)|transparent/);
    await expect(page.locator('#nearby-activity')).toHaveCount(0);
    await page.keyboard.press(key);
    expect(await clicks(page), 'one click for one press').toBe(1);
    await expect(page.locator('#nearby-activity'), 'Nearby activity opened').toBeAttached();
  });
}

// UIFIX-52 (BL-537): two pre-existing audit warnings of the Slowest tab of All operations, found by the audit screen x-all-slowest-loaded. On develop the small line under the
// API name of each slowest request ("Demo East / demo.audit · synchronous · success") was 10.83 px (the browser's "smaller" of a 13 px heading cell) and the words of the two
// headings started 2 px left of the controls and notes under them (the heading box has 12 px of padding, everything else 14 px). The first column of the slowest list also started
// its words 6 px left of them on a phone (8 px of cell padding there). Only the words move: the heading boxes keep their left and width.
const SLOW_SMALL = '.traffic-slowness .slow-result tbody th small';

const wordsLeft = (page: Page, selector: string) => page.locator(selector).first().evaluate((e) => {
  const range = document.createRange();
  range.selectNodeContents(e);
  return Math.round(range.getBoundingClientRect().left * 100) / 100;
});

for (const size of OP_SIZES) {
  test('UIFIX-52a Slowest tab at ' + size.width + ' px: the line under each API name is at least 11 px', async ({ page }) => {
    await page.setViewportSize(size);
    await openSlowestLists(page);
    const sizes = await page.locator(SLOW_SMALL).evaluateAll((els) => els.map((e) => ({ px: parseFloat(getComputedStyle(e).fontSize), text: (e.textContent ?? '').trim() })));
    expect(sizes.length, 'one line per slowest request').toBeGreaterThan(2);
    for (const f of sizes) {
      expect(f.text, 'the line still names source, kind and outcome').toMatch(/^\S.* · \S+ · \S+$/);
      expect(f.px, 'font size of "' + f.text + '"').toBeGreaterThanOrEqual(11);
    }
    // it stays the quieter line: smaller than the API name above it
    const [small, name] = await page.locator('.traffic-slowness .slow-result tbody th').first().evaluate((th) => [parseFloat(getComputedStyle(th.querySelector('small')!).fontSize), parseFloat(getComputedStyle(th).fontSize)]);
    expect(small, 'the line under the name is smaller than the name').toBeLessThan(name);
  });

  test('UIFIX-52b Slowest tab at ' + size.width + ' px: the words of both headings start where the controls and notes start; the heading boxes and the list do not move sideways', async ({ page }) => {
    await page.setViewportSize(size);
    await openSlowestLists(page);
    const notes = await wordsLeft(page, '.traffic-slowness > .activity-explanation > p');
    const controls = await wordsLeft(page, '.traffic-slowness .volume-controls > label');
    expect(notes, 'the note and the controls start on one line').toBe(controls);
    const headings = page.locator('.traffic-slowness > .section-heading');
    expect(await headings.count()).toBe(2);
    for (let i = 0; i < 2; i++) {
      const words = await headings.nth(i).evaluate((h) => [...h.querySelectorAll('h3, p')].map((e) => Math.round(e.getBoundingClientRect().left * 100) / 100));
      for (const left of words) expect(left, 'heading ' + i + ' words start at ' + notes).toBe(notes);
    }
    // the boxes: each heading is still as wide as the section (the fix pads the words, it does not narrow or shift the box)
    const [section, boxes] = await page.locator('.traffic-slowness').first().evaluate((sec) => [sec.getBoundingClientRect().left + ',' + sec.getBoundingClientRect().width, [...sec.querySelectorAll(':scope > .section-heading')].map((h) => h.getBoundingClientRect().left + ',' + h.getBoundingClientRect().width)] as const);
    for (const b of boxes) expect(b, 'the heading box keeps the left and width of the section').toBe(section);
    // the right end of a heading with a button is unchanged: the button still ends 12 px inside the box
    const [boxRight, buttonRight] = await page.locator('.traffic-slowness > .section-heading').nth(1).evaluate((h) => [h.getBoundingClientRect().right, h.querySelector('button')!.getBoundingClientRect().right]);
    if (size.width > 600) expect(Math.round(boxRight - buttonRight), 'the button keeps its 12 px of room on the right (on a phone the button wraps under the words)').toBe(12);
    else expect(buttonRight, 'the wrapped button stays inside the box').toBeLessThanOrEqual(boxRight);
    // the slowest list: its first column's words start with the summary line above it (on a phone they started 6 px left of it)
    const summary = await wordsLeft(page, '.traffic-slowness .slow-result .volume-summary');
    const firstColumn = await wordsLeft(page, '.traffic-slowness .slow-result tbody tr:first-child > :first-child');
    expect(firstColumn, 'the first column starts under the summary line').toBe(summary);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), 'sideways overflow of the page').toBeLessThanOrEqual(0);
  });
}

// UIFIX-53 (BL-538): on develop the first column of `.volume-table` (`tbody th`, overflow-wrap: anywhere) got the table's smallest width, about 71 px at 1440 and 1100 px, so
// "Payroll.Run" broke after a few letters, every row of the slowest list was 223 px tall (50 rows = 11,220 px) and the time-window column broke the same way at 1100 px. Now the column has a
// minimum width (11rem in the slowest list, 9rem in the other `.volume-table` lists) and the Started and API / audit time cells may wrap at their spaces.
for (const size of OP_SIZES) {
  test('UIFIX-53a Slowest tab at ' + size.width + ' px: the API / source column is wide enough to read, rows are short, the name is not broken', async ({ page }) => {
    await page.setViewportSize(size);
    await openSlowestLists(page);
    await layoutSettled(page);
    const facts = await page.locator('.traffic-slowness .slow-result tbody tr').evaluateAll((rows) => rows.map((tr) => {
      const th = tr.querySelector('th')!;
      const name = th.firstChild as Text;
      const range = document.createRange();
      range.selectNodeContents(name);
      return { w: th.getBoundingClientRect().width, h: tr.getBoundingClientRect().height, nameLines: new Set([...range.getClientRects()].map((q) => Math.round(q.top))).size, name: (name.textContent ?? '').trim() };
    }));
    expect(facts.length, 'one row per slowest request').toBeGreaterThan(2);
    for (const f of facts) {
      expect(f.w, 'width of the API / source cell of ' + f.name).toBeGreaterThanOrEqual(150);
      expect(f.nameLines, 'the API name ' + f.name + ' is on one line').toBe(1);
      expect(f.h, 'row height of ' + f.name).toBeLessThanOrEqual(130);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), 'sideways overflow of the page').toBeLessThanOrEqual(0);
    // On a wide window the list fits its panel, so the last column is whole; on narrower ones the table's own wrapper scrolls, as it did before.
    const [need, have, buttonRight, wrapRight] = await page.locator('.traffic-slowness .slow-result .volume-table-wrap').evaluate((wrap) => [wrap.scrollWidth, wrap.clientWidth, wrap.querySelector('tbody td button')!.getBoundingClientRect().right, wrap.getBoundingClientRect().right]);
    if (size.width >= 1280) {
      expect(need, 'the list fits its panel').toBeLessThanOrEqual(have);
      expect(buttonRight, '"Nearby activity" is whole').toBeLessThanOrEqual(wrapRight);
    }
  });

  test('UIFIX-53b Slowest tab at ' + size.width + ' px: the time window column of the exact windows table keeps a readable width and the same row height or less', async ({ page }) => {
    await page.setViewportSize(size);
    await openSlowestLists(page);
    await layoutSettled(page);
    const facts = await page.locator('.traffic-slowness .traffic-windows tbody tr').evaluateAll((rows) => rows.map((tr) => ({ w: tr.querySelector('th')!.getBoundingClientRect().width, h: tr.getBoundingClientRect().height })));
    expect(facts.length).toBeGreaterThan(2);
    for (const f of facts) {
      expect(f.w, 'width of the time window cell').toBeGreaterThanOrEqual(120);
      expect(f.h, 'row height of the time window').toBeLessThanOrEqual(100);
    }
  });
}
