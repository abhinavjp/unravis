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
//   UIFIX-14  Tap targets of 24 x 24 px or more and desktop does not move: the "Change" link of the Review step (14a), the "Open the release page" link of the update bar (14b), the unfinished-request counts of Where is it? (14c), the Counters / Programs tabs of the Blackbox charts on a phone (14d), desktop side box (14e), and the keyboard focus rings of the first three, which stay the old rings (14f) (BL-475 group 4)
//   UIFIX-10  Slow call screen: the time zone label, the peak and zero labels and the "drag" hint are at least 11px, the lanes and the minimap do not move, nothing collides (BL-483)
import { test, expect, type Page } from '../support/fixtures';
import { openCase } from '../support/ui';

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
// squares included, which are grey on develop because `.hm-swatch` comes after `.level-N`) fails here.
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
  .hm-swatch { display: inline-block; width: 16px; height: 12px; border: 1px solid var(--line-soft); border-radius: 1px; background: var(--surface-2); }
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
    // and the legend squares, whatever colour develop gives them, stay the colour of the "few requests" square (grey, from `.hm-swatch`)
    const colours = await page.locator('.hm-legend .hm-swatch').evaluateAll((els) => els.map((e) => getComputedStyle(e).backgroundColor));
    expect(colours.slice(0, 4)).toEqual([colours[4], colours[4], colours[4], colours[4]]);
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
    const snap = () => [...container.querySelectorAll('*')].filter((e) => !e.closest('svg') || e.tagName.toLowerCase() === 'svg').map((e) => {
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

async function openChartsDialog(page: Page): Promise<void> {
  await openBlackbox(page);
  await page.getByRole('button', { name: /^Open status: web-a/ }).first().click();
  await page.getByRole('button', { name: 'Open charts' }).first().click();
  await expect(page.locator('dialog.bbc[open] .bbc-tabs button').first()).toBeVisible();
}

test('UIFIX-14d Blackbox charts on a phone: the Counters and Programs tabs keep their height (they were squeezed to 10 px)', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 600 }); // short, so the side box (40vh) scrolls: that is where the tabs were squeezed
  await openChartsDialog(page);
  const sizes = await page.locator('dialog.bbc[open] .bbc-tabs button').evaluateAll((els) => els.map((e) => { const r = e.getBoundingClientRect(); return { w: r.width, h: r.height }; }));
  expect(sizes.length).toBe(2);
  for (const z of sizes) { expect(z.w).toBeGreaterThanOrEqual(24); expect(z.h).toBeGreaterThanOrEqual(24); }
});

test('UIFIX-14e Blackbox charts on desktop: the side box does not move (the tab rule is for narrow windows only)', async ({ page }) => {
  await openChartsDialog(page);
  await expectNothingMoved(page, 'dialog.bbc[open]', [['.bbc-tabs', { 'flex-shrink': '1' }]], 'Charts side box');
  const h = await page.locator('dialog.bbc[open] .bbc-tabs').evaluate((e) => e.getBoundingClientRect().height);
  expect(h).toBeGreaterThanOrEqual(24);
});

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
      // Byte for byte, except the rounded ring of the Review link: a positioned control rasterises the four arcs of its ring with a 1/255 different edge
      // (4 pixels, one colour level), so there a handful of pixels may differ by up to 2 levels. A moved or resized ring differs by far more.
      const tolerance = name === 'Review Change link' ? { pixels: 8, levels: 2 } : { pixels: 0, levels: 0 };
      for (const [i, what] of ['at rest', 'focused'].entries()) {
        const d = await pictureDifference(page, now[i], old[i]);
        expect(d.pixels, what + ': pixels that differ from the old rules (largest step ' + d.levels + ')').toBeLessThanOrEqual(tolerance.pixels);
        expect(d.levels, what + ': the largest colour step against the old rules').toBeLessThanOrEqual(tolerance.levels);
      }
      expect(now[1].equals(now[0]), 'focused: nothing is drawn, so the ring is missing').toBe(false);
    });
  }
}
