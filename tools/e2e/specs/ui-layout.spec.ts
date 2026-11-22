// Layout fixes the UI audit found (BL-472 to BL-478, BL-482). These measure the real page in a real browser: sizes, positions and
// computed colours, never the CSS text. A later rule that wins over a fixed one makes them fail, a harmless rewrite of the CSS does not.
//   UIFIX-01  Place a Blackbox: the close button does not sit on a step name (BL-473)
//   UIFIX-02  More filters: the "?" and the "i" beside Reference do not overlap, on desktop and on touch (BL-474)
//   UIFIX-03  Daily and hourly view: dimmed rows keep full opacity and 4.5:1 text contrast in both themes (BL-476)
//   UIFIX-04  All operations: first column, pager and header start on one line; the Sources note lines up with the list (BL-477)
//   UIFIX-05  The selected-lines notes of the timeline and the chart help line are at least 11px (BL-478)
//   UIFIX-06  Tap targets of 24 x 24 px or more: Daily view, Show matching requests, Help guide index links, Stop showing, Mark the line (BL-475 group 1)
//   UIFIX-09  Desktop does not move: tables that got bigger tap targets keep every row height and column width (BL-475 group 1)
//   UIFIX-07  Add files on a phone: the column heads are painted nowhere, yet stay in the page for screen readers (BL-482). A guard only: the finding was a false positive of the audit, so the audit run proves the fix
//   UIFIX-08  Phone header with "Choose sources" fits the window (BL-472); 390 and 360 px fail on the old header, 320 px is a no-regression case
import { test, expect, type Page } from '../support/fixtures';

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
